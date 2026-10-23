"""Reusable stage dispatch. Go owns queue/retry state; this only runs the claimed lease."""
import logging
import os
from contextlib import contextmanager
import multiprocessing as mp
from queue import Empty
import threading
import time

import httpx

from .contracts import LeaseLost, WorkerError, json_bytes, validate_result
from .lifecycle import Guard, cleanup, cleanup_expired, epoch, scratch_path, validate_claim
from .transport import Transport
from .deletion import check_fence, clean_claim, exclusive_root

LOG = logging.getLogger(__name__)


def execute_claim(settings, claim, guard, directory, transport=None, handlers=None, *, report_timeout=True):
    from workers.video.handlers import HANDLERS
    from workers.audio.handler import audio_analysis
    transport = transport or Transport(settings)
    handlers = {**HANDLERS, 'audio_analysis': audio_analysis} if handlers is None else handlers
    created = False
    try:
        validate_claim(claim, settings)
        check_fence(settings.temp_root, claim)
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
        if error.code == "PROCESSING_TIMEOUT" and not report_timeout:
            # The parent owns timeout reporting for spawned workers, preventing duplicate fail calls.
            return "timed_out"
        try:
            # A local processing timeout can fail while the Go lease is still live.
            transport.fail(claim, error, guard.for_reporting())
        except (LeaseLost, WorkerError, httpx.TransportError):
            pass
        LOG.error("job=%s stage=%s error=%s", claim["job_id"], claim["stage"], error.code)
        return "failed"
    finally:
        transport.close()
        if created:
            cleanup(settings.temp_root, directory)


def _child(settings, claim, guard, directory, outcome):
    outcome.put(execute_claim(settings, claim, guard, directory, report_timeout=False))


