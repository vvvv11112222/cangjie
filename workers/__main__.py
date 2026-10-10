import argparse
import json
import logging
from pathlib import Path
import shutil

from workers import PROCESSOR_VERSION
from workers.vision_core.media import digest_file, ffmpeg_executable


def main():
    parser = argparse.ArgumentParser(description="团队视频 Worker 与本地视觉验收入口")
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("runtime-info", help="查看处理器版本和实际 FFmpeg 摘要")
    run = commands.add_parser("run", help="领取 Go 正式任务（需要内部接口已实现）")
    run.add_argument("--once", action="store_true")
    models = commands.add_parser("prepare-models", help="从已下载目录复制并校验固定模型")
    models.add_argument("--source", type=Path, required=True)
    models.add_argument("--output", type=Path, default=Path("var/vision-models"))
    local = commands.add_parser("analyze-local", help="本地 P1 行为分析，输出视觉观察报告")
    local.add_argument("video", type=Path)
    local.add_argument("--output", type=Path, required=True)
    local.add_argument("--config", type=Path, help="AnalysisConfig JSON；未提供时全视频、2 FPS")
    args = parser.parse_args()
    if args.command == "runtime-info":
        print(json.dumps({"processor_version": PROCESSOR_VERSION, "ffmpeg_binary": ffmpeg_executable(),
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
    elif args.command == "analyze-local":
        from workers.video.local import analyze_local
        config = json.loads(args.config.read_text(encoding="utf-8")) if args.config else None
        def progress(**values):
            if values.get("sample_count", 0) % 20 == 0:
                print(values.get("message", ""), flush=True)
        report = analyze_local(args.video, args.output, config, progress=progress)
        print(json.dumps({"output": str(args.output.resolve()), "statistics": report["statistics"]}, ensure_ascii=False, indent=2))
    elif args.command == "run":
        from workers.common.config import Settings
        from workers.common.runner import Runner
        logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
        try:
            Runner(Settings.from_env()).run(once=args.once)
        except KeyboardInterrupt:
            pass


if __name__ == "__main__":
    main()
