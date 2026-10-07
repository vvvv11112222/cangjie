"""Same inputs/settings for legacy and team, in separate sequential GPU processes.

The old web demonstration must be idle and its model cache released first.
The tool refuses the known insufficient-free-memory condition instead of producing
a misleading speed result. No baseline code or data is modified.
"""
import argparse
import importlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import threading
import time


def main():
    parser = argparse.ArgumentParser(description="相同模型、参数、采样画面的原版/团队版速度验收")
    parser.add_argument("--implementation", choices=("baseline", "team"), required=True)
    parser.add_argument("--lab-root", type=Path, required=True)
    parser.add_argument("--job", action="append", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists():
        raise ValueError("请使用新的输出目录，避免覆盖已有测试")
    # Do not run a second GPU model instance while another program fills VRAM.
    if os.environ.get("VISION_RUNTIME", "directml") != "cpu" and shutil.which("nvidia-smi"):
        measured = subprocess.run(["nvidia-smi", "--query-gpu=memory.free", "--format=csv,noheader,nounits"],
                                  capture_output=True, text=True, check=True, timeout=10)
        if max(int(v.strip()) for v in measured.stdout.splitlines()) < 3072:
            raise SystemExit("显卡空闲显存不足 3 GB；请先停止另一份视觉模型进程，再验收速度。")
    if args.implementation == "baseline":
        sys.path.insert(0, str(args.lab_root.resolve()))
        prefix = "lab"
    else:
        prefix = "workers.vision_core"
    analyze = importlib.import_module(prefix + ".analysis").analyze
    Config = importlib.import_module(prefix + ".config").AnalysisConfig
    args.output.mkdir(parents=True)
    measurements = []
    for job_id in args.job:
        original = (args.lab_root / "data" / job_id).resolve()
        if not original.is_relative_to((args.lab_root / "data").resolve()):
            raise ValueError("任务路径超出原实验目录")
        job = json.loads((original / "job.json").read_text(encoding="utf-8"))
        directory = args.output / f"{len(measurements):02d}-{job_id}"
        directory.mkdir()
        try:
            os.link(original / "playback.mp4", directory / "playback.mp4")
        except OSError:
            shutil.copyfile(original / "playback.mp4", directory / "playback.mp4")
        shutil.copyfile(original / "proxy-info.json", directory / "proxy-info.json")
        model_start = None
        model_seconds = 0
        def progress(**values):
            nonlocal model_start, model_seconds
            if values.get("stage") == "loading_models":
                model_start = time.perf_counter()
            elif values.get("stage") == "analyzing" and model_start is not None:
                model_seconds = time.perf_counter() - model_start
                model_start = None
            if values.get("sample_count", 0) % 40 == 0:
                print(values.get("message", ""), flush=True)
        started = time.perf_counter()
        report = analyze(job_id, directory, job["metadata"], Config(**job["configuration"]), threading.Event(), progress)
        measurement = {"job_id": job_id, "implementation": args.implementation,
                       "source_sha256": job["metadata"]["sha256"], "configuration": job["configuration"],
                       "wall_seconds": round(time.perf_counter() - started, 3),
                       "model_initialization_seconds": round(model_seconds, 3),
                       "processing_seconds": round(report["statistics"]["elapsed_seconds"] - model_seconds, 3),
                       "provider": report["provenance"]["provider"], "samples": report["statistics"]["sample_count"]}
        (directory / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        measurements.append(measurement)
        (args.output / "timing.json").write_text(json.dumps(measurements, ensure_ascii=False, indent=2), encoding="utf-8")
        print(json.dumps(measurement, ensure_ascii=False), flush=True)


if __name__ == "__main__":
    main()
