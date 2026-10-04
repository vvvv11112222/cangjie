package governance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLedgerCheckRejectsMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deletions.jsonl")
	ledger, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Check(context.Background()); err == nil {
		t.Fatal("expected malformed ledger to fail readiness")
	}
}
