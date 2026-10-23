package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

type deletionFixture struct {
	exportFixture
	service                       *Service
	admin                         identity.Principal
	sessionID, runID, jobID, root string
}

func setupDeletionFixture(t *testing.T) deletionFixture {
	t.Helper()
	f := deletionFixture{exportFixture: setupExportFixture(t, "json"), root: t.TempDir()}
	ctx := context.Background()
	if err := f.pool.QueryRow(ctx, `SELECT session_id::text,run_id::text,requested_by::text FROM teaching.export_jobs WHERE id=$1`, f.exportID).Scan(&f.sessionID, &f.runID, &f.admin.UserID); err != nil {
		t.Fatal(err)
	}
	f.admin.Roles = []identity.RoleBinding{{RoleCode: "sys_admin"}}
	if _, err := f.pool.Exec(ctx, `INSERT INTO teaching.role_bindings(user_id,role_code) VALUES($1,'sys_admin') ON CONFLICT DO NOTHING`, f.admin.UserID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts) VALUES($1,'report','succeeded',1) RETURNING id::text`, f.runID).Scan(&f.jobID); err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(f.root)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := InitializeLedger(filepath.Join(t.TempDir(), "deletions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	f.service = NewService(f.pool, local, ledger, Config{ExecutorID: "recovery-test", Lease: time.Minute, Recheck: time.Millisecond, MaxAttempts: 3})
	return f
}

func TestLedgerReplayPreservesIndependentBilling(t *testing.T) {
	f := setupDeletionFixture(t)
	ctx := context.Background()
	var callID string
	if err := f.pool.QueryRow(ctx, `INSERT INTO teaching.model_calls(run_id,job_id,call_key,provider,model_name,prompt_version,status,reserved_cost,billing_period,price_version,request_sha256,dispatch_started_at) VALUES($1,$2,$3,'fixture','fixture','p0-v1','reserved',1,'2026-10-01','v1',$4,now()) RETURNING id::text`, f.runID, f.jobID, "report:"+f.jobID, strings.Repeat("a", 64)).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	var unsentJob string
	if err := f.pool.QueryRow(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts) VALUES($1,'validate','queued',3) RETURNING id::text`, f.runID).Scan(&unsentJob); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO teaching.model_calls(run_id,job_id,call_key,provider,model_name,prompt_version,status,reserved_cost,billing_period,price_version,request_sha256) VALUES($1,$2,'not-sent','fixture','fixture','p0-v1','reserved',1,'2026-10-01','v1',$3)`, f.runID, unsentJob, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ledger.Append(Tombstone{TombstoneID: "11111111-1111-4111-8111-111111111111", SessionID: f.sessionID, RequestedBy: f.admin.UserID, IdempotencyKey: "restore", Reason: "synthetic restore", RequestSHA256: strings.Repeat("a", 64), RequestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := f.service.ReplayLedger(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	var version int
	if err := f.pool.QueryRow(ctx, `SELECT status,lock_version FROM teaching.model_calls WHERE id=$1`, callID).Scan(&status, &version); err != nil || status != "unknown" || version != 2 {
		t.Fatalf("replay accounting %s version=%d err=%v", status, version, err)
	}
	var released bool
	if err := f.pool.QueryRow(ctx, `SELECT status='failed' AND actual_cost=0 AND settled_at IS NOT NULL FROM teaching.model_calls WHERE call_key='not-sent'`).Scan(&released); err != nil || !released {
		t.Fatalf("unsent reservation retained: %v %v", released, err)
	}
	if worked, err := f.service.ProcessDeletion(ctx); err != nil || !worked {
		t.Fatalf("delete: %v %v", worked, err)
	}
	a := analysis.NewService(f.pool, f.service.store, analysis.Config{ReportTimeout: time.Millisecond})
	if _, err := a.ReapExpired(ctx); err != nil {
		t.Fatal(err)
	}
	call, err := a.ReconcileModelCall(ctx, f.admin, callID, analysis.ReconcileCall{LockVersion: version, Status: "succeeded", ActualCost: "0.500000", Reason: "synthetic provider bill"})
	if err != nil || call.Status != "succeeded" {
		t.Fatalf("orphan bill unreconcilable: %+v %v", call, err)
	}
	if err = f.service.ReplayLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, `SELECT status FROM teaching.model_calls WHERE id=$1`, callID).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("replay overwrote settlement: %s %v", status, err)
	}
	if _, err = a.ReconcileModelCall(ctx, f.admin, callID, analysis.ReconcileCall{LockVersion: version, Status: "succeeded", ActualCost: "0.000000", Reason: "stale version"}); err == nil {
		t.Fatal("stale reconciliation accepted")
	}
}

func TestDatabaseDeletionFailuresStopAtMaxAttempts(t *testing.T) {
	f := setupDeletionFixture(t)
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `CREATE FUNCTION teaching.test_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic deletion fault'; END $$; CREATE TRIGGER test_reject_delete BEFORE DELETE ON teaching.source_records FOR EACH ROW EXECUTE FUNCTION teaching.test_reject_delete()`)
	if err != nil {
		t.Fatal(err)
	}
	defer f.pool.Exec(ctx, `DROP TRIGGER IF EXISTS test_reject_delete ON teaching.source_records; DROP FUNCTION teaching.test_reject_delete()`)
	d, _, err := f.service.CreateDeletion(ctx, f.admin, f.sessionID, "failure", "synthetic failure")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if worked, err := f.service.ProcessDeletion(ctx); !worked || err == nil {
			t.Fatalf("attempt %d not exercised: %v %v", i, worked, err)
		}
		if _, err = f.pool.Exec(ctx, `UPDATE teaching.data_deletion_requests SET available_at=now()-interval '1 second' WHERE id=$1`, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	if worked, err := f.service.ProcessDeletion(ctx); worked || err != nil {
		t.Fatalf("failed task reclaimed: %v %v", worked, err)
	}
	var state, lessonState string
	var attempts int
	if err = f.pool.QueryRow(ctx, `SELECT d.status,d.attempts,ls.status FROM teaching.data_deletion_requests d JOIN teaching.lesson_sessions ls ON ls.id=d.session_id WHERE d.id=$1`, d.ID).Scan(&state, &attempts, &lessonState); err != nil || state != "failed" || attempts != 3 || lessonState != "deleting" {
		t.Fatalf("terminal state %s/%d/%s err=%v", state, attempts, lessonState, err)
	}
	if _, err = f.pool.Exec(ctx, `DROP TRIGGER test_reject_delete ON teaching.source_records`); err != nil {
		t.Fatal(err)
	}
	// The existing admin creation API permits an explicit new cleanup intent.
	if _, _, err = f.service.CreateDeletion(ctx, f.admin, f.sessionID, "admin-resume", "explicit recovery"); err != nil {
		t.Fatal(err)
	}
	if worked, err := f.service.ProcessDeletion(ctx); !worked || err != nil {
		t.Fatalf("admin recovery failed: %v %v", worked, err)
	}
}

