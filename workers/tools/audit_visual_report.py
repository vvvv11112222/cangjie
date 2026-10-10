"""Summarize observation gaps without treating visibility as accuracy or attendance."""
import argparse
from collections import defaultdict
import json
from pathlib import Path


def audit(report):
    groups = {name: {"observations": 0, "hand_known": 0, "head_known": 0,
                     "hand_positive": 0, "head_positive": 0}
              for name in ("small_head_under_16px", "head_16_to_32px", "head_at_least_32px")}
    seat_tracks = defaultdict(set)
    track_lengths = []
    observation_total = 0
    for person in report["persons"]:
        observations = person["observations"]
        track_lengths.append(len(observations))
        for observation in observations:
            x1, y1, x2, y2 = observation["bbox"]
            size = min(x2 - x1, y2 - y1)
            key = "small_head_under_16px" if size < 16 else "head_16_to_32px" if size < 32 else "head_at_least_32px"
            count = groups[key]
            count["observations"] += 1
            observation_total += 1
            values = observation["behaviors"]
            for kind, prefix in (("hand_raise", "hand"), ("head_down", "head")):
                count[prefix + "_known"] += int(values.get(kind) is not None)
                count[prefix + "_positive"] += int(values.get(kind) is True)
            if observation.get("seat_id"):
                seat_tracks[observation["seat_id"]].add(person["track_id"])
    for group in groups.values():
        for prefix in ("hand", "head"):
            group[prefix + "_evaluable_ratio"] = round(group[prefix + "_known"] / group["observations"], 4) if group["observations"] else None
    known_counts = [c["visible_count"] for c in report["counts"] if c["visible_count"] is not None]
    return {"schema_version": "visual-observation-audit-v1", "source_sha256": report["metadata"]["sha256"],
            "width": report["metadata"]["width"], "height": report["metadata"]["height"],
            "samples": len(report["counts"]), "person_observations": observation_total,
            "visible_count_mean": round(sum(known_counts) / len(known_counts), 3) if known_counts else None,
            "anonymous_track_count": len(track_lengths),
            "tracks_with_only_one_observation": sum(length == 1 for length in track_lengths),
            "seats_associated_with_multiple_tracks": sum(len(ids) > 1 for ids in seat_tracks.values()),
            "by_head_pixels": groups,
            "limitations": ["可判断比例不是识别准确率；需要独立人工标注才能计算漏检和误判。",
                            "按头部像素大小分组，不代表测得真实距离。",
                            "编号多、一个座位关联多个编号只能用于排查，不能直接认定是同一个学生断轨。",
                            "画面人数可能包含教师和倒影，不能当作出勤人数。"]}


def main():
    parser = argparse.ArgumentParser(description="检查已有视觉报告的远处目标覆盖和编号情况；不加载模型")
    parser.add_argument("report", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists():
        parser.error("输出已经存在，请选择新文件")
    result = audit(json.loads(args.report.read_text(encoding="utf-8")))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
