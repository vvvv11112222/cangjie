package analysis

import (
	"context"
	"testing"
)

func TestAvailabilityRequiresBudgetForAudioAndReportTogether(t *testing.T) {
	f := setupReportFixture(t)
	cfg := billingTestConfig()
	cfg.ReportAPIKey = "synthetic-not-dispatched"
	cfg.CloudASR.Enabled = true
	cfg.CloudASR.APIKey = "synthetic-not-dispatched"
	cfg.CloudASR.MaxInputTokens = 8000
	cfg.CloudASR.MaxOutputTokens = 1000
	cfg.CloudASR.InputPriceMicros = 1_000_000
	cfg.CloudASR.OutputPriceMicros = 1_000_000
	// Each stage reserves 9,000 micros; either fits alone in 15,000.
	cfg.ReportMonthlyBudgetMicros = 15_000
	s := NewService(f.pool, f.store, cfg)
	_, audio, report, fullBudget, reportBudget, err := s.Availability(context.Background())
	if err != nil || !audio || !report || fullBudget || !reportBudget {
		t.Fatalf("partial budget presented as full readiness: %v/%v/%v/%v, %v", audio, report, fullBudget, reportBudget, err)
	}
	s.cfg.ReportMonthlyBudgetMicros = 18_000
	_, _, _, fullBudget, reportBudget, err = s.Availability(context.Background())
	if err != nil || !fullBudget || !reportBudget {
		t.Fatalf("combined reservation at the budget boundary rejected: %v/%v, %v", fullBudget, reportBudget, err)
	}
}
