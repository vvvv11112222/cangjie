"""Real footage P0 check through a clearly labelled local protocol harness."""
import argparse
from dataclasses import replace
import json
from pathlib import Path
import time
from uuid import uuid4

from workers import PROCESSOR_VERSION
from workers.common.config import Settings
from workers.common.runner import Runner
from workers.tests.fake_go import FakeGo, make_claim
from workers.vision_core.media import digest_file, ffmpeg_executable, inspect_media


def main():
    parser = argparse.ArgumentParser(description="视频职责本地验收；Go 接口使用协议模拟服务")
    parser.add_argument("video", type=Path)
    parser.add_argument("--output", type=Path, default=Path("var/p0-validation") / str(uuid4()))
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    metadata = inspect_media(args.video)
    settings = Settings("http://127.0.0.1:8080/internal/v1", "test-worker", "test-token",
                        (args.output / "scratch").resolve(), PROCESSOR_VERSION, digest_file(ffmpeg_executable()),
                        heartbeat_seconds=1)
    measurements = []
    for stage in ("probe", "video_analysis"):
        claim = make_claim(settings, args.video, stage, metadata["duration_ms"], interval=5000, limit=240)
        fake = FakeGo(args.video, claim)
        server = fake.server()
        runner = Runner(replace(settings, api_base=f"http://127.0.0.1:{server.server_port}/internal/v1"))
        started = time.perf_counter()
        try:
            assert runner.transport.claim() == claim
            outcome = runner.run_claim(claim)
            if outcome != "succeeded":
                raise RuntimeError(f"{stage} 失败：{fake.failures}")
            result = json.loads(fake.complete_bytes[-1])
            (args.output / f"{stage}.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
            assets = args.output / stage
            assets.mkdir()
            for asset in fake.assets.values():
                (assets / asset["key"].split("/")[-1]).write_bytes(asset["bytes"])
            measurements.append({"stage": stage, "wall_seconds": round(time.perf_counter() - started, 3),
                                 "artifact_count": len(fake.assets), "outcome": outcome})
        finally:
            runner.transport.close()
            server.shutdown()
            server.server_close()
    summary = {"mode": "real media / synthetic Go protocol harness", "real_go_integration": False,
               "source_sha256": metadata["sha256"], "duration_ms": metadata["duration_ms"], "measurements": measurements}
    (args.output / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
