package modelsettings

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/config"
)

// Token prices are CNY per million tokens; duration billing is explicit.
// Model and endpoint binding prevents silently applying another model's tier.
type Pricing struct {
	Confirmed      bool   `json:"confirmed"`
	Source         string `json:"price_source"`
	Version        string `json:"price_version"`
	MonthlyBudget  string `json:"monthly_budget_cny"`
	APIBase        string `json:"api_base"`
	ASRModel       string `json:"asr_model"`
	ASRRevision    string `json:"asr_revision"`
	ASRBillingMode string `json:"asr_billing_mode,omitempty"`
	ASRSecond      string `json:"asr_cny_per_second,omitempty"`
	ASRInput       string `json:"asr_input_cny_per_million_tokens"`
	ASROutput      string `json:"asr_output_cny_per_million_tokens"`
	ReportModel    string `json:"report_model"`
	ReportRevision string `json:"report_revision"`
	ReportInput    string `json:"report_input_cny_per_million_tokens"`
	ReportOutput   string `json:"report_output_cny_per_million_tokens"`
}

var decimal = regexp.MustCompile(`^[0-9]{1,9}(\.[0-9]{1,6})?$`)

func Money(raw string) (int64, error) {
	if !decimal.MatchString(raw) {
		return 0, errors.New("invalid decimal")
	}
	parts := strings.SplitN(raw, ".", 2)
	whole, _ := strconv.ParseInt(parts[0], 10, 64)
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	frac, _ := strconv.ParseInt(fraction+strings.Repeat("0", 6-len(fraction)), 10, 64)
	return whole*1000000 + frac, nil
}

func SafeEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())))
}

func (p Pricing) Validate() error {
	if !p.Confirmed || strings.TrimSpace(p.Source) == "" || strings.TrimSpace(p.Version) == "" {
		return errors.New("价格尚未确认：填写 price_source、price_version 并核对后设置 confirmed=true；不会生成启用配置")
	}
	if !SafeEndpoint(p.APIBase) {
		return errors.New("价格表 api_base 必须是无凭据/查询参数的 HTTPS 地址，本机模拟测试可用 HTTP")
	}
	for _, field := range []struct{ name, value string }{
		{"asr_model", p.ASRModel}, {"asr_revision", p.ASRRevision},
		{"report_model", p.ReportModel}, {"report_revision", p.ReportRevision},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("价格表缺少 %s；修订应使用供应商实际标识，不能编造固定版本", field.name)
		}
	}
	if p.ASRBillingMode != "" && p.ASRBillingMode != "tokens" && p.ASRBillingMode != "seconds" {
		return errors.New("asr_billing_mode 只能为 tokens 或 seconds")
	}
	prices := []struct{ name, value string }{
		{"monthly_budget_cny", p.MonthlyBudget}, {"report_input_cny_per_million_tokens", p.ReportInput},
		{"report_output_cny_per_million_tokens", p.ReportOutput},
	}
	if p.ASRBillingMode == "seconds" {
		prices = append(prices, struct{ name, value string }{"asr_cny_per_second", p.ASRSecond})
	} else {
		prices = append(prices, struct{ name, value string }{"asr_input_cny_per_million_tokens", p.ASRInput}, struct{ name, value string }{"asr_output_cny_per_million_tokens", p.ASROutput})
	}
	for _, field := range prices {
		v, err := Money(field.value)
		if err != nil || v <= 0 {
			return fmt.Errorf("%s 须为已核对的正数（人民币/每百万 token，预算字段除外），最多六位小数", field.name)
		}
	}
	return nil
}

func PrepareSettings(demo map[string]string, p Pricing) (map[string]string, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	base := strings.TrimRight(demo["BAILIAN_BASE_URL"], "/")
	if base != strings.TrimRight(p.APIBase, "/") || demo["BAILIAN_ASR_MODEL"] != p.ASRModel || demo["BAILIAN_TEXT_MODEL"] != p.ReportModel {
		return nil, errors.New("价格表的地址或模型与 demo 不一致；请核对地域、模型和计费档位，不自动替换")
	}
	if strings.TrimSpace(demo["DASHSCOPE_API_KEY"]) == "" {
		return nil, errors.New("指定 demo 配置没有 DASHSCOPE_API_KEY")
	}
	_, promptSHA, err := analysis.ReportPrompt(analysis.ComprehensivePromptVersion)
	if err != nil {
		return nil, errors.New("无法读取内置报告模板")
	}
	asrInput, _ := Money(p.ASRInput)
	asrOutput, _ := Money(p.ASROutput)
	hints := "zh,en"
	if v, ok := demo["BAILIAN_ASR_LANGUAGE_HINTS"]; ok {
		hints = v
	}
	settings := map[string]string{
		"ASR_CLOUD_ENABLED": "true", "ASR_API_BASE": base, "ASR_API_KEY": demo["DASHSCOPE_API_KEY"],
		"ASR_MODEL_NAME": p.ASRModel, "ASR_MODEL_REVISION": p.ASRRevision, "ASR_LANGUAGE_HINTS": hints,
		"ASR_PRICE_VERSION": p.Version, "ASR_INPUT_PRICE_MICROS": strconv.FormatInt(asrInput, 10),
		"ASR_OUTPUT_PRICE_MICROS": strconv.FormatInt(asrOutput, 10), "ASR_MAX_INPUT_TOKENS": "32768", "ASR_MAX_OUTPUT_TOKENS": "4096",
		"REPORT_API_BASE": base, "REPORT_API_KEY": demo["DASHSCOPE_API_KEY"], "REPORT_MODEL": p.ReportModel, "REPORT_MODEL_REVISION": p.ReportRevision,
		"REPORT_PROMPT_VERSION": analysis.ComprehensivePromptVersion, "REPORT_PROMPT_SHA256": promptSHA,
		"REPORT_SELECTION_VERSION": "time-window-v1", "REPORT_MAX_INPUT_TOKENS": "8000", "REPORT_MAX_OUTPUT_TOKENS": "6000",
		"MODEL_BUDGET_CURRENCY": "CNY", "MODEL_BUDGET_TIMEZONE": "Asia/Shanghai", "MODEL_MONTHLY_BUDGET": p.MonthlyBudget,
		"MODEL_PRICE_VERSION": p.Version, "MODEL_INPUT_PRICE_PER_MILLION": p.ReportInput, "MODEL_OUTPUT_PRICE_PER_MILLION": p.ReportOutput,
	}
	if p.ASRBillingMode == "seconds" {
		perSecond, _ := Money(p.ASRSecond)
		settings["ASR_BILLING_MODE"] = "seconds"
		settings["ASR_PRICE_PER_SECOND_MICROS"] = strconv.FormatInt(perSecond, 10)
		delete(settings, "ASR_INPUT_PRICE_MICROS")
		delete(settings, "ASR_OUTPUT_PRICE_MICROS")
	}
	if _, err := CheckSettings(settings); err != nil {
		return nil, err
	}
	return settings, nil
}

