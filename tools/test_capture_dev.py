"""Lifecycle safety checks for the Windows developer launcher; no application DB required."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import json
import secrets
import time
import unittest
from unittest.mock import patch

import capture_dev as dev


@unittest.skipUnless(os.name == "nt", "Windows launcher")
class LifecycleTests(unittest.TestCase):
    def test_legacy_worker_with_running_jobs_is_not_terminated(self):
        with patch.object(dev, 'processes', return_value={'postgres': {'pid': 1}, 'worker': {'pid': 2}}), \
                patch.object(dev, 'alive', return_value=True), \
                patch.object(dev, 'psql', side_effect=['0', '1']), patch.object(dev, 'identity') as terminate:
            with self.assertRaisesRegex(RuntimeError, 'Legacy worker has running analysis jobs'):
                dev.stop({})
            terminate.assert_not_called()

    def test_tree_shutdown_failure_retains_worker_record(self):
        record = {'pid': 2, 'worker_job': 'test-job'}
        current = {'worker': record}
        with patch.object(dev, 'processes', return_value=current), patch.object(dev, 'alive', return_value=True), \
                patch.object(dev, 'identity'), patch.object(dev, 'save_json'), patch('capture_worker_job.Job') as job:
            job.return_value.terminate_and_wait.side_effect = RuntimeError('still active')
            with self.assertRaisesRegex(RuntimeError, 'still active'):
                dev.stop({})
            self.assertEqual(current.get('worker'), record)
            job.return_value.close.assert_called_once()

    def worker_tree(self, directory):
        receipt = Path(directory) / 'pids.json'
        worker_code = (
            "import os, subprocess, sys, json, time; from pathlib import Path; "
            "child=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)'],creationflags=subprocess.CREATE_NO_WINDOW); "
            f"Path({str(receipt)!r}).write_text(json.dumps([os.getpid(),child.pid])); time.sleep(60)"
        )
        job_name = 'Local\\CaptureWorkerTest-' + secrets.token_hex(16)
        supervisor = subprocess.Popen([sys.executable, '-c',
            "import sys; sys.path.insert(0,sys.argv[1]); from capture_worker_job import supervise; raise SystemExit(supervise(sys.argv[2],sys.argv[3]))",
            str(Path(dev.__file__).parent), job_name, worker_code], creationflags=dev.NO_WINDOW)
        deadline = time.monotonic() + 10
        while not receipt.exists() and supervisor.poll() is None and time.monotonic() < deadline:
            time.sleep(.05)
        if not receipt.exists():
            supervisor.kill(); supervisor.wait()
            self.fail('worker tree did not start')
        records = [dev.identity(pid) for pid in json.loads(receipt.read_text())]
        record = dict(dev.identity(supervisor.pid), worker_job=job_name)
        return supervisor, record, records

    def assert_tree_stopped(self, records):
        deadline = time.monotonic() + 10
        while any(dev.alive(record) for record in records) and time.monotonic() < deadline:
            time.sleep(.05)
        self.assertTrue(all(not dev.alive(record) for record in records))

    def test_stop_waits_for_worker_tree_and_leaves_unrelated_process(self):
        from capture_worker_job import Job
        unrelated = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'], creationflags=dev.NO_WINDOW)
        try:
            with tempfile.TemporaryDirectory() as directory:
                process, record, children = self.worker_tree(directory)
                try:
                    with patch.object(dev, 'processes', return_value={'worker': record}), patch.object(dev, 'save_json'):
                        dev.stop({})
                    process.wait(timeout=10)
                    self.assert_tree_stopped(children)
                    self.assertIsNone(unrelated.poll())
                    self.assertFalse(dev.alive(record))
                finally:
                    job = Job(record['worker_job'])
                    try: job.terminate_and_wait()
                    finally: job.close()
                    if process.poll() is None: process.kill()
                    process.wait(timeout=10)
        finally:
            unrelated.kill(); unrelated.wait(timeout=10)

    def test_supervisor_crash_also_terminates_descendants(self):
        from capture_worker_job import Job
        with tempfile.TemporaryDirectory() as directory:
            process, record, children = self.worker_tree(directory)
            try:
                dev.identity(record['pid'], terminate=True, expected=record)
                process.wait(timeout=10)
                self.assert_tree_stopped(children)
            finally:
                job = Job(record['worker_job'])
                try: job.terminate_and_wait()
                finally: job.close()
                if process.poll() is None: process.kill()
                process.wait(timeout=10)

    def test_failed_job_assignment_never_starts_worker_code(self):
        from capture_worker_job import Job, supervise
        with tempfile.TemporaryDirectory() as directory:
            marker = Path(directory) / 'must-not-exist'
            with patch.object(Job, 'assign', side_effect=RuntimeError('assignment failed')):
                with self.assertRaisesRegex(RuntimeError, 'assignment failed'):
                    supervise('Local\\CaptureWorkerTest-' + secrets.token_hex(16), f"from pathlib import Path; Path({str(marker)!r}).write_text('started')")
            self.assertFalse(marker.exists())

    def test_reused_pid_is_not_terminated(self):
        process = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"],
                                   creationflags=dev.NO_WINDOW)
        try:
            record = dev.identity(process.pid)
            self.assertTrue(dev.alive(record))
            stale = dict(record, created=record["created"] - 1)
            self.assertFalse(dev.alive(stale))
            with self.assertRaisesRegex(RuntimeError, "reused PID"):
                dev.identity(process.pid, terminate=True, expected=stale)
            self.assertIsNone(process.poll())
            dev.identity(process.pid, terminate=True, expected=record)
            process.wait(timeout=5)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()

    def test_failed_command_keeps_diagnostics_without_echoing_secret_argument(self):
        with tempfile.TemporaryDirectory() as temporary, patch.object(dev, "STATE", Path(temporary)):
            with self.assertRaises(RuntimeError) as error:
                dev.run([sys.executable, "-c", "import sys; print('diagnostic'); sys.exit(7)", "secret-value"])
            self.assertNotIn("secret-value", str(error.exception))
            self.assertIn("diagnostic", (Path(temporary) / "setup.log").read_text())

    def test_stop_refuses_active_capture_before_terminating_anything(self):
        with patch.object(dev, "processes", return_value={"postgres": {"pid": 1}}), \
                patch.object(dev, "alive", return_value=True), \
                patch.object(dev, "psql", return_value="1"), \
                patch.object(dev, "identity") as terminate:
            with self.assertRaisesRegex(RuntimeError, "active captures"):
                dev.stop({})
            terminate.assert_not_called()

    def test_environment_cannot_inherit_external_db_or_paid_model(self):
        config = dict(ip="192.168.1.10", ports=dev.PORTS,
                      secrets={key: key + "-random" for key in ("database", "worker", "control", "event", "login")},
                      runtime=dict(processor_version="test", ffmpeg_build_sha256="a" * 64, ffmpeg_binary="worker-ffmpeg"),
                      tools=dict(ffmpeg="capture-ffmpeg", ffprobe="capture-ffprobe", node="C:/node/node.exe"))
        with patch.dict(os.environ, dict(DATABASE_URL="external", REPORT_API_KEY="paid-key",
                                        MODEL_MONTHLY_BUDGET="100", MTX_APIADDRESS=":9997")):
            env = dev.environment(config)
        self.assertIn("127.0.0.1:15436/capture_dev", env["DATABASE_URL"])
        self.assertEqual(env["MODEL_MONTHLY_BUDGET"], "0")
        self.assertEqual(env["REPORT_API_KEY"], "")
        self.assertNotIn("MTX_APIADDRESS", env)
        self.assertEqual(env["VISION_FFMPEG_BINARY"], "worker-ffmpeg")
        self.assertNotEqual(env["WORKER_TOKEN"], env["CAPTURE_EVENT_TOKEN"])


if __name__ == "__main__":
    unittest.main()
