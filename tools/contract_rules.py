"""Executable contract rules for synthetic integration fixtures, not a running API.

Consumers must repeat these checks against locked database rows and actual files.
Context is supplied by fixtures to represent that trusted server-side state.
"""
import hashlib
import json
from datetime import datetime


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(value):
    return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True,
                                    separators=(",", ":"), allow_nan=False).encode("utf-8")).hexdigest()


def report_digest(report):
    content = {k: report[k] for k in ("summary", "summary_evidence_ids", "dimensions", "provenance")}
    content["observations"] = [{k: v for k, v in o.items() if k != "review_status"} for o in report["observations"]]
    return digest(content)


def check_semantics(value, definition=None, context=None):
    context = context or {}

    def visit(item):
        if isinstance(item, list):
            for child in item:
                visit(child)
        elif isinstance(item, dict):
            for start, end in (("planned_start_at", "planned_end_at"), ("starts_at", "ends_at"),
                               ("from", "to"), ("start_date", "end_date")):
                if start in item and end in item:
                    require(datetime.fromisoformat(item[start].replace("Z", "+00:00")) <
                            datetime.fromisoformat(item[end].replace("Z", "+00:00")), "time bounds out of order")
            if "start_ms" in item and "end_ms" in item:
                require(item["start_ms"] < item["end_ms"], "invalid interval")
                if context.get("duration_ms") is not None:
                    require(item["end_ms"] <= context["duration_ms"], "interval exceeds source")
            if "timestamp_ms" in item and item["timestamp_ms"] is not None and context.get("duration_ms"):
                require(item["timestamp_ms"] < context["duration_ms"], "frame exceeds source")
            if "dimensions" in item:
                require({d["dimension_code"] for d in item["dimensions"]} ==
                        {"content", "pace", "thinking", "expression", "management", "technology"}, "six distinct dimensions required")
            if "segments" in item:
                segments = item["segments"]
                require([s["segment_no"] for s in segments] == list(range(len(segments))), "segment numbering")
                require([s["start_ms"] for s in segments] == sorted(s["start_ms"] for s in segments), "segment order")
            if "coverage" in item:
                previous = 0
                for window in item["coverage"]:
                    require(window["start_ms"] >= previous, "coverage must be sorted and disjoint")
                    previous = window["end_ms"]
            if "summary_evidence_ids" in item:
                require(bool(item["summary"].strip()) == bool(item["summary_evidence_ids"]), "summary requires evidence; empty summary has no references")
            for child in item.values():
                visit(child)
    visit(value)

    if definition == "Run":
        plan = ["probe"] if value["mode"] == "media_prepare" else ["probe", "audio_analysis", "video_analysis", "evidence", "report", "validate"]
        require(value["stage_plan"] == plan, "mode stage plan mismatch")
        jobs = value["jobs"]
        require(len({j["stage"] for j in jobs}) == len(jobs), "duplicate stage")
        require(all(j["stage"] in plan and j["attempts"] <= j["max_attempts"] for j in jobs), "invalid stage or attempts")
        require(all(j["max_attempts"] == 1 for j in jobs if j["stage"] == "report"), "report must not retry")
        if value["status"] not in ("queued", "running"):
            require({j["stage"] for j in jobs} == set(plan), "terminal run has waiting stages")
            require(all(j["status"] not in ("queued", "running") for j in jobs), "terminal run has active jobs")
        if value["mode"] == "media_prepare":
            require(value["report_id"] is None, "preparation cannot create report")
            if value["status"] == "succeeded":
                require(jobs[0]["status"] == "succeeded", "preparation requires probe")
        elif value["status"] == "succeeded":
            require(value["report_id"] is not None, "successful analysis needs validated draft")

    if definition in ("Report", "PatchReport", "ReportCandidate"):
        observations = value["observations"]
        if definition != "ReportCandidate":
            ids = [o["id"] for o in observations if o["id"] is not None]
            require(len(ids) == len(set(ids)), "duplicate observation id")
        references = set(value["summary_evidence_ids"])
        for dim in value["dimensions"]:
            references.update(dim["summary_evidence_ids"])
        for obs in observations:
            references.update(obs["evidence_ids"])
        if "allowed_evidence_ids" in context:
            require(references <= set(context["allowed_evidence_ids"]), "reference absent from allowed input evidence")
        if definition == "Report":
            if value["content_sha256"] is not None:
                require(value["provenance"] is not None, "new report needs provenance")
                require(value["content_sha256"] == report_digest(value), "report content digest mismatch")
            attestation = [value[k] for k in ("reviewed_content_sha256", "reviewed_by", "reviewed_at")]
            require(all(v is None for v in attestation) or all(v is not None for v in attestation), "partial report attestation")
            if value["reviewed_content_sha256"] is not None:
                require(value["reviewed_content_sha256"] == value["content_sha256"], "stale whole-report confirmation")
            if value["status"] == "published" and value["content_sha256"] is not None:
                require(value["reviewed_by"] is not None, "publication lacks whole-report confirmation")
                kept = [o for o in observations if o["review_status"] != "rejected"]
                require(bool(kept) and all(o["review_status"] in ("accepted", "revised") for o in kept), "unreviewed or empty publication")
            if value["content_sha256"] is None:
                require(not value["allowed_actions"], "legacy report must be read-only")

    if definition in ("StageResult", "ProbeResult", "AudioResult", "VideoResult"):
        claim = context.get("claim")
        if claim:
            for key in ("job_id", "run_id", "session_id", "media_asset_id", "stage", "execution"):
                require(value[key] == claim[key], "result differs from pinned claim: " + key)
        if value["stage"] == "audio_analysis":
            require(value["model"] == value["execution"]["asr_model"], "ASR model differs from execution")
            for segment in value["segments"]:
                require(any(w["start_ms"] <= segment["start_ms"] < segment["end_ms"] <= w["end_ms"] for w in value["coverage"]), "segment outside processed coverage")

    if definition == "ModelInput":
        ids = [e["id"] for e in value["evidence"]]
        require(len(ids) == len(set(ids)), "duplicate model input evidence")
        for e in value["evidence"]:
            require(hashlib.sha256(e["text_content"].encode("utf-8")).hexdigest() == e["text_sha256"], "model input text digest mismatch")

    if definition == "Claim":
        require(value["execution"]["parameters_sha256"] == digest(value["parameters"]), "claim parameters digest mismatch")

    if definition == "Media":
        if value["kind"] == "report_export" or value["status"] != "ready":
            require(value["playback_url"] is None, "generic media route must not expose export or unready content")
    if definition == "Source" and value["rights_status"] != "verified":
        require(not value["allowed_uses"] and not value["external_processing_allowed"], "unverified source cannot grant uses")
    if definition == "ScheduleQuery":
        require(not ("at" in value and ("from" in value or "to" in value)), "at and range are exclusive")
        require(("from" in value) == ("to" in value), "range requires both bounds")