type Summary struct {
	ConfigurationValid       bool   `json:"configuration_valid"`
	ASRModel                 string `json:"asr_model"`
	ReportModel              string `json:"report_model"`
	MonthlyBudget            string `json:"monthly_budget_cny"`
	KeysConfigured           bool   `json:"keys_configured"`
	ProviderConnectionTested bool   `json:"provider_connection_tested"`
	BilledCalls              int    `json:"billed_calls"`
}

func CheckSettings(settings map[string]string) (Summary, error) {
	for key := range settings {
		if !config.IsModelSetting(key) {
			return Summary{}, errors.New("配置包含不支持的字段；只允许明确列出的 ASR/REPORT/MODEL 设置，不回显字段或内容")
		}
	}
	if settings["ASR_CLOUD_ENABLED"] != "true" || strings.TrimSpace(settings["ASR_API_KEY"]) == "" || strings.TrimSpace(settings["REPORT_API_KEY"]) == "" {
		return Summary{}, errors.New("尚未显式启用云 ASR 或缺少 ASR_API_KEY/REPORT_API_KEY")
	}
	if !SafeEndpoint(settings["ASR_API_BASE"]) || !SafeEndpoint(settings["REPORT_API_BASE"]) {
		return Summary{}, errors.New("模型地址不安全；使用无凭据/查询参数的 HTTPS 地址，本机模拟可用 HTTP")
	}
	lookup := func(key string) (string, bool) {
		if key == "DATABASE_URL" {
			return "postgres://localhost/model_config_check?sslmode=disable", true
		}
		v, ok := settings[key]
		return v, ok
	}
	cfg, err := config.FromLookup(lookup)
	if err != nil {
		// Config errors normally name fields, but do not forward untrusted values.
		return Summary{}, errors.New("主项目模型配置校验失败；检查必填项、正数单价、月预算、token 上限和超时，不回显原始内容")
	}
	cloud, err := analysis.LoadCloudASR(func(key string) string { return settings[key] })
	if err != nil || !cfg.ReportEnabled {
		return Summary{}, errors.New("云 ASR 配置无效或报告未启用；检查模型修订、整数微元价格、token 上限及正数月预算")
	}
	if cfg.ReportMaxInputTokens > 131072 || cfg.ReportMaxOutputTokens > 32768 || cfg.ReportInputPriceMicros > 1000000000 || cfg.ReportOutputPriceMicros > 1000000000 {
		return Summary{}, errors.New("报告 token 或价格超出配置工具的保守上限；请核对单位，不会自动裁剪或放宽")
	}
	if err := analysis.ValidateReportPrompt(true, cfg.ReportPromptVersion, cfg.ReportPromptSHA256); err != nil {
		return Summary{}, errors.New("报告模板版本/摘要不匹配；使用 go run ./cmd/report-prompt 核对")
	}
	asrReserve, reserveErr := cloud.ReserveMicros(40000)
	if reserveErr != nil {
		return Summary{}, errors.New("无法预留语音请求费用，请核对计费方式与单价")
	}
	reportReserve := (int64(cfg.ReportMaxInputTokens)*cfg.ReportInputPriceMicros + int64(cfg.ReportMaxOutputTokens)*cfg.ReportOutputPriceMicros + 999999) / 1000000
	if cfg.ReportMonthlyBudgetMicros < asrReserve || cfg.ReportMonthlyBudgetMicros < reportReserve {
		return Summary{}, errors.New("月预算不足以预留单次 ASR 或报告请求；请核对价格与额度，不会自动提高预算")
	}
	return Summary{ConfigurationValid: true, ASRModel: cfg.ASRModelName, ReportModel: cfg.ReportModel, MonthlyBudget: settings["MODEL_MONTHLY_BUDGET"], KeysConfigured: true}, nil
}
