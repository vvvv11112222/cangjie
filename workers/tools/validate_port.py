"""Compare the portable engine against an existing local vision-lab result."""
import argparse
import json
from pathlib import Path
import time
from uuid import uuid4

from workers.video.local import analyze_local


def compare(before, after):
    def observations(report):
        return {(p["track_id"], o["timestamp_ms"]): o for p in report["persons"] for o in p["observations"]}
    a, b = observations(before), observations(after)
    differing = 0
    for key in a.keys() | b.keys():
        if key not in a or key not in b:
            differing += 1
            continue
        for field in ("bbox", "frame_index", "seat_id", "behaviors", "pose_available", "standing"):
            if field == "bbox":
                if max(abs(x - y) for x, y in zip(a[key][field], b[key][field])) <= .001:
                    continue
            if a[key].get(field) != b[key].get(field):
                differing += 1
                break
    def events(report):
        return [{k: event.get(k) for k in ("event_type", "track_id", "start_ms", "end_ms", "duration_ms")}
                for event in report["events"]]
    fields = ("hand_evaluable", "head_evaluable", "pose_instances", "person_instances", "person_track_count",
              "seat_count", "pose_crop_retries", "pose_identity_rejected", "candidate_counts")
    stats_equal = all(before["statistics"].get(k) == after["statistics"].get(k) for k in fields)
    return {"observations_before": len(a), "observations_after": len(b),
            "differing_observations": differing, "counts_equal": before["counts"] == after["counts"],
            "events_equal": events(before) == events(after), "statistics_equal": stats_equal,
            "equivalent": differing == 0 and before["counts"] == after["counts"] and
                          events(before) == events(after) and stats_equal}


def main():
    parser = argparse.ArgumentParser(description="原实验版与团队版本结果对照；原目录只读")
    parser.add_argument("--lab-root", type=Path, required=True)
    parser.add_argument("--job", action="append", required=True)
    parser.add_argument("--output", type=Path, default=Path("var/validation") / str(uuid4()))
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    comparisons = []
    for job_id in args.job:
        original = (args.lab_root / "data" / job_id).resolve()
        if not original.is_relative_to((args.lab_root / "data").resolve()):
            raise ValueError("任务路径超出原实验目录")
        job = json.loads((original / "job.json").read_text(encoding="utf-8"))
        baseline = json.loads((original / "report.json").read_text(encoding="utf-8"))
        started = time.perf_counter()
        report = analyze_local(original / "source.bin", args.output / job_id, job["configuration"],
                               progress=lambda **v: print(v["message"], flush=True) if v.get("sample_count", 0) % 20 == 0 else None)
        result = {"source_sha256": job["metadata"]["sha256"],
                  "wall_seconds": round(time.perf_counter() - started, 3),
                  "analysis_seconds": report["statistics"]["elapsed_seconds"],
                  "provider": report["provenance"]["provider"],
                  "comparison": compare(baseline, report)}
        comparisons.append(result)
        (args.output / "comparison.json").write_text(json.dumps(comparisons, ensure_ascii=False, indent=2), encoding="utf-8")
        print(json.dumps(result, ensure_ascii=False), flush=True)
    if not all(result["comparison"]["equivalent"] for result in comparisons):
        raise SystemExit("迁移前后结果有差异，请检查 comparison.json")


if __name__ == "__main__":
    main()
