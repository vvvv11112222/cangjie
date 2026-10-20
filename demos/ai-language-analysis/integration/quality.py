"""Freeze reference annotations and evaluate using the unmodified team calculator.

Inputs contain classroom text. Store them under the ignored evaluation/ directory.
Only digests and metrics are printed. A synthetic pass always exits 1.
"""
import argparse
import copy
import hashlib
import importlib.util
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("team_quality", ROOT / "upstream/tools/check_quality.py")
team = importlib.util.module_from_spec(spec)
spec.loader.exec_module(team)


def reference_digest(evaluation):
    """Exclude hypotheses and actual timings so each model uses the same gold set."""
    if evaluation.get("split") != "held_out" or type(evaluation.get("synthetic")) is not bool:
        raise ValueError("held_out split and explicit synthetic flag required")
    sources = evaluation["source_records"]
    ids = set()
    for source in sources:
        if not source.get("id") or source["id"] in ids:
            raise ValueError("Missing or duplicate source id")
        ids.add(source["id"])
        digest = source.get("sha256", "")
        if len(digest) != 64 or any(c not in "0123456789abcdef" for c in digest):
            raise ValueError("Source SHA256 required")
        if not isinstance(source.get("rights_reference"), str) or not source["rights_reference"].strip():
            raise ValueError("Source rights record reference required")
    frozen = []
    for sample in evaluation["samples"]:
        if sample["source_id"] not in ids:
            raise ValueError("Sample has no source record")
        frozen.append({key: sample[key] for key in ("id", "source_id", "scenario", "reference", "duration_ms")})
        frozen[-1]["timings"] = [{key: pair[key] for key in ("reference_start_ms", "reference_end_ms")}
                                for pair in sample["timings"]]
    value = {"split": "held_out", "synthetic": evaluation["synthetic"],
             "source_records": sorted(sources, key=lambda s: s["id"]),
             "samples": sorted(frozen, key=lambda s: s["id"])}
    return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True,
                                     separators=(",", ":")).encode("utf-8")).hexdigest()


def freeze(evaluation):
    result = copy.deepcopy(evaluation)
    result["dataset_sha256"] = reference_digest(result)
    # Validate sample/scene/timing structure before writing a frozen file.
    team.measure(result, json.loads(team.GATES.read_text(encoding="utf-8")))
    return result


def measure(evaluation):
    if evaluation.get("dataset_sha256") != reference_digest(evaluation):
        raise ValueError("Frozen references changed; do not refreeze a held-out set after tuning")
    return team.measure(evaluation, json.loads(team.GATES.read_text(encoding="utf-8")))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("freeze", "check"))
    parser.add_argument("evaluation", type=Path)
    parser.add_argument("--output", type=Path, help="new frozen file; existing files are never overwritten")
    args = parser.parse_args()
    try:
        evaluation = json.loads(args.evaluation.read_text(encoding="utf-8"))
        if args.mode == "freeze":
            if not args.output:
                parser.error("freeze requires --output")
            if "dataset_sha256" in evaluation:
                raise ValueError("Already frozen; retain the original dataset and run check")
            result = freeze(evaluation)
            with args.output.open("x", encoding="utf-8", newline="\n") as output:
                json.dump(result, output, ensure_ascii=False, indent=2)
                output.write("\n")
            print(json.dumps({"dataset_sha256": result["dataset_sha256"], "synthetic": result["synthetic"],
                              "status": "references_frozen_not_quality_accepted"}))
            return 0
        if args.output:
            parser.error("check prints metrics; --output is for freeze only")
        result = measure(evaluation)
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0 if result["acceptance_passed"] else 1
    except (OSError, ValueError, KeyError, TypeError, AttributeError):
        # Never echo an exception that could contain classroom text.
        parser.exit(2, "Invalid local evaluation. Check source records, frozen references, scenes and timing bounds.\n")


if __name__ == "__main__":
    raise SystemExit(main())
