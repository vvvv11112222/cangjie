from dataclasses import dataclass, field
import numpy as np
from scipy.optimize import linear_sum_assignment

LABELS = {
    "hand_raise": "举手候选", "leave_seat": "离座候选", "head_down": "低头线索（实验）",
    "possible_phone": "疑似持手机", "entry": "进入线索", "exit": "离开线索",
    "late_entry": "迟到进入线索", "early_exit": "早退离开线索",
}


def inside(point, rect, width, height):
    if rect is None:
        return True
    return rect.x1 <= point[0] / width <= rect.x2 and rect.y1 <= point[1] / height <= rect.y2


def midpoint(box):
    return ((box[0] + box[2]) / 2, (box[1] + box[3]) / 2)


def iou(a, b):
    left, top = max(a[0], b[0]), max(a[1], b[1])
    right, bottom = min(a[2], b[2]), min(a[3], b[3])
    intersection = max(0, right - left) * max(0, bottom - top)
    union = (a[2] - a[0]) * (a[3] - a[1]) + (b[2] - b[0]) * (b[3] - b[1]) - intersection
    return intersection / union if union else 0


@dataclass
class Track:
    id: str
    box: list
    last_ms: int
    first_ms: int
    hits: int = 1
    seat: int | None = None
    last_anchor: tuple | None = None
    head_reference: list = field(default_factory=list)
    door_side: int | None = None
    door_last_ms: int | None = None
    door_last_event_ms: int = -10000
    velocity: tuple = (0.0,0.0)
    appearance: object = None


class Tracker:
    """Conservative short-gap IoU tracker; IDs are not student identities."""
    def __init__(self, max_gap_ms=1200, appearance_matching=False):
        self.tracks = {}
        self.counter = 0
        self.max_gap_ms = max_gap_ms
        self.appearance_matching=appearance_matching

    def update(self, detections, timestamp):
        self.tracks = {k: v for k, v in self.tracks.items() if timestamp - v.last_ms <= self.max_gap_ms}
        live = list(self.tracks.values())
        pairs = {}
        if live and detections:
            def match_cost(t,d):
                if not self.appearance_matching:return 1-iou(t.box,d['bbox'])
                dt=(timestamp-t.last_ms)/1000
                delta=np.array(t.velocity)*min(dt,1)
                box=np.array(t.box)+np.tile(delta,2)
                size=np.maximum(np.array([box[2]-box[0],box[3]-box[1]]),8)
                distance=np.linalg.norm((np.array(midpoint(d['bbox']))-midpoint(box))/size)
                if distance>1.8:return 100.0
                overlap=iou(box,d['bbox'])
                appearance=0
                if t.appearance is not None and d.get('appearance') is not None:
                    appearance=min(1,float(np.linalg.norm(t.appearance-d['appearance']))/1.2)
                return .45*(1-overlap)+.4*min(1,distance/1.8)+.15*appearance
            cost=np.array([[match_cost(t,d) for d in detections] for t in live])
            rows, columns = linear_sum_assignment(cost)
            for r, c in zip(rows, columns):
                if cost[r, c] <= (0.67 if self.appearance_matching else 0.8):
                    pairs[int(c)] = live[int(r)]
        output = []
        for index, detection in enumerate(detections):
            track = pairs.get(index)
            if track is None:
                self.counter += 1
                track = Track(f"T{self.counter:04d}", detection["bbox"], timestamp, timestamp)
                self.tracks[track.id] = track
            else:
                dt=max(.001,(timestamp-track.last_ms)/1000)
                movement=(np.array(midpoint(detection['bbox']))-midpoint(track.box))/dt
                track.velocity=tuple(.5*np.array(track.velocity)+.5*movement)
                track.box, track.last_ms, track.hits = detection["bbox"], timestamp, track.hits + 1
            if detection.get('appearance') is not None:
                track.appearance=detection['appearance'] if track.appearance is None else .8*track.appearance+.2*detection['appearance']
            output.append(track)
        return output


