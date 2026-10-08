package classroom

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/optional"
)

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type Session struct {
	ID                    string    `json:"id"`
	OfferingID            string    `json:"offering_id"`
	ScheduleEntryID       *string   `json:"schedule_entry_id"`
	Title                 string    `json:"title"`
	PlannedStartAt        time.Time `json:"planned_start_at"`
	PlannedEndAt          time.Time `json:"planned_end_at"`
	IsDemo                bool      `json:"is_demo"`
	Status                string    `json:"status"`
	TranscriptLockVersion int       `json:"transcript_lock_version"`
	PrimaryMediaAssetID   *string   `json:"primary_media_asset_id"`
	LatestRunID           *string   `json:"latest_run_id"`
	CurrentReportID       *string   `json:"current_report_id"`
	AllowedActions        []string  `json:"allowed_actions"`
}

type CreateSession struct {
	OfferingID      string                 `json:"offering_id"`
	ScheduleEntryID optional.Value[string] `json:"schedule_entry_id"`
	Title           string                 `json:"title"`
	PlannedStartAt  string                 `json:"planned_start_at"`
	PlannedEndAt    string                 `json:"planned_end_at"`
	IsDemo          *bool                  `json:"is_demo"`
}

type PatchSession struct {
	Title  *string `json:"title"`
	Status *string `json:"status"`
}

type SessionQuery struct {
	After          string
	Limit          int
	CollegeID      string
	EnrollmentYear *int
	UnknownYear    bool
	ClassGroupID   string
	TeacherID      string
	From           *time.Time
	To             *time.Time
	Status         string
}

const sessionColumns = `ls.id::text,ls.offering_id::text,ls.schedule_entry_id::text,ls.title,ls.planned_start_at,ls.planned_end_at,ls.is_demo,ls.status,ls.transcript_lock_version,
	(SELECT m.id::text FROM teaching.media_assets m WHERE m.session_id=ls.id AND m.is_primary AND m.status<>'deleted' ORDER BY m.created_at DESC,m.id DESC LIMIT 1),
	(SELECT r.id::text FROM teaching.analysis_runs r WHERE r.session_id=ls.id ORDER BY r.created_at DESC,r.id DESC LIMIT 1),
	(SELECT rp.id::text FROM teaching.reports rp WHERE rp.session_id=ls.id AND rp.status='published' LIMIT 1)`

func scanSession(row pgx.Row) (Session, error) {
	var v Session
	err := row.Scan(&v.ID, &v.OfferingID, &v.ScheduleEntryID, &v.Title, &v.PlannedStartAt, &v.PlannedEndAt, &v.IsDemo, &v.Status,
		&v.TranscriptLockVersion, &v.PrimaryMediaAssetID, &v.LatestRunID, &v.CurrentReportID)
	return v, err
}

func parseRange(startRaw, endRaw string) (time.Time, time.Time, error) {
	start, err := time.Parse(time.RFC3339, startRaw)
	if err != nil {
		return time.Time{}, time.Time{}, invalid("planned_start_at must be an RFC3339 date-time")
	}
	end, err := time.Parse(time.RFC3339, endRaw)
	if err != nil {
		return time.Time{}, time.Time{}, invalid("planned_end_at must be an RFC3339 date-time")
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, invalid("planned_end_at must be after planned_start_at")
	}
	return start, end, nil
}

func (s *Service) List(ctx context.Context, p identity.Principal, q SessionQuery) ([]Session, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sessionColumns+`,teaching.college_of(o.org_unit_id)::text,o.teacher_id::text
		FROM teaching.lesson_sessions ls
		JOIN teaching.course_offerings o ON o.id=ls.offering_id
		JOIN teaching.class_groups cg ON cg.id=o.class_group_id
		WHERE ($1='' OR ls.id>$1::uuid)
		AND ($2='' OR teaching.college_of(o.org_unit_id)=$2::uuid)
		AND ($3::integer IS NULL OR cg.enrollment_year=$3)
		AND (NOT $4 OR cg.enrollment_year IS NULL)
		AND ($5='' OR o.class_group_id=$5::uuid) AND ($6='' OR o.teacher_id=$6::uuid)
		AND ($7::timestamptz IS NULL OR (ls.planned_start_at<$8 AND ls.planned_end_at>$7))
		AND (($9='' AND ls.status NOT IN ('archived','deleting','deleted')) OR ($9<>'' AND ls.status=$9))
		AND ($10 OR teaching.college_of(o.org_unit_id)=ANY($11::uuid[]) OR ($13 AND o.teacher_id=$12))
		ORDER BY ls.id LIMIT $14`, q.After, q.CollegeID, q.EnrollmentYear, q.UnknownYear, q.ClassGroupID, q.TeacherID,
		q.From, q.To, q.Status, p.Has("sys_admin"), p.CollegeScopes(), p.UserID, p.Has("teacher"), q.Limit+1)
	if err != nil {
		return nil, "", storageError(err)
	}
	defer rows.Close()
	items := []Session{}
	for rows.Next() {
		var v Session
		var org, teacher string
		if err := rows.Scan(&v.ID, &v.OfferingID, &v.ScheduleEntryID, &v.Title, &v.PlannedStartAt, &v.PlannedEndAt, &v.IsDemo, &v.Status,
			&v.TranscriptLockVersion, &v.PrimaryMediaAssetID, &v.LatestRunID, &v.CurrentReportID, &org, &teacher); err != nil {
			return nil, "", err
		}
		v.AllowedActions = allowedActions(p, org, teacher, v.Status)
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > q.Limit {
		next = items[q.Limit-1].ID
		items = items[:q.Limit]
	}
	return items, next, nil
}