class Runner:
    def __init__(self, settings):
        self.settings = settings
        self.transport = Transport(settings)
        self._root_mutex = threading.RLock()
        self._root_owned = False

    @contextmanager
    def ownership(self):
        with self._root_mutex:
            if self._root_owned:
                yield
                return
            with exclusive_root(self.settings.temp_root):
                self._root_owned = True
                try:
                    yield
                finally:
                    self._root_owned = False

    def cleanup_once(self):
        if not self.settings.cleanup_enabled:
            raise WorkerError("CONFIG_MISMATCH", "本部署尚未启用后端清理协议")
        with self.ownership():
            claim = self.transport.claim_cleanup()
            if claim is None:
                return None
            try:
                clean_claim(self.settings.temp_root, claim)
            except OSError as error:
                raise WorkerError("CLEANUP_INCOMPLETE", "临时文件暂时无法清理，不回报成功", True) from error
            return self.transport.complete_cleanup(claim)

    def run_claim(self, claim):
        with self.ownership():
            try:
                check_fence(self.settings.temp_root, claim)
            except LeaseLost:
                return "stopped"
            return self._run_claim(claim)

    def _run_claim(self, claim):
        timeout = self.settings.probe_timeout if claim["stage"] == "probe" else self.settings.asr_timeout if claim["stage"] == "audio_analysis" else self.settings.video_timeout
        guard = Guard(claim, timeout=timeout)
        directory = scratch_path(self.settings.temp_root, claim)
        if directory.exists():
            self.transport.fail(claim, WorkerError("PROCESSING_FAILED", "该租约临时目录已存在，拒绝覆盖"), guard)
            return "failed"
        context = mp.get_context("spawn")
        outcome = context.Queue()
        process = context.Process(target=_child, args=(self.settings, claim, guard, directory, outcome))
        try:
            result = self.transport.heartbeat(claim, 0, guard)
            guard.expires.value = epoch(result["lease_expires_at"])
            process.start()
            next_heartbeat = time.monotonic() + self.settings.heartbeat_seconds
            while process.is_alive():
                if guard.progress.value < 100:
                    guard.check()
                if guard.progress.value < 100 and time.monotonic() >= next_heartbeat:
                    heartbeat_delay = self.settings.heartbeat_seconds
                    try:
                        result = self.transport.heartbeat(claim, guard.progress.value, guard)
                        guard.expires.value = epoch(result["lease_expires_at"])
                        # The child writes the immutable hard deadline once. It may clean up
                        # immediately after completion; never rewrite files during renewal.
                    except httpx.TransportError:
                        # Keep the last acknowledged expiry; never assume a renewed lease.
                        LOG.warning("job=%s heartbeat unreachable", claim["job_id"])
                    except WorkerError as error:
                        if not error.retryable or error.code != "WORKER_API_ERROR":
                            raise
                        LOG.warning("job=%s heartbeat temporarily unavailable", claim["job_id"])
                        heartbeat_delay = max(heartbeat_delay, error.retry_after or 0)
                    next_heartbeat = time.monotonic() + heartbeat_delay
                process.join(timeout=0.2)
            process.join()
            if process.exitcode:
                self.transport.fail(claim, WorkerError("PROCESSING_FAILED", "媒体子进程异常退出", True), guard)
                return "failed"
            result = outcome.get(timeout=2)
            if result == "timed_out":
                raise WorkerError("PROCESSING_TIMEOUT", "媒体阶段超过本地执行时限", True)
            return result
        except LeaseLost:
            # Go may commit complete() immediately before rejecting a racing heartbeat.
            # Only a child that received the actual completion ACK can return success.
            # Local expiry or an already signalled cancellation gets no grace here.
            if process.pid is not None and not guard.event.is_set():
                process.join(timeout=2)
                if not process.is_alive() and process.exitcode == 0:
                    try:
                        if outcome.get(timeout=.2) == "succeeded":
                            return "succeeded"
                    except Empty:
                        pass
            return "stopped"
        except WorkerError as error:
            try:
                self.transport.fail(claim, error, guard.for_reporting())
            except (LeaseLost, WorkerError, httpx.TransportError):
                pass
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
        with self.ownership():
            self._run(once=once)

    def _run(self, once=False):
        cleanup_expired(self.settings.temp_root)
        live = None
        if self.settings.vision_enabled and os.environ.get('CAPTURE_LIVE_VISION_ENABLED')=='true':
            from .live import LiveProcessor
            live=LiveProcessor(self.transport)
        try:
            failures = 0
            while True:
                try:
                    # No media child survives run_claim(). Cleanup therefore runs
                    # only after every local writer stops, before the next claim.
                    if self.settings.cleanup_enabled:
                        self.cleanup_once()
                    claim = self.transport.claim()
                except WorkerError as error:
                    if not error.retryable or once:
                        raise
                    failures += 1
                    delay = max(error.retry_after or 0, min(30, 2 ** min(failures - 1, 5)))
                    if error.claim_uncertain:
                        # No recovery endpoint exists in 1.1; do not immediately repeat an uncertain claim.
                        delay = max(delay, self.settings.lease_seconds)
                    LOG.warning("claim temporarily unavailable; poll resumes after %.1f seconds", delay)
                    time.sleep(delay)
                    continue
                failures = 0
                if claim is not None:
                    if live is not None: live.close()  # Never share GPU with an offline analysis child.
                    LOG.info("job=%s stage=%s started", claim["job_id"], claim["stage"])
                    outcome = self.run_claim(claim)
                    LOG.info("job=%s outcome=%s", claim["job_id"], outcome)
                if once:
                    return
                if claim is None:
                    if live is None: time.sleep(5)
                    else:
                        # Go waits up to four seconds for a fresh frame. Offline
                        # work gets priority at the next iteration, without a
                        # high-frequency empty HTTP polling loop.
                        try: live.poll()
                        except WorkerError as error:
                            LOG.warning('live preview unavailable; offline polling continues: %s',error.code)
                        time.sleep(.25)
        finally:
            if live is not None: live.close()
            self.transport.close()
