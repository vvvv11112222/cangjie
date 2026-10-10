"""Lease ownership, hard deadlines and isolated temporary storage."""
from datetime import datetime
import copy
import multiprocessing as mp
from pathlib import Path
import shutil
import time
from uuid import UUID

from .contracts import LeaseLost, WorkerError, parameter_digest, validate


def epoch(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


class Guard:
    def __init__(self, claim, *, event=None, expires=None, progress=None, timeout=None):
        context = mp.get_context("spawn")
        self.event = event if event is not None else context.Event()
        self.expires = expires if expires is not None else context.Value("d", epoch(claim["lease_expires_at"]))
        self.progress = progress if progress is not None else context.Value("i", 0)
        self.deadline = epoch(claim["execution_deadline_at"])
        self.local_deadline = time.time() + timeout if timeout else self.deadline

    def check_lease(self):
        if self.event.is_set() or time.time() >= min(self.expires.value, self.deadline):
            self.event.set()
            raise LeaseLost("任务已取消、租约到期或超过执行截止时间")

    def check(self):
        self.check_lease()
        if time.time() >= self.local_deadline:
            raise WorkerError("PROCESSING_TIMEOUT", "媒体阶段超过本地执行时限", True)

    def for_reporting(self):
        # Share lease/cancellation state, but do not apply the processing budget to fail().
        guard = copy.copy(self)
        guard.local_deadline = self.deadline
        return guard

    def is_set(self):
        try:
            self.check()
        except LeaseLost:
            return True
        return False

    def wait(self, seconds):
        end = time.monotonic() + max(0, seconds)
        while True:
            self.check()
            remaining = end - time.monotonic()
            if remaining <= 0:
                return
            # Renewal can extend expires while sleeping; still honor the entire Retry-After.
            live = min(self.expires.value, self.deadline, self.local_deadline) - time.time()
            self.event.wait(min(remaining, max(0, live)))

    def update(self, **values):
        self.check()
        self.progress.value = max(self.progress.value, min(99, int(values.get("progress", 0))))


def validate_claim(claim, settings):
    from workers.vision_core.media import digest_file, ffmpeg_executable
    validate(claim, "Claim")
    expected = claim["execution"]
    if (expected["processor_version"] != settings.processor_version or
        expected["ffmpeg_build_sha256"] != settings.ffmpeg_sha256 or
        settings.ffmpeg_sha256 != digest_file(ffmpeg_executable()) or
        expected["parameters_sha256"] != parameter_digest(claim["parameters"])):
        raise WorkerError("CONFIG_MISMATCH", "任务固定的处理器、FFmpeg 或参数摘要与当前 Worker 不一致")
    if claim["stage"] not in settings.capabilities:
        raise WorkerError("CONFIG_MISMATCH", "本 Worker 未注册该处理阶段")
    if claim["input_url"] != f"/internal/v1/jobs/{claim['job_id']}/input":
        raise WorkerError("INVALID_RESULT", "任务媒体地址与任务编号不一致")
    if claim["stage"] == "video_analysis" and claim["duration_ms"] is None:
        raise WorkerError("CONFIG_MISMATCH", "视频阶段需要 probe 已确定的录像时长")


def scratch_path(root, claim):
    root = Path(root).resolve()
    job, lease = str(UUID(claim["job_id"])), str(UUID(claim["lease_token"]))
    target = (root / job / lease).resolve()
    if not target.is_relative_to(root) or target == root:
        raise ValueError("临时目录超出 Worker 根目录")
    return target


def cleanup(root, directory):
    root, directory = Path(root).resolve(), Path(directory).resolve()
    if directory == root or not directory.is_relative_to(root):
        raise ValueError("拒绝清理 Worker 根目录以外的路径")
    if directory.exists():
        shutil.rmtree(directory)


def cleanup_expired(root):
    """Only our UUID/UUID directories with an expired lease marker are eligible."""
    root = Path(root).resolve()
    if not root.exists():
        return
    for marker in root.glob("*/*/lease.deadline"):
        try:
            UUID(marker.parent.name)
            UUID(marker.parent.parent.name)
            if float(marker.read_text(encoding="utf-8")) <= time.time():
                cleanup(root, marker.parent)
        except (ValueError, OSError):
            continue
