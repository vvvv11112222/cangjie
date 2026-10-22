package config

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// IsModelSetting is shared by the opt-in runtime loader and offline setup tool.
// The file cannot replace database, authorization, storage or Worker secrets.
func IsModelSetting(key string) bool {
	for _, name := range strings.Fields("ASR_CLOUD_ENABLED ASR_API_BASE ASR_API_KEY ASR_MODEL_NAME ASR_MODEL_REVISION ASR_LANGUAGE_HINTS ASR_PRICE_VERSION ASR_BILLING_MODE ASR_PRICE_PER_SECOND_MICROS ASR_INPUT_PRICE_MICROS ASR_OUTPUT_PRICE_MICROS ASR_MAX_INPUT_TOKENS ASR_MAX_OUTPUT_TOKENS REPORT_API_BASE REPORT_API_KEY REPORT_MODEL REPORT_MODEL_REVISION REPORT_PROMPT_VERSION REPORT_PROMPT_SHA256 REPORT_SELECTION_VERSION REPORT_MAX_INPUT_TOKENS REPORT_MAX_OUTPUT_TOKENS REPORT_TIMEOUT_SECONDS MODEL_BUDGET_CURRENCY MODEL_BUDGET_TIMEZONE MODEL_MONTHLY_BUDGET MODEL_PRICE_VERSION MODEL_INPUT_PRICE_PER_MILLION MODEL_OUTPUT_PRICE_PER_MILLION") {
		if name == key {
			return true
		}
	}
	return false
}

// WithModelFile returns one immutable lookup shared by report and ASR loading.
// Explicit file settings override the base lookup without mutating os.Environ.
func WithModelFile(base func(string) (string, bool), path string) (func(string) (string, bool), error) {
	if path == "" {
		return base, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("MODEL_CONFIG_FILE cannot be read; credentials and contents are not logged")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("MODEL_CONFIG_FILE must be a regular JSON file under 1 MiB")
	}
	dec := json.NewDecoder(io.LimitReader(file, 1<<20))
	var settings map[string]string
	if dec.Decode(&settings) != nil || len(settings) == 0 {
		return nil, errors.New("MODEL_CONFIG_FILE must contain a non-empty string settings object; contents are not logged")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, errors.New("MODEL_CONFIG_FILE contains extra JSON")
	}
	for key := range settings {
		if !IsModelSetting(key) {
			return nil, errors.New("MODEL_CONFIG_FILE contains an unsupported setting; only approved ASR/REPORT/MODEL keys are allowed")
		}
	}
	return func(key string) (string, bool) {
		if value, ok := settings[key]; ok {
			return value, true
		}
		return base(key)
	}, nil
}
