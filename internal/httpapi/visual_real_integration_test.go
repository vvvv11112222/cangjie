package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/classroom"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/identity"
	mediaservice "github.com/vvvv11112222/cangjie/internal/media"
	"github.com/vvvv11112222/cangjie/internal/storage"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in real model test: local authorized media and pinned weights are never
// downloaded by CI or checked into Git. Only a disposable database is accepted.
func TestRealVisualUploadIntegration(t *testing.T) {
	input := os.Getenv("WORKER_VISUAL_INPUT")
	if input == "" {
		t.Skip("WORKER_VISUAL_INPUT is not set; real models require explicit local media")
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	u, err := url.Parse(dsn)
	if err != nil || u.Path != "/cangjie_worker_integration" || os.Getenv("WORKER_INTEGRATION_ALLOW_DATABASE_RESET") != "1" {
		t.Fatal("disposable cangjie_worker_integration required")
	}
	python, root := os.Getenv("WORKER_INTEGRATION_PYTHON"), os.Getenv("WORKER_INTEGRATION_ROOT")
	if python == "" || root == "" {
		t.Fatal("Python and repository root required")
	}
	bridge := func(token string, args ...string) map[string]any {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, python, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "WORKER_TOKEN="+token, "PYTHONUTF8=1", "VISION_RUNTIME=cpu", "VISION_CV_THREADS=4", "VISION_ORT_THREADS=4")
		var diag bytes.Buffer
		cmd.Stderr = &diag
		raw, err := cmd.Output()
		if err != nil {
			t.Fatalf("bridge %v: %s", err, diag.String())
		}
		var v map[string]any
		if err = json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("bridge JSON: %v", err)
		}
		return v
	}
	info := bridge("", "-m", "workers", "runtime-info")
	model := info["visual_model"].(map[string]any)
	ctx := context.Background()
	lock, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close(context.Background())
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('cangjie_integration_test_fixture',0))`); err != nil {
		t.Fatal(err)
	}
	migrations, _ := filepath.Abs("../../database")
	if err = database.RunMigrations(ctx, dsn, migrations); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `TRUNCATE teaching.academic_terms,teaching.classrooms,teaching.org_units CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err = identity.BootstrapAdmin(ctx, pool, identity.BootstrapRequest{SchoolCode: "SCHOOL", SchoolName: "School", Username: "admin", DisplayName: "Admin", Password: integrationPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err = identity.BootstrapDevelopmentUsers(ctx, pool, identity.DevelopmentUsersRequest{SchoolCode: "SCHOOL", SchoolName: "School", CollegeCode: "COL-A", CollegeName: "College A", AcademicUsername: "academic", AcademicDisplayName: "Academic", SupervisorUsername: "supervisor", SupervisorDisplayName: "Supervisor", TeacherUsername: "teacher", TeacherDisplayName: "Teacher", Password: integrationPassword}); err != nil {
		t.Fatal(err)
	}
	college := queryID(t, pool, `SELECT id::text FROM teaching.org_units WHERE code='COL-A'`)
	teacherID := queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='teacher'`)
	term := queryID(t, pool, `INSERT INTO teaching.academic_terms(code,name,start_date,end_date) VALUES('V-T','Visual','2026-09-01','2027-01-01') RETURNING id::text`)
	course := queryID(t, pool, `INSERT INTO teaching.courses(org_unit_id,code,name) VALUES('`+college+`','V-C','Visual') RETURNING id::text`)
	group := queryID(t, pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES('`+college+`','V-G','Visual',2026) RETURNING id::text`)
	offering := queryID(t, pool, `INSERT INTO teaching.course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id) VALUES('`+college+`','V-O','`+term+`','`+course+`','`+teacherID+`','`+group+`') RETURNING id::text`)
	sessionID := queryID(t, pool, `INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by) VALUES('`+offering+`','Visual',now(),now()+interval '1 hour','`+teacherID+`') RETURNING id::text`)
	sourceID := queryID(t, pool, `INSERT INTO teaching.source_records(session_id,source_type,title,attribution,rights_status,allowed_uses,registered_by,verified_by,verified_at) VALUES('`+sessionID+`','self_recorded','Local authorized test','Local integration only; excluded from Git','verified','["playback","analysis"]','`+teacherID+`','`+teacherID+`',now()) RETURNING id::text`)
	tmp := t.TempDir()
	store, err := storage.NewLocal(filepath.Join(tmp, "media"))
	if err != nil {
		t.Fatal(err)
	}
	token := "synthetic-visual-worker-integration-credential"
	svc := analysis.NewService(pool, store, analysis.Config{VisionEnabled: true, VisionModelRevision: model["revision"].(string), Lease: time.Minute, ProbeTimeout: 3 * time.Minute, VideoTimeout: 3 * time.Minute, MediaJobAttempts: 2, ProcessorVersion: info["processor_version"].(string), FFmpegSHA256: info["ffmpeg_build_sha256"].(string), ASRDevice: "cpu", MaxKeyframes: 240, KeyframeIntervalMS: 30000, MaxMediaDurationMS: 7200000, MaxVideoHeight: 1080, MaxArtifactBytes: 10 << 20})
	server := httptest.NewServer(New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Identity: identity.NewService(pool, 8*time.Hour), Academic: academic.NewService(pool), Classroom: classroom.NewService(pool), Media: mediaservice.NewService(pool, store, 10<<20, 14*24*time.Hour), Analysis: svc, PublicOrigin: "http://frontend.test", WorkerToken: token, WorkerID: "worker-01", WorkerCapabilities: []string{"probe", "video_analysis"}, MaxArtifactBytes: 10 << 20, MaxUploadBytes: 10 << 20, UploadTimeout: time.Minute}))
	defer server.Close()
	client := newTestClient(t)
	csrf := loginTestUser(t, client, server.URL, "teacher")
	video, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	uploaded := requestMultipart(t, client, server.URL+"/api/v1/sessions/"+sessionID+"/media", sourceID, video, csrf, http.StatusCreated)
	run := requestWithHeaders(t, client, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/analysis-runs", map[string]any{"media_asset_id": uploaded["id"], "mode": "visual_only", "input_transcript_revision_id": nil, "config_profile": "vision-v1"}, csrf, map[string]string{"Idempotency-Key": "real-visual"}, http.StatusAccepted)
	id := run["id"].(string)
	for _, stage := range []string{"probe", "video_analysis"} {
		result := bridge(token, "-m", "workers.tools.check_real_go", "work", "--api-base", server.URL+"/internal/v1", "--run-id", id, "--stage", stage, "--scratch", filepath.Join(tmp, "scratch"), "--visual")
		t.Logf("real stage=%s wall_seconds=%v", stage, result["wall_seconds"])
	}
	finished := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+id, nil, "", http.StatusOK)
	if finished["status"] != "succeeded" || len(finished["jobs"].([]any)) != 2 || finished["report_id"] != nil {
		t.Fatal("visual run did not finish independently")
	}
	summary := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+id+"/visual", nil, "", http.StatusOK)
	visual := summary["visual"].(map[string]any)
	if _, ok := visual["persons"]; ok {
		t.Fatal("summary unbounded")
	}
	persons := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+id+"/visual/persons?limit=1", nil, "", http.StatusOK)
	list := persons["items"].([]any)
	if len(list) != 1 {
		t.Fatal("no real person detected")
	}
	track := list[0].(map[string]any)["track_id"].(string)
	observations := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+id+"/visual/persons/"+track+"?limit=1", nil, "", http.StatusOK)
	if len(observations["items"].([]any)) != 1 {
		t.Fatal("no person observation")
	}
	results := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+id+"/results", nil, "", http.StatusOK)
	frames := results["frames"].([]any)
	if len(frames) == 0 {
		t.Fatal("no frame evidence")
	}
	requestContent(t, client, server.URL+"/api/v1/media/"+frames[0].(map[string]any)["asset_id"].(string)+"/content", "", http.StatusOK, nil)
	if status := rawStatus(http.DefaultClient, http.MethodGet, server.URL+"/api/v1/analysis-runs/"+id+"/visual", nil, nil); status != http.StatusUnauthorized {
		t.Fatal("anonymous result readable")
	}
	var personsTotal, observationsTotal int
	if err := pool.QueryRow(ctx, `SELECT jsonb_array_length(metadata->'result'->'visual'->'persons'),(SELECT sum(jsonb_array_length(p->'observations')) FROM jsonb_array_elements(metadata->'result'->'visual'->'persons') p) FROM teaching.job_artifacts WHERE run_id=$1 AND artifact_key='__result/video_analysis'`, id).Scan(&personsTotal, &observationsTotal); err != nil {
		t.Fatal(err)
	}
	handPositive, headPositive := 0, 0
	for _, item := range visual["counts"].([]any) {
		c := item.(map[string]any)
		if n, ok := c["hand_raise_count"].(float64); ok {
			handPositive += int(n)
		}
		if n, ok := c["head_down_count"].(float64); ok {
			headPositive += int(n)
		}
	}
	t.Logf("real persons=%d observations=%d positive_hand_observations=%d positive_head_observations=%d", personsTotal, observationsTotal, handPositive, headPositive)
	t.Logf("real CPU upload/visual/person/frame complete: samples=%d events=%d frames=%d", len(visual["counts"].([]any)), len(results["events"].([]any)), len(frames))
}
