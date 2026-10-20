package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func requireLinkError(t *testing.T, err error, code string) *EvidenceLinkError {
	t.Helper()
	var link *EvidenceLinkError
	if !errors.As(err, &link) || link.Code != code || link.ObservationIndex != 1 {
		t.Fatalf("expected %s at observation 1, got %v", code, err)
	}
	if !strings.Contains(link.Error(), "第 1 条结论") || !strings.Contains(link.Error(), "先交代求解目标") || strings.Contains(link.Error(), "ev-") {
		t.Fatal("diagnostic is not readable", link.Error())
	}
	return link
}

func TestEventLinkFailuresAreSpecificAndReadable(t *testing.T) {
	segments, set, report := coachingFixture()
	for _, code := range []string{"missing_event", "unknown_event", "duplicate_event", "dimension_mismatch", "source_mismatch", "unobserved_dimension"} {
		t.Run(code, func(t *testing.T) {
			bad := cloneValue(report)
			switch code {
			case "missing_event":
				bad.Observations[0].EventIDs = []string{}
			case "unknown_event":
				bad.Observations[0].EventIDs = []string{"ev-999"}
			case "duplicate_event":
				bad.Observations[0].EventIDs = []string{"ev-001", "ev-001"}
			case "dimension_mismatch":
				bad.Observations[0].Code = "thinking"
			case "source_mismatch":
				segments = append(segments, Segment{ID: "e-0002", Start: 10000, End: 20000, Text: "无关内容。"})
				bad.Observations[0].IDs = []string{"e-0002"}
			case "unobserved_dimension":
				bad.Dimensions[0] = Dimension{Code: "content", Status: "insufficient", Limitation: "不足", IDs: []string{}}
			}
			link := requireLinkError(t, validateEventReport(bad, set.Events, segments), code)
			if code == "dimension_mismatch" && (!strings.Contains(link.Message, "思维引导") || !strings.Contains(link.Message, "00:00–00:10") || link.EventID != "ev-001") {
				t.Fatal("missing dimension/source details")
			}
			if code == "dimension_mismatch" && (!strings.Contains(link.Message, "当前归为内容组织") || !strings.Contains(link.Message, "需核对事件分类和结论范围")) {
				t.Fatal("classification check presented as proof of semantic support")
			}
		})
	}
}

func TestBindingDerivesAllCrossBlockSourcesAndDeduplicates(t *testing.T) {
	segments, set, report := coachingFixture()
	segments = append(segments, Segment{ID: "e-0002", Start: 10000, End: 20000, Text: "接着介绍矩阵。"})
	set.Events[0].Quotes = append(set.Events[0].Quotes, EventQuote{SegmentID: "e-0002", Text: segments[1].Text})
	for _, supplied := range [][]string{nil, {}, {"e-0001"}, {"e-0002", "e-0001", "e-0001"}} {
		bound := cloneValue(report)
		bound.Observations[0].IDs = supplied
		bound.Observations[0].EventIDs = []string{"ev-001", "ev-001"}
		if err := bindObservationEvidence(&bound, set.Events, segments); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(bound.Observations[0].EventIDs, []string{"ev-001"}) || !reflect.DeepEqual(bound.Observations[0].IDs, []string{"e-0001", "e-0002"}) {
			t.Fatal("sources not derived completely/in transcript order")
		}
		if err := validateEventReport(bound, set.Events, segments); err != nil {
			t.Fatal(err)
		}
		before := cloneValue(bound)
		if err := bindObservationEvidence(&bound, set.Events, segments); err != nil || !reflect.DeepEqual(before, bound) {
			t.Fatal("binding is not idempotent", err)
		}
	}
	bad := cloneValue(report)
	bad.Observations[0].IDs = []string{"e-0001"}
	requireLinkError(t, validateEventReport(bad, set.Events, segments), "incomplete_sources")
	// Sharing an event across different conclusions is legitimate.
	report.Observations = append(report.Observations, cloneValue(report.Observations[0]))
	if err := bindObservationEvidence(&report, set.Events, segments); err != nil {
		t.Fatal(err)
	}
	if err := validateEventReport(report, set.Events, segments); err != nil {
		t.Fatal(err)
	}
}

