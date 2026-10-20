"""Build and smoke-test an isolated demo container without .env or real media.

Requires a running Docker daemon. Keeps result logs under bin/verification/.
Never starts compose or mounts the application's existing data directory.
"""
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--goproxy", help="optional Docker Go module proxy build argument")
    args = parser.parse_args()
    if not shutil.which("docker"):
        parser.exit(2, "Docker unavailable; container runtime acceptance was not performed.\n")
    subprocess.run(["docker", "info"], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    parent = ROOT / "bin/verification"
    parent.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="docker-smoke-", dir=parent))
    name = "cangjie-demo-smoke-" + uuid.uuid4().hex[:12]
    tag = name + ":test"
    result = {"status": "failed", "real_model_calls": 0, "real_classroom_acceptance": False}
    with (work / "commands.log").open("w", encoding="utf-8") as log:
        def run(*command, capture=False):
            p = subprocess.run(["docker", *command], cwd=ROOT, stdout=subprocess.PIPE if capture else log,
                               stderr=log, text=True, encoding="utf-8", timeout=1200)
            if p.returncode:
                raise RuntimeError("Docker command failed; inspect local commands.log")
            return p.stdout

        try:
            build = ["build", "--tag", tag]
            if args.goproxy:
                build.extend(["--build-arg", "GOPROXY=" + args.goproxy])
            run(*build, ".")
            run("run", "--detach", "--name", name, "--publish", "127.0.0.1::8090",
                "--tmpfs", "/app/data:rw,uid=10001,gid=10001,mode=0700",
                "--env", "DASHSCOPE_API_KEY=", "--env", "BAILIAN_BASE_URL=",
                "--env", "BAILIAN_REPORT_AUDIT=0", tag)
            state = json.loads(run("inspect", name, capture=True))[0]
            port = state["NetworkSettings"]["Ports"]["8090/tcp"][0]["HostPort"]
            base = "http://127.0.0.1:" + port
            health = None
            for _ in range(60):
                try:
                    with urllib.request.urlopen(base + "/api/health", timeout=2) as response:
                        health = json.load(response)
                    break
                except OSError:
                    time.sleep(.25)
            if not health or health.get("status") != "ok" or health.get("analysis_version") != "six-dim-coaching-v8":
                raise RuntimeError("Container health/version check failed")
            for route in ("/", "/report.css", "/analysis.js", "/events.js", "/jobs.js", "/transcript.js", "/recovery.js"):
                with urllib.request.urlopen(base + route, timeout=3) as response:
                    if response.status != 200 or len(response.read()) < 100:
                        raise RuntimeError("Embedded web asset unavailable")
            with urllib.request.urlopen(base + "/api/jobs", timeout=3) as response:
                if json.load(response).get("jobs") != []:
                    raise RuntimeError("Container store not isolated")
            if run("exec", name, "id", "-u", capture=True).strip() != "10001":
                raise RuntimeError("Unexpected container user")
            run("exec", name, "ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i",
                "sine=frequency=440:duration=1", "-ar", "16000", "-ac", "1", "/tmp/smoke.wav")
            probe = json.loads(run("exec", name, "ffprobe", "-v", "error", "-show_format", "-show_streams",
                                   "-of", "json", "/tmp/smoke.wav", capture=True))
            if probe["streams"][0]["sample_rate"] != "16000" or probe["streams"][0]["channels"] != 1 or abs(float(probe["format"]["duration"]) - 1) > .01:
                raise RuntimeError("Container media processing check failed")
            run("stop", "--time", "15", name)
            state = json.loads(run("inspect", name, capture=True))[0]
            if state["State"]["ExitCode"] != 0:
                raise RuntimeError("Container did not exit cleanly on SIGTERM")
            result.update(status="passed", image_platform=run("image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", tag, capture=True).strip(),
                          checks=["build", "isolated-start", "non-root", "health", "web-assets", "empty-store", "ffmpeg", "ffprobe", "sigterm"])
        finally:
            subprocess.run(["docker", "logs", name], stdout=log, stderr=log, timeout=30)
            subprocess.run(["docker", "rm", "--force", name], stdout=log, stderr=log, timeout=30)
            subprocess.run(["docker", "image", "rm", tag], stdout=log, stderr=log, timeout=30)
            (work / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({**result, "work": str(work)}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
