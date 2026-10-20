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

// Exercise both real request constructors: a mock supplies results, so this
// tests context delivery and side effects, never the model's semantic accuracy.
func TestNarrativeRulesSharedByGenerationAndReview(t *testing.T) {
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	segments, events, report := coachingFixture()
	before := cloneValue(report)
	var inputs []map[string]json.RawMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 {
			t.Error("invalid text-only request", err)
			http.Error(w, "invalid fixture", 400)
			return
		}
		var input map[string]json.RawMessage
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil {
			t.Error(err)
		}
		inputs = append(inputs, input)
		var answer any = draftFixture(report, events.Events)
		if _, reviewing := input["candidate_report"]; reviewing {
			answer = auditFixture(report)
			var targets []auditTarget
			_ = json.Unmarshal(input["review_targets"], &targets)
			roles := narrativeFieldRoles()
			for _, target := range targets {
				for _, field := range target.Fields {
					role := field
					if field == "summary" && strings.HasPrefix(target.Key, "dimension:") {
						role = "dimension_summary"
					}
					if target.FieldRoles[field] != roles[role] || roles[role] == "" {
						t.Error("review bypasses shared field boundary", target.Key, field)
					}
				}
			}
		}
		raw, _ := json.Marshal(answer)
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	if _, _, err := requestSixDimReport(context.Background(), provider.URL, "fake", segments, ReadingResult{}, events); err != nil {
		t.Fatal(err)
	}
	a := auditReport(context.Background(), provider.URL, "fake", report, segments, events)
	if a.Status != "no_flags" || a.NarrativeProtocol != narrativeProtocol || len(inputs) != 2 || !reflect.DeepEqual(before, report) {
		t.Fatal("review changed report, retried or lost version", a.Status, len(inputs))
	}
	for _, input := range inputs {
		var adviceVersion string
		_ = json.Unmarshal(input["advice_protocol"], &adviceVersion)
		if adviceVersion != adviceProtocol {
			t.Fatal("generation or review lost grounded advice protocol")
		}
		if _, reviewing := input["candidate_report"]; !reviewing {
			var methods []AdviceMethod
			_ = json.Unmarshal(input["advice_methods"], &methods)
			if !reflect.DeepEqual(methods, adviceMethods()) || input["advice_templates"] != nil {
				t.Fatal("generation must receive method goals instead of fixed suggestion prose")
			}
		}
		var protocol string
		var rules []semanticRule
		var roles, limits map[string]string
		var original []Segment
		_ = json.Unmarshal(input["narrative_protocol"], &protocol)
		_ = json.Unmarshal(input["narrative_rules"], &rules)
		_ = json.Unmarshal(input["narrative_field_roles"], &roles)
		_ = json.Unmarshal(input["input_limits"], &limits)
		_ = json.Unmarshal(input["original_segments"], &original)
		if protocol != narrativeProtocol || !reflect.DeepEqual(rules, narrativeRules()) || !reflect.DeepEqual(roles, narrativeFieldRoles()) || !reflect.DeepEqual(limits, narrativeInputLimits()) || !reflect.DeepEqual(original, segments) {
			t.Fatal("generation or review lost shared boundaries or full original context")
		}
	}
}

func TestNarrativeRelationReviewRequiresSourceAndSpecificProse(t *testing.T) {
	var c auditCalibrationCase
	for _, candidate := range auditCalibrationCases() {
		if candidate.ID == "relation-direction-overclaim" {
			c = candidate
		}
	}
	if c.ID == "" {
		t.Fatal("missing directional contrast")
	}
	f := anchoredFinding(c.Report, c.Segments, c.Target, []string{c.Field})
	f.Criterion, f.Kind = "relation_fidelity", "context_conflict"
	f.Reason = "原文从动量导数写法出发，在恒定质量条件下化简；报告的小结把本段展示的方向倒写，需核对。"
	if err := validateAuditFinding(f, c.Report, c.Segments); err != nil {
		t.Fatal("anchored relation concern rejected", err)
	}
	for _, change := range []func(*AuditFinding){
		func(f *AuditFinding) { f.SourceIDs = []string{}; f.SourceQuotes = nil },
		func(f *AuditFinding) { f.SourceQuotes[0].Text = "原文不存在的反向推导" },
		func(f *AuditFinding) { f.ReportQuotes[0].Text = "报告不存在的专业断言" },
		func(f *AuditFinding) {
			f.Fields = []string{"coverage_status"}
			f.ReportQuotes = []AuditReportQuote{{Field: "coverage_status", Text: "observed"}}
		},
	} {
		bad := cloneValue(f)
		change(&bad)
		if validateAuditFinding(bad, c.Report, c.Segments) == nil {
			t.Fatal("unanchored relation concern or status change accepted")
		}
	}
}

