import subprocess

import pytest

from workers.common.lifecycle import Guard
from workers.vision_core.media import ffmpeg_executable, inspect_media
from .fake_go import make_claim
from .test_worker import execute


def convert(source, output, arguments):
    subprocess.run([ffmpeg_executable(), "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
                    "-i", str(source), *arguments, str(output)], check=True, timeout=30)
    return output


@pytest.mark.parametrize("container", ["mov", "mkv"])
def test_supported_containers(source, settings, tmp_path, container):
    converted = convert(source, tmp_path / f"sample.{container}", ["-c:v", "copy"])
    outcome, fake = execute(converted, settings, make_claim(settings, converted, "probe"))
    assert outcome == "succeeded" and fake.complete_bytes


def test_nonzero_container_origin(source, settings, tmp_path):
    converted = convert(source, tmp_path / "offset.mp4", ["-c:v", "copy", "-output_ts_offset", "2"])
    assert inspect_media(converted)["origin_s"] == 2
    outcome, fake = execute(converted, settings, make_claim(settings, converted, "probe"))
    assert outcome == "succeeded" and fake.complete_bytes


def test_audio_starts_later_than_video(source, settings, tmp_path):
    converted = convert(source, tmp_path / "late-audio.mp4", [
        "-itsoffset", "0.5", "-f", "lavfi", "-i", "sine=frequency=440:duration=1.5",
        "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "-t", "2"])
    outcome, fake = execute(converted, settings, make_claim(settings, converted, "probe"))
    assert outcome == "succeeded" and not fake.failures


def test_corrupt_media_is_rejected(source, settings, tmp_path):
    broken = tmp_path / "broken.mp4"
    broken.write_bytes(b"not a video")
    outcome, fake = execute(broken, settings, make_claim(settings, broken, "probe"))
    assert outcome == "failed" and fake.failures[0]["error_code"] == "UNSUPPORTED_MEDIA"
    assert not fake.assets


def test_hevc_source_can_create_h264_proxy(source, settings, tmp_path):
    converted = convert(source, tmp_path / "hevc.mp4", ["-c:v", "libx265", "-preset", "ultrafast",
                                                         "-x265-params", "log-level=error"])
    assert inspect_media(converted)["codec"] == "hevc"
    outcome, fake = execute(converted, settings, make_claim(settings, converted, "probe"))
    assert outcome == "succeeded" and not fake.failures
