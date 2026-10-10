package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/classroom"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/identity"
	mediaservice "github.com/vvvv11112222/cangjie/internal/media"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

func TestPhaseThreeSourceUploadPlaybackAndAuthorization(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	fixtureLock, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureLock.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('cangjie_integration_test_fixture', 0))`); err != nil {
		t.Fatal(err)
	}
	defer fixtureLock.Close(context.Background()) //nolint:errcheck
	migrationDir, _ := filepath.Abs(filepath.Join("..", "..", "database"))
	if err := database.RunMigrations(ctx, databaseURL, migrationDir); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `TRUNCATE teaching.academic_terms,teaching.classrooms,teaching.org_units CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.BootstrapAdmin(ctx, pool, identity.BootstrapRequest{SchoolCode: "SCHOOL", SchoolName: "School", Username: "admin", DisplayName: "Admin", Password: integrationPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.BootstrapDevelopmentUsers(ctx, pool, identity.DevelopmentUsersRequest{SchoolCode: "SCHOOL", SchoolName: "School", CollegeCode: "COL-A", CollegeName: "College A", AcademicUsername: "academic", AcademicDisplayName: "Academic", SupervisorUsername: "supervisor", SupervisorDisplayName: "Supervisor", TeacherUsername: "teacher", TeacherDisplayName: "Teacher", Password: integrationPassword}); err != nil {
		t.Fatal(err)
	}
	college := queryID(t, pool, `SELECT id::text FROM teaching.org_units WHERE code='COL-A'`)
	teacherID := queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='teacher'`)
	term := queryID(t, pool, `INSERT INTO teaching.academic_terms(code,name,start_date,end_date) VALUES('P3-TERM','P3 Term','2026-09-01','2027-01-01') RETURNING id::text`)
	course := queryID(t, pool, `INSERT INTO teaching.courses(org_unit_id,code,name) VALUES('`+college+`','P3-COURSE','P3 Course') RETURNING id::text`)
	group := queryID(t, pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES('`+college+`','P3-GROUP','P3 Group',2026) RETURNING id::text`)
	offering := queryID(t, pool, `INSERT INTO teaching.course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id) VALUES('`+college+`','P3-OFFERING','`+term+`','`+course+`','`+teacherID+`','`+group+`') RETURNING id::text`)
	sessionID := queryID(t, pool, `INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by) VALUES('`+offering+`','P3 Lesson',now(),now()+interval '1 hour','`+teacherID+`') RETURNING id::text`)

	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mediaService := mediaservice.NewService(pool, store, 1<<20, 14*24*time.Hour)
	handler := New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Identity: identity.NewService(pool, 8*time.Hour), Academic: academic.NewService(pool), Classroom: classroom.NewService(pool), Media: mediaService, PublicOrigin: "http://frontend.test", MaxUploadBytes: 1 << 20, UploadTimeout: 500 * time.Millisecond})
	server := httptest.NewServer(handler)
	defer server.Close()
	teacher, academic, supervisor := newTestClient(t), newTestClient(t), newTestClient(t)
	teacherCSRF := loginTestUser(t, teacher, server.URL, "teacher")
	academicCSRF := loginTestUser(t, academic, server.URL, "academic")
	_ = loginTestUser(t, supervisor, server.URL, "supervisor")

	source := requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/sources", map[string]any{"session_id": sessionID, "source_type": "self_recorded", "title": "Authorized recording", "attribution": "Teaching team", "source_url": nil, "authorization_note": "Internal class recording", "requested_uses": []string{"playback", "analysis"}, "external_processing_requested": false}, teacherCSRF, http.StatusCreated)
	sourceID := source["id"].(string)
	requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/sources/"+sourceID+"/verify", map[string]any{"rights_status": "verified", "allowed_uses": []string{"playback"}, "external_processing_allowed": false, "reason": "checked"}, teacherCSRF, http.StatusForbidden)
	requestJSON(t, academic, http.MethodPost, server.URL+"/api/v1/sources/"+sourceID+"/verify", map[string]any{"rights_status": "verified", "allowed_uses": []string{"playback"}, "external_processing_allowed": false, "reason": "checked"}, academicCSRF, http.StatusOK)

	slowBody, slowWriter := io.Pipe()
	slowMultipart := multipart.NewWriter(slowWriter)
	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	go func() {
		defer slowWriter.Close() //nolint:errcheck
		close(slowStarted)
		_ = slowMultipart.WriteField("source_record_id", sourceID)
		part, createErr := slowMultipart.CreateFormFile("file", "slow.mp4")
		if createErr == nil {
			_, _ = part.Write([]byte{0, 0, 0, 20, 'f', 't', 'y', 'p'})
		}
		<-slowRelease
		_ = slowMultipart.Close()
	}()
	<-slowStarted
	slowRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/media", slowBody)
	slowRequest.Header.Set("Content-Type", slowMultipart.FormDataContentType())
	slowRequest.Header.Set("Origin", "http://frontend.test")
	slowRequest.Header.Set("X-CSRF-Token", teacherCSRF)
	slowClient := *teacher
	slowClient.Timeout = 5 * time.Second
	slowResponse, slowErr := slowClient.Do(slowRequest)
	close(slowRelease)
	if slowErr != nil {
		t.Fatalf("slow upload did not receive a timeout response: %v", slowErr)
	}
	slowRaw, _ := io.ReadAll(slowResponse.Body)
	slowResponse.Body.Close()
	if slowResponse.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("slow upload status=%d body=%s", slowResponse.StatusCode, slowRaw)
	}

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	serverURL.Path = "/api/v1/sessions/" + sessionID + "/media"
	cookies := teacher.Jar.Cookies(serverURL)
	if len(cookies) == 0 {
		t.Fatal("authenticated client has no session cookie")
	}
	const boundary = "cangjie-stalled-upload"
	partialBody := fmt.Sprintf("--%s\r\nContent-Disposition: form-data; name=\"source_record_id\"\r\n\r\n%s\r\n--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"stalled.mp4\"\r\nContent-Type: video/mp4\r\n\r\n", boundary, sourceID, boundary)
	connection, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err = connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	cookieHeader := ""
	for index, cookie := range cookies {
		if index > 0 {
			cookieHeader += "; "
		}
		cookieHeader += cookie.String()
	}
	requestHead := fmt.Sprintf("POST /api/v1/sessions/%s/media HTTP/1.1\r\nHost: %s\r\nContent-Type: multipart/form-data; boundary=%s\r\nContent-Length: %d\r\nOrigin: http://frontend.test\r\nX-CSRF-Token: %s\r\nCookie: %s\r\n\r\n", sessionID, serverURL.Host, boundary, len(partialBody)+1024, teacherCSRF, cookieHeader)
	startedAt := time.Now()
	if _, err = io.WriteString(connection, requestHead+partialBody); err != nil {
		t.Fatal(err)
	}
	rawRequest := &http.Request{Method: http.MethodPost}
	stalledResponse, err := http.ReadResponse(bufio.NewReader(connection), rawRequest)
	if err != nil {
		t.Fatalf("stalled Content-Length upload did not receive a response: %v", err)
	}
	stalledResponse.Body.Close()
	if stalledResponse.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("stalled Content-Length upload status=%d", stalledResponse.StatusCode)
	}
	if elapsed := time.Since(startedAt); elapsed >= 1500*time.Millisecond {
		t.Fatalf("stalled Content-Length upload response took %v", elapsed)
	}

	video := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'a', 'v', 'c', '1'}
	requestMultipart(t, academic, server.URL+"/api/v1/sessions/"+sessionID+"/media", sourceID, video, academicCSRF, http.StatusForbidden)
	requestMultipart(t, teacher, server.URL+"/api/v1/sessions/"+sessionID+"/media", sourceID, nil, teacherCSRF, http.StatusUnsupportedMediaType)
	requestMultipart(t, teacher, server.URL+"/api/v1/sessions/"+sessionID+"/media", sourceID, []byte("not an mp4"), teacherCSRF, http.StatusUnsupportedMediaType)
	tooLarge := append(append([]byte(nil), video...), make([]byte, 1<<20)...)
	requestMultipart(t, teacher, server.URL+"/api/v1/sessions/"+sessionID+"/media", sourceID, tooLarge, teacherCSRF, http.StatusRequestEntityTooLarge)
	uploaded := requestMultipart(t, teacher, server.URL+"/api/v1/sessions/"+sessionID+"/media", sourceID, video, teacherCSRF, http.StatusCreated)
	mediaID := uploaded["id"].(string)
	if got := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/sessions/"+sessionID+"/sources", nil, "", http.StatusOK)["items"].([]any); len(got) != 1 {
		t.Fatalf("source list=%#v", got)
	}
	if got := requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/sessions/"+sessionID+"/media", nil, "", http.StatusOK)["items"].([]any); len(got) != 1 {
		t.Fatalf("media list=%#v", got)
	}
	requestContent(t, teacher, server.URL+"/api/v1/media/"+mediaID+"/content", "", http.StatusConflict, nil)
	if _, err := pool.Exec(ctx, `UPDATE teaching.media_assets SET status='ready',duration_ms=1000,playback_asset_id=id WHERE id=$1`, mediaID); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, academic, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/primary-media", map[string]any{"media_asset_id": mediaID}, academicCSRF, http.StatusForbidden)
	selected := requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/sessions/"+sessionID+"/primary-media", map[string]any{"media_asset_id": mediaID}, teacherCSRF, http.StatusOK)
	if selected["primary_media_asset_id"] != mediaID {
		t.Fatalf("primary media=%v", selected["primary_media_asset_id"])
	}
	metadata := requestJSON(t, supervisor, http.MethodGet, server.URL+"/api/v1/media/"+mediaID, nil, "", http.StatusOK)
	if metadata["playback_url"] != "/api/v1/media/"+mediaID+"/content" {
		t.Fatalf("playback url=%v", metadata["playback_url"])
	}
	requestContent(t, supervisor, server.URL+"/api/v1/media/"+mediaID+"/content", "bytes=4-7", http.StatusPartialContent, []byte("ftyp"))
	requestContent(t, teacher, server.URL+"/api/v1/media/"+mediaID+"/content", "bytes=1-2,4-5", http.StatusRequestedRangeNotSatisfiable, nil)
	requestContent(t, academic, server.URL+"/api/v1/media/"+mediaID+"/content", "", http.StatusNotFound, nil)
	requestJSON(t, academic, http.MethodPost, server.URL+"/api/v1/sources/"+sourceID+"/verify", map[string]any{"rights_status": "rejected", "allowed_uses": []string{}, "external_processing_allowed": false, "reason": "revoked"}, academicCSRF, http.StatusOK)
	requestContent(t, teacher, server.URL+"/api/v1/media/"+mediaID+"/content", "", http.StatusForbidden, nil)
	requestJSON(t, academic, http.MethodPost, server.URL+"/api/v1/sources/"+sourceID+"/verify", map[string]any{"rights_status": "verified", "allowed_uses": []string{"playback"}, "external_processing_allowed": false, "reason": "renewed"}, academicCSRF, http.StatusOK)
	if _, err := pool.Exec(ctx, `UPDATE teaching.media_assets SET expires_at=now()-interval '1 second' WHERE id=$1`, mediaID); err != nil {
		t.Fatal(err)
	}
	requestContent(t, teacher, server.URL+"/api/v1/media/"+mediaID+"/content", "", http.StatusGone, nil)
}

func requestMultipart(t *testing.T, client *http.Client, target, sourceID string, content []byte, csrf string, want int) map[string]any {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("source_record_id", sourceID)
	part, err := w.CreateFormFile("file", "lesson.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, target, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Origin", "http://frontend.test")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("multipart status=%d want=%d body=%s", resp.StatusCode, want, raw)
	}
	var envelope map[string]any
	_ = json.Unmarshal(raw, &envelope)
	if data, ok := envelope["data"].(map[string]any); ok {
		return data
	}
	return envelope
}

func requestContent(t *testing.T, client *http.Client, target, byteRange string, want int, expected []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, target, nil)
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("content status=%d want=%d body=%s", resp.StatusCode, want, body)
	}
	if expected != nil && !bytes.Equal(body, expected) {
		t.Fatalf("content=%q want=%q", body, expected)
	}
}
