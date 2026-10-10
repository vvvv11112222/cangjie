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
