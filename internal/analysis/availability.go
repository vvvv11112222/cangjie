package analysis

import (
	"context"
	"time"

	"github.com/vvvv11112222/cangjie/internal/apperror"
)

// Availability describes configuration and budget only. The HTTP layer also
// checks recently authenticated Worker announcements before presenting readiness.
// No provider requests, credentials, or internal addresses leave this service.
func (s *Service) Availability(ctx context.Context) (visual, audio, report, budget, reportBudget bool, err error) {
	s.modelGate.RLock()
	defer s.modelGate.RUnlock()
	cfg := s.ModelConfig()
	if active := s.ModelRevision(); active != "" {
		var saved string
		if err = s.pool.QueryRow(ctx, `SELECT revision::text FROM teaching.runtime_model_settings WHERE singleton`).Scan(&saved); err != nil {
			return
		}
		if saved != active {
			err = apperror.New(503, "SERVICE_UNAVAILABLE", "model settings changed; apply the saved configuration or restart this API")
			return
		}
	}
	visual = cfg.VisionEnabled && cfg.VisionModelRevision != ""
	audio = cfg.CloudASR.Enabled && cfg.CloudASR.APIKey != ""
	report = cfg.ReportEnabled && cfg.ReportAPIKey != ""
	if !report || cfg.ReportMonthlyBudgetMicros <= 0 {
		return
	}
	var used int64
	var unreconciled bool
	err = s.pool.QueryRow(ctx, `SELECT
		COALESCE(round(sum(CASE WHEN status IN ('reserved','unknown') THEN reserved_cost ELSE actual_cost END)*1000000),0)::bigint,
		COALESCE(bool_or(actual_cost>reserved_cost AND reconciled_by IS NULL),false)
		FROM teaching.model_calls WHERE currency=$1 AND billing_period=$2::date`,
		cfg.ReportBudgetCurrency, billingPeriod(time.Now(), cfg.ReportBudgetTimezone)).Scan(&used, &unreconciled)
	if err != nil {
		return
	}
	asrReserve, asrErr := cfg.CloudASR.ReserveMicros(40000)
	reportReserve, reportErr := tokenCostMicros(int64(cfg.ReportMaxInputTokens), int64(cfg.ReportMaxOutputTokens), cfg.ReportInputPriceMicros, cfg.ReportOutputPriceMicros)
	reportBudget = reportErr == nil && reportReserve > 0 && !unreconciled && used <= cfg.ReportMonthlyBudgetMicros-reportReserve
	// The full workflow needs both reservations. Check the subtraction in stages
	// so a very large configured reservation cannot overflow their sum.
	budget = audio && reportBudget && asrErr == nil && asrReserve > 0 &&
		asrReserve <= cfg.ReportMonthlyBudgetMicros-reportReserve &&
		used <= cfg.ReportMonthlyBudgetMicros-reportReserve-asrReserve
	return
}
