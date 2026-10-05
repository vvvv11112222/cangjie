package academic

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/optional"
)

type reviewFixture struct {
	pool                         *pgxpool.Pool
	school, collegeA, collegeB   string
	term, room                   string
	courseA, courseB             string
	groupA, groupB               string
	teacherA, teacherB, teacherC string
	offeringB                    string
}

func newReviewFixture(t *testing.T) reviewFixture {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
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
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE teaching.academic_terms, teaching.classrooms, teaching.org_units CASCADE`); err != nil {
		t.Fatal(err)
	}
	f := reviewFixture{pool: pool}
	f.school = scanID(t, pool, `INSERT INTO teaching.org_units(code,name,kind) VALUES('REVIEW-SCHOOL','School','school') RETURNING id::text`)
	f.collegeA = scanID(t, pool, `INSERT INTO teaching.org_units(parent_id,code,name,kind) VALUES($1,'REVIEW-A','College A','college') RETURNING id::text`, f.school)
	f.collegeB = scanID(t, pool, `INSERT INTO teaching.org_units(parent_id,code,name,kind) VALUES($1,'REVIEW-B','College B','college') RETURNING id::text`, f.school)
	f.term = scanID(t, pool, `INSERT INTO teaching.academic_terms(code,name,start_date,end_date) VALUES('REVIEW-TERM','Term','2026-09-01','2027-01-31') RETURNING id::text`)
	f.room = scanID(t, pool, `INSERT INTO teaching.classrooms(code,name) VALUES('REVIEW-ROOM','Room') RETURNING id::text`)
	f.courseA = scanID(t, pool, `INSERT INTO teaching.courses(org_unit_id,code,name) VALUES($1,'REVIEW-COURSE-A','Course A') RETURNING id::text`, f.collegeA)
	f.courseB = scanID(t, pool, `INSERT INTO teaching.courses(org_unit_id,code,name) VALUES($1,'REVIEW-COURSE-B','Course B') RETURNING id::text`, f.collegeB)
	f.groupA = scanID(t, pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES($1,'REVIEW-GROUP-A','Group A',2026) RETURNING id::text`, f.collegeA)
	f.groupB = scanID(t, pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES($1,'REVIEW-GROUP-B','Group B',2026) RETURNING id::text`, f.collegeB)
	f.teacherA = createTeacher(t, pool, f.collegeA, "review-teacher-a")
	f.teacherB = createTeacher(t, pool, f.collegeB, "review-teacher-b")
	f.teacherC = createTeacher(t, pool, f.collegeB, "review-teacher-c")
	f.offeringB = scanID(t, pool, `INSERT INTO teaching.course_offerings(org_unit_id,term_id,course_id,teacher_id,class_group_id,code) VALUES($1,$2,$3,$4,$5,'REVIEW-OFFERING-B') RETURNING id::text`, f.collegeB, f.term, f.courseB, f.teacherB, f.groupB)
	return f
}

func TestPatchResourcesRequiresWriteAccessToOriginalAndTarget(t *testing.T) {
	f := newReviewFixture(t)
	svc := NewService(f.pool)
	p := identity.Principal{UserID: f.teacherA, Roles: []identity.RoleBinding{
		{RoleCode: "academic_admin", ScopeOrgID: &f.collegeA},
		{RoleCode: "supervisor", ScopeOrgID: &f.collegeB},
	}}
	_, err := svc.PatchCourse(context.Background(), p, f.courseB, PatchCourse{OrgUnitID: &f.collegeA})
	assertAppError(t, err, http.StatusForbidden, "FORBIDDEN")
	_, err = svc.PatchClassGroup(context.Background(), p, f.groupB, PatchClassGroup{OrgUnitID: &f.collegeA})
	assertAppError(t, err, http.StatusForbidden, "FORBIDDEN")
	_, err = svc.PatchOffering(context.Background(), p, f.offeringB, PatchOffering{OrgUnitID: &f.collegeA, CourseID: &f.courseA, TeacherID: &f.teacherA, ClassGroupID: &f.groupA})
	assertAppError(t, err, http.StatusForbidden, "FORBIDDEN")
}

func TestTeacherListsRequireCurrentTeacherRole(t *testing.T) {
	f := newReviewFixture(t)
	svc := NewService(f.pool)
	p := identity.Principal{UserID: f.teacherB}
	if courses, _, err := svc.ListCourses(context.Background(), p, "", 100); err != nil || len(courses) != 0 {
		t.Fatalf("courses=%v err=%v", courses, err)
	}
	if groups, _, err := svc.ListClassGroups(context.Background(), p, "", 100); err != nil || len(groups) != 0 {
		t.Fatalf("groups=%v err=%v", groups, err)
	}
	if offerings, _, err := svc.ListOfferings(context.Background(), p, "", 100); err != nil || len(offerings) != 0 {
		t.Fatalf("offerings=%v err=%v", offerings, err)
	}
}

func TestOrganizationMoveChecksReferencedSubtree(t *testing.T) {
	f := newReviewFixture(t)
	collegeWithChildRefs := scanID(t, f.pool, `INSERT INTO teaching.org_units(parent_id,code,name,kind) VALUES($1,'REVIEW-C','College C','college') RETURNING id::text`, f.school)
	department := scanID(t, f.pool, `INSERT INTO teaching.org_units(parent_id,code,name,kind) VALUES($1,'REVIEW-DEPT','Department','department') RETURNING id::text`, collegeWithChildRefs)
	departmentCourse := scanID(t, f.pool, `INSERT INTO teaching.courses(org_unit_id,code,name) VALUES($1,'REVIEW-DEPT-COURSE','Department Course') RETURNING id::text`, department)
	departmentGroup := scanID(t, f.pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES($1,'REVIEW-DEPT-GROUP','Department Group',2026) RETURNING id::text`, department)
	departmentTeacher := createTeacher(t, f.pool, department, "review-dept-teacher")
	_ = scanID(t, f.pool, `INSERT INTO teaching.course_offerings(org_unit_id,term_id,course_id,teacher_id,class_group_id,code) VALUES($1,$2,$3,$4,$5,'REVIEW-DEPT-OFFERING') RETURNING id::text`, department, f.term, departmentCourse, departmentTeacher, departmentGroup)
	svc := NewService(f.pool)
	newKind := "department"
	_, err := svc.PatchOrgUnit(context.Background(), identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}, collegeWithChildRefs, PatchOrgUnit{ParentID: optional.Value[string]{Set: true, Value: &f.collegeB}, Kind: &newKind})
	assertAppError(t, err, http.StatusConflict, "INVALID_STATE")

	collegeWithChild := scanID(t, f.pool, `INSERT INTO teaching.org_units(parent_id,code,name,kind) VALUES($1,'REVIEW-D','College D','college') RETURNING id::text`, f.school)
	_ = scanID(t, f.pool, `INSERT INTO teaching.org_units(parent_id,code,name,kind) VALUES($1,'REVIEW-EMPTY-DEPT','Empty Department','department') RETURNING id::text`, collegeWithChild)
	_, err = svc.PatchOrgUnit(context.Background(), identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}, collegeWithChild, PatchOrgUnit{ParentID: optional.Value[string]{Set: true, Value: &f.collegeB}, Kind: &newKind})
	assertAppError(t, err, http.StatusConflict, "INVALID_STATE")
}

