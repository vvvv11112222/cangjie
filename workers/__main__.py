import argparse
import json
import logging
from pathlib import Path
import shutil

from workers import PROCESSOR_VERSION
from workers.vision_core.media import digest_file, ffmpeg_executable


def main():
    parser = argparse.ArgumentParser(description="观课音视频处理 Worker")
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("runtime-info", help="查看处理器版本和实际 FFmpeg 摘要")
    run = commands.add_parser("run", help="领取后端音视频处理任务")
    run.add_argument("--once", action="store_true")
    commands.add_parser("cleanup-once", help="只领取一项后端删除清理并回报，需要显式启用")
    models = commands.add_parser("prepare-models", help="从已下载目录复制并校验固定模型")
    models.add_argument("--source", type=Path, required=True)
    models.add_argument("--output", type=Path, default=Path("var/vision-models"))
    args = parser.parse_args()
    if args.command == "runtime-info":
        from workers.video.visual import model_identity
        print(json.dumps({"processor_version": PROCESSOR_VERSION, "visual_model": model_identity(), "ffmpeg_binary": ffmpeg_executable(),
                          "ffmpeg_build_sha256": digest_file(ffmpeg_executable())}, ensure_ascii=False, indent=2))
    elif args.command == "prepare-models":
        registry = json.loads((Path(__file__).parent / "config/models.manifest.json").read_text(encoding="utf-8"))
        if args.output.resolve() == args.source.resolve():
            raise ValueError("模型输出目录必须与输入目录不同")
        # Verify all originals before copying anything. Never modify the source folder.
        for name, model in registry.items():
            if digest_file(args.source / name) != model["sha256"]:
                raise ValueError(f"模型摘要不匹配：{name}")
        args.output.mkdir(parents=True, exist_ok=True)
        for name, model in registry.items():
            target = args.output / name
            if target.exists() and digest_file(target) != model["sha256"]:
                raise ValueError(f"输出目录有不同版本的模型：{name}，请换一个目录")
            if not target.exists():
                shutil.copyfile(args.source / name, target)
        (args.output / "manifest.json").write_text(json.dumps(registry, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"已准备 {len(registry)} 个固定模型：{args.output.resolve()}")
    elif args.command in ("run", "cleanup-once"):
        from workers.common.config import Settings
        from workers.common.runner import Runner
        logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
        try:
            runner = Runner(Settings.from_env())
            if args.command == "cleanup-once":
                try:
                    print(json.dumps({"receipt": runner.cleanup_once()}, ensure_ascii=False))
                finally:
                    runner.transport.close()
            else:
                runner.run(once=args.once)
        except KeyboardInterrupt:
            pass


if __name__ == "__main__":
    main()
