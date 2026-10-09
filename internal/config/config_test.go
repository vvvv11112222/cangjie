package config

import (
	"strings"
	"testing"
)

func TestFromLookupValidMinimum(t *testing.T) {
	cfg, err := FromLookup(mapLookup(map[string]string{"DATABASE_URL": "postgres://localhost/teaching"}))
	if err != nil {
		t.Fatalf("FromLookup() error = %v", err)
	}
	if cfg.ReportEnabled {
		t.Fatal("reporting must be disabled for the default zero budget")
	}
	if cfg.MaxUploadBytes != 4294967296 || cfg.UploadTimeout.String() != "30m0s" || cfg.MediaRetention.Hours() != 14*24 {
		t.Fatalf("unexpected media defaults: bytes=%d timeout=%s retention=%s", cfg.MaxUploadBytes, cfg.UploadTimeout, cfg.MediaRetention)
	}
}

func TestFromLookupRequiresDatabaseURL(t *testing.T) {
	_, err := FromLookup(mapLookup(nil))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL error, got %v", err)
	}
}

func TestFromLookupDoesNotExposeInvalidDatabaseURL(t *testing.T) {
	const secret = "synthetic-secret"
	_, err := FromLookup(mapLookup(map[string]string{
		"DATABASE_URL": "postgres://user:" + secret + "@localhost/db%zz",
	}))
	if err == nil {
		t.Fatal("expected invalid DATABASE_URL error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("DATABASE_URL error exposed credentials: %v", err)
	}
}

func TestFromLookupRejectsHeartbeatAtLease(t *testing.T) {
	_, err := FromLookup(mapLookup(map[string]string{
		"DATABASE_URL":          "postgres://localhost/teaching",
		"JOB_LEASE_SECONDS":     "30",
		"JOB_HEARTBEAT_SECONDS": "30",
	}))
	if err == nil || !strings.Contains(err.Error(), "JOB_HEARTBEAT_SECONDS") {
		t.Fatalf("expected heartbeat error, got %v", err)
	}
}

func TestFromLookupRequiresPinnedReportConfiguration(t *testing.T) {
	_, err := FromLookup(mapLookup(map[string]string{
		"DATABASE_URL":         "postgres://localhost/teaching",
		"MODEL_MONTHLY_BUDGET": "10",
	}))
	if err == nil || !strings.Contains(err.Error(), "REPORT_API_BASE") {
		t.Fatalf("expected report configuration error, got %v", err)
	}
}

func TestFromLookupAcceptsPinnedReportConfiguration(t *testing.T) {
	cfg, err := FromLookup(mapLookup(map[string]string{"DATABASE_URL": "postgres://localhost/teaching", "MODEL_MONTHLY_BUDGET": "10.123456", "REPORT_API_BASE": "https://model.example/v1", "REPORT_API_KEY": "fixture", "REPORT_MODEL": "report-model", "REPORT_MODEL_REVISION": "revision-1", "REPORT_PROMPT_VERSION": "p0-v1", "REPORT_PROMPT_SHA256": strings.Repeat("a", 64), "REPORT_SELECTION_VERSION": "time-window-v1", "MODEL_PRICE_VERSION": "price-1", "MODEL_INPUT_PRICE_PER_MILLION": "1.000001", "MODEL_OUTPUT_PRICE_PER_MILLION": "2.5"}))
	if err != nil {
		t.Fatalf("valid report configuration rejected: %v", err)
	}
	if !cfg.ReportEnabled || cfg.ReportTimeout.String() != "3m0s" || cfg.ReportMonthlyBudgetMicros != 10_123_456 || cfg.ReportInputPriceMicros != 1_000_001 || cfg.ReportOutputPriceMicros != 2_500_000 {
		t.Fatalf("unexpected report configuration: enabled=%v timeout=%s budget=%d prices=%d/%d", cfg.ReportEnabled, cfg.ReportTimeout, cfg.ReportMonthlyBudgetMicros, cfg.ReportInputPriceMicros, cfg.ReportOutputPriceMicros)
	}
}

func TestFromLookupRejectsInexactModelMoney(t *testing.T) {
	_, err := FromLookup(mapLookup(map[string]string{"DATABASE_URL": "postgres://localhost/teaching", "MODEL_MONTHLY_BUDGET": "0.0000001"}))
	if err == nil || !strings.Contains(err.Error(), "MODEL_MONTHLY_BUDGET") {
		t.Fatalf("expected fixed-precision money error, got %v", err)
	}
}

func TestFromLookupRejectsOriginWithPath(t *testing.T) {
	_, err := FromLookup(mapLookup(map[string]string{
		"DATABASE_URL":  "postgres://localhost/teaching",
		"PUBLIC_ORIGIN": "https://example.test/application",
	}))
	if err == nil || !strings.Contains(err.Error(), "PUBLIC_ORIGIN") {
		t.Fatalf("expected PUBLIC_ORIGIN error, got %v", err)
	}
}

func TestFromLookupRejectsInvalidSessionTTL(t *testing.T) {
	_, err := FromLookup(mapLookup(map[string]string{
		"DATABASE_URL":        "postgres://localhost/teaching",
		"SESSION_TTL_SECONDS": "0",
	}))
	if err == nil || !strings.Contains(err.Error(), "SESSION_TTL_SECONDS") {
		t.Fatalf("expected session TTL error, got %v", err)
	}
}

func TestFromLookupRejectsInvalidMediaLimits(t *testing.T) {
	for _, key := range []string{"MAX_UPLOAD_BYTES", "UPLOAD_TIMEOUT_SECONDS", "MEDIA_RETENTION_DAYS"} {
		t.Run(key, func(t *testing.T) {
			_, err := FromLookup(mapLookup(map[string]string{"DATABASE_URL": "postgres://localhost/teaching", key: "0"}))
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected %s error, got %v", key, err)
			}
		})
	}
}

func TestValidateWorkerRuntimeRequiresPinnedCredentialAndExecution(t *testing.T) {
	cfg, err := FromLookup(mapLookup(map[string]string{"DATABASE_URL": "postgres://localhost/teaching"}))
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.ValidateWorkerRuntime(); err == nil || !strings.Contains(err.Error(), "WORKER_TOKEN") {
		t.Fatalf("expected worker token error, got %v", err)
	}
	cfg.WorkerToken = strings.Repeat("x", 32)
	cfg.ProcessorVersion = "fixed-json-v1"
	cfg.FFmpegSHA256 = strings.Repeat("a", 64)
	cfg.ASRModelName = "fixed-json-asr"
	cfg.ASRModelRevision = "p0-v1"
	if err = cfg.ValidateWorkerRuntime(); err != nil {
		t.Fatalf("valid worker runtime rejected: %v", err)
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
