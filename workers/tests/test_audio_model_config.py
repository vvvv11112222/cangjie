from dataclasses import replace
import pytest

from workers.audio.handler import audio_analysis
from workers.common.config import Settings
from workers.common.contracts import WorkerError


def test_cloud_model_must_match_startup_config_unless_claim_mode_is_explicit(settings, tmp_path):
    claim = {'execution': {'asr_model': {'name': 'go-configured-asr', 'revision': 'v2'}}, 'duration_ms': 0}
    fixed = replace(settings, asr_model_name='old-model', asr_model_revision='v1')
    with pytest.raises(WorkerError, match='ASR 模型与固定任务配置不一致'):
        audio_analysis(claim, tmp_path, fixed, None, None)
    # Opt-in skips only the startup model match. Timeline validation still runs.
    with pytest.raises(WorkerError, match='音频阶段需要原录像时长'):
        audio_analysis(claim, tmp_path, replace(fixed, asr_model_from_claim=True), None, None)


def test_invalid_claim_model_flag_is_rejected(monkeypatch):
    monkeypatch.setenv('ASR_MODEL_FROM_CLAIM', 'yes')
    with pytest.raises(ValueError, match='ASR_MODEL_FROM_CLAIM'):
        Settings.from_env()
