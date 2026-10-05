package database

import (
	"context"
	"strings"
	"testing"
)

func TestInvalidDatabaseURLDoesNotExposeCredentials(t *testing.T) {
	const secret = "synthetic-secret"
	databaseURL := "postgres://user:" + secret + "@localhost/db%zz"

	if _, err := Open(context.Background(), databaseURL); err == nil {
		t.Fatal("Open accepted an invalid DATABASE_URL")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("Open error exposed credentials: %v", err)
	}

	if err := RunMigrations(context.Background(), databaseURL, t.TempDir()); err == nil {
		t.Fatal("RunMigrations accepted an invalid DATABASE_URL")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("RunMigrations error exposed credentials: %v", err)
	}
}
