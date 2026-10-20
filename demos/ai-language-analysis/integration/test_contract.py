"""Offline end-to-end conversion against the exact pinned upstream Schema."""
import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from jsonschema import Draft202012Validator, FormatChecker
from jsonschema.exceptions import ValidationError

ROOT = Path(__file__).resolve().parents[1]


class ContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        parent = ROOT / "bin/verification"
        parent.mkdir(parents=True, exist_ok=True)
        # Retain artifacts under ignored bin/ for reproducible inspection. Windows
        # scanners may temporarily hold the executable after process exit.
        cls.temp = Path(tempfile.mkdtemp(prefix="contract-", dir=parent))
        cls.binary = cls.temp / ("adapt-report.exe" if os.name == "nt" else "adapt-report")
        subprocess.run(["go", "build", "-o", str(cls.binary), "./cmd/adapt-report"], cwd=ROOT, check=True)
        schema = json.loads((ROOT / "integration/upstream/contracts/v1.schema.json").read_text(encoding="utf-8"))
        # Keep upstream $defs intact and change only the validation entrypoint.
        cls.schema = {"$schema": schema["$schema"], "$defs": schema["$defs"], "$ref": "#/$defs/ReportCandidate"}
        cls.validator = Draft202012Validator(cls.schema, format_checker=FormatChecker())
        fixture = json.loads((ROOT / "integration/testdata/adapter-input.json").read_text(encoding="utf-8"))
        for evidence in fixture["model_input"]["evidence"]:
            evidence["text_sha256"] = hashlib.sha256(evidence["text_content"].encode()).hexdigest()
        model_bytes = json.dumps(fixture["model_input"], ensure_ascii=False, separators=(",", ":")).encode()
        fixture["context"]["manifest_sha256"] = hashlib.sha256(model_bytes).hexdigest()
        for name, data in (("report", json.dumps(fixture["report"], ensure_ascii=False).encode()),
                           ("model-input", model_bytes), ("context", json.dumps(fixture["context"]).encode())):
            (cls.temp / (name + ".json")).write_bytes(data)
        result = subprocess.run([str(cls.binary), "--report", str(cls.temp / "report.json"),
                                 "--model-input", str(cls.temp / "model-input.json"),
                                 "--context", str(cls.temp / "context.json")], cwd=cls.temp,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
        cls.result = json.loads(result.stdout)

    def test_actual_cli_output_matches_strict_upstream_candidate(self):
        self.validator.validate(self.result["candidate"])
        self.assertEqual(len(self.result["candidate"]["dimensions"]), 6)
        self.assertEqual(len(self.result["candidate"]["dimensions"][0]["coverage"]), 2)
        provenance = json.loads((ROOT / "integration/upstream/provenance.json").read_text(encoding="utf-8"))
        self.assertEqual(self.result["extensions"]["upstream_commit"], provenance["commit"])

    def test_coaching_extensions_are_kept_outside_candidate(self):
        with self.assertRaises(ValidationError):
            self.validator.validate(self.result)
        original = self.result["extensions"]["source_report"]["observations"][0]
        self.assertTrue(original["analysis"])
        self.assertTrue(original["verification"])
        self.assertIn("quality_audit", self.result["extensions"]["source_report"])

    def test_schema_rejects_local_ids_extra_fields_and_missing_coverage(self):
        for mutate in (lambda c: c.update(summary_evidence_ids=["seg-001"]),
                       lambda c: c["observations"][0].update(event_ids=["ev-001"]),
                       lambda c: c["dimensions"][0].pop("coverage")):
            value = copy.deepcopy(self.result["candidate"])
            mutate(value)
            self.assertTrue(list(self.validator.iter_errors(value)))

    def test_pinned_resources_match_git_blob_sha(self):
        provenance = json.loads((ROOT / "integration/upstream/provenance.json").read_text(encoding="utf-8"))
        for item in provenance["files"]:
            raw = (ROOT / "integration/upstream" / item["path"]).read_bytes()
            blob = b"blob " + str(len(raw)).encode() + b"\0" + raw
            self.assertEqual(hashlib.sha1(blob).hexdigest(), item["blob_sha"], item["path"])


if __name__ == "__main__":
    unittest.main()
