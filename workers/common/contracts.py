"""Validate public DTOs against the team's unmodified strict schema."""
import hashlib
import json
from functools import lru_cache
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker


class WorkerError(Exception):
    def __init__(self, code, message, retryable=False):
        super().__init__(message)
        self.code, self.retryable = code, retryable


class LeaseLost(Exception):
    """Stop without attempting further reads, uploads or callbacks."""


def json_bytes(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True,
                      separators=(",", ":"), allow_nan=False).encode("utf-8")


def parameter_digest(value):
    return hashlib.sha256(json_bytes(value)).hexdigest()


@lru_cache(maxsize=None)
def validator(definition):
    schema = json.loads((Path(__file__).resolve().parents[2] /
                         "contracts/v1.schema.json").read_text(encoding="utf-8"))
    return Draft202012Validator({**schema, "$ref": f"#/$defs/{definition}"},
                               format_checker=FormatChecker())


def validate(value, definition):
    checked = validator(definition)
    try:
        checked.validate(value)
    except Exception as error:
        # Do not log the invalid payload (it can contain media or credentials).
        raise WorkerError("INVALID_RESULT", f"{definition} 不符合团队协议") from error


def validate_result(result, claim):
    validate(result, "StageResult")
    for key in ("job_id", "run_id", "session_id", "media_asset_id", "stage", "execution"):
        if result[key] != claim[key]:
            raise WorkerError("INVALID_RESULT", f"结果的 {key} 与领取任务不一致")
    duration = claim.get("duration_ms") or result.get("duration_ms")
    previous = 0
    for window in result.get("coverage", []):
        if not previous <= window["start_ms"] < window["end_ms"] <= duration:
            raise WorkerError("TIMELINE_INVALID", "处理范围超出录像或相互重叠")
        previous = window["end_ms"]
    for frame in result.get("frames", []):
        if not 0 <= frame["timestamp_ms"] < duration or not any(
            w["start_ms"] <= frame["timestamp_ms"] < w["end_ms"] for w in result["coverage"]
        ):
            raise WorkerError("TIMELINE_INVALID", "关键帧不在实际处理范围内")
