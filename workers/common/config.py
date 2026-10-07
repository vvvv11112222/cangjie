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
    lease_seconds: int = 120

    def __post_init__(self):
        url = urlsplit(self.api_base)
        if (url.scheme not in ("http", "https") or not url.netloc or url.username or
                url.password or url.query or url.fragment or url.path != "/internal/v1"):
            raise ValueError("WORKER_API_BASE 必须是内部 Go API 的 /internal/v1 地址")
        if not self.token or not self.worker_id or self.processor_version != PROCESSOR_VERSION:
            raise ValueError("需要 Worker 凭据及与代码一致的 processor_version")
        if not self.capabilities or not set(self.capabilities) <= {"probe", "video_analysis"}:
            raise ValueError("当前视频 Worker 只注册 probe、video_analysis")
        if (not 0 < self.heartbeat_seconds < self.lease_seconds or self.max_input_bytes <= 0 or
            self.max_artifact_bytes <= 0 or self.max_duration_ms <= 0 or self.max_height <= 0 or
            min(self.probe_timeout, self.video_timeout) < self.lease_seconds):
            raise ValueError("心跳及文件上限必须为正数")
        if len(self.ffmpeg_sha256) != 64 or self.ffmpeg_sha256 != digest_file(ffmpeg_executable()):
            raise ValueError("FFMPEG_BUILD_SHA256 与实际二进制不一致；先运行 runtime-info")

    @classmethod
    def from_env(cls):
        if os.environ.get("WORKER_CONCURRENCY", "1") != "1":
            raise ValueError("本视频入口一次执行一个任务；可使用不同 worker_id 启动多个进程")
        return cls(
            api_base=os.environ.get("WORKER_API_BASE", "http://127.0.0.1:8080/internal/v1").rstrip("/"),
            worker_id=os.environ.get("WORKER_ID", "video-01"), token=os.environ.get("WORKER_TOKEN", ""),
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
            lease_seconds=int(os.environ.get("JOB_LEASE_SECONDS", "120")),
        )
