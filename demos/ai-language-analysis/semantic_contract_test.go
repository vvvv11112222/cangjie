package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestActivityGateKeepsExplicitStudentInstructionAndRejectsMissingMaterial(t *testing.T) {
	segments := []Segment{{ID: "s1", Start: 0, End: 30000, Text: "请两人一组讨论这个例子。把理由写在纸上。"}}
	catalog := evidenceCatalog(segments)
	set := EventSet{Events: []TeachingEvent{{ID: "event", Type: "organization", Summary: "要求两人讨论并写出理由", Quotes: catalog,
		Activity: &ActivityInstruction{LearnerAction: "两人讨论例子并写出理由", QuoteIDs: []string{catalog[0].QuoteID, catalog[1].QuoteID}}}}}
	before := cloneValue(set)
	if err := validateActivityInstructions(set); err != nil || !reflect.DeepEqual(before, set) {
		t.Fatal("explicit task rejected or evidence edited", err)
	}
	for _, change := range []func(*TeachingEvent){
		func(e *TeachingEvent) { e.Activity = nil },
		func(e *TeachingEvent) { e.Activity.LearnerAction = " " },
		func(e *TeachingEvent) { e.Activity.QuoteIDs = nil },
		func(e *TeachingEvent) { e.Activity.QuoteIDs = []string{"unknown"} },
		func(e *TeachingEvent) { e.Activity.QuoteIDs = []string{catalog[0].QuoteID, catalog[0].QuoteID} },
		func(e *TeachingEvent) { e.Type = "transition" },
	} {
		bad := cloneValue(set)
		change(&bad.Events[0])
		if validateActivityInstructions(bad) == nil {
			t.Fatal("incomplete or conflicting task material accepted")
		}
	}
	// No language-specific keyword rewrite: the old misclassification remains
	// available for diagnosis, rather than being converted to a transition.
	bad := EventSet{Events: []TeachingEvent{{ID: "old", Type: "organization", Summary: "强调两点", Quotes: []EventQuote{{Text: "Now there are two important points I want to make."}}}}}
	if validateActivityInstructions(bad) == nil || bad.Events[0].Type != "organization" {
		t.Fatal("unsupported organization accepted or silently reclassified")
	}
	fabricated := cloneValue(set)
	fabricated.Events[0].Activity = &ActivityInstruction{LearnerAction: "注意两个要点", QuoteIDs: []string{catalog[0].QuoteID}}
	fabricated.Events[0].Quotes = []EventQuote{{QuoteID: catalog[0].QuoteID, SegmentID: "s1", Text: "我要强调两个要点。"}}
	if validateActivityInstructions(fabricated) == nil {
		t.Fatal("generated passive attention phrase was accepted as a student task")
	}
	for _, text := range []string{"请解释你的理由", "Please compare the two cases and write one reason.", "两人一组讨论并写出理由"} {
		if !explicitLearnerTaskQuote(text) {
			t.Fatal("explicit task marker was rejected", text)
		}
	}
	for _, text := range []string{"我要强调两个要点。", "I want to explain two points."} {
		if explicitLearnerTaskQuote(text) {
			t.Fatal("teacher narration was accepted as a student task", text)
		}
	}
}

