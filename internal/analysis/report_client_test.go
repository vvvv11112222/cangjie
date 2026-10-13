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
