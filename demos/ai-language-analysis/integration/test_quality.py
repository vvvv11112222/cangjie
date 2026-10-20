import copy
import hashlib
import unittest

from quality import freeze, measure


def synthetic_evaluation():
    return {"split": "held_out", "synthetic": True,
            "source_records": [{"id": "invented-text", "sha256": hashlib.sha256(b"synthetic").hexdigest(),
                                "rights_reference": "Invented test strings, no real classroom"}],
            "samples": [{"id": scene, "source_id": "invented-text", "scenario": scene,
                         "reference": "蛋白质，结构 123", "hypothesis": "蛋白质结构123", "duration_ms": 10000,
                         "timings": [{"reference_start_ms": 1000, "reference_end_ms": 5000,
                                      "actual_start_ms": 1200, "actual_end_ms": 5300}]}
                        for scene in ("clear", "noisy")]}


class FrozenQualityTests(unittest.TestCase):
    def test_synthetic_success_is_not_classroom_acceptance(self):
        result = measure(freeze(synthetic_evaluation()))
        self.assertTrue(result["metrics_passed"])
        self.assertFalse(result["acceptance_passed"])
        self.assertEqual(result["timing_p95_ms"], 300)

    def test_frozen_references_source_and_timing_cannot_change(self):
        original = freeze(synthetic_evaluation())
        for mutate in (lambda v: v["samples"][0].update(reference="修改参考"),
                       lambda v: v["samples"][0]["timings"][0].update(reference_start_ms=1100),
                       lambda v: v["source_records"][0].update(sha256="b" * 64),
                       lambda v: v.update(synthetic=False)):
            value = copy.deepcopy(original)
            mutate(value)
            with self.assertRaises(ValueError):
                measure(value)

    def test_new_hypothesis_uses_same_reference_digest_and_can_fail(self):
        value = freeze(synthetic_evaluation())
        value["samples"][0]["hypothesis"] = "完全错误的转写结果"
        self.assertFalse(measure(value)["metrics_passed"])

    def test_missing_scene_and_out_of_bounds_rejected(self):
        value = synthetic_evaluation()
        value["samples"].pop()
        with self.assertRaises(ValueError):
            freeze(value)
        value = synthetic_evaluation()
        value["samples"][0]["timings"][0]["actual_end_ms"] = 10001
        with self.assertRaises(ValueError):
            freeze(value)

    def test_missing_source_or_rights_record_rejected(self):
        for key in ("sha256", "rights_reference"):
            value = synthetic_evaluation()
            del value["source_records"][0][key]
            with self.assertRaises(ValueError):
                freeze(value)


if __name__ == "__main__":
    unittest.main()
