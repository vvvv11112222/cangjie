"""Regressions for PR #8: recovery, limits, timeouts and report export."""
from contextlib import contextmanager
from dataclasses import replace
from datetime import datetime, timezone
from email.utils import format_datetime
import csv
import io
import json
import time
import threading
from types import SimpleNamespace

import av
import httpx
import pytest

from workers.common.contracts import LeaseLost, WorkerError
from workers.common.lifecycle import Guard, scratch_path
from workers.common.runner import Runner, execute_claim
from workers.common.transport import Transport
from workers.video.handlers import inspect_source
from workers.vision_core.media import check_cancel, inspect_media
from workers.vision_core.reporting import csv_persons
from workers.vision_core.review import review_observation
from .fake_go import FakeGo, make_claim
from .test_timeline import convert
from .test_worker import execute


@pytest.mark.parametrize("failure", [429, 503, "network"])
def test_claim_polling_recovers_without_immediate_uncertain_replay(settings, monkeypatch, failure):
    calls, sleeps = [], []

    def handle(request):
        calls.append(request.content)
        if len(calls) == 1:
            if failure == "network":
                raise httpx.ReadError("reply lost", request=request)
            return httpx.Response(failure, headers={"Retry-After": "7"})
        if len(calls) == 2:
            return httpx.Response(204)
        raise KeyboardInterrupt

    runner = Runner(settings)
    runner.transport.close()
    runner.transport = Transport(settings, httpx.Client(transport=httpx.MockTransport(handle)))
    monkeypatch.setattr("workers.common.runner.time.sleep", sleeps.append)
    with pytest.raises(KeyboardInterrupt):
        runner.run()
    assert len(calls) == 3 and len(sleeps) == 2
    assert sleeps[0] >= (7 if failure == 429 else settings.lease_seconds)
    assert sleeps[1] == 5
    assert runner.transport.client.is_closed


@pytest.mark.parametrize("status", [401, 403, 409, 410])
def test_claim_access_rejection_does_not_poll_again(settings, status):
    calls = []
    def handle(request):
        calls.append(request)
        return httpx.Response(status)
    runner = Runner(settings)
    runner.transport.close()
    runner.transport = Transport(settings, httpx.Client(transport=httpx.MockTransport(handle)))
    with pytest.raises(LeaseLost):
        runner.run()
    assert len(calls) == 1 and runner.transport.client.is_closed


@pytest.mark.parametrize("operation", ["input", "artifacts", "complete"])
def test_rate_limit_retries_same_job_with_retry_after(source, settings, monkeypatch, operation):
    claim = make_claim(settings, source)
    fake = FakeGo(source, claim)
    attempts, content_types, sleeps = [], [], []
    def handle(request):
        if request.url.path.endswith("/" + operation):
            attempts.append(request.content)
            content_types.append(request.headers.get('Content-Type'))
            if len(attempts) == 1:
                return httpx.Response(429, headers={"Retry-After": "2"})
        return fake.handle(request)
    def wait(guard, seconds):
        guard.check()
        sleeps.append(seconds)
    monkeypatch.setattr(Guard, "wait", wait)
    transport = Transport(settings, httpx.Client(transport=httpx.MockTransport(handle)))
    outcome = execute_claim(settings, claim, Guard(claim), scratch_path(settings.temp_root, claim), transport)
    assert outcome == "succeeded" and not fake.failures
    assert sleeps == [2]
    assert len(attempts) >= 2
    if operation == "complete":
        assert attempts[0] == attempts[1]
    if operation == "artifacts":
        # Multipart boundaries may differ, but the immutable key and file must match.
        from email.parser import BytesParser
        from email.policy import default
        def parts(index):
            message = BytesParser(policy=default).parsebytes(
                b'Content-Type: ' + content_types[index].encode() + b'\r\n\r\n' + attempts[index])
            return {p.get_param('name', header='content-disposition'): p.get_payload(decode=True)
                    for p in message.iter_parts()}
        assert parts(0) == parts(1)
        assert len(fake.assets) == 4


