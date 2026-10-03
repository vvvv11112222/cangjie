"""Measure frozen held-out ASR results. Never interpret synthetic data as M2/M4 acceptance.

Usage: python tools/check_quality.py <local-evaluation.json>
Input shape and scope are documented in docs/开发与验收计划.md.
Only metrics are printed; classroom text stays in the input file.
"""
import argparse
import json
import math
import re
import unicodedata
from pathlib import Path

GATES = Path(__file__).resolve().parents[1] / "contracts/quality-gates.json"


def normalize(text):
    return "".join(c for c in text if not c.isspace() and not unicodedata.category(c).startswith("P"))


def edit_distance(reference, hypothesis):
    previous = list(range(len(hypothesis) + 1))
    for i, ref in enumerate(reference, 1):
        current = [i]
        for j, hyp in enumerate(hypothesis, 1):
            current.append(min(current[-1] + 1, previous[j] + 1, previous[j - 1] + (ref != hyp)))
        previous = current
    return previous[-1]


def measure(evaluation, gates):
    if evaluation.get("split") != gates["required_split"]:
        raise ValueError("Evaluation must use the frozen held_out split")
    if not re.fullmatch(r"[a-f0-9]{64}", evaluation.get("dataset_sha256", "")):
        raise ValueError("Missing frozen dataset digest")
    if type(evaluation.get("synthetic")) is not bool:
        raise ValueError("Explicit synthetic flag required")
    counts = {s: {"errors": 0, "characters": 0, "samples": 0} for s in gates["cer_max"]}
    timing_errors = []
    ids = set()
    for sample in evaluation["samples"]:
        if sample["id"] in ids:
            raise ValueError("Duplicate sample id")
        ids.add(sample["id"])
        scene = sample["scenario"]
        if scene not in counts:
            raise ValueError("Unknown scenario")
        reference, hypothesis = normalize(sample["reference"]), normalize(sample["hypothesis"])
        if not reference:
            raise ValueError("A labeled speech sample needs nonempty reference characters")
        counts[scene]["errors"] += edit_distance(reference, hypothesis)
        counts[scene]["characters"] += len(reference)
        counts[scene]["samples"] += 1
        duration = sample["duration_ms"]
        if type(duration) is not int or duration <= 0 or not sample["timings"]:
            raise ValueError("Duration and labeled timing pairs required")
        for pair in sample["timings"]:
            for prefix in ("reference", "actual"):
                start, end = pair[prefix + "_start_ms"], pair[prefix + "_end_ms"]
                if type(start) is not int or type(end) is not int or not 0 <= start < end <= duration:
                    raise ValueError("Timing is outside the source timeline")
            timing_errors.extend(abs(pair["actual_" + bound] - pair["reference_" + bound])
                                 for bound in ("start_ms", "end_ms"))
    if any(not c["samples"] for c in counts.values()):
        raise ValueError("Both clear and noisy held-out samples are required")
    p95 = sorted(timing_errors)[math.ceil(len(timing_errors) * .95) - 1]
    passed = p95 <= gates["timing_p95_max_ms"]
    for scene, count in counts.items():
        count["cer"] = count["errors"] / count["characters"]
        count["passed"] = count["cer"] <= gates["cer_max"][scene]
        passed = passed and count["passed"]
    return {"gate_version": gates["version"], "dataset_sha256": evaluation["dataset_sha256"],
            "synthetic": evaluation["synthetic"], "cer": counts, "timing_p95_ms": p95,
            "timing_points": len(timing_errors), "metrics_passed": passed,
            "acceptance_passed": passed and not evaluation["synthetic"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evaluation", type=Path)
    args = parser.parse_args()
    try:
        result = measure(json.loads(args.evaluation.read_text(encoding="utf-8")),
                         json.loads(GATES.read_text(encoding="utf-8")))
    except (ValueError, KeyError, TypeError) as error:
        parser.exit(2, f"Invalid evaluation: {error}\n")
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0 if result["acceptance_passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
