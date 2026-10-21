// Package devseed supplies explicitly synthetic, non-media development fixtures.
package devseed

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

//go:embed testdata/nonvideo.sql
var fixtureSQL string

// Counts are the records owned by nonvideo-v1, not total database row counts.
func Plan() map[string]int {
	return map[string]int{"org_units": 6, "user_accounts": 14, "role_bindings": 14,
		"academic_terms": 3, "courses": 8, "class_groups": 8, "classrooms": 5,
		"course_offerings": 12, "schedule_entries": 48, "lesson_sessions": 44, "source_records": 12}
}

type Request struct {
	Environment string
	Password    string
}

func (r Request) Validate() error {
	if r.Environment != "development" && r.Environment != "test" {
		return fmt.Errorf("non-video fixtures require APP_ENV=development or test")
	}
	if len(r.Password) < 12 || len(r.Password) > 72 || strings.TrimSpace(r.Password) != r.Password {
		return fmt.Errorf("BOOTSTRAP_TEST_PASSWORD must contain 12–72 bytes with no surrounding whitespace")
	}
	return nil
}

// Apply creates only missing fixture records in one transaction. Existing fixture
// passwords, edits and statuses are preserved; natural-key collisions roll back.
// Media, captures, jobs, transcripts, reports and model calls are never inserted.
func Apply(ctx context.Context, pool *pgxpool.Pool, request Request) (map[string]int, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash synthetic account password: %w", err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin non-video seed: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('nonvideo-fixtures-v1'))`); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE nonvideo_seed_context(password_hash text NOT NULL) ON COMMIT DROP`); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO nonvideo_seed_context VALUES($1)`, string(hash)); err != nil {
		return nil, err
	}
	// No bind arguments: pgx uses simple protocol for this embedded multi-statement SQL.
	if _, err := tx.Exec(ctx, fixtureSQL); err != nil {
		return nil, fmt.Errorf("insert synthetic fixtures (nothing committed): %w", err)
	}
	actual := map[string]int{}
	for table, expected := range Plan() {
		// table is a hard-coded identifier from Plan(), never supplied by a caller.
		var count int
		query := fmt.Sprintf(`SELECT count(*) FROM teaching.%s t JOIN nonvideo_seed_manifest m ON m.kind=$1 AND m.id=t.id`, table)
		if err := tx.QueryRow(ctx, query, table).Scan(&count); err != nil {
			return nil, err
		}
		if count != expected {
			return nil, fmt.Errorf("fixture count mismatch for %s: got %d, want %d", table, count, expected)
		}
		actual[table] = count
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit non-video fixtures: %w", err)
	}
	return actual, nil
}
