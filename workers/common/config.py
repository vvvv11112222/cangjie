from dataclasses import dataclass
import os
from pathlib import Path
from urllib.parse import urlsplit

from workers import PROCESSOR_VERSION
from workers.vision_core.media import digest_file, ffmpeg_executable


@dataclass(frozen=True)
class Settings:
    api_base: str
    worker_id: str
    token: str
    temp_root: Path
    processor_version: str
    ffmpeg_sha256: str
    capabilities: tuple = ("probe", "video_analysis")
    heartbeat_seconds: float = 30
    max_input_bytes: int = 4 * 1024**3
    max_artifact_bytes: int = 4 * 1024**3
    max_duration_ms: int = 7200000
    max_height: int = 1080
    probe_timeout: int = 1800
    video_timeout: int = 3600
    asr_timeout: int = 14400
    asr_planner: str = ''
    asr_model_name: str = ''
    asr_model_revision: str = ''
    asr_model_from_claim: bool = False
    lease_seconds: int = 120
    vision_enabled: bool = False
    cleanup_enabled: bool = False
    cleanup_contract_path: Path | None = None

    def __post_init__(self):
        url = urlsplit(self.api_base)
        if (url.scheme not in ("http", "https") or not url.netloc or url.username or
                url.password or url.query or url.fragment or url.path != "/internal/v1"):
            raise ValueError("WORKER_API_BASE 必须是内部 Go API 的 /internal/v1 地址")
        if not self.token or not self.worker_id or self.processor_version != PROCESSOR_VERSION:
            raise ValueError("需要 Worker 凭据及与代码一致的 processor_version")
        if not self.capabilities or not set(self.capabilities) <= {"probe", "video_analysis", "audio_analysis"}:
            raise ValueError("Worker 只注册 probe、video_analysis、audio_analysis")
        if 'audio_analysis' in self.capabilities and (not Path(self.asr_planner).is_file() or not self.asr_model_name or not self.asr_model_revision or self.asr_timeout < self.lease_seconds):
            raise ValueError('音频能力要求 ASR_PLANNER、模型名称/修订和有效时限')
        if (not 0 < self.heartbeat_seconds < self.lease_seconds or self.max_input_bytes <= 0 or
            self.max_artifact_bytes <= 0 or self.max_duration_ms <= 0 or self.max_height <= 0 or
            min(self.probe_timeout, self.video_timeout) < self.lease_seconds):
            raise ValueError("心跳及文件上限必须为正数")
        if len(self.ffmpeg_sha256) != 64 or self.ffmpeg_sha256 != digest_file(ffmpeg_executable()):
            raise ValueError("FFMPEG_BUILD_SHA256 与实际二进制不一致；先运行 runtime-info")
        if type(self.vision_enabled) is not bool:
            raise ValueError("vision_enabled 必须是布尔值")
        if type(self.asr_model_from_claim) is not bool:
            raise ValueError('asr_model_from_claim 必须是布尔值')
        if type(self.cleanup_enabled) is not bool:
            raise ValueError("cleanup_enabled 必须是布尔值")
        if self.cleanup_enabled:
            from .deletion import cleanup_schema
            cleanup_schema(self)

    @classmethod
    def from_env(cls):
        if os.environ.get("WORKER_CONCURRENCY", "1") != "1":
            raise ValueError("本视频入口一次执行一个任务；可使用不同 worker_id 启动多个进程")
        vision_flag = os.environ.get("VISION_ANALYSIS_ENABLED", "0")
        if vision_flag not in ("0", "1"):
            raise ValueError("VISION_ANALYSIS_ENABLED 必须为 0 或 1")
        cleanup_flag = os.environ.get("WORKER_CLEANUP_ENABLED", "0")
        if cleanup_flag not in ("0", "1"):
            raise ValueError("WORKER_CLEANUP_ENABLED 必须为 0 或 1")
        model_flag = os.environ.get('ASR_MODEL_FROM_CLAIM', '0')
        if model_flag not in ('0', '1'):
            raise ValueError('ASR_MODEL_FROM_CLAIM 必须为 0 或 1')
        return cls(
            api_base=os.environ.get("WORKER_API_BASE", "http://127.0.0.1:8080/internal/v1").rstrip("/"),
            worker_id=os.environ.get("WORKER_ID", "worker-01"), token=os.environ.get("WORKER_TOKEN", ""),
            temp_root=Path(os.environ.get("WORKER_TEMP_ROOT", "var/worker-tmp")).resolve(),
            processor_version=os.environ.get("WORKER_PROCESSOR_VERSION", ""),
            ffmpeg_sha256=os.environ.get("FFMPEG_BUILD_SHA256", ""),
            capabilities=tuple(os.environ.get("WORKER_CAPABILITIES", "probe,video_analysis").split(",")),
            heartbeat_seconds=float(os.environ.get("JOB_HEARTBEAT_SECONDS", "30")),
            max_input_bytes=int(os.environ.get("MAX_UPLOAD_BYTES", str(4 * 1024**3))),
            max_artifact_bytes=int(os.environ.get("MAX_ARTIFACT_BYTES", str(4 * 1024**3))),
            max_duration_ms=int(os.environ.get("MAX_MEDIA_DURATION_MS", "7200000")),
            max_height=int(os.environ.get("MAX_VIDEO_HEIGHT", "1080")),
            probe_timeout=int(os.environ.get("PROBE_TIMEOUT_SECONDS", "1800")),
            video_timeout=int(os.environ.get("VIDEO_TIMEOUT_SECONDS", "3600")),
            asr_timeout=int(os.environ.get('ASR_TIMEOUT_SECONDS', '14400')),
            asr_planner=os.environ.get('ASR_PLANNER', ''),
            asr_model_name=os.environ.get('ASR_MODEL_NAME', ''),
            asr_model_revision=os.environ.get('ASR_MODEL_REVISION', ''),
            asr_model_from_claim=model_flag == '1',
            lease_seconds=int(os.environ.get("JOB_LEASE_SECONDS", "120")),
            vision_enabled=vision_flag == "1",
            cleanup_enabled=cleanup_flag == "1",
            cleanup_contract_path=(Path(os.environ["WORKER_CLEANUP_CONTRACT_PATH"]).resolve()
                                   if os.environ.get("WORKER_CLEANUP_CONTRACT_PATH") else None),
        )
