package analysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/database"
)

func TestCallReportModelPreservesUsageWhenCandidateIsInvalid(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "provider-request-1",
			"choices": []map[string]any{{"message": map[string]any{"content": `not-json`}}},
			"usage":   map[string]any{"prompt_tokens": 17, "completion_tokens": 5},
		})
	}))
	defer provider.Close()

	service := &Service{cfg: Config{ReportAPIBase: provider.URL, ReportAPIKey: "test"}}
	result, err := service.callReportModel(context.Background(), []byte(`{}`))
	if err == nil || !result.UsageKnown || result.InputTokens != 17 || result.OutputTokens != 5 || result.ProviderRequestID != "provider-request-1" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestSettleUnknownModelCallUsesBudgetLockAndAcceptsLateUsage(t *testing.T) {
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
	if err := database.RunMigrations(ctx, databaseURL, migrationDir); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var jobID, callID string
	period := billingPeriod(time.Now(), "Asia/Shanghai")
	if err = pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO teaching.model_calls(call_key,provider,model_name,prompt_version,status,currency,reserved_cost,billing_period,price_version,request_sha256)
		VALUES($1,'fixture','fixture','p0-v1','unknown','CNY',0.000100,$2::date,'fixture-price',$3) RETURNING id::text`, "report:"+jobID, period, strings.Repeat("a", 64)).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM teaching.model_calls WHERE id=$1`, callID) //nolint:errcheck

	blocker, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "CNY:"+period); err != nil {
		t.Fatal(err)
	}
	service := &Service{pool: pool}
	lockedCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	err = service.settleModelCall(lockedCtx, jobID, modelCallResult{ProviderRequestID: "late-request", InputTokens: 3, OutputTokens: 2, UsageKnown: true}, 7)
	cancel()
	if err == nil {
		t.Fatal("late settlement bypassed the period budget lock")
	}
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = service.settleModelCall(ctx, jobID, modelCallResult{ProviderRequestID: "late-request", InputTokens: 3, OutputTokens: 2, UsageKnown: true}, 7); err != nil {
		t.Fatal(err)
	}
	var status, cost, providerID string
	if err = pool.QueryRow(ctx, `SELECT status,to_char(actual_cost,'FM999999999990.000000'),provider_request_id FROM teaching.model_calls WHERE id=$1`, callID).Scan(&status, &cost, &providerID); err != nil || status != "succeeded" || cost != "0.000007" || providerID != "late-request" {
		t.Fatalf("late settlement status=%s cost=%s provider=%s err=%v", status, cost, providerID, err)
	}
	var protectedJobID, protectedCallID, orgID, userID string
	if err = pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&protectedJobID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO teaching.org_units(code,name,kind) VALUES($1,'Settlement Reconciler','school') RETURNING id::text`, "RECONCILE-"+protectedJobID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO teaching.user_accounts(org_unit_id,username,password_hash,display_name) VALUES($1,$2,'fixture','Settlement Reconciler') RETURNING id::text`, orgID, "reconciler-"+protectedJobID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO teaching.model_calls(call_key,provider,model_name,prompt_version,status,currency,reserved_cost,actual_cost,billing_period,price_version,request_sha256,reconciled_by,reconciliation_reason,provider_request_id,settled_at)
		VALUES($1,'fixture','fixture','p0-v1','failed','CNY',0.000100,0.000009,$2::date,'fixture-price',$3,$4,'manual reconciliation','manual-request',now()) RETURNING id::text`, "report:"+protectedJobID, period, strings.Repeat("b", 64), userID).Scan(&protectedCallID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM teaching.model_calls WHERE id=$1`, protectedCallID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM teaching.user_accounts WHERE id=$1`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM teaching.org_units WHERE id=$1`, orgID)
	})
	if err = service.settleModelCall(ctx, protectedJobID, modelCallResult{ProviderRequestID: "late-overwrite", InputTokens: 99, OutputTokens: 99, UsageKnown: true}, 99); err == nil {
		t.Fatal("late provider settlement overwrote an administrator reconciliation")
	}
	if err = pool.QueryRow(ctx, `SELECT status,to_char(actual_cost,'FM999999999990.000000'),provider_request_id FROM teaching.model_calls WHERE id=$1`, protectedCallID).Scan(&status, &cost, &providerID); err != nil || status != "failed" || cost != "0.000009" || providerID != "manual-request" {
		t.Fatalf("reconciled model call changed status=%s cost=%s provider=%s err=%v", status, cost, providerID, err)
	}
}

