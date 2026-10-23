"""Transient live frames; the pinned offline models and unknown-state rules.

No media, evidence or behavioral history is persisted by live preview. Track IDs
are scoped to this process and capture; final reports use the saved recording.
"""
import base64
import time
import cv2
import numpy as np
from .behavior import Tracker, raised_hand, pose_belongs_to_head, head_down_from_angles, head_is_arm_artifact
from .camera import CameraMotion, transform_box
from .models import get_models


class LiveDetector:
    def __init__(self):
        cv2.setNumThreads(4)
        cv2.ocl.setUseOpenCL(False)
        self.models = get_models()
        self.capture = None
        self.shape = None
        self.tracker = Tracker(max_gap_ms=10000, appearance_matching=True)
        self.camera = CameraMotion()

    def infer(self, work):
        data = base64.b64decode(work['image_base64'], validate=True)
        image = cv2.imdecode(np.frombuffer(data, np.uint8), cv2.IMREAD_COLOR)
        if image is None or image.shape[0]>1080 or image.shape[1]>1920:
            raise ValueError('Invalid live frame')
        height, width = image.shape[:2]
        if self.capture != work['capture_id'] or self.shape != image.shape:
            self.capture = work['capture_id']
            self.shape = image.shape
            self.tracker = Tracker(max_gap_ms=10000, appearance_matching=True)
            self.camera = CameraMotion()
        movement, cut = self.camera.update(image)
        if cut:
            self.tracker.tracks.clear()
        elif movement is not None:
            for track in self.tracker.tracks.values(): track.box = transform_box(track.box,movement)
        # Same head detector/crops as classroom offline analysis. No room/phone
        # detector: these do not contribute to the two requested live behaviors.
        people = sorted(self.models.detect_classroom_heads(image,.25),key=lambda d:d['confidence'],reverse=True)
        for person in people:
            x1,y1,x2,y2 = map(int,person['bbox'])
            crop = image[max(0,y1):min(height,y2),max(0,x1):min(width,x2)]
            if crop.size:
                hist=cv2.calcHist([cv2.cvtColor(crop,cv2.COLOR_BGR2HSV)],[0,1],None,[8,4],[0,180,0,256]).flatten()
                person['appearance']=hist/max(np.linalg.norm(hist),1e-6)
        tracks=self.tracker.update(people,int(time.monotonic()*1000))
        eligible=sorted([i for i,t in enumerate(tracks) if t.box[3]-t.box[1]>=6 and t.box[2]-t.box[0]>=5],
                        key=lambda i:(tracks[i].box[2]-tracks[i].box[0])*(tracks[i].box[3]-tracks[i].box[1]),reverse=True)[:80]
        poses=dict(zip(eligible,self.models.estimate_poses(image,[people[i]['pose_bbox'] for i in eligible],classroom=True)))
        competitors={i:[t.box for j,t in enumerate(tracks) if j!=i] for i in eligible}
        retry=[i for i in eligible if not pose_belongs_to_head(*poses[i],tracks[i].box,competitors[i])]
        boxes=[]
        for i in retry:
            x1,y1,x2,y2=tracks[i].box;w,h,cx=x2-x1,y2-y1,(x1+x2)/2
            boxes.append([cx-1.5*w,y1-.7*h,cx+1.5*w,y1+2.7*h])
        poses.update(zip(retry,self.models.estimate_poses(image,boxes,classroom=True)))
        poses={i:p for i,p in poses.items() if pose_belongs_to_head(*p,tracks[i].box,competitors[i])}
        neighbours=[(tracks[i].box,*pose) for i,pose in poses.items()]
        result=[]
        for i,track in enumerate(tracks):
            if head_is_arm_artifact(track.box,people[i]['confidence'],track.hits,poses.get(i),neighbours):
                self.tracker.tracks.pop(track.id,None)
                continue
            hand=head=None
            if i in poses:
                points,scores=poses[i]
                hand=raised_hand(points,scores,track.box,width)[0]
                if min(track.box[2]-track.box[0],track.box[3]-track.box[1])>=16 and scores[0]>=.4 and max(scores[1],scores[2])>=.35:
                    head=head_down_from_angles(self.models.estimate_head_angles(image,track.box),25)
            if len(result)>=80: break
            box=np.clip(np.asarray(track.box,dtype=float)/[width,height,width,height],0,1).tolist()
            if box[0]>=box[2] or box[1]>=box[3]: continue
            result.append(dict(track_id='L'+track.id.lstrip('T'),bbox=box,
                               hand_raise=None if hand is None else bool(hand),head_down=None if head is None else bool(head)))
        return dict(sequence=work['sequence'],width=width,height=height,people=result,error_code=None)
