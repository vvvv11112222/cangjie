package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

func TestManagementDeletionAndPasswords(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	lock, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close(ctx) //nolint:errcheck
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
	_, err = identity.BootstrapAdmin(ctx, pool, identity.BootstrapRequest{SchoolCode: "SCHOOL", SchoolName: "School", Username: "admin", DisplayName: "Admin", Password: integrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	_, err = identity.BootstrapDevelopmentUsers(ctx, pool, identity.DevelopmentUsersRequest{SchoolCode: "SCHOOL", SchoolName: "School", CollegeCode: "COL-A", CollegeName: "A", AcademicUsername: "academic", AcademicDisplayName: "Academic", SupervisorUsername: "supervisor", SupervisorDisplayName: "Supervisor", TeacherUsername: "teacher", TeacherDisplayName: "Teacher", Password: integrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Identity: identity.NewService(pool, time.Hour), Academic: academic.NewService(pool), PublicOrigin: "http://frontend.test"}))
	defer server.Close()
	admin := newTestClient(t)
	csrf := loginTestUser(t, admin, server.URL, "admin")
	academicClient := newTestClient(t)
	academicCSRF := loginTestUser(t, academicClient, server.URL, "academic")
	teacher := newTestClient(t)
	teacherCSRF := loginTestUser(t, teacher, server.URL, "teacher")
	school := queryID(t, pool, `SELECT id::text FROM teaching.org_units WHERE code='SCHOOL'`)
	college := queryID(t, pool, `SELECT id::text FROM teaching.org_units WHERE code='COL-A'`)
	teacherID := queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='teacher'`)
	adminID := queryID(t, pool, `SELECT id::text FROM teaching.user_accounts WHERE username='admin'`)
	create := func(resource string, body map[string]any) string {
		return requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/"+resource, body, csrf, http.StatusCreated)["id"].(string)
	}
	remove := func(resource, id string, status int) {
		requestJSON(t, admin, http.MethodDelete, server.URL+"/api/v1/"+resource+"/"+id, nil, csrf, status)
	}
	// Existing references and the school root are protected; no cascading deletes.
	remove("org-units", college, http.StatusConflict)
	remove("org-units", school, http.StatusConflict)
	empty := create("org-units", map[string]any{"parent_id": school, "code": "EMPTY", "name": "Empty", "kind": "college"})
	requestJSON(t, academicClient, http.MethodDelete, server.URL+"/api/v1/org-units/"+empty, nil, academicCSRF, http.StatusForbidden)
	remove("org-units", empty, http.StatusNoContent)
	remove("org-units", empty, http.StatusNotFound)
	group := create("class-groups", map[string]any{"org_unit_id": college, "code": "GROUP", "name": "Group", "enrollment_year": 2026, "expected_size": 40})
	requestJSON(t, teacher, http.MethodDelete, server.URL+"/api/v1/class-groups/"+group, nil, teacherCSRF, http.StatusForbidden)
	requestJSON(t, admin, http.MethodDelete, server.URL+"/api/v1/class-groups/"+group, nil, "", http.StatusForbidden)
	course := create("courses", map[string]any{"org_unit_id": college, "code": "COURSE", "name": "Course", "description": ""})
	term := create("terms", map[string]any{"code": "TERM", "name": "Term", "start_date": "2026-09-01", "end_date": "2027-01-01"})
	room := create("classrooms", map[string]any{"code": "ROOM", "name": "Room", "capacity": 40})
	offering := create("offerings", map[string]any{"org_unit_id": college, "code": "OFFERING", "course_id": course, "class_group_id": group, "teacher_id": teacherID, "term_id": term, "status": "active"})
	schedule := create("schedules", map[string]any{"offering_id": offering, "classroom_id": room, "starts_at": "2026-10-26T08:00:00+08:00", "ends_at": "2026-10-26T09:40:00+08:00", "status": "active"})
	remove("class-groups", group, http.StatusConflict)
	remove("courses", course, http.StatusConflict)
	remove("offerings", offering, http.StatusConflict)
	remove("schedules", schedule, http.StatusNoContent)
	remove("offerings", offering, http.StatusNoContent)
	requestJSON(t, academicClient, http.MethodDelete, server.URL+"/api/v1/class-groups/"+group, nil, academicCSRF, http.StatusNoContent)
	remove("courses", course, http.StatusNoContent)
	remove("terms", term, http.StatusNoContent)
	remove("classrooms", room, http.StatusNoContent)
	// Academic staff cannot delete resources outside their college.
	other := create("org-units", map[string]any{"parent_id": school, "code": "OTHER", "name": "Other", "kind": "college"})
	otherGroup := create("class-groups", map[string]any{"org_unit_id": other, "code": "OTHER-GROUP", "name": "Other group", "enrollment_year": 2026, "expected_size": nil})
	requestJSON(t, academicClient, http.MethodDelete, server.URL+"/api/v1/class-groups/"+otherGroup, nil, academicCSRF, http.StatusForbidden)
	remove("class-groups", otherGroup, http.StatusNoContent)
	remove("org-units", other, http.StatusNoContent)
	// Incorrect current password and cross-scope resets cannot change credentials.
	for _, id := range []string{adminID, strings.ToUpper(adminID)} {
		requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/users/"+id+"/password", map[string]any{"new_password": "cannot-bypass-old-password-123"}, csrf, http.StatusBadRequest)
	}
	change := server.URL + "/api/v1/auth/password"
	requestJSON(t, teacher, http.MethodPost, change, map[string]any{"current_password": "wrong", "new_password": "new-test-password-123"}, teacherCSRF, http.StatusBadRequest)
	requestJSON(t, academicClient, http.MethodPost, server.URL+"/api/v1/users/"+adminID+"/password", map[string]any{"new_password": "new-test-password-123"}, academicCSRF, http.StatusNotFound)
	requestJSON(t, teacher, http.MethodPost, server.URL+"/api/v1/users/"+adminID+"/password", map[string]any{"new_password": "new-test-password-123"}, teacherCSRF, http.StatusForbidden)
	second := newTestClient(t)
	loginTestUser(t, second, server.URL, "teacher")
	requestJSON(t, teacher, http.MethodPost, change, map[string]any{"current_password": integrationPassword, "new_password": "new-test-password-123"}, teacherCSRF, http.StatusNoContent)
	requestJSON(t, teacher, http.MethodGet, server.URL+"/api/v1/auth/me", nil, "", http.StatusUnauthorized)
	requestJSON(t, second, http.MethodGet, server.URL+"/api/v1/auth/me", nil, "", http.StatusUnauthorized)
	fresh := newTestClient(t)
	token := getCSRF(t, fresh, server.URL)
	requestJSON(t, fresh, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{"username": "teacher", "password": integrationPassword}, token, http.StatusUnauthorized)
	logged := requestJSON(t, fresh, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]any{"username": "teacher", "password": "new-test-password-123"}, token, http.StatusOK)
	requestJSON(t, admin, http.MethodPost, server.URL+"/api/v1/users/"+teacherID+"/password", map[string]any{"new_password": "reset-test-password-456"}, csrf, http.StatusNoContent)
	requestJSON(t, fresh, http.MethodGet, server.URL+"/api/v1/auth/me", nil, logged["csrf_token"].(string), http.StatusUnauthorized)
	// Administrator self-service uses the same old-password check; no lockout of the
	// last administrator, and no password/hash is stored in the audit metadata.
	requestJSON(t, admin, http.MethodPost, change, map[string]any{"current_password": integrationPassword, "new_password": "admin-new-password-789"}, csrf, http.StatusNoContent)
	requestJSON(t, admin, http.MethodGet, server.URL+"/api/v1/auth/me", nil, "", http.StatusUnauthorized)
	var leaks int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM teaching.audit_logs WHERE metadata::text LIKE '%password-%'`).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatalf("credential audit leak: count=%d err=%v", leaks, err)
	}
}
