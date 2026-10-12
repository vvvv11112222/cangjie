from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from types import SimpleNamespace

import pytest

from workers.vision_core import models


def test_cpu_provider_uses_cpu_limit_even_when_gpu_flag_is_true(monkeypatch):
    captured = {}
    def construct(path, sess_options, providers):
        captured.update(options=sess_options, providers=providers)
        return SimpleNamespace(get_providers=lambda: ["CPUExecutionProvider"])
    monkeypatch.setattr(models, "runtime_kind", "cpu")
    monkeypatch.setattr(models, "runtime_settings", {"cpu_threads": 2, "gpu_threads": 1})
    monkeypatch.delenv("VISION_CPU_THREADS", raising=False)
    monkeypatch.setattr(models.ort, "InferenceSession", construct)
    models.session(Path("test.onnx"), use_gpu=True)
    assert captured["providers"] == ["CPUExecutionProvider"]
    assert captured["options"].intra_op_num_threads == 2
    assert captured["options"].get_session_config_entry("session.intra_op.allow_spinning") == "0"
    assert captured["options"].get_session_config_entry("session.inter_op.allow_spinning") == "0"


@pytest.mark.parametrize("threads", ["0", "17", "invalid"])
def test_invalid_cpu_limit_fails_before_model_is_loaded(monkeypatch, threads):
    monkeypatch.setattr(models, "runtime_kind", "cpu")
    monkeypatch.setenv("VISION_CPU_THREADS", threads)
    def forbidden(*args, **kwargs):
        pytest.fail("invalid configuration must not load a model")
    monkeypatch.setattr(models.ort, "InferenceSession", forbidden)
    with pytest.raises(ValueError):
        models.session(Path("test.onnx"), use_gpu=True)


def test_lazy_session_is_loaded_once_and_keeps_return_values():
    loaded = []
    runtime = SimpleNamespace(run=lambda: 7)
    session = models._LazySession(lambda: loaded.append(True) or runtime)
    assert loaded == []
    with ThreadPoolExecutor(max_workers=2) as pool:
        assert list(pool.map(lambda _: session.run(), range(4))) == [7] * 4
    assert loaded == [True]


def test_cpu_status_does_not_present_available_gpu_as_active(monkeypatch):
    monkeypatch.setattr(models, "runtime_kind", "cpu")
    monkeypatch.setattr(models, "_instance", None)
    def forbidden():
        pytest.fail("CPU status does not select a GPU")
    monkeypatch.setattr(models, "selected_device", forbidden)
    assert models.runtime_status()["device"] is None
    assert models.runtime_status()["execution_engine"] == "cpu"


@pytest.fixture
def clean_session_records(monkeypatch):
    monkeypatch.setattr(models, "_session_records", {})
    monkeypatch.setattr(models, "_instance", None)


@pytest.mark.parametrize("device_id", [0, 2])
def test_cuda_status_uses_actual_session_options_without_dxgi(monkeypatch, clean_session_records, device_id):
    captured = {}
    def forbidden():
        pytest.fail("CUDA must not use the DirectML/DXGI device inventory")
    def construct(path, sess_options, providers):
        captured["providers"] = providers
        return SimpleNamespace(get_providers=lambda: ["CUDAExecutionProvider", "CPUExecutionProvider"],
                               get_provider_options=lambda: {"CUDAExecutionProvider": {"device_id": str(device_id)}})
    monkeypatch.setattr(models, "runtime_kind", "cuda")
    monkeypatch.setattr(models, "selected_device", forbidden)
    monkeypatch.setattr(models.ort, "get_available_providers", lambda: ["CUDAExecutionProvider", "CPUExecutionProvider"])
    monkeypatch.setattr(models.ort, "InferenceSession", construct)
    models.session(Path("cuda.onnx"), use_gpu=True)
    status = models.runtime_status()
    assert captured["providers"][0][1]["device_id"] == 0
    assert status["device"] == {"provider": "CUDAExecutionProvider", "device_id": device_id}
    assert status["sessions"][0]["device"] == status["device"]
    assert status["execution_engine"] == "cuda" and status["warnings"] == []


