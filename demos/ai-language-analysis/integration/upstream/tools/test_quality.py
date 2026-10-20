import copy
import json
import unittest

from check_quality import GATES, edit_distance, measure, normalize


class QualityTests(unittest.TestCase):
    def setUp(self):
        self.gates = json.loads(GATES.read_text(encoding="utf-8"))
        self.evaluation = {"split": "held_out", "dataset_sha256": "a" * 64, "synthetic": True,
                           "samples": [{"id": scene, "scenario": scene, "reference": "a" * 100,
                                        "hypothesis": "a" * (100 - errors) + "b" * errors,
                                        "duration_ms": 10000,
                                        "timings": [{"reference_start_ms": 1000, "reference_end_ms": 5000,
                                                     "actual_start_ms": 2500, "actual_end_ms": 6500}]}
                                       for scene, errors in (("clear", 15), ("noisy", 30))]}

    def test_normalization_keeps_digits_and_terms(self):
        self.assertEqual(normalize("矩阵 A， 12.5！\n"), "矩阵A125")
        self.assertEqual(edit_distance("abc", "axcd"), 2)

    def test_boundary_pass_is_never_real_acceptance(self):
        result = measure(self.evaluation, self.gates)
        self.assertTrue(result["metrics_passed"])
        self.assertFalse(result["acceptance_passed"])
        self.assertEqual(result["timing_p95_ms"], 1500)

    def test_cer_above_boundary_fails(self):
        self.evaluation["samples"][0]["hypothesis"] += "x"
        self.assertFalse(measure(self.evaluation, self.gates)["metrics_passed"])

    def test_timing_above_boundary_fails(self):
        self.evaluation["samples"][0]["timings"][0]["actual_start_ms"] += 1
        self.assertFalse(measure(self.evaluation, self.gates)["metrics_passed"])

    def test_source_bounds(self):
        self.evaluation["samples"][0]["timings"][0]["actual_end_ms"] = 10001
        with self.assertRaises(ValueError):
            measure(self.evaluation, self.gates)

    def test_missing_scene_and_duplicate_sample_rejected(self):
        for samples in (self.evaluation["samples"][:1], [self.evaluation["samples"][0]] * 2):
            value = copy.deepcopy(self.evaluation)
            value["samples"] = samples
            with self.assertRaises(ValueError):
                measure(value, self.gates)


if __name__ == "__main__":
    unittest.main()
