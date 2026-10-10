package governance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestLedgerAppendAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deletions.jsonl")
	ledger, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Tombstone{TombstoneID: "tombstone", SessionID: "session", RequestedBy: "admin", Reason: "retention", IdempotencyKey: "delete-1", RequestSHA256: "sha", RequestedAt: time.Now().UTC().Truncate(time.Second)}
	if err = ledger.Append(want); err != nil {
		t.Fatal(err)
	}
	got, err := ledger.ReadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TombstoneID != want.TombstoneID || !got[0].RequestedAt.Equal(want.RequestedAt) {
		t.Fatalf("entries=%#v", got)
	}
}