def pose_belongs_to_head(points, scores, head_box, competing_heads=()):
    x1,y1,x2,y2 = head_box
    w,h = x2-x1,y2-y1
    supported=bool(scores[0]>=.3 and x1-.25*w <= points[0,0] <= x2+.25*w
                and y1-.25*h <= points[0,1] <= y2+.25*h)
    if not supported:return False
    x,y=points[0]
    if not (x1<=x<=x2 and y1<=y<=y2):
        # Margin may cover localisation jitter, but cannot borrow another face.
        if any(a<=x<=c and b<=y<=d for a,b,c,d in competing_heads):return False
    return True


def raised_hand(points, scores, head_box=None, image_width=None):
    if head_box is not None:
        h = head_box[3]-head_box[1]
        # Reject a crop's pose if it belongs to a neighbouring student's face.
        if not pose_belongs_to_head(points,scores,head_box):
            return None,None
    supported = False
    for shoulder, elbow, wrist in ((5, 7, 9), (6, 8, 10)):
        score = float(min(scores[shoulder], scores[elbow], scores[wrist]))
        required=(.45 if h>=35 else .35) if head_box is not None else .45
        if score < required:
            continue
        upper_arm = np.linalg.norm(points[elbow] - points[shoulder])
        if upper_arm < (max(3,.2*h) if head_box is not None else 12):
            continue
        supported = True
        # Image y increases downwards. A raised wrist is above the shoulder.
        if head_box is not None:
            forearm=points[elbow]-points[wrist]
            forearm_length=np.linalg.norm(forearm)
            upright=forearm[1]>.7*forearm_length
            # An upright forearm held beside the ear must not be discarded as
            # chin support solely because the wrist is close to the nose.
            beside_face=(h>=30 and score>=.55 and forearm_length>.75*h and forearm[1]>.9*forearm_length
                         and points[elbow,1]>points[shoulder,1]+.4*h
                         and abs(points[elbow,0]-points[shoulder,0])>.35*h
                         and points[0,1]+.1*h<=points[wrist,1]<=points[0,1]+.25*h
                         and abs(points[wrist,0]-points[0,0])>.4*h)
            if np.linalg.norm(points[wrist]-points[0]) < .6*h and not beside_face:
                continue
            # Chin support: close to the face, but wrist still below shoulder.
            if np.linalg.norm(points[wrist]-points[0]) < .75*h and points[wrist,1]>points[shoulder,1]:
                continue
            high_wrist=points[wrist,1]<points[shoulder,1]-.5*h
            if high_wrist or (upright and points[wrist,1] <= points[shoulder,1]+.15*upper_arm):
                return True,score
        elif points[wrist, 1] < points[shoulder, 1] - 0.2 * upper_arm:
            return True, score
    # A clipped head often shifts the crop and hides the outside arm. A negative
    # result here cannot rule out an out-of-frame raised wrist.
    if head_box is not None and image_width is not None and (head_box[0]<=1 or head_box[2]>=image_width-1):
        return None,None
    return (False, 0.5) if supported else (None, None)


def head_signal(track, points, scores, timestamp):
    """Experimental relative 2D head geometry, never attention/sleep detection.

Only after a user-enabled initial neutral reference; weak facial points are unknown.
"""
    facial = [0, 1, 2, 3, 4]
    if min(scores[facial]) < 0.5 or min(scores[[5, 6]]) < 0.5:
        return None, None
    span = float(np.linalg.norm(points[3] - points[4]))
    if span < 18:
        return None, None
    feature = float((points[0, 1] - (points[3, 1] + points[4, 1]) / 2) / span)
    if timestamp - track.first_ms <= 3000:
        track.head_reference.append(feature)
        return None, None
    if len(track.head_reference) < 3:
        return None, None
    delta = feature - float(np.median(track.head_reference))
    return delta > 0.25, float(min(scores[facial]))


def head_down_from_angles(angles,threshold=25):
    if not angles or abs(angles['yaw'])>60 or abs(angles['roll'])>45 or abs(angles['pitch'])>80:
        return None
    return bool(angles['pitch'] <= -threshold)


