package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
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
)

const integrationPassword = "phase-one-test-password"

func TestPhaseOneAuthorizationFlow(t *testing.T) {
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
		fixtureLock.Close(ctx) //nolint:errcheck
		t.Fatal(err)
	}
	defer fixtureLock.Close(context.Background()) //nolint:errcheck
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "database"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RunMigrations(ctx, databaseURL, migrationDir); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `TRUNCATE teaching.academic_terms, teaching.classrooms, teaching.org_units CASCADE`); err != nil {
		t.Fatal(err)
	}

	_, err = identity.BootstrapAdmin(ctx, pool, identity.BootstrapRequest{SchoolCode: "SCHOOL", SchoolName: "School", Username: "admin", DisplayName: "Admin", Password: integrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	_, err = identity.BootstrapDevelopmentUsers(ctx, pool, identity.DevelopmentUsersRequest{SchoolCode: "SCHOOL", SchoolName: "School", CollegeCode: "COL-A", CollegeName: "College A", AcademicUsername: "academic", AcademicDisplayName: "Academic", SupervisorUsername: "supervisor", SupervisorDisplayName: "Supervisor", TeacherUsername: "teacher", TeacherDisplayName: "Teacher", Password: integrationPassword})
	if err != nil {
		t.Fatal(err)
	}

	handler := New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Identity: identity.NewService(pool, 8*time.Hour), Academic: academic.NewService(pool), Classroom: classroom.NewService(pool), PublicOrigin: "http://frontend.test"})
	server := httptest.NewServer(handler)
	defer server.Close()
	admin := newTestClient(t)
	adminCSRF := loginTestUser(t, admin, server.URL, "admin")
	rotated := requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{"username": "admin", "password": integrationPassword}, adminCSRF, http.StatusOK)
	if rotated["csrf_token"] == adminCSRF {
		t.Fatal("login did not rotate the authenticated session")
	}
	adminCSRF = rotated["csrf_token"].(string)
	var revoked int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM teaching.auth_sessions a JOIN teaching.user_accounts u ON u.id=a.user_id WHERE u.username='admin' AND a.revoked_at IS NOT NULL`).Scan(&revoked); err != nil || revoked != 1 {
		t.Fatalf("revoked admin sessions=%d err=%v", revoked, err)
	}
	adminID := queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='admin'`)
	requestJSON(t, admin, http.MethodPatch, server.URL+"/api/v1/users/"+adminID, map[string]any{"status": "disabled"}, adminCSRF, http.StatusConflict)
	adminRoleID := queryID(t, pool, `SELECT id::text FROM teaching.role_bindings WHERE user_id='`+adminID+`' AND role_code='sys_admin'`)
	requestJSON(t, admin, http.MethodDelete, server.URL+"/api/v1/users/"+adminID+"/role-bindings/"+adminRoleID, nil, adminCSRF, http.StatusConflict)
	requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/classrooms", map[string]any{"code": "MISSING-CAPACITY", "name": "Invalid"}, adminCSRF, http.StatusBadRequest)
	requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/org-units", map[string]any{"code": "MISSING-PARENT", "name": "Invalid", "kind": "college"}, adminCSRF, http.StatusBadRequest)

	otherCollege := requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/org-units", map[string]any{"parent_id": queryID(t, pool, `SELECT id::text FROM teaching.org_units WHERE code='SCHOOL'`), "code": "COL-B", "name": "College B", "kind": "college"}, adminCSRF, http.StatusCreated)["id"].(string)
	requestJSON(t, admin, http.MethodPatch, server.URL+"/api/v1/org-units/"+otherCollege, map[string]any{"name": "College B Updated"}, adminCSRF, http.StatusOK)
	otherCourse := requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/courses", map[string]any{"org_unit_id": otherCollege, "code": "OTHER-101", "name": "Other Course", "description": ""}, adminCSRF, http.StatusCreated)
	if otherCourse["id"] == "" {
		t.Fatal("missing other course id")
	}

	academicClient := newTestClient(t)
	academicCSRF := loginTestUser(t, academicClient, server.URL, "academic")
	collegeA := queryID(t, pool, `SELECT id::text FROM teaching.org_units WHERE code='COL-A'`)
	academicID := queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='academic'`)
	requestJSON(t, academicClient, http.MethodPatch, server.URL+"/api/v1/users/"+academicID, map[string]any{"display_name": "Changed"}, academicCSRF, http.StatusForbidden)
	requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/courses", map[string]any{"org_unit_id": collegeA, "code": "BAD-CSRF", "name": "Denied", "description": ""}, "wrong-token", http.StatusForbidden)
	requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/courses", map[string]any{"org_unit_id": otherCollege, "code": "DENIED", "name": "Denied", "description": ""}, academicCSRF, http.StatusForbidden)
	course := requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/courses", map[string]any{"org_unit_id": collegeA, "code": "OWN-101", "name": "Own Course", "description": ""}, academicCSRF, http.StatusCreated)
	requestJSON(t, academicClient, http.MethodPatch, server.URL+"/api/v1/courses/"+course["id"].(string), map[string]any{"description": "Updated"}, academicCSRF, http.StatusOK)
	group := requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/class-groups", map[string]any{"org_unit_id": collegeA, "code": "CLASS-1", "name": "Class 1", "enrollment_year": 2026, "expected_size": 30}, academicCSRF, http.StatusCreated)
	requestJSON(t, academicClient, http.MethodPatch, server.URL+"/api/v1/class-groups/"+group["id"].(string), map[string]any{"expected_size": nil}, academicCSRF, http.StatusOK)
	teacherID := queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='teacher'`)
	requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/users/"+teacherID+"/role-bindings", map[string]any{"role_code": "sys_admin", "scope_org_id": nil}, academicCSRF, http.StatusForbidden)
	requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/users/"+teacherID+"/role-bindings", map[string]any{"role_code": "teacher"}, adminCSRF, http.StatusBadRequest)
	createdUser := requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/users", map[string]any{"org_unit_id": collegeA, "username": "reviewer", "display_name": "Reviewer", "status": "active", "initial_password": integrationPassword}, academicCSRF, http.StatusCreated)
	createdRole := requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/users/"+createdUser["id"].(string)+"/role-bindings", map[string]any{"role_code": "supervisor", "scope_org_id": collegeA}, academicCSRF, http.StatusCreated)
	requestJSON(t, academicClient, http.MethodDelete, server.URL+"/api/v1/users/"+createdUser["id"].(string)+"/role-bindings/"+createdRole["id"].(string), nil, academicCSRF, http.StatusNoContent)

	term := requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/terms", map[string]any{"code": "2026-FALL", "name": "Fall 2026", "start_date": "2026-09-01", "end_date": "2027-01-20"}, adminCSRF, http.StatusCreated)
	requestJSON(t, admin, http.MethodPatch, server.URL+"/api/v1/terms/"+term["id"].(string), map[string]any{"name": "Fall Term 2026"}, adminCSRF, http.StatusOK)
	classroom := requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/classrooms", map[string]any{"code": "ROOM-1", "name": "Room 1", "capacity": nil}, adminCSRF, http.StatusCreated)
	requestJSON(t, admin, http.MethodPatch, server.URL+"/api/v1/classrooms/"+classroom["id"].(string), map[string]any{"capacity": 40}, adminCSRF, http.StatusOK)
	offering := requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/offerings", map[string]any{"org_unit_id": collegeA, "term_id": term["id"], "course_id": course["id"], "teacher_id": teacherID, "class_group_id": group["id"], "code": "OWN-101-1", "status": "active"}, academicCSRF, http.StatusCreated)

	teacherClient := newTestClient(t)
	teacherCSRF := loginTestUser(t, teacherClient, server.URL, "teacher")
	secondRoom := requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/classrooms", map[string]any{"code": "ROOM-2", "name": "Room 2", "capacity": 40}, adminCSRF, http.StatusCreated)
	schedule := requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/schedules", map[string]any{"offering_id": offering["id"], "classroom_id": classroom["id"], "starts_at": "2026-10-10T09:00:00+08:00", "ends_at": "2026-10-10T10:00:00+08:00", "status": "active"}, academicCSRF, http.StatusCreated)
	requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/schedules", map[string]any{"offering_id": offering["id"], "classroom_id": classroom["id"], "starts_at": "2026-10-10T10:00:00+08:00", "ends_at": "2026-10-10T11:00:00+08:00", "status": "active"}, academicCSRF, http.StatusCreated)
	atPage := requestJSON(t, teacherClient, http.MethodGet, server.URL+"/api/v1/schedules?teacher_id="+teacherID+"&at=2026-10-10T09%3A30%3A00%2B08%3A00", nil, "", http.StatusOK)
	if got := atPage["items"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != schedule["id"] {
		t.Fatalf("schedule point query=%#v", got)
	}
	requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/schedules", map[string]any{"offering_id": offering["id"], "classroom_id": secondRoom["id"], "starts_at": "2026-10-10T09:30:00+08:00", "ends_at": "2026-10-10T10:30:00+08:00", "status": "active"}, academicCSRF, http.StatusConflict)
	requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/schedules/import", map[string]any{"rows": []any{
		map[string]any{"offering_id": offering["id"], "classroom_id": classroom["id"], "starts_at": "2026-10-10T11:00:00+08:00", "ends_at": "2026-10-10T12:00:00+08:00"},
		map[string]any{"offering_id": offering["id"], "classroom_id": secondRoom["id"], "starts_at": "2026-10-10T11:30:00+08:00", "ends_at": "2026-10-10T12:30:00+08:00"},
	}}, academicCSRF, http.StatusConflict)
	var imported int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM teaching.schedule_entries WHERE starts_at='2026-10-10 11:00+08'`).Scan(&imported); err != nil || imported != 0 {
		t.Fatalf("failed schedule import left %d rows: %v", imported, err)
	}
	session := requestJSON(t, teacherClient, http.MethodPost, server.URL+"/api/v1/sessions", map[string]any{"offering_id": offering["id"], "schedule_entry_id": schedule["id"], "title": "Lesson 1", "planned_start_at": "2026-10-10T09:00:00+08:00", "planned_end_at": "2026-10-10T10:00:00+08:00", "is_demo": false}, teacherCSRF, http.StatusCreated)
	requestJSON(t, teacherClient, http.MethodPost, server.URL+"/api/v1/sessions", map[string]any{"offering_id": offering["id"], "schedule_entry_id": schedule["id"], "title": "Wrong time", "planned_start_at": "2026-10-10T09:01:00+08:00", "planned_end_at": "2026-10-10T10:00:00+08:00", "is_demo": false}, teacherCSRF, http.StatusBadRequest)
	requestJSON(t, academicClient, http.MethodPatch, server.URL+"/api/v1/schedules/"+schedule["id"].(string), map[string]any{"ends_at": "2026-10-10T09:59:00+08:00"}, academicCSRF, http.StatusConflict)
	visibleSessions := requestJSON(t, teacherClient, http.MethodGet, server.URL+"/api/v1/sessions?enrollment_year=2026&teacher_id="+teacherID, nil, "", http.StatusOK)
	if got := visibleSessions["items"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != session["id"] {
		t.Fatalf("teacher session directory=%#v", got)
	}
	requestJSON(t, academicClient, http.MethodPatch, server.URL+"/api/v1/sessions/"+session["id"].(string), map[string]any{"status": "archived"}, academicCSRF, http.StatusOK)
	if got := requestJSON(t, teacherClient, http.MethodGet, server.URL+"/api/v1/sessions", nil, "", http.StatusOK)["items"].([]any); len(got) != 0 {
		t.Fatalf("default directory includes archived=%#v", got)
	}
	if got := requestJSON(t, teacherClient, http.MethodGet, server.URL+"/api/v1/sessions?status=archived", nil, "", http.StatusOK)["items"].([]any); len(got) != 1 {
		t.Fatalf("archived directory=%#v", got)
	}
	requestJSON(t, academicClient, http.MethodPatch, server.URL+"/api/v1/offerings/"+offering["id"].(string), map[string]any{"status": "archived"}, academicCSRF, http.StatusOK)
	page := requestJSON(t, teacherClient, http.MethodGet, server.URL+"/api/v1/courses", nil, "", http.StatusOK)
	items := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != course["id"] {
		t.Fatalf("teacher course scope = %#v", items)
	}
	requestJSON(t, teacherClient, http.MethodGet, server.URL+"/api/v1/courses/"+otherCourse["id"].(string), nil, "", http.StatusNotFound)
	teacherRoleID := queryID(t, pool, `SELECT id::text FROM teaching.role_bindings WHERE user_id='`+teacherID+`' AND role_code='teacher'`)
	requestJSON(t, admin, http.MethodDelete, server.URL+"/api/v1/users/"+teacherID+"/role-bindings/"+teacherRoleID, nil, adminCSRF, http.StatusNoContent)
	for _, resource := range []string{"courses", "class-groups", "offerings"} {
		page := requestJSON(t, teacherClient, http.MethodGet, server.URL+"/api/v1/"+resource, nil, "", http.StatusOK)
		if got := page["items"].([]any); len(got) != 0 {
			t.Fatalf("revoked teacher %s scope = %#v", resource, got)
		}
	}

	bad := newTestClient(t)
	csrf := getCSRF(t, bad, server.URL)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/login", bytes.NewReader(mustJSON(map[string]any{"username": "teacher", "password": integrationPassword})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.test")
	req.Header.Set("X-CSRF-Token", csrf)
	response, err := bad.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin login status=%d", response.StatusCode)
	}

	requestJSON(t, admin, http.MethodPatch, server.URL+"/api/v1/users/"+teacherID, map[string]any{"status": "disabled"}, adminCSRF, http.StatusOK)
	requestJSON(t, teacherClient, http.MethodGet, server.URL+"/api/v1/auth/me", nil, "", http.StatusUnauthorized)

	supervisorClient := newTestClient(t)
	supervisorCSRF := loginTestUser(t, supervisorClient, server.URL, "supervisor")
	requestJSON(t, supervisorClient, http.MethodPost, server.URL+"/api/v1/auth/logout", map[string]any{}, supervisorCSRF, http.StatusNoContent)
	requestJSON(t, supervisorClient, http.MethodGet, server.URL+"/api/v1/auth/me", nil, "", http.StatusUnauthorized)
}

func newTestClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}
func getCSRF(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	data := requestJSON(t, c, http.MethodGet, base+"/api/v1/auth/csrf", nil, "", http.StatusOK)
	return data["csrf_token"].(string)
}
func loginTestUser(t *testing.T, c *http.Client, base, username string) string {
	t.Helper()
	csrf := getCSRF(t, c, base)
	data := requestJSON(t, c, http.MethodPost, base+"/api/v1/auth/login", map[string]any{"username": username, "password": integrationPassword}, csrf, http.StatusOK)
	return data["csrf_token"].(string)
}
func requestJSON(t *testing.T, c *http.Client, method, target string, body any, csrf string, want int) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(mustJSON(body))
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", "http://frontend.test")
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, target, resp.StatusCode, want, raw)
	}
	if len(raw) == 0 {
		return map[string]any{}
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if data, ok := envelope["data"].(map[string]any); ok {
		return data
	}
	return envelope
}
func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
func queryID(t *testing.T, pool *pgxpool.Pool, sql string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), sql).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
