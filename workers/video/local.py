"""Local P1 demonstration with the existing algorithms, separate from Go DTOs."""
import json
from pathlib import Path
import threading
import time
from uuid import uuid4


def analyze_local(source, output, config=None, *, cancel=None, progress=None, preview=None):
    from workers.vision_core.analysis import analyze
    from workers.vision_core.config import AnalysisConfig
    from workers.vision_core.media import inspect_media, make_proxy, digest_file
    from workers.vision_core.reporting import markdown_report, html_report, csv_counts, csv_persons

    source, output = Path(source).resolve(), Path(output).resolve()
    if output.exists() and any(output.iterdir()):
        raise ValueError("输出目录已有文件，请选择新的空目录，避免覆盖原结果")
    output.mkdir(parents=True, exist_ok=True)
    cancel = cancel if cancel is not None else threading.Event()
    progress = progress if progress is not None else lambda **values: None
    config = config if config is not None else AnalysisConfig(duration_s=0)
    if not isinstance(config, AnalysisConfig):
        config = AnalysisConfig.model_validate(config)
    metadata = inspect_media(source, max_duration_ms=7_200_000)
    total_started = time.perf_counter()
    progress(stage="preparing", message="准备播放视频", progress=0)
    proxy_info = make_proxy(source, output / "playback.mp4", cancel)
    (output / "proxy-info.json").write_text(json.dumps(proxy_info, ensure_ascii=False), encoding="utf-8")
    preparation_seconds = time.perf_counter() - total_started
    model_started = None
    model_seconds = 0
    def observed_progress(**values):
        nonlocal model_started, model_seconds
        if values.get("stage") == "loading_models":
            model_started = time.perf_counter()
        elif values.get("stage") == "analyzing" and model_started is not None:
            model_seconds = time.perf_counter() - model_started
            model_started = None
        progress(**values)
    report = analyze(str(uuid4()), output, metadata, config, cancel, observed_progress, preview)
    report["provenance"]["team_execution_timings"] = {
        "preparation_seconds": round(preparation_seconds, 3),
        "model_initialization_seconds": round(model_seconds, 3),
        "processing_seconds": round(report["statistics"]["elapsed_seconds"] - model_seconds, 3),
        "wall_seconds_before_export": round(time.perf_counter() - total_started, 3),
    }
    report["provenance"]["team_port"] = {
        "source_version": "vision-lab-0.4.3",
        "source_port_sha256": digest_file(Path(__file__).resolve().parents[1] / "source-port.json"),
        "public_contract": "local P1 only; not a contracts/1.1 StageResult",
    }
    (output / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2, allow_nan=False), encoding="utf-8")
    (output / "report.md").write_text(markdown_report(report), encoding="utf-8")
    (output / "report.html").write_text(html_report(report, output), encoding="utf-8")
    (output / "counts.csv").write_text(csv_counts(report), encoding="utf-8")
    (output / "persons.csv").write_text(csv_persons(report), encoding="utf-8")
    return report
