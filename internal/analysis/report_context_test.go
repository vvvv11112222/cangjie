package analysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVisualContextKeepsUnknownAndBoundedTimeWindows(t *testing.T) {
	one := 1
	result := VideoResult{Visual: &VisualData{Counts: []VisualCount{
		{TimestampMS: 0}, {TimestampMS: 500, VisibleCount: &one, HandRaiseCount: &one},
	}}}
	items := summarizeVisualContext(result, "source", "sha")
	if len(items) != 1 || !strings.Contains(items[0].Text, "低头最多看不清") || items[0].EndMS != 501 || items[0].Provenance["needs_review"] != true {
		t.Fatalf("unknown was lost: %+v", items)
	}
	for i := 0; i < 15000; i++ {
		result.Visual.Counts = append(result.Visual.Counts, VisualCount{TimestampMS: int64(i+2) * 500})
	}
	if items = summarizeVisualContext(result, "source", "sha"); len(items) > 24 {
		t.Fatalf("unbounded model material: %d", len(items))
	}
}

func TestContextOnlyBackgroundDoesNotExpandObservedCoverage(t *testing.T) {
	coverage := coverageForEvidence([]modelEvidence{{StartMS: 20000, EndMS: 21000}, {StartMS: 0, EndMS: 1, ContextOnly: true}, {StartMS: 10000, EndMS: 11000}})
	if len(coverage) != 2 || coverage[0].StartMS != 10000 || coverage[1].StartMS != 20000 {
		t.Fatalf("background/order fabricated coverage: %+v", coverage)
	}
}

func TestComprehensivePromptIsVersionedAndPreservesLegacy(t *testing.T) {
	text, sha, err := ReportPrompt(ComprehensivePromptVersion)
	if err != nil || !strings.Contains(text, "behavior") || !strings.Contains(text, "课表") || !strings.Contains(text, "不证明") {
		t.Fatal("missing comprehensive interpretation rules")
	}
	if err = ValidateReportPrompt(true, ComprehensivePromptVersion, sha); err != nil {
		t.Fatal(err)
	}
	if err = ValidateReportPrompt(true, ComprehensivePromptVersion, "wrong"); err == nil {
		t.Fatal("prompt digest mismatch accepted")
	}
	old, _, _ := ReportPrompt(TeacherPromptVersion)
	if old != teacherReportPrompt || old == text {
		t.Fatal("legacy prompt was replaced")
	}
}

func TestComprehensiveCandidateCannotIgnoreSuppliedVisualEvidence(t *testing.T) {
	input := modelInput{PromptVersion: ComprehensivePromptVersion, Coverage: []Interval{{StartMS: 0, EndMS: 1000}}, Evidence: []modelEvidence{{ID: "speech", Kind: "transcript"}, {ID: "visual", Kind: "behavior"}}}
	candidate := teacherCandidate(input)
	if err := validateCandidate(candidate, input); err == nil {
		t.Fatal("comprehensive report silently omitted visual evidence")
	}
	candidate.SummaryEvidenceIDs = append(candidate.SummaryEvidenceIDs, "visual")
	if err := validateCandidate(candidate, input); err != nil {
		t.Fatal(err)
	}
}