def head_is_arm_artifact(box, confidence, hits, pose, neighbours):
    """Only reject a new weak, unowned head on an independently owned raised arm.

    Occluded heads alone are never grounds for rejection. neighbours holds
    (head_box, points, scores) for poses which passed the face ownership gate.
    """
    if pose is not None or confidence >= .5 or hits > 1:
        return False
    center=np.array(midpoint(box));area=(box[2]-box[0])*(box[3]-box[1])
    for head,points,scores in neighbours:
        h=head[3]-head[1]
        if area > .7*(head[2]-head[0])*h or raised_hand(points,scores,head)[0] is not True:
            continue
        for shoulder,elbow,wrist in ((5,7,9),(6,8,10)):
            if min(scores[[shoulder,elbow,wrist]]) < .42:
                continue
            a,b=points[elbow],points[wrist];delta=b-a
            if delta[1]>=0:continue
            ratio=np.clip(np.dot(center-a,delta)/max(np.dot(delta,delta),1),0,1)
            if np.linalg.norm(center-(a+ratio*delta)) < .3*h:
                return True
    return False


def standing_pose(points,scores):
    supported=False
    for hip,knee,ankle in ((11,13,15),(12,14,16)):
        if min(scores[[hip,knee,ankle]])<.5:continue
        upper=points[knee]-points[hip];lower=points[ankle]-points[knee]
        if min(np.linalg.norm(upper),np.linalg.norm(lower))<10:continue
        supported=True
        if upper[1]>.8*np.linalg.norm(upper) and lower[1]>.8*np.linalg.norm(lower):return True
    return False if supported else None


class EventAccumulator:
    def __init__(self, sample_interval_ms, minimums):
        self.active = {}
        self.events = []
        self.interval = sample_interval_ms
        self.minimums = minimums

    def update(self, kind, track_id, timestamp, value, confidence, evidence):
        key = (kind, track_id)
        if value is not True:
            self.finish(key)
            return
        item = self.active.get(key)
        if item and timestamp - item["last_ms"] > self.interval * 1.6:
            self.finish(key)
            item = None
        if item is None:
            item = {"event_type": kind, "track_id": track_id, "start_ms": timestamp,
                    "last_ms": timestamp, "confidence": confidence, "evidence": [evidence]}
            self.active[key] = item
        else:
            item["last_ms"] = timestamp
            item["confidence"] = min(item["confidence"], confidence)
            if len(item["evidence"]) == 1:
                item["evidence"].append(evidence)
            elif len(item["evidence"]) == 2:
                item["evidence"].append(evidence)
            else:
                item["evidence"][-1] = evidence

    def close_missing(self, present_ids):
        for key in list(self.active):
            if key[1] not in present_ids:
                self.finish(key)

    def finish(self, key):
        item = self.active.pop(key, None)
        if item is None:
            return
        last = item.pop("last_ms")
        end = last + self.interval
        # Require observed persistence; one isolated sample proves no duration.
        if last - item["start_ms"] >= self.minimums[item["event_type"]]:
            item.update(end_ms=end, label=LABELS[item["event_type"]], review_status="unreviewed")
            item["confidence"] = round(float(item["confidence"]), 4)
            self.events.append(item)

    def finish_all(self, end_ms):
        for key in list(self.active):
            self.finish(key)
        for event in self.events:
            event["end_ms"] = min(event["end_ms"], end_ms)
        self.events = [e for e in self.events if e["end_ms"] > e["start_ms"]]


def door_crossing(track, anchor, timestamp, config, width, height):
    door = config.door_roi
    if door is None or not inside(anchor, door, width, height):
        track.door_side = None
        track.door_last_ms = None
        return None
    line_x = (door.x1 + door.x2) / 2 * width
    margin = max(3, (door.x2 - door.x1) * width * 0.05)
    if abs(anchor[0] - line_x) < margin:
        return None
    side = 1 if anchor[0] > line_x else -1
    previous = track.door_side
    last = track.door_last_ms
    track.door_side, track.door_last_ms = side, timestamp
    if previous is None or previous == side or track.hits < 3 or last is None:
        return None
    if timestamp - last > 1500 or timestamp - track.door_last_event_ms < 3000:
        return None
    track.door_last_event_ms = timestamp
    entered = (side == 1) == (config.inside_direction == "right")
    if entered:
        return "late_entry" if config.schedule_enabled and timestamp > (config.class_start_s + config.grace_s) * 1000 else "entry"
    if config.schedule_enabled and config.class_end_s is not None and timestamp < (config.class_end_s - config.grace_s) * 1000:
        return "early_exit"
    return "exit"