func TestConcurrentCourseMoveAndOfferingCreationRemainConsistent(t *testing.T) {
	f := newReviewFixture(t)
	svc := NewService(f.pool)
	admin := identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}
	tracingCourse := scanID(t, f.pool, `INSERT INTO teaching.courses(org_unit_id,code,name) VALUES($1,'REVIEW-RACE-COURSE','Race Course') RETURNING id::text`, f.collegeB)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() {
		defer wg.Done()
		<-start
		_, err := svc.PatchCourse(context.Background(), admin, tracingCourse, PatchCourse{OrgUnitID: &f.collegeA})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := svc.CreateOffering(context.Background(), admin, CreateOffering{OrgUnitID: f.collegeB, TermID: f.term, CourseID: tracingCourse, TeacherID: f.teacherB, ClassGroupID: f.groupB, Code: "REVIEW-RACE-OFFERING", Status: "active"})
		errs <- err
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			var appErr *apperror.Error
			if !errors.As(err, &appErr) || appErr.Status != http.StatusConflict && appErr.Status != http.StatusBadRequest {
				t.Fatalf("unexpected race result: %v", err)
			}
		}
	}
	var inconsistent bool
	if err := f.pool.QueryRow(context.Background(), `SELECT EXISTS(
		SELECT 1 FROM teaching.course_offerings o JOIN teaching.courses c ON c.id=o.course_id
		WHERE o.course_id=$1 AND teaching.college_of(o.org_unit_id)<>teaching.college_of(c.org_unit_id)
	)`, tracingCourse).Scan(&inconsistent); err != nil || inconsistent {
		t.Fatalf("inconsistent offering=%t err=%v", inconsistent, err)
	}
}

