package analysis

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/vvvv11112222/cangjie/internal/apperror"
)

type modelRuntime struct {
	Config   Config
	Revision string
}

func (s *Service) ModelConfig() Config {
	if value := s.modelRuntime.Load(); value != nil {
		return value.Config
	}
	return s.cfg
}
func (s *Service) ModelRevision() string {
	if value := s.modelRuntime.Load(); value != nil {
		return value.Revision
	}
	return ""
}

// InitializeModels runs before HTTP/background workers start.
func (s *Service) InitializeModels(cfg Config, revision string) {
	s.modelRuntime.Store(&modelRuntime{cfg, revision})
}
func (s *Service) ModelBudgetLimit() int64 { return s.ModelConfig().ReportMonthlyBudgetMicros }

func ModelSettingsIdle(ctx context.Context, tx interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (bool, error) {
	var busy bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.analysis_runs WHERE status IN('queued','running'))
	 OR EXISTS(SELECT 1 FROM teaching.analysis_jobs WHERE status IN('queued','running'))
	 OR EXISTS(SELECT 1 FROM teaching.model_calls WHERE status IN('reserved','unknown') OR (actual_cost>reserved_cost AND reconciled_by IS NULL))`).Scan(&busy)
	return !busy, err
}

// The shared database lock also fences Create on other API processes. A stale
// process refuses new runs until restarted rather than using its old keys.
func (s *Service) checkModelRevision(ctx context.Context, tx pgx.Tx) error {
	if s.ModelRevision() == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('runtime_model_settings',0))`); err != nil {
		return err
	}
	var revision string
	if err := tx.QueryRow(ctx, `SELECT revision::text FROM teaching.runtime_model_settings WHERE singleton`).Scan(&revision); err != nil {
		return err
	}
	if revision != s.ModelRevision() {
		return apperror.New(503, "SERVICE_UNAVAILABLE", "model settings changed on another process; restart this API")
	}
	return nil
}

func (s *Service) ApplyModelsWhileIdle(ctx context.Context, prepare func(pgx.Tx) (Config, string, error)) error {
	s.modelGate.Lock()
	defer s.modelGate.Unlock()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('runtime_model_settings',0))`); err != nil {
		return err
	}
	idle, err := ModelSettingsIdle(ctx, tx)
	if err != nil {
		return err
	}
	if !idle {
		return apperror.New(409, "STATE_CONFLICT", "存在排队、运行中的分析或待核对账单，请处理完后再应用模型配置")
	}
	cfg, revision, err := prepare(tx)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.modelRuntime.Store(&modelRuntime{cfg, revision})
	return nil
}
