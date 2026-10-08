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
	MaxUploadBytes     int64
	UploadTimeout      time.Duration
	MediaRetention     time.Duration
	WorkerToken        string
	WorkerID           string
	WorkerCapabilities []string
	ProcessorVersion   string
	FFmpegSHA256       string
	ASRModelName       string
	ASRModelRevision   string
	ASRDevice          string
	MediaJobAttempts   int
	MaxArtifactBytes   int64
	MaxMediaDurationMS int64
	MaxVideoHeight     int
	KeyframeIntervalMS int
	MaxKeyframes       int
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
	uploadTimeout, err := seconds(value("UPLOAD_TIMEOUT_SECONDS", "1800"), "UPLOAD_TIMEOUT_SECONDS")
	if err != nil {
		return Config{}, err
	}
	maxUploadBytes, err := positiveInt64(value("MAX_UPLOAD_BYTES", "4294967296"), "MAX_UPLOAD_BYTES")
	if err != nil {
		return Config{}, err
	}
	retentionDays, err := positiveInt64(value("MEDIA_RETENTION_DAYS", "14"), "MEDIA_RETENTION_DAYS")
	if err != nil {
		return Config{}, err
	}
	maxArtifactBytes, err := positiveInt64(value("MAX_ARTIFACT_BYTES", "4294967296"), "MAX_ARTIFACT_BYTES")
	if err != nil {
		return Config{}, err
	}
	maxDuration, err := positiveInt64(value("MAX_MEDIA_DURATION_MS", "7200000"), "MAX_MEDIA_DURATION_MS")
	if err != nil {
		return Config{}, err
	}
	mediaAttempts, err := positiveInt(value("MEDIA_JOB_MAX_ATTEMPTS", "3"), "MEDIA_JOB_MAX_ATTEMPTS")
	if err != nil {
		return Config{}, err
	}
	maxVideoHeight, err := positiveInt(value("MAX_VIDEO_HEIGHT", "1080"), "MAX_VIDEO_HEIGHT")
	if err != nil {
		return Config{}, err
	}
	keyframeInterval, err := positiveInt(value("KEYFRAME_INTERVAL_MS", "30000"), "KEYFRAME_INTERVAL_MS")
	if err != nil {
		return Config{}, err
	}
	maxKeyframes, err := positiveInt(value("MAX_KEYFRAMES", "240"), "MAX_KEYFRAMES")
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
		MaxUploadBytes:     maxUploadBytes,
		UploadTimeout:      uploadTimeout,
		MediaRetention:     time.Duration(retentionDays) * 24 * time.Hour,
		WorkerToken:        value("WORKER_TOKEN", ""),
		WorkerID:           value("WORKER_ID", "worker-01"),
		WorkerCapabilities: splitCSV(value("WORKER_CAPABILITIES", "probe,audio_analysis,video_analysis")),
		ProcessorVersion:   value("WORKER_PROCESSOR_VERSION", ""),
		FFmpegSHA256:       value("FFMPEG_BUILD_SHA256", ""),
		ASRModelName:       value("ASR_MODEL_NAME", ""),
		ASRModelRevision:   value("ASR_MODEL_REVISION", ""),
		ASRDevice:          value("ASR_DEVICE", "cpu"),
		MediaJobAttempts:   mediaAttempts,
		MaxArtifactBytes:   maxArtifactBytes,
		MaxMediaDurationMS: maxDuration,
		MaxVideoHeight:     maxVideoHeight,
		KeyframeIntervalMS: keyframeInterval,
		MaxKeyframes:       maxKeyframes,
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
		return fmt.Errorf("DATABASE_URL is invalid")
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

func (cfg Config) ValidateWorkerRuntime() error {
	if len(cfg.WorkerToken) < 32 {
		return fmt.Errorf("WORKER_TOKEN must contain at least 32 characters")
	}
	if cfg.WorkerID == "" || len(cfg.WorkerCapabilities) == 0 {
		return fmt.Errorf("WORKER_ID and WORKER_CAPABILITIES are required")
	}
	allowedCapabilities := map[string]bool{"probe": true, "audio_analysis": true, "video_analysis": true}
	for _, capability := range cfg.WorkerCapabilities {
		if !allowedCapabilities[capability] {
			return fmt.Errorf("WORKER_CAPABILITIES contains an invalid stage")
		}
	}
	for _, required := range []string{"probe", "audio_analysis", "video_analysis"} {
		if !contains(cfg.WorkerCapabilities, required) {
			return fmt.Errorf("WORKER_CAPABILITIES must include probe, audio_analysis, and video_analysis")
		}
	}
	if cfg.ProcessorVersion == "" || !isSHA256(cfg.FFmpegSHA256) {
		return fmt.Errorf("WORKER_PROCESSOR_VERSION and a SHA-256 FFMPEG_BUILD_SHA256 are required")
	}
	if cfg.ASRDevice != "cpu" && cfg.ASRDevice != "cuda" {
		return fmt.Errorf("ASR_DEVICE must be cpu or cuda")
	}
	if contains(cfg.WorkerCapabilities, "audio_analysis") && (cfg.ASRModelName == "" || cfg.ASRModelRevision == "") {
		return fmt.Errorf("ASR_MODEL_NAME and ASR_MODEL_REVISION are required for audio_analysis")
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

func positiveInt64(raw, key string) (int64, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return n, nil
}

func positiveInt(raw, key string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return n, nil
}

func splitCSV(raw string) []string {
	seen := map[string]bool{}
	var values []string
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	return values
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
