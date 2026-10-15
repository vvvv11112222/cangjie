package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/capture"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/media"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

func TestLiveCaptureAuthorizationConcurrencyAndStop(t *testing.T) {
	ctx := context.Background()
	pool, lesson, source := captureDatabaseFixture(t)
	local, e := storage.NewLocal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	var online bool
	var mu sync.Mutex
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/paths/get/") {
			if !online {
				w.WriteHeader(404)
				return
			}
			fmt.Fprintf(w, `{"ready":true,"tracks":["H264","Opus"],"source":{"id":"00000000-0000-4000-8000-000000000099","type":"webRTCSession"}}`)
			return
		}
		if strings.Contains(r.URL.Path, "/webrtcsessions/kick/") {
			online = false
			return
		}
		fmt.Fprint(w, `{"pageCount":1,"items":[]}`)
	}))
	defer control.Close()
	ids := identity.NewService(pool, time.Hour)
	ms := media.NewService(pool, local, 1<<20, time.Hour)
	as := analysis.NewService(pool, local, analysis.Config{})
	recordRoot := t.TempDir()
	cs, e := capture.New(pool, ms, as, ids, capture.Config{PublicURL: "https://capture.test", MediaURL: "https://capture.test/media", ControlURL: control.URL, ControlToken: strings.Repeat("c", 32), EventToken: strings.Repeat("e", 32), RecordRoot: recordRoot})
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(New(Options{Identity: ids, Academic: academic.NewService(pool), Capture: cs, PublicOrigin: "http://frontend.test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	defer srv.Close()
	client := newTestClient(t)
	csrf := loginTestUser(t, client, srv.URL, "admin")
	create := func(key string, want int) map[string]any {
		return requestWithHeaders(t, client, "POST", srv.URL+"/api/v1/sessions/"+lesson+"/live-captures", map[string]any{"source_record_id": source, "config_profile": "phone-demo-v1"}, csrf, map[string]string{"Idempotency-Key": key}, want)
	}
	activeURL := srv.URL + "/api/v1/sessions/" + lesson + "/live-captures/active"
	if status := rawStatus(client, "GET", activeURL, nil, nil); status != 404 {
		t.Fatalf("missing active capture status=%d", status)
	}
	first := create("capture-test", 201)
	id := first["id"].(string)
	if active := requestJSON(t, client, "GET", activeURL, nil, "", 200); active["id"] != id {
		t.Fatal("active capture lookup returned a different capture")
	}
	base := srv.URL + "/api/v1/live-captures/" + id
	if v := create("capture-test", 200); v["id"] != id {
		t.Fatal("idempotent create returned a different capture")
	}
	create("different", 409)
	requestWorker(t, "GET", base, nil, "", "", 401)
	pair := requestJSON(t, client, "POST", base+"/pairings", map[string]any{}, csrf, 201)
	u, _ := url.Parse(pair["phone_url"].(string))
	fragment, _ := url.ParseQuery(u.Fragment)
	token := fragment.Get("pairing_token")
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- rawStatus(http.DefaultClient, "POST", srv.URL+"/api/v1/capture-pairings/exchange", map[string]any{"pairing_token": token}, map[string]string{"Content-Type": "application/json"})
		}()
	}
	wg.Wait()
	close(statuses)
	good, bad := 0, 0
	for status := range statuses {
		if status == 200 {
			good++
		}
		if status == 403 {
			bad++
		}
	}
	if good != 1 || bad != 1 {
		t.Fatalf("pairing must be consumed once: success=%d rejected=%d", good, bad)
	}
	// The lost response is deliberately unrecoverable; stop it and create a new capture.
	requestJSON(t, client, "POST", base+"/stop", map[string]any{}, csrf, 202)
	requestJSON(t, client, "POST", base+"/stop", map[string]any{}, csrf, 202)
	if _, e = pool.Exec(ctx, `UPDATE teaching.live_captures SET state='failed' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if status := rawStatus(client, "GET", activeURL, nil, nil); status != 404 {
		t.Fatalf("terminal capture remained active status=%d", status)
	}
	id = create("capture-second", 201)["id"].(string)
	base = srv.URL + "/api/v1/live-captures/" + id
	pair = requestJSON(t, client, "POST", base+"/pairings", map[string]any{}, csrf, 201)
	u, _ = url.Parse(pair["phone_url"].(string))
	fragment, _ = url.ParseQuery(u.Fragment)
	pub := requestWorker(t, "POST", srv.URL+"/api/v1/capture-pairings/exchange", map[string]any{"pairing_token": fragment.Get("pairing_token")}, "", "", 200)
	publish := pub["publish_token"].(string)
	phone := pub["control_token"].(string)
	requestWorker(t, "GET", base, nil, publish, "", 403)
	requestWorker(t, "GET", base, nil, phone, "", 200)
	path := queryID(t, pool, `SELECT stream_path FROM teaching.live_captures WHERE id='`+id+`'`)
	auth := capture.AuthRequest{Token: publish, Action: "publish", Path: path, Protocol: "webrtc", ID: "00000000-0000-4000-8000-000000000099"}
	requestWorker(t, "POST", srv.URL+"/internal/v1/streams/auth", auth, "", "", 204)
	auth.Action = "read"
	requestWorker(t, "POST", srv.URL+"/internal/v1/streams/auth", auth, "", "", 403)
	auth.Action = "publish"
	mu.Lock()
	online = true
	mu.Unlock()
	file := filepath.Join(recordRoot, filepath.FromSlash(path), "2026-10-15_00-00-00-000001.mp4")
	if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, []byte("synthetic-nonempty-recorder-part"), 0600); e != nil {
		t.Fatal(e)
	}
	event := capture.Event{ID: "online-test", Type: "online", Path: path, ConnectionID: auth.ID, OccurredAt: time.Now().UTC()}
	requestWorker(t, "POST", srv.URL+"/internal/v1/streams/events", event, "wrong", "", 401)
	requestWorker(t, "POST", srv.URL+"/internal/v1/streams/events", event, strings.Repeat("e", 32), "", 204)
	requestWorker(t, "POST", srv.URL+"/internal/v1/streams/events", event, strings.Repeat("e", 32), "", 204)
	event.Type = "offline"
	requestWorker(t, "POST", srv.URL+"/internal/v1/streams/events", event, strings.Repeat("e", 32), "", 409)
	// Drive reconciliation to prove recording state requires actual server media/files.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go cs.Run(runCtx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		v := requestWorker(t, "GET", base, nil, phone, "", 200)
		if v["state"] == "recording" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recorder did not become recording")
		}
		time.Sleep(50 * time.Millisecond)
	}
	view := requestJSON(t, client, "POST", base+"/viewer-sessions", map[string]any{}, csrf, 201)
	auth.Action = "read"
	auth.Token = view["view_token"].(string)
	auth.ID = "00000000-0000-4000-8000-000000000098"
	requestWorker(t, "POST", srv.URL+"/internal/v1/streams/auth", auth, "", "", 204)
	requestWorker(t, "POST", base+"/stop", map[string]any{}, auth.Token, "", 403)
	requestWorker(t, "POST", base+"/stop", map[string]any{}, phone, "", 202)
	requestWorker(t, "POST", srv.URL+"/internal/v1/streams/auth", auth, "", "", 403)
	event.ID = "late-online"
	event.Type = "online"
	requestWorker(t, "POST", srv.URL+"/internal/v1/streams/events", event, strings.Repeat("e", 32), "", 204)
	if state := queryID(t, pool, `SELECT state FROM teaching.live_captures WHERE id='`+id+`'`); state != "finalizing" {
		t.Fatal("late event resurrected capture")
	}
}

func captureDatabaseFixture(t *testing.T) (*pgxpool.Pool, string, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	lock, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = lock.Close(ctx) })
	if _, e = lock.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('cangjie_integration_test_fixture',0))`); e != nil {
		t.Fatal(e)
	}
	migrations, _ := filepath.Abs("../../database")
	if e = database.RunMigrations(ctx, dsn, migrations); e != nil {
		t.Fatal(e)
	}
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	if _, e = pool.Exec(ctx, `TRUNCATE teaching.academic_terms,teaching.classrooms,teaching.org_units CASCADE`); e != nil {
		t.Fatal(e)
	}
	_, e = identity.BootstrapAdmin(ctx, pool, identity.BootstrapRequest{SchoolCode: "C-SCHOOL", SchoolName: "Capture school", Username: "admin", DisplayName: "Admin", Password: integrationPassword})
	if e != nil {
		t.Fatal(e)
	}
	_, e = identity.BootstrapDevelopmentUsers(ctx, pool, identity.DevelopmentUsersRequest{SchoolCode: "C-SCHOOL", SchoolName: "Capture school", CollegeCode: "COL-C", CollegeName: "Capture college", AcademicUsername: "academic", AcademicDisplayName: "Academic", SupervisorUsername: "supervisor", SupervisorDisplayName: "Supervisor", TeacherUsername: "teacher", TeacherDisplayName: "Teacher", Password: integrationPassword})
	if e != nil {
		t.Fatal(e)
	}
	user := queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='teacher'`)
	org := queryID(t, pool, `SELECT id::text FROM teaching.org_units WHERE code='COL-C'`)
	term := queryID(t, pool, `INSERT INTO teaching.academic_terms(code,name,start_date,end_date) VALUES('CT','CT','2026-01-01','2027-12-31') RETURNING id::text`)
	course := queryID(t, pool, fmt.Sprintf(`INSERT INTO teaching.courses(org_unit_id,code,name) VALUES('%s','CC','CC') RETURNING id::text`, org))
	group := queryID(t, pool, fmt.Sprintf(`INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES('%s','CG','CG',2026) RETURNING id::text`, org))
	offering := queryID(t, pool, fmt.Sprintf(`INSERT INTO teaching.course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id) VALUES('%s','CO','%s','%s','%s','%s') RETURNING id::text`, org, term, course, user, group))
	lesson := queryID(t, pool, fmt.Sprintf(`INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by) VALUES('%s','Capture',now(),now()+interval '1 hour','%s') RETURNING id::text`, offering, user))
	source := queryID(t, pool, fmt.Sprintf(`INSERT INTO teaching.source_records(session_id,source_type,title,attribution,rights_status,allowed_uses,registered_by,verified_by,verified_at) VALUES('%s','self_recorded','Capture','test','verified','["playback"]','%s','%s',now()) RETURNING id::text`, lesson, user, user))

	return pool, lesson, source
}