func TestGroundedIssueNeedsLinkedPositiveEvidenceAndPreservesHistory(t *testing.T) {
	scene := calibrationScene([]string{"这个例子必须使用方法甲。这个例子不能使用方法甲。"}, []string{"reasoning"}, []string{"对同一例子给出冲突要求"})
	scene.Report.Observations = []Observation{{Title: "同一例子的要求相互冲突", Code: "thinking", Type: "issue", Text: "对同一例子先要求使用方法甲，又要求不能使用。", Analysis: "两处要求冲突，当前片段未澄清。", IDs: []string{"source-1"}, EventIDs: []string{"event-1"}}}
	draft := draftFixture(scene.Report, scene.Events.Events)
	catalog := evidenceCatalog(scene.Segments)
	draft.Observations[0].Problem = &ProblemBasis{Criterion: "transcript_contradiction", QuoteIDs: []string{catalog[0].QuoteID, catalog[1].QuoteID}, Explanation: "对同一情形同时要求使用与禁止使用。", ContextCheck: "全文只有这两处要求，没有后续澄清。"}
	before := cloneValue(draft)
	report, err := bindReportDraft(draft, scene.Events.Events, scene.Segments)
	if err != nil || report.Observations[0].Suggestion == "" || validateEventReport(report, scene.Events.Events, scene.Segments) != nil || !reflect.DeepEqual(before, draft) {
		t.Fatal("grounded issue lost its action, citations or immutable input", err)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "problem_basis") || strings.Contains(string(encoded), "quote_ids") {
		t.Fatal("generation-only problem material leaked to public report")
	}
	for _, change := range []func(*ObservationDraft){
		func(o *ObservationDraft) { o.Problem = nil },
		func(o *ObservationDraft) { o.Problem.Criterion = "missing_complete_definition" },
		func(o *ObservationDraft) { o.Problem.ContextCheck = " " },
		func(o *ObservationDraft) { o.Problem.Explanation = "" },
		func(o *ObservationDraft) { o.Problem.QuoteIDs = o.Problem.QuoteIDs[:1] },
		func(o *ObservationDraft) { o.Problem.QuoteIDs[1] = o.Problem.QuoteIDs[0] },
		func(o *ObservationDraft) { o.Problem.QuoteIDs[1] = "not-a-source" },
		func(o *ObservationDraft) { o.Type = "observation" },
	} {
		bad := cloneValue(draft)
		change(&bad.Observations[0])
		if _, err := bindReportDraft(bad, scene.Events.Events, scene.Segments); err == nil {
			t.Fatal("unfounded or hidden issue material accepted")
		}
	}
	// A real quote elsewhere cannot stand in for evidence linked to this finding.
	other := append(cloneValue(scene.Segments), Segment{ID: "other", Start: 30000, End: 60000, Text: "另一件事。"})
	bad := cloneValue(draft)
	bad.Observations[0].Problem.QuoteIDs[1] = "other-q01"
	if _, err := bindReportDraft(bad, scene.Events.Events, other); err == nil {
		t.Fatal("unrelated positive evidence accepted")
	}
	// Historical reports do not need the new generation-only material.
	if err := validateReport(report, scene.Segments); err != nil {
		t.Fatal("historical/public report compatibility lost", err)
	}
}

func TestSemanticContractsReachModelsAndFailedExtractionIsRetained(t *testing.T) {
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	segments, events, report := coachingFixture()
	for _, stage := range []string{"report", "audit", "events"} {
		t.Run(stage, func(t *testing.T) {
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct{ Messages []struct{ Content string } }
				if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Messages) != 2 {
					t.Error("invalid text request")
					return
				}
				var input map[string]json.RawMessage
				_ = json.Unmarshal([]byte(req.Messages[1].Content), &input)
				var protocol string
				_ = json.Unmarshal(input["semantic_protocol"], &protocol)
				if protocol != semanticProtocol {
					t.Error("shared decision contract missing")
				}
				var original []Segment
				_ = json.Unmarshal(input["original_segments"], &original)
				if !reflect.DeepEqual(original, segments) {
					t.Error("full original context changed")
				}
				var answer any
				switch stage {
				case "report":
					if string(input["problem_criteria"]) == "" || string(input["evidence_catalog"]) == "" {
						t.Error("problem criteria or source catalog absent")
					}
					answer = draftFixture(report, events.Events)
				case "audit":
					if string(input["review_criteria"]) == "" {
						t.Error("review criteria absent")
					}
					answer = auditFixture(report)
				case "events":
					bad := cloneValue(events)
					bad.Events[0].Type = "organization"
					answer = bad
				}
				raw, _ := json.Marshal(answer)
				chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer provider.Close()
			switch stage {
			case "report":
				if _, _, err := requestSixDimReport(context.Background(), provider.URL, "fake", segments, ReadingResult{}, events); err != nil {
					t.Fatal(err)
				}
			case "audit":
				if a := auditReport(context.Background(), provider.URL, "fake", report, segments, events); a.Status != "no_flags" {
					t.Fatal("valid empty review rejected", a.Diagnostic)
				}
			case "events":
				_, _, err := extractEvents(context.Background(), provider.URL, "fake", segments)
				rejected, ok := err.(*eventContractError)
				if !ok || rejected.raw == "" || !strings.Contains(rejected.Error(), "学生要做什么") {
					t.Fatal("rejected activity or original output not preserved", err)
				}
			}
			if calls != 1 {
				t.Fatal("new contract caused retry or additional request", calls)
			}
		})
	}
}

