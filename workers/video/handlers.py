"""P0 implementation: no model load and no behavioral inference in the 1.1 DTO."""
from fractions import Fraction
import json

import av
import cv2

from workers.common.contracts import WorkerError
from workers.vision_core.media import inspect_media, make_proxy, sampled_frames, half_up_ms


def inspect_source(path, settings):
    try:
        metadata = inspect_media(path)
        with av.open(str(path)) as container:
            formats = set(container.format.name.split(","))
        if not formats.intersection({"mov", "mp4", "matroska"}):
            raise WorkerError("UNSUPPORTED_MEDIA", "P0 支持 MP4、MOV 或 MKV 视频")
        if metadata["codec"] not in ("h264", "hevc"):
            raise WorkerError("UNSUPPORTED_MEDIA", "P0 支持 H.264 或 H.265 视频")
        if metadata["height"] > settings.max_height or metadata["duration_ms"] > settings.max_duration_ms:
            raise WorkerError("MEDIA_TOO_LARGE", "录像时长或分辨率超过约定上限")
        return metadata
    except WorkerError:
        raise
    except av.error.FFmpegError as error:
        raise WorkerError("UNSUPPORTED_MEDIA", "媒体损坏或无法解码") from error
    except ValueError as error:
        raise WorkerError("TIMELINE_INVALID", "无法建立有效的录像时间轴") from error


def video_times(path, guard):
    with av.open(str(path)) as container:
        origin = Fraction(container.start_time, av.time_base) if container.start_time is not None else min(
            (Fraction(s.start_time) * s.time_base for s in container.streams if s.start_time is not None),
            default=Fraction(0))
        previous = -1
        for frame in container.decode(video=0):
            guard.check()
            if frame.pts is None:
                raise WorkerError("TIMELINE_INVALID", "视频缺少帧时间戳")
            timestamp = half_up_ms(Fraction(frame.pts) * frame.time_base - origin)
            if timestamp < 0 or timestamp < previous:
                raise WorkerError("TIMELINE_INVALID", "视频存在负时间或时间倒退")
            previous = timestamp
            yield timestamp


def check_proxy_timeline(source, proxy, guard):
    from itertools import zip_longest
    count = 0
    for original, playback in zip_longest(video_times(source, guard), video_times(proxy, guard)):
        if original is None or playback is None or abs(original - playback) > 1:
            raise WorkerError("TIMELINE_INVALID", "播放代理帧时间与原录像不一致")
        count += 1
    if not count:
        raise WorkerError("TIMELINE_INVALID", "播放代理没有可解码的视频帧")
    def audio_start(path):
        with av.open(str(path)) as container:
            if not container.streams.audio:
                return None
            origin = Fraction(container.start_time, av.time_base) if container.start_time is not None else min(
                (Fraction(s.start_time) * s.time_base for s in container.streams if s.start_time is not None),
                default=Fraction(0))
            frame = next(container.decode(audio=0), None)
            if frame is None or frame.pts is None:
                raise WorkerError("TIMELINE_INVALID", "音轨无法解码或缺少时间戳")
            return half_up_ms(Fraction(frame.pts) * frame.time_base - origin)
    original_audio, proxy_audio = audio_start(source), audio_start(proxy)
    if (original_audio is None) != (proxy_audio is None) or (
        original_audio is not None and (original_audio < 0 or proxy_audio < 0 or abs(original_audio - proxy_audio) > 25)
    ):
        raise WorkerError("TIMELINE_INVALID", "播放代理音轨起点与原录像不一致")
    return count


def base_result(claim):
    return {key: claim[key] for key in ("schema_version", "job_id", "run_id", "session_id", "media_asset_id", "stage", "execution")}


def probe(claim, directory, settings, transport, guard):
    source = directory / "input.bin"
    metadata = inspect_source(source, settings)
    guard.update(progress=10)
    proxy = directory / "playback.mp4"
    make_proxy(source, proxy, guard)
    check_proxy_timeline(source, proxy, guard)
    guard.update(progress=90)
    asset = transport.upload(claim, proxy, "proxy", None, guard)
    limitations = [] if metadata["has_audio"] else ["录像没有音轨；音频阶段应返回无音轨限制。"]
    return {**base_result(claim), "duration_ms": metadata["duration_ms"],
            "has_audio": metadata["has_audio"], "width": metadata["width"], "height": metadata["height"],
            "video_codec": metadata["codec"], "playback_asset_id": asset,
            "origin_offset_ms": 0, "limitations": limitations}


def video_analysis(claim, directory, settings, transport, guard):
    source = directory / "input.bin"
    metadata = inspect_source(source, settings)
    duration = claim["duration_ms"]
    if abs(duration - metadata["duration_ms"]) > 1:
        raise WorkerError("TIMELINE_INVALID", "指定媒体时长与 probe 结果不一致")
    parameters = claim["parameters"]
    interval, limit = parameters["keyframe_interval_ms"], parameters["max_keyframes"]
    audit, frames = {}, []
    last = None
    # max_keyframes stops processing early. Do not advertise unprocessed remainder as covered.
    iterator = sampled_frames(source, 0, duration, Fraction(1000, interval), guard, audit)
    try:
        for timestamp, image in iterator:
            guard.check()
            file = directory / f"frame-{len(frames):06d}-{timestamp}.jpg"
            ok, encoded = cv2.imencode(".jpg", image, [cv2.IMWRITE_JPEG_QUALITY, 90])
            if not ok:
                raise WorkerError("PROCESSING_FAILED", "关键帧编码失败")
            file.write_bytes(encoded.tobytes())
            asset = transport.upload(claim, file, "keyframe", timestamp, guard)
            frames.append({"asset_id": asset, "timestamp_ms": timestamp})
            last = timestamp
            guard.update(progress=int(timestamp / duration * 95))
            if len(frames) >= limit:
                break
    finally:
        iterator.close()
    if not frames:
        raise WorkerError("TIMELINE_INVALID", "录像内没有可用关键帧")
    end = min(duration, audit.get("last_decoded_ms", last) + 1)
    limitations = ["仅按约定间隔提取画面，未判断举手、低头等行为；行为分析需 P1 扩展接口。",
                   "处理范围记录实际解码窗口；不代表窗口内连续发生某种行为。"]
    if len(frames) == limit:
        limitations.append("已达到关键帧数量上限，后续录像未继续处理。")
    return {**base_result(claim), "frames": frames, "events": [], "model": None,
            "sampling": {"interval_ms": interval, "max_frames": limit},
            "coverage": [{"start_ms": frames[0]["timestamp_ms"], "end_ms": end}],
            "limitations": limitations}


HANDLERS = {"probe": probe, "video_analysis": video_analysis}
