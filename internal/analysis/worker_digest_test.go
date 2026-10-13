package analysis

import (
	"testing"
)

func TestWorkerParameterDigestMatchesContract(t *testing.T) {
	s := &Service{cfg: Config{ProcessorVersion: "test", ASRDevice: "cpu", KeyframeIntervalMS: 30000, MaxKeyframes: 240}}
	snap := s.newSnapshot("full")
	// contracts/examples/claim.json and Python parameter_digest use this exact digest.
	const expected = "fc41a2843acef1d611b1f8f0ea4870b7f74b6f8acdb96ef578fd0066ade6ed52"
	for stage, execution := range snap.Executions {
		if execution.ParametersSHA256 != expected {
			t.Fatalf("%s parameter digest = %s, want %s", stage, execution.ParametersSHA256, expected)
		}
	}
	changed := &Service{cfg: Config{ASRDevice: "cpu", KeyframeIntervalMS: 1000, MaxKeyframes: 240}}
	if changed.newSnapshot("full").Executions["probe"].ParametersSHA256 == expected {
		t.Fatal("changed parameters retained the same digest")
	}
}