func TestConcurrentClassMoveAndOfferingCreationRemainConsistent(t *testing.T) {
	f := newReviewFixture(t)
	svc := NewService(f.pool)
	admin := identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}
	tracingGroup := scanID(t, f.pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES($1,'REVIEW-RACE-GROUP','Race Group',2026) RETURNING id::text`, f.collegeB)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() {
		defer wg.Done()
		<-start
		_, err := svc.PatchClassGroup(context.Background(), admin, tracingGroup, PatchClassGroup{OrgUnitID: &f.collegeA})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := svc.CreateOffering(context.Background(), admin, CreateOffering{OrgUnitID: f.collegeB, TermID: f.term, CourseID: f.courseB, TeacherID: f.teacherB, ClassGroupID: tracingGroup, Code: "REVIEW-RACE-CLASS-OFFERING", Status: "active"})
		errs <- err
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			var appErr *apperror.Error
			if !errors.As(err, &appErr) || appErr.Status != http.StatusConflict && appErr.Status != http.StatusBadRequest {
				t.Fatalf("unexpected race result: %v", err)
			}
		}
	}
	var inconsistent bool
	if err := f.pool.QueryRow(context.Background(), `SELECT EXISTS(
		SELECT 1 FROM teaching.course_offerings o JOIN teaching.class_groups g ON g.id=o.class_group_id
		WHERE o.class_group_id=$1 AND teaching.college_of(o.org_unit_id)<>teaching.college_of(g.org_unit_id)
	)`, tracingGroup).Scan(&inconsistent); err != nil || inconsistent {
		t.Fatalf("inconsistent offering=%t err=%v", inconsistent, err)
	}
}

func TestPatchOfferingSynchronizesSchedulesAndRollsBackConflicts(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	svc := NewService(f.pool)
	admin := identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}
	scheduleID := scanID(t, f.pool, `INSERT INTO teaching.schedule_entries(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at) VALUES($1,$2,$3,$4,'2026-10-01 09:00+08','2026-10-01 10:00+08') RETURNING id::text`, f.offeringB, f.teacherB, f.groupB, f.room)
	replacementGroup := scanID(t, f.pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES($1,'REVIEW-GROUP-REPLACEMENT','Replacement Group',2026) RETURNING id::text`, f.collegeB)
	if _, err := svc.PatchOffering(ctx, admin, f.offeringB, PatchOffering{TeacherID: &f.teacherC, ClassGroupID: &replacementGroup}); err != nil {
		t.Fatal(err)
	}
	var scheduledTeacher, scheduledGroup string
	if err := f.pool.QueryRow(ctx, `SELECT teacher_id::text,class_group_id::text FROM teaching.schedule_entries WHERE id=$1`, scheduleID).Scan(&scheduledTeacher, &scheduledGroup); err != nil {
		t.Fatal(err)
	}
	if scheduledTeacher != f.teacherC || scheduledGroup != replacementGroup {
		t.Fatalf("schedule teacher/group=%s/%s", scheduledTeacher, scheduledGroup)
	}

	otherOffering := scanID(t, f.pool, `INSERT INTO teaching.course_offerings(org_unit_id,term_id,course_id,teacher_id,class_group_id,code) VALUES($1,$2,$3,$4,$5,'REVIEW-CONFLICT-OFFERING') RETURNING id::text`, f.collegeB, f.term, f.courseB, f.teacherB, f.groupB)
	otherRoom := scanID(t, f.pool, `INSERT INTO teaching.classrooms(code,name) VALUES('REVIEW-ROOM-2','Room 2') RETURNING id::text`)
	otherGroup := scanID(t, f.pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES($1,'REVIEW-GROUP-C','Group C',2026) RETURNING id::text`, f.collegeB)
	if _, err := f.pool.Exec(ctx, `UPDATE teaching.course_offerings SET class_group_id=$2 WHERE id=$1`, otherOffering, otherGroup); err != nil {
		t.Fatal(err)
	}
	_ = scanID(t, f.pool, `INSERT INTO teaching.schedule_entries(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at) VALUES($1,$2,$3,$4,'2026-10-01 09:00+08','2026-10-01 10:00+08') RETURNING id::text`, otherOffering, f.teacherB, otherGroup, otherRoom)
	_, err := svc.PatchOffering(ctx, admin, f.offeringB, PatchOffering{TeacherID: &f.teacherB})
	assertAppError(t, err, http.StatusConflict, "SCHEDULE_CONFLICT")
	if got := scanID(t, f.pool, `SELECT teacher_id::text FROM teaching.course_offerings WHERE id=$1`, f.offeringB); got != f.teacherC {
		t.Fatalf("offering teacher changed after rollback: %s", got)
	}
	if got := scanID(t, f.pool, `SELECT teacher_id::text FROM teaching.schedule_entries WHERE id=$1`, scheduleID); got != f.teacherC {
		t.Fatalf("schedule teacher changed after rollback: %s", got)
	}
	if got := scanID(t, f.pool, `SELECT class_group_id::text FROM teaching.schedule_entries WHERE id=$1`, scheduleID); got != replacementGroup {
		t.Fatalf("schedule class changed after rollback: %s", got)
	}
}

func TestOfferingArchiveDoesNotRewriteDisabledTeacher(t *testing.T) {
	f := newReviewFixture(t)
	if _, err := f.pool.Exec(context.Background(), `UPDATE teaching.user_accounts SET status='disabled' WHERE id=$1`, f.teacherB); err != nil {
		t.Fatal(err)
	}
	svc := NewService(f.pool)
	status := "archived"
	got, err := svc.PatchOffering(context.Background(), identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}, f.offeringB, PatchOffering{Status: &status})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != status || got.TeacherID != f.teacherB {
		t.Fatalf("offering=%+v", got)
	}
}

func TestOfferingHistoryFreezeReturnsInvalidState(t *testing.T) {
	f := newReviewFixture(t)
	_ = scanID(t, f.pool, `INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by) VALUES($1,'Lesson','2026-10-01 09:00+08','2026-10-01 10:00+08',$2) RETURNING id::text`, f.offeringB, f.teacherB)
	svc := NewService(f.pool)
	_, err := svc.PatchOffering(context.Background(), identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}, f.offeringB, PatchOffering{TeacherID: &f.teacherC})
	assertAppError(t, err, http.StatusConflict, "INVALID_STATE")
	if got := scanID(t, f.pool, `SELECT teacher_id::text FROM teaching.course_offerings WHERE id=$1`, f.offeringB); got != f.teacherB {
		t.Fatalf("frozen offering teacher=%s", got)
	}
}

func TestConcurrentUserRenameCannotReactivateDisabledAccount(t *testing.T) {
	f := newReviewFixture(t)
	svc := identity.NewService(f.pool, time.Hour)
	admin := identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}
	for i := 0; i < 20; i++ {
		if _, err := f.pool.Exec(context.Background(), `UPDATE teaching.user_accounts SET status='active',display_name='Teacher B' WHERE id=$1`, f.teacherB); err != nil {
			t.Fatal(err)
		}
		name := "Renamed"
		disabled := "disabled"
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			_, err := svc.PatchUser(context.Background(), admin, f.teacherB, identity.PatchUser{DisplayName: &name})
			errs <- err
		}()
		go func() {
			<-start
			_, err := svc.PatchUser(context.Background(), admin, f.teacherB, identity.PatchUser{Status: &disabled})
			errs <- err
		}()
		close(start)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		var status string
		if err := f.pool.QueryRow(context.Background(), `SELECT status FROM teaching.user_accounts WHERE id=$1`, f.teacherB).Scan(&status); err != nil || status != "disabled" {
			t.Fatalf("iteration %d status=%s err=%v", i, status, err)
		}
	}
}

func createTeacher(t *testing.T, pool *pgxpool.Pool, orgID, username string) string {
	t.Helper()
	id := scanID(t, pool, `INSERT INTO teaching.user_accounts(org_unit_id,username,password_hash,display_name) VALUES($1,$2,'test-hash',$2) RETURNING id::text`, orgID, username)
	if _, err := pool.Exec(context.Background(), `INSERT INTO teaching.role_bindings(user_id,role_code) VALUES($1,'teacher')`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func scanID(t *testing.T, pool *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertAppError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Status != status || appErr.Code != code {
		t.Fatalf("error=%v want status=%d code=%s", err, status, code)
	}
}
