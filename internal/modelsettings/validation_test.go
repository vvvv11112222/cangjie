package modelsettings

import (
	"encoding/json"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"testing"
)

func TestModelValuesRequireExplicitEnabledAndRejectUnknownFields(t *testing.T) {
	body, _ := json.Marshal(validValues())
	var fields map[string]any
	_ = json.Unmarshal(body, &fields)
	delete(fields, "enabled")
	body, _ = json.Marshal(fields)
	var value Values
	if json.Unmarshal(body, &value) == nil {
		t.Fatal("omitted enabled flag accepted")
	}
	fields["enabled"] = nil
	body, _ = json.Marshal(fields)
	if json.Unmarshal(body, &value) == nil {
		t.Fatal("null enabled flag accepted")
	}
	fields["enabled"] = false
	fields["asr_api_key"] = "synthetic-forbidden"
	body, _ = json.Marshal(fields)
	if json.Unmarshal(body, &value) == nil {
		t.Fatal("secret in public values accepted")
	}
}

func validValues() Values {
	return Values{Enabled: true, ASRBase: "https://models.example.test/v1", ASRModel: "synthetic-asr", ASRRevision: "v1", ASRLanguage: "zh,en", ASRInput: "1", ASROutput: "1", ASRMaxInput: 32768, ASRMaxOutput: 4096,
		ReportBase: "https://models.example.test/v1", ReportModel: "synthetic-report", ReportRevision: "v1", ReportInput: "1", ReportOutput: "1", ReportMaxInput: 8000, ReportMaxOutput: 6000, MonthlyBudget: "30", PriceVersion: "synthetic-v1", PriceSource: "Synthetic test prices"}
}
func TestBuildModelConfigRequiresConfirmedPricesAndCombinedBudget(t *testing.T) {
	base := analysis.Config{ReportBudgetCurrency: "CNY", ReportBudgetTimezone: "Asia/Shanghai"}
	keys := secrets{"synthetic-asr-key", "synthetic-report-key"}
	v := validValues()
	if _, err := BuildConfig(base, v, keys, false); err == nil {
		t.Fatal("unconfirmed prices accepted")
	}
	c, err := BuildConfig(base, v, keys, true)
	if err != nil || !c.CloudASR.Enabled || !c.ReportEnabled || c.ReportMonthlyBudgetMicros != 30_000_000 {
		t.Fatalf("valid settings: %+v %v", c, err)
	}
	v.MonthlyBudget = "0.04"
	if _, err = BuildConfig(base, v, keys, true); err == nil {
		t.Fatal("budget covering only ASR was accepted")
	}
	v = validValues()
	v.ReportBase = "https://user:synthetic-key@models.example.test/v1"
	if _, err = BuildConfig(base, v, keys, true); err == nil {
		t.Fatal("credential-bearing endpoint accepted")
	}
	v = validValues()
	v.Enabled = false
	c, err = BuildConfig(c, v, secrets{}, false)
	if err != nil || c.CloudASR.Enabled || c.ReportEnabled || c.ReportAPIKey != "" {
		t.Fatal("disable did not stop cloud stages", err)
	}
}

func TestDurationBillingSurvivesWebSettingsRoundTrip(t *testing.T) {
	v := validValues()
	v.ASRBillingMode = "seconds"
	v.ASRSecond = "0.00022"
	v.ASRInput = "0"
	v.ASROutput = "0"
	v.MonthlyBudget = "2"
	base := analysis.Config{ReportBudgetCurrency: "CNY", ReportBudgetTimezone: "Asia/Shanghai"}
	c, err := BuildConfig(base, v, secrets{"synthetic-dotted.key", "synthetic-report-key"}, true)
	if err != nil || c.CloudASR.BillingMode != "seconds" || c.CloudASR.PriceSecondMicros != 220 {
		t.Fatalf("duration config: %v", err)
	}
	if reserve, err := c.CloudASR.ReserveMicros(40000); err != nil || reserve != 8800 {
		t.Fatalf("duration reservation: %d %v", reserve, err)
	}
	encoded, _ := json.Marshal(FromConfig(c))
	var decoded Values
	if err = json.Unmarshal(encoded, &decoded); err != nil || decoded.ASRBillingMode != "seconds" || decoded.ASRSecond != "0.000220" {
		t.Fatalf("duration round trip: %v", err)
	}
	if c.ReportPromptVersion != analysis.ComprehensivePromptVersion {
		t.Fatal("web configuration lost comprehensive prompt")
	}
	v.ASRSecond = "0"
	if _, err = BuildConfig(base, v, secrets{"synthetic-asr-key", "synthetic-report-key"}, true); err == nil {
		t.Fatal("zero duration price accepted")
	}
}