func TestDeletionPreservesDerivedMediaParentsUntilRemoval(t *testing.T) {
	f := setupDeletionFixture(t)
	ctx := context.Background()
	var sourceID, proxyID string
	if err := f.pool.QueryRow(ctx, `SELECT id::text FROM teaching.media_assets WHERE session_id=$1 AND kind='source' LIMIT 1`, f.sessionID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	// A real prepared recording has a source -> playback proxy link and derived
	// proxy/audio/keyframe -> source links. Derived parents are required by CHECK.
	for _, kind := range []string{"proxy", "audio", "keyframe"} {
		var id string
		if err := f.pool.QueryRow(ctx, `INSERT INTO teaching.media_assets(session_id,parent_asset_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,status)
			VALUES($1,$2,$3,'filesystem',$4,$5,'application/octet-stream',1,'ready') RETURNING id::text`, f.sessionID, sourceID, kind, "synthetic-derived/"+kind, strings.Repeat("a", 64)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if kind == "proxy" {
			proxyID = id
		}
	}
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.media_assets SET playback_asset_id=$2 WHERE id=$1`, sourceID, proxyID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.CreateDeletion(ctx, f.admin, f.sessionID, "derived-media", "synthetic prepared recording cleanup"); err != nil {
		t.Fatal(err)
	}
	if worked, err := f.service.ProcessDeletion(ctx); !worked || err != nil {
		t.Fatalf("delete derived media: worked=%v err=%v", worked, err)
	}
	var remaining int
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM teaching.media_assets WHERE session_id=$1`, f.sessionID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("media remaining=%d err=%v", remaining, err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT status FROM teaching.lesson_sessions WHERE id=$1`, f.sessionID).Scan(&state); err != nil || state != "deleted" {
		t.Fatalf("classroom state=%s err=%v", state, err)
	}
}

func TestExportConfirmedRollbackRemovesObject(t *testing.T) {
	f := setupDeletionFixture(t)
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `CREATE FUNCTION teaching.test_reject_export_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='succeeded' THEN RAISE EXCEPTION 'synthetic rollback' USING ERRCODE='23514'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER test_reject_export_commit AFTER UPDATE ON teaching.export_jobs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION teaching.test_reject_export_commit()`)
	if err != nil {
		t.Fatal(err)
	}
	defer f.pool.Exec(ctx, `DROP TRIGGER test_reject_export_commit ON teaching.export_jobs; DROP FUNCTION teaching.test_reject_export_commit()`)
	_, err = f.service.ProcessExport(ctx)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
		t.Fatalf("not a confirmed rollback: %v", err)
	}
	var count int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM teaching.media_assets WHERE kind='report_export'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback assets=%d err=%v", count, err)
	}
	files, err := filepath.Glob(filepath.Join(f.root, "exports", f.exportID, "*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("rollback left untracked file: %v %v", files, err)
	}
}

type dropCommitConfirmation struct {
	net.Conn
	armed *atomic.Bool
}

func (c *dropCommitConfirmation) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.armed.Load() && bytes.Contains(p[:n], []byte("COMMIT\x00")) {
		c.armed.Store(false)
		_ = c.Conn.Close()
		return 0, io.ErrUnexpectedEOF
	}
	return n, err
}

func TestExportNetworkCommitLossPreservesObject(t *testing.T) {
	f := setupDeletionFixture(t)
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(f.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	armed := &atomic.Bool{}
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &dropCommitConfirmation{c, armed}, nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	f.service.pool = pool
	armed.Store(true)
	if _, err = f.service.ProcessExport(ctx); err == nil || armed.Load() {
		t.Fatalf("COMMIT loss not exercised: %v", err)
	}
	var key, state string
	if err = f.pool.QueryRow(ctx, `SELECT e.status,m.object_key FROM teaching.export_jobs e JOIN teaching.media_assets m ON m.id=e.output_asset_id WHERE e.id=$1`, f.exportID).Scan(&state, &key); err != nil || state != "succeeded" {
		t.Fatalf("commit did not persist: %s %v", state, err)
	}
	file, err := f.service.store.Open(key)
	if err != nil {
		t.Fatalf("committed bytes removed: %v", err)
	}
	_ = file.Close()
}

func TestPublishedPDFPreservesSourceAndProvenance(t *testing.T) {
	f := setupDeletionFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.source_records SET title='固定来源标题',attribution='固定来源署名' WHERE session_id=$1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.reports SET summary='中文固定摘要',provenance='{"model":{"name":"fixture-model","revision":"fixed-revision"},"prompt_sha256":"fixed-prompt-digest","coverage":[{"start_ms":0,"end_ms":1000}],"limitations":["覆盖不足"]}' WHERE run_id=$1`, f.runID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.export_jobs SET format='pdf' WHERE id=$1`, f.exportID); err != nil {
		t.Fatal(err)
	}
	if worked, err := f.service.ProcessExport(ctx); err != nil || !worked {
		t.Fatalf("PDF export: %v %v", worked, err)
	}
	file, err := f.service.OpenExport(ctx, f.admin, f.exportID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(file.File)
	_ = file.File.Close()
	if err != nil {
		t.Fatal(err)
	}
	var reportID string
	if err = f.pool.QueryRow(ctx, `SELECT report_id::text FROM teaching.export_jobs WHERE id=$1`, f.exportID).Scan(&reportID); err != nil {
		t.Fatal(err)
	}
	snap, err := f.service.buildExport(ctx, reportID)
	if err != nil {
		t.Fatal(err)
	}
	rawSnapshot, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"中文固定摘要", "固定来源标题", "固定来源署名", "fixed-revision", "fixed-prompt-digest"} {
		if !bytes.Contains(rawSnapshot, []byte(want)) {
			t.Fatalf("JSON trace missing: %s", want)
		}
	}
	for _, want := range []string{"中文固定摘要", "固定来源标题", "固定来源署名"} {
		if !strings.Contains(strings.Join(pdfLines(snap), ""), want) {
			t.Fatalf("PDF content missing: %s", want)
		}
	}
	if output := os.Getenv("PDF_VALIDATION_OUTPUT"); output != "" {
		if err = os.WriteFile(strings.TrimSuffix(output, ".pdf")+"-database.pdf", raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.pool.Exec(ctx, `UPDATE teaching.reports SET summary='later content' WHERE run_id=$1`, f.runID); err != nil {
		t.Fatal(err)
	}
	file, err = f.service.OpenExport(ctx, f.admin, f.exportID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := io.ReadAll(file.File)
	_ = file.File.Close()
	if !bytes.Equal(raw, after) {
		t.Fatal("existing export changed")
	}
	if _, err = f.pool.Exec(ctx, `UPDATE teaching.source_records SET rights_status='rejected' WHERE session_id=$1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.OpenExport(ctx, f.admin, f.exportID); err == nil {
		t.Fatal("revoked source export readable")
	}
}

func TestDeletionWaitsForRealWorkerConfirmation(t *testing.T) {
	f := setupDeletionFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts) VALUES($1,'probe','succeeded',3)`, f.runID); err != nil {
		t.Fatal(err)
	}
	d, _, err := f.service.CreateDeletion(ctx, f.admin, f.sessionID, "worker-confirmation", "synthetic deletion")
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := f.service.ProcessDeletion(ctx); !worked || err != nil {
		t.Fatalf("waiting: %v %v", worked, err)
	}
	var status, code string
	if err = f.pool.QueryRow(ctx, `SELECT status,error_code FROM teaching.data_deletion_requests WHERE id=$1`, d.ID).Scan(&status, &code); err != nil || status != "blocked" || code != "WORKER_CLEANUP_PENDING" {
		t.Fatalf("missing worker receipt declared complete: %s/%s %v", status, code, err)
	}
}
