package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelFileOverridesOnlyApprovedSettingsAndDoesNotMutateEnvironment(t *testing.T) {
	file := filepath.Join(t.TempDir(), "analysis.private.json")
	if err := os.WriteFile(file, []byte(`{"ASR_API_KEY":"synthetic-private-key","REPORT_MODEL":"synthetic-report","MODEL_MONTHLY_BUDGET":"30"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASR_API_KEY", "ambient-value")
	base := mapLookup(map[string]string{"DATABASE_URL": "postgres://localhost/synthetic", "ASR_API_KEY": "base-value"})
	lookup, err := WithModelFile(base, file)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup("ASR_API_KEY"); v != "synthetic-private-key" {
		t.Fatal("private file was not used")
	}
	if v, _ := lookup("MODEL_MONTHLY_BUDGET"); v != "30" {
		t.Fatal("budget changed")
	}
	if v, _ := lookup("DATABASE_URL"); v != "postgres://localhost/synthetic" {
		t.Fatal("non-model setting lost")
	}
	if os.Getenv("ASR_API_KEY") != "ambient-value" {
		t.Fatal("process environment mutated")
	}
	if err := os.WriteFile(file, []byte(`{"REPORT_MODEL":"changed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup("REPORT_MODEL"); v != "synthetic-report" {
		t.Fatal("configuration snapshot changed after file edit")
	}
}

func TestModelFileRejectsUnsafeOrMalformedInputWithoutLeakingContents(t *testing.T) {
	for _, raw := range []string{`{"DATABASE_URL":"synthetic-private-key"}`, `{"WORKER_TOKEN":"synthetic-private-key"}`, `{"MODEL_CONFIG_FILE":"recursive"}`, `{"ASR_API_KEY":23}`, `{"ASR_API_KEY":"synthetic-private-key"`, `{} {}`, `null`, `{}`, strings.Repeat(" ", (1<<20)+1)} {
		file := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := WithModelFile(os.LookupEnv, file)
		if err == nil || strings.Contains(err.Error(), "synthetic-private-key") {
			t.Fatal("unsafe file accepted or secret logged")
		}
	}
	if _, err := WithModelFile(os.LookupEnv, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing explicit file silently ignored")
	}
}

func TestNoModelFileKeepsExistingLookup(t *testing.T) {
	lookup, err := WithModelFile(mapLookup(map[string]string{"REPORT_MODEL": "existing"}), "")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup("REPORT_MODEL"); v != "existing" {
		t.Fatal("legacy configuration changed")
	}
}
