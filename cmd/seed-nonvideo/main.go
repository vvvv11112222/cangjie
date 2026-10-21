package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vvvv11112222/cangjie/internal/config"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/devseed"
)

func main() {
	apply := flag.Bool("apply", false, "insert missing synthetic non-video fixtures into the configured development/test database")
	flag.Parse()
	if !*apply {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "plan_only", "fixture": "nonvideo-v1", "counts": devseed.Plan()})
		return
	}
	cfg, err := config.Load()
	if err != nil {
		fail(err)
	}
	request := devseed.Request{Environment: cfg.AppEnv, Password: os.Getenv("BOOTSTRAP_TEST_PASSWORD")}
	if err := request.Validate(); err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fail(err)
	}
	defer pool.Close()
	counts, err := devseed.Apply(ctx, pool.Pool, request)
	if err != nil {
		fail(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "applied", "fixture": "nonvideo-v1", "counts": counts})
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "non-video fixture seed failed:", err)
	os.Exit(1)
}
