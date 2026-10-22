package analysis

import (
	"context"
	"time"
)

// Availability describes configuration and budget only. The HTTP layer also
// checks recently authenticated Worker announcements before presenting readiness.
// No provider requests, credentials, or internal addresses leave this service.
func (s *Service) Availability(ctx context.Context) (visual, audio, report, budget, reportBudget bool, err error) {
	visual = s.cfg.VisionEnabled && s.cfg.VisionModelRevision != ""
	audio = s.cfg.CloudASR.Enabled && s.cfg.CloudASR.APIKey != ""
	report = s.cfg.ReportEnabled && s.cfg.ReportAPIKey != ""
	if !report || s.cfg.ReportMonthlyBudgetMicros <= 0 {
		return
	}
	var used int64
	var unreconciled bool
	err = s.pool.QueryRow(ctx, `SELECT
		COALESCE(round(sum(CASE WHEN status IN ('reserved','unknown') THEN reserved_cost ELSE actual_cost END)*1000000),0)::bigint,
		COALESCE(bool_or(actual_cost>reserved_cost AND reconciled_by IS NULL),false)
		FROM teaching.model_calls WHERE currency=$1 AND billing_period=$2::date`,
		s.cfg.ReportBudgetCurrency, billingPeriod(time.Now(), s.cfg.ReportBudgetTimezone)).Scan(&used, &unreconciled)
	if err != nil {
		return
	}
	asrReserve, asrErr := s.cfg.CloudASR.ReserveMicros(40000)
	reportReserve, reportErr := tokenCostMicros(int64(s.cfg.ReportMaxInputTokens), int64(s.cfg.ReportMaxOutputTokens), s.cfg.ReportInputPriceMicros, s.cfg.ReportOutputPriceMicros)
	reportBudget = reportErr == nil && reportReserve > 0 && !unreconciled && used <= s.cfg.ReportMonthlyBudgetMicros-reportReserve
	// The full workflow needs both reservations. Check the subtraction in stages
	// so a very large configured reservation cannot overflow their sum.
	budget = audio && reportBudget && asrErr == nil && asrReserve > 0 &&
		asrReserve <= s.cfg.ReportMonthlyBudgetMicros-reportReserve &&
		used <= s.cfg.ReportMonthlyBudgetMicros-reportReserve-asrReserve
	return
}
