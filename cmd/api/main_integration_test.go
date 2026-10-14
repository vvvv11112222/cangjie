package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/governance"
)

func TestInstalledAPIUsesConfiguredMigrations(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	dbConfig, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	if dbConfig.Database != "cangjie_backend_acceptance" {
		t.Skip("requires disposable cangjie_backend_acceptance database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, dbConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('cangjie_integration_test_fixture',0))`); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended('cangjie_integration_test_fixture',0))`)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "database")
	if err := database.RunMigrations(ctx, url, directory); err != nil {
		t.Fatal(err)
	}
	// Real LOGIN roles ensure readiness works with the deployment business account.
	prefix := fmt.Sprintf("installed_api_%d", time.Now().UnixNano())
	migrator, app := prefix+"_migration", prefix+"_app"
	quoted := pgx.Identifier{migrator}.Sanitize() + "," + pgx.Identifier{app}.Sanitize()
	password := "'" + strings.ReplaceAll(dbConfig.Password, "'", "''") + "'"
	if _, err := conn.Exec(ctx, "CREATE ROLE "+pgx.Identifier{migrator}.Sanitize()+" NOLOGIN; CREATE ROLE "+pgx.Identifier{app}.Sanitize()+" LOGIN PASSWORD "+password); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, err := conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS teaching CASCADE; DROP TABLE IF EXISTS public.schema_migrations; DROP OWNED BY "+quoted+" CASCADE; DROP ROLE "+quoted)
		if err != nil {
			t.Errorf("installed API fixture cleanup: %v", err)
		}
	}()
	body, err := os.ReadFile(filepath.Join(directory, "configure_roles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SELECT set_config('cangjie.migration_role',$1,false),set_config('cangjie.app_role',$2,false)`, migrator, app); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	ledgerPath := filepath.Join(scratch, "ledger", "deletions.jsonl")
	if _, err := governance.InitializeLedger(ledgerPath); err != nil {
		t.Fatal(err)
	}
	executable := "api"
	goExecutable := "go"
	if runtime.GOOS == "windows" {
		executable += ".exe"
		goExecutable += ".exe"
	}
	binary := filepath.Join(scratch, executable)
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", goExecutable), "build", "-o", binary, "./cmd/api")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build API: %v\n%s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	connectionURL, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	connectionURL.User = neturl.UserPassword(app, dbConfig.Password)
	appURL := connectionURL.String()
	overrides := map[string]string{
		"APP_ENV": "test", "DATABASE_URL": appURL, "DATABASE_MIGRATIONS_DIR": directory,
		"API_LISTEN_ADDR": address, "PUBLIC_ORIGIN": "http://" + address,
		"MEDIA_ROOT": filepath.Join(scratch, "media"), "WORKER_TEMP_ROOT": filepath.Join(scratch, "worker-tmp"),
		"DELETION_LEDGER_PATH": ledgerPath,
		"WORKER_TOKEN":         strings.Repeat("x", 32), "WORKER_PROCESSOR_VERSION": "fixed-json-v1",
		"FFMPEG_BUILD_SHA256": strings.Repeat("a", 64), "ASR_MODEL_NAME": "fixed-json-asr",
		"ASR_MODEL_REVISION": "p0-v1", "ASR_DEVICE": "cpu",
		"WORKER_CAPABILITIES": "probe,audio_analysis,video_analysis", "MODEL_MONTHLY_BUDGET": "0",
	}
	var environment []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	processCtx, stop := context.WithCancel(ctx)
	defer stop()
	process := exec.CommandContext(processCtx, binary)
	process.Dir, process.Env = scratch, environment
	log, err := os.Create(filepath.Join(scratch, "api.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	process.Stdout, process.Stderr = log, log
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stop(); process.Wait() }()
	client := &http.Client{Timeout: time.Second}
	ready := func(want int) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			response, err := client.Get("http://" + address + "/health/ready")
			if err == nil {
				status := response.StatusCode
				response.Body.Close()
				if status == want {
					return
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		output, _ := os.ReadFile(log.Name())
		t.Fatalf("readiness did not reach %d from external cwd: %s", want, output)
	}
	ready(http.StatusOK)
	if _, err := conn.Exec(ctx, `ALTER TABLE teaching.model_calls DROP CONSTRAINT ck_call_settlement`); err != nil {
		t.Fatal(err)
	}
	ready(http.StatusServiceUnavailable)
	if _, err := conn.Exec(ctx, `ALTER TABLE teaching.model_calls ADD CONSTRAINT ck_call_settlement CHECK (
		(status IN('reserved','unknown') AND actual_cost IS NULL AND settled_at IS NULL)
		OR (status IN('succeeded','failed') AND actual_cost IS NOT NULL AND settled_at IS NOT NULL))`); err != nil {
		t.Fatal(err)
	}
	ready(http.StatusOK)
}
