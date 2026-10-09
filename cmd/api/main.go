package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/classroom"
	"github.com/vvvv11112222/cangjie/internal/config"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/governance"
	"github.com/vvvv11112222/cangjie/internal/httpapi"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/media"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateWorkerRuntime(); err != nil {
		return err
	}

	logger := newLogger(cfg.AppEnv)
	slog.SetDefault(logger)

	mediaStore, err := storage.NewLocal(cfg.MediaRoot)
	if err != nil {
		return err
	}
	ledger, err := governance.OpenLedger(cfg.DeletionLedgerPath)
	if err != nil {
		return err
	}

	pool, err := database.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	analysisService := analysis.NewService(pool.Pool, mediaStore, analysis.Config{
		Lease: cfg.JobLease, ProbeTimeout: cfg.ProbeTimeout, ASRTimeout: cfg.ASRTimeout, VideoTimeout: cfg.VideoTimeout,
		MediaJobAttempts: cfg.MediaJobAttempts, ProcessorVersion: cfg.ProcessorVersion, FFmpegSHA256: cfg.FFmpegSHA256,
		ASRModelName: cfg.ASRModelName, ASRModelRevision: cfg.ASRModelRevision, ASRDevice: cfg.ASRDevice,
		KeyframeIntervalMS: cfg.KeyframeIntervalMS, MaxKeyframes: cfg.MaxKeyframes, MaxVideoHeight: cfg.MaxVideoHeight,
		MaxMediaDurationMS: cfg.MaxMediaDurationMS, MaxArtifactBytes: cfg.MaxArtifactBytes,
		GoStageTimeout: cfg.GoStageTimeout, ReportEnabled: cfg.ReportEnabled,
		ReportAPIBase: cfg.ReportAPIBase, ReportAPIKey: cfg.ReportAPIKey,
		ReportModel: cfg.ReportModel, ReportModelRevision: cfg.ReportModelRevision,
		ReportPromptVersion: cfg.ReportPromptVersion, ReportPromptSHA256: cfg.ReportPromptSHA256,
		ReportSelectionVersion: cfg.ReportSelectionVersion, ReportTimeout: cfg.ReportTimeout,
		ReportPriceVersion:   cfg.ReportPriceVersion,
		ReportBudgetCurrency: cfg.ReportBudgetCurrency, ReportBudgetTimezone: cfg.ReportBudgetTimezone,
		ReportMonthlyBudgetMicros: cfg.ReportMonthlyBudgetMicros,
		ReportInputPriceMicros:    cfg.ReportInputPriceMicros, ReportOutputPriceMicros: cfg.ReportOutputPriceMicros,
		ReportMaxInputTokens: cfg.ReportMaxInputTokens, ReportMaxOutputTokens: cfg.ReportMaxOutputTokens,
	})

	checks := httpapi.ReadinessChecks{
		"database": pool.Ping,
		"media_storage": func(ctx context.Context) error {
			return mediaStore.Check(ctx)
		},
		"deletion_ledger": func(ctx context.Context) error {
			return ledger.Check(ctx)
		},
	}
	origin, _ := url.Parse(cfg.PublicOrigin)
	handler := httpapi.New(httpapi.Options{
		Logger:             logger,
		Readiness:          checks,
		Identity:           identity.NewService(pool.Pool, cfg.SessionTTL),
		Academic:           academic.NewService(pool.Pool),
		Classroom:          classroom.NewService(pool.Pool),
		Media:              media.NewService(pool.Pool, mediaStore, cfg.MaxUploadBytes, cfg.MediaRetention),
		Analysis:           analysisService,
		WorkerToken:        cfg.WorkerToken,
		WorkerID:           cfg.WorkerID,
		WorkerCapabilities: cfg.WorkerCapabilities,
		MaxArtifactBytes:   cfg.MaxArtifactBytes,
		PublicOrigin:       cfg.PublicOrigin,
		SecureCookie:       origin.Scheme == "https",
		SessionTTL:         cfg.SessionTTL,
		MaxUploadBytes:     cfg.MaxUploadBytes,
		UploadTimeout:      cfg.UploadTimeout,
	})
	server := &http.Server{
		Addr:              cfg.APIListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go analysisService.RunReaper(ctx, cfg.JobReaper)
	go analysisService.RunGoWorker(ctx, time.Second)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening", "address", cfg.APIListenAddr, "environment", cfg.AppEnv)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func newLogger(environment string) *slog.Logger {
	options := &slog.HandlerOptions{Level: slog.LevelInfo}
	if environment == "development" || environment == "test" {
		return slog.New(slog.NewTextHandler(os.Stdout, options))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, options))
}
