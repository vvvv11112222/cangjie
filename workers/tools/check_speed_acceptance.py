"""Audit two sequential benchmark runs; do not infer human accuracy from parity."""
import argparse
import copy
import json
from pathlib import Path

from workers.tools.validate_port import compare


def result_content(report):
    result = copy.deepcopy(report)
    result.pop("provenance")
    result["statistics"].pop("elapsed_seconds")
    result["statistics"].pop("timing_seconds")
    return result


def execution_signature(report):
    provenance = report["provenance"]
    names = {s["model"] for s in provenance["runtime_status"]["sessions"]}
    fields = ("provider", "detector_provider", "pose_providers", "execution_engine",
              "pose_batch_size", "fixed_pose_batches", "pose_files", "pose_decode_on_graph",
              "yolox_provider", "precision", "detection_pipelined", "rules_version", "head_method")
    return {
        "settings": {k: provenance[k] for k in fields},
        "device": provenance["runtime_status"]["device"],
        "weights": {name: provenance["weights"][name]["sha256"] for name in sorted(names)},
    }


def audit(baseline, team):
    before = json.loads((baseline / "timing.json").read_text(encoding="utf-8"))
    after = json.loads((team / "timing.json").read_text(encoding="utf-8"))
    if not before or len(before) != len(after):
        raise ValueError("两轮必须检查同样的录像，顺序一致")
    cases = []
    for index, (a, b) in enumerate(zip(before, after)):
        if (a["job_id"], a["source_sha256"], a["configuration"], a["samples"]) != (
                b["job_id"], b["source_sha256"], b["configuration"], b["samples"]):
            raise ValueError("录像、参数或检查画面数不同，不能进行公平对比")
        name = f"{index:02d}-{a['job_id']}"
        original = json.loads((baseline / name / "report.json").read_text(encoding="utf-8"))
        migrated = json.loads((team / name / "report.json").read_text(encoding="utf-8"))
        parity = compare(original, migrated)
        parity["all_result_fields_equal"] = result_content(original) == result_content(migrated)
        parity["execution_equal"] = execution_signature(original) == execution_signature(migrated)
        parity["equivalent"] = all((parity["equivalent"], parity["all_result_fields_equal"],
                                     parity["execution_equal"]))
        cases.append({"job_id": a["job_id"], "duration_ms": original["metadata"]["duration_ms"],
                      "samples": a["samples"], "baseline_seconds": a["wall_seconds"],
                      "team_seconds": b["wall_seconds"],
                      "time_ratio": round(b["wall_seconds"] / a["wall_seconds"], 4),
                      "no_slower_in_this_run": b["wall_seconds"] <= a["wall_seconds"],
                      "quality_parity": parity})
    return {"scope": "same decoded input, analysis and evidence writing; excludes upload/transcoding",
            "timing_note": "单轮实测存在系统负载、编译缓存和温度波动；不能推导长期提速百分比。",
            "accuracy_note": "结果一致不等于人工标注准确率合格。",
            "cases": cases,
            "quality_parity_passed": all(c["quality_parity"]["equivalent"] for c in cases),
            "speed_baseline_passed_in_this_run": all(c["no_slower_in_this_run"] for c in cases)}


def main():
    parser = argparse.ArgumentParser(description="同录像、同配置的迁移速度与完整结果验收")
    parser.add_argument("--baseline", type=Path, required=True)
    parser.add_argument("--team", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists():
        raise ValueError("输出文件已经存在，请选择新文件")
    result = audit(args.baseline, args.team)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if not result["quality_parity_passed"] or not result["speed_baseline_passed_in_this_run"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
