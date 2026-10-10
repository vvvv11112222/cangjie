from dataclasses import replace
from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path
import time

import pytest

from workers.common.contracts import LeaseLost, WorkerError, validate_result
from workers.common.lifecycle import Guard, cleanup, cleanup_expired, scratch_path, validate_claim
from workers.common.runner import Runner, execute_claim
from workers.common.transport import Transport
from workers.vision_core.media import half_up_ms
from .fake_go import FakeGo, make_claim


def execute(source, settings, claim, fake=None):
    fake = fake if fake is not None else FakeGo(source, claim)
    outcome = execute_claim(settings, claim, Guard(claim), scratch_path(settings.temp_root, claim),
                            Transport(settings, fake.client()))
    return outcome, fake


@pytest.mark.parametrize("stage", ["probe", "video_analysis"])
def test_real_processing_contract(source, settings, stage):
    claim = make_claim(settings, source, stage)
    outcome, fake = execute(source, settings, claim)
    assert outcome == "succeeded" and not fake.failures
    result = json.loads(fake.complete_bytes[0])
    validate_result(result, claim)
    assert len(fake.assets) == (1 if stage == "probe" else 4)
    if stage == "video_analysis":
        assert [f["timestamp_ms"] for f in result["frames"]] == [0, 500, 1000, 1500]
        assert result["events"] == [] and result["model"] is None
    else:
        assert result["duration_ms"] == 2000 and result["origin_offset_ms"] == 0
        assert not result["has_audio"] and result["limitations"]
    assert not scratch_path(settings.temp_root, claim).exists()


def test_exact_completion_retry(source, settings):
    claim = make_claim(settings, source)
    fake = FakeGo(source, claim)
    fake.fail_complete_once = True
    outcome, fake = execute(source, settings, claim, fake)
    assert outcome == "succeeded" and len(fake.complete_bytes) == 2
    assert fake.complete_bytes[0] == fake.complete_bytes[1]


def test_limited_frames_do_not_claim_full_coverage(source, settings):
    claim = make_claim(settings, source, limit=1)
    outcome, fake = execute(source, settings, claim)
    result = json.loads(fake.complete_bytes[0])
    assert outcome == "succeeded" and len(result["frames"]) == 1
    assert result["coverage"][0]["end_ms"] == 1


@pytest.mark.parametrize("field,value,code", [
    ("input_sha256", "0" * 64, "INPUT_DIGEST_MISMATCH"),
    ("input_url", "/internal/v1/jobs/wrong/input", "INVALID_RESULT"),
])
def test_invalid_input_stops_before_artifacts(source, settings, field, value, code):
    claim = make_claim(settings, source)
    claim[field] = value
    outcome, fake = execute(source, settings, claim)
    assert outcome == "failed" and fake.failures[0]["error_code"] == code
    assert not fake.assets and not fake.complete_bytes


def test_config_mismatch_does_not_download(source, settings):
    claim = make_claim(settings, source)
    claim["execution"]["processor_version"] = "different-worker"
    outcome, fake = execute(source, settings, claim)
    assert outcome == "failed" and fake.failures[0]["error_code"] == "CONFIG_MISMATCH"
    assert not any(path.endswith("/input") for _, path in fake.requests)


def test_download_never_follows_redirect(source, settings):
    claim = make_claim(settings, source)
    fake = FakeGo(source, claim)
    fake.redirect_input = True
    outcome, fake = execute(source, settings, claim, fake)
    assert outcome == "failed" and not fake.assets
    assert len([p for _, p in fake.requests if p.endswith("/input")]) == 1


def test_expired_lease_has_no_callbacks(source, settings):
    claim = make_claim(settings, source)
    claim["lease_expires_at"] = (datetime.now(timezone.utc) - timedelta(seconds=1)).isoformat()
    outcome, fake = execute(source, settings, claim)
    assert outcome == "stopped" and fake.requests == []