// Use the archived failure as immutable input, but author the response locally.
// This proves the review can locate prose concerns across field/type boundaries;
// it does NOT prove a real model will discover them or that the concerns are true.
func TestNarrativeSavedReportReviewKeepsRawReportAndTranscript(t *testing.T) {
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	const source = "testdata/public-courses/replays/physics-review.json"
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != "3089282b74cfb36c200eea5990858f3cc5fca02a661338e6d2be25fc42db4b8a" {
		t.Fatal("archived failure changed; do not replace it with improved output")
	}
	var archive struct {
		Analysis AnalysisOutput `json:"analysis"`
	}
	if err = json.Unmarshal(raw, &archive); err != nil {
		t.Fatal(err)
	}
	out := archive.Analysis
	// Parse the same bytes independently: a marshal/unmarshal clone normalizes
	// omitted empty slices in old audits and would not be an exact snapshot.
	var before struct {
		Analysis AnalysisOutput `json:"analysis"`
	}
	if err = json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	draft := auditFixture(out.Report)
	add := func(target, field, criterion, kind, reason string, indices ...int) {
		f := AuditFinding{Target: target, Fields: []string{field}, Criterion: criterion, Kind: kind, Reason: reason,
			ReportQuotes: []AuditReportQuote{{Field: field, Text: auditFieldText(out.Report, target, field)}}, SourceIDs: []string{}}
		for _, index := range indices {
			s := out.Segments[index]
			f.SourceIDs = append(f.SourceIDs, s.ID)
			f.SourceQuotes = append(f.SourceQuotes, AuditSourceQuote{SegmentID: s.ID, Text: s.Text})
		}
		draft.Findings = append(draft.Findings, f)
	}
	add("summary", "summary", "claim_scope", "unsupported_judgment", "片段未标师生身份，不能由这些文字认定未邀请或未检查理解。", 5, 6, 7)
	add("dimension:thinking", "summary", "relation_fidelity", "context_conflict", "原文从动量导数在恒定质量条件下化简，需核对小结描述的推导方向。", 6, 7)
	add("observation:1", "analysis", "claim_scope", "unsupported_judgment", "亮点分析也不能凭片段未区分角色，就断言只有教师展示而没有学生思考任务。", 5)
	if err = validateAudit(draft, out.Report, out.Segments); err != nil {
		t.Fatal("fixture does not accurately anchor the saved report", err)
	}
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 {
			t.Error("bad review request", err)
			return
		}
		var input struct {
			Segments []Segment `json:"original_segments"`
			Report   Report    `json:"candidate_report"`
		}
		_ = json.Unmarshal([]byte(request.Messages[1].Content), &input)
		candidate := cloneValue(out.Report)
		candidate.Audit = nil
		if !reflect.DeepEqual(input.Segments, out.Segments) || !reflect.DeepEqual(input.Report, candidate) {
			t.Error("review input rewrote archived content")
		}
		draftRaw, _ := json.Marshal(draft)
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(draftRaw)}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	a := auditReport(context.Background(), provider.URL, "fake", out.Report, out.Segments, out.Events)
	if a.Status != "needs_review" || calls != 1 || !reflect.DeepEqual(a.Findings, draft.Findings) || !reflect.DeepEqual(before.Analysis, out) {
		t.Fatal("review lost concerns, rewrote history or retried", a.Status, calls)
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(raw) {
		t.Fatal("saved report changed", err)
	}
	t.Log("mock review locates summary, thinking summary and highlight analysis; archived output unchanged; zero real model calls")
}

func TestNarrativeContrastPairsChangeOnlyExpectedField(t *testing.T) {
	cases := auditCalibrationCases()
	for i := 0; i < len(cases); i += 2 {
		clean, risky := cases[i], cases[i+1]
		if !strings.HasPrefix(clean.ID, "absence-") && !strings.HasPrefix(clean.ID, "relation-") && !strings.HasPrefix(clean.ID, "biology-") {
			continue
		}
		changed := 0
		for _, target := range reportAuditTargets(clean.Report) {
			for _, field := range target.Fields {
				if auditFieldText(clean.Report, target.Key, field) != auditFieldText(risky.Report, target.Key, field) {
					changed++
					if target.Key != risky.Target || field != risky.Field {
						t.Fatal("contrast contaminates a different field", risky.ID, target.Key, field)
					}
				}
			}
		}
		if changed != 1 || !reflect.DeepEqual(clean.Events, risky.Events) || !reflect.DeepEqual(clean.Segments, risky.Segments) || len(clean.Report.Observations) != len(risky.Report.Observations) {
			t.Fatal("contrast did not preserve source or all other fields", clean.ID)
		}
		for j, finding := range clean.Report.Observations {
			if finding.Type != risky.Report.Observations[j].Type {
				t.Fatal("contrast changed the discovery type", clean.ID)
			}
		}
	}
}