func TestReportContextPinsSameMediaVisualAndAcademicEvidence(t *testing.T) {
	f := setupReportFixture(t)
	s, j := prepareBillingReport(t, f)
	ctx := context.Background()
	one := 1
	result := VideoResult{Visual: &VisualData{InputSHA256: strings.Repeat("a", 64), Counts: []VisualCount{{TimestampMS: 0, VisibleCount: &one, HandRaiseCount: &one}}}, Limitations: []string{"合成视觉测试，未验收识别精度"}}
	seedVisual := func(mediaID, key string) string {
		t.Helper()
		var runID, jobID string
		err := f.pool.QueryRow(ctx, `INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,pipeline_version,config_snapshot,mode,status,request_sha256,finished_at)
			VALUES($1,$2,$3,$4,$5,$5,'vision-v1','{}','visual_only','succeeded',$5,now()) RETURNING id::text`, f.session, mediaID, f.admin.UserID, key, strings.Repeat("a", 64)).Scan(&runID)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.pool.QueryRow(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,progress) VALUES($1,'video_analysis','succeeded',1,100) RETURNING id::text`, runID).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(result)
		if _, err = f.pool.Exec(ctx, `INSERT INTO teaching.job_artifacts(job_id,run_id,session_id,lease_token,artifact_key,metadata) VALUES($1,$2,$3,gen_random_uuid(),'__result/video_analysis',jsonb_build_object('result',$4::jsonb))`, jobID, runID, f.session, raw); err != nil {
			t.Fatal(err)
		}
		return runID
	}
	wanted := seedVisual(f.media, "same-video")
	var otherMedia string
	if err := f.pool.QueryRow(ctx, `INSERT INTO teaching.media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,duration_ms,status) VALUES($1,$2,'source','filesystem','other.mp4',$3,'video/mp4',12,60000,'ready') RETURNING id::text`, f.session, f.source, strings.Repeat("a", 64)).Scan(&otherMedia); err != nil {
		t.Fatal(err)
	}
	seedVisual(otherMedia, "newer-other-video")
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.captureReportContext(ctx, tx, f.session, f.media, strings.Repeat("a", 64))
	_ = tx.Rollback(ctx)
	if err != nil || c.VisualRunID != wanted || len(c.Evidence) < 2 || c.Academic["teacher_name"] == "" {
		t.Fatalf("wrong material selection: %+v %v", c, err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE teaching.courses SET name='changed after submission'`); err != nil {
		t.Fatal(err)
	}
	_, promptSHA, _ := ReportPrompt(ComprehensivePromptVersion)
	s.cfg.ReportPromptVersion, s.cfg.ReportPromptSHA256 = ComprehensivePromptVersion, promptSHA
	s.cfg.ReportMaxInputTokens, s.cfg.ReportMaxOutputTokens = 8000, 2000
	snap := s.newSnapshot("full")
	snap.Report.Context = c
	raw, _ := json.Marshal(snap)
	if _, err = f.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET config_snapshot=$2 WHERE id=$1`, f.run, raw); err != nil {
		t.Fatal(err)
	}
	tx, err = f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.insertReportContextEvidence(ctx, tx, j); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	input, err := s.buildModelInput(ctx, j)
	if err != nil || input.CourseContext["course_name"] == "changed after submission" {
		t.Fatalf("context was not frozen: %v", err)
	}
	kinds := map[string]int{}
	for _, e := range input.Evidence {
		kinds[e.Kind]++
		var run string
		if err = f.pool.QueryRow(ctx, `SELECT run_id::text FROM teaching.evidence_items WHERE id=$1`, e.ID).Scan(&run); err != nil || run != f.run {
			t.Fatal("foreign report evidence")
		}
	}
	if kinds["behavior"] == 0 || kinds["transcript"] == 0 || kinds["manual"] == 0 {
		t.Fatalf("missing modality: %+v", kinds)
	}
	// Exercise request, model validation, billing, persistence and public read.
	requests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var sent modelInput
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &sent); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		candidate := teacherCandidate(sent)
		for _, e := range sent.Evidence {
			if e.Kind == "behavior" {
				candidate.SummaryEvidenceIDs = append(candidate.SummaryEvidenceIDs, e.ID)
				break
			}
		}
		payload, _ := json.Marshal(candidate)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-context", "usage": map[string]int{"prompt_tokens": 500, "completion_tokens": 200}, "choices": []any{map[string]any{"message": map[string]string{"content": string(payload)}}}})
	}))
	defer provider.Close()
	s.cfg.ReportAPIBase = provider.URL
	if _, err = f.pool.Exec(ctx, `DELETE FROM teaching.reports WHERE id=$1`, f.report); err != nil {
		t.Fatal(err)
	}
	if err = s.executeReport(ctx, j); err != nil {
		t.Fatal(err)
	}
	if did, err := s.ProcessNextGoJob(ctx); err != nil || !did {
		t.Fatalf("validate: %v %v", did, err)
	}
	var reportID string
	if err = f.pool.QueryRow(ctx, `SELECT id::text FROM teaching.reports WHERE run_id=$1`, f.run).Scan(&reportID); err != nil {
		t.Fatal(err)
	}
	report, err := s.GetReport(ctx, f.admin, reportID)
	if err != nil || requests != 1 || report.Provenance["included_materials"] == nil {
		t.Fatalf("comprehensive report failed: %v calls=%d", err, requests)
	}
	// Missing/mismatched video is a disclosed absence, never a zero-action claim.
	tx, err = f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := s.captureReportContext(ctx, tx, f.session, f.media, strings.Repeat("b", 64))
	_ = tx.Rollback(ctx)
	if err != nil || missing.VisualRunID != "" || len(missing.Limitations) != 1 || !strings.Contains(missing.Limitations[0], "尚无已完成") {
		t.Fatalf("missing visual was fabricated: %+v %v", missing, err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE teaching.job_artifacts SET metadata=jsonb_set(metadata,'{result,limitations}','["changed"]') WHERE run_id=$1`, wanted); err != nil {
		t.Fatal(err)
	}
	tx, err = f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.insertReportContextEvidence(ctx, tx, j); err == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("modified fixed visual was accepted")
	}
	_ = tx.Rollback(ctx)
}
