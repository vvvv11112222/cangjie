package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/classroom"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/identity"
	mediaservice "github.com/vvvv11112222/cangjie/internal/media"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

func TestPhaseFourAnalysisWorkerLeaseAndRecovery(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	fixtureLock, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixtureLock.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('cangjie_integration_test_fixture',0))`); err != nil {
		t.Fatal(err)
	}
	defer fixtureLock.Close(context.Background()) //nolint:errcheck
	migrationDir, _ := filepath.Abs(filepath.Join("..", "..", "database"))
	if err = database.RunMigrations(ctx, databaseURL, migrationDir); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `TRUNCATE teaching.academic_terms,teaching.classrooms,teaching.org_units CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err = identity.BootstrapAdmin(ctx, pool, identity.BootstrapRequest{SchoolCode: "SCHOOL", SchoolName: "School", Username: "admin", DisplayName: "Admin", Password: integrationPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err = identity.BootstrapDevelopmentUsers(ctx, pool, identity.DevelopmentUsersRequest{SchoolCode: "SCHOOL", SchoolName: "School", CollegeCode: "COL-A", CollegeName: "College A", AcademicUsername: "academic", AcademicDisplayName: "Academic", SupervisorUsername: "supervisor", SupervisorDisplayName: "Supervisor", TeacherUsername: "teacher", TeacherDisplayName: "Teacher", Password: integrationPassword}); err != nil {
		t.Fatal(err)
	}
	college := queryID(t, pool, `SELECT id::text FROM teaching.org_units WHERE code='COL-A'`)
	teacherID := queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='teacher'`)
	term := queryID(t, pool, `INSERT INTO teaching.academic_terms(code,name,start_date,end_date) VALUES('P4-T','P4','2026-09-01','2027-01-01') RETURNING id::text`)
	course := queryID(t, pool, `INSERT INTO teaching.courses(org_unit_id,code,name) VALUES('`+college+`','P4-C','P4') RETURNING id::text`)
	group := queryID(t, pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES('`+college+`','P4-G','P4',2026) RETURNING id::text`)
	offering := queryID(t, pool, `INSERT INTO teaching.course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id) VALUES('`+college+`','P4-O','`+term+`','`+course+`','`+teacherID+`','`+group+`') RETURNING id::text`)
	sessionID := queryID(t, pool, `INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by) VALUES('`+offering+`','P4',now(),now()+interval '1 hour','`+teacherID+`') RETURNING id::text`)
	sourceID := queryID(t, pool, `INSERT INTO teaching.source_records(session_id,source_type,title,attribution,rights_status,allowed_uses,external_processing_allowed,registered_by,verified_by,verified_at) VALUES('`+sessionID+`','self_recorded','P4','team','verified','["playback","analysis"]',true,'`+teacherID+`','`+teacherID+`',now()) RETURNING id::text`)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	video := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'a', 'v', 'c', '1'}
	staged, err := store.Stage(ctx, bytes.NewReader(video), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Commit(staged, "source/p4.mp4"); err != nil {
		t.Fatal(err)
	}
	mediaID := queryID(t, pool, `INSERT INTO teaching.media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,status) VALUES('`+sessionID+`','`+sourceID+`','source','filesystem','source/p4.mp4','`+strings.Repeat("a", 64)+`','video/mp4',24,'pending') RETURNING id::text`)
	workerToken := "0123456789abcdef0123456789abcdef"
	reportProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 {
			t.Errorf("invalid report request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var input struct {
			Evidence []struct {
				ID string `json:"id"`
			} `json:"evidence"`
		}
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil || len(input.Evidence) != 1 {
			t.Errorf("invalid model input: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		evidenceID := input.Evidence[0].ID
		dimensions := []map[string]any{{"dimension_code": "content", "coverage_status": "observed", "summary": "课堂介绍了固定主题", "limitation": "", "coverage": []map[string]any{{"start_ms": 1000, "end_ms": 3000}}, "summary_evidence_ids": []string{evidenceID}}}
		for _, code := range []string{"pace", "thinking", "expression", "management", "technology"} {
			dimensions = append(dimensions, map[string]any{"dimension_code": code, "coverage_status": "insufficient", "summary": "", "limitation": "当前仅有文本证据", "coverage": []any{}, "summary_evidence_ids": []any{}})
		}
		candidate := map[string]any{"summary": "固定转写课堂摘要", "summary_evidence_ids": []string{evidenceID}, "dimensions": dimensions, "observations": []map[string]any{{"dimension_code": "content", "observation_type": "observation", "observation_text": "教师讲解了固定转写主题。", "suggestion": "", "evidence_ids": []string{evidenceID}}}}
		content, _ := json.Marshal(candidate)
		writeJSON(w, http.StatusOK, map[string]any{"id": "fixture-request", "choices": []map[string]any{{"message": map[string]any{"content": string(content)}}}, "usage": map[string]any{"prompt_tokens": 120, "completion_tokens": 80}})
	}))
	defer reportProvider.Close()
	cfg := analysis.Config{Lease: 2 * time.Second, ProbeTimeout: time.Minute, ASRTimeout: time.Minute, VideoTimeout: time.Minute, GoStageTimeout: time.Minute, MediaJobAttempts: 3, ProcessorVersion: "fixed-json-v1", FFmpegSHA256: strings.Repeat("b", 64), ASRModelName: "fixed-json-asr", ASRModelRevision: "p0-v1", ASRDevice: "cpu", KeyframeIntervalMS: 30000, MaxKeyframes: 240, MaxVideoHeight: 1080, MaxMediaDurationMS: 7200000, MaxArtifactBytes: 1 << 20, ReportEnabled: true, ReportAPIBase: reportProvider.URL, ReportAPIKey: "fixture-key", ReportModel: "synthetic-report", ReportModelRevision: "fixture-1", ReportPromptVersion: "p0-v1", ReportPromptSHA256: strings.Repeat("d", 64), ReportSelectionVersion: "time-window-v1", ReportPriceVersion: "fixture-price", ReportBudgetCurrency: "CNY", ReportBudgetTimezone: "Asia/Shanghai", ReportMonthlyBudgetMicros: 100_000_000, ReportInputPriceMicros: 1_000_000, ReportOutputPriceMicros: 2_000_000, ReportTimeout: time.Minute, ReportMaxInputTokens: 8000, ReportMaxOutputTokens: 3000}
	analysisService := analysis.NewService(pool, store, cfg)
	handler := New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Identity: identity.NewService(pool, 8*time.Hour), Academic: academic.NewService(pool), Classroom: classroom.NewService(pool), Media: mediaservice.NewService(pool, store, 1<<20, 14*24*time.Hour), Analysis: analysisService, PublicOrigin: "http://frontend.test", WorkerToken: workerToken, WorkerID: "worker-01", WorkerCapabilities: []string{"probe", "audio_analysis", "video_analysis"}, MaxArtifactBytes: 1 << 20})
	server := httptest.NewServer(handler)
	defer server.Close()
	teacher := newTestClient(t)
	csrf := loginTestUser(t, teacher, server.URL, "teacher")
	supervisor := newTestClient(t)
	supervisorCSRF := loginTestUser(t, supervisor, server.URL, "supervisor")
	admin := newTestClient(t)
	adminCSRF := loginTestUser(t, admin, server.URL, "admin")

	create := map[string]any{"media_asset_id": mediaID, "mode": "media_prepare", "input_transcript_revision_id": nil, "config_profile": "p0-v1"}
	requestWithHeaders(t, supervisor, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", create, supervisorCSRF, map[string]string{"Idempotency-Key": "supervisor-denied"}, http.StatusForbidden)
	run := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", create, csrf, map[string]string{"Idempotency-Key": "prepare-1"}, http.StatusAccepted)
	runID := run["id"].(string)
	replay := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", create, csrf, map[string]string{"Idempotency-Key": "prepare-1"}, http.StatusOK)
	if replay["id"] != runID {
		t.Fatalf("idempotent replay returned %v", replay["id"])
	}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, "", "", http.StatusUnauthorized)
	claim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
	jobID := claim["job_id"].(string)
	lease := claim["lease_token"].(string)
	if _, err = pool.Exec(ctx, `UPDATE teaching.analysis_runs SET config_snapshot=config_snapshot-'media_limits' WHERE id=$1`, runID); err != nil {
		t.Fatal(err)
	}
	requestWorkerContent(t, server.URL+claim["input_url"].(string), workerToken, lease, "bytes=4-7", http.StatusPartialContent, []byte("ftyp"))
	probe := map[string]any{"schema_version": "1.1", "job_id": jobID, "run_id": runID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "probe", "duration_ms": 60000, "has_audio": true, "width": 1280, "height": 720, "video_codec": "h264", "playback_asset_id": mediaID, "origin_offset_ms": 0, "limitations": []string{}, "execution": claim["execution"]}
	completed := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+jobID+"/complete", probe, workerToken, lease, http.StatusOK)
	if completed["duplicate"] != false {
		t.Fatal("first completion reported duplicate")
	}
	duplicate := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+jobID+"/complete", probe, workerToken, lease, http.StatusOK)
	if duplicate["duplicate"] != true {
		t.Fatal("repeat completion was not idempotent")
	}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+jobID+"/heartbeat", map[string]any{"progress": 50}, workerToken, lease, http.StatusConflict)
	ready := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+runID, nil, "", http.StatusOK)
	if ready["status"] != "succeeded" {
		t.Fatalf("prepare status=%v", ready["status"])
	}
	prepareResults := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+runID+"/results", nil, "", http.StatusOK)
	if prepareResults["run_status"] != "succeeded" {
		t.Fatalf("playback-only prepare results=%#v", prepareResults)
	}

	full := map[string]any{"media_asset_id": mediaID, "mode": "full", "input_transcript_revision_id": nil, "config_profile": "p0-v1"}
	fullRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", full, csrf, map[string]string{"Idempotency-Key": "full-1"}, http.StatusAccepted)
	fullID := fullRun["id"].(string)
	probeClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
	probePayload := map[string]any{"schema_version": "1.1", "job_id": probeClaim["job_id"], "run_id": fullID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "probe", "duration_ms": 60000, "has_audio": true, "width": 1280, "height": 720, "video_codec": "h264", "playback_asset_id": mediaID, "origin_offset_ms": 0, "limitations": []string{}, "execution": probeClaim["execution"]}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+probeClaim["job_id"].(string)+"/complete", probePayload, workerToken, probeClaim["lease_token"].(string), http.StatusOK)
	audioClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"audio_analysis"}}, workerToken, "", http.StatusOK)
	audioPayload := map[string]any{"schema_version": "1.1", "job_id": audioClaim["job_id"], "run_id": fullID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "audio_analysis", "segments": []map[string]any{{"segment_no": 0, "start_ms": 1000, "end_ms": 3000, "text_content": "固定转写", "speaker_label": "unknown"}}, "audio_asset_id": nil, "model": map[string]any{"name": "fixed-json-asr", "revision": "p0-v1"}, "coverage": []map[string]any{{"start_ms": 0, "end_ms": 60001}}, "limitations": []string{}, "execution": audioClaim["execution"]}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+audioClaim["job_id"].(string)+"/complete", audioPayload, workerToken, audioClaim["lease_token"].(string), http.StatusUnprocessableEntity)
	audioPayload["coverage"] = []map[string]any{{"start_ms": 0, "end_ms": 60000}}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+audioClaim["job_id"].(string)+"/complete", audioPayload, workerToken, audioClaim["lease_token"].(string), http.StatusOK)
	videoClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"video_analysis"}}, workerToken, "", http.StatusOK)
	frame := uploadWorkerArtifact(t, server.URL+"/internal/v1/jobs/"+videoClaim["job_id"].(string)+"/artifacts", workerToken, videoClaim["lease_token"].(string), []byte("jpeg"), videoClaim["lease_token"].(string)+"/frame-0", "keyframe", "1000", http.StatusCreated)
	videoPayload := map[string]any{"schema_version": "1.1", "job_id": videoClaim["job_id"], "run_id": fullID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "video_analysis", "frames": []map[string]any{{"asset_id": frame["asset_id"], "timestamp_ms": 1000}}, "events": []any{}, "model": nil, "sampling": map[string]any{"interval_ms": 30000, "max_frames": 240}, "coverage": []map[string]any{{"start_ms": 0, "end_ms": 60000}}, "limitations": []string{}, "execution": videoClaim["execution"]}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+videoClaim["job_id"].(string)+"/complete", videoPayload, workerToken, videoClaim["lease_token"].(string), http.StatusOK)
	for _, stage := range []string{"evidence", "report", "validate"} {
		if processed, err := analysisService.ProcessNextGoJob(ctx); err != nil || !processed {
			t.Fatalf("process %s: processed=%v err=%v", stage, processed, err)
		}
	}
	finished := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+fullID, nil, "", http.StatusOK)
	if finished["status"] != "succeeded" {
		t.Fatalf("full phase4 status=%v", finished["status"])
	}
	for _, raw := range finished["jobs"].([]any) {
		if status := raw.(map[string]any)["status"]; status == "queued" || status == "running" {
			t.Fatalf("terminal run retained active job: %#v", raw)
		}
	}
	results := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+fullID+"/results", nil, "", http.StatusOK)
	if len(results["segments"].([]any)) != 1 || len(results["frames"].([]any)) != 1 || len(results["evidence"].([]any)) != 2 || results["transcript_revision_id"] == nil {
		t.Fatalf("fixed results=%#v", results)
	}
	revision := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/transcript-revisions/"+results["transcript_revision_id"].(string), nil, "", http.StatusOK)
	if len(revision["segments"].([]any)) != 1 || revision["source_type"] != "asr" {
		t.Fatalf("transcript revision=%#v", revision)
	}
	reportID := finished["report_id"].(string)
	report := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/reports/"+reportID, nil, "", http.StatusOK)
	if len(report["dimensions"].([]any)) != 6 || len(report["observations"].([]any)) != 1 {
		t.Fatalf("validated report=%#v", report)
	}

	var overageCallID string
	var overageLock int
	if err = pool.QueryRow(ctx, `UPDATE teaching.model_calls SET reserved_cost=0.000161,actual_cost=0.000280,reconciled_by=NULL,reconciliation_reason=NULL
		WHERE run_id=$1 RETURNING id::text,lock_version`, fullID).Scan(&overageCallID, &overageLock); err != nil {
		t.Fatal(err)
	}
	budgetRunID := createReportReadyRun(t, pool, fullID, "budget-overage-blocked")
	if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
		t.Fatalf("skip budget-blocked report: processed=%v err=%v", processed, processErr)
	}
	budgetBlocked := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+budgetRunID, nil, "", http.StatusOK)
	if budgetBlocked["status"] != "partial" || budgetBlocked["error_code"] != "BUDGET_EXHAUSTED" {
		t.Fatalf("budget-blocked run=%#v", budgetBlocked)
	}
	requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/model-calls/"+overageCallID+"/reconcile", map[string]any{
		"lock_version": overageLock, "status": "succeeded", "actual_cost": "0.000280", "provider_request_id": "fixture-request", "reason": "确认超预留供应商账单",
	}, adminCSRF, http.StatusOK)

	afterReconcileID := createReportReadyRun(t, pool, fullID, "budget-after-reconcile")
	for _, stage := range []string{"report", "validate"} {
		if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
			t.Fatalf("process post-reconciliation %s: processed=%v err=%v", stage, processed, processErr)
		}
	}
	if got := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+afterReconcileID, nil, "", http.StatusOK); got["status"] != "succeeded" {
		t.Fatalf("post-reconciliation run=%#v", got)
	}

	orphanRunID := createReportReadyRun(t, pool, fullID, "orphaned-model-call")
	requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/analysis-runs/"+orphanRunID+"/cancel", map[string]any{}, csrf, http.StatusOK)
	var orphanCallID string
	if err = pool.QueryRow(ctx, `INSERT INTO teaching.model_calls(run_id,job_id,call_key,provider,model_name,prompt_version,status,currency,reserved_cost,billing_period,price_version,request_sha256,dispatch_started_at)
		SELECT r.id,j.id,'orphan:'||j.id::text,'fixture','synthetic-report','p0-v1','reserved','CNY',0.000161,date_trunc('month',now() AT TIME ZONE 'Asia/Shanghai')::date,'fixture-price',$2,now()-interval '2 minutes'
		FROM teaching.analysis_runs r JOIN teaching.analysis_jobs j ON j.run_id=r.id AND j.stage='report' WHERE r.id=$1 RETURNING id::text`, orphanRunID, strings.Repeat("f", 64)).Scan(&orphanCallID); err != nil {
		t.Fatal(err)
	}
	if count, reapErr := analysisService.ReapExpired(ctx); reapErr != nil || count != 1 {
		t.Fatalf("orphan model-call recovery count=%d err=%v", count, reapErr)
	}
	var orphanStatus string
	var orphanLock int
	if err = pool.QueryRow(ctx, `SELECT status,lock_version FROM teaching.model_calls WHERE id=$1`, orphanCallID).Scan(&orphanStatus, &orphanLock); err != nil || orphanStatus != "unknown" {
		t.Fatalf("orphan model call status=%s lock=%d err=%v", orphanStatus, orphanLock, err)
	}
	requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/model-calls/"+orphanCallID+"/reconcile", map[string]any{
		"lock_version": orphanLock, "status": "failed", "actual_cost": "0.000000", "provider_request_id": nil, "reason": "重启恢复后确认未收费",
	}, adminCSRF, http.StatusOK)

	cancelRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", create, csrf, map[string]string{"Idempotency-Key": "cancel-1"}, http.StatusAccepted)
	cancelClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
	requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/analysis-runs/"+cancelRun["id"].(string)+"/cancel", map[string]any{}, csrf, http.StatusOK)
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+cancelClaim["job_id"].(string)+"/heartbeat", map[string]any{"progress": 10}, workerToken, cancelClaim["lease_token"].(string), http.StatusConflict)

	failRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", create, csrf, map[string]string{"Idempotency-Key": "fail-1"}, http.StatusAccepted)
	for attempt := 1; attempt <= 3; attempt++ {
		claim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
		wantStatus := "queued"
		if attempt == 3 {
			wantStatus = "failed"
		}
		failure := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+claim["job_id"].(string)+"/fail", map[string]any{"error_code": "BROKEN_MEDIA", "message": "fixed failure", "retryable": true}, workerToken, claim["lease_token"].(string), http.StatusOK)
		if failure["status"] != wantStatus {
			t.Fatalf("attempt %d status=%v", attempt, failure["status"])
		}
		if attempt < 3 {
			if _, err = pool.Exec(ctx, `UPDATE teaching.analysis_jobs SET available_at=now() WHERE run_id=$1 AND stage='probe'`, failRun["id"]); err != nil {
				t.Fatal(err)
			}
		}
	}
	failed := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+failRun["id"].(string), nil, "", http.StatusOK)
	if failed["status"] != "failed" {
		t.Fatalf("failed run=%#v", failed)
	}

	recoveryRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", create, csrf, map[string]string{"Idempotency-Key": "recovery-1"}, http.StatusAccepted)
	recoveryClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
	if _, err = pool.Exec(ctx, `UPDATE teaching.analysis_jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, recoveryClaim["job_id"]); err != nil {
		t.Fatal(err)
	}
	if count, reapErr := analysisService.ReapExpired(ctx); reapErr != nil || count != 1 {
		t.Fatalf("reap count=%d err=%v", count, reapErr)
	}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+recoveryClaim["job_id"].(string)+"/heartbeat", map[string]any{"progress": 1}, workerToken, recoveryClaim["lease_token"].(string), http.StatusConflict)
	if _, err = pool.Exec(ctx, `UPDATE teaching.analysis_jobs SET available_at=now() WHERE id=$1`, recoveryClaim["job_id"]); err != nil {
		t.Fatal(err)
	}
	newClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
	if newClaim["lease_token"] == recoveryClaim["lease_token"] {
		t.Fatal("recovered job reused an old lease token")
	}
	requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/analysis-runs/"+recoveryRun["id"].(string)+"/cancel", map[string]any{}, csrf, http.StatusOK)

	raceRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", create, csrf, map[string]string{"Idempotency-Key": "race-1"}, http.StatusAccepted)
	raceClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
	raceProbe := map[string]any{"schema_version": "1.1", "job_id": raceClaim["job_id"], "run_id": raceRun["id"], "session_id": sessionID, "media_asset_id": mediaID, "stage": "probe", "duration_ms": 60000, "has_audio": true, "width": 1280, "height": 720, "video_codec": "h264", "playback_asset_id": mediaID, "origin_offset_ms": 0, "limitations": []string{}, "execution": raceClaim["execution"]}
	start := make(chan struct{})
	statuses := make(chan int, 2)
	go func() {
		<-start
		statuses <- rawStatus(teacher, http.MethodPost, server.URL+"/api/v1/analysis-runs/"+raceRun["id"].(string)+"/cancel", map[string]any{}, map[string]string{"Origin": "http://frontend.test", "X-CSRF-Token": csrf})
	}()
	go func() {
		<-start
		statuses <- rawStatus(http.DefaultClient, http.MethodPost, server.URL+"/internal/v1/jobs/"+raceClaim["job_id"].(string)+"/complete", raceProbe, map[string]string{"Authorization": "Bearer " + workerToken, "X-Lease-Token": raceClaim["lease_token"].(string)})
	}()
	close(start)
	first, second := <-statuses, <-statuses
	if !((first == http.StatusOK && second == http.StatusConflict) || (first == http.StatusConflict && second == http.StatusOK)) {
		t.Fatalf("cancel/complete race statuses=%d,%d", first, second)
	}
	traced := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+raceRun["id"].(string), nil, "", http.StatusOK)
	if traced["status"] != "succeeded" && traced["status"] != "cancelled" {
		t.Fatalf("race run status=%v", traced["status"])
	}

	revokedRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", create, csrf, map[string]string{"Idempotency-Key": "revoked-1"}, http.StatusAccepted)
	revokedClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
	if _, err = pool.Exec(ctx, `UPDATE teaching.source_records SET rights_status='rejected',allowed_uses='[]',rights_version=rights_version+1 WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	if count, reapErr := analysisService.ReapExpired(ctx); reapErr != nil || count != 1 {
		t.Fatalf("revocation sweep count=%d err=%v", count, reapErr)
	}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+revokedClaim["job_id"].(string)+"/heartbeat", map[string]any{"progress": 1}, workerToken, revokedClaim["lease_token"].(string), http.StatusConflict)
	revoked := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+revokedRun["id"].(string), nil, "", http.StatusOK)
	if revoked["status"] != "failed" || revoked["error_code"] != "SOURCE_NOT_VERIFIED" {
		t.Fatalf("revoked run=%#v", revoked)
	}
}

