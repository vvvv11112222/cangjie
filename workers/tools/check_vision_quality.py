"""Frame-level visual quality measurements against independent manual annotations.

Unknown predictions on labelled positives count as misses, not true negatives.
No acceptance threshold or authenticity claim is inferred by this tool.
"""
import argparse
import hashlib
import json
from pathlib import Path

import numpy as np
from scipy.optimize import linear_sum_assignment

from workers.vision_core.behavior import iou

KINDS = ("hand_raise", "head_down", "leave_seat", "possible_phone")


def measure_frame(labels, predictions, counters):
    if not labels and not predictions:
        return
    pairs = {}
    if labels and predictions:
        overlaps = np.array([[iou(label["bbox"], pred["bbox"]) for pred in predictions] for label in labels])
        for row, column in zip(*linear_sum_assignment(1 - overlaps)):
            if overlaps[row, column] >= .25:
                pairs[int(row)] = int(column)
    used = set(pairs.values())
    for kind, count in counters.items():
        for index, label in enumerate(labels):
            truth = label["behaviors"].get(kind)
            if truth is None:
                continue
            if type(truth) is not bool:
                raise ValueError("人工标签必须为 true、false 或 null")
            observation = predictions[pairs[index]]["behaviors"].get(kind) if index in pairs else None
            count["labelled"] += 1
            count["unknown_or_missed"] += int(observation is None)
            count["tp"] += int(truth is True and observation is True)
            count["fn"] += int(truth is True and observation is not True)
            count["fp"] += int(truth is False and observation is True)
        # This is only valid with full-frame manual annotations (enforced by caller).
        count["fp"] += sum(p["behaviors"].get(kind) is True for i, p in enumerate(predictions) if i not in used)


def evaluate(dataset, base):
    if dataset.get("schema_version") != "visual-eval-v1" or not dataset.get("samples"):
        raise ValueError("需要 visual-eval-v1 格式与非空人工标注样本")
    kinds = dataset.get("event_types", ["hand_raise", "head_down"])
    if not kinds or len(set(kinds)) != len(kinds) or not set(kinds) <= set(KINDS):
        raise ValueError("行为类型无效")
    counters = {kind: dict(tp=0, fp=0, fn=0, labelled=0, unknown_or_missed=0) for kind in kinds}
    sources, frames = set(), 0
    for sample in dataset["samples"]:
        report = json.loads((base / sample["report_path"]).read_text(encoding="utf-8"))
        if report["metadata"]["sha256"] != sample["source_sha256"]:
            raise ValueError("标注样本与检测报告的原录像摘要不同")
        sources.add(sample["source_sha256"])
        observations = {}
        for person in report["persons"]:
            for observation in person["observations"]:
                observations.setdefault(observation["timestamp_ms"], []).append(observation)
        valid_times = {count["timestamp_ms"] for count in report["counts"]}
        seen = set()
        for frame in sample["frames"]:
            timestamp = frame["timestamp_ms"]
            if frame.get("complete_annotation") is not True or timestamp not in valid_times or timestamp in seen:
                raise ValueError("需完整标注实际检测过的画面，不能重复标注或选择未检测时刻")
            seen.add(timestamp)
            for person in frame["people"]:
                box = person["bbox"]
                if len(box) != 4 or not box[0] < box[2] or not box[1] < box[3]:
                    raise ValueError("人工头部框应为原画面像素坐标 [x1,y1,x2,y2]")
            measure_frame(frame["people"], observations.get(timestamp, []), counters)
            frames += 1
    metrics = {}
    for kind, count in counters.items():
        metrics[kind] = {**count,
            "precision": round(count["tp"] / (count["tp"] + count["fp"]), 4) if count["tp"] + count["fp"] else None,
            "recall": round(count["tp"] / (count["tp"] + count["fn"]), 4) if count["tp"] + count["fn"] else None,
            "evaluable_ratio": round(1 - count["unknown_or_missed"] / count["labelled"], 4) if count["labelled"] else None}
    return {"measurement": "fully annotated sampled frames; head-box IoU >= 0.25",
            "split": dataset.get("split"), "synthetic": dataset.get("synthetic", True),
            "source_count": len(sources), "annotated_frames": frames, "metrics": metrics,
            "acceptance_passed": False,
            "reason": "仅计算指标；真实来源、独立划分、各类样本充分性和验收门槛需团队核对。"}


def main():
    parser = argparse.ArgumentParser(description="行为检测人工标注集测量，不自动宣称准确率达标")
    parser.add_argument("dataset", type=Path)
    args = parser.parse_args()
    raw = args.dataset.read_bytes()
    result = evaluate(json.loads(raw), args.dataset.resolve().parent)
    result["dataset_sha256"] = hashlib.sha256(raw).hexdigest()
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
