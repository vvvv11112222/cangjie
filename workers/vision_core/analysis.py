from collections import Counter
from pathlib import Path
import json
import hashlib
import time
import os
import cv2
import numpy as np

from .behavior import Tracker, EventAccumulator, LABELS, inside, midpoint, raised_hand, head_down_from_angles, door_crossing, pose_belongs_to_head,standing_pose,head_is_arm_artifact
from .config import VERSION,Rect
from .media import sampled_frames, check_cancel
from .models import get_models
from .models import runtime_settings
from .models import runtime_status
from .pipeline import detection_pipeline
from .reporting import build_report
from .layout import SeatMap,normalized
from .camera import CameraMotion,transform_box


def save_jpeg(path, image):
    ok, encoded = cv2.imencode(".jpg", image, [cv2.IMWRITE_JPEG_QUALITY, 83])
    if not ok:
        raise ValueError("证据图片编码失败")
    path.write_bytes(encoded.tobytes())


def analyze(job_id, directory, metadata, config, cancel, progress, preview=None):
    cv2.ocl.setUseOpenCL(False)  # OpenCV preference is local to this worker thread.
    cv2.setNumThreads(int(os.environ.get('VISION_CV_THREADS',runtime_settings.get('opencv_threads',32))))
    started = time.monotonic()
    check_cancel(cancel)
    classroom = config.detection_mode == 'classroom'
    progress(stage="loading_models", message="加载检测、姿态与头姿模型", progress=0)
    models = get_models()
    models.pose_metrics={}
    start_ms = round(config.start_s * 1000)
    duration_ms = metadata["duration_ms"]
    requested_end = min(duration_ms, round((config.start_s + config.duration_s) * 1000)) if config.duration_s else duration_ms
    if start_ms >= requested_end:
        raise ValueError("分析开始位置超出录像范围")
    interval = max(1, round(1000 / config.sampling_fps))
    tracker = Tracker(max_gap_ms=max(3000, interval * 3),appearance_matching=True)
    aggregator = EventAccumulator(interval, {"hand_raise": round(config.hand_min_s * 1000),
        "leave_seat": round(config.leave_min_s * 1000), "head_down": round(config.head_min_s * 1000),
        "possible_phone": round(config.phone_min_s * 1000)})
    stats = {"hand_evaluable": 0, "seat_evaluable": 0, "head_evaluable": 0, "person_instances": 0,
             "pose_instances": 0, "small_person_instances": 0, "dark_frames": 0, "blur_hints": 0,
             "repeated_frame_hints": 0, "valid_frames": 0, "phone_evaluable": 0, "pose_identity_rejected": 0,
             "pose_crop_retries": 0, "pose_crop_recovered": 0, "head_arm_artifacts_rejected":0}
    points, snapshots, audit, persons = [], [], {}, {}
    seat_map=None;room_objects=[];pose_seconds=head_seconds=detect_seconds=preview_seconds=0
    manual_door=bool(config.door_roi)
    camera=CameraMotion();camera_updates=scene_cuts=0
    evidence_dir = directory / "evidence"
    evidence_dir.mkdir(exist_ok=True)
    limitations = [
        "按时间采样推理，短暂行为可能落在采样间隔中；候选起止点存在采样误差。",
        "头部检测用于课堂可见人数，常规模式使用人体检测；遮挡仍会漏检，可见人数不能当作出勤人数。",
        "使用运动、空间距离与颜色特征维持匿名轨迹；长遮挡、切镜和相似目标交叉仍可能造成编号变化，不等于永久个人身份。",
        "所有行为和进出记录均为待复核线索，不直接认定迟到、早退、违规或学习态度。",
        "未完成独立人工标注集质量验收，模型分数不代表本项目准确率。",
    ]
    if config.student_roi is None:
        limitations.append("未限定学生区域：画面中的教师或其他人员也可能计入可见人数。")
    if classroom:
        limitations.append("课堂模式按可见头部计数。请配置排除区域去掉镜面、屏幕中的人像；未配置时反射人像可能重复计数。")
    if config.head_down_enabled:
        limitations.append("低头依据可见脸部的三维头姿俯仰角；严重侧脸、遮挡、过小脸部和异常旋转保留未知，不认定走神或睡觉。")
    previous_gray, last_snapshot_ms, frame_limit = None, -10000, False
    progress(stage="analyzing", message="逐时刻进行真实检测、姿态与候选事件分析", progress=0)
    frames=sampled_frames(directory / "playback.mp4", start_ms, requested_end, config.sampling_fps, cancel, audit)
    pipelined=classroom and models.classroom_detector.get_providers()[0]=='CPUExecutionProvider' and bool(int(os.environ.get('VISION_PREFETCH',runtime_settings.get('prefetch_detection',0))))
    detector=lambda image:models.detect_classroom(image,config.confidence) if classroom else models.detect(image,config.confidence)
    samples=detection_pipeline(frames,lambda image:models.detect_classroom_heads(image,config.confidence),lambda:check_cancel(cancel),lambda:audit.get('decoded_frames',1)-1) if pipelined else ((t,image,None,0,None) for t,image in frames)
    for timestamp, image, cached_heads, detection_work, sampled_index in samples:
        if len(points) >= config.max_samples:
            frame_limit = True
            break
        check_cancel(cancel)
        height, width = image.shape[:2]
        if seat_map is None:
            seat_map=SeatMap(config.seat_rois,width,height,config.auto_seats)
            if config.auto_door:
                room_objects=models.detect_room(image)
                doors=[d for d in room_objects if d['kind'] in ('door','doorway')]
                if config.door_roi is None and doors:
                    config=config.model_copy(update={'door_roi':normalized(max(doors,key=lambda d:d['confidence'])['bbox'],width,height)})
        gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
        small_gray = cv2.resize(gray, (160, 90))
        brightness = float(gray.mean())
        sharpness = float(cv2.Laplacian(small_gray, cv2.CV_64F).var())
        valid = not (brightness < 6 and float(gray.std()) < 4)
        stats["dark_frames"] += int(not valid)
        stats["valid_frames"] += int(valid)
        stats["blur_hints"] += int(sharpness < 12)
        if previous_gray is not None:
            stats["repeated_frame_hints"] += int(float(cv2.absdiff(small_gray, previous_gray).mean()) < 0.1)
        previous_gray = small_gray
        movement,cut=camera.update(image)
        if cut:
            scene_cuts+=1
            tracker.tracks.clear()
            # Coordinates are not carried across a different shot.
            seat_map=SeatMap([],width,height,config.auto_seats)
            limitations.append(f'{timestamp/1000:.2f} 秒疑似切镜，轨迹编号和自动占用位置重新建立；切镜前的区域不用于之后的离座判断。')
        elif movement is not None:
            camera_updates+=1
            for track in tracker.tracks.values():track.box=transform_box(track.box,movement)
            seat_map.compensate_camera(movement)
        inference_started=time.perf_counter()
        detections = models.detect_classroom(image,config.confidence,heads=cached_heads) if pipelined and valid else (detector(image) if valid else [])
        detect_seconds+=detection_work+time.perf_counter()-inference_started
        detections = [d for d in detections if not any(inside(midpoint(d['bbox']),r,width,height) for r in config.exclude_rois)]
        people = sorted([d for d in detections if d["class_id"] == 0], key=lambda d: d["confidence"], reverse=True)
        phones = [d for d in detections if d["class_id"] == 67]
        for person in people:
            x1,y1,x2,y2=map(int,person['bbox'])
            crop=image[max(0,y1):min(height,y2),max(0,x1):min(width,x2)]
            if crop.size:
                hist=cv2.calcHist([cv2.cvtColor(crop,cv2.COLOR_BGR2HSV)],[0,1],None,[8,4],[0,180,0,256]).flatten()
                person['appearance']=hist/max(np.linalg.norm(hist),1e-6)
        tracks = tracker.update(people, timestamp)
        student_indices = {i for i, t in enumerate(tracks) if inside(midpoint(t.box), config.student_roi, width, height)}
        stats["person_instances"] += len(student_indices)
        eligible = [i for i, t in enumerate(tracks) if (i in student_indices or t.seat is not None)
                    and t.box[3] - t.box[1] >= (6 if classroom else 80) and t.box[2] - t.box[0] >= (5 if classroom else 30)]
        stats["small_person_instances"] += sum(1 for i in student_indices if i not in eligible)
        stats["phone_evaluable"] += sum(1 for i in student_indices if tracks[i].box[3] - tracks[i].box[1] >= 80)
        eligible = sorted(eligible, key=lambda i: (tracks[i].box[2] - tracks[i].box[0]) * (tracks[i].box[3] - tracks[i].box[1]), reverse=True)[:config.max_pose_people]
        predicted = {};standing={};inference_started=time.perf_counter()
        initial=models.estimate_poses(image,[people[i].get('pose_bbox',tracks[i].box) for i in eligible],
                                      classroom=classroom,cancel=lambda:check_cancel(cancel))
        predicted.update(zip(eligible,initial))
        competitors={i:[t.box for j,t in enumerate(tracks) if j!=i] for i in eligible} if classroom else {}
        retries=[];retry_boxes=[]
        if classroom:
            for i in eligible:
                if not pose_belongs_to_head(*predicted[i],tracks[i].box,competitors[i]):
                    x1,y1,x2,y2=tracks[i].box;w,h,cx=x2-x1,y2-y1,(x1+x2)/2
                    retries.append(i);retry_boxes.append([cx-1.5*w,y1-.7*h,cx+1.5*w,y1+2.7*h])
            stats['pose_crop_retries']+=len(retries)
            recovered=models.estimate_poses(image,retry_boxes,classroom=True,cancel=lambda:check_cancel(cancel))
            for i,pose in zip(retries,recovered):
                predicted[i]=pose
                if pose_belongs_to_head(*pose,tracks[i].box,competitors[i]):stats['pose_crop_recovered']+=1
            for i in eligible:
                if not pose_belongs_to_head(*predicted[i],tracks[i].box,competitors[i]):
                    del predicted[i];stats['pose_identity_rejected']+=1
        body_indices=[];body_boxes=[]
        for i in eligible:
            check_cancel(cancel)
            body=people[i].get('body_bbox') if classroom else tracks[i].box
            if body is not None and (not classroom or tracks[i].box[3]-tracks[i].box[1]>=30):
                body_indices.append(i);body_boxes.append(body)
        whole_poses=models.estimate_poses(image,body_boxes,classroom=classroom,cancel=lambda:check_cancel(cancel))
        for i,whole_pose in zip(body_indices,whole_poses):
            if not classroom or pose_belongs_to_head(*whole_pose,tracks[i].box,competitors[i]):
                standing[i]=standing_pose(*whole_pose)
        stats["pose_instances"] += len(predicted)
        pose_seconds+=time.perf_counter()-inference_started
        rejected=set()
        if classroom:
            neighbours=[(tracks[i].box,*pose) for i,pose in predicted.items()]
            rejected={i for i,t in enumerate(tracks) if head_is_arm_artifact(t.box,people[i]['confidence'],t.hits,predicted.get(i),neighbours)}
            stats['head_arm_artifacts_rejected']+=len(rejected)
            stats['person_instances']-=len(rejected & student_indices)
            student_indices-=rejected
            for i in rejected:tracker.tracks.pop(tracks[i].id,None)
        # Occupied manual/automatic seats get first claim in this frame.
        for i in sorted(student_indices,key=lambda i:tracks[i].seat is None):
            anchor=midpoint(tracks[i].box)
            if i in predicted and min(predicted[i][1][[5,6]])>=.45:
                anchor=tuple((predicted[i][0][5]+predicted[i][0][6])/2)
            seat_map.observe(tracks[i],tracks[i].box,anchor,timestamp,standing.get(i))
        flags, instantaneous = [], []
        frame_people=[]
        annotated = image.copy()
        for index, track in enumerate(tracks):
            if index in rejected:continue
            values = {kind: (None, None) for kind in ("hand_raise", "leave_seat", "head_down", "possible_phone")}
            anchor = midpoint(track.box)
            pose = predicted.get(index)
            head_angles=None;head_box=track.box if classroom else None
            if pose is not None:
                keypoints, scores = pose
                if min(scores[[5, 6]]) >= 0.45:
                    anchor = tuple((keypoints[5] + keypoints[6]) / 2)
                    if track.seat is not None:
                        departure=seat_map.departure(track,track.box,anchor,standing.get(index))
                        values["leave_seat"] = (departure, float(min(scores[[5, 6]])) if departure is not None else None)
                        stats["seat_evaluable"] += int(departure is not None)
                if index in student_indices:
                    values["hand_raise"] = raised_hand(keypoints, scores, track.box if classroom else None,width if classroom else None)
                    stats["hand_evaluable"] += int(values["hand_raise"][0] is not None)
                    if config.head_down_enabled:
                        if head_box is None and min(scores[[0,3,4]])>=.35:
                            face=keypoints[[0,1,2,3,4]];x1,y1=face.min(axis=0);x2,y2=face.max(axis=0)
                            w=max(12,x2-x1);h=max(16,abs(anchor[1]-keypoints[0,1]))
                            head_box=[x1-.15*w,keypoints[0,1]-.6*h,x2+.15*w,keypoints[0,1]+.4*h]
                        if head_box is not None and min(head_box[2]-head_box[0],head_box[3]-head_box[1])>=16 and scores[0]>=.4 and max(scores[1],scores[2])>=.35:
                            head_start=time.perf_counter();head_angles=models.estimate_head_angles(image,head_box)
                            head_seconds+=time.perf_counter()-head_start
                            result=head_down_from_angles(head_angles,config.head_pitch_down_deg)
                            if result is not None:values['head_down']=(result,float(scores[0]))
                        stats["head_evaluable"] += int(values["head_down"][0] is not None)
                for a, b in ((5, 7), (7, 9), (6, 8), (8, 10)):
                    if values['hand_raise'][0] is True and min(scores[[a, b]]) > 0.45:
                        cv2.line(annotated, tuple(keypoints[a].astype(int)), tuple(keypoints[b].astype(int)), (70, 210, 255), 2)
            if index in student_indices:
                possible = []
                for phone in phones:
                    center = midpoint(phone["bbox"])
                    x1, y1, x2, y2 = people[index].get('pose_bbox',track.box)
                    if x1 <= center[0] <= x2 and y1 <= center[1] <= y2:
                        if pose is not None:
                            kp, sc = pose
                            wrists = [kp[w] for w in (9, 10) if sc[w] >= 0.45]
                            if not wrists or min(np.linalg.norm(w - center) for w in wrists) > 0.3 * (y2 - y1):
                                continue
                        possible.append(phone["confidence"])
                if possible or track.box[3]-track.box[1]>=80:
                    values["possible_phone"] = (bool(possible), max(possible) if possible else 0.5)
            for kind, (value, score) in values.items():
                flags.append((kind, track.id, value, score))
            crossing = door_crossing(track, anchor, timestamp, config, width, height)
            if crossing:
                instantaneous.append({"event_type": crossing, "label": LABELS[crossing], "track_id": track.id,
                    "start_ms": max(start_ms, timestamp - interval), "end_ms": min(requested_end, timestamp + interval),
                    "confidence": round(people[index]["confidence"], 4), "review_status": "unreviewed"})
            color = (65, 185, 100) if index in student_indices and values['hand_raise'][0] is False and (values['head_down'][0] is False or not config.head_down_enabled) else (170, 170, 170)
            if values['hand_raise'][0] is True: color = (40,40,240)
            elif values['head_down'][0] is True:color=(240,130,40)
            x1, y1, x2, y2 = map(int, track.box)
            cv2.rectangle(annotated, (x1, y1), (x2, y2), color, 2)
            label = track.id + (' UP' if values['hand_raise'][0] is True else '')+(' DOWN' if values['head_down'][0] is True else '')
            cv2.putText(annotated, label, (x1, max(10, y1 - 2)), cv2.FONT_HERSHEY_SIMPLEX, 0.3 if classroom else 0.5, color, 1)
            person_record=persons.setdefault(track.id,{'track_id':track.id,'first_ms':timestamp,'last_ms':timestamp,'observations':[]})
            person_record['last_ms']=timestamp
            observation={'timestamp_ms':timestamp,'frame_index':sampled_index if sampled_index is not None else audit.get('decoded_frames',1)-1,'bbox':list(map(float,track.box)),
                         'seat_id':seat_map.seats[track.seat]['seat_id'] if track.seat is not None else None,
                         'behaviors':{k:v[0] for k,v in values.items()},'behavior_scores':{k:v[1] for k,v in values.items()},
                         'detection_confidence':people[index]['confidence'],
                         'head_angles':head_angles,'pose_available':pose is not None,'standing':standing.get(index)}
            notes={}
            if values['hand_raise'][0] is None:
                notes['hand_raise']='手臂被遮挡、画面不清楚，或无法确定手臂属于谁' if pose is None else '手臂位置看不清，暂时不能判断是否举手'
                if classroom and (track.box[0]<=1 or track.box[2]>=width-1):notes['hand_raise']='目标贴近画面边缘，外侧手臂可能未入镜或定位不可靠'
            if config.head_down_enabled and values['head_down'][0] is None:
                notes['head_down']='脸转向侧面，暂时不能判断是否低头' if head_angles else '脸太小、被遮挡或看不清，暂时不能判断是否低头'
            observation['behavior_notes']=notes
            person_record['observations'].append(observation)
            frame_people.append({'track_id':track.id,**observation})
        for phone in phones:
            x1, y1, x2, y2 = map(int, phone["bbox"])
            cv2.rectangle(annotated, (x1, y1), (x2, y2), (210, 90, 240), 2)
        # Automatic seats remain in the report. Do not cover every student's
        # behavior box with seat rectangles in the teacher-facing evidence.
        for rect, color in [(config.student_roi, (100, 230, 130)), *[(r, (255, 170, 80)) for r in config.seat_rois], (config.door_roi, (200, 130, 255)), *[(r,(80,80,240)) for r in config.exclude_rois]]:
            if rect:
                cv2.rectangle(annotated, (int(rect.x1 * width), int(rect.y1 * height)), (int(rect.x2 * width), int(rect.y2 * height)), color, 2)
        new_candidate = any(v is True and (k, t) not in aggregator.active for k, t, v, _ in flags)
        periodic = timestamp - last_snapshot_ms >= 5000
        ongoing = any(v is True for _, _, v, _ in flags) and timestamp - last_snapshot_ms >= 2000
        evidence = {"timestamp_ms": timestamp, "original_file": None, "annotated_file": None}
        if len(snapshots) < 1000:
            original = f"frame-{timestamp}.jpg"
            markup = f"frame-{timestamp}-annotated.jpg"
            save_jpeg(evidence_dir / original, image)
            save_jpeg(evidence_dir / markup, annotated)
            evidence.update(original_file=original, annotated_file=markup)
            snapshots.append(evidence.copy())
            last_snapshot_ms = timestamp
        for person in frame_people:
            person['original_file']=evidence['original_file'];person['annotated_file']=evidence['annotated_file']
            persons[person['track_id']]['observations'][-1].update(original_file=evidence['original_file'],annotated_file=evidence['annotated_file'])
        for kind, track_id, value, score in flags:
            aggregator.update(kind, track_id, timestamp, value, score, evidence.copy())
        aggregator.close_missing({t.id for i,t in enumerate(tracks) if i not in rejected})
        for event in instantaneous:
            event["evidence"] = [evidence.copy()]
            aggregator.events.append(event)
        points.append({"timestamp_ms": timestamp, "visible_count": len(student_indices) if valid else None,
                       "brightness": round(brightness, 2), "sharpness": round(sharpness, 2), "valid": valid,
                       "pose_evaluated": len(predicted),
                       "hand_raise_count": sum(k=='hand_raise' and v is True for k,t,v,s in flags) if valid and any(k=='hand_raise' and v is not None for k,t,v,s in flags) else None,
                       "hand_evaluable_count": sum(k=='hand_raise' and v is not None for k,t,v,s in flags) if valid else 0})
        points[-1].update(head_down_count=sum(k=='head_down' and v is True for k,t,v,s in flags) if valid and any(k=='head_down' and v is not None for k,t,v,s in flags) else None,
                          head_evaluable_count=sum(k=='head_down' and v is not None for k,t,v,s in flags),frame_index=sampled_index if sampled_index is not None else audit.get('decoded_frames',1)-1)
        if preview is not None:
            preview_started=time.perf_counter()
            # Reuse saved evidence; no extra detection or pose inference.
            jpeg=None
            if evidence['original_file'] is None:
                ok,encoded=cv2.imencode('.jpg',image,[cv2.IMWRITE_JPEG_QUALITY,75])
                if ok:jpeg=encoded.tobytes()
            preview({'sequence':len(points),'timestamp_ms':timestamp,'width':width,'height':height,
                     'original_file':evidence['original_file'],'head_down_enabled':config.head_down_enabled,
                     'people':[{'track_id':p['track_id'],'bbox':p['bbox'],'behaviors':p['behaviors'],
                                'seat_id':p['seat_id']} for p in frame_people],
                     'counts':points[-1].copy()},jpeg)
            preview_seconds+=time.perf_counter()-preview_started
        progress(stage="analyzing", message=f"已检查 {len(points)} 张画面，正在分析录像的 {timestamp / 1000:.1f} 秒处",
                 progress=min(99, int((timestamp - start_ms) / (requested_end - start_ms) * 100)),
                 sample_count=len(points), last_timestamp_ms=timestamp)
    if not points:
        raise ValueError("所选范围内没有可用视频帧")
    source_frame_ms = round(1000 / metadata.get("nominal_fps", 25)) if metadata.get("nominal_fps") else 40
    last_observed = points[-1]["timestamp_ms"] if frame_limit else audit.get("last_decoded_ms", points[-1]["timestamp_ms"])
    actual_end = min(requested_end, last_observed + source_frame_ms)
    aggregator.finish_all(actual_end)
    if frame_limit:
        limitations.append("达到配置的最大采样数，剩余录像未分析。")
    if actual_end < requested_end - 1000:
        limitations.append("解码结束早于请求范围末尾，覆盖按实际解码位置计算。")
    if len(snapshots) >= 1000:
        limitations.append("证据图片达到 1000 张上限，后续事件仅保留原录像时间点。")
    if stats["small_person_instances"]:
        limitations.append(f"{stats['small_person_instances']} 次目标观察低于本版姿态尺寸门槛，未据此判断举手或低头。")
    if stats["person_instances"] > stats["pose_instances"]:
        limitations.append("部分人次因尺寸、区域或姿态人数上限未进入姿态推理；姿态覆盖不足不记为没有动作。")
    if stats['pose_identity_rejected']:
        limitations.append(f"{stats['pose_identity_rejected']} 次裁剪的姿态鼻点不能对应目标头部，已作为证据不足排除，防止借用邻座关键点。")
    coverage = {"start_ms": points[0]["timestamp_ms"], "end_ms": actual_end, "requested_start_ms": start_ms,
                "requested_end_ms": requested_end, "percent_of_video": round((actual_end - points[0]["timestamp_ms"]) / duration_ms * 100, 2),
                "sampling_is_continuous_observation": False, "decoded_frames": audit.get("decoded_frames", 0)}
    aggregator.events.sort(key=lambda e: (e["start_ms"], e["event_type"]))
    for index, event in enumerate(aggregator.events):
        event["event_id"] = f"E{index + 1:05d}"
    report=build_report(job_id, config.title, metadata, config, points, aggregator.events, snapshots, stats,
                        {**models.provenance, "runtime_status":runtime_status(), "pose_execution_metrics":models.pose_metrics,
                         "detection_pipelined":pipelined,"processor_version": VERSION, "tracking": "short-gap-iou-v1",
                         "rules_version": "upright-beside-face-quality-gate-v4.2", "head_method":"6d-headpose-negative-pitch-yaw60-v2",
                         "source_code_sha256":{name:hashlib.sha256((Path(__file__).parent/name).read_bytes()).hexdigest() for name in ('analysis.py','behavior.py','models.py','review.py')},
                         "tracking":"motion-distance-color-assignment-v2", "pose_crop_strategy": "upper-body-with-competing-face-ownership-and-tight-retry" if classroom else "person-box",
                         "counting_method": 'head' if classroom else 'person',
                         "ffmpeg": json.loads((directory / "proxy-info.json").read_text(encoding="utf-8"))},
                        coverage, time.monotonic() - started, limitations)
    report['persons']=list(persons.values())
    for person in report['persons']:
        person['summary']={k:sum(o['behaviors'][k] is True for o in person['observations']) for k in ('hand_raise','head_down','leave_seat','possible_phone')}
        person['sample_count']=len(person['observations'])
    report['layout']=seat_map.report(room_objects,config.door_roi if manual_door else False)
    report['statistics'].update(person_track_count=len(persons),seat_count=len(seat_map.seats),
        head_arm_artifacts_rejected=stats['head_arm_artifacts_rejected'],
        camera_alignment_frames=camera_updates,scene_cut_count=scene_cuts,
        timing_seconds={'detection':round(detect_seconds,3),'pose':round(pose_seconds,3),'head_pose':round(head_seconds,3),
                        'preview_publish':round(preview_seconds,4)})
    return report
