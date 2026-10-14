package academic

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

func TestScheduleConflictsIdentifyTeacherClassAndRoom(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	svc := NewService(f.pool)
	admin := identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}
	room2 := scanID(t, f.pool, `INSERT INTO teaching.classrooms(code,name) VALUES('REVIEW-ROOM-2','Room 2') RETURNING id::text`)
	room3 := scanID(t, f.pool, `INSERT INTO teaching.classrooms(code,name) VALUES('REVIEW-ROOM-3','Room 3') RETURNING id::text`)
	groupC := scanID(t, f.pool, `INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES($1,'REVIEW-GROUP-C','Group C',2026) RETURNING id::text`, f.collegeB)
	offeringTeacher := scanID(t, f.pool, `INSERT INTO teaching.course_offerings(org_unit_id,term_id,course_id,teacher_id,class_group_id,code) VALUES($1,$2,$3,$4,$5,'REVIEW-OFFERING-TEACHER') RETURNING id::text`, f.collegeB, f.term, f.courseB, f.teacherB, groupC)
	offeringClass := scanID(t, f.pool, `INSERT INTO teaching.course_offerings(org_unit_id,term_id,course_id,teacher_id,class_group_id,code) VALUES($1,$2,$3,$4,$5,'REVIEW-OFFERING-CLASS') RETURNING id::text`, f.collegeB, f.term, f.courseB, f.teacherC, f.groupB)
	offeringRoom := scanID(t, f.pool, `INSERT INTO teaching.course_offerings(org_unit_id,term_id,course_id,teacher_id,class_group_id,code) VALUES($1,$2,$3,$4,$5,'REVIEW-OFFERING-ROOM') RETURNING id::text`, f.collegeB, f.term, f.courseB, f.teacherC, groupC)

	base := CreateSchedule{OfferingID: f.offeringB, ClassroomID: f.room, StartsAt: "2026-10-20T09:00:00+08:00", EndsAt: "2026-10-20T10:00:00+08:00", Status: "active"}
	if _, err := svc.CreateSchedule(ctx, admin, base); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSchedule(ctx, admin, CreateSchedule{OfferingID: f.offeringB, ClassroomID: f.room, StartsAt: "2026-10-20T10:00:00+08:00", EndsAt: "2026-10-20T11:00:00+08:00", Status: "active"}); err != nil {
		t.Fatalf("adjacent schedule: %v", err)
	}

	cases := []struct {
		name, offering, room, resource string
	}{
		{"teacher", offeringTeacher, room2, "teacher"},
		{"class", offeringClass, room3, "class_group"},
		{"room", offeringRoom, f.room, "classroom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateSchedule(ctx, admin, CreateSchedule{OfferingID: tc.offering, ClassroomID: tc.room, StartsAt: "2026-10-20T09:30:00+08:00", EndsAt: "2026-10-20T10:30:00+08:00", Status: "active"})
			var appErr *apperror.Error
			if !errors.As(err, &appErr) || appErr.Status != http.StatusConflict || appErr.Code != "SCHEDULE_CONFLICT" || appErr.Details["resource_type"] != tc.resource {
				t.Fatalf("error=%#v", err)
			}
		})
	}
}

func TestPatchScheduleLocksOfferingBeforeSchedule(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	svc := NewService(f.pool)
	admin := identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}
	schedule, err := svc.CreateSchedule(ctx, admin, CreateSchedule{OfferingID: f.offeringB, ClassroomID: f.room, StartsAt: "2026-10-22T09:00:00+08:00", EndsAt: "2026-10-22T10:00:00+08:00", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background()) //nolint:errcheck
	if _, err = blocker.Exec(ctx, `SELECT 1 FROM teaching.course_offerings WHERE id=$1 FOR UPDATE`, f.offeringB); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		cancelled := "cancelled"
		_, patchErr := svc.PatchSchedule(context.Background(), admin, schedule.ID, PatchSchedule{Status: &cancelled})
		done <- patchErr
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err = blocker.Exec(ctx, `SET LOCAL statement_timeout='1s'`); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, `SELECT 1 FROM teaching.schedule_entries WHERE id=$1 FOR UPDATE`, schedule.ID); err != nil {
		t.Fatalf("schedule updater locked the schedule before the offering: %v", err)
	}
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("patch after offering lock release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("schedule patch did not finish after offering lock release")
	}
}

func TestScheduleImportRollsBackEveryRow(t *testing.T) {
	f := newReviewFixture(t)
	svc := NewService(f.pool)
	admin := identity.Principal{Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}
	_, err := svc.ImportSchedules(context.Background(), admin, ImportSchedules{Rows: []ImportScheduleRow{
		{OfferingID: f.offeringB, ClassroomID: f.room, StartsAt: "2026-10-21T09:00:00+08:00", EndsAt: "2026-10-21T10:00:00+08:00"},
		{OfferingID: f.offeringB, ClassroomID: f.room, StartsAt: "2026-10-21T09:30:00+08:00", EndsAt: "2026-10-21T10:30:00+08:00"},
	}})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != "SCHEDULE_CONFLICT" {
		t.Fatalf("import error=%v", err)
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM teaching.schedule_entries WHERE starts_at::date='2026-10-21'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled back rows=%d err=%v", count, err)
	}
}
