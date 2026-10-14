package governance

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

type exportFixture struct {
	pool     *pgxpool.Pool
	exportID string
}

func setupExportFixture(t *testing.T, format string) exportFixture {
	t.Helper()
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
	t.Cleanup(func() { _ = fixtureLock.Close(context.Background()) })
	migrationDir, _ := filepath.Abs(filepath.Join("..", "..", "database"))
	if err = database.RunMigrations(ctx, databaseURL, migrationDir); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Exec(ctx, `TRUNCATE teaching.academic_terms,teaching.classrooms,teaching.org_units CASCADE`); err != nil {
		t.Fatal(err)
	}
	var schoolID, orgID, userID, roleUserID, termID, courseID, groupID, offeringID, sessionID, sourceID, mediaID, runID, reportID, exportID string
	queries := []struct {
		dst   *string
		query string
		args  []any
	}{
		{&schoolID, `INSERT INTO teaching.org_units(code,name,kind) VALUES('EXPORT-SCHOOL','Export School','school') RETURNING id::text`, nil},
		{&orgID, `INSERT INTO teaching.org_units(parent_id,code,name,kind) VALUES($1,'EXPORT-COLLEGE','Export College','college') RETURNING id::text`, []any{&schoolID}},
		{&userID, `INSERT INTO teaching.user_accounts(org_unit_id,username,password_hash,display_name) VALUES($1,'export-admin','fixture','Export Admin') RETURNING id::text`, []any{&orgID}},
		{&roleUserID, `INSERT INTO teaching.role_bindings(user_id,role_code) VALUES($1,'teacher') RETURNING user_id::text`, []any{&userID}},
		{&termID, `INSERT INTO teaching.academic_terms(code,name,start_date,end_date) VALUES('EXPORT-T','Export Term','2026-09-01','2027-01-31') RETURNING id::text`, nil},
		{&courseID, `INSERT INTO teaching.courses(org_unit_id,code,name) VALUES($1,'EXPORT-C','Export Course') RETURNING id::text`, []any{&orgID}},
		{&groupID, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES($1,'EXPORT-G','Export Group',2026) RETURNING id::text`, []any{&orgID}},
		{&offeringID, `INSERT INTO teaching.course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id) VALUES($1,'EXPORT-O',$2,$3,$4,$5) RETURNING id::text`, []any{&orgID, &termID, &courseID, &userID, &groupID}},
		{&sessionID, `INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by,status) VALUES($1,'Export Session',now(),now()+interval '1 hour',$2,'ready') RETURNING id::text`, []any{&offeringID, &userID}},
		{&sourceID, `INSERT INTO teaching.source_records(session_id,source_type,title,attribution,rights_status,allowed_uses,registered_by,verified_by,verified_at) VALUES($1,'self_recorded','Export Source','team','verified','["analysis","export"]',$2,$2,now()) RETURNING id::text`, []any{&sessionID, &userID}},
		{&mediaID, `INSERT INTO teaching.media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,status) VALUES($1,$2,'source','filesystem','source/export.mp4',$3,'video/mp4',1,'ready') RETURNING id::text`, []any{&sessionID, &sourceID, strings.Repeat("a", 64)}},
		{&runID, `INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,pipeline_version,config_snapshot,status,request_sha256,finished_at) VALUES($1,$2,$3,'export-run',$4,$5,'p0-v1','{}','succeeded',$6,now()) RETURNING id::text`, []any{&sessionID, &mediaID, &userID, strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)}},
		{&reportID, `INSERT INTO teaching.reports(run_id,session_id,revision,status,summary,content_sha256,reviewed_content_sha256,reviewed_by,reviewed_at,created_by,published_by,published_at,provenance) VALUES($1,$2,1,'published','Export report',$3,$3,$4,now(),$4,$4,now(),'{}') RETURNING id::text`, []any{&runID, &sessionID, strings.Repeat("e", 64), &userID}},
		{&exportID, `INSERT INTO teaching.export_jobs(report_id,run_id,session_id,requested_by,format,idempotency_key,request_sha256,expires_at,storage_generation) SELECT $1,$2,$3,$4,$5,'export-fixture',$6,now()+interval '1 hour',storage_generation FROM teaching.lesson_sessions WHERE id=$3 RETURNING id::text`, []any{&reportID, &runID, &sessionID, &userID, format, strings.Repeat("f", 64)}},
	}
	for _, item := range queries {
		args := make([]any, len(item.args))
		for i, arg := range item.args {
			if value, ok := arg.(*string); ok {
				args[i] = *value
			} else {
				args[i] = arg
			}
		}
		if err = pool.QueryRow(ctx, item.query, args...).Scan(item.dst); err != nil {
			t.Fatalf("seed export fixture: %v", err)
		}
	}
	return exportFixture{pool: pool, exportID: exportID}
}

type firstCommitGate struct {
	storage.Backend
	entered chan string
	release chan struct{}
	used    atomic.Bool
}

func (g *firstCommitGate) Commit(staged *storage.StagedObject, key string) error {
	if g.used.CompareAndSwap(false, true) {
		g.entered <- key
		<-g.release
	}
	return g.Backend.Commit(staged, key)
}

func TestStaleExportExecutorCannotDeleteWinningObject(t *testing.T) {
	fixture := setupExportFixture(t, "json")
	ctx := context.Background()
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gate := &firstCommitGate{Backend: local, entered: make(chan string, 1), release: make(chan struct{})}
	config := Config{Lease: time.Minute, Recheck: time.Millisecond, MaxAttempts: 3, ExportRetention: time.Hour, ExecutorID: "executor-a"}
	first := NewService(fixture.pool, gate, nil, config)
	firstResult := make(chan error, 1)
	go func() {
		_, processErr := first.ProcessExport(ctx)
		firstResult <- processErr
	}()
	staleKey := <-gate.entered
	if _, err = fixture.pool.Exec(ctx, `UPDATE teaching.export_jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, fixture.exportID); err != nil {
		t.Fatal(err)
	}
	config.ExecutorID = "executor-b"
	winner := NewService(fixture.pool, gate, nil, config)
	if worked, processErr := winner.ProcessExport(ctx); processErr != nil || !worked {
		t.Fatalf("winning executor worked=%v err=%v", worked, processErr)
	}
	close(gate.release)
	if processErr := <-firstResult; processErr == nil {
		t.Fatal("stale executor unexpectedly committed database state")
	}
	var status, winningKey string
	if err = fixture.pool.QueryRow(ctx, `SELECT e.status,m.object_key FROM teaching.export_jobs e JOIN teaching.media_assets m ON m.id=e.output_asset_id WHERE e.id=$1`, fixture.exportID).Scan(&status, &winningKey); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || winningKey == staleKey {
		t.Fatalf("status=%s winning_key=%s stale_key=%s", status, winningKey, staleKey)
	}
	file, err := local.Open(winningKey)
	if err != nil {
		t.Fatalf("winning export was removed: %v", err)
	}
	_, _ = io.Copy(io.Discard, file)
	_ = file.Close()
	if stale, staleErr := local.Open(staleKey); staleErr == nil {
		_ = stale.Close()
		t.Fatal("stale executor object survived cleanup")
	}
}

func TestExportCommitConfirmationLossPreservesCommittedObject(t *testing.T) {
	fixture := setupExportFixture(t, "json")
	ctx := context.Background()
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(fixture.pool, local, nil, Config{Lease: time.Minute, Recheck: time.Millisecond, MaxAttempts: 3, ExportRetention: time.Hour, ExecutorID: "uncertain-commit"})
	lostConfirmation := errors.New("commit confirmation lost")
	service.commitExportTx = func(ctx context.Context, tx pgx.Tx) error {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return commitErr
		}
		return lostConfirmation
	}
	worked, processErr := service.ProcessExport(ctx)
	if !worked || !errors.Is(processErr, lostConfirmation) {
		t.Fatalf("worked=%v err=%v", worked, processErr)
	}
	var status, key string
	if err = fixture.pool.QueryRow(ctx, `SELECT e.status,m.object_key FROM teaching.export_jobs e JOIN teaching.media_assets m ON m.id=e.output_asset_id WHERE e.id=$1`, fixture.exportID).Scan(&status, &key); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Fatalf("committed export status=%s", status)
	}
	file, err := local.Open(key)
	if err != nil {
		t.Fatalf("object referenced by committed export was removed: %v", err)
	}
	_ = file.Close()
}