func TestMalformedChoicesPreserveBilling(t *testing.T) {
	for _, choices := range []string{`[]`, `{}`, `17`, `[{"message":{}}]`, `[{"message":{"content":17}}]`, `[{"message":{"content":"not-json"}}]`} {
		t.Run(choices, func(t *testing.T) {
			requests := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = w.Write([]byte(`{"id":"known-bill","usage":{"prompt_tokens":17,"completion_tokens":5},"choices":` + choices + `}`))
			}))
			defer provider.Close()
			s := &Service{cfg: Config{ReportAPIBase: provider.URL}}
			result, err := s.callReportModel(context.Background(), []byte(`{}`))
			if err == nil || !result.UsageKnown || result.InputTokens != 17 || result.OutputTokens != 5 || result.ProviderRequestID != "known-bill" || requests != 1 {
				t.Fatalf("billing was lost or request retried: %#v, %v, requests=%d", result, err, requests)
			}
		})
	}
}

func TestMalformedUsageRemainsUnknown(t *testing.T) {
	for _, usage := range []string{`null`, `{}`, `{"prompt_tokens":-1,"completion_tokens":5}`, `{"prompt_tokens":"17","completion_tokens":5}`} {
		t.Run(usage, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"id":"unknown-bill","choices":[],"usage":` + usage + `}`))
			}))
			defer provider.Close()
			s := &Service{cfg: Config{ReportAPIBase: provider.URL}}
			result, err := s.callReportModel(context.Background(), []byte(`{}`))
			if err == nil || result.UsageKnown || result.ProviderRequestID != "unknown-bill" {
				t.Fatalf("invalid usage claimed known: %#v, %v", result, err)
			}
		})
	}
}

func TestFitSingleEvidenceToSerializedLimit(t *testing.T) {
	for _, text := range []string{strings.Repeat("中", 512), strings.Repeat("\"\\\n<>&中", 10000)} {
		config := reportSnapshot{Model: Model{Name: "fixture"}, SystemPrompt: reportSystemPrompt, MaxInputTokens: 256, MaxOutputTokens: 64}
		input := modelInput{SchemaVersion: SchemaVersion, Limitations: []string{}, Evidence: []modelEvidence{{ID: "e-1", Kind: "transcript", StartMS: 0, EndMS: 1000, TextContent: text, TextSHA256: digestBytes([]byte(text))}}}
		fitted, err := fitModelInputToRequest(input, config, 0)
		if err != nil {
			t.Fatal(err)
		}
		body, err := buildReportRequest(fitted, config)
		if err != nil || !json.Valid(body) || len(body) > config.MaxInputTokens*8 {
			t.Fatalf("invalid bounded body: %d, %v", len(body), err)
		}
		e := fitted.Evidence[0]
		if e.TextContent == "" || !utf8.ValidString(e.TextContent) || len(e.TextContent) >= len(text) || e.TextSHA256 != digestBytes([]byte(e.TextContent)) || input.Evidence[0].TextContent != text {
			t.Fatalf("invalid excerpt or source mutation: %#v", e)
		}
		if !strings.Contains(strings.Join(fitted.Limitations, ","), "MODEL_INPUT_TRUNCATED:e-1") {
			t.Fatal("missing disclosure")
		}
		config.MaxInputTokens = 1
		if _, err = fitModelInputToRequest(input, config, 0); err == nil {
			t.Fatal("impossible packaging must fail")
		}
	}
}

func TestFitModelInputAccountsForCompleteRequestOverhead(t *testing.T) {
	config := reportSnapshot{Model: Model{Name: "fixture"}, SystemPrompt: reportSystemPrompt, MaxInputTokens: 256, MaxOutputTokens: 64}
	input := modelInput{
		SchemaVersion: SchemaVersion,
		RunID:         strings.Repeat("r", 36), SessionID: strings.Repeat("s", 36), MediaAssetID: strings.Repeat("m", 36),
		InputSHA256: strings.Repeat("a", 64), CourseContext: map[string]string{"course_name": "课程", "session_title": "课堂"},
		Limitations: []string{},
	}
	for i := 0; i < 20; i++ {
		input.Evidence = append(input.Evidence, modelEvidence{ID: strings.Repeat(string(rune('a'+i)), 36), Kind: "transcript", StartMS: int64(i * 1000), EndMS: int64(i*1000 + 500), TextContent: "短证据", TextSHA256: strings.Repeat("b", 64)})
	}

	fitted, err := fitModelInputToRequest(input, config, 0)
	if err != nil {
		t.Fatal(err)
	}
	body, err := buildReportRequest(fitted, config)
	if err != nil || len(body) > config.MaxInputTokens*8 {
		t.Fatalf("bounded request bytes=%d err=%v", len(body), err)
	}
	if len(fitted.Evidence) >= len(input.Evidence) || !strings.HasPrefix(fitted.Limitations[len(fitted.Limitations)-1], "MODEL_INPUT_SKIPPED_COUNT:") {
		t.Fatalf("selection was not disclosed: evidence=%d limitations=%v", len(fitted.Evidence), fitted.Limitations)
	}
}
