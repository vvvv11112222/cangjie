package analysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vvvv11112222/cangjie/internal/identity"
)

func TestTeacherPromptIdentityAndOptIn(t *testing.T) {
	text, sha, err := ReportPrompt(TeacherPromptVersion)
	if err != nil || len(text) == 0 || sha != digestBytes([]byte(text)) {
		t.Fatalf("invalid prompt identity: %v", err)
	}
	if err = ValidateReportPrompt(true, TeacherPromptVersion, sha); err != nil {
		t.Fatal(err)
	}
	if err = ValidateReportPrompt(true, TeacherPromptVersion, strings.Repeat("a", 64)); err == nil {
		t.Fatal("mismatched prompt was enabled")
	}
	if err = ValidateReportPrompt(true, "unknown-template", sha); err == nil {
		t.Fatal("unknown prompt was enabled")
	}
	if err = ValidateReportPrompt(false, "", ""); err != nil {
		t.Fatal("disabled report should not require a prompt")
	}
	example, err := os.ReadFile("../../.env.example")
	if err != nil || !strings.Contains(string(example), "REPORT_PROMPT_SHA256="+sha) {
		t.Fatalf("configuration example does not identify the bundled prompt: %v", err)
	}
	legacy, _, err := ReportPrompt("p0-v1")
	if err != nil || legacy != reportSystemPrompt {
		t.Fatal("legacy template changed")
	}
}

func teacherCandidate(input modelInput) ReportCandidate {
	ids := []string{input.Evidence[0].ID}
	candidate := ReportCandidate{Summary: "教师先复习结构层次，再讨论变性条件；可复盘学生如何解释二者的联系。", SummaryEvidenceIDs: ids}
	for _, code := range dimensionCodes {
		d := Dimension{DimensionCode: code, CoverageStatus: "insufficient", Limitation: "只有局部转写，无法判断本维度的具体教学效果。", Coverage: []Interval{}, SummaryEvidenceIDs: []string{}}
		if code == "content" || code == "pace" {
			d.CoverageStatus, d.Summary, d.Limitation = "observed", "回顾结构层次→讨论影响结构稳定的条件。", "仅分析所提供的片段。"
			d.Coverage, d.SummaryEvidenceIDs = input.Coverage, ids
		}
		candidate.Dimensions = append(candidate.Dimensions, d)
	}
	candidate.Observations = []CandidateObservation{{DimensionCode: "content", ObservationType: "observation", ObservationText: "教师把结构层次与变性条件接续讲解。转写未记录学生的解释，尚不能确认学生是否理解两者关系。", Suggestion: "做法：讲完条件后请学生说明一种条件改变了哪些相互作用。\n课堂示例：变性后一级结构一定会改变吗？为什么？\n观察效果：听学生能否区分空间结构变化和肽键断裂。", EvidenceIDs: ids}}
	return candidate
}

func TestTeacherIssueRequiresSuggestionAndActualEvidence(t *testing.T) {
	input := modelInput{PromptVersion: TeacherPromptVersion, Evidence: []modelEvidence{{ID: "sent"}}, Coverage: []Interval{{StartMS: 0, EndMS: 1000}}}
	candidate := teacherCandidate(input)
	if err := validateCandidate(candidate, input); err != nil {
		t.Fatal(err)
	}
	candidate.Observations[0].ObservationType, candidate.Observations[0].Suggestion = "issue", " "
	if err := validateCandidate(candidate, input); err == nil {
		t.Fatal("issue without advice accepted")
	}
	candidate.Observations[0].Suggestion = "请学生解释原因。"
	candidate.Observations[0].EvidenceIDs = []string{"not-sent"}
	if err := validateCandidate(candidate, input); err == nil {
		t.Fatal("advice allowed a fabricated evidence reference")
	}
}

// This crosses the actual request, budget, validation, persistence and read boundary.
// The provider is local and synthetic; it does not measure model output quality.
func TestTeacherReportNativePipeline(t *testing.T) {
	f := setupReportFixture(t)
	s, job := prepareBillingReport(t, f)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `DELETE FROM teaching.reports WHERE id=$1`, f.report); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.evidence_items SET description='先复习蛋白质结构层次，再讨论哪些条件会改变其结构。' WHERE id=$1`, f.evidence); err != nil {
		t.Fatal(err)
	}
	text, sha, _ := ReportPrompt(TeacherPromptVersion)
	s.cfg.ReportPromptVersion, s.cfg.ReportPromptSHA256 = TeacherPromptVersion, sha
	raw, _ := json.Marshal(s.newSnapshot("full"))
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET config_snapshot=$2 WHERE id=$1`, f.run, raw); err != nil {
		t.Fatal(err)
	}
	requests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 {
			t.Error("invalid model request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var input modelInput
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil || len(input.Evidence) != 1 || request.Messages[0].Content != text || input.PromptSHA256 != sha {
			t.Error("request lost pinned prompt or exact evidence")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		candidate, _ := json.Marshal(teacherCandidate(input))
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-teacher-report", "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 50}, "choices": []any{map[string]any{"message": map[string]string{"content": string(candidate)}}}})
	}))
	defer provider.Close()
	s.cfg.ReportAPIBase = provider.URL
	if err := s.executeReport(ctx, job); err != nil {
		t.Fatal(err)
	}
	// A runtime configuration change must not replace the queued run's snapshot.
	s.cfg.ReportPromptVersion, s.cfg.ReportPromptSHA256 = "p0-v1", "changed"
	if snap, err := s.reportConfigForRun(ctx, f.run); err != nil || snap.SystemPrompt != text || snap.PromptSHA256 != sha {
		t.Fatalf("run was not pinned: %v", err)
	}
	if did, err := s.ProcessNextGoJob(ctx); err != nil || !did {
		t.Fatalf("validate job did=%v err=%v", did, err)
	}
	var reportID, billStatus string
	if err := f.pool.QueryRow(ctx, `SELECT id::text FROM teaching.reports WHERE run_id=$1 AND status='draft'`, f.run).Scan(&reportID); err != nil {
		t.Fatal(err)
	}
	report, err := s.GetReport(ctx, f.admin, reportID)
	if err != nil || report.Status != "draft" || len(report.Dimensions) != 6 || len(report.Observations) != 1 || !strings.Contains(report.Observations[0].Suggestion, "课堂示例：") || report.Observations[0].ReviewStatus != "unreviewed" || report.ReviewedContentSHA256 != nil {
		t.Fatalf("teacher report lost advice or review gate: %+v err=%v", report, err)
	}
	// Report provenance is snake_case, as required by the public contract.
	if report.Provenance["prompt_version"] != TeacherPromptVersion || report.Provenance["prompt_sha256"] != sha {
		t.Fatalf("report provenance mismatch: %+v", report.Provenance)
	}
	if _, err := s.GetReport(ctx, identity.Principal{}, reportID); err == nil {
		t.Fatal("report was readable without permission")
	}
	if err := f.pool.QueryRow(ctx, `SELECT status FROM teaching.model_calls WHERE job_id=$1`, job.ID).Scan(&billStatus); err != nil || billStatus != "succeeded" || requests != 1 {
		t.Fatalf("expected one settled call, requests=%d status=%s err=%v", requests, billStatus, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET config_snapshot=jsonb_set(config_snapshot,'{report,system_prompt}','"tampered"'::jsonb) WHERE id=$1`, f.run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.reportConfigForRun(ctx, f.run); err == nil {
		t.Fatal("tampered prompt snapshot was accepted")
	}
}
