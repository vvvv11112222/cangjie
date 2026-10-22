package modelsettings

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelSettingsApplyPersistsPrivateKeysAndFencesOldRevisions(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil || parsed.Database != "cangjie_backend_acceptance" {
		t.Fatal("requires disposable cangjie_backend_acceptance")
	}
	ctx := context.Background()
	lock, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close(ctx)
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('cangjie_integration_test_fixture',0))`); err != nil {
		t.Fatal(err)
	}
	directory, _ := filepath.Abs("../../database")
	if err = database.RunMigrations(ctx, dsn, directory); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `TRUNCATE teaching.org_units CASCADE; UPDATE teaching.runtime_model_settings SET revision=gen_random_uuid(),settings='{}',secret_revision=NULL,updated_by=NULL`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `UPDATE teaching.runtime_model_settings SET revision=gen_random_uuid(),settings='{}',secret_revision=NULL,updated_by=NULL`)
	if _, err = identity.BootstrapAdmin(ctx, pool, identity.BootstrapRequest{SchoolCode: "MODEL-SCHOOL", SchoolName: "Synthetic", Username: "model-admin", DisplayName: "Admin", Password: "synthetic-model-password"}); err != nil {
		t.Fatal(err)
	}
	var user string
	if err = pool.QueryRow(ctx, `SELECT id::text FROM teaching.user_accounts WHERE username='model-admin'`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	actor := identity.Principal{UserID: user, Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}
	cfg := analysis.Config{ReportBudgetCurrency: "CNY", ReportBudgetTimezone: "Asia/Shanghai", ASRModelName: "not-installed", ASRModelRevision: "not-installed"}
	a := analysis.NewService(pool, nil, cfg)
	root := t.TempDir()
	store, err := New(ctx, pool, a, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get(ctx, identity.Principal{}); err == nil {
		t.Fatal("non-admin read allowed")
	}
	before, err := store.Get(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	asrKey, reportKey := "synthetic-private-asr-key", "synthetic-private-report-key"
	input := Update{ExpectedRevision: before.Revision, Values: validValues(), ASRAPIKey: &asrKey, ReportAPIKey: &reportKey, PricingConfirmed: true, Reason: "Synthetic setup"}
	saved, err := store.Apply(ctx, actor, input, "synthetic-request")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != saved.ActiveRevision || !saved.ASRKeyConfigured || !saved.ReportKeyConfigured || !a.ModelConfig().CloudASR.Enabled {
		t.Fatal("settings not applied")
	}
	public, _ := json.Marshal(saved)
	if strings.Contains(string(public), asrKey) || strings.Contains(string(public), reportKey) {
		t.Fatal("key leaked in GET")
	}
	var dbPublic, audit string
	if err = pool.QueryRow(ctx, `SELECT settings::text FROM teaching.runtime_model_settings`).Scan(&dbPublic); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT metadata::text FROM teaching.audit_logs WHERE action='model_settings.apply' ORDER BY id DESC LIMIT 1`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dbPublic+audit, asrKey) || strings.Contains(dbPublic+audit, reportKey) {
		t.Fatal("key leaked in database settings/audit")
	}
	if _, err = store.Apply(ctx, actor, input, "stale"); err == nil {
		t.Fatal("stale revision overwrote settings")
	}
	restored := analysis.NewService(pool, nil, cfg)
	if _, err = New(ctx, pool, restored, root); err != nil || restored.ModelConfig().CloudASR.APIKey != asrKey || restored.ModelRevision() != saved.Revision {
		t.Fatal("restart did not restore applied config", err)
	}
	input.ExpectedRevision = saved.Revision
	input.ASRAPIKey = nil
	input.ReportAPIKey = nil
	input.Values.ASRModel = "another-synthetic-model"
	kept, err := store.Apply(ctx, actor, input, "keep-key")
	if err != nil || !kept.ASRKeyConfigured || a.ModelConfig().CloudASR.APIKey != asrKey {
		t.Fatal("omitted key was not preserved", err)
	}
	input.ExpectedRevision = kept.Revision
	input.Values.ASRBase = "https://other.example.test/v1"
	if _, err = store.Apply(ctx, actor, input, "endpoint-change"); err == nil {
		t.Fatal("key reused on changed endpoint")
	}
	if _, _, err = restored.Create(ctx, actor, "00000000-0000-4000-8000-000000000001", "stale-process", analysis.CreateRun{MediaAssetID: "00000000-0000-4000-8000-000000000002", Mode: "full", ConfigProfile: "p0-v1"}); err == nil || !strings.Contains(err.Error(), "model settings changed") {
		t.Fatal("stale process was not fenced", err)
	}
	input.Values.ASRBase = validValues().ASRBase
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(index int) {
			candidate := input
			candidate.Values.MonthlyBudget = []string{"31", "32"}[index]
			_, err := store.Apply(ctx, actor, candidate, "concurrent-admin")
			results <- err
		}(i)
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("competing administrators both applied or both failed: %d", successes)
	}
	latest, err := store.Get(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	priorBudget := a.ModelBudgetLimit()
	input.ExpectedRevision = latest.Revision
	badActor := actor
	badActor.UserID = "00000000-0000-4000-8000-000000000099"
	if _, err = store.Apply(ctx, badActor, input, "audit-failure"); err == nil {
		t.Fatal("audit failure was ignored")
	}
	afterFailure, err := store.Get(ctx, actor)
	if err != nil || afterFailure.Revision != latest.Revision || a.ModelBudgetLimit() != priorBudget {
		t.Fatal("rolled back configuration became active", err)
	}
}
