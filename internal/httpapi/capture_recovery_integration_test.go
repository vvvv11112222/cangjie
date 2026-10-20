package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/capture"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/media"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

func TestLiveCaptureRevocationCleanupAndFinalizeBackoff(t *testing.T) {
	pool, lesson, source := captureDatabaseFixture(t)
	ctx := context.Background()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/paths/get/") {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"pageCount":1,"items":[]}`)
	}))
	defer control.Close()
	store, e := storage.NewLocal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	ids := identity.NewService(pool, time.Hour)
	p, _, e := ids.Login(ctx, "admin", integrationPassword, "")
	if e != nil {
		t.Fatal(e)
	}
	cs, e := capture.New(pool, media.NewService(pool, store, 1<<20, time.Hour), analysis.NewService(pool, store, analysis.Config{}), ids, capture.Config{PublicURL: "https://capture.test", MediaURL: "https://capture.test/media", ControlURL: control.URL, ControlToken: strings.Repeat("c", 32), EventToken: strings.Repeat("e", 32), RecordRoot: root, FFprobe: filepath.Join(root, "missing-probe")})
	if e != nil {
		t.Fatal(e)
	}
	create := func(key string) capture.Capture {
		t.Helper()
		v, _, e := cs.Create(ctx, p, lesson, key, capture.Create{SourceID: source, Profile: "phone-demo-v1"})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	first := create("revoke")
	pair, e := cs.Pair(ctx, p, first.ID)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(pair.URL)
	f, _ := url.ParseQuery(u.Fragment)
	pub, e := cs.Exchange(ctx, capture.Exchange{Token: f.Get("pairing_token")})
	if e != nil {
		t.Fatal(e)
	}
	path := queryID(t, pool, `SELECT stream_path FROM teaching.live_captures WHERE id='`+first.ID+`'`)
	auth := capture.AuthRequest{Action: "publish", Protocol: "webrtc", Path: path, ID: "00000000-0000-4000-8000-000000000020", Token: pub.PublishToken}
	if e = cs.Authenticate(ctx, auth); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE teaching.source_records SET allowed_uses='[]',rights_version=rights_version+1 WHERE id=$1`, source); e != nil {
		t.Fatal(e)
	}
	if e = cs.Authenticate(ctx, auth); e == nil {
		t.Fatal("revoked source retained publish access")
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); cs.Run(runCtx) }()
	defer func() { cancel(); <-done }()
	end := time.Now().Add(4 * time.Second)
	for {
		state := queryID(t, pool, `SELECT state FROM teaching.live_captures WHERE id='`+first.ID+`'`)
		if state == "failed" {
			break
		}
		if time.Now().After(end) {
			t.Fatal("revocation did not stop capture")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, e = cs.ControlPrincipal(ctx, first.ID, pub.ControlToken); e == nil {
		t.Fatal("revoked capture retained control grant")
	}
	if _, e = pool.Exec(ctx, `UPDATE teaching.source_records SET allowed_uses='["playback"]',rights_version=rights_version+1 WHERE id=$1`, source); e != nil {
		t.Fatal(e)
	}
	second := create("cleanup")
	path = queryID(t, pool, `SELECT stream_path FROM teaching.live_captures WHERE id='`+second.ID+`'`)
	file := filepath.Join(root, filepath.FromSlash(path), "part.mp4")
	if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, []byte("synthetic recording"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = cs.CleanupSession(ctx, lesson); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(file); !os.IsNotExist(e) {
		t.Fatal("cleanup left recording")
	}
	third := create("retry")
	thirdPair, e := cs.Pair(ctx, p, third.ID)
	if e != nil {
		t.Fatal(e)
	}
	thirdURL, _ := url.Parse(thirdPair.URL)
	thirdFragment, _ := url.ParseQuery(thirdURL.Fragment)
	thirdPublisher, e := cs.Exchange(ctx, capture.Exchange{Token: thirdFragment.Get("pairing_token")})
	if e != nil {
		t.Fatal(e)
	}
	path = queryID(t, pool, `SELECT stream_path FROM teaching.live_captures WHERE id='`+third.ID+`'`)
	file = filepath.Join(root, filepath.FromSlash(path), "part.mp4")
	if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, []byte("synthetic invalid media"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = cs.Stop(ctx, p, third.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE teaching.live_captures SET stop_requested_at=now()-interval '4 seconds',finalize_token=gen_random_uuid(),finalize_until=now()-interval '1 second',attempts=1 WHERE id=$1`, third.ID); e != nil {
		t.Fatal(e)
	}
	end = time.Now().Add(4 * time.Second)
	for {
		var n int
		var active bool
		if e = pool.QueryRow(ctx, `SELECT attempts,finalize_token IS NOT NULL FROM teaching.live_captures WHERE id=$1`, third.ID).Scan(&n, &active); e != nil {
			t.Fatal(e)
		}
		if n == 2 && !active {
			break
		}
		if time.Now().After(end) {
			t.Fatal("finalizer did not release failed lease")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(1200 * time.Millisecond)
	var attempts int
	var delayed bool
	if e = pool.QueryRow(ctx, `SELECT attempts,available_at>now() FROM teaching.live_captures WHERE id=$1`, third.ID).Scan(&attempts, &delayed); e != nil {
		t.Fatal(e)
	}
	if attempts != 2 || !delayed {
		t.Fatalf("failed finalizer bypassed backoff: attempts=%d delayed=%v", attempts, delayed)
	}
	if _, e = pool.Exec(ctx, `UPDATE teaching.live_captures SET available_at=now() WHERE id=$1`, third.ID); e != nil {
		t.Fatal(e)
	}
	end = time.Now().Add(4 * time.Second)
	for {
		v, err := cs.Get(ctx, p, third.ID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State == "failed" {
			break
		}
		if time.Now().After(end) {
			t.Fatal("operational failure did not reach terminal state")
		}
		time.Sleep(50 * time.Millisecond)
	}
	phonePrincipal, e := cs.ControlPrincipal(ctx, third.ID, thirdPublisher.ControlToken)
	if e != nil {
		t.Fatal("operational failure hid terminal status from phone:", e)
	}
	failed, e := cs.Get(ctx, phonePrincipal, third.ID)
	if e != nil || failed.State != "failed" || failed.ErrorCode == nil || *failed.ErrorCode != "CAPTURE_FINALIZE_FAILED" {
		t.Fatalf("phone terminal status=%+v err=%v", failed, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE teaching.capture_grants SET expires_at=now()-interval '1 second' WHERE capture_id=$1 AND purpose='control'`, third.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = cs.ControlPrincipal(ctx, third.ID, thirdPublisher.ControlToken); e == nil {
		t.Fatal("expired control token retained access")
	}
}
