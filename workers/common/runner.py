"""Reusable stage dispatch. Go owns queue/retry state; this only runs the claimed lease."""
import logging
import multiprocessing as mp
import time

import httpx

from .contracts import LeaseLost, WorkerError, json_bytes, validate_result
from .lifecycle import Guard, cleanup, cleanup_expired, epoch, scratch_path, validate_claim
from .transport import Transport

LOG = logging.getLogger(__name__)


def execute_claim(settings, claim, guard, directory, transport=None, handlers=None):
    from workers.video.handlers import HANDLERS
    transport = transport or Transport(settings)
    handlers = HANDLERS if handlers is None else handlers
    created = False
    try:
        validate_claim(claim, settings)
        guard.check()
        directory.mkdir(parents=True, exist_ok=False)
        created = True
        (directory / "lease.deadline").write_text(str(guard.deadline), encoding="utf-8")
        transport.download(claim, directory / "input.bin", guard)
        result = handlers[claim["stage"]](claim, directory, settings, transport, guard)
        validate_result(result, claim)
        transport.complete(claim, json_bytes(result), guard)
        guard.progress.value = 100
        return "succeeded"
    except LeaseLost:
        return "stopped"
    except Exception as error:
        if not isinstance(error, WorkerError):
            error = WorkerError("PROCESSING_FAILED", "媒体阶段执行失败；请查看受控本地诊断日志")
        try:
            # A local processing timeout can fail while the Go lease is still live.
            guard.local_deadline = guard.deadline
            guard.event.clear() if error.code == "PROCESSING_TIMEOUT" else None
            transport.fail(claim, error, guard)
        except (LeaseLost, WorkerError, httpx.TransportError):
            pass
        LOG.error("job=%s stage=%s error=%s", claim["job_id"], claim["stage"], error.code)
        return "failed"
    finally:
        transport.close()
        if created:
            cleanup(settings.temp_root, directory)


def _child(settings, claim, guard, directory, outcome):
    outcome.put(execute_claim(settings, claim, guard, directory))


class Runner:
    def __init__(self, settings):
        self.settings = settings
        self.transport = Transport(settings)

    def run_claim(self, claim):
        timeout = self.settings.probe_timeout if claim["stage"] == "probe" else self.settings.video_timeout
        guard = Guard(claim, timeout=timeout)
        directory = scratch_path(self.settings.temp_root, claim)
        if directory.exists():
            self.transport.fail(claim, WorkerError("PROCESSING_FAILED", "该租约临时目录已存在，拒绝覆盖"), guard)
            return "failed"
        context = mp.get_context("spawn")
        outcome = context.Queue()
        process = context.Process(target=_child, args=(self.settings, claim, guard, directory, outcome))
        try:
            result = self.transport.heartbeat(claim, 0)
            guard.expires.value = epoch(result["lease_expires_at"])
            process.start()
            next_heartbeat = time.monotonic() + self.settings.heartbeat_seconds
            while process.is_alive():
                if guard.progress.value < 100:
                    guard.check()
                if guard.progress.value < 100 and time.monotonic() >= next_heartbeat:
                    try:
                        result = self.transport.heartbeat(claim, guard.progress.value)
                        guard.expires.value = epoch(result["lease_expires_at"])
                        (directory / "lease.deadline").write_text(str(guard.deadline), encoding="utf-8") if directory.exists() else None
                    except httpx.TransportError:
                        # Keep the last acknowledged expiry; never assume a renewed lease.
                        LOG.warning("job=%s heartbeat unreachable", claim["job_id"])
                    next_heartbeat = time.monotonic() + self.settings.heartbeat_seconds
                process.join(timeout=0.2)
            process.join()
            if process.exitcode:
                self.transport.fail(claim, WorkerError("PROCESSING_FAILED", "媒体子进程异常退出", True), guard)
                return "failed"
            return outcome.get(timeout=2)
        except LeaseLost:
            return "stopped"
        except WorkerError as error:
            if error.code == "PROCESSING_TIMEOUT":
                guard.local_deadline = guard.deadline
                guard.event.clear()
                self.transport.fail(claim, error, guard)
            return "failed"
        finally:
            guard.event.set()
            if process.pid is not None and process.is_alive():
                process.join(timeout=2)  # Let FFmpeg terminate itself first.
            if process.pid is not None and process.is_alive():
                process.terminate()
                process.join(timeout=5)
            if process.pid is not None and process.is_alive():
                process.kill()
                process.join()
            cleanup(self.settings.temp_root, directory)
            outcome.close()

    def run(self, once=False):
        cleanup_expired(self.settings.temp_root)
        try:
            while True:
                claim = self.transport.claim()
                if claim is not None:
                    LOG.info("job=%s stage=%s started", claim["job_id"], claim["stage"])
                    outcome = self.run_claim(claim)
                    LOG.info("job=%s outcome=%s", claim["job_id"], outcome)
                if once:
                    return
                if claim is None:
                    time.sleep(5)
        finally:
            self.transport.close()
