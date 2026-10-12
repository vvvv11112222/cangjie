"""Bridge for the real Go/PostgreSQL fixture; no simulated API or direct SQL access."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time

from workers import PROCESSOR_VERSION
from workers.common.config import Settings
from workers.common.runner import Runner
from workers.vision_core.media import digest_file, ffmpeg_executable, inspect_media


def prepare(output, odd):
    output.mkdir(parents=True, exist_ok=False)
    video = output / "source.mp4"
    dimensions = "161x120" if odd else "320x240"
    subprocess.run([ffmpeg_executable(), "-hide_banner", "-loglevel", "error", "-f", "lavfi",
                    "-i", f"testsrc=size={dimensions}:rate=12:duration=4", "-an", "-c:v", "libx264",
                    "-pix_fmt", "yuv444p" if odd else "yuv420p", str(video)], check=True, timeout=30)
    metadata = inspect_media(video)
    return {"video": str(video.resolve()), "duration_ms": metadata["duration_ms"],
            "processor_version": PROCESSOR_VERSION, "ffmpeg_sha256": digest_file(ffmpeg_executable()),
            "source_sha256": metadata["sha256"], "width": metadata["width"], "height": metadata["height"]}


def work(api_base, expected_stage, expected_run, scratch):
    # Credentials stay in the child environment, never in CLI arguments or reports.
    settings = Settings(api_base, "worker-01", os.environ["WORKER_TOKEN"], scratch.resolve(),
                        PROCESSOR_VERSION, digest_file(ffmpeg_executable()), heartbeat_seconds=.25,
                        max_artifact_bytes=10 * 1024**2)
    runner = Runner(settings)
    started = time.perf_counter()
    try:
        claim = runner.transport.claim()
        if claim is None or claim["stage"] != expected_stage or claim["run_id"] != expected_run:
            raise RuntimeError("Go did not allocate the expected stage/run")
        outcome = runner.run_claim(claim)
        if outcome != "succeeded":
            raise RuntimeError(f"Real Go stage {expected_stage} returned {outcome}")
        return {"job_id": claim["job_id"], "stage": expected_stage, "outcome": outcome,
                "wall_seconds": round(time.perf_counter() - started, 3), "real_go_integration": True}
    finally:
        runner.transport.close()


def main():
    parser = argparse.ArgumentParser(description="Real Go/PostgreSQL integration bridge")
    sub = parser.add_subparsers(dest="command", required=True)
    generation = sub.add_parser("prepare")
    generation.add_argument("--output", type=Path, required=True)
    generation.add_argument("--odd", action="store_true")
    worker = sub.add_parser("work")
    worker.add_argument("--api-base", required=True)
    worker.add_argument("--stage", choices=("probe", "video_analysis"), required=True)
    worker.add_argument("--run-id", required=True)
    worker.add_argument("--scratch", type=Path, required=True)
    args = parser.parse_args()
    result = (prepare(args.output, args.odd) if args.command == "prepare" else
              work(args.api_base, args.stage, args.run_id, args.scratch))
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    main()
