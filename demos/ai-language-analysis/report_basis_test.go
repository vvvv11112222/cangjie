package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Local provider fixtures explicitly use the NEW generation format. This is
// deliberately separate from Report serialization/history compatibility tests.
func draftFixture(report Report, events []TeachingEvent) ReportDraft {
	draft := ReportDraft{Summary: report.Summary, IDs: cloneValue(report.IDs), Dimensions: cloneValue(report.Dimensions)}
	nextStep := 0
	draft.NextStep = &nextStep
	if report.Observations != nil {
		draft.Observations = []ObservationDraft{}
	}
	for _, o := range report.Observations {
		key := ""
		var focus []AdviceQuote
		for _, basis := range reportBases(events) {
			if o.Code == basis.Code && len(o.EventIDs) == 1 && o.EventIDs[0] == basis.Evidence.ID {
				key = basis.Key
				for _, q := range basis.Evidence.Quotes {
					focus = append(focus, AdviceQuote{SegmentID: q.SegmentID, Text: q.Text})
				}
			}
		}
		finding := ObservationDraft{Key: key, Type: o.Type, Title: o.Title, Text: o.Text, Analysis: o.Analysis}
		if o.Suggestion != "" || o.Type == "issue" {
			finding.Coaching = &CoachingDraft{Quotes: focus, Suggestion: o.Suggestion, Verification: o.Verification}
			if o.Type == "issue" && o.Suggestion == "" {
				finding.Coaching.Suggestion = "下次讨论方法甲时，可请学生对照两处要求，说明同一例子应遵循哪项要求，教师据此澄清。"
				finding.Coaching.Verification = "看学生能否对同一例子给出一致的操作要求。"
			}
		}
		draft.Observations = append(draft.Observations, finding)
		if o.Suggestion != "" && o.Type != "issue" && nextStep == 0 {
			nextStep = len(draft.Observations)
		}
	}
	return draft
}

func TestBasisSelectionSeparatesGoalFromPlanWithoutReclassifying(t *testing.T) {
	segments, set, report := coachingFixture()
	segments = append(segments, Segment{ID: "e-0002", Start: 91000, End: 152000, Text: "先讲解行图像、列图像，再介绍矩阵形式。"})
	set.Events = append(set.Events, TeachingEvent{ID: "ev-002", Type: "transition", Summary: "预告本节课计划",
		DimensionCodes: []string{"pace"}, Quotes: []EventQuote{{SegmentID: "e-0002", Text: segments[1].Text}}})
	before := cloneValue(set)
	bases := reportBases(set.Events)
	if len(bases) != 2 || bases[0].Code != "content" || bases[1].Code != "pace" || bases[0].Key == bases[1].Key || !strings.Contains(bases[0].Scope, "不能") || !strings.Contains(bases[1].Scope, "预告不等于") {
		t.Fatal("goal and plan not independently scoped", bases)
	}
	draft := draftFixture(report, set.Events)
	bound, err := bindReportDraft(draft, set.Events, segments)
	if err != nil || validateEventReport(bound, set.Events, segments) != nil || !reflect.DeepEqual(bound.Observations[0].EventIDs, []string{"ev-001"}) || !reflect.DeepEqual(bound.Observations[0].IDs, []string{"e-0001"}) {
		t.Fatal("goal selection borrowed plan or failed publication guards", err)
	}
	draft.Observations[0].Key = bases[1].Key
	*draft.NextStep = 0
	draft.Observations[0].Coaching = nil
	draft.Observations[0].Title, draft.Observations[0].Text = "预告三种讲解视角", "教师预告行图像、列图像和矩阵形式的顺序。"
	bound, err = bindReportDraft(draft, set.Events, segments)
	if err != nil || bound.Observations[0].Code != "pace" || !reflect.DeepEqual(bound.Observations[0].EventIDs, []string{"ev-002"}) || !reflect.DeepEqual(bound.Observations[0].IDs, []string{"e-0002"}) || !reflect.DeepEqual(before, set) {
		t.Fatal("plan selection did not retain fixed scope/source", err)
	}
	// A selected basis cannot be overridden by independent old-contract fields.
	raw, _ := json.Marshal(draft)
	var wire map[string]any
	_ = json.Unmarshal(raw, &wire)
	for _, field := range []string{"dimension_code", "event_ids", "evidence_ids"} {
		finding := wire["observations"].([]any)[0].(map[string]any)
		finding[field] = "content"
		raw, _ = json.Marshal(wire)
		if _, err := decodeReportDraft(string(raw)); err == nil || strings.Contains(err.Error(), field) {
			t.Fatal("independent link override accepted or technical key leaked", field, err)
		}
		delete(finding, field)
	}
	// Already rejected legacy output remains rejected; it is never converted
	// into a pace conclusion by looking at its title or dropping bad links.
	report.Observations[0].Title = "以核心问题统整三种视角"
	report.Observations[0].EventIDs = []string{"ev-001", "ev-002"}
	snapshot := cloneValue(report)
	if bindObservationEvidence(&report, set.Events, segments) == nil || !reflect.DeepEqual(snapshot, report) {
		t.Fatal("old mixed-dimension candidate silently repaired")
	}
	raw, _ = json.Marshal(report)
	if _, err := decodeReportDraft(string(raw)); err == nil {
		t.Fatal("legacy report accepted as new generation")
	}
}

