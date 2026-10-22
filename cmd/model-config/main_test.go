package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/config"
)

func fixture() (map[string]string, pricing) {
	// Synthetic prices exercise unit conversion, not a provider price claim.
	demo := map[string]string{"BAILIAN_BASE_URL": "https://models.example/v1", "DASHSCOPE_API_KEY": "synthetic-private-key", "BAILIAN_ASR_MODEL": "synthetic-asr", "BAILIAN_TEXT_MODEL": "synthetic-report"}
	p := pricing{Confirmed: true, Source: "synthetic test only", Version: "synthetic-v1", MonthlyBudget: "30", APIBase: demo["BAILIAN_BASE_URL"], ASRModel: "synthetic-asr", ASRRevision: "synthetic-r1", ASRInput: "0.123456", ASROutput: "0.5", ReportModel: "synthetic-report", ReportRevision: "synthetic-r2", ReportInput: "1.25", ReportOutput: "2.5"}
	return demo, p
}

func TestPrepareUsesMainConfigurationAndExactPriceUnits(t *testing.T) {
	demo, p := fixture()
	t.Setenv("DASHSCOPE_API_KEY", "ambient-key-must-not-be-used")
	settings, err := prepareSettings(demo, p)
	if err != nil {
		t.Fatal(err)
	}
	if settings["ASR_INPUT_PRICE_MICROS"] != "123456" || settings["ASR_OUTPUT_PRICE_MICROS"] != "500000" || settings["MODEL_INPUT_PRICE_PER_MILLION"] != "1.25" || settings["MODEL_MONTHLY_BUDGET"] != "30" {
		t.Fatal("price units or approved budget changed")
	}
	if settings["ASR_API_KEY"] != demo["DASHSCOPE_API_KEY"] || settings["REPORT_API_KEY"] != demo["DASHSCOPE_API_KEY"] || demo["ASR_API_KEY"] != "" {
		t.Fatal("used ambient credential or mutated source")
	}
	result, err := checkSettings(settings)
	if err != nil || !result.ConfigurationValid || result.ProviderConnectionTested || result.BilledCalls != 0 {
		t.Fatal("offline check misreported actual provider validation", err)
	}
	public, _ := json.Marshal(result)
	if bytes.Contains(public, []byte(demo["DASHSCOPE_API_KEY"])) {
		t.Fatal("credential leaked to diagnostic output")
	}
}

func TestPrepareDurationBillingKeepsSecondsSeparateFromTokens(t *testing.T) {
	demo, p := fixture()
	demo["BAILIAN_ASR_LANGUAGE_HINTS"] = ""
	p.ASRBillingMode = "seconds"
	p.ASRSecond = "0.00022"
	p.ASRInput = ""
	p.ASROutput = ""
	settings, err := prepareSettings(demo, p)
	if err != nil {
		t.Fatal(err)
	}
	if settings["ASR_BILLING_MODE"] != "seconds" || settings["ASR_PRICE_PER_SECOND_MICROS"] != "220" || settings["ASR_INPUT_PRICE_MICROS"] != "" {
		t.Fatal("duration price converted into fake token price")
	}
	if settings["ASR_LANGUAGE_HINTS"] != "" {
		t.Fatal("automatic language detection was overridden")
	}
	if _, err = checkSettings(settings); err != nil {
		t.Fatal(err)
	}
	settings["MODEL_MONTHLY_BUDGET"] = "0.000001"
	if _, err = checkSettings(settings); err == nil {
		t.Fatal("insufficient duration reserve accepted")
	}
}

