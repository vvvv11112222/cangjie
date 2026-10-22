package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/governance"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

func TestAuditReadScopePaginationAndRedaction(t *testing.T) {
	pool, _, _ := captureDatabaseFixture(t)
	ctx := context.Background()
	actor := func(name string) string {
		var id string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM teaching.user_accounts WHERE username=$1`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for _, name := range []string{"admin", "academic", "supervisor", "teacher"} {
		if _, err := pool.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,result,request_id,metadata) VALUES($1,'test.audit','user','success','synthetic','{"private_text":"DO-NOT-RETURN"}')`, actor(name)); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(New(Options{Identity: identity.NewService(pool, time.Hour), Academic: academic.NewService(pool), Governance: governance.NewService(pool, nil, nil, governance.Config{}), PublicOrigin: "http://frontend.test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	defer server.Close()
	base := server.URL + "/api/v1/audit-logs"
	requestJSON(t, newTestClient(t), http.MethodGet, base, nil, "", http.StatusUnauthorized)
	clients := map[string]*http.Client{}
	for _, name := range []string{"admin", "academic", "supervisor", "teacher"} {
		c := newTestClient(t)
		loginTestUser(t, c, server.URL, name)
		clients[name] = c
	}
	requestJSON(t, clients["teacher"], http.MethodGet, base, nil, "", http.StatusForbidden)
	for _, name := range []string{"academic", "supervisor"} {
		page := requestJSON(t, clients[name], http.MethodGet, base+"?action=test.audit", nil, "", http.StatusOK)
		items := page["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("%s scope leaked: %v", name, page)
		}
		row := items[0].(map[string]any)
		if row["actor_user_id"] != actor(name) {
			t.Fatal("foreign actor leaked")
		}
		if _, ok := row["metadata"]; ok {
			t.Fatal("private metadata leaked")
		}
	}
	first := requestJSON(t, clients["admin"], http.MethodGet, base+"?action=test.audit&limit=2", nil, "", http.StatusOK)
	if len(first["items"].([]any)) != 2 || first["next_cursor"] == nil {
		t.Fatal("missing pagination", first)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO teaching.audit_logs(action,resource_type,result,request_id) VALUES('test.audit','user','success','synthetic')`); err != nil {
		t.Fatal(err)
	}
	second := requestJSON(t, clients["admin"], http.MethodGet, base+"?action=test.audit&limit=2&cursor="+url.QueryEscape(first["next_cursor"].(string)), nil, "", http.StatusOK)
	if len(second["items"].([]any)) != 2 || second["next_cursor"] != nil {
		t.Fatal("new insert disturbed old pages", second)
	}
	seen := map[string]bool{}
	for _, page := range []map[string]any{first, second} {
		for _, v := range page["items"].([]any) {
			id := v.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatal("duplicate audit row")
			}
			seen[id] = true
		}
	}
	for _, query := range []string{"limit=101", "limit=-1", "cursor=MA", "cursor=LTQ", "result=unknown", "actor_user_id=" + actor("teacher"), "limit=1&limit=2"} {
		requestJSON(t, clients["admin"], http.MethodGet, base+"?"+query, nil, "", http.StatusBadRequest)
	}
	page := requestJSON(t, clients["admin"], http.MethodGet, base+"?result=denied", nil, "", http.StatusOK)
	if len(page["items"].([]any)) != 0 {
		t.Fatal("filter ignored")
	}
}

func TestVisualDeploymentRejectsUnconfiguredFullAnalysis(t *testing.T) {
	pool, lesson, source := captureDatabaseFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE teaching.source_records SET allowed_uses='["playback","analysis"]' WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	var mediaID string
	if err := pool.QueryRow(ctx, `INSERT INTO teaching.media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,duration_ms,status) VALUES($1,$2,'source','filesystem','synthetic/source.mp4',$3,'video/mp4',12,60000,'ready') RETURNING id::text`, lesson, source, strings.Repeat("a", 64)).Scan(&mediaID); err != nil {
		t.Fatal(err)
	}
	ids := identity.NewService(pool, time.Hour)
	admin, err := ids.PrincipalByID(ctx, queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='admin'`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := analysis.Config{ASRModelName: "not-installed", MediaJobAttempts: 3}
	service := analysis.NewService(pool, nil, cfg)
	input := analysis.CreateRun{MediaAssetID: mediaID, Mode: "full", ConfigProfile: "p0-v1"}
	_, _, err = service.Create(ctx, admin, lesson, "unconfigured", input)
	var app *apperror.Error
	if !errors.As(err, &app) || app.Status != 503 || app.Details["reason"] == nil {
		t.Fatalf("missing setup error: %v", err)
	}
	if n := queryID(t, pool, `SELECT count(*)::text FROM teaching.analysis_runs`); n != "0" {
		t.Fatal("queued work without audio worker", n)
	}
	// Existing successful submissions remain replayable even after deployment settings change.
	cfg.ASRModelName = "fixed-json-asr"
	configured := analysis.NewService(pool, nil, cfg)
	prior, _, err := configured.Create(ctx, admin, lesson, "historic", input)
	if err != nil {
		t.Fatal(err)
	}
	replay, reused, err := service.Create(ctx, admin, lesson, "historic", input)
	if err != nil || !reused || replay.ID != prior.ID {
		t.Fatalf("idempotent history lost: %v", err)
	}
	input.Mode = "media_prepare"
	if _, _, err = service.Create(ctx, admin, lesson, "playback-only", input); err != nil {
		t.Fatal("playback preparation blocked", err)
	}
}
