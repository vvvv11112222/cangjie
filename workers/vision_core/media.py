from fractions import Fraction
from pathlib import Path
import hashlib
import subprocess
import time
import os

import av
import cv2
import imageio_ffmpeg


class Cancelled(Exception):
    pass


def check_cancel(cancel):
    if hasattr(cancel, "check"):
        cancel.check()  # A Worker Guard must preserve timeout vs lease-loss exceptions.
        return
    if cancel.is_set():
        raise Cancelled("分析已取消")


def digest_file(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as f:
        for data in iter(lambda: f.read(1024 * 1024), b""):
            h.update(data)
    return h.hexdigest()


def half_up_ms(seconds):
    # Fraction avoids binary-float drift and Python's ties-to-even rounding.
    value = Fraction(seconds) * 1000
    return (value.numerator * 2 + value.denominator) // (2 * value.denominator)


def inspect_media(path, *, max_duration_ms=None):
    with av.open(str(path)) as container:
        if not container.streams.video:
            raise ValueError("媒体没有视频流")
        video = container.streams.video[0]
        origin = Fraction(container.start_time, av.time_base) if container.start_time is not None else min(
            (Fraction(s.start_time) * s.time_base for s in container.streams if s.start_time is not None),
            default=Fraction(0),
        )
        duration = container.duration / av.time_base if container.duration else None
        if not duration and video.duration is not None:
            duration = float(video.duration * video.time_base)
        if duration is None or duration <= 0:
            raise ValueError("无法确定有效录像时长")
        if max_duration_ms is not None and half_up_ms(Fraction(str(duration))) > max_duration_ms:
            raise ValueError("录像超过本地分析时长上限")
        frame = next(container.decode(video), None)
        if frame is None or frame.pts is None:
            raise ValueError("视频不能解码或首帧时间戳缺失")
        first_ms = half_up_ms(Fraction(frame.pts) * frame.time_base - origin)
        if first_ms < 0:
            raise ValueError("首帧早于公共时间起点，无法建立可靠时间轴")
        return {
            "duration_ms": half_up_ms(Fraction(str(duration))),
            "width": video.codec_context.width,
            "height": video.codec_context.height,
            "codec": video.codec_context.name,
            "has_audio": bool(container.streams.audio),
            "nominal_fps": float(video.average_rate) if video.average_rate else None,
            "time_base": str(video.time_base),
            "origin_s": float(origin),
            "first_video_ms": first_ms,
            "file_bytes": Path(path).stat().st_size,
            "sha256": digest_file(path),
            "ffmpeg_libraries": {k: list(v) for k, v in av.library_versions.items()},
        }


def ffmpeg_executable():
    return os.environ.get("VISION_FFMPEG_BINARY") or imageio_ffmpeg.get_ffmpeg_exe()


def make_proxy(source, target, cancel, *, timeout_s=3600):
    executable = ffmpeg_executable()
    command = [
        executable, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
        "-copyts", "-start_at_zero", "-i", str(source),
        "-map", "0:v:0", "-map", "0:a:0?", "-c:v", "libx264", "-preset", "veryfast",
        "-crf", "24", "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2:0:0:black",
        "-pix_fmt", "yuv420p", "-fps_mode", "passthrough",
        "-enc_time_base:v", "demux",
        "-c:a", "aac", "-movflags", "+faststart", str(target),
    ]
    error_log = target.with_suffix(".ffmpeg.txt")
    with error_log.open("wb") as errors:
        process = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=errors)
        try:
            started = time.monotonic()
            while process.poll() is None:
                check_cancel(cancel)
                if timeout_s is not None and time.monotonic() - started > timeout_s:
                    raise TimeoutError("播放代理生成超过 1 小时")
                time.sleep(0.2)
            if process.returncode:
                raise ValueError("FFmpeg 转码失败：" + error_log.read_text(encoding="utf-8", errors="replace")[-1200:])
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
    output = inspect_media(target)
    return {"metadata": output, "ffmpeg_binary_sha256": digest_file(executable), "command": command[1:]}


def first_frame(path, output):
    with av.open(str(path)) as container:
        frame = next(container.decode(video=0))
        image = frame.to_ndarray(format="bgr24")
        ok, encoded = cv2.imencode(".jpg", image)
        if not ok:
            raise ValueError("无法编码预览图片")
        output.write_bytes(encoded.tobytes())


def sampled_frames(path, start_ms, end_ms, fps, cancel, audit=None):
    interval = Fraction(1000) / Fraction(str(fps))
    next_sample = Fraction(start_ms)
    previous = -1
    with av.open(str(path)) as container:
        video = container.streams.video[0]
        origin = Fraction(container.start_time, av.time_base) if container.start_time is not None else min(
            (Fraction(s.start_time) * s.time_base for s in container.streams if s.start_time is not None),
            default=Fraction(0),
        )
        # Decode sequentially: seek targets are not substituted for actual PTS.
        for frame in container.decode(video):
            check_cancel(cancel)
            if frame.pts is None:
                raise ValueError("解码帧缺少 PTS，停止生成不可靠时间证据")
            timestamp = half_up_ms(Fraction(frame.pts) * frame.time_base - origin)
            if timestamp < 0 or timestamp < previous:
                raise ValueError("检测到非单调视频时间轴")
            previous = timestamp
            if audit is not None:
                audit["decoded_frames"] = audit.get("decoded_frames", 0) + 1
                audit["last_decoded_ms"] = timestamp
            if timestamp >= end_ms:
                if audit is not None:
                    audit["requested_end_reached"] = True
                break
            if timestamp < next_sample:
                continue
            yield timestamp, frame.to_ndarray(format="bgr24")
            while next_sample <= timestamp:
                next_sample += interval