func TestPreparationRefusesUnverifiedOrMismatchedPrices(t *testing.T) {
	for _, scenario := range []string{"unconfirmed", "source", "version", "model", "endpoint", "revision", "zero", "fraction", "exponent", "negative", "tiny-budget", "empty-key"} {
		t.Run(scenario, func(t *testing.T) {
			demo, p := fixture()
			switch scenario {
			case "unconfirmed":
				p.Confirmed = false
			case "source":
				p.Source = ""
			case "version":
				p.Version = ""
			case "model":
				p.ASRModel = "another-model"
			case "endpoint":
				p.APIBase = "https://another-region.example/v1"
			case "revision":
				p.ReportRevision = ""
			case "zero":
				p.ASRInput = "0"
			case "fraction":
				p.ReportInput = "0.1234567"
			case "exponent":
				p.ASROutput = "1e-3"
			case "negative":
				p.MonthlyBudget = "-30"
			case "tiny-budget":
				p.MonthlyBudget = "0.000001"
			case "empty-key":
				demo["DASHSCOPE_API_KEY"] = ""
			}
			if _, err := prepareSettings(demo, p); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}

func TestCheckRejectsUnsafeSettingsWithoutEchoingSecrets(t *testing.T) {
	for _, scenario := range []string{"missing-key", "disabled", "zero-budget", "unknown-field", "prompt", "insecure-report", "url-secret", "query-secret", "fragment", "asr-price", "tokens"} {
		t.Run(scenario, func(t *testing.T) {
			demo, p := fixture()
			settings, err := prepareSettings(demo, p)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing-key":
				delete(settings, "REPORT_API_KEY")
			case "disabled":
				settings["ASR_CLOUD_ENABLED"] = "false"
			case "zero-budget":
				settings["MODEL_MONTHLY_BUDGET"] = "0"
			case "unknown-field":
				settings[demo["DASHSCOPE_API_KEY"]] = "ignored"
			case "prompt":
				settings["REPORT_PROMPT_SHA256"] = strings.Repeat("0", 64)
			case "insecure-report":
				settings["REPORT_API_BASE"] = "http://models.example/v1"
			case "url-secret":
				settings["REPORT_API_BASE"] = "https://user:synthetic-private-key@models.example/v1"
			case "query-secret":
				settings["ASR_API_BASE"] += "?key=synthetic-private-key"
			case "fragment":
				settings["REPORT_API_BASE"] += "#key"
			case "asr-price":
				settings["ASR_INPUT_PRICE_MICROS"] = "-1"
			case "tokens":
				settings["REPORT_MAX_INPUT_TOKENS"] = "9223372036854775807"
			}
			_, err = checkSettings(settings)
			if err == nil || strings.Contains(err.Error(), demo["DASHSCOPE_API_KEY"]) {
				t.Fatal("invalid configuration accepted or secret echoed")
			}
		})
	}
}

func TestCLIHasNoOverwriteOrImplicitCredentialDiscovery(t *testing.T) {
	dir := t.TempDir()
	worksheet := filepath.Join(dir, "pricing.json")
	var out bytes.Buffer
	if err := run([]string{"init-pricing", "-budget", "30", "-output", worksheet}, &out); err != nil {
		t.Fatal(err)
	}
	var draft pricing
	if err := readJSON(worksheet, &draft); err != nil || draft.Confirmed || draft.MonthlyBudget != "30" {
		t.Fatal("draft incorrectly enabled", err)
	}
	before, _ := os.ReadFile(worksheet)
	if err := run([]string{"init-pricing", "-budget", "100", "-output", worksheet}, &out); err == nil {
		t.Fatal("overwrote existing worksheet")
	}
	after, _ := os.ReadFile(worksheet)
	if !bytes.Equal(before, after) {
		t.Fatal("old worksheet changed")
	}
	target := filepath.Join(dir, "analysis.private.json")
	if err := run([]string{"prepare", "-demo-env", filepath.Join(dir, "missing.env"), "-pricing", worksheet, "-output", target}, &out); err == nil || !strings.Contains(err.Error(), "价格尚未确认") {
		t.Fatal("read secret before validating pricing", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("unconfirmed prices produced credential file")
	}
}

func TestCLIProducesOnlyValidatedPrivateFileAndSanitizedSummary(t *testing.T) {
	dir := t.TempDir()
	demo, p := fixture()
	worksheet, source, target := filepath.Join(dir, "pricing.json"), filepath.Join(dir, "demo.env"), filepath.Join(dir, "analysis.private.json")
	if err := writeNewJSON(worksheet, p); err != nil {
		t.Fatal(err)
	}
	var env bytes.Buffer
	for key, value := range demo {
		env.WriteString(key + "=" + value + "\n")
	}
	if err := os.WriteFile(source, env.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"prepare", "-demo-env", source, "-pricing", worksheet, "-output", target}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), demo["DASHSCOPE_API_KEY"]) {
		t.Fatal("secret in CLI output")
	}
	if err := run([]string{"check", "-config", target}, &out); err != nil {
		t.Fatal(err)
	}
	// Exercise the exact loader used by cmd/api, not only the setup checker.
	lookup, err := config.WithModelFile(func(key string) (string, bool) {
		if key == "DATABASE_URL" {
			return "postgres://localhost/config_only", true
		}
		return "", false
	}, target)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.FromLookup(lookup)
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := analysis.LoadCloudASR(func(key string) string { value, _ := lookup(key); return value })
	if err != nil || !cfg.ReportEnabled || !cloud.Enabled || cfg.ReportMonthlyBudgetMicros != 30000000 || cfg.ReportAPIKey != demo["DASHSCOPE_API_KEY"] || cloud.APIKey != cfg.ReportAPIKey || cfg.ASRModelName != p.ASRModel {
		t.Fatal("generated settings did not reach both native model consumers")
	}
	if err := run([]string{"prepare", "-demo-env", source, "-pricing", worksheet, "-output", target}, &out); err == nil {
		t.Fatal("overwrote configured file")
	}
	if err := run([]string{"prepare", "-demo-env", source, "-pricing", worksheet, "-output", filepath.Join(dir, "tracked.json")}, &out); err == nil {
		t.Fatal("unsafe output filename")
	}
	if err := run([]string{"check", "--synthetic-private-key"}, &out); err == nil || strings.Contains(err.Error(), "synthetic-private-key") {
		t.Fatal("argument value echoed")
	}
}

func TestReadJSONRejectsUnknownFieldsTrailingDataAndOversize(t *testing.T) {
	for _, raw := range []string{`{"unrecognized":"secret"}`, `{} {}`, `null`, strings.Repeat(" ", (1<<20)+1)} {
		file := filepath.Join(t.TempDir(), "input.json")
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		var p pricing
		err := readJSON(file, &p)
		if raw == "null" {
			err = p.validate()
		}
		if err == nil {
			t.Fatal("invalid pricing input accepted")
		}
	}
}
