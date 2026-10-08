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

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
