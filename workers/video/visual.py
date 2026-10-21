"""Go adapter for the existing vision core. No second task system or transcoder."""
import hashlib
import json
import os
from pathlib import Path

from workers.common.contracts import WorkerError, json_bytes
from .handlers import base_result, inspect_source


def require_acceleration(status):
    """Do not finish a CPU result as an explicitly GPU-selected execution."""
    if os.environ.get('VISION_REQUIRE_GPU') != '1':
        return
    requested = [r for r in status['sessions'] if r['gpu_requested']]
    expected = int(os.environ['VISION_DML_DEVICE'])
    if not requested or any(r['provider'] != 'DmlExecutionProvider' or not r.get('device') or
                            r['device']['device_id'] != expected for r in requested):
        raise WorkerError('CONFIG_MISMATCH', '独显推理未按部署选择生效，请检查视觉设备日志；未降级为 CPU 继续分析')


def model_identity():
    root = Path(__file__).resolve().parents[1]
    paths = [root / "config/models.manifest.json"]
    paths += sorted((root / "vision_core").glob("*.py"))
    # Hash registry and inference/rule code, not absolute paths, caches or user media.
    digest = hashlib.sha256()
    for path in paths:
        digest.update(path.relative_to(root).as_posix().encode())
        digest.update(path.read_bytes().replace(b"\r\n", b"\n"))
    return {"name": "classroom-vision", "revision": digest.hexdigest()}


def package_report(report, claim, image_ids):
    """Keep raw positive/negative/unknown decisions; never promote them to reviews."""
    frames = [{"asset_id": asset, "timestamp_ms": timestamp}
              for timestamp, asset in sorted(image_ids.items())]
    persons = []
    for person in report["persons"]:
        observations = []
        for o in person["observations"]:
            # Tracking can extend a box by a subpixel beyond the decoded image.
            b = o["bbox"]
            width, height = report["metadata"]["width"], report["metadata"]["height"]
            box = [max(0., min(float(b[i]), float(width if i % 2 == 0 else height))) for i in range(4)]
            observations.append({"timestamp_ms": o["timestamp_ms"], "bbox": box,
                                 "behaviors": o["behaviors"],
                                 "frame_asset_id": image_ids.get(o["timestamp_ms"])})
        persons.append({"track_id": person["track_id"], "observations": observations})
    events = []
    for event in report["events"]:
        if event["event_type"] not in ("hand_raise", "head_down", "leave_seat", "possible_phone"):
            continue  # No class schedule: entry/exit cannot be labelled late/early.
        ids = list(dict.fromkeys(image_ids[e["timestamp_ms"]] for e in event.get("evidence", [])
                    if e["timestamp_ms"] in image_ids and event["start_ms"] <= e["timestamp_ms"] < event["end_ms"]))
        events.append({k: event[k] for k in ("event_type", "track_id", "start_ms", "end_ms", "confidence")})
        events[-1]["frame_asset_ids"] = ids
    c = report["coverage"]
    params = claim["parameters"]
    return {**base_result(claim), "frames": frames, "events": events,
            "model": params["visual"]["model"],
            "sampling": {"interval_ms": params["keyframe_interval_ms"], "max_frames": params["max_keyframes"]},
            "coverage": [{"start_ms": c["start_ms"], "end_ms": c["end_ms"]}],
            "limitations": report["limitations"] + [
                "编号只代表本次录像中的匿名轨迹，遮挡和切镜可能使编号变化。",
                "举手或低头是画面观察，不能直接代表听课质量；未检查的时刻不补判断。",
                "证据图片数量受任务上限约束；没有图片的观察仍可定位原录像核对。"],
            "visual": {"input_sha256": claim["input_sha256"],
                       "width": report["metadata"]["width"], "height": report["metadata"]["height"],
                       "counts": [{k: point.get(k) for k in ("timestamp_ms", "visible_count", "hand_raise_count", "head_down_count")}
                                  for point in report["counts"]], "persons": persons}}


