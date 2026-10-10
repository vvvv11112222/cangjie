package analysis

import (
	"fmt"
	"strings"
	"testing"
)

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

func TestValidateCandidatePreservesCoverageGaps(t *testing.T) {
	input := modelInput{
		Coverage: []Interval{{StartMS: 0, EndMS: 1000}, {StartMS: 59000, EndMS: 60000}},
		Evidence: []modelEvidence{{ID: "sent"}},
	}
	dimensions := make([]Dimension, 0, 6)
	for _, code := range dimensionCodes {
		dimensions = append(dimensions, Dimension{DimensionCode: code, CoverageStatus: "insufficient", Limitation: "no evidence", Coverage: []Interval{}, SummaryEvidenceIDs: []string{}})
	}
	dimensions[0] = Dimension{DimensionCode: "content", CoverageStatus: "observed", Summary: "unsupported gap", Coverage: []Interval{{StartMS: 1000, EndMS: 2000}}, SummaryEvidenceIDs: []string{"sent"}}
	if err := validateCandidate(ReportCandidate{Dimensions: dimensions}, input); err == nil {
		t.Fatal("candidate was allowed to cite an uncovered gap")
	}
}

func TestReportConfigurationIsPinnedInSnapshot(t *testing.T) {
	base := Config{
		ProcessorVersion: "processor", FFmpegSHA256: strings.Repeat("a", 64), ASRModelName: "asr", ASRModelRevision: "1", ASRDevice: "cpu",
		KeyframeIntervalMS: 30000, MaxKeyframes: 10, MaxMediaDurationMS: 60000, MaxVideoHeight: 1080,
		ReportEnabled: true, ReportModel: "model-a", ReportModelRevision: "revision-a", ReportPromptVersion: "prompt-a", ReportPromptSHA256: strings.Repeat("b", 64),
		ReportSelectionVersion: "selection-a", ReportPriceVersion: "price-a", ReportBudgetCurrency: "CNY", ReportMaxInputTokens: 100, ReportMaxOutputTokens: 20,
		ReportInputPriceMicros: 1_000_000, ReportOutputPriceMicros: 2_000_000,
	}
	first := (&Service{cfg: base}).newSnapshot("full")
	changed := base
	changed.ReportModel = "model-b"
	changed.ReportModelRevision = "revision-b"
	changed.ReportMaxInputTokens = 200
	second := (&Service{cfg: changed}).newSnapshot("full")
	if digestJSON(first) == digestJSON(second) {
		t.Fatal("report configuration changes did not affect the run snapshot")
	}
	if first.Report.Model.Name != "model-a" || first.Report.Model.Revision != "revision-a" || first.Report.MaxInputTokens != 100 {
		t.Fatalf("original snapshot changed: %#v", first.Report)
	}
}

func TestModelInputLimitAndCostHelpers(t *testing.T) {
	if got := truncateRunes("甲乙丙丁", 2); got != "甲乙" {
		t.Fatalf("truncateRunes=%q", got)
	}
	cost, err := tokenCostMicros(120, 80, 1_000_000, 2_000_000)
	if err != nil || cost != 280 {
		t.Fatalf("token cost=%d err=%v", cost, err)
	}
}

func TestModelLimitationsAndCompletePromptAreBounded(t *testing.T) {
	values := make([]string, 10_000)
	for index := range values {
		values[index] = fmt.Sprintf("MODEL_INPUT_SKIPPED:%036d", index)
	}
	limitations := boundedModelLimitations(values)
	if len(limitations) != 32 || limitations[len(limitations)-1] != "LIMITATIONS_SKIPPED_COUNT:9969" {
		t.Fatalf("bounded limitations=%d tail=%q", len(limitations), limitations[len(limitations)-1])
	}
	input := modelInput{
		SchemaVersion: SchemaVersion, RunID: "run", SessionID: "session", MediaAssetID: "media", InputSHA256: strings.Repeat("a", 64),
		Model: Model{Name: "fixture", Revision: "1"}, PromptVersion: "p0-v1", PromptSHA256: strings.Repeat("b", 64),
		SelectionVersion: "time-window-v1", CourseContext: map[string]string{"course_name": "course", "session_title": "lesson"},
		Coverage: []Interval{{StartMS: 0, EndMS: 1}}, Limitations: limitations,
		Evidence: []modelEvidence{{ID: "evidence", Kind: "transcript", StartMS: 0, EndMS: 1, TextContent: "正文", TextSHA256: digestBytes([]byte("正文"))}},
	}
	config := reportSnapshot{Model: input.Model, SystemPrompt: reportSystemPrompt, MaxInputTokens: 8000, MaxOutputTokens: 100}
	body, err := buildReportRequest(input, config)
	if err != nil || len(body) > config.MaxInputTokens*8 {
		t.Fatalf("bounded request bytes=%d err=%v", len(body), err)
	}
	config.MaxInputTokens = 1
	if _, err = buildReportRequest(input, config); err == nil {
		t.Fatal("complete prompt limit did not reject an oversized request")
	}
}
