import subprocess
from fractions import Fraction
from types import SimpleNamespace

import numpy as np
import pytest

from workers.common.contracts import LeaseLost, WorkerError
from workers.common.lifecycle import Guard
from workers.video import handlers
from workers.vision_core import media
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


@pytest.mark.parametrize("timestamps", [[0, 500, None], [0, 500, 400], [0, 500, -100]])
def test_later_invalid_timestamp_reports_timeline_error(source, settings, monkeypatch, timestamps):
    # Keep the actual sampler, handler, uploads and execute_claim failure callback.
    claim = make_claim(settings, source)
    monkeypatch.setattr(handlers, "inspect_source", lambda *args: {"duration_ms": claim["duration_ms"]})
    class Container:
        start_time = 0
        streams = SimpleNamespace(video=[object()])
        def __enter__(self):
            return self
        def __exit__(self, *args):
            pass
        def decode(self, video):
            for timestamp in timestamps:
                yield SimpleNamespace(pts=timestamp, time_base=Fraction(1, 1000),
                                      to_ndarray=lambda **kwargs: np.zeros((120, 160, 3), np.uint8))
    monkeypatch.setattr(media.av, "open", lambda *args: Container())
    outcome, fake = execute(source, settings, claim)
    assert outcome == "failed" and not fake.complete_bytes
    assert len(fake.assets) == 2  # The invalid timestamp occurs after successful sampling.
    assert len(fake.failures) == 1
    assert fake.failures[0]["error_code"] == "TIMELINE_INVALID"
    assert fake.failures[0]["retryable"] is False


@pytest.mark.parametrize("error, outcome, code", [
    (LeaseLost("lease revoked"), "stopped", None),
    (WorkerError("PROCESSING_TIMEOUT", "local deadline", True), "failed", "PROCESSING_TIMEOUT"),
    (ValueError("unrelated processing error"), "failed", "PROCESSING_FAILED"),
])
def test_sampling_keeps_lease_timeout_and_other_error_classification(source, settings, monkeypatch, error, outcome, code):
    claim = make_claim(settings, source)
    def interrupted(*args, **kwargs):
        yield 0, np.zeros((120, 160, 3), np.uint8)
        raise error
    monkeypatch.setattr(handlers, "sampled_frames", interrupted)
    actual, fake = execute(source, settings, claim)
    assert actual == outcome and not fake.complete_bytes
    assert [failure["error_code"] for failure in fake.failures] == ([] if code is None else [code])