func (s *Service) Create(ctx context.Context, p identity.Principal, in CreateSession) (Session, error) {
	if !in.ScheduleEntryID.Set {
		return Session{}, invalid("schedule_entry_id is required and may be null")
	}
	if in.IsDemo == nil {
		return Session{}, invalid("is_demo is required")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return Session{}, invalid("title is required")
	}
	start, end, err := parseRange(in.PlannedStartAt, in.PlannedEndAt)
	if err != nil {
		return Session{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Session{}, fmt.Errorf("begin lesson creation: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var org, teacher, offeringStatus string
	err = tx.QueryRow(ctx, `SELECT teaching.college_of(org_unit_id)::text,teacher_id::text,status FROM teaching.course_offerings WHERE id=$1 FOR UPDATE`, in.OfferingID).Scan(&org, &teacher, &offeringStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, invalid("offering_id does not exist")
	}
	if err != nil {
		return Session{}, storageError(err)
	}
	if offeringStatus != "active" {
		return Session{}, apperror.New(http.StatusConflict, "INVALID_STATE", "lessons require an active offering")
	}
	if !canCreate(p, org, teacher) {
		return Session{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "lesson creation is outside your role and scope")
	}
	if in.ScheduleEntryID.Value != nil {
		var scheduledOffering, scheduleStatus string
		var scheduledStart, scheduledEnd time.Time
		err = tx.QueryRow(ctx, `SELECT offering_id::text,starts_at,ends_at,status FROM teaching.schedule_entries WHERE id=$1 FOR UPDATE`, *in.ScheduleEntryID.Value).Scan(&scheduledOffering, &scheduledStart, &scheduledEnd, &scheduleStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, invalid("schedule_entry_id does not exist")
		}
		if err != nil {
			return Session{}, storageError(err)
		}
		if scheduledOffering != in.OfferingID || !scheduledStart.Equal(start) || !scheduledEnd.Equal(end) {
			return Session{}, invalid("scheduled lessons must use the schedule offering and planned time")
		}
		if scheduleStatus != "active" {
			return Session{}, apperror.New(http.StatusConflict, "INVALID_STATE", "cancelled schedules cannot create lessons")
		}
	}
	v, err := scanSession(tx.QueryRow(ctx, `INSERT INTO teaching.lesson_sessions AS ls(offering_id,schedule_entry_id,title,planned_start_at,planned_end_at,is_demo,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+sessionColumns, in.OfferingID, in.ScheduleEntryID.Value, title, start, end, *in.IsDemo, p.UserID))
	if err != nil {
		return Session{}, storageError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, storageError(err)
	}
	v.AllowedActions = allowedActions(p, org, teacher, v.Status)
	return v, nil
}

func (s *Service) Patch(ctx context.Context, p identity.Principal, id string, in PatchSession) (Session, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Session{}, fmt.Errorf("begin lesson update: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	v, err := scanSession(tx.QueryRow(ctx, `SELECT `+sessionColumns+` FROM teaching.lesson_sessions ls WHERE ls.id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, notFound()
	}
	if err != nil {
		return Session{}, storageError(err)
	}
	var org, teacher string
	if err := tx.QueryRow(ctx, `SELECT teaching.college_of(org_unit_id)::text,teacher_id::text FROM teaching.course_offerings WHERE id=$1`, v.OfferingID).Scan(&org, &teacher); err != nil {
		return Session{}, storageError(err)
	}
	title := v.Title
	if in.Title != nil {
		title = strings.TrimSpace(*in.Title)
		if title == "" {
			return Session{}, invalid("title is required")
		}
		if !canCreate(p, org, teacher) {
			return Session{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "lesson editing is outside your role and scope")
		}
	}
	status := v.Status
	if in.Status != nil {
		if *in.Status != "archived" || (v.Status != "planned" && v.Status != "ready") {
			return Session{}, apperror.New(http.StatusConflict, "INVALID_STATE", "only planned or ready lessons can be archived")
		}
		if !canArchive(p, org) {
			return Session{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "lesson archiving is outside your role and scope")
		}
		status = "archived"
	}
	v, err = scanSession(tx.QueryRow(ctx, `UPDATE teaching.lesson_sessions AS ls SET title=$2,status=$3 WHERE id=$1 RETURNING `+sessionColumns, id, title, status))
	if err != nil {
		return Session{}, storageError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, storageError(err)
	}
	v.AllowedActions = allowedActions(p, org, teacher, v.Status)
	return v, nil
}

func (s *Service) SelectPrimaryMedia(ctx context.Context, p identity.Principal, sessionID, mediaID string) (Session, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Session{}, fmt.Errorf("begin primary media selection: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var org, teacher, status string
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT teaching.college_of(o.org_unit_id)::text,o.teacher_id::text,ls.status,ls.content_expires_at
		FROM teaching.lesson_sessions ls JOIN teaching.course_offerings o ON o.id=ls.offering_id WHERE ls.id=$1 FOR UPDATE OF ls`, sessionID).
		Scan(&org, &teacher, &status, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, notFound()
	}
	if err != nil {
		return Session{}, storageError(err)
	}
	if !canSelectPrimary(p, teacher) {
		return Session{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "primary media selection is outside your role and scope")
	}
	if status == "deleting" || status == "deleted" || !expires.After(time.Now()) {
		return Session{}, apperror.New(http.StatusConflict, "INVALID_STATE", "the lesson cannot change primary media")
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.media_assets source
		JOIN teaching.media_assets playback ON playback.id=source.playback_asset_id AND playback.session_id=source.session_id
		WHERE source.id=$1 AND source.session_id=$2 AND source.kind='source' AND source.status='ready' AND source.expires_at>now()
		AND playback.status='ready' AND playback.expires_at>now())`, mediaID, sessionID).Scan(&valid); err != nil {
		return Session{}, storageError(err)
	}
	if !valid {
		return Session{}, apperror.New(http.StatusConflict, "INVALID_STATE", "primary media must be a ready source asset in the lesson")
	}
	if _, err := tx.Exec(ctx, `UPDATE teaching.media_assets SET is_primary=false WHERE session_id=$1 AND is_primary`, sessionID); err != nil {
		return Session{}, storageError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE teaching.media_assets SET is_primary=true WHERE id=$1 AND session_id=$2`, mediaID, sessionID); err != nil {
		return Session{}, storageError(err)
	}
	result, err := scanSession(tx.QueryRow(ctx, `SELECT `+sessionColumns+` FROM teaching.lesson_sessions ls WHERE ls.id=$1`, sessionID))
	if err != nil {
		return Session{}, storageError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, storageError(err)
	}
	result.AllowedActions = allowedActions(p, org, teacher, result.Status)
	return result, nil
}

func canSelectPrimary(p identity.Principal, teacher string) bool {
	return p.Has("sys_admin") || (p.Has("teacher") && p.UserID == teacher)
}

func canCreate(p identity.Principal, college, teacher string) bool {
	return p.Has("sys_admin") || p.Scoped("academic_admin", college) || (p.Has("teacher") && p.UserID == teacher)
}

func canArchive(p identity.Principal, college string) bool {
	return p.Has("sys_admin") || p.Scoped("academic_admin", college)
}

func allowedActions(p identity.Principal, college, teacher, status string) []string {
	set := map[string]bool{}
	if status != "deleting" && status != "deleted" {
		if p.Has("sys_admin") || (p.Has("teacher") && p.UserID == teacher) {
			for _, action := range []string{"upload", "prepare_media", "analyze", "edit_transcript", "view_results", "view_report"} {
				set[action] = true
			}
		}
		if p.Has("sys_admin") || p.Scoped("supervisor", college) {
			set["view_results"], set["view_report"] = true, true
		}
		if (status == "planned" || status == "ready") && canArchive(p, college) {
			set["archive"] = true
		}
		if p.Has("sys_admin") {
			set["delete"] = true
		}
	}
	result := make([]string, 0, len(set))
	for action := range set {
		result = append(result, action)
	}
	sort.Strings(result)
	return result
}

func invalid(message string) error {
	return apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", message)
}
func notFound() error { return apperror.New(http.StatusNotFound, "NOT_FOUND", "lesson not found") }

func storageError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return apperror.New(http.StatusConflict, "INVALID_STATE", "lesson already exists for this schedule")
		case "23503", "23514", "22P02":
			return invalid("related resource or field is invalid")
		}
	}
	return fmt.Errorf("classroom storage: %w", err)
}
