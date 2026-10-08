package analysis

import "testing"

func TestValidateCandidateRejectsEvidenceNotSentToModel(t *testing.T) {
	input := modelInput{Coverage: []Interval{{StartMS: 0, EndMS: 10000}}, Evidence: []modelEvidence{{ID: "sent"}}}
	dimensions := make([]Dimension, 0, 6)
	for _, code := range dimensionCodes {
		dimensions = append(dimensions, Dimension{DimensionCode: code, CoverageStatus: "insufficient", Limitation: "no evidence", Coverage: []Interval{}, SummaryEvidenceIDs: []string{}})
	}
	candidate := ReportCandidate{Summary: "invented", SummaryEvidenceIDs: []string{"same-run-but-not-sent"}, Dimensions: dimensions}
	if err := validateCandidate(candidate, input); err == nil {
		t.Fatal("candidate with an unsent evidence id was accepted")
	}
}

func TestValidateCandidateRequiresLimitedDimensionsToAvoidFacts(t *testing.T) {
	input := modelInput{Coverage: []Interval{{StartMS: 0, EndMS: 10000}}, Evidence: []modelEvidence{{ID: "sent"}}}
	dimensions := make([]Dimension, 0, 6)
	for _, code := range dimensionCodes {
		dimensions = append(dimensions, Dimension{DimensionCode: code, CoverageStatus: "insufficient", Limitation: "no evidence", Coverage: []Interval{}, SummaryEvidenceIDs: []string{}})
	}
	dimensions[2].Summary = "unsupported conclusion"
	if err := validateCandidate(ReportCandidate{Dimensions: dimensions}, input); err == nil {
		t.Fatal("limited dimension with a factual summary was accepted")
	}
}
