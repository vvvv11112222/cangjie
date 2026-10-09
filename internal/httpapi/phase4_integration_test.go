package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

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

type reportProviderGate struct {
	started chan struct{}
	release chan struct{}
}

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
	var requestHashes sync.Map
	var providerGates sync.Map
	var omitUsageRuns sync.Map
	var reportCalls atomic.Int64
	reportProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		rawRequest, readErr := io.ReadAll(r.Body)
		if err := json.Unmarshal(rawRequest, &request); readErr != nil || err != nil || len(request.Messages) != 2 {
			t.Errorf("invalid report request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var input struct {
			RunID    string `json:"run_id"`
			Coverage []struct {
				StartMS int64 `json:"start_ms"`
				EndMS   int64 `json:"end_ms"`
			} `json:"coverage"`
			Evidence []struct {
				ID string `json:"id"`
			} `json:"evidence"`
		}
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil || len(input.Evidence) == 0 || len(input.Coverage) == 0 {
			t.Errorf("invalid model input: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reportCalls.Add(1)
		if rawGate, ok := providerGates.Load(input.RunID); ok {
			gate := rawGate.(*reportProviderGate)
			close(gate.started)
			<-gate.release
		}
		sum := sha256.Sum256(rawRequest)
		requestHashes.Store(input.RunID, hex.EncodeToString(sum[:]))
		evidenceID := input.Evidence[0].ID
		dimensions := []map[string]any{{"dimension_code": "content", "coverage_status": "observed", "summary": "课堂介绍了固定主题", "limitation": "", "coverage": []map[string]any{{"start_ms": input.Coverage[0].StartMS, "end_ms": input.Coverage[0].EndMS}}, "summary_evidence_ids": []string{evidenceID}}}
		for _, code := range []string{"pace", "thinking", "expression", "management", "technology"} {
			dimensions = append(dimensions, map[string]any{"dimension_code": code, "coverage_status": "insufficient", "summary": "", "limitation": "当前仅有文本证据", "coverage": []any{}, "summary_evidence_ids": []any{}})
		}
		candidate := map[string]any{"summary": "固定转写课堂摘要", "summary_evidence_ids": []string{evidenceID}, "dimensions": dimensions, "observations": []map[string]any{{"dimension_code": "content", "observation_type": "observation", "observation_text": "教师讲解了固定转写主题。", "suggestion": "", "evidence_ids": []string{evidenceID}}}}
		content, _ := json.Marshal(candidate)
		response := map[string]any{"id": "fixture-request", "choices": []map[string]any{{"message": map[string]any{"content": string(content)}}}}
		if _, omit := omitUsageRuns.Load(input.RunID); !omit {
			response["usage"] = map[string]any{"prompt_tokens": 120, "completion_tokens": 80}
		}
		writeJSON(w, http.StatusOK, response)
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

	full := map[string]any{"media_asset_id": mediaID, "mode": "full", "input_transcript_revision_id": nil, "config_profile": "p0-v1"}
	fullRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", full, csrf, map[string]string{"Idempotency-Key": "full-1"}, http.StatusAccepted)
	fullID := fullRun["id"].(string)
	probeClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
	probePayload := map[string]any{"schema_version": "1.1", "job_id": probeClaim["job_id"], "run_id": fullID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "probe", "duration_ms": 60000, "has_audio": true, "width": 1280, "height": 720, "video_codec": "h264", "playback_asset_id": mediaID, "origin_offset_ms": 0, "limitations": []string{}, "execution": probeClaim["execution"]}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+probeClaim["job_id"].(string)+"/complete", probePayload, workerToken, probeClaim["lease_token"].(string), http.StatusOK)
	duplicateStatuses := make(chan int, 2)
	for range 2 {
		go func() {
			duplicateStatuses <- rawStatus(http.DefaultClient, http.MethodPost, server.URL+"/internal/v1/jobs/"+probeClaim["job_id"].(string)+"/complete", probePayload, map[string]string{"Authorization": "Bearer " + workerToken, "X-Lease-Token": probeClaim["lease_token"].(string)})
		}()
	}
	if firstDuplicate, secondDuplicate := <-duplicateStatuses, <-duplicateStatuses; firstDuplicate != http.StatusOK || secondDuplicate != http.StatusOK {
		t.Fatalf("identical complete replays=%d,%d", firstDuplicate, secondDuplicate)
	}
	audioClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"audio_analysis"}}, workerToken, "", http.StatusOK)
	audioPayload := map[string]any{"schema_version": "1.1", "job_id": audioClaim["job_id"], "run_id": fullID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "audio_analysis", "segments": []map[string]any{{"segment_no": 0, "start_ms": 1000, "end_ms": 3000, "text_content": "固定转写", "speaker_label": "unknown"}}, "audio_asset_id": nil, "model": map[string]any{"name": "fixed-json-asr", "revision": "p0-v1"}, "coverage": []map[string]any{{"start_ms": 0, "end_ms": 60001}}, "limitations": []string{}, "execution": audioClaim["execution"]}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+audioClaim["job_id"].(string)+"/complete", audioPayload, workerToken, audioClaim["lease_token"].(string), http.StatusUnprocessableEntity)
	audioPayload["coverage"] = []map[string]any{{"start_ms": 0, "end_ms": 60000}}
	audioPayload["segments"] = []map[string]any{{"segment_no": 0, "start_ms": 2000, "end_ms": 3000, "text_content": "后段", "speaker_label": "unknown"}, {"segment_no": 1, "start_ms": 1000, "end_ms": 1500, "text_content": "倒序", "speaker_label": "unknown"}}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+audioClaim["job_id"].(string)+"/complete", audioPayload, workerToken, audioClaim["lease_token"].(string), http.StatusUnprocessableEntity)
	audioPayload["segments"] = []map[string]any{{"segment_no": 0, "start_ms": 1000, "end_ms": 3000, "text_content": "固定转写", "speaker_label": "unknown"}}
	restartedCfg := cfg
	restartedCfg.ASRModelName = "changed-asr"
	restartedCfg.ASRModelRevision = "changed-revision"
	restartedCfg.KeyframeIntervalMS = 1000
	restartedCfg.MaxKeyframes = 1
	restartedCfg.MaxMediaDurationMS = 1
	restartedCfg.MaxVideoHeight = 1
	restartedCfg.ReportModel = "changed-report-model"
	restartedCfg.ReportModelRevision = "changed-report-revision"
	restartedCfg.ReportPromptVersion = "changed-prompt"
	restartedCfg.ReportPromptSHA256 = strings.Repeat("e", 64)
	restartedCfg.ReportSelectionVersion = "changed-selection"
	restartedCfg.ReportMaxInputTokens = 1
	restartedService := analysis.NewService(pool, store, restartedCfg)
	if completedAfterRestart, completeErr := restartedService.Complete(ctx, audioClaim["job_id"].(string), audioClaim["lease_token"].(string), mustJSON(audioPayload)); completeErr != nil || completedAfterRestart.Duplicate {
		t.Fatalf("pinned audio result after configuration change=%#v err=%v", completedAfterRestart, completeErr)
	}
	videoClaim := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"video_analysis"}}, workerToken, "", http.StatusOK)
	frame := uploadWorkerArtifact(t, server.URL+"/internal/v1/jobs/"+videoClaim["job_id"].(string)+"/artifacts", workerToken, videoClaim["lease_token"].(string), []byte("jpeg"), videoClaim["lease_token"].(string)+"/frame-0", "keyframe", "1000", http.StatusCreated)
	unusedFrame := uploadWorkerArtifact(t, server.URL+"/internal/v1/jobs/"+videoClaim["job_id"].(string)+"/artifacts", workerToken, videoClaim["lease_token"].(string), []byte("unused-jpeg"), videoClaim["lease_token"].(string)+"/frame-unused", "keyframe", "2000", http.StatusCreated)
	videoPayload := map[string]any{"schema_version": "1.1", "job_id": videoClaim["job_id"], "run_id": fullID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "video_analysis", "frames": []map[string]any{{"asset_id": frame["asset_id"], "timestamp_ms": 1000}}, "events": []any{}, "model": nil, "sampling": map[string]any{"interval_ms": 30000, "max_frames": 240}, "coverage": []map[string]any{{"start_ms": 0, "end_ms": 60000}}, "limitations": []string{}, "execution": videoClaim["execution"]}
	if completedAfterRestart, completeErr := restartedService.Complete(ctx, videoClaim["job_id"].(string), videoClaim["lease_token"].(string), mustJSON(videoPayload)); completeErr != nil || completedAfterRestart.Duplicate {
		t.Fatalf("pinned video result after configuration change=%#v err=%v", completedAfterRestart, completeErr)
	}
	var unusedCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM teaching.media_assets WHERE id=$1`, unusedFrame["asset_id"]).Scan(&unusedCount); err != nil || unusedCount != 0 {
		t.Fatalf("unreferenced artifact remained visible: count=%d err=%v", unusedCount, err)
	}
	for _, stage := range []string{"evidence", "report", "validate"} {
		if processed, err := restartedService.ProcessNextGoJob(ctx); err != nil || !processed {
			t.Fatalf("process %s: processed=%v err=%v", stage, processed, err)
		}
	}
	finished := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+fullID, nil, "", http.StatusOK)
	if finished["status"] != "succeeded" {
		t.Fatalf("full phase4 status=%v", finished["status"])
	}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+probeClaim["job_id"].(string)+"/complete", probePayload, workerToken, probeClaim["lease_token"].(string), http.StatusOK)
	var requestSHA, reservedCost, actualCost string
	var inputTokens, outputTokens int64
	if err = pool.QueryRow(ctx, `SELECT request_sha256,to_char(reserved_cost,'FM999999999990.000000'),to_char(actual_cost,'FM999999999990.000000'),input_tokens,output_tokens FROM teaching.model_calls WHERE run_id=$1`, fullID).
		Scan(&requestSHA, &reservedCost, &actualCost, &inputTokens, &outputTokens); err != nil {
		t.Fatal(err)
	}
	wantRequestSHA, ok := requestHashes.Load(fullID)
	if !ok || requestSHA != wantRequestSHA.(string) || reservedCost != "0.014000" || actualCost != "0.000280" || inputTokens != 120 || outputTokens != 80 {
		t.Fatalf("model accounting request_sha=%s want=%v reserved=%s actual=%s tokens=%d/%d", requestSHA, wantRequestSHA, reservedCost, actualCost, inputTokens, outputTokens)
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
	provenance := report["provenance"].(map[string]any)
	if provenance["prompt_version"] != "p0-v1" || provenance["selection_version"] != "time-window-v1" || provenance["model"].(map[string]any)["name"] != "synthetic-report" {
		t.Fatalf("report did not retain pinned configuration: %#v", provenance)
	}
	var legacyReportID string
	if err = pool.QueryRow(ctx, `INSERT INTO teaching.reports(run_id,session_id,revision,status,summary,created_by,expires_at,summary_evidence_ids,content_sha256,provenance)
		SELECT r.run_id,r.session_id,(SELECT max(revision)+1 FROM teaching.reports WHERE run_id=r.run_id),'draft','legacy unverified report',r.created_by,r.expires_at,'{}',NULL,NULL
		FROM teaching.reports r WHERE r.id=$1 RETURNING id::text`, reportID).Scan(&legacyReportID); err != nil {
		t.Fatal(err)
	}
	legacyReport := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/reports/"+legacyReportID, nil, "", http.StatusOK)
	if actions := legacyReport["allowed_actions"].([]any); len(actions) != 0 {
		t.Fatalf("legacy report exposed write actions: %#v", actions)
	}
	legacyPatchObservations := make([]map[string]any, 0, len(report["observations"].([]any)))
	for _, raw := range report["observations"].([]any) {
		o := raw.(map[string]any)
		legacyPatchObservations = append(legacyPatchObservations, map[string]any{"dimension_code": o["dimension_code"], "observation_type": o["observation_type"], "observation_text": o["observation_text"], "suggestion": o["suggestion"], "evidence_ids": o["evidence_ids"]})
	}
	requestJSON(t, teacher, http.MethodPatch, server.URL+"/api/v1/reports/"+legacyReportID, map[string]any{
		"lock_version": legacyReport["lock_version"], "summary": report["summary"], "summary_evidence_ids": report["summary_evidence_ids"],
		"dimensions": report["dimensions"], "observations": legacyPatchObservations, "reason": "不可补写历史来源",
	}, csrf, http.StatusConflict)
	requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/reports/"+legacyReportID+"/review", map[string]any{"lock_version": legacyReport["lock_version"], "action": "submit", "observation_id": nil, "content_sha256": nil, "reason": "不可复核历史来源"}, csrf, http.StatusConflict)
	requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+legacyReportID+"/publish", map[string]any{"lock_version": legacyReport["lock_version"], "expected_current_report_id": nil}, supervisorCSRF, http.StatusConflict)
	manualRevision := requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/transcript-revisions", map[string]any{
		"media_asset_id": mediaID, "base_revision_id": revision["id"], "transcript_lock_version": revision["transcript_lock_version"],
		"reason": "纠正固定转写", "segments": []map[string]any{{"segment_no": 0, "start_ms": 1000, "end_ms": 3000, "text_content": "修订后的课堂文本", "speaker_label": "teacher"}},
	}, csrf, http.StatusCreated)
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/sessions/"+sessionID+"/transcript-revisions?media_asset_id="+mediaID, nil, "", http.StatusOK)
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/sessions/"+sessionID+"/transcript-revisions", nil, "", http.StatusBadRequest)
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/sessions/"+sessionID+"/transcript-revisions?media_asset_id=not-a-uuid", nil, "", http.StatusBadRequest)
	requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/transcript-revisions", map[string]any{
		"media_asset_id": mediaID, "base_revision_id": revision["id"], "transcript_lock_version": revision["transcript_lock_version"],
		"reason": "陈旧写入", "segments": []map[string]any{{"segment_no": 0, "start_ms": 1000, "end_ms": 3000, "text_content": "不应保存", "speaker_label": "teacher"}},
	}, csrf, http.StatusConflict)
	reportOnly := map[string]any{"media_asset_id": mediaID, "mode": "report_only", "input_transcript_revision_id": manualRevision["id"], "config_profile": "p0-v1"}
	reportOnlyRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", reportOnly, csrf, map[string]string{"Idempotency-Key": "report-only-1"}, http.StatusAccepted)
	for _, stage := range []string{"evidence", "report", "validate"} {
		if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
			t.Fatalf("process report_only %s: processed=%v err=%v", stage, processed, processErr)
		}
	}
	revisedResults := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+reportOnlyRun["id"].(string)+"/results", nil, "", http.StatusOK)
	if revisedResults["transcript_revision_id"] != manualRevision["id"] || revisedResults["segments"].([]any)[0].(map[string]any)["text_content"] != "修订后的课堂文本" {
		t.Fatalf("report_only did not use fixed revision: %#v", revisedResults)
	}
	oldReport := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/reports/"+reportID, nil, "", http.StatusOK)
	if oldReport["summary"] != report["summary"] || oldReport["lock_version"] != report["lock_version"] {
		t.Fatalf("old report changed after transcript revision: %#v", oldReport)
	}
	revisedRun := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+reportOnlyRun["id"].(string), nil, "", http.StatusOK)
	revisedReportID := revisedRun["report_id"].(string)
	revisedReport := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/reports/"+revisedReportID, nil, "", http.StatusOK)
	revisedReport = requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/reports/"+revisedReportID+"/review", map[string]any{
		"lock_version": revisedReport["lock_version"], "action": "submit", "observation_id": nil, "content_sha256": nil, "reason": "提交复核",
	}, csrf, http.StatusOK)
	requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+revisedReportID+"/publish", map[string]any{
		"lock_version": revisedReport["lock_version"], "expected_current_report_id": nil,
	}, supervisorCSRF, http.StatusUnprocessableEntity)
	observationID := revisedReport["observations"].([]any)[0].(map[string]any)["id"]
	revisedReport = requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+revisedReportID+"/review", map[string]any{
		"lock_version": revisedReport["lock_version"], "action": "accept", "observation_id": observationID, "content_sha256": nil, "reason": "证据匹配",
	}, supervisorCSRF, http.StatusOK)
	revisedReport = requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+revisedReportID+"/review", map[string]any{
		"lock_version": revisedReport["lock_version"], "action": "confirm_report", "observation_id": nil, "content_sha256": revisedReport["content_sha256"], "reason": "整篇确认",
	}, supervisorCSRF, http.StatusOK)
	requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/reports/"+revisedReportID+"/publish", map[string]any{
		"lock_version": revisedReport["lock_version"], "expected_current_report_id": nil,
	}, csrf, http.StatusForbidden)
	published := requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+revisedReportID+"/publish", map[string]any{
		"lock_version": revisedReport["lock_version"], "expected_current_report_id": nil,
	}, supervisorCSRF, http.StatusOK)
	if published["status"] != "published" {
		t.Fatalf("published report=%#v", published)
	}
	draftA := requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/reports/"+revisedReportID+"/revisions", map[string]any{"lock_version": published["lock_version"], "reason": "候选 A"}, csrf, http.StatusCreated)
	draftB := requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/reports/"+revisedReportID+"/revisions", map[string]any{"lock_version": published["lock_version"], "reason": "候选 B"}, csrf, http.StatusCreated)
	patchObservations := make([]map[string]any, 0, len(draftA["observations"].([]any)))
	for _, raw := range draftA["observations"].([]any) {
		o := raw.(map[string]any)
		patchObservations = append(patchObservations, map[string]any{"id": o["id"], "dimension_code": o["dimension_code"], "observation_type": o["observation_type"], "observation_text": o["observation_text"], "suggestion": o["suggestion"], "evidence_ids": o["evidence_ids"]})
	}
	patchObservations = append(patchObservations, map[string]any{"dimension_code": "content", "observation_type": "highlight", "observation_text": "第二条可独立复核的观察。", "suggestion": "", "evidence_ids": patchObservations[0]["evidence_ids"]})
	var invalidDimensions []map[string]any
	if err = json.Unmarshal(mustJSON(draftA["dimensions"]), &invalidDimensions); err != nil {
		t.Fatal(err)
	}
	for _, dimension := range invalidDimensions {
		if dimension["coverage_status"] != "observed" {
			dimension["summary"] = "不足维度不应允许写入的事实摘要"
			dimension["summary_evidence_ids"] = draftA["summary_evidence_ids"]
			break
		}
	}
	requestJSON(t, teacher, http.MethodPatch, server.URL+"/api/v1/reports/"+draftA["id"].(string), map[string]any{
		"lock_version": draftA["lock_version"], "summary": draftA["summary"], "summary_evidence_ids": draftA["summary_evidence_ids"],
		"dimensions": invalidDimensions, "observations": patchObservations, "reason": "尝试写入不足维度事实",
	}, csrf, http.StatusBadRequest)
	draftA = requestJSON(t, teacher, http.MethodPatch, server.URL+"/api/v1/reports/"+draftA["id"].(string), map[string]any{
		"lock_version": draftA["lock_version"], "summary": "人工编辑后的摘要", "summary_evidence_ids": draftA["summary_evidence_ids"],
		"dimensions": draftA["dimensions"], "observations": patchObservations, "reason": "人工修正文案",
	}, csrf, http.StatusOK)
	prepareForPublish := func(value map[string]any, client *http.Client, token string) map[string]any {
		value = requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/reports/"+value["id"].(string)+"/review", map[string]any{"lock_version": value["lock_version"], "action": "submit", "observation_id": nil, "content_sha256": nil, "reason": "提交"}, csrf, http.StatusOK)
		observationIDs := make([]string, 0, len(value["observations"].([]any)))
		for _, raw := range value["observations"].([]any) {
			observationIDs = append(observationIDs, raw.(map[string]any)["id"].(string))
		}
		for _, obsID := range observationIDs {
			value = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/reports/"+value["id"].(string)+"/review", map[string]any{"lock_version": value["lock_version"], "action": "accept", "observation_id": obsID, "content_sha256": nil, "reason": "确认"}, token, http.StatusOK)
		}
		return requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/reports/"+value["id"].(string)+"/review", map[string]any{"lock_version": value["lock_version"], "action": "confirm_report", "observation_id": nil, "content_sha256": value["content_sha256"], "reason": "整篇确认"}, token, http.StatusOK)
	}
	draftA = prepareForPublish(draftA, supervisor, supervisorCSRF)
	draftB = prepareForPublish(draftB, supervisor, supervisorCSRF)
	confirmedObservations := make([]map[string]any, 0, len(draftA["observations"].([]any)))
	for _, raw := range draftA["observations"].([]any) {
		o := raw.(map[string]any)
		confirmedObservations = append(confirmedObservations, map[string]any{"id": o["id"], "dimension_code": o["dimension_code"], "observation_type": o["observation_type"], "observation_text": o["observation_text"], "suggestion": o["suggestion"], "evidence_ids": o["evidence_ids"]})
	}
	draftA = requestJSON(t, teacher, http.MethodPatch, server.URL+"/api/v1/reports/"+draftA["id"].(string), map[string]any{
		"lock_version": draftA["lock_version"], "summary": draftA["summary"].(string) + "（复核后修订）", "summary_evidence_ids": draftA["summary_evidence_ids"],
		"dimensions": draftA["dimensions"], "observations": confirmedObservations, "reason": "确认后修改正文",
	}, csrf, http.StatusOK)
	if draftA["status"] != "draft" || draftA["reviewed_content_sha256"] != nil {
		t.Fatalf("editing confirmed content did not clear attestation: %#v", draftA)
	}
	finalObservationIDs := []string{draftA["observations"].([]any)[0].(map[string]any)["id"].(string), draftA["observations"].([]any)[1].(map[string]any)["id"].(string)}
	draftA = requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/reports/"+draftA["id"].(string)+"/review", map[string]any{"lock_version": draftA["lock_version"], "action": "submit", "observation_id": nil, "content_sha256": nil, "reason": "提交最终复核"}, csrf, http.StatusOK)
	draftA = requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+draftA["id"].(string)+"/review", map[string]any{"lock_version": draftA["lock_version"], "action": "reject", "observation_id": finalObservationIDs[0], "content_sha256": nil, "reason": "排除不纳入正式报告的观察"}, supervisorCSRF, http.StatusOK)
	draftA = requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+draftA["id"].(string)+"/review", map[string]any{"lock_version": draftA["lock_version"], "action": "accept", "observation_id": finalObservationIDs[1], "content_sha256": nil, "reason": "保留有效观察"}, supervisorCSRF, http.StatusOK)
	draftA = requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+draftA["id"].(string)+"/review", map[string]any{"lock_version": draftA["lock_version"], "action": "confirm_report", "observation_id": nil, "content_sha256": draftA["content_sha256"], "reason": "整篇确认"}, supervisorCSRF, http.StatusOK)
	publishedA := requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+draftA["id"].(string)+"/publish", map[string]any{"lock_version": draftA["lock_version"], "expected_current_report_id": strings.ToUpper(revisedReportID)}, supervisorCSRF, http.StatusOK)
	if observations := publishedA["observations"].([]any); len(observations) != 1 || observations[0].(map[string]any)["id"] != finalObservationIDs[1] {
		t.Fatalf("published report leaked rejected observation: %#v", observations)
	}
	requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+draftB["id"].(string)+"/publish", map[string]any{"lock_version": draftB["lock_version"], "expected_current_report_id": revisedReportID}, supervisorCSRF, http.StatusConflict)
	var expiredEvidenceID string
	if err = pool.QueryRow(ctx, `INSERT INTO teaching.evidence_items(run_id,session_id,media_asset_id,kind,start_ms,end_ms,description,availability)
		SELECT r.run_id,r.session_id,ar.media_asset_id,'manual',3000,3001,'expired synthetic evidence','expired'
		FROM teaching.reports r JOIN teaching.analysis_runs ar ON ar.id=r.run_id WHERE r.id=$1 RETURNING id::text`, draftB["id"]).Scan(&expiredEvidenceID); err != nil {
		t.Fatal(err)
	}
	draftBObservationID := draftB["observations"].([]any)[0].(map[string]any)["id"].(string)
	if _, err = pool.Exec(ctx, `INSERT INTO teaching.observation_evidence(observation_id,report_id,evidence_id,run_id,session_id)
		SELECT ro.id,ro.report_id,$2,ro.run_id,ro.session_id FROM teaching.report_observations ro WHERE ro.id=$1`, draftBObservationID, expiredEvidenceID); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+draftB["id"].(string)+"/publish", map[string]any{"lock_version": draftB["lock_version"], "expected_current_report_id": publishedA["id"]}, supervisorCSRF, http.StatusUnprocessableEntity)
	var currentReportID string
	if err = pool.QueryRow(ctx, `SELECT id::text FROM teaching.reports WHERE session_id=$1 AND status='published'`, sessionID).Scan(&currentReportID); err != nil || currentReportID != publishedA["id"] {
		t.Fatalf("failed publication changed current pointer: got=%s err=%v", currentReportID, err)
	}
	withdrawn := requestJSON(t, supervisor, http.MethodPost, server.URL+"/api/v1/reports/"+publishedA["id"].(string)+"/withdraw", map[string]any{"lock_version": publishedA["lock_version"], "reason": "撤回竞争发布结果"}, supervisorCSRF, http.StatusOK)
	if withdrawn["status"] != "withdrawn" {
		t.Fatalf("withdrawn report=%#v", withdrawn)
	}

	partialRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", full, csrf, map[string]string{"Idempotency-Key": "partial-video-1"}, http.StatusAccepted)
	partialID := partialRun["id"].(string)
	partialProbe := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
	partialProbeResult := map[string]any{"schema_version": "1.1", "job_id": partialProbe["job_id"], "run_id": partialID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "probe", "duration_ms": 60000, "has_audio": true, "width": 1280, "height": 720, "video_codec": "h264", "playback_asset_id": mediaID, "origin_offset_ms": 0, "limitations": []string{}, "execution": partialProbe["execution"]}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+partialProbe["job_id"].(string)+"/complete", partialProbeResult, workerToken, partialProbe["lease_token"].(string), http.StatusOK)
	partialAudio := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"audio_analysis"}}, workerToken, "", http.StatusOK)
	partialAudioResult := map[string]any{"schema_version": "1.1", "job_id": partialAudio["job_id"], "run_id": partialID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "audio_analysis", "segments": []map[string]any{{"segment_no": 0, "start_ms": 0, "end_ms": 1000, "text_content": "开头片段", "speaker_label": "unknown"}, {"segment_no": 1, "start_ms": 59000, "end_ms": 60000, "text_content": "结尾片段", "speaker_label": "unknown"}}, "audio_asset_id": nil, "model": map[string]any{"name": "fixed-json-asr", "revision": "p0-v1"}, "coverage": []map[string]any{{"start_ms": 0, "end_ms": 1000}, {"start_ms": 59000, "end_ms": 60000}}, "limitations": []string{"ASR_PARTIAL"}, "execution": partialAudio["execution"]}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+partialAudio["job_id"].(string)+"/complete", partialAudioResult, workerToken, partialAudio["lease_token"].(string), http.StatusOK)
	partialVideo := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"video_analysis"}}, workerToken, "", http.StatusOK)
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+partialVideo["job_id"].(string)+"/fail", map[string]any{"error_code": "VIDEO_BROKEN", "message": "synthetic video failure", "retryable": false}, workerToken, partialVideo["lease_token"].(string), http.StatusOK)
	for _, stage := range []string{"evidence", "report", "validate"} {
		if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
			t.Fatalf("process partial %s: processed=%v err=%v", stage, processed, processErr)
		}
	}
	partialFinished := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+partialID, nil, "", http.StatusOK)
	if partialFinished["status"] != "partial" || partialFinished["error_code"] != "VIDEO_BROKEN" {
		t.Fatalf("required-stage aggregation=%#v", partialFinished)
	}
	var modelInputRaw []byte
	if err = pool.QueryRow(ctx, `SELECT ja.metadata->'model_input' FROM teaching.job_artifacts ja JOIN teaching.analysis_jobs j ON j.id=ja.job_id WHERE j.run_id=$1 AND ja.artifact_key='__model_input'`, partialID).Scan(&modelInputRaw); err != nil {
		t.Fatal(err)
	}
	var capturedInput struct {
		Coverage    []map[string]any `json:"coverage"`
		Limitations []string         `json:"limitations"`
	}
	if err = json.Unmarshal(modelInputRaw, &capturedInput); err != nil || len(capturedInput.Coverage) != 2 || !slices.Contains(capturedInput.Limitations, "audio_analysis:ASR_PARTIAL") {
		t.Fatalf("model input coverage/limitations=%s err=%v", modelInputRaw, err)
	}

	noSpeechRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", full, csrf, map[string]string{"Idempotency-Key": "no-speech-frame-1"}, http.StatusAccepted)
	noSpeechID := noSpeechRun["id"].(string)
	noSpeechProbe := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"probe"}}, workerToken, "", http.StatusOK)
	noSpeechProbeResult := map[string]any{"schema_version": "1.1", "job_id": noSpeechProbe["job_id"], "run_id": noSpeechID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "probe", "duration_ms": 60000, "has_audio": true, "width": 1280, "height": 720, "video_codec": "h264", "playback_asset_id": mediaID, "origin_offset_ms": 0, "limitations": []string{}, "execution": noSpeechProbe["execution"]}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+noSpeechProbe["job_id"].(string)+"/complete", noSpeechProbeResult, workerToken, noSpeechProbe["lease_token"].(string), http.StatusOK)
	noSpeechAudio := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"audio_analysis"}}, workerToken, "", http.StatusOK)
	noSpeechAudioResult := map[string]any{"schema_version": "1.1", "job_id": noSpeechAudio["job_id"], "run_id": noSpeechID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "audio_analysis", "segments": []any{}, "audio_asset_id": nil, "model": map[string]any{"name": "fixed-json-asr", "revision": "p0-v1"}, "coverage": []map[string]any{{"start_ms": 0, "end_ms": 60000}}, "limitations": []string{"NO_SPEECH"}, "execution": noSpeechAudio["execution"]}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+noSpeechAudio["job_id"].(string)+"/complete", noSpeechAudioResult, workerToken, noSpeechAudio["lease_token"].(string), http.StatusOK)
	noSpeechVideo := requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/claim", map[string]any{"worker_id": "worker-01", "capabilities": []string{"video_analysis"}}, workerToken, "", http.StatusOK)
	noSpeechFrame := uploadWorkerArtifact(t, server.URL+"/internal/v1/jobs/"+noSpeechVideo["job_id"].(string)+"/artifacts", workerToken, noSpeechVideo["lease_token"].(string), []byte("no-speech-jpeg"), noSpeechVideo["lease_token"].(string)+"/frame-0", "keyframe", "1000", http.StatusCreated)
	noSpeechVideoResult := map[string]any{"schema_version": "1.1", "job_id": noSpeechVideo["job_id"], "run_id": noSpeechID, "session_id": sessionID, "media_asset_id": mediaID, "stage": "video_analysis", "frames": []map[string]any{{"asset_id": noSpeechFrame["asset_id"], "timestamp_ms": 1000}}, "events": []any{}, "model": nil, "sampling": map[string]any{"interval_ms": 30000, "max_frames": 240}, "coverage": []map[string]any{{"start_ms": 0, "end_ms": 60000}}, "limitations": []string{}, "execution": noSpeechVideo["execution"]}
	requestWorker(t, http.MethodPost, server.URL+"/internal/v1/jobs/"+noSpeechVideo["job_id"].(string)+"/complete", noSpeechVideoResult, workerToken, noSpeechVideo["lease_token"].(string), http.StatusOK)
	if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
		t.Fatalf("process no-speech evidence: processed=%v err=%v", processed, processErr)
	}
	noSpeechFinished := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+noSpeechID, nil, "", http.StatusOK)
	noSpeechResults := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+noSpeechID+"/results", nil, "", http.StatusOK)
	if noSpeechFinished["status"] != "partial" || noSpeechFinished["error_code"] != "NO_SPEECH" || len(noSpeechResults["evidence"].([]any)) != 1 || noSpeechResults["evidence"].([]any)[0].(map[string]any)["kind"] != "frame" {
		t.Fatalf("no-speech frame evidence run=%#v results=%#v", noSpeechFinished, noSpeechResults)
	}

	var currentTranscriptLock int
	if err = pool.QueryRow(ctx, `SELECT transcript_lock_version FROM teaching.lesson_sessions WHERE id=$1`, sessionID).Scan(&currentTranscriptLock); err != nil {
		t.Fatal(err)
	}
	longRevision := requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/transcript-revisions", map[string]any{
		"media_asset_id": mediaID, "base_revision_id": manualRevision["id"], "transcript_lock_version": currentTranscriptLock,
		"reason": "验证首条超长证据上限", "segments": []map[string]any{{"segment_no": 0, "start_ms": 1000, "end_ms": 3000, "text_content": strings.Repeat("超", 17000), "speaker_label": "teacher"}},
	}, csrf, http.StatusCreated)
	longRequest := map[string]any{"media_asset_id": mediaID, "mode": "report_only", "input_transcript_revision_id": longRevision["id"], "config_profile": "p0-v1"}
	longRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", longRequest, csrf, map[string]string{"Idempotency-Key": "long-first-evidence-1"}, http.StatusAccepted)
	for _, stage := range []string{"evidence", "report", "validate"} {
		if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
			t.Fatalf("process long evidence %s: processed=%v err=%v", stage, processed, processErr)
		}
	}
	var longInputRaw []byte
	if err = pool.QueryRow(ctx, `SELECT ja.metadata->'model_input' FROM teaching.job_artifacts ja JOIN teaching.analysis_jobs j ON j.id=ja.job_id WHERE j.run_id=$1 AND ja.artifact_key='__model_input'`, longRun["id"]).Scan(&longInputRaw); err != nil {
		t.Fatal(err)
	}
	var longInput struct {
		Limitations []string `json:"limitations"`
		Evidence    []struct {
			ID   string `json:"id"`
			Text string `json:"text_content"`
		} `json:"evidence"`
	}
	if err = json.Unmarshal(longInputRaw, &longInput); err != nil || len(longInput.Evidence) != 1 {
		t.Fatalf("first evidence model input=%s err=%v", longInputRaw, err)
	}
	longChars := utf8.RuneCountInString(longInput.Evidence[0].Text)
	if longChars != 16000 || !slices.Contains(longInput.Limitations, "MODEL_INPUT_TRUNCATED:"+longInput.Evidence[0].ID) {
		t.Fatalf("first evidence limit was not enforced: chars=%d limitations=%#v", longChars, longInput.Limitations)
	}

	if _, err = pool.Exec(ctx, `UPDATE teaching.model_calls SET actual_cost=99.98 WHERE id=(SELECT id FROM teaching.model_calls WHERE status='succeeded' ORDER BY created_at LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT transcript_lock_version FROM teaching.lesson_sessions WHERE id=$1`, sessionID).Scan(&currentTranscriptLock); err != nil {
		t.Fatal(err)
	}
	budgetRevisionTwo := requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/transcript-revisions", map[string]any{
		"media_asset_id": mediaID, "base_revision_id": longRevision["id"], "transcript_lock_version": currentTranscriptLock,
		"reason": "预算并发使用独立固定输入", "segments": []map[string]any{{"segment_no": 0, "start_ms": 1000, "end_ms": 3000, "text_content": "第二个预算竞争批次", "speaker_label": "teacher"}},
	}, csrf, http.StatusCreated)
	budgetSessionTwo := queryID(t, pool, `INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by) VALUES('`+offering+`','P4 budget peer',now()+interval '2 hours',now()+interval '3 hours','`+teacherID+`') RETURNING id::text`)
	budgetSourceTwo := queryID(t, pool, `INSERT INTO teaching.source_records(session_id,source_type,title,attribution,rights_status,allowed_uses,external_processing_allowed,registered_by,verified_by,verified_at) VALUES('`+budgetSessionTwo+`','self_recorded','P4 budget peer','team','verified','["playback","analysis"]',true,'`+teacherID+`','`+teacherID+`',now()) RETURNING id::text`)
	budgetStaged, stageErr := store.Stage(ctx, bytes.NewReader(video), 1<<20)
	if stageErr != nil {
		t.Fatal(stageErr)
	}
	if stageErr = store.Commit(budgetStaged, "source/p4-budget-peer.mp4"); stageErr != nil {
		t.Fatal(stageErr)
	}
	budgetMediaTwo := queryID(t, pool, `INSERT INTO teaching.media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,duration_ms,status) VALUES('`+budgetSessionTwo+`','`+budgetSourceTwo+`','source','filesystem','source/p4-budget-peer.mp4','`+strings.Repeat("c", 64)+`','video/mp4',24,60000,'ready') RETURNING id::text`)
	budgetSourceRun := queryID(t, pool, `INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,pipeline_version,config_snapshot,mode,status,request_sha256,finished_at)
		SELECT '`+budgetSessionTwo+`','`+budgetMediaTwo+`','`+teacherID+`','budget-peer-source','`+strings.Repeat("c", 64)+`',config_hash,pipeline_version,config_snapshot,'full','succeeded','`+strings.Repeat("e", 64)+`',now() FROM teaching.analysis_runs WHERE id='`+fullID+`' RETURNING id::text`)
	budgetRevisionPeer := queryID(t, pool, `INSERT INTO teaching.transcript_revisions(session_id,media_asset_id,source_run_id,revision_no,source_type,content_sha256,created_by,reason) VALUES('`+budgetSessionTwo+`','`+budgetMediaTwo+`','`+budgetSourceRun+`',1,'asr','`+strings.Repeat("d", 64)+`','`+teacherID+`','budget concurrency peer') RETURNING id::text`)
	if _, err = pool.Exec(ctx, `INSERT INTO teaching.transcript_revision_segments(revision_id,segment_no,start_ms,end_ms,text_content,speaker_label) VALUES($1,0,1000,3000,'跨课堂预算竞争','teacher')`, budgetRevisionPeer); err != nil {
		t.Fatal(err)
	}
	budgetReportTwo := map[string]any{"media_asset_id": budgetMediaTwo, "mode": "report_only", "input_transcript_revision_id": budgetRevisionPeer, "config_profile": "p0-v1"}
	budgetRunOne := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", reportOnly, csrf, map[string]string{"Idempotency-Key": "budget-race-1"}, http.StatusAccepted)
	budgetRunTwo := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+budgetSessionTwo+"/analysis-runs", budgetReportTwo, csrf, map[string]string{"Idempotency-Key": "budget-race-2"}, http.StatusAccepted)
	for range 2 {
		if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
			t.Fatalf("prepare budget report: processed=%v err=%v", processed, processErr)
		}
	}
	gate := &reportProviderGate{started: make(chan struct{}), release: make(chan struct{})}
	providerGates.Store(budgetRunOne["id"].(string), gate)
	budgetResult := make(chan error, 1)
	callCountBefore := reportCalls.Load()
	go func() {
		processed, processErr := analysisService.ProcessNextGoJob(ctx)
		if processErr != nil {
			budgetResult <- processErr
			return
		}
		if !processed {
			budgetResult <- fmt.Errorf("first budget report was not processed")
			return
		}
		budgetResult <- nil
	}()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first budget call did not reach the provider")
	}
	if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
		t.Fatalf("second concurrent budget report: processed=%v err=%v", processed, processErr)
	}
	close(gate.release)
	if processErr := <-budgetResult; processErr != nil {
		t.Fatal(processErr)
	}
	providerGates.Delete(budgetRunOne["id"].(string))
	if got := reportCalls.Load() - callCountBefore; got != 1 {
		t.Fatalf("concurrent budget guard allowed %d provider calls, want 1", got)
	}
	budgetBlocked := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+budgetRunTwo["id"].(string), nil, "", http.StatusOK)
	if budgetBlocked["status"] != "partial" || budgetBlocked["error_code"] != "INVALID_RESULT" {
		t.Fatalf("budget-blocked run=%#v", budgetBlocked)
	}
	var blockedCalls int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM teaching.model_calls WHERE run_id=$1`, budgetRunTwo["id"]).Scan(&blockedCalls); err != nil || blockedCalls != 0 {
		t.Fatalf("budget-blocked run persisted model call: count=%d err=%v", blockedCalls, err)
	}
	if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
		t.Fatalf("validate allowed budget report: processed=%v err=%v", processed, processErr)
	}
	if _, err = pool.Exec(ctx, `UPDATE teaching.model_calls SET actual_cost=0.000280 WHERE status='succeeded'`); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT transcript_lock_version FROM teaching.lesson_sessions WHERE id=$1`, sessionID).Scan(&currentTranscriptLock); err != nil {
		t.Fatal(err)
	}
	inFlightRevision := requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/transcript-revisions", map[string]any{
		"media_asset_id": mediaID, "base_revision_id": budgetRevisionTwo["id"], "transcript_lock_version": currentTranscriptLock,
		"reason": "在途记账固定输入", "segments": []map[string]any{{"segment_no": 0, "start_ms": 1000, "end_ms": 3000, "text_content": "在途调用取消测试", "speaker_label": "teacher"}},
	}, csrf, http.StatusCreated)
	inFlightRequest := map[string]any{"media_asset_id": mediaID, "mode": "report_only", "input_transcript_revision_id": inFlightRevision["id"], "config_profile": "p0-v1"}
	inFlightRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", inFlightRequest, csrf, map[string]string{"Idempotency-Key": "in-flight-cancel-1"}, http.StatusAccepted)
	if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
		t.Fatalf("prepare in-flight report: processed=%v err=%v", processed, processErr)
	}
	inFlightGate := &reportProviderGate{started: make(chan struct{}), release: make(chan struct{})}
	providerGates.Store(inFlightRun["id"].(string), inFlightGate)
	inFlightResult := make(chan error, 1)
	go func() {
		processed, processErr := analysisService.ProcessNextGoJob(ctx)
		if processErr == nil && !processed {
			processErr = fmt.Errorf("in-flight report was not processed")
		}
		inFlightResult <- processErr
	}()
	select {
	case <-inFlightGate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight report did not reach the provider")
	}
	requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/analysis-runs/"+inFlightRun["id"].(string)+"/cancel", map[string]any{}, csrf, http.StatusOK)
	close(inFlightGate.release)
	if processErr := <-inFlightResult; processErr != nil {
		t.Fatal(processErr)
	}
	providerGates.Delete(inFlightRun["id"].(string))
	var inFlightCallStatus, inFlightActual string
	if err = pool.QueryRow(ctx, `SELECT status,to_char(actual_cost,'FM999999999990.000000') FROM teaching.model_calls WHERE run_id=$1`, inFlightRun["id"]).Scan(&inFlightCallStatus, &inFlightActual); err != nil || inFlightCallStatus != "succeeded" || inFlightActual != "0.000280" {
		t.Fatalf("in-flight accounting status=%s actual=%s err=%v", inFlightCallStatus, inFlightActual, err)
	}
	inFlightFinished := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+inFlightRun["id"].(string), nil, "", http.StatusOK)
	if inFlightFinished["status"] != "cancelled" || inFlightFinished["report_id"] != nil {
		t.Fatalf("cancelled in-flight result was adopted: %#v", inFlightFinished)
	}
	if err = pool.QueryRow(ctx, `SELECT transcript_lock_version FROM teaching.lesson_sessions WHERE id=$1`, sessionID).Scan(&currentTranscriptLock); err != nil {
		t.Fatal(err)
	}
	unknownRevision := requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/transcript-revisions", map[string]any{
		"media_asset_id": mediaID, "base_revision_id": inFlightRevision["id"], "transcript_lock_version": currentTranscriptLock,
		"reason": "未知账单对账固定输入", "segments": []map[string]any{{"segment_no": 0, "start_ms": 1000, "end_ms": 3000, "text_content": "供应商未返回用量", "speaker_label": "teacher"}},
	}, csrf, http.StatusCreated)
	unknownRequest := map[string]any{"media_asset_id": mediaID, "mode": "report_only", "input_transcript_revision_id": unknownRevision["id"], "config_profile": "p0-v1"}
	unknownRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", unknownRequest, csrf, map[string]string{"Idempotency-Key": "unknown-call-1"}, http.StatusAccepted)
	if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
		t.Fatalf("prepare unknown-call report: processed=%v err=%v", processed, processErr)
	}
	omitUsageRuns.Store(unknownRun["id"].(string), true)
	if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
		t.Fatalf("process unknown-call report: processed=%v err=%v", processed, processErr)
	}
	omitUsageRuns.Delete(unknownRun["id"].(string))
	var unknownCallID, unknownCallStatus string
	var unknownCallLock int
	if err = pool.QueryRow(ctx, `SELECT id::text,status,lock_version FROM teaching.model_calls WHERE run_id=$1`, unknownRun["id"]).Scan(&unknownCallID, &unknownCallStatus, &unknownCallLock); err != nil || unknownCallStatus != "unknown" {
		t.Fatalf("missing usage did not create unknown call: id=%s status=%s lock=%d err=%v", unknownCallID, unknownCallStatus, unknownCallLock, err)
	}
	requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", unknownRequest, csrf, map[string]string{"Idempotency-Key": "unknown-call-blocked"}, http.StatusConflict)
	requestJSON(t, supervisor, http.MethodGet, server.URL+"/api/v1/model-calls?limit=100", nil, "", http.StatusForbidden)
	modelCallPage := requestJSON(t, admin, http.MethodGet, server.URL+"/api/v1/model-calls?limit=100", nil, "", http.StatusOK)
	if len(modelCallPage["items"].([]any)) == 0 {
		t.Fatal("administrator model-call ledger was empty")
	}
	reconciled := requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/model-calls/"+unknownCallID+"/reconcile", map[string]any{"lock_version": unknownCallLock, "status": "failed", "actual_cost": "0.000000", "provider_request_id": nil, "reason": "测试供应商缺少用量，按失败对账"}, adminCSRF, http.StatusOK)
	if reconciled["status"] != "failed" || reconciled["actual_cost"] != "0.000000" {
		t.Fatalf("reconciled model call=%#v", reconciled)
	}
	afterReconcile := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", unknownRequest, csrf, map[string]string{"Idempotency-Key": "unknown-call-after-reconcile"}, http.StatusAccepted)
	requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/analysis-runs/"+afterReconcile["id"].(string)+"/cancel", map[string]any{}, csrf, http.StatusOK)
	if err = pool.QueryRow(ctx, `SELECT transcript_lock_version FROM teaching.lesson_sessions WHERE id=$1`, sessionID).Scan(&currentTranscriptLock); err != nil {
		t.Fatal(err)
	}
	revocationRevision := requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/transcript-revisions", map[string]any{
		"media_asset_id": mediaID, "base_revision_id": unknownRevision["id"], "transcript_lock_version": currentTranscriptLock,
		"reason": "发送前撤权固定输入", "segments": []map[string]any{{"segment_no": 0, "start_ms": 1000, "end_ms": 3000, "text_content": "发送边界撤销外部处理许可", "speaker_label": "teacher"}},
	}, csrf, http.StatusCreated)
	revocationRequest := map[string]any{"media_asset_id": mediaID, "mode": "report_only", "input_transcript_revision_id": revocationRevision["id"], "config_profile": "p0-v1"}
	revocationRun := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", revocationRequest, csrf, map[string]string{"Idempotency-Key": "dispatch-revocation-1"}, http.StatusAccepted)
	if processed, processErr := analysisService.ProcessNextGoJob(ctx); processErr != nil || !processed {
		t.Fatalf("prepare dispatch revocation report: processed=%v err=%v", processed, processErr)
	}
	blocker, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	if _, err = blocker.Exec(ctx, `SELECT 1 FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, sessionID); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatal(err)
	}
	revocationResult := make(chan error, 1)
	callsBeforeRevocation := reportCalls.Load()
	go func() {
		processed, processErr := analysisService.ProcessNextGoJob(ctx)
		if processErr == nil && !processed {
			processErr = fmt.Errorf("dispatch revocation report was not processed")
		}
		revocationResult <- processErr
	}()
	if _, err = pool.Exec(ctx, `UPDATE teaching.source_records SET external_processing_allowed=false,rights_version=rights_version+1 WHERE id=$1`, sourceID); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if processErr := <-revocationResult; processErr != nil {
		t.Fatal(processErr)
	}
	if got := reportCalls.Load() - callsBeforeRevocation; got != 0 {
		t.Fatalf("revoked dispatch sent %d provider calls", got)
	}
	var revokedDispatchCalls int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM teaching.model_calls WHERE run_id=$1`, revocationRun["id"]).Scan(&revokedDispatchCalls); err != nil || revokedDispatchCalls != 0 {
		t.Fatalf("revoked dispatch persisted calls=%d err=%v", revokedDispatchCalls, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE teaching.source_records SET external_processing_allowed=true,rights_version=rights_version+1 WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}

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
	if _, err = pool.Exec(ctx, `UPDATE teaching.media_assets SET expires_at=now()-interval '1 second' WHERE id=$1`, mediaID); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/reports/"+reportID, nil, "", http.StatusOK)
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+fullID+"/results", nil, "", http.StatusGone)
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/transcript-revisions/"+revision["id"].(string), nil, "", http.StatusGone)
	if _, err = pool.Exec(ctx, `UPDATE teaching.media_assets SET expires_at=now()+interval '14 days' WHERE id=$1`, mediaID); err != nil {
		t.Fatal(err)
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
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+fullID+"/results", nil, "", http.StatusForbidden)
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/transcript-revisions/"+revision["id"].(string), nil, "", http.StatusForbidden)
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/reports/"+reportID, nil, "", http.StatusForbidden)
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
