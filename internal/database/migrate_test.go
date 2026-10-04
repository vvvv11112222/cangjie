package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithoutTransactionControl(t *testing.T) {
	input := "BEGIN;\nCREATE TABLE example(id int);\n  COMMIT;\n"
	got := withoutTransactionControl(input)
	if strings.Contains(got, "BEGIN;") || strings.Contains(got, "COMMIT;") {
		t.Fatalf("transaction control was not removed: %q", got)
	}
	if !strings.Contains(got, "CREATE TABLE") {
		t.Fatalf("migration body was removed: %q", got)
	}
}

func TestLoadMigrationsSortsAndIgnoresChecks(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"002_second.sql":   "BEGIN;\nSELECT 2;\nCOMMIT;",
		"001_first.sql":    "BEGIN;\nSELECT 1;\nCOMMIT;",
		"check_schema.sql": "SELECT false;",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	items, err := loadMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Name != "001_first.sql" || items[1].Name != "002_second.sql" {
		t.Fatalf("unexpected migration order: %#v", items)
	}
}