func TestBindingNeverDropsUnknownMismatchedOrUnrelatedReferences(t *testing.T) {
	segments, set, report := coachingFixture()
	segments = append(segments, Segment{ID: "e-0002", Start: 10000, End: 20000, Text: "其他原文。"})
	set.Events = append(set.Events, TeachingEvent{ID: "ev-002", Type: "transition", Summary: "讲授顺序", DimensionCodes: []string{"pace"}, Quotes: set.Events[0].Quotes})
	for _, tc := range []struct {
		events, sources []string
		code            string
	}{
		{[]string{"ev-001", "ev-999"}, nil, "unknown_event"},
		{[]string{"ev-001", "ev-002"}, nil, "dimension_mismatch"},
		{[]string{"ev-002"}, nil, "dimension_mismatch"},
		{[]string{"ev-001"}, []string{"e-9999"}, "source_mismatch"},
		{[]string{"ev-001"}, []string{"e-0001", "e-0002"}, "source_mismatch"},
	} {
		bad := cloneValue(report)
		bad.Observations[0].EventIDs, bad.Observations[0].IDs = tc.events, tc.sources
		before := cloneValue(bad)
		requireLinkError(t, bindObservationEvidence(&bad, set.Events, segments), tc.code)
		if !reflect.DeepEqual(before, bad) {
			t.Fatal("invalid association silently changed")
		}
	}
}

func TestCoverageNormalizationDoesNotDiscardBadSummarySources(t *testing.T) {
	segments, set, report := coachingFixture()
	report.Dimensions[0].IDs = []string{"e-0001", "e-9999"}
	normalizeReportCoverage(&report, set.Events)
	if !reflect.DeepEqual(report.Dimensions[0].IDs, []string{"e-0001", "e-9999"}) || validateEventReport(report, set.Events, segments) == nil {
		t.Fatal("invalid supported-dimension source hidden")
	}
}

func TestDimensionChoicesContainOnlyAllowedEventsAndExplicitEmptyLists(t *testing.T) {
	_, set, _ := coachingFixture()
	set.Events = append(set.Events, TeachingEvent{ID: "ev-002", Type: "definition", DimensionCodes: []string{"content", "expression"}})
	choices := reportEventChoices(set.Events)
	if len(choices) != 6 || len(choices[0].Events) != 2 || len(choices[3].Events) != 1 {
		t.Fatal("choices incomplete")
	}
	for _, choice := range choices {
		if choice.Events == nil {
			t.Fatal("missing explicit list")
		}
		for _, e := range choice.Events {
			if !contains(e.DimensionCodes, choice.Code) {
				t.Fatal("cross-dimension choice")
			}
		}
	}
	choices[0].Events[0].DimensionCodes[0] = "changed"
	if set.Events[0].DimensionCodes[0] != "content" {
		t.Fatal("choices mutate evidence")
	}
	// Semantic classification cannot be proven by these structural tests.
	prompt, _ := assets.ReadFile("events_prompt.txt")
	for _, constraint := range []string{"This is the first lecture", "the third way is the matrix form", "The whole semester will be examples", "要求学生分组"} {
		if !strings.Contains(string(prompt), constraint) {
			t.Fatal("semantic boundary missing from prompt", constraint)
		}
	}
}