def test_cancellation_stops_active_child(source, settings):
    claim = make_claim(settings, source, stage="probe")
    fake = FakeGo(source, claim)
    fake.cancel_heartbeat = True
    server = fake.server()
    try:
        settings = replace(settings, api_base=f"http://127.0.0.1:{server.server_port}/internal/v1")
        runner = Runner(settings)
        assert runner.run_claim(claim) == "stopped"
        runner.transport.close()
        assert not fake.complete_bytes and not scratch_path(settings.temp_root, claim).exists()
    finally:
        server.shutdown()
        server.server_close()


def test_real_http_and_spawned_worker(source, settings):
    claim = make_claim(settings, source)
    fake = FakeGo(source, claim)
    server = fake.server()
    try:
        settings = replace(settings, api_base=f"http://127.0.0.1:{server.server_port}/internal/v1")
        runner = Runner(settings)
        assert runner.transport.claim() == claim
        assert runner.run_claim(claim) == "succeeded"
        assert runner.transport.claim() is None
        runner.transport.close()
        assert len(fake.complete_bytes) == 1 and len(fake.assets) == 4
    finally:
        server.shutdown()
        server.server_close()


def test_parent_heartbeats_never_rewrite_child_cleanup_marker(source, settings, monkeypatch):
    claim = make_claim(settings, source)
    fake = FakeGo(source, claim)
    server = fake.server()
    original_write = Path.write_text
    heartbeat_count = []
    try:
        configured = replace(settings, api_base=f"http://127.0.0.1:{server.server_port}/internal/v1",
                             heartbeat_seconds=.01)
        runner = Runner(configured)
        original_heartbeat = runner.transport.heartbeat
        def heartbeat(*args, **kwargs):
            heartbeat_count.append(1)
            return original_heartbeat(*args, **kwargs)
        def reject_parent_marker(path, *args, **kwargs):
            if path.name == "lease.deadline":
                raise FileNotFoundError("child completed and deleted the directory during renewal")
            return original_write(path, *args, **kwargs)
        # Spawned children import a fresh module; this interception affects only the parent.
        monkeypatch.setattr(Path, "write_text", reject_parent_marker)
        monkeypatch.setattr(runner.transport, "heartbeat", heartbeat)
        try:
            assert runner.run_claim(claim) == "succeeded"
        finally:
            runner.transport.close()
        assert len(heartbeat_count) >= 2
        assert len(fake.complete_bytes) == 1 and not fake.failures
        assert not scratch_path(settings.temp_root, claim).exists()
    finally:
        server.shutdown()
        server.server_close()


def test_cleanup_containment_and_expired_marker(source, settings, tmp_path):
    claim = make_claim(settings, source)
    directory = scratch_path(settings.temp_root, claim)
    directory.mkdir(parents=True)
    (directory / "lease.deadline").write_text(str(time.time() - 1), encoding="utf-8")
    unrelated = settings.temp_root / "unrelated"
    unrelated.mkdir()
    cleanup_expired(settings.temp_root)
    assert not directory.exists() and unrelated.exists()
    with pytest.raises(ValueError):
        cleanup(settings.temp_root, tmp_path)
    with pytest.raises(ValueError):
        cleanup(settings.temp_root, settings.temp_root)


def test_half_up_timestamps():
    from fractions import Fraction
    assert half_up_ms(Fraction(1, 2000)) == 1
    assert half_up_ms(Fraction(3, 2000)) == 2


def test_port_keeps_existing_behavior_logic():
    root = Path(__file__).resolve().parents[1]
    provenance = json.loads((root / "source-port.json").read_text(encoding="utf-8"))
    for name in provenance["unchanged_logic"]:
        digest = hashlib.sha256((root / "vision_core" / name).read_bytes()).hexdigest()
        assert digest == provenance["files"][name]["source_sha256"]


def test_existing_lease_directory_is_never_deleted(source, settings):
    claim = make_claim(settings, source)
    directory = scratch_path(settings.temp_root, claim)
    directory.mkdir(parents=True)
    marker = directory / "existing.txt"
    marker.write_text("belongs to another attempt", encoding="utf-8")
    outcome, fake = execute(source, settings, claim)
    assert outcome == "failed" and marker.exists()
    assert not fake.assets and not fake.complete_bytes
