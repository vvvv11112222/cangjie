package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Curated public inputs are shipped with the source; local experiment runs are not.
const publicCourseRoot = "testdata/public-courses"

type publicCourseSnapshot struct {
	Segments []Segment `json:"segments"`
	Events   EventSet  `json:"events"`
	Report   Report    `json:"report"`
}

type publicCourseEvidence struct {
	Key          string `json:"key"`
	Source       string `json:"source"`
	SegmentIndex int    `json:"segment_index"`
	Start        int64  `json:"start_ms"`
	End          int64  `json:"end_ms"`
	Quote        string `json:"quote"`
}

func publicCourseFiles(t *testing.T) (map[string][]byte, map[string]publicCourseEvidence) {
	t.Helper()
	var record struct {
		Sources  []struct{ Key, Path, SHA256 string }
		Evidence []publicCourseEvidence
	}
	read := func(path string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(publicCourseRoot, path))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := json.Unmarshal(read("AI辅助验收记录.json"), &record); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, source := range record.Sources {
		b := read(source.Path)
		if fmt.Sprintf("%x", sha256.Sum256(b)) != source.SHA256 {
			t.Fatalf("archived source changed: %s", source.Key)
		}
		files[source.Key] = b
	}
	evidence := map[string]publicCourseEvidence{}
	for _, e := range record.Evidence {
		evidence[e.Key] = e
	}
	if len(files) != 4 || len(evidence) != 15 {
		t.Fatal("fixed review sources or quotations are missing")
	}
	return files, evidence
}

func publicCourseReport(t *testing.T, b []byte) publicCourseSnapshot {
	t.Helper()
	var snapshot publicCourseSnapshot
	if err := json.Unmarshal(b, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Segments) != 8 || len(snapshot.Report.Dimensions) != 6 {
		t.Fatal("incomplete archived report input")
	}
	return snapshot
}

// These are provenance checks against fixed AI-reviewed archives, not teacher
// labels or an evaluation of a live model's semantic accuracy.
func TestPublicCourseReviewProvenance(t *testing.T) {
	files, evidence := publicCourseFiles(t)
	for key, e := range evidence {
		t.Run(key, func(t *testing.T) {
			if e.SegmentIndex == 0 {
				stamp := func(ms int64) string {
					return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
				}
				want := stamp(e.Start) + " --> " + stamp(e.End)
				for _, cue := range strings.Split(strings.ReplaceAll(string(files[e.Source]), "\r\n", "\n"), "\n\n") {
					if strings.Contains(cue, want) && strings.Contains(strings.Join(strings.Fields(cue), " "), e.Quote) {
						return
					}
				}
				t.Fatal("caption quote or timing changed")
			}
			s := publicCourseReport(t, files[e.Source]).Segments[e.SegmentIndex-1]
			if s.Start != e.Start || s.End != e.End || !strings.Contains(s.Text, e.Quote) {
				t.Fatal("original segment quote or timing changed")
			}
		})
	}
}

func TestPublicCourseCoverageStatusCanBeAudited(t *testing.T) {
	files, evidence := publicCourseFiles(t)
	snapshot := publicCourseReport(t, files["physics_report"])
	f := anchoredFinding(snapshot.Report, snapshot.Segments, "dimension:management", []string{"coverage_status"})
	f.Criterion, f.SuggestedCoverage = "dimension_definition", "insufficient"
	e := evidence["physics_two_points"]
	s := snapshot.Segments[e.SegmentIndex-1]
	f.Kind, f.Reason = "context_conflict", "强调两点属于讲解安排，不能仅凭这一句认定有学生任务；请核对活动组织的依据状态。"
	f.SourceIDs, f.SourceQuotes = []string{s.ID}, []AuditSourceQuote{{SegmentID: s.ID, Text: e.Quote}}
	if f.ReportQuotes[0].Text != "observed" {
		t.Fatal("historical classification is no longer the expected regression input")
	}
	if err := validateAuditFinding(f, snapshot.Report, snapshot.Segments); err != nil {
		t.Fatal("coverage classification cannot be pinpointed", err)
	}
	for _, quote := range []string{"insufficient", "observe", "有相关文字证据"} {
		bad := cloneValue(f)
		bad.ReportQuotes[0].Text = quote
		if validateAuditFinding(bad, snapshot.Report, snapshot.Segments) == nil {
			t.Fatal("inexact classification quote accepted", quote)
		}
	}
}