func TestBasisSelectionKeepsMultiDimensionAnglesAndAllCrossBlockQuotes(t *testing.T) {
	segments, set, report := coachingFixture()
	segments = append(segments, Segment{ID: "e-0002", Start: 10000, End: 20000, Text: "矩阵是矩形排列的数。"})
	set.Events[0].Type, set.Events[0].DimensionCodes = "definition", []string{"content", "expression"}
	set.Events[0].Quotes = append(set.Events[0].Quotes, EventQuote{SegmentID: segments[1].ID, Text: segments[1].Text})
	bases := reportBases(set.Events)
	if len(bases) != 2 || bases[0].Code == bases[1].Code || bases[0].Key == bases[1].Key {
		t.Fatal("multi-dimension event lost an angle")
	}
	for _, basis := range bases {
		draft := draftFixture(report, set.Events)
		draft.Observations[0].Key = basis.Key
		bound, err := bindReportDraft(draft, set.Events, segments)
		if err != nil || bound.Observations[0].Code != basis.Code || !reflect.DeepEqual(bound.Observations[0].IDs, []string{"e-0001", "e-0002"}) {
			t.Fatal("angle or cross-block source lost", err)
		}
		encoded, _ := json.Marshal(bound)
		if strings.Contains(string(encoded), "basis-") || strings.Contains(string(encoded), "evidence_key") {
			t.Fatal("generation key leaked into public report")
		}
	}
	bases[0].Evidence.Quotes[0].Text = "changed"
	if set.Events[0].Quotes[0].Text == "changed" {
		t.Fatal("basis aliases immutable evidence")
	}
}

func TestMissingUnknownAndInvalidBasisAreRejected(t *testing.T) {
	segments, set, report := coachingFixture()
	for _, key := range []string{"", " ", "basis-999"} {
		draft := draftFixture(report, set.Events)
		draft.Observations[0].Key = key
		_, err := bindReportDraft(draft, set.Events, segments)
		code := "missing_basis"
		if key == "basis-999" {
			code = "unknown_basis"
		}
		requireLinkError(t, err, code)
		if strings.Contains(err.Error(), "basis-") || !strings.Contains(err.Error(), "无需重新转写") {
			t.Fatal("basis failure is not readable", err)
		}
	}
	for _, value := range []string{`null`, `[]`, `123`, `"basis-001,basis-002"`} {
		raw, _ := json.Marshal(draftFixture(report, set.Events))
		var wire map[string]any
		_ = json.Unmarshal(raw, &wire)
		var selected any
		_ = json.Unmarshal([]byte(value), &selected)
		wire["observations"].([]any)[0].(map[string]any)["evidence_key"] = selected
		raw, _ = json.Marshal(wire)
		draft, err := decodeReportDraft(string(raw))
		if err == nil {
			_, err = bindReportDraft(draft, set.Events, segments)
		}
		if err == nil {
			t.Fatal("invalid/plural basis accepted", value)
		}
	}
	// A tampered event still fails the unchanged source binding guard.
	draft := draftFixture(report, set.Events)
	set.Events[0].Quotes = append(set.Events[0].Quotes, EventQuote{SegmentID: "missing"})
	if _, err := bindReportDraft(draft, set.Events, segments); err == nil {
		t.Fatal("missing cross-block evidence accepted")
	}
}

func TestNewContractFailureFeedbackDoesNotRecycleGenerationKeys(t *testing.T) {
	segments, set, report := coachingFixture()
	draft := draftFixture(report, set.Events)
	draft.Observations[0].Key = "basis-999"
	raw, _ := json.Marshal(draft)
	_, err := bindReportDraft(draft, set.Events, segments)
	diagnostic := failedReportDiagnostic(string(raw), nil, "basis_binding", err, segments, set, ModelMeta{})
	feedback := buildReportFeedback(*diagnostic)
	encoded, _ := json.Marshal(feedback)
	if len(feedback.Issues) != 1 || feedback.Issues[0].Code != "unknown_basis" || strings.Contains(string(encoded), "basis-") || diagnostic.Raw != string(raw) {
		t.Fatal("new contract failure lost feedback/raw output or recycled old keys", feedback)
	}
	_, err = decodeReportDraft(`{"observations":[{"dimension_code":"content"}]}`)
	diagnostic = failedReportDiagnostic("original malformed format", nil, "json", err, segments, set, ModelMeta{})
	feedback = buildReportFeedback(*diagnostic)
	if len(feedback.Issues) != 1 || feedback.Issues[0].Code != "invalid_report_format" {
		t.Fatal("format failure not available for manual retry guidance")
	}
}