def test_exhausted_rate_limit_is_reported_retryable(source, settings, monkeypatch):
    claim, attempts = make_claim(settings, source), []
    fake = FakeGo(source, claim)
    def handle(request):
        if request.url.path.endswith("/complete"):
            attempts.append(request.content)
            return httpx.Response(429, headers={"Retry-After": "0"})
        return fake.handle(request)
    transport = Transport(settings, httpx.Client(transport=httpx.MockTransport(handle)))
    outcome = execute_claim(settings, claim, Guard(claim), scratch_path(settings.temp_root, claim), transport)
    assert outcome == "failed" and len(attempts) == 3
    assert len(set(attempts)) == 1
    assert fake.failures[0]["error_code"] == "WORKER_API_ERROR"
    assert fake.failures[0]["retryable"] is True


def test_retry_after_http_date_and_invalid_value(settings, monkeypatch):
    now = 1700000000
    monkeypatch.setattr("workers.common.transport.time.time", lambda: now)
    date = format_datetime(datetime.fromtimestamp(now + 12, timezone.utc), usegmt=True)
    with pytest.raises(WorkerError) as caught:
        Transport.check_response(httpx.Response(429, headers={"Retry-After": date}), (200,))
    assert caught.value.retryable and caught.value.retry_after == 12
    for text in ("junk", "-1", "inf"):
        with pytest.raises(WorkerError) as invalid:
            Transport.check_response(httpx.Response(429, headers={"Retry-After": text}), (200,))
        assert invalid.value.retryable and invalid.value.retry_after is None


def test_media_detected_local_timeout_reports_correct_reason(source, settings):
    claim = make_claim(settings, source)
    fake, guard = FakeGo(source, claim), Guard(claim)
    def handler(*args):
        guard.local_deadline = time.time() - 1
        check_cancel(guard)
    outcome = execute_claim(settings, claim, guard, scratch_path(settings.temp_root, claim),
                            Transport(settings, fake.client()), {claim["stage"]: handler})
    assert outcome == "failed"
    assert fake.failures == [{"error_code": "PROCESSING_TIMEOUT",
                              "message": "媒体阶段超过本地执行时限", "retryable": True}]
    assert not guard.event.is_set() and not fake.complete_bytes


def test_timeout_never_clears_external_revocation(source, settings):
    claim = make_claim(settings, source)
    fake, guard = FakeGo(source, claim), Guard(claim)
    def handler(*args):
        guard.event.set()
        raise WorkerError("PROCESSING_TIMEOUT", "timeout raced with cancellation", True)
    execute_claim(settings, claim, guard, scratch_path(settings.temp_root, claim),
                  Transport(settings, fake.client()), {claim["stage"]: handler})
    assert guard.event.is_set() and not fake.failures and not fake.complete_bytes


def test_media_cancellation_is_stopped_not_processing_failure(source, settings):
    claim = make_claim(settings, source)
    fake, guard = FakeGo(source, claim), Guard(claim)
    def handler(*args):
        guard.event.set()
        check_cancel(guard)
    result = execute_claim(settings, claim, guard, scratch_path(settings.temp_root, claim),
                           Transport(settings, fake.client()), {claim["stage"]: handler})
    assert result == "stopped" and not fake.failures and not fake.complete_bytes


def test_retry_after_does_not_sleep_past_lease(source, settings):
    guard = Guard(make_claim(settings, source))
    guard.expires.value = time.time() + .03
    started = time.monotonic()
    with pytest.raises(LeaseLost):
        guard.wait(60)
    assert time.monotonic() - started < 1


def test_renewed_lease_does_not_shorten_retry_after(source, settings):
    guard = Guard(make_claim(settings, source))
    guard.expires.value = time.time() + .03
    def renew():
        guard.expires.value = time.time() + 2
    timer = threading.Timer(.01, renew)
    timer.start()
    try:
        started = time.monotonic()
        guard.wait(.07)
        assert time.monotonic() - started >= .06
    finally:
        timer.join()


