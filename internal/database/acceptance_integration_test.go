package database

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// This suite requires its own disposable database, never the shared dev DB.
func TestDatabaseUpgradeAcceptance(t *testing.T) {
	url := os.Getenv("DATABASE_ACCEPTANCE_URL")
	if url == "" {
		t.Skip("DATABASE_ACCEPTANCE_URL is not set")
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil || cfg.Database != "cangjie_database_acceptance" || os.Getenv("DATABASE_ACCEPTANCE_ALLOW_RESET") != "1" {
		t.Fatal("requires cangjie_database_acceptance and DATABASE_ACCEPTANCE_ALLOW_RESET=1")
	}
	ctx := context.Background()
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('database_acceptance'))`); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..", "database")
	items, err := loadMigrations(root)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	reset := func() {
		if _, err := conn.Exec(ctx, `DROP SCHEMA IF EXISTS teaching CASCADE; DROP TABLE IF EXISTS public.schema_migrations`); err != nil {
			t.Fatal(err)
		}
	}
	defer reset()
	baseline := func() {
		reset()
		if _, err := conn.Exec(ctx, `CREATE TABLE public.schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
			t.Fatal(err)
		}
		for _, item := range items[:6] {
			if err := applyMigration(ctx, conn, item); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Run("fresh_and_idempotent", func(t *testing.T) {
		reset()
		for i := 0; i < 2; i++ {
			if err := RunMigrations(ctx, url, root); err != nil {
				t.Fatal(err)
			}
		}
		if err := pool.CheckSchema(ctx, root); err != nil {
			t.Fatal(err)
		}
		var tables, validated int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='teaching'`).Scan(&tables); err != nil || tables != 37 {
			t.Fatalf("tables=%d err=%v", tables, err)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname='teaching' AND NOT convalidated`).Scan(&validated); err != nil || validated != 0 {
			t.Fatalf("unvalidated=%d err=%v", validated, err)
		}
	})
	t.Run("clean_006_upgrade_and_readiness", func(t *testing.T) {
		baseline()
		if err := pool.CheckSchema(ctx, root); err == nil {
			t.Fatal("pending upgrade was ready")
		}
		if err := RunMigrations(ctx, url, root); err != nil {
			t.Fatal(err)
		}
		if err := pool.CheckSchema(ctx, root); err != nil {
			t.Fatal(err)
		}
		// A migration history alone must not hide constraint drift.
		if _, err := conn.Exec(ctx, `ALTER TABLE teaching.model_calls DROP CONSTRAINT ck_call_settlement`); err != nil {
			t.Fatal(err)
		}
		if err := pool.CheckSchema(ctx, root); err == nil {
			t.Fatal("missing critical constraint was ready")
		}
	})
	t.Run("dirty_settlement_blocks_without_fabricating_cost", func(t *testing.T) {
		baseline()
		// NOT VALID applies to new writes; simulate a genuine pre-003 historical row.
		if _, err := conn.Exec(ctx, `ALTER TABLE teaching.model_calls DROP CONSTRAINT ck_call_settlement;
		INSERT INTO teaching.model_calls(call_key,provider,model_name,prompt_version,status,reserved_cost,billing_period)
		VALUES('legacy-dirty','synthetic','synthetic','p0-v1','succeeded',1,'2026-10-01');
		ALTER TABLE teaching.model_calls ADD CONSTRAINT ck_call_settlement CHECK (
		(status IN('reserved','unknown') AND actual_cost IS NULL AND settled_at IS NULL)
		OR (status IN('succeeded','failed') AND actual_cost IS NOT NULL AND settled_at IS NOT NULL)) NOT VALID`); err != nil {
			t.Fatal(err)
		}
		if err := RunMigrations(ctx, url, root); err == nil || !strings.Contains(err.Error(), "ck_call_settlement") {
			t.Fatalf("expected settlement rejection: %v", err)
		}
		var recorded bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.schema_migrations WHERE name='007_validate_runtime_constraints.sql')`).Scan(&recorded); err != nil || recorded {
			t.Fatalf("failed migration recorded: %v", err)
		}
		var jobValidated bool
		if err := conn.QueryRow(ctx, `SELECT convalidated FROM pg_constraint WHERE conrelid='teaching.analysis_jobs'::regclass AND conname='ck_job_execution'`).Scan(&jobValidated); err != nil || jobValidated {
			t.Fatalf("failed validation did not roll back preceding constraint validation: %v", err)
		}
		var preserved bool
		if err := conn.QueryRow(ctx, `SELECT actual_cost IS NULL AND settled_at IS NULL FROM teaching.model_calls WHERE call_key='legacy-dirty'`).Scan(&preserved); err != nil || !preserved {
			t.Fatal("legacy cost was changed")
		}
		// Explicit synthetic reconciliation, not an automatic migration repair.
		if _, err := conn.Exec(ctx, `UPDATE teaching.model_calls SET actual_cost=0.25,settled_at=now(),reconciliation_reason='synthetic verified bill' WHERE call_key='legacy-dirty'`); err != nil {
			t.Fatal(err)
		}
		if err := RunMigrations(ctx, url, root); err != nil {
			t.Fatal(err)
		}
		if err := pool.CheckSchema(ctx, root); err != nil {
			t.Fatal(err)
		}
	})
}