@pytest.mark.parametrize("options", [{}, {"device_id": "invalid"}, {"device_id": "-1"}])
def test_unknown_cuda_device_is_explicit_not_a_candidate(monkeypatch, clean_session_records, options):
    monkeypatch.setattr(models, "runtime_kind", "cuda")
    monkeypatch.setattr(models.ort, "get_available_providers", lambda: ["CUDAExecutionProvider"])
    monkeypatch.setattr(models.ort, "InferenceSession", lambda *args, **kwargs:
                        SimpleNamespace(get_providers=lambda: ["CUDAExecutionProvider"],
                                        get_provider_options=lambda: {"CUDAExecutionProvider": options}))
    monkeypatch.setattr(models, "selected_device", lambda: pytest.fail("CUDA does not select DXGI adapters"))
    models.session(Path("cuda.onnx"), use_gpu=True)
    status = models.runtime_status()
    assert status["device"] is None and status["sessions"][0]["device"] is None
    assert status["execution_engine"] == "cuda" and status["warnings"]


@pytest.mark.parametrize("fallback", ["unavailable", "silent", "exception"])
def test_cuda_cpu_fallback_records_no_gpu(monkeypatch, clean_session_records, fallback):
    monkeypatch.setattr(models, "runtime_kind", "cuda")
    monkeypatch.setattr(models, "selected_device", lambda: pytest.fail("CUDA fallback does not use DXGI"))
    monkeypatch.setattr(models.ort, "get_available_providers", lambda:
                        ["CPUExecutionProvider"] if fallback == "unavailable" else ["CUDAExecutionProvider", "CPUExecutionProvider"])
    def construct(path, sess_options, providers):
        if fallback == "exception" and providers != ["CPUExecutionProvider"]:
            raise RuntimeError("CUDA initialization failed")
        return SimpleNamespace(get_providers=lambda: ["CPUExecutionProvider"])
    monkeypatch.setattr(models.ort, "InferenceSession", construct)
    models.session(Path("cuda.onnx"), use_gpu=True)
    status = models.runtime_status()
    assert status["device"] is None and status["sessions"][0]["device"] is None
    assert status["execution_engine"] == "cpu" and status["configured_engine"] == "cuda"
    assert status["warnings"]


def test_directml_status_records_the_adapter_used_to_create_session(monkeypatch, clean_session_records):
    adapter = {"device_id": 1, "name": "DirectML adapter", "dedicated_mb": 8192}
    captured = {}
    monkeypatch.setattr(models, "runtime_kind", "directml")
    monkeypatch.setattr(models, "selected_device", lambda: {"selected": adapter, "error": None})
    monkeypatch.setattr(models.ort, "get_available_providers", lambda: ["DmlExecutionProvider", "CPUExecutionProvider"])
    def construct(path, sess_options, providers):
        captured["providers"] = providers
        return SimpleNamespace(get_providers=lambda: ["DmlExecutionProvider", "CPUExecutionProvider"])
    monkeypatch.setattr(models.ort, "InferenceSession", construct)
    models.session(Path("dml.onnx"), use_gpu=True)
    monkeypatch.setattr(models, "selected_device", lambda: pytest.fail("status uses recorded session, not a fresh candidate"))
    status = models.runtime_status()
    assert captured["providers"][0][1]["device_id"] == 1
    assert status["device"] == {**adapter, "provider": "DmlExecutionProvider"}
    assert status["execution_engine"] == "directml"


def test_no_cuda_session_does_not_report_an_active_device(monkeypatch, clean_session_records):
    monkeypatch.setattr(models, "runtime_kind", "cuda")
    monkeypatch.setattr(models, "selected_device", lambda: pytest.fail("no CUDA session means no selected device"))
    status = models.runtime_status()
    assert status["device"] is None and status["execution_engine"] == "unknown"
