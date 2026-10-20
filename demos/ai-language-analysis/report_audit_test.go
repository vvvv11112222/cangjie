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

// Existing protocol fixtures intentionally exercise three text stages. Audit
// tests explicitly enable the additional stage against local mock providers.
func TestMain(m *testing.M) {
	_ = os.Setenv("BAILIAN_REPORT_AUDIT", "0")
	os.Exit(m.Run())
}
func auditFixture(report Report) auditDraft {
	d := auditDraft{CheckedTargets: []string{}, Findings: []AuditFinding{}}
	for _, t := range reportAuditTargets(report) {
		d.CheckedTargets = append(d.CheckedTargets, t.Key)
	}
	return d
}
func anchoredFinding(report Report, segments []Segment, target string, fields []string) AuditFinding {
	f := AuditFinding{Target: target, Fields: fields, Kind: "subject_claim", Criterion: "subject_verification", Reason: "报告解读需结合上下文核对。", SourceIDs: []string{segments[0].ID}, SourceQuotes: []AuditSourceQuote{{SegmentID: segments[0].ID, Text: segments[0].Text}}}
	for _, field := range fields {
		f.ReportQuotes = append(f.ReportQuotes, AuditReportQuote{Field: field, Text: auditFieldText(report, target, field)})
	}
	return f
}
func TestAuditRequiresCompleteCoverageAndValidLocations(t *testing.T) {
	segments, _, report := coachingFixture()
	d := auditFixture(report)
	if err := validateAudit(d, report, segments); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*auditDraft){
		func(d *auditDraft) { d.CheckedTargets = d.CheckedTargets[1:] },
		func(d *auditDraft) { d.CheckedTargets = append(d.CheckedTargets, d.CheckedTargets[0]) },
		func(d *auditDraft) { d.Findings = nil },
		func(d *auditDraft) {
			d.Findings = []AuditFinding{{Target: "observation:99", Fields: []string{"suggestion"}, Kind: "subject_claim", Reason: "待核实", SourceIDs: []string{}}}
		},
		func(d *auditDraft) {
			d.Findings = []AuditFinding{{Target: "observation:1", Fields: []string{"unknown"}, Kind: "subject_claim", Reason: "待核实", SourceIDs: []string{}}}
		},
		func(d *auditDraft) {
			d.Findings = []AuditFinding{{Target: "summary", Fields: []string{"summary"}, Kind: "context_conflict", Reason: "与后文不符", SourceIDs: []string{}}}
		},
		func(d *auditDraft) {
			d.Findings = []AuditFinding{{Target: "summary", Fields: []string{"summary"}, Kind: "context_conflict", Reason: "与后文不符", SourceIDs: []string{"unknown"}}}
		},
	} {
		bad := cloneValue(d)
		change(&bad)
		if validateAudit(bad, report, segments) == nil {
			t.Fatal("invalid review accepted", bad)
		}
	}
}
func TestAuditDoesNotEditReportOrRetryAndDoesNotCallASR(t *testing.T) {
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	for _, mode := range []string{"flag", "clean", "http_error", "invalid", "incomplete", "misquote", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			segments, events, report := coachingFixture()
			before := cloneValue(report)
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					Stream   bool `json:"stream"`
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&req) != nil || !req.Stream || len(req.Messages) != 2 {
					t.Error("not a text-only review")
				}
				var input struct {
					Segments      []Segment     `json:"original_segments"`
					Report        Report        `json:"candidate_report"`
					Targets       []auditTarget `json:"review_targets"`
					Template      auditDraft    `json:"response_template"`
					RubricVersion string        `json:"rubric_version"`
					RubricSHA256  string        `json:"rubric_sha256"`
				}
				if json.Unmarshal([]byte(req.Messages[1].Content), &input) != nil || !reflect.DeepEqual(input.Segments, segments) || len(input.Targets) != 8 || input.Report.Audit != nil {
					t.Error("incomplete source or report input")
				}
				if validateAudit(input.Template, report, segments) != nil {
					t.Error("response template does not cover this report")
				}
				rubric, _ := assets.ReadFile("rubric.md")
				if !strings.Contains(req.Messages[0].Content, string(rubric)) || input.RubricVersion != rubricVersion || input.RubricSHA256 != fmt.Sprintf("%x", sha256.Sum256(rubric)) {
					t.Error("audit did not receive the exact shared rubric")
				}
				if mode == "http_error" {
					http.Error(w, "secret provider details", 500)
					return
				}
				d := auditFixture(report)
				if mode == "flag" {
					d.Findings = append(d.Findings, anchoredFinding(report, segments, "observation:1", []string{"analysis", "suggestion"}))
				}
				if mode == "mixed" {
					d.Findings = append(d.Findings, anchoredFinding(report, segments, "summary", []string{"summary"}))
				}
				if mode == "misquote" || mode == "mixed" {
					f := anchoredFinding(report, segments, "observation:1", []string{"analysis"})
					f.ReportQuotes[0].Text = "把报告没有说过的话当作待核对原句"
					d.Findings = append(d.Findings, f)
				}
				if mode == "incomplete" {
					d.CheckedTargets = d.CheckedTargets[1:]
				}
				raw, _ := json.Marshal(d)
				if mode == "invalid" {
					raw = []byte("{broken")
				}
				chunk, _ := json.Marshal(map[string]any{"id": "audit-local", "choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer provider.Close()
			a := auditReport(context.Background(), provider.URL, "fake", report, segments, events)
			if a.Version != reportAuditVersion || a.RubricVersion != rubricVersion || a.RubricSHA256 == "" {
				t.Fatal("review standards not traceable")
			}
			want := "unavailable"
			if mode == "clean" {
				want = "no_flags"
			}
			if mode == "flag" {
				want = "needs_review"
			}
			if mode == "mixed" || mode == "misquote" {
				want = "partial_review"
			}
			if a.Status != want || calls != 1 || !reflect.DeepEqual(before, report) || strings.Contains(a.Message, "secret") {
				t.Fatalf("status=%s calls=%d report mutated=%v", a.Status, calls, !reflect.DeepEqual(before, report))
			}
			if mode != "http_error" && a.RawResponse == "" {
				t.Fatal("raw audit response missing from diagnostic record")
			}
			if want == "unavailable" && (a.Diagnostic == "" || strings.Contains(a.Diagnostic, "secret")) {
				t.Fatal("missing or unsafe failure diagnostic")
			}
			if want == "partial_review" {
				if len(a.RejectedFindings) != 1 || (mode == "mixed" && len(a.Findings) != 1) || (mode == "misquote" && len(a.Findings) != 0) {
					t.Fatal("unlocated findings not isolated", a)
				}
			}
		})
	}
}
func TestPipelinePublishesAuditWithImmutableVersion(t *testing.T) {
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	segments, events, report := coachingFixture()
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var answer any
		switch calls {
		case 1:
			answer = map[string]any{"boundaries": []any{}}
		case 2:
			answer = events
		case 3:
			answer = draftFixture(report, events.Events)
		case 4:
			d := auditFixture(report)
			d.Findings = []AuditFinding{anchoredFinding(report, segments, "observation:1", []string{"analysis"})}
			answer = d
		default:
			t.Error("automatic extra request")
		}
		raw, _ := json.Marshal(answer)
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	t.Setenv("BAILIAN_BASE_URL", provider.URL)
	t.Setenv("DASHSCOPE_API_KEY", "fake")
	store, e := newJobStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	_, e = store.create("audit-only", "unused", "test.mp4")
	if e != nil {
		t.Fatal(e)
	}
	if e = store.initialRevision("audit-only", segments); e != nil {
		t.Fatal(e)
	}
	_ = store.update("audit-only", func(j *Job) error { j.Mode = "report_only"; j.InputRevision = 1; j.ASRStatus = asrComplete; return nil })
	m := testManager(store)
	m.run("audit-only")
	job, _ := store.get("audit-only")
	if calls != 4 || job.Status != jobSucceeded || job.Report.Audit.Status != "needs_review" || len(job.ReportVersions) != 1 || !reflect.DeepEqual(job.Segments, segments) {
		t.Fatal("audit pipeline/storage mismatch", calls, job.Status)
	}
	reopened, e := newJobStore(store.root)
	if e != nil {
		t.Fatal(e)
	}
	saved, _ := reopened.get("audit-only")
	if saved.ReportVersions[0].Report.Audit.Status != "needs_review" || len(saved.ReportReviews) != 0 {
		t.Fatal("audit lost or counted as human review")
	}
}
