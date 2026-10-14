package main

import (
	"log/slog"
	"os"

	"github.com/vvvv11112222/cangjie/internal/config"
	"github.com/vvvv11112222/cangjie/internal/governance"
)

func main() {
	cfg, err := config.Load()
	if err == nil {
		_, err = governance.InitializeLedger(cfg.DeletionLedgerPath)
	}
	if err != nil {
		slog.Error("deletion ledger initialization failed", "error", err)
		os.Exit(1)
	}
	slog.Info("deletion ledger is initialized", "path", cfg.DeletionLedgerPath)
}