// A local provider replays authored findings. It verifies full-context request
// delivery, accepted locations and immutability, not model reasoning quality.
func TestPublicCourseAuditReplaysFullContext(t *testing.T) {
	t.Setenv("BAILIAN_REPORT_AUDIT", "1")
	files, evidence := publicCourseFiles(t)
	for _, source := range []string{"physics_report", "biology_report"} {
		t.Run(source, func(t *testing.T) {
			snapshot := publicCourseReport(t, files[source])
			before, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			draft := auditFixture(snapshot.Report)
			add := func(target, field, evidenceKey, reason string) {
				f := anchoredFinding(snapshot.Report, snapshot.Segments, target, []string{field})
				e := evidence[evidenceKey]
				s := snapshot.Segments[e.SegmentIndex-1]
				f.Kind, f.Reason = "context_conflict", reason
				f.Criterion = "context_consistency"
				if field == "coverage_status" {
					f.Criterion, f.SuggestedCoverage = "dimension_definition", "insufficient"
				}
				f.SourceIDs, f.SourceQuotes = []string{s.ID}, []AuditSourceQuote{{SegmentID: s.ID, Text: e.Quote}}
				draft.Findings = append(draft.Findings, f)
			}
			if source == "physics_report" {
				add("dimension:management", "coverage_status", "physics_two_points", "讲解安排不等于要求学生完成任务，需核对活动组织依据状态。")
			} else {
				add("observation:1", "analysis", "biology_goal_return", "后文再次联系血红蛋白与疾病，不能说没有显性回扣。")
				add("observation:2", "analysis", "biology_responses", "后块有热、pH及承接语，不能概括为没有回答内容；仍应保留角色不明确的限制。")
				add("observation:4", "analysis", "biology_reform", "前块已交代重新形成与高热聚集，例子应与该解释合读，再判断边界是否遗漏。")
			}
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/chat/completions" {
					t.Error("unexpected request; replay permits text review only")
				}
				var req struct{ Messages []struct{ Content string } }
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 2 {
					t.Error("invalid text request", err)
					http.Error(w, "bad fixture request", 400)
					return
				}
				var input struct {
					Segments       []Segment     `json:"original_segments"`
					Events         EventSet      `json:"events"`
					Report         Report        `json:"candidate_report"`
					Targets        []auditTarget `json:"review_targets"`
					Template       auditDraft    `json:"response_template"`
					AdviceProtocol string        `json:"advice_protocol"`
				}
				if err := json.Unmarshal([]byte(req.Messages[1].Content), &input); err != nil {
					t.Error(err)
				}
				candidate := cloneValue(snapshot.Report)
				candidate.Audit = nil
				if !reflect.DeepEqual(input.Segments, snapshot.Segments) || !reflect.DeepEqual(input.Events, snapshot.Events) || !reflect.DeepEqual(input.Report, candidate) {
					t.Error("original context lost, rewritten or polluted with historical audit")
				}
				if input.AdviceProtocol != adviceProtocol || validateAudit(input.Template, candidate, input.Segments) != nil {
					t.Error("incomplete review protocol")
				}
				for _, target := range input.Targets {
					for _, field := range target.Fields {
						if target.FieldRoles[field] == "" {
							t.Error("field has no review role", field)
						}
					}
					if strings.HasPrefix(target.Key, "dimension:") && !contains(target.Fields, "coverage_status") {
						t.Error("dimension status omitted")
					}
					if strings.HasPrefix(target.Key, "observation:") && (!strings.Contains(target.FieldRoles["suggestion"], "未来") || !strings.Contains(target.FieldRoles["verification"], "未来")) {
						t.Error("future advice/checks not distinguished from classroom facts")
					}
				}
				raw, _ := json.Marshal(draft)
				chunk, _ := json.Marshal(map[string]any{"id": "public-course-offline", "choices": []any{map[string]any{"delta": map[string]string{"content": string(raw)}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer provider.Close()
			audit := auditReport(context.Background(), provider.URL, "offline-fixture", snapshot.Report, snapshot.Segments, snapshot.Events)
			if calls != 1 || audit.Status != "needs_review" || !reflect.DeepEqual(audit.Findings, draft.Findings) || len(audit.RejectedFindings) != 0 {
				t.Fatalf("unexpected replay result: calls=%d status=%s diagnostic=%s", calls, audit.Status, audit.Diagnostic)
			}
			after, err := json.Marshal(snapshot)
			if err != nil || string(before) != string(after) {
				t.Fatal("archived input changed during review")
			}
		})
	}
}