def test_download_retry_restarts_partial_file(source, settings, tmp_path, monkeypatch):
    claim, attempts = make_claim(settings, source), []
    content = source.read_bytes()
    class Interrupted(httpx.SyncByteStream):
        def __iter__(self):
            yield content[:13]
            raise httpx.ReadError('stream interrupted')
    def handle(request):
        attempts.append(request)
        return httpx.Response(200, stream=Interrupted()) if len(attempts) == 1 else httpx.Response(200, content=content)
    monkeypatch.setattr(Guard, 'wait', lambda guard, seconds: guard.check())
    transport = Transport(settings, httpx.Client(transport=httpx.MockTransport(handle)))
    try:
        target = tmp_path / 'download.bin'
        transport.download(claim, target, Guard(claim))
        assert len(attempts) == 2 and target.read_bytes() == content
    finally:
        transport.close()


def test_parent_reports_local_timeout_with_live_lease(source, settings):
    claim = make_claim(settings, source, "probe")
    fake = FakeGo(source, claim)
    server = fake.server()
    try:
        configured = replace(settings, api_base=f"http://127.0.0.1:{server.server_port}/internal/v1",
                             probe_timeout=.1, lease_seconds=.05, heartbeat_seconds=.01)
        runner = Runner(configured)
        try:
            assert runner.run_claim(claim) == "failed"
        finally:
            runner.transport.close()
        assert len(fake.failures) == 1
        assert fake.failures[0]["error_code"] == "PROCESSING_TIMEOUT"
        assert not fake.complete_bytes and not scratch_path(settings.temp_root, claim).exists()
    finally:
        server.shutdown()
        server.server_close()


@pytest.mark.parametrize("size", ["161:120", "160:121", "161:121"])
def test_odd_dimension_proxy_preserves_source_timeline(source, settings, tmp_path, size):
    odd = convert(source, tmp_path / "odd.mp4", ["-vf", f"scale={size}", "-c:v", "libx264",
                  "-pix_fmt", "yuv444p", "-output_ts_offset", "2"])
    original = inspect_media(odd)
    outcome, fake = execute(odd, settings, make_claim(settings, odd, "probe"))
    assert outcome == "succeeded" and not fake.failures
    result = json.loads(fake.complete_bytes[0])
    assert (result["width"], result["height"]) == (original["width"], original["height"])
    proxy = tmp_path / "proxy.mp4"
    proxy.write_bytes(next(iter(fake.assets.values()))["bytes"])
    playback = inspect_media(proxy)
    assert playback["width"] == original["width"] + original["width"] % 2
    assert playback["height"] == original["height"] + original["height"] % 2
    assert playback["duration_ms"] == original["duration_ms"]


def test_formal_duration_limit_comes_from_settings(source, settings, monkeypatch):
    real_open = av.open
    @contextmanager
    def long_metadata(*args, **kwargs):
        with real_open(*args, **kwargs) as container:
            yield SimpleNamespace(duration=9_000_000_000, start_time=container.start_time,
                                  streams=container.streams, decode=container.decode, format=container.format)
    monkeypatch.setattr(av, "open", long_metadata)
    allowed = replace(settings, max_duration_ms=10_800_000)
    assert inspect_source(source, allowed)["duration_ms"] == 9_000_000
    with pytest.raises(WorkerError) as caught:
        inspect_source(source, settings)
    assert caught.value.code == "MEDIA_TOO_LARGE"


@pytest.mark.parametrize("text", ["=1+1", "+SUM(1,2)", "-1+1", "@SUM(1)", " \t=1+1", "\tstudent", "\r=1", "\n=1", "老师，已核对"])
def test_csv_review_text_is_safe_without_changing_json(text):
    report = {"persons": [{"track_id": "T1", "observations": [{"timestamp_ms": 500,
        "frame_index": 10, "seat_id": None, "behaviors": dict(hand_raise=True, head_down=None,
        leave_seat=False, possible_phone=None), "head_angles": {"pitch": -30.5}, "pose_available": True}]}]}
    review_observation(report, "T1", 500, "hand_raise", True, text, text, 0, "2026-10-08T00:00:00Z")
    row = next(csv.DictReader(io.StringIO(csv_persons(report).lstrip("\ufeff"))))
    expected = text if text == "老师，已核对" else "'" + text
    assert row["hand_raise_review_reason"] == expected and row["hand_raise_reviewer"] == expected
    assert row["pitch"] == "-30.5" and row["head_down"] == "unknown"
    assert report["review_history"][0]["reason"] == text