def visual_analysis(claim, directory, settings, transport, guard):
    if not settings.vision_enabled or claim["parameters"]["visual"]["model"] != model_identity():
        raise WorkerError("CONFIG_MISMATCH", "视觉模型和规则版本与领取任务不一致")
    source = directory / "input.bin"
    metadata = inspect_source(source, settings)
    if metadata["sha256"] != claim["input_sha256"] or abs(metadata["duration_ms"] - claim["duration_ms"]) > 1:
        raise WorkerError("TIMELINE_INVALID", "录像摘要或时长与领取任务不一致")
    # The core uses encoded pixel coordinates. A display rotation would disagree
    # with browser playback: reject explicitly until normalized media is provided.
    import av
    with av.open(str(source)) as container:
        first = next(container.decode(video=0))
        rotation = getattr(first, "rotation", 0)
        if rotation or container.streams.video[0].metadata.get("rotate", "0") not in ("0", ""):
            raise WorkerError("UNSUPPORTED_MEDIA", "请先将带旋转标记的视频转正后再做视觉分析")
    from workers.vision_core.config import AnalysisConfig
    from workers.vision_core.analysis import analyze
    if os.environ.get('VISION_REQUIRE_GPU') == '1':
        from workers.vision_core.models import get_models, runtime_status
        get_models()
        active = runtime_status()
        require_acceleration(active)
        print('vision_job='+claim['job_id']+' active_runtime='+json.dumps(active, ensure_ascii=False),flush=True)
    work = directory / "visual"
    work.mkdir()
    # Both files share a lease directory; hardlink avoids a full video copy and
    # avoids re-encoding a proxy that probe has already generated.
    os.link(source, work / "playback.mp4")
    (work / "proxy-info.json").write_text(json.dumps({"strategy": "source-hardlink-no-transcode"}), encoding="utf-8")
    p = claim["parameters"]
    config = AnalysisConfig(duration_s=0, sampling_fps=1000 / p["keyframe_interval_ms"],
                            max_samples=p["visual"]["max_samples"], max_pose_people=p["visual"]["max_pose_people"],
                            head_down_enabled=p["visual"]["head_down_enabled"])
    def progress(**values):
        guard.update(progress=int(values.get("progress", 0) * .90))
    report = analyze(claim["job_id"], work, metadata, config, guard, progress)
    active = report['provenance']['runtime_status']
    require_acceleration(active)
    print('vision_job='+claim['job_id']+' timings='+json.dumps(report['statistics']['timing_seconds'])+
          ' runtime='+json.dumps(active, ensure_ascii=False),flush=True)
    # Check the observation limit before uploading pictures. No silent loss.
    if sum(len(p["observations"]) for p in report["persons"]) > 100000:
        raise WorkerError("MEDIA_TOO_LARGE", "逐人记录超过十万条，请将录像分段分析")
    image_ids = {}
    snapshots = sorted(report["snapshots"], key=lambda s: s["timestamp_ms"])
    # Prefer event pictures; remaining pictures retain chronological order.
    priority = {e["timestamp_ms"] for event in report["events"] for e in event.get("evidence", [])}
    selected = sorted(snapshots, key=lambda s: (s["timestamp_ms"] not in priority, s["timestamp_ms"]))[:p["max_keyframes"]]
    for index, snapshot in enumerate(selected):
        guard.check()
        path = work / "evidence" / snapshot["original_file"]
        image_ids[snapshot["timestamp_ms"]] = transport.upload(claim, path, "keyframe", snapshot["timestamp_ms"], guard)
        guard.update(progress=90 + int(8 * (index + 1) / max(1, len(selected))))
    result = package_report(report, claim, image_ids)
    if len(json_bytes(result)) > 32 * 1024**2:
        raise WorkerError("MEDIA_TOO_LARGE", "视觉结果超过 32 MiB，请将录像分段分析")
    return result
