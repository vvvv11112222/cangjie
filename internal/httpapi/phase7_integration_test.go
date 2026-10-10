package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
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
	"github.com/vvvv11112222/cangjie/internal/classroom"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/governance"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

func TestPhaseSevenExportGovernanceAndDeletionRecovery(t *testing.T) {
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
	adminID := queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='admin'`)
	term := queryID(t, pool, `INSERT INTO teaching.academic_terms(code,name,start_date,end_date) VALUES('P7-T','P7','2026-09-01','2027-01-01') RETURNING id::text`)
	course := queryID(t, pool, `INSERT INTO teaching.courses(org_unit_id,code,name) VALUES('`+college+`','P7-C','P7') RETURNING id::text`)
	group := queryID(t, pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES('`+college+`','P7-G','P7',2026) RETURNING id::text`)
	offering := queryID(t, pool, `INSERT INTO teaching.course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id) VALUES('`+college+`','P7-O','`+term+`','`+course+`','`+teacherID+`','`+group+`') RETURNING id::text`)
	sessionID := queryID(t, pool, `INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by,status) VALUES('`+offering+`','P7 classroom',now(),now()+interval '1 hour','`+teacherID+`','ready') RETURNING id::text`)
	sourceID := queryID(t, pool, `INSERT INTO teaching.source_records(session_id,source_type,title,attribution,rights_status,allowed_uses,registered_by,verified_by,verified_at) VALUES('`+sessionID+`','self_recorded','P7 source','team','verified','["playback","analysis","export"]','`+teacherID+`','`+adminID+`',now()) RETURNING id::text`)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	video := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	staged, err := store.Stage(ctx, bytes.NewReader(video), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Commit(staged, "source/p7.mp4"); err != nil {
		t.Fatal(err)
	}
	mediaID := queryID(t, pool, `INSERT INTO teaching.media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,duration_ms,status) VALUES('`+sessionID+`','`+sourceID+`','source','filesystem','source/p7.mp4','`+strings.Repeat("a", 64)+`','video/mp4',12,60000,'ready') RETURNING id::text`)
	runID := queryID(t, pool, `INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,pipeline_version,config_snapshot,status,request_sha256,finished_at) VALUES('`+sessionID+`','`+mediaID+`','`+teacherID+`','p7-run','`+strings.Repeat("a", 64)+`','`+strings.Repeat("b", 64)+`','p0-v1','{}','succeeded','`+strings.Repeat("c", 64)+`',now()) RETURNING id::text`)
	segmentID := queryID(t, pool, `INSERT INTO teaching.transcript_segments(run_id,session_id,segment_no,start_ms,end_ms,text_content) VALUES('`+runID+`','`+sessionID+`',0,0,1000,'仅用于生命周期测试') RETURNING id::text`)
	evidenceID := queryID(t, pool, `INSERT INTO teaching.evidence_items(run_id,session_id,media_asset_id,transcript_segment_id,kind,start_ms,end_ms,description) VALUES('`+runID+`','`+sessionID+`','`+mediaID+`','`+segmentID+`','transcript',0,1000,'生命周期证据') RETURNING id::text`)
	reportID := queryID(t, pool, `INSERT INTO teaching.reports(run_id,session_id,revision,status,summary,content_sha256,reviewed_content_sha256,reviewed_by,reviewed_at,created_by,published_by,published_at,provenance) VALUES('`+runID+`','`+sessionID+`',1,'published','阶段七固定报告','`+strings.Repeat("d", 64)+`','`+strings.Repeat("d", 64)+`','`+adminID+`',now(),'`+teacherID+`','`+adminID+`',now(),'{}') RETURNING id::text`)
	jobID := queryID(t, pool, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,progress) VALUES('`+runID+`','report','succeeded',1,100) RETURNING id::text`)
	modelCallID := queryID(t, pool, `INSERT INTO teaching.model_calls(run_id,job_id,call_key,provider,model_name,prompt_version,reserved_cost,actual_cost,status,settled_at,billing_period,price_version,request_sha256) VALUES('`+runID+`','`+jobID+`','p7-call','fixture','fixture','p0-v1',1.000000,0.500000,'succeeded',now(),date_trunc('month',now() AT TIME ZONE 'Asia/Shanghai')::date,'p7-price','`+strings.Repeat("e", 64)+`') RETURNING id::text`)
	ledger, err := governance.OpenLedger(filepath.Join(t.TempDir(), "deletions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	workerRoot := t.TempDir()
	workerJobDir := filepath.Join(workerRoot, jobID)
	if err = os.MkdirAll(workerJobDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workerJobDir, "partial.tmp"), []byte("temporary classroom output"), 0o600); err != nil {
		t.Fatal(err)
	}
	gov := governance.NewService(pool, store, ledger, governance.Config{Lease: time.Minute, Recheck: time.Second, MaxAttempts: 3, ExportRetention: time.Hour, ExecutorID: "test-governance", BudgetCurrency: "CNY", BudgetMonthlyMicros: 10_000_000, WorkerTempRoot: workerRoot})
	if err = gov.RecordWorker(ctx, "worker-01", []string{"video_analysis", "probe"}); err != nil {
		t.Fatal(err)
	}
	handler := New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Identity: identity.NewService(pool, 8*time.Hour), Academic: academic.NewService(pool), Classroom: classroom.NewService(pool), Governance: gov, PublicOrigin: "http://frontend.test"})
	server := httptest.NewServer(handler)
	defer server.Close()
	teacher := newTestClient(t)
	teacherCSRF := loginTestUser(t, teacher, server.URL, "teacher")
	admin := newTestClient(t)
	adminCSRF := loginTestUser(t, admin, server.URL, "admin")
	supervisor := newTestClient(t)
	_ = loginTestUser(t, supervisor, server.URL, "supervisor")
	exported := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/reports/"+reportID+"/exports", map[string]any{"format": "json"}, teacherCSRF, map[string]string{"Idempotency-Key": "export-1"}, http.StatusAccepted)
	exportID := exported["id"].(string)
	replayed := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/reports/"+reportID+"/exports", map[string]any{"format": "json"}, teacherCSRF, map[string]string{"Idempotency-Key": "export-1"}, http.StatusOK)
	if replayed["id"] != exportID {
		t.Fatalf("export replay=%v", replayed)
	}
	if worked, processErr := gov.ProcessExport(ctx); processErr != nil || !worked {
		t.Fatalf("process export worked=%v err=%v", worked, processErr)
	}
	ready := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/exports/"+exportID, nil, "", http.StatusOK)
	if ready["status"] != "succeeded" || ready["download_url"] == nil {
		t.Fatalf("export=%#v", ready)
	}
	requestJSON(t, supervisor, http.MethodGet, server.URL+"/api/v1/exports/"+exportID, nil, "", http.StatusNotFound)
	response, err := teacher.Get(server.URL + "/api/v1/exports/" + exportID + "/content")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("阶段七固定报告")) {
		t.Fatalf("download status=%d body=%s", response.StatusCode, body)
	}
	pdfExport := requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/reports/"+reportID+"/exports", map[string]any{"format": "pdf"}, teacherCSRF, map[string]string{"Idempotency-Key": "export-pdf"}, http.StatusAccepted)
	if worked, processErr := gov.ProcessExport(ctx); processErr != nil || !worked {
		t.Fatalf("process PDF export worked=%v err=%v", worked, processErr)
	}
	pdfResponse, pdfErr := teacher.Get(server.URL + "/api/v1/exports/" + pdfExport["id"].(string) + "/content")
	if pdfErr != nil {
		t.Fatal(pdfErr)
	}
	pdfBody, _ := io.ReadAll(pdfResponse.Body)
	pdfResponse.Body.Close()
	if pdfResponse.StatusCode != http.StatusOK || !bytes.HasPrefix(pdfBody, []byte("%PDF-1.4")) {
		t.Fatalf("PDF status=%d prefix=%q", pdfResponse.StatusCode, pdfBody[:min(8, len(pdfBody))])
	}
	if _, err = pool.Exec(ctx, `UPDATE teaching.source_records SET allowed_uses='["playback","analysis"]',rights_version=rights_version+1 WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/exports/"+exportID+"/content", nil, "", http.StatusGone)
	if _, err = pool.Exec(ctx, `UPDATE teaching.source_records SET allowed_uses='["playback","analysis","export"]',rights_version=rights_version+1 WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/operations", nil, "", http.StatusForbidden)
	operations := requestJSON(t, admin, http.MethodGet, server.URL+"/api/v1/operations", nil, "", http.StatusOK)
	if operations["budget"].(map[string]any)["limit"] != "10.000000" || len(operations["workers"].([]any)) != 1 || operations["storage_free_bytes"].(float64) <= 0 {
		t.Fatalf("operations=%#v", operations)
	}
	if _, err = pool.Exec(ctx, `UPDATE teaching.media_assets SET expires_at=now()-interval '1 second' WHERE id=$1`, mediaID); err != nil {
		t.Fatal(err)
	}
	if removed, sweepErr := gov.SweepExpired(ctx); sweepErr != nil || removed != 1 {
		t.Fatalf("expiry sweep removed=%d err=%v", removed, sweepErr)
	}
	var availability string
	if err = pool.QueryRow(ctx, `SELECT availability FROM teaching.evidence_items WHERE id=$1`, evidenceID).Scan(&availability); err != nil || availability != "expired" {
		t.Fatalf("availability=%s err=%v", availability, err)
	}
	requestWithHeaders(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/deletion-requests", map[string]any{"reason": "retention request"}, teacherCSRF, map[string]string{"Idempotency-Key": "delete-denied"}, http.StatusForbidden)
	deletion := requestWithHeaders(t, admin, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/deletion-requests", map[string]any{"reason": "retention request"}, adminCSRF, map[string]string{"Idempotency-Key": "delete-1"}, http.StatusAccepted)
	deletionID := deletion["id"].(string)
	revokedExport := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/exports/"+exportID, nil, "", http.StatusOK)
	if revokedExport["download_url"] != nil || revokedExport["status"] != "expired" {
		t.Fatalf("revoked export=%#v", revokedExport)
	}
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/exports/"+exportID+"/content", nil, "", http.StatusGone)
	if worked, processErr := gov.ProcessDeletion(ctx); processErr != nil || !worked {
		t.Fatalf("process deletion worked=%v err=%v", worked, processErr)
	}
	done := requestJSON(t, admin, http.MethodGet, server.URL+"/api/v1/deletion-requests/"+deletionID, nil, "", http.StatusOK)
	if done["status"] != "succeeded" {
		t.Fatalf("deletion=%#v", done)
	}
	var sessionStatus, title string
	if err = pool.QueryRow(ctx, `SELECT status,title FROM teaching.lesson_sessions WHERE id=$1`, sessionID).Scan(&sessionStatus, &title); err != nil || sessionStatus != "deleted" || title != "[deleted]" {
		t.Fatalf("session status=%s title=%s err=%v", sessionStatus, title, err)
	}
	if _, statErr := os.Stat(workerJobDir); !os.IsNotExist(statErr) {
		t.Fatalf("worker temp survived deletion: %v", statErr)
	}
	var retained int
	var linked bool
	if err = pool.QueryRow(ctx, `SELECT count(*),bool_or(run_id IS NOT NULL OR job_id IS NOT NULL) FROM teaching.model_calls WHERE id=$1`, modelCallID).Scan(&retained, &linked); err != nil || retained != 1 || linked {
		t.Fatalf("retained model call count=%d linked=%v err=%v", retained, linked, err)
	}
	replaySession := queryID(t, pool, `INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by) VALUES('`+offering+`','Replay target',now()+interval '2 hours',now()+interval '3 hours','`+teacherID+`') RETURNING id::text`)
	if err = ledger.Append(governance.Tombstone{TombstoneID: "11111111-1111-4111-8111-111111111111", SessionID: replaySession, RequestedBy: adminID, Reason: "recovery replay", IdempotencyKey: "replay-1", RequestSHA256: strings.Repeat("f", 64), RequestedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err = gov.ReplayLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM teaching.lesson_sessions WHERE id=$1`, replaySession).Scan(&sessionStatus); err != nil || sessionStatus != "deleting" {
		t.Fatalf("replayed status=%s err=%v", sessionStatus, err)
	}
}
