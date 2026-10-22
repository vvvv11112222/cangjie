package modelsettings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/config"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

type Values struct {
	Enabled         bool   `json:"enabled"`
	ASRBase         string `json:"asr_api_base"`
	ASRModel        string `json:"asr_model"`
	ASRRevision     string `json:"asr_revision"`
	ASRLanguage     string `json:"asr_language_hints"`
	ASRInput        string `json:"asr_input_cny_per_million_tokens"`
	ASROutput       string `json:"asr_output_cny_per_million_tokens"`
	ASRMaxInput     int    `json:"asr_max_input_tokens"`
	ASRMaxOutput    int    `json:"asr_max_output_tokens"`
	ReportBase      string `json:"report_api_base"`
	ReportModel     string `json:"report_model"`
	ReportRevision  string `json:"report_revision"`
	ReportInput     string `json:"report_input_cny_per_million_tokens"`
	ReportOutput    string `json:"report_output_cny_per_million_tokens"`
	ReportMaxInput  int    `json:"report_max_input_tokens"`
	ReportMaxOutput int    `json:"report_max_output_tokens"`
	MonthlyBudget   string `json:"monthly_budget_cny"`
	PriceVersion    string `json:"price_version"`
	PriceSource     string `json:"price_source"`
}

// All public values are explicit. In particular an omitted/null enabled flag
// must never be interpreted as an administrator asking to disable the models.
func (v *Values) UnmarshalJSON(raw []byte) error {
	type plain Values
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return fmt.Errorf("模型配置必须是对象")
	}
	shape := reflect.TypeOf(plain{})
	for i := 0; i < shape.NumField(); i++ {
		name := shape.Field(i).Tag.Get("json")
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("模型配置缺少必填字段")
		}
	}
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("模型配置字段或类型不正确")
	}
	*v = Values(value)
	return nil
}

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type Update struct {
	ExpectedRevision string  `json:"expected_revision"`
	Values           Values  `json:"values"`
	ASRAPIKey        *string `json:"asr_api_key,omitempty"`
	ReportAPIKey     *string `json:"report_api_key,omitempty"`
	PricingConfirmed bool    `json:"pricing_confirmed"`
	Reason           string  `json:"reason"`
}
type View struct {
	Revision                 string `json:"revision"`
	ActiveRevision           string `json:"active_revision"`
	Values                   Values `json:"values"`
	ASRKeyConfigured         bool   `json:"asr_key_configured"`
	ReportKeyConfigured      bool   `json:"report_key_configured"`
	CanApply                 bool   `json:"can_apply"`
	ProviderConnectionTested bool   `json:"provider_connection_tested"`
}
type secrets struct {
	ASR    string `json:"asr_api_key"`
	Report string `json:"report_api_key"`
}
type Service struct {
	pool        *pgxpool.Pool
	analysis    *analysis.Service
	root        string
	initial     Values
	initialKeys secrets
}

func New(ctx context.Context, pool *pgxpool.Pool, a *analysis.Service, root string) (*Service, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return nil, fmt.Errorf("cannot create private model settings directory")
	}
	c := a.ModelConfig()
	s := &Service{pool: pool, analysis: a, root: absolute, initial: FromConfig(c), initialKeys: secrets{c.CloudASR.APIKey, c.ReportAPIKey}}
	revision, v, keys, err := s.load(ctx, pool)
	if err != nil {
		return nil, err
	}
	var stored bool
	if err = pool.QueryRow(ctx, `SELECT secret_revision IS NOT NULL FROM teaching.runtime_model_settings WHERE singleton`).Scan(&stored); err != nil {
		return nil, err
	}
	if stored {
		c, err = BuildConfig(c, v, keys, true)
		if err != nil {
			return nil, err
		}
	}
	a.InitializeModels(c, revision)
	return s, nil
}

