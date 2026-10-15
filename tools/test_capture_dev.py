"""Lifecycle safety checks for the Windows developer launcher; no application DB required."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import capture_dev as dev


@unittest.skipUnless(os.name == "nt", "Windows launcher")
class LifecycleTests(unittest.TestCase):
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
