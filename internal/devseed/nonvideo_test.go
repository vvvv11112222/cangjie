package devseed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/database"
	"golang.org/x/crypto/bcrypt"
)

func randomPassword(t *testing.T) string {
	t.Helper()
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b[:])
}

func TestRequestValidation(t *testing.T) {
	p := randomPassword(t)
	for _, environment := range []string{"production", "", "staging"} {
		if (Request{environment, p}).Validate() == nil {
			t.Fatalf("accepted forbidden environment %q", environment)
		}
	}
	for _, password := range []string{"", p[:11], " " + p, p + " "} {
		if (Request{"development", password}).Validate() == nil {
			t.Fatal("accepted invalid password length or whitespace")
		}
	}
	for _, environment := range []string{"development", "test"} {
		if err := (Request{environment, p}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlanIsIndependentAndExcludesMedia(t *testing.T) {
	a := Plan()
	a["courses"] = 0
	if Plan()["courses"] != 8 {
		t.Fatal("Plan exposes mutable global state")
	}
	for _, table := range []string{"media_assets", "live_captures", "analysis_runs", "transcript_segments", "reports", "model_calls"} {
		if _, ok := Plan()[table]; ok {
			t.Fatalf("media-dependent table %s in non-video plan", table)
		}
	}
}

// Only runs against an explicitly provisioned, empty, dedicated fixture test DB.
// No DROP/TRUNCATE is performed. Subsequent invocations should use a new empty DB.
func TestNonVideoFixturesPostgres(t *testing.T) {
	url := os.Getenv("DEVSEED_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("DEVSEED_TEST_DATABASE_URL not set; requires an empty dedicated test database")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	var exists bool
	err = conn.QueryRow(ctx, "SELECT to_regnamespace('teaching') IS NOT NULL").Scan(&exists)
	conn.Close(ctx) //nolint:errcheck
	if err != nil || exists {
		t.Fatalf("requires empty database; teaching schema exists=%t err=%v", exists, err)
	}
	dir, err := filepath.Abs(filepath.Join("..", "..", "database"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RunMigrations(ctx, url, dir); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// An unrelated existing row must survive both seed and rollback paths.
	if _, err := pool.Exec(ctx, `INSERT INTO teaching.classrooms(code,name) VALUES('EXISTING_ROOM','Existing room')`); err != nil {
		t.Fatal(err)
	}
	password := randomPassword(t)
	counts, err := Apply(ctx, pool, Request{"test", password})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(counts, Plan()) {
		t.Fatalf("counts=%v", counts)
	}
	var firstHash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM teaching.user_accounts WHERE username='testdata_teacher_1'`).Scan(&firstHash); err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(firstHash), []byte(password)); err != nil {
		t.Fatal("seed password cannot authenticate")
	}
	if _, err := pool.Exec(ctx, `UPDATE teaching.courses SET description='Preserve this edit' WHERE code='TESTDATA_COURSE_1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, pool, Request{"test", randomPassword(t)}); err != nil {
		t.Fatalf("second seed must be idempotent: %v", err)
	}
	var afterHash, description string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM teaching.user_accounts WHERE username='testdata_teacher_1'`).Scan(&afterHash); err != nil {
		t.Fatal(err)
	}
	if afterHash != firstHash {
		t.Fatal("rerun replaced existing account password")
	}
	if err := pool.QueryRow(ctx, `SELECT description FROM teaching.courses WHERE code='TESTDATA_COURSE_1'`).Scan(&description); err != nil || description != "Preserve this edit" {
		t.Fatalf("existing description changed: %s / %v", description, err)
	}
	for _, table := range []string{"media_assets", "live_captures", "analysis_runs", "analysis_jobs", "transcript_segments", "reports", "model_calls"} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM teaching."+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unexpected media-dependent rows in %s: %d / %v", table, count, err)
		}
	}
	var scopes, lessons, external, existing int
	checks := []struct {
		query string
		dest  *int
		want  int
	}{
		{`SELECT count(*) FROM teaching.role_bindings WHERE role_code IN ('academic_admin','supervisor') AND scope_org_id IS NOT NULL`, &scopes, 4},
		{`SELECT count(*) FROM teaching.lesson_sessions WHERE is_demo AND status IN ('planned','archived')`, &lessons, 44},
		{`SELECT count(*) FROM teaching.source_records WHERE rights_status<>'pending' OR external_processing_allowed OR redistribution_allowed`, &external, 0},
		{`SELECT count(*) FROM teaching.classrooms WHERE code='EXISTING_ROOM'`, &existing, 1},
	}
	for _, check := range checks {
		if err := pool.QueryRow(ctx, check.query).Scan(check.dest); err != nil || *check.dest != check.want {
			t.Fatalf("check %s: got=%d want=%d err=%v", check.query, *check.dest, check.want, err)
		}
	}
	// An incompatible existing identity must roll back inserts already made.
	if _, err := pool.Exec(ctx, `DELETE FROM teaching.org_units WHERE code='TESTDATA_COLLEGE_3';
		UPDATE teaching.user_accounts SET username='changed_fixture_teacher' WHERE username='testdata_teacher_1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, pool, Request{"test", password}); err == nil {
		t.Fatal("incompatible existing fixture account accepted")
	}
	var recreated int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM teaching.org_units WHERE code='TESTDATA_COLLEGE_3'`).Scan(&recreated); err != nil || recreated != 0 {
		t.Fatalf("failed seed left partial inserts: count=%d err=%v", recreated, err)
	}
	if _, err := Apply(ctx, pool, Request{"production", password}); err == nil {
		t.Fatal("production apply accepted")
	}
}
