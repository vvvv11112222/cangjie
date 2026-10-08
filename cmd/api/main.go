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
	"github.com/vvvv11112222/cangjie/internal/classroom"
	"github.com/vvvv11112222/cangjie/internal/config"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/governance"
	"github.com/vvvv11112222/cangjie/internal/httpapi"
	"github.com/vvvv11112222/cangjie/internal/identity"
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
		Logger:       logger,
		Readiness:    checks,
		Identity:     identity.NewService(pool.Pool, cfg.SessionTTL),
		Academic:     academic.NewService(pool.Pool),
		Classroom:    classroom.NewService(pool.Pool),
		PublicOrigin: cfg.PublicOrigin,
		SecureCookie: origin.Scheme == "https",
		SessionTTL:   cfg.SessionTTL,
	})
	server := &http.Server{
		Addr:              cfg.APIListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