func amount(value int64) string { return fmt.Sprintf("%d.%06d", value/1000000, value%1000000) }
func fallback(value, other int) int {
	if value > 0 {
		return value
	}
	return other
}
func FromConfig(c analysis.Config) Values {
	if c.ASRModelName == "not-installed" {
		c.ASRModelName = ""
	}
	if c.ASRModelRevision == "not-installed" {
		c.ASRModelRevision = ""
	}
	return Values{Enabled: c.CloudASR.Enabled && c.ReportEnabled, ASRBase: c.CloudASR.APIBase, ASRModel: c.ASRModelName, ASRRevision: c.ASRModelRevision, ASRLanguage: c.CloudASR.LanguageHints,
		ASRInput: amount(c.CloudASR.InputPriceMicros), ASROutput: amount(c.CloudASR.OutputPriceMicros), ASRMaxInput: fallback(int(c.CloudASR.MaxInputTokens), 32768), ASRMaxOutput: fallback(int(c.CloudASR.MaxOutputTokens), 4096),
		ReportBase: c.ReportAPIBase, ReportModel: c.ReportModel, ReportRevision: c.ReportModelRevision, ReportInput: amount(c.ReportInputPriceMicros), ReportOutput: amount(c.ReportOutputPriceMicros), ReportMaxInput: fallback(c.ReportMaxInputTokens, 8000), ReportMaxOutput: fallback(c.ReportMaxOutputTokens, 6000), MonthlyBudget: amount(c.ReportMonthlyBudgetMicros), PriceVersion: c.ReportPriceVersion}
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Service) load(ctx context.Context, q queryer) (string, Values, secrets, error) {
	var revision string
	var raw []byte
	var secretRevision *string
	err := q.QueryRow(ctx, `SELECT revision::text,settings,secret_revision::text FROM teaching.runtime_model_settings WHERE singleton`).Scan(&revision, &raw, &secretRevision)
	if err != nil {
		return "", Values{}, secrets{}, err
	}
	if secretRevision == nil {
		return revision, s.initial, s.initialKeys, nil
	}
	var v Values
	var keys secrets
	if json.Unmarshal(raw, &v) != nil {
		return "", v, keys, fmt.Errorf("stored public model settings are invalid")
	}
	// Both the UUID and path originate in our singleton row, never from a client path.
	if *secretRevision != revision || len(revision) != 36 || strings.ContainsAny(revision, "/\\.") {
		return "", v, keys, fmt.Errorf("invalid model secret reference")
	}
	path := filepath.Join(s.root, revision+".private.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16384 {
		return "", v, keys, fmt.Errorf("private model settings are missing or invalid; restore the matching private directory")
	}
	body, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(body, &keys) != nil {
		return "", v, keys, fmt.Errorf("cannot read private model settings; contents are not logged")
	}
	return revision, v, keys, nil
}
func (s *Service) Get(ctx context.Context, p identity.Principal) (View, error) {
	if !p.Has("sys_admin") {
		return View{}, apperror.New(403, "FORBIDDEN", "system administrator required")
	}
	revision, v, keys, err := s.load(ctx, s.pool)
	if err != nil {
		return View{}, err
	}
	idle, err := analysis.ModelSettingsIdle(ctx, s.pool)
	if err != nil {
		return View{}, err
	}
	return View{Revision: revision, ActiveRevision: s.analysis.ModelRevision(), Values: v, ASRKeyConfigured: keys.ASR != "", ReportKeyConfigured: keys.Report != "", CanApply: idle}, nil
}
func (s *Service) Apply(ctx context.Context, p identity.Principal, in Update, requestID string) (View, error) {
	if !p.Has("sys_admin") {
		return View{}, apperror.New(403, "FORBIDDEN", "system administrator required")
	}
	if !revisionPattern.MatchString(in.ExpectedRevision) {
		return View{}, apperror.New(400, "INVALID_ARGUMENT", "配置版本格式不正确，请重新读取配置")
	}
	if strings.TrimSpace(in.Reason) == "" || len([]rune(in.Reason)) > 1000 {
		return View{}, apperror.New(400, "INVALID_ARGUMENT", "请填写不超过1000字的配置变更说明")
	}
	err := s.analysis.ApplyModelsWhileIdle(ctx, func(tx pgx.Tx) (analysis.Config, string, error) {
		current, v, keys, err := s.load(ctx, tx)
		if err != nil {
			return analysis.Config{}, "", err
		}
		if current != in.ExpectedRevision {
			return analysis.Config{}, "", apperror.New(409, "REVISION_CONFLICT", "配置已被其他管理员修改，请重新读取后再保存")
		}
		if (v.ASRBase != in.Values.ASRBase && keys.ASR != "" && in.ASRAPIKey == nil) || (v.ReportBase != in.Values.ReportBase && keys.Report != "" && in.ReportAPIKey == nil) {
			return analysis.Config{}, "", apperror.New(400, "INVALID_ARGUMENT", "接口地址变更时请重新填写或明确清除对应密钥")
		}
		if in.ASRAPIKey != nil {
			keys.ASR = *in.ASRAPIKey
		}
		if in.ReportAPIKey != nil {
			keys.Report = *in.ReportAPIKey
		}
		next, err := BuildConfig(s.analysis.ModelConfig(), in.Values, keys, in.PricingConfirmed)
		if err != nil {
			return analysis.Config{}, "", apperror.New(400, "INVALID_ARGUMENT", err.Error())
		}
		var revision string
		if err = tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&revision); err != nil {
			return next, "", err
		}
		body, _ := json.Marshal(keys)
		file, err := os.OpenFile(filepath.Join(s.root, revision+".private.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return next, "", fmt.Errorf("cannot persist private model settings")
		}
		_, writeErr := file.Write(body)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return next, "", fmt.Errorf("cannot persist private model settings")
		}
		public, _ := json.Marshal(in.Values)
		_, err = tx.Exec(ctx, `UPDATE teaching.runtime_model_settings SET revision=$1,secret_revision=$1,settings=$2,updated_by=$3,updated_at=now() WHERE singleton`, revision, public, p.UserID)
		if err != nil {
			return next, "", err
		}
		// Do not put keys, raw requests, URLs or user-entered model strings in audit metadata.
		_, err = tx.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,resource_id,result,request_id,metadata) VALUES($1,'model_settings.apply','model_settings',$2,'success',$3,jsonb_build_object('previous_revision',$4::text,'enabled',$5::boolean,'reason',$6::text))`, p.UserID, revision, requestID, current, in.Values.Enabled, in.Reason)
		return next, revision, err
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, p)
}

func BuildConfig(base analysis.Config, v Values, keys secrets, confirmed bool) (analysis.Config, error) {
	for _, key := range []string{keys.ASR, keys.Report} {
		if len(key) > 4096 || strings.IndexFunc(key, unicode.IsSpace) >= 0 || strings.IndexFunc(key, unicode.IsControl) >= 0 {
			return base, fmt.Errorf("密钥长度或格式不正确")
		}
	}
	for _, value := range []string{v.ASRBase, v.ReportBase, v.ASRModel, v.ASRRevision, v.ReportModel, v.ReportRevision, v.ASRLanguage, v.PriceVersion} {
		if len([]rune(value)) > 2048 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return base, fmt.Errorf("模型字段过长或含控制字符")
		}
	}
	if len([]rune(v.PriceSource)) > 2048 || strings.IndexFunc(v.PriceSource, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 {
		return base, fmt.Errorf("价格来源说明过长或含非法控制字符")
	}
	for _, value := range []string{v.MonthlyBudget, v.ASRInput, v.ASROutput, v.ReportInput, v.ReportOutput} {
		if _, err := Money(value); err != nil {
			return base, fmt.Errorf("预算和单价请填写最多六位小数的非负金额")
		}
	}
	if v.ASRMaxInput < 1 || v.ASRMaxInput > 131072 || v.ASRMaxOutput < 1 || v.ASRMaxOutput > 4096 || v.ReportMaxInput < 1 || v.ReportMaxInput > 131072 || v.ReportMaxOutput < 1 || v.ReportMaxOutput > 32768 {
		return base, fmt.Errorf("token上限超出当前接口支持范围")
	}
	if !v.Enabled {
		base.CloudASR = analysis.CloudASRConfig{}
		base.ReportEnabled = false
		base.ReportAPIKey = ""
		base.ReportMonthlyBudgetMicros = 0
		return base, nil
	}
	if strings.HasSuffix(strings.TrimRight(v.ASRBase, "/"), "/chat/completions") || strings.HasSuffix(strings.TrimRight(v.ReportBase, "/"), "/chat/completions") {
		return base, fmt.Errorf("请填写接口基础地址，不要包含 /chat/completions")
	}
	p := Pricing{Confirmed: confirmed, Source: v.PriceSource, Version: v.PriceVersion, MonthlyBudget: v.MonthlyBudget, APIBase: v.ASRBase, ASRModel: v.ASRModel, ASRRevision: v.ASRRevision, ASRInput: v.ASRInput, ASROutput: v.ASROutput, ReportModel: v.ReportModel, ReportRevision: v.ReportRevision, ReportInput: v.ReportInput, ReportOutput: v.ReportOutput}
	if err := p.Validate(); err != nil {
		return base, err
	}
	asrInput, _ := Money(v.ASRInput)
	asrOutput, _ := Money(v.ASROutput)
	_, promptSHA, err := analysis.ReportPrompt(analysis.TeacherPromptVersion)
	if err != nil {
		return base, err
	}
	settings := map[string]string{"ASR_CLOUD_ENABLED": "true", "ASR_API_BASE": v.ASRBase, "ASR_API_KEY": keys.ASR, "ASR_MODEL_NAME": v.ASRModel, "ASR_MODEL_REVISION": v.ASRRevision, "ASR_LANGUAGE_HINTS": v.ASRLanguage, "ASR_PRICE_VERSION": v.PriceVersion, "ASR_INPUT_PRICE_MICROS": strconv.FormatInt(asrInput, 10), "ASR_OUTPUT_PRICE_MICROS": strconv.FormatInt(asrOutput, 10), "ASR_MAX_INPUT_TOKENS": strconv.Itoa(v.ASRMaxInput), "ASR_MAX_OUTPUT_TOKENS": strconv.Itoa(v.ASRMaxOutput),
		"REPORT_API_BASE": v.ReportBase, "REPORT_API_KEY": keys.Report, "REPORT_MODEL": v.ReportModel, "REPORT_MODEL_REVISION": v.ReportRevision, "REPORT_PROMPT_VERSION": analysis.TeacherPromptVersion, "REPORT_PROMPT_SHA256": promptSHA, "REPORT_SELECTION_VERSION": "time-window-v1", "REPORT_MAX_INPUT_TOKENS": strconv.Itoa(v.ReportMaxInput), "REPORT_MAX_OUTPUT_TOKENS": strconv.Itoa(v.ReportMaxOutput), "MODEL_BUDGET_CURRENCY": base.ReportBudgetCurrency, "MODEL_BUDGET_TIMEZONE": base.ReportBudgetTimezone, "MODEL_MONTHLY_BUDGET": v.MonthlyBudget, "MODEL_PRICE_VERSION": v.PriceVersion, "MODEL_INPUT_PRICE_PER_MILLION": v.ReportInput, "MODEL_OUTPUT_PRICE_PER_MILLION": v.ReportOutput}
	if _, err = CheckSettings(settings); err != nil {
		return base, err
	}
	cfg, err := config.FromLookup(func(k string) (string, bool) {
		if k == "DATABASE_URL" {
			return "postgres://localhost/offline-model-validation?sslmode=disable", true
		}
		value, ok := settings[k]
		return value, ok
	})
	if err != nil {
		return base, fmt.Errorf("模型配置校验失败")
	}
	cloud, err := analysis.LoadCloudASR(func(k string) string { return settings[k] })
	if err != nil {
		return base, fmt.Errorf("语音配置校验失败")
	}
	budget, _ := Money(v.MonthlyBudget)
	asrReserve := (int64(v.ASRMaxInput)*asrInput + int64(v.ASRMaxOutput)*asrOutput + 999999) / 1000000
	reportReserve := (int64(v.ReportMaxInput)*cfg.ReportInputPriceMicros + int64(v.ReportMaxOutput)*cfg.ReportOutputPriceMicros + 999999) / 1000000
	if budget < asrReserve+reportReserve {
		return base, fmt.Errorf("月预算不足以同时预留一次语音和报告请求，请核对单价与额度")
	}
	base.CloudASR = cloud
	base.ASRModelName = v.ASRModel
	base.ASRModelRevision = v.ASRRevision
	base.ReportEnabled = true
	base.ReportAPIBase = v.ReportBase
	base.ReportAPIKey = keys.Report
	base.ReportModel = v.ReportModel
	base.ReportModelRevision = v.ReportRevision
	base.ReportPromptVersion = analysis.TeacherPromptVersion
	base.ReportPromptSHA256 = promptSHA
	base.ReportSelectionVersion = "time-window-v1"
	base.ReportPriceVersion = v.PriceVersion
	base.ReportMonthlyBudgetMicros = budget
	base.ReportInputPriceMicros = cfg.ReportInputPriceMicros
	base.ReportOutputPriceMicros = cfg.ReportOutputPriceMicros
	base.ReportMaxInputTokens = v.ReportMaxInput
	base.ReportMaxOutputTokens = v.ReportMaxOutput
	return base, nil
}
