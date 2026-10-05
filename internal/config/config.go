package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv             string
	APIListenAddr      string
	PublicOrigin       string
	DatabaseURL        string
	MediaRoot          string
	WorkerTempRoot     string
	DeletionLedgerPath string
	JobLease           time.Duration
	JobHeartbeat       time.Duration
	JobReaper          time.Duration
	ProbeTimeout       time.Duration
	ASRTimeout         time.Duration
	VideoTimeout       time.Duration
	GoStageTimeout     time.Duration
	ReportEnabled      bool
	SessionTTL         time.Duration
}

func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}
	return FromLookup(os.LookupEnv)
}

func FromLookup(lookup func(string) (string, bool)) (Config, error) {
	value := func(key, fallback string) string {
		if current, ok := lookup(key); ok {
			return strings.TrimSpace(current)
		}
		return fallback
	}

	lease, err := seconds(value("JOB_LEASE_SECONDS", "120"), "JOB_LEASE_SECONDS")
	if err != nil {
		return Config{}, err
	}
	heartbeat, err := seconds(value("JOB_HEARTBEAT_SECONDS", "30"), "JOB_HEARTBEAT_SECONDS")
	if err != nil {
		return Config{}, err
	}
	reaper, err := seconds(value("JOB_REAPER_SECONDS", "15"), "JOB_REAPER_SECONDS")
	if err != nil {
		return Config{}, err
	}
	probe, err := seconds(value("PROBE_TIMEOUT_SECONDS", "1800"), "PROBE_TIMEOUT_SECONDS")
	if err != nil {
		return Config{}, err
	}
	asr, err := seconds(value("ASR_TIMEOUT_SECONDS", "14400"), "ASR_TIMEOUT_SECONDS")
	if err != nil {
		return Config{}, err
	}
	video, err := seconds(value("VIDEO_TIMEOUT_SECONDS", "3600"), "VIDEO_TIMEOUT_SECONDS")
	if err != nil {
		return Config{}, err
	}
	goStage, err := seconds(value("GO_STAGE_TIMEOUT_SECONDS", "300"), "GO_STAGE_TIMEOUT_SECONDS")
	if err != nil {
		return Config{}, err
	}
	sessionTTL, err := seconds(value("SESSION_TTL_SECONDS", "28800"), "SESSION_TTL_SECONDS")
	if err != nil {
		return Config{}, err
	}

	budget, err := nonNegativeFloat(value("MODEL_MONTHLY_BUDGET", "0"), "MODEL_MONTHLY_BUDGET")
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		AppEnv:             value("APP_ENV", "development"),
		APIListenAddr:      value("API_LISTEN_ADDR", "127.0.0.1:8080"),
		PublicOrigin:       value("PUBLIC_ORIGIN", "http://localhost:8080"),
		DatabaseURL:        value("DATABASE_URL", ""),
		MediaRoot:          value("MEDIA_ROOT", "./var/media"),
		WorkerTempRoot:     value("WORKER_TEMP_ROOT", "./var/worker-tmp"),
		DeletionLedgerPath: value("DELETION_LEDGER_PATH", "./var/governance/deletions.jsonl"),
		JobLease:           lease,
		JobHeartbeat:       heartbeat,
		JobReaper:          reaper,
		ProbeTimeout:       probe,
		ASRTimeout:         asr,
		VideoTimeout:       video,
		GoStageTimeout:     goStage,
		ReportEnabled:      budget > 0,
		SessionTTL:         sessionTTL,
	}
	if err := validate(cfg, value, budget); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validate(cfg Config, value func(string, string) string, budget float64) error {
	switch cfg.AppEnv {
	case "development", "test", "production":
	default:
		return fmt.Errorf("APP_ENV must be development, test, or production")
	}
	if _, _, err := net.SplitHostPort(cfg.APIListenAddr); err != nil {
		return fmt.Errorf("API_LISTEN_ADDR must be host:port: %w", err)
	}
	origin, err := url.ParseRequestURI(cfg.PublicOrigin)
	if err != nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" {
		return fmt.Errorf("PUBLIC_ORIGIN must be an absolute http(s) URL")
	}
	if cfg.AppEnv == "production" && origin.Scheme != "https" {
		return fmt.Errorf("PUBLIC_ORIGIN must use https in production")
	}
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if _, err := url.Parse(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("DATABASE_URL is invalid: %w", err)
	}
	for key, path := range map[string]string{
		"MEDIA_ROOT": cfg.MediaRoot, "WORKER_TEMP_ROOT": cfg.WorkerTempRoot,
		"DELETION_LEDGER_PATH": cfg.DeletionLedgerPath,
	} {
		if path == "" || filepath.Clean(path) == "." {
			return fmt.Errorf("%s must name a dedicated path", key)
		}
	}
	if cfg.JobHeartbeat >= cfg.JobLease {
		return fmt.Errorf("JOB_HEARTBEAT_SECONDS must be shorter than JOB_LEASE_SECONDS")
	}
	if cfg.JobReaper >= cfg.JobLease {
		return fmt.Errorf("JOB_REAPER_SECONDS must be shorter than JOB_LEASE_SECONDS")
	}
	for key, timeout := range map[string]time.Duration{
		"PROBE_TIMEOUT_SECONDS":    cfg.ProbeTimeout,
		"ASR_TIMEOUT_SECONDS":      cfg.ASRTimeout,
		"VIDEO_TIMEOUT_SECONDS":    cfg.VideoTimeout,
		"GO_STAGE_TIMEOUT_SECONDS": cfg.GoStageTimeout,
	} {
		if timeout < cfg.JobLease {
			return fmt.Errorf("%s must not be shorter than JOB_LEASE_SECONDS", key)
		}
	}
	if budget > 0 {
		required := []string{"REPORT_API_BASE", "REPORT_API_KEY", "REPORT_MODEL", "REPORT_MODEL_REVISION", "REPORT_PROMPT_VERSION", "REPORT_PROMPT_SHA256", "REPORT_SELECTION_VERSION", "MODEL_PRICE_VERSION"}
		for _, key := range required {
			if value(key, "") == "" {
				return fmt.Errorf("%s is required when MODEL_MONTHLY_BUDGET is positive", key)
			}
		}
		for _, key := range []string{"MODEL_INPUT_PRICE_PER_MILLION", "MODEL_OUTPUT_PRICE_PER_MILLION"} {
			price, err := nonNegativeFloat(value(key, ""), key)
			if err != nil || price == 0 {
				return fmt.Errorf("%s must be positive when reporting is enabled", key)
			}
		}
	}
	return nil
}

func seconds(raw, key string) (time.Duration, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return time.Duration(n) * time.Second, nil
}

func nonNegativeFloat(raw, key string) (float64, error) {
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative number", key)
	}
	return n, nil
}