func TestReviewNeedsKnownCriterionAndSeparatesStatusFromWording(t *testing.T) {
	segments, _, report := coachingFixture()
	f := anchoredFinding(report, segments, "dimension:content", []string{"coverage_status"})
	f.Criterion = "dimension_definition"
	f.SuggestedCoverage = "insufficient"
	if err := validateAuditFinding(f, report, segments); err != nil {
		t.Fatal(err)
	}
	for _, criterion := range []string{"", "pure_thinking_event", "each_block_must_support_every_word"} {
		bad := cloneValue(f)
		bad.Criterion = criterion
		if validateAuditFinding(bad, report, segments) == nil {
			t.Fatal("invented or missing review standard accepted", criterion)
		}
	}
	for _, change := range []func(*AuditFinding){
		func(f *AuditFinding) { f.SuggestedCoverage = "" },
		func(f *AuditFinding) { f.SuggestedCoverage = "observed" },
		func(f *AuditFinding) { f.SuggestedCoverage = "unknown" },
		func(f *AuditFinding) { f.SourceIDs = []string{}; f.SourceQuotes = nil },
		func(f *AuditFinding) { f.Criterion = "claim_scope" },
	} {
		bad := cloneValue(f)
		change(&bad)
		if validateAuditFinding(bad, report, segments) == nil {
			t.Fatal("classification approval or incomplete proposed correction accepted as a concern")
		}
	}
	f = anchoredFinding(report, segments, "observation:1", []string{"analysis"})
	f.Criterion = "dimension_definition"
	if validateAuditFinding(f, report, segments) == nil {
		t.Fatal("dimension criterion used to deny unrelated observation field")
	}
}

