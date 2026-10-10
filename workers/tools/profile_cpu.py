"""Bounded CPU-only diagnosis; does not call Go or change runtime configuration.

Reports real model providers, model loading and per-model calls separately.
Classroom media and detailed reports remain in a user-selected local directory.
"""
import argparse
from collections import defaultdict
import ctypes
import json
import os
from pathlib import Path
import platform
import time


def available_memory_mb():
    if os.name != "nt":
        return None
    class MemoryStatus(ctypes.Structure):
        _fields_ = [("length", ctypes.c_ulong), ("load", ctypes.c_ulong),
                    ("total", ctypes.c_ulonglong), ("available", ctypes.c_ulonglong),
                    ("page_total", ctypes.c_ulonglong), ("page_available", ctypes.c_ulonglong),
                    ("virtual_total", ctypes.c_ulonglong), ("virtual_available", ctypes.c_ulonglong),
                    ("extended", ctypes.c_ulonglong)]
    status = MemoryStatus()
    status.length = ctypes.sizeof(status)
    if not ctypes.windll.kernel32.GlobalMemoryStatusEx(ctypes.byref(status)):
        raise ctypes.WinError()
    return round(status.available / 1024**2)


def main():
    parser = argparse.ArgumentParser(description="短片段 CPU 耗时诊断；绝不启用显卡")
    parser.add_argument("source", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--start-s", type=float, default=0)
    parser.add_argument("--duration-s", type=float, default=1)
    parser.add_argument("--deadline-s", type=float, default=180)
    parser.add_argument("--threads", type=int, choices=(1, 2, 4), default=2)
    args = parser.parse_args()
    if not 0 < args.duration_s <= 4 or not 0 < args.deadline_s <= 240:
        parser.error("诊断限 4 秒录像，处理期限最多 240 秒")
    if args.output.exists():
        parser.error("请使用新的输出目录，避免覆盖结果")
    memory = available_memory_mb()
    if memory is not None and memory < 2048:
        raise SystemExit("空闲内存不足 2 GB，本次不加载模型")

    # Set before importing the model module so inherited GPU flags cannot win.
    runtime = Path(__file__).resolve().parents[1] / "config/runtime.safe-cpu.json"
    os.environ.update(VISION_RUNTIME="cpu", VISION_RUNTIME_CONFIG=str(runtime),
                      VISION_BATCH_SIZE="1", VISION_FIXED_BATCH="0", VISION_PREFETCH="0",
                      VISION_GPU_DETECTION="0", VISION_GPU_YOLOX="0", VISION_GPU_THREADS="1",
                      VISION_CV_THREADS="2", VISION_DECODED_POSES="0", VISION_TF32="0",
                      VISION_CPU_THREADS=str(args.threads))
    from workers.vision_core import models
    from workers.vision_core.config import AnalysisConfig
    from workers.vision_core.media import inspect_media
    from workers.video.local import analyze_local

    # analyze_local prepares the whole file, so bound input length as well.
    if inspect_media(args.source)["duration_ms"] > 31000:
        raise SystemExit("请先截取最多 31 秒的诊断录像，避免转码整节课")

    loads, calls = [], defaultdict(lambda: {"calls": 0, "seconds": 0.0})
    original_session = models.session
    started = time.perf_counter()
    deadline = started + args.deadline_s

    def check_budget():
        if time.perf_counter() >= deadline:
            raise TimeoutError("短片段诊断达到本地处理期限")
        free = available_memory_mb()
        if free is not None and free < 768:
            raise MemoryError("空闲内存低于 768 MB，停止诊断")

    class TimedSession:
        def __init__(self, session, name):
            self._session, self.name = session, name

        def __getattr__(self, key):
            return getattr(self._session, key)

        def run(self, outputs, feeds, *arguments, **keywords):
            check_budget()
            then = time.perf_counter()
            try:
                return self._session.run(outputs, feeds, *arguments, **keywords)
            finally:
                calls[self.name]["calls"] += 1
                calls[self.name]["seconds"] += time.perf_counter() - then

    def measured_session(path, *arguments, **keywords):
        check_budget()
        then = time.perf_counter()
        session = original_session(path, *arguments, **keywords)
        provider = session.get_providers()[0]
        if provider != "CPUExecutionProvider":
            raise RuntimeError("诊断拒绝非 CPU 计算设备")
        options = session.get_session_options()
        loads.append({"model": path.name, "seconds": round(time.perf_counter() - then, 4),
                      "provider": provider, "threads": options.intra_op_num_threads,
                      "free_memory_mb_after_load": available_memory_mb()})
        print("已加载 " + path.name + "，实际设备 CPU", flush=True)
        return TimedSession(session, path.name)

    models.session = measured_session

    class Deadline:
        def is_set(self):
            check_budget()
            return False

    last_stage = None
    def progress(**values):
        nonlocal last_stage
        check_budget()
        if values.get("stage") != last_stage or values.get("sample_count"):
            print(values.get("message", ""), flush=True)
        last_stage = values.get("stage")

    try:
        report = analyze_local(args.source, args.output,
                               AnalysisConfig(start_s=args.start_s, duration_s=args.duration_s),
                               cancel=Deadline(), progress=progress)
        timing = report["provenance"]["team_execution_timings"]
        result = {"schema_version": "cpu-profile-v1", "engine": "cpu",
                  "platform": platform.platform(), "logical_cpus": os.cpu_count(),
                  "free_memory_mb_before": memory,
                  "source_sha256": report["metadata"]["sha256"],
                  "model_manifest": {k: v["sha256"] for k, v in report["provenance"]["weights"].items()},
                  "configuration": report["configuration"], "samples": report["statistics"]["sample_count"],
                  "wall_seconds": round(time.perf_counter() - started, 4),
                  "timings": timing, "stages": report["statistics"]["timing_seconds"],
                  "model_loads": loads,
                  "model_inference": {name: {"calls": count["calls"], "seconds": round(count["seconds"], 4)}
                                      for name, count in calls.items()},
                  "pose_crops": report["provenance"]["pose_execution_metrics"],
                  "acceptance_passed": False,
                  "limitations": ["只诊断短片段，不代表整课速度或独立人工质量验收。",
                                  "处理期限和内存检查在模型调用之间生效，不能打断正在运行的单次 ONNX 调用。"]}
        (args.output / "profile.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
        print(json.dumps(result, ensure_ascii=False), flush=True)
    finally:
        models.session = original_session


if __name__ == "__main__":
    main()