func createReportReadyRun(t *testing.T, pool *pgxpool.Pool, sourceRunID, key string) string {
	t.Helper()
	ctx := context.Background()
	var runID, sessionID, mediaID, requesterID, revisionID string
	err := pool.QueryRow(ctx, `INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,pipeline_version,config_snapshot,mode,status,request_sha256)
		SELECT session_id,media_asset_id,requested_by,$2,input_sha256,config_hash,pipeline_version,config_snapshot,'full','running',$3
		FROM teaching.analysis_runs WHERE id=$1 RETURNING id::text,session_id::text,media_asset_id::text,requested_by::text`, sourceRunID, key, strings.Repeat("e", 64)).
		Scan(&runID, &sessionID, &mediaID, &requesterID)
	if err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT metadata->>'transcript_revision_id' FROM teaching.job_artifacts WHERE run_id=$1 AND artifact_key='__result/evidence'`, sourceRunID).Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	var segmentID string
	if err = pool.QueryRow(ctx, `INSERT INTO teaching.transcript_segments(run_id,session_id,segment_no,start_ms,end_ms,speaker_label,text_content)
		SELECT $2,session_id,segment_no,start_ms,end_ms,speaker_label,text_content FROM teaching.transcript_segments WHERE run_id=$1 ORDER BY segment_no LIMIT 1 RETURNING id::text`, sourceRunID, runID).Scan(&segmentID); err != nil {
		t.Fatal(err)
	}
	var evidenceJobID string
	if err = pool.QueryRow(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,attempts,max_attempts,progress) VALUES($1,'evidence','succeeded',1,3,100) RETURNING id::text`, runID).Scan(&evidenceJobID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO teaching.job_artifacts(job_id,run_id,session_id,artifact_key,metadata)
		VALUES($1,$2,$3,'__result/evidence',jsonb_build_object('transcript_revision_id',$4::text))`, evidenceJobID, runID, sessionID, revisionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO teaching.evidence_items(run_id,session_id,media_asset_id,transcript_segment_id,kind,start_ms,end_ms,description,provenance,created_by)
		SELECT $1,$2,$3,$4,'transcript',start_ms,end_ms,text_content,'{}'::jsonb,$5 FROM teaching.transcript_segments WHERE id=$4`, runID, sessionID, mediaID, segmentID, requesterID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,max_attempts) VALUES($1,'report',1)`, runID); err != nil {
		t.Fatal(err)
	}
	return runID
}

func requestWithHeaders(t *testing.T, c *http.Client, method, target string, body any, csrf string, headers map[string]string, want int) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(method, target, bytes.NewReader(mustJSON(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://frontend.test")
	req.Header.Set("X-CSRF-Token", csrf)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s status=%d want=%d body=%s", target, resp.StatusCode, want, raw)
	}
	var envelope map[string]any
	_ = json.Unmarshal(raw, &envelope)
	if data, ok := envelope["data"].(map[string]any); ok {
		return data
	}
	return envelope
}
func requestWorker(t *testing.T, method, target string, body any, bearer, lease string, want int) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(mustJSON(body))
	}
	req, _ := http.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if lease != "" {
		req.Header.Set("X-Lease-Token", lease)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("worker %s status=%d want=%d body=%s", target, resp.StatusCode, want, raw)
	}
	var envelope map[string]any
	_ = json.Unmarshal(raw, &envelope)
	if data, ok := envelope["data"].(map[string]any); ok {
		return data
	}
	return envelope
}
func requestWorkerContent(t *testing.T, target, bearer, lease, byteRange string, want int, expected []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-Lease-Token", lease)
	req.Header.Set("Range", byteRange)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want || !bytes.Equal(raw, expected) {
		t.Fatalf("worker content status=%d body=%q", resp.StatusCode, raw)
	}
}
func uploadWorkerArtifact(t *testing.T, target, bearer, lease string, content []byte, key, kind, timestamp string, want int) map[string]any {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("artifact_key", key)
	_ = w.WriteField("kind", kind)
	_ = w.WriteField("timestamp_ms", timestamp)
	_ = w.WriteField("origin_offset_ms", "0")
	part, _ := w.CreateFormFile("file", "artifact.bin")
	_, _ = part.Write(content)
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, target, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-Lease-Token", lease)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("artifact status=%d want=%d body=%s", resp.StatusCode, want, raw)
	}
	var envelope map[string]any
	_ = json.Unmarshal(raw, &envelope)
	return envelope["data"].(map[string]any)
}

func rawStatus(client *http.Client, method, target string, body any, headers map[string]string) int {
	req, _ := http.NewRequest(method, target, bytes.NewReader(mustJSON(body)))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}