func TestPublishValidationAlsoPreservesFailedCandidate(t *testing.T) {
	s, segments := storedFixture(t)
	if err := s.update("sample", func(j *Job) error {
		j.Status, j.Mode, j.InputRevision = jobQueued, "report_only", 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := testManager(s)
	m.analyzeText = func(context.Context, string, string, []Segment, progressFunc) (AnalysisOutput, error) {
		out := readyFixture(segments)
		out.Report = Report{Summary: "未经校验的候选"}
		return out, nil
	}
	m.run("sample")
	j, _ := s.internalGet("sample")
	if j.ReportStatus != reportFailed || len(j.ReportVersions) != 0 || len(j.ReportDiagnostics) != 1 || j.ReportDiagnostics[0].Stage != "publish_validation" || j.ReportDiagnostics[0].Candidate.Summary != "未经校验的候选" || j.Report.Summary != "" {
		t.Fatal("publish guard lost draft or exposed rejected report", j)
	}
}

func TestReportBindingPipelineWithLocalModel(t *testing.T) {
	for _, scenario := range []string{"valid_basis", "omitted_sources", "duplicate_summary_sources", "unknown_summary_source", "missing_basis", "empty_basis", "missing_basis_field", "unknown_basis", "mixed_dimensions", "invalid_json", "invalid_schema", "missing_coaching", "invented_coaching_quote", "missing_coaching_check"} {
		t.Run(scenario, func(t *testing.T) {
			segments, set, candidate := coachingFixture()
			switch scenario {
			case "omitted_sources":
				candidate.Observations[0].IDs = nil
			case "duplicate_summary_sources":
				candidate.IDs = []string{"e-0001", "e-0001"}
				candidate.Dimensions[0].IDs = []string{"e-0001", "e-0001"}
			case "unknown_summary_source":
				candidate.Dimensions[0].IDs = []string{"e-0001", "e-9999", "e-0001"}
			case "missing_basis", "missing_basis_field":
				// Regression: all five conclusions can have valid-looking prose
				// and existing source blocks while omitting the event selection.
				for len(candidate.Observations) < 5 {
					candidate.Observations = append(candidate.Observations, cloneValue(candidate.Observations[0]))
				}
			case "mixed_dimensions":
				set.Events = append(set.Events, TeachingEvent{ID: "ev-002", Type: "transition", Summary: "顺序", DimensionCodes: []string{"pace"}, Quotes: cloneValue(set.Events[0].Quotes)})
				candidate.Observations[0].EventIDs = []string{"ev-001", "ev-002"}
			case "invalid_schema":
				candidate.Observations[0].Analysis = ""
			}
			draft := draftFixture(candidate, set.Events)
			switch scenario {
			case "missing_coaching":
				draft.Observations[0].Coaching = nil
			case "invented_coaching_quote":
				draft.Observations[0].Coaching.Quotes[0].Text = "原文不存在的建议依据"
			case "missing_coaching_check":
				draft.Observations[0].Coaching.Verification = ""
			}
			if scenario == "missing_basis" || scenario == "empty_basis" || scenario == "missing_basis_field" {
				for i := range draft.Observations {
					draft.Observations[i].Key = ""
				}
			}
			if scenario == "unknown_basis" {
				draft.Observations[0].Key = "basis-999"
			}
			raw, _ := json.Marshal(draft)
			if scenario == "mixed_dimensions" {
				raw, _ = json.Marshal(candidate)
			}
			if scenario == "missing_basis_field" || scenario == "missing_basis" {
				var wire map[string]any
				if err := json.Unmarshal(raw, &wire); err != nil {
					t.Fatal(err)
				}
				for _, observation := range wire["observations"].([]any) {
					if scenario == "missing_basis_field" {
						delete(observation.(map[string]any), "evidence_key")
					} else {
						observation.(map[string]any)["evidence_key"] = nil
					}
				}
				raw, _ = json.Marshal(wire)
			}
			if scenario == "invalid_json" {
				raw = []byte("{broken")
			}
			wireEvents := cloneValue(set)
			for i := range wireEvents.Events {
				wireEvents.Events[i].Quotes = []EventQuote{{QuoteID: "e-0001-q01"}}
			}
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Stream   bool `json:"stream"`
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !request.Stream {
					t.Error("non-text request", err)
				}
				calls++
				answer := string(raw)
				switch calls {
				case 1:
					answer = `{"boundaries":[]}`
				case 2:
					b, _ := json.Marshal(wireEvents)
					answer = string(b)
				case 3:
					var input struct {
						Protocol string                   `json:"evidence_protocol"`
						Choices  []DimensionSummaryChoice `json:"dimension_summary_choices"`
						Bases    []ReportBasis            `json:"evidence_bases"`
						Schema   map[string]any           `json:"output_schema"`
					}
					if len(request.Messages) != 2 || json.Unmarshal([]byte(request.Messages[1].Content), &input) != nil || input.Protocol != evidenceProtocol || len(input.Choices) != 6 || input.Choices[2].HasEvidence || len(input.Bases) != len(set.Events) {
						t.Error("report not given constrained per-dimension event input")
					}
					if input.Schema["type"] != "object" || !strings.HasSuffix(request.Messages[0].Content, reportContractReminder) {
						t.Error("report contract missing or not last after rubric")
					}
				default:
					t.Error("unexpected paid retry")
				}
				chunk, _ := json.Marshal(map[string]any{"id": "local-report", "choices": []any{map[string]any{"delta": map[string]string{"content": answer}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer provider.Close()
			out, err := analyzeSegments(context.Background(), provider.URL, "fake", segments, func(string, string, int) {})
			if err != nil || calls != 3 || len(out.Events.Events) != len(set.Events) || !reflect.DeepEqual(out.Segments, segments) {
				t.Fatal("pipeline or immutable evidence changed", err)
			}
			if scenario == "valid_basis" || scenario == "omitted_sources" || scenario == "duplicate_summary_sources" {
				if out.ReportStatus != reportReady || out.ReportDiagnostic != nil || len(out.Report.Observations[0].EventIDs) != 1 || !reflect.DeepEqual(out.Report.Observations[0].IDs, []string{"e-0001"}) {
					t.Fatal("safe binding not accepted", out)
				}
				if !reflect.DeepEqual(out.Report.IDs, []string{"e-0001"}) || !reflect.DeepEqual(out.Report.Dimensions[0].IDs, []string{"e-0001"}) {
					t.Fatal("equivalent summary references not normalized")
				}
			} else {
				d := out.ReportDiagnostic
				if out.ReportStatus != reportFailed || d == nil || d.Raw != string(raw) || d.Request.ID != "local-report" || len(out.Report.Observations) != 0 || !reflect.DeepEqual(d.Events, out.Events) {
					t.Fatal("failure draft lost or published", out)
				}
				if d.Candidate != nil && !reflect.DeepEqual(d.Candidate.Observations[0].Text, candidate.Observations[0].Text) {
					t.Fatal("diagnostic prose mutated")
				}
				if scenario == "unknown_basis" {
					requireLinkError(t, d.LinkError, "unknown_basis")
				}
				if scenario == "mixed_dimensions" {
					if d.Stage != "json" {
						t.Fatal("old mixed association format accepted")
					}
				}
				if scenario == "missing_basis" || scenario == "empty_basis" || scenario == "missing_basis_field" {
					requireLinkError(t, d.LinkError, "missing_basis")
					if !strings.Contains(out.FailureMessage, "模型漏填") || !strings.Contains(out.FailureMessage, "无需重新转写") {
						t.Fatal("missing evidence error does not identify model output failure")
					}
				}
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "raw_model_output") {
				t.Fatal("draft leaked from legacy API")
			}
		})
	}
}

func TestSummaryReferenceNormalizationPreservesInvalidAndMissingSources(t *testing.T) {
	segments, set, report := coachingFixture()
	report.IDs = []string{"e-0001", "e-9999", "e-0001", "e-9999"}
	report.Dimensions[0].IDs = nil
	before := cloneValue(report)
	normalizeReportReferenceDuplicates(&report)
	if !reflect.DeepEqual(report.IDs, []string{"e-0001", "e-9999"}) || report.Dimensions[0].IDs != nil || !reflect.DeepEqual(before.Observations, report.Observations) {
		t.Fatal("normalization invented/dropped evidence or changed conclusions")
	}
	if validateReport(report, segments) == nil || validateEventReport(report, set.Events, segments) == nil {
		t.Fatal("unknown source accepted after normalization")
	}
	report.IDs = []string{}
	normalizeReportReferenceDuplicates(&report)
	if report.IDs == nil || validateReport(report, segments) == nil {
		t.Fatal("empty summary sources silently filled")
	}
}

func TestReportOutputSchemaRequiresEvidenceWithinEachDimension(t *testing.T) {
	segments, set, _ := coachingFixture()
	set.Events = append(set.Events, TeachingEvent{ID: "ev-002", DimensionCodes: []string{"pace"}})
	before := cloneValue(set)
	schema := reportOutputSchema(set.Events, segments)
	properties := schema["properties"].(map[string]any)
	object := properties["observations"].(map[string]any)["items"].(map[string]any)
	fields := object["properties"].(map[string]any)
	keys := fields["evidence_key"].(map[string]any)["enum"].([]string)
	if len(keys) != 2 || !contains(object["required"].([]string), "evidence_key") || object["additionalProperties"] != false || fields["event_ids"] != nil || fields["dimension_code"] != nil || fields["evidence_ids"] != nil {
		t.Fatal("schema permits missing basis or independent conflicting links")
	}
	for i, basis := range reportBases(set.Events) {
		if keys[i] != basis.Key {
			t.Fatal("schema key outside immutable catalog")
		}
	}
	empty := reportOutputSchema(nil, segments)["properties"].(map[string]any)["observations"].(map[string]any)
	if empty["maxItems"] != 0 || !reflect.DeepEqual(before, set) {
		t.Fatal("no-evidence report permits unsupported conclusions or input mutated")
	}
}

func TestFailedDiagnosticPersistsSeparatelyFromPublishedHistory(t *testing.T) {
	s, original := storedFixture(t)
	old := readyFixture(original)
	if err := s.update("sample", func(j *Job) error {
		j.Status, j.Mode, j.InputRevision = jobQueued, "report_only", 1
		j.ReportVersions = []ReportVersion{{Version: 1, InputRevision: 1, ASRStatus: asrComplete, ReportStatus: reportReady, Segments: original, Report: old.Report, Events: old.Events}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := testManager(s)
	candidate := old.Report
	candidate.Observations = []Observation{{Code: "thinking", Type: "observation", Title: "先交代求解目标", EventIDs: []string{"ev-999"}}}
	raw, _ := json.Marshal(candidate)
	m.analyzeText = func(_ context.Context, _, _ string, input []Segment, _ progressFunc) (AnalysisOutput, error) {
		out := readyFixture(input)
		err := linkError("unknown_event", 0, candidate.Observations[0], TeachingEvent{ID: "ev-999"}, "", input)
		out.ReportStatus, out.FailureStage, out.FailureMessage = reportFailed, "report", err.Error()
		out.ReportDiagnostic = failedReportDiagnostic(string(raw), &candidate, "event_binding", err, input, out.Events, ModelMeta{ID: "local-report"})
		return out, nil
	}
	m.run("sample")
	internal, _ := s.internalGet("sample")
	if internal.Status != jobPartial || len(internal.ReportVersions) != 1 || len(internal.ReportDiagnostics) != 1 || internal.ReportDiagnostics[0].InputRevision != 1 {
		t.Fatal("diagnostic lost or treated as a report version", internal)
	}
	public, _ := s.get("sample")
	if public.ReportDiagnostics != nil || s.list()[0].ReportDiagnostics != nil {
		t.Fatal("draft leaked from task API")
	}
	reloaded, err := newJobStore(s.root)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := reloaded.internalGet("sample")
	if !reflect.DeepEqual(internal.ReportDiagnostics, after.ReportDiagnostics) {
		t.Fatal("diagnostic failed to reload")
	}
	m = testManager(reloaded)
	if err := m.retry("sample"); err != nil {
		t.Fatal(err)
	}
	m.analyzeText = func(_ context.Context, _, _ string, input []Segment, _ progressFunc) (AnalysisOutput, error) {
		return readyFixture(input), nil
	}
	m.run("sample") // No media exists: a regression to ASR fails this test.
	after, _ = reloaded.internalGet("sample")
	if after.Status != jobSucceeded || len(after.ReportVersions) != 2 || !reflect.DeepEqual(internal.ReportDiagnostics, after.ReportDiagnostics) || !reflect.DeepEqual(reloaded.revisionsFor("sample")[0].Segments, original) {
		t.Fatal("retry lost diagnostics/history or changed transcript")
	}
}
