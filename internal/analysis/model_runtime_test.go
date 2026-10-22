package analysis

import (
	"context"
	"github.com/jackc/pgx/v5"
	"testing"
)

func TestModelUpdateRejectsQueuedRunAndKeepsPriorConfig(t *testing.T) {
	f := setupReportFixture(t)
	s := NewService(f.pool, f.store, billingTestConfig())
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET status='queued',finished_at=NULL WHERE id=$1`, f.run); err != nil {
		t.Fatal(err)
	}
	called := false
	err := s.ApplyModelsWhileIdle(ctx, func(pgx.Tx) (Config, string, error) { called = true; return Config{}, "changed", nil })
	if err == nil || called || s.ModelRevision() != "" {
		t.Fatalf("busy config update accepted: %v, callback=%v", err, called)
	}
}