func TestActivityRejectionStopsPipelineAndRetainsDiagnostic(t *testing.T) {
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	segments := []Segment{{ID: "s1", Start: 0, End: 30000, Text: "Now there are two important points I want to make."}}
	before := cloneValue(segments)
	catalog := evidenceCatalog(segments)
	rejected := EventSet{Events: []TeachingEvent{{ID: "event", Type: "organization", Summary: "教师预告讲解两点", Quotes: catalog}}}
	eventRaw, _ := json.Marshal(rejected)
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		answer := `{"boundaries":[]}`
		if calls == 2 {
			answer = string(eventRaw)
		} else if calls > 2 {
			t.Error("pipeline called report, audit or retried after invalid activity")
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": answer}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	out, err := analyzeSegments(context.Background(), provider.URL, "fake", segments, func(string, string, int) {})
	if err != nil || calls != 2 || out.ReportStatus != reportFailed || out.FailureStage != "events" || out.Reading.Status != "ready" {
		t.Fatal("activity rejection did not preserve available work or stop further calls", err, calls, out.ReportStatus, out.Reading.Status)
	}
	if len(out.Events.Events) != 0 || len(out.Report.Observations) != 0 || !reflect.DeepEqual(before, out.Segments) || !reflect.DeepEqual(before, segments) {
		t.Fatal("rejected activity published, report fabricated or transcript modified")
	}
	if out.ReportDiagnostic == nil || out.ReportDiagnostic.Raw != string(eventRaw) {
		t.Fatal("pipeline lost rejected extraction diagnostic")
	}
	public, _ := json.Marshal(out)
	if strings.Contains(string(public), "report_diagnostic") || strings.Contains(string(public), "教师预告讲解两点") {
		t.Fatal("raw rejected event leaked into public analysis")
	}
}

func TestInvalidOrganizationIsolatedAndOtherDimensionsStillPublish(t *testing.T) {
	t.Setenv("BAILIAN_REPORT_AUDIT", "0")
	segments, validSet, report := coachingFixture()
	catalog := evidenceCatalog(segments)
	invalid := TeachingEvent{ID: "ev-organization", Type: "organization", Summary: "教师强调两个要点", DimensionCodes: []string{"management"}, Quotes: []EventQuote{{QuoteID: catalog[0].QuoteID}}}
	all := EventSet{Events: append(cloneValue(validSet.Events), invalid)}
	reportDraft := draftFixture(report, validSet.Events)
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var answer any
		switch calls {
		case 1:
			answer = map[string]any{"boundaries": []any{}}
		case 2:
			answer = all
		case 3:
			answer = reportDraft
		default:
			t.Errorf("unexpected model request %d", calls)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		raw, _ := json.Marshal(answer)
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	out, err := analyzeSegments(context.Background(), provider.URL, "fake", segments, func(string, string, int) {})
	if err != nil || out.ReportStatus != reportReady || out.FailureStage != "" || calls != 3 {
		t.Fatalf("valid events did not continue to report generation: err=%v status=%s stage=%s calls=%d", err, out.ReportStatus, out.FailureStage, calls)
	}
	if len(out.Events.Events) != 1 || out.Events.Events[0].ID != "ev-001" {
		t.Fatalf("rejected organization leaked into public events: %+v", out.Events)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "活动组织暂不能判断") {
		t.Fatalf("missing readable partial-result warning: %+v", out.Warnings)
	}
	for _, d := range out.Report.Dimensions {
		if d.Code == "management" && d.Status != "insufficient" {
			t.Fatalf("management dimension used rejected event: %+v", d)
		}
	}
	if out.ReportDiagnostic == nil || len(out.ReportDiagnostic.RejectedEvents) != 1 || out.ReportDiagnostic.RejectedEvents[0].Event.ID != invalid.ID {
		t.Fatalf("rejected event was not retained in diagnostic: %+v", out.ReportDiagnostic)
	}
	if len(out.Report.Observations) == 0 || out.Report.Observations[0].Suggestion == "" {
		t.Fatal("valid report lost its actionable suggestion")
	}
}

// Replay the exact saved failed review, without calling a model or changing the
// archived report. This measures contract checks, not semantic accuracy.
func TestGroundedSavedReviewSeparatesClassificationApprovals(t *testing.T) {
	const source = "testdata/public-courses/replays/physics-review.json"
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != "3089282b74cfb36c200eea5990858f3cc5fca02a661338e6d2be25fc42db4b8a" {
		t.Fatal("saved review source changed; do not replace a failing fixture with a better model response")
	}
	var archive struct {
		Analysis AnalysisOutput `json:"analysis"`
	}
	if err = json.Unmarshal(raw, &archive); err != nil || archive.Analysis.Report.Audit == nil {
		t.Fatal("saved review missing", err)
	}
	a := archive.Analysis.Report.Audit
	var draft auditDraft
	if err = strictJSON(a.RawResponse, &draft); err != nil || validateAuditCoverage(draft, archive.Analysis.Report) != nil || len(draft.Findings) != 10 {
		t.Fatal("saved raw review is no longer the measured ten-item response", err)
	}
	accepted, rejectedClassifications := 0, 0
	for _, f := range draft.Findings {
		err = validateAuditFinding(f, archive.Analysis.Report, archive.Analysis.Segments)
		if err == nil {
			accepted++
			if f.Target != "observation:2" || f.Criterion != "advice_relevance" {
				t.Fatal("unexpected accepted review; inspect rather than change the expected count", f.Target)
			}
		} else if f.Criterion == "dimension_definition" {
			rejectedClassifications++
		}
	}
	if accepted != 1 || rejectedClassifications != 6 || a.Status != "partial_review" || len(a.Findings) != 7 {
		t.Fatal("classification approvals survived, remaining concern lost, or old report changed", accepted, rejectedClassifications)
	}
	after, err := os.ReadFile(source)
	if err != nil || string(raw) != string(after) {
		t.Fatal("offline replay changed historical evidence", err)
	}
	t.Log("saved response: 10 entries; current contract accepts 1 for human review, isolates 9 including 6 classification approvals; archived report unchanged")
}

// The live physics run predates grounded-feedback-v3. Replay its exact event
// extraction to verify the new gate rejects the fabricated “pay attention” task
// without changing the archived response or making another model request.
func TestGroundedSavedPhysicsEventsRejectPassiveActivity(t *testing.T) {
	const source = "testdata/public-courses/replays/physics-events.json"
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != "5899415bbfbefe75fe4779e355652d50f30a2ecf7086e171c0f980cbb73f3875" {
		t.Fatal("new physics fixture changed; do not replace the measured response")
	}
	var archive struct {
		Analysis AnalysisOutput `json:"analysis"`
	}
	if err = json.Unmarshal(raw, &archive); err != nil {
		t.Fatal(err)
	}
	if err = validateActivityInstructions(archive.Analysis.Events); err == nil || !strings.Contains(err.Error(), "明确要求学生") {
		t.Fatal("passive teacher narration was not rejected by the new activity gate", err)
	}
	if len(archive.Analysis.Events.Events) == 0 || archive.Analysis.Events.Events[16].Type != "organization" {
		t.Fatal("measured passive organization fixture changed")
	}
	valid, rejected := isolateInvalidActivityEvents(archive.Analysis.Events)
	if len(valid.Events) != len(archive.Analysis.Events.Events)-1 || len(rejected) != 1 || rejected[0].Event.ID != archive.Analysis.Events.Events[16].ID {
		t.Fatalf("saved event response was not safely partitioned: valid=%d rejected=%d", len(valid.Events), len(rejected))
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(raw) {
		t.Fatal("saved live response changed", err)
	}
	t.Log("saved physics event response is rejected by grounded-feedback-v3; archive unchanged; zero real model calls")
}
