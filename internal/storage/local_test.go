package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

func TestLocalCheck(t *testing.T) {
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("readiness file was not removed: %v", entries)
	}
}

func TestLocalStagesCommitsAndRejectsOversize(t *testing.T) {
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	staged, err := store.Stage(context.Background(), bytes.NewReader([]byte("media")), 5)
	if err != nil {
		t.Fatal(err)
	}
	if staged.Size != 5 || staged.SHA256 != "721c9525ade2ea8903d343ef25cf68b9bf4ab0aad56bb7b01fbe48d09bc7fcf4" {
		t.Fatalf("unexpected staged object: %#v", staged)
	}
	if err := store.Commit(staged, "sessions/id/source/test.mp4"); err != nil {
		t.Fatal(err)
	}
	file, err := store.Open("sessions/id/source/test.mp4")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := store.Stage(context.Background(), bytes.NewReader([]byte("too large")), 3); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize error=%v", err)
	}
	if _, err := store.Stage(context.Background(), bytes.NewReader(nil), 3); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty error=%v", err)
	}
	if _, err := store.Open("../outside"); err == nil {
		t.Fatal("path traversal was accepted")
	}
}
