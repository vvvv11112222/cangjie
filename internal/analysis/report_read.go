package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

func (s *Service) GetReport(ctx context.Context, p identity.Principal, id string) (Report, error) {
	var out Report
	var teacher, college string
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, `SELECT r.id::text,r.run_id::text,r.session_id::text,r.revision,r.lock_version,r.status,r.summary,
		r.summary_evidence_ids,r.content_sha256,r.reviewed_content_sha256,r.reviewed_by::text,r.reviewed_at,r.provenance,r.expires_at,
		o.teacher_id::text,teaching.college_of(o.org_unit_id)::text
		FROM teaching.reports r JOIN teaching.lesson_sessions ls ON ls.id=r.session_id JOIN teaching.course_offerings o ON o.id=ls.offering_id WHERE r.id=$1`, id).
		Scan(&out.ID, &out.RunID, &out.SessionID, &out.Revision, &out.LockVersion, &out.Status, &out.Summary, &out.SummaryEvidenceIDs, &out.ContentSHA256, &out.ReviewedContentSHA256, &out.ReviewedBy, &out.ReviewedAt, &out.Provenance, &expiresAt, &teacher, &college)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !canView(p, teacher, college) {
		return Report{}, notFound()
	}
	if err != nil {
		return Report{}, err
	}
	if !expiresAt.After(time.Now()) {
		return Report{}, expired()
	}
	if err = s.requireRunContentReadable(ctx, out.RunID, false); err != nil {
		return Report{}, err
	}
	dRows, err := s.pool.Query(ctx, `SELECT dimension_code,coverage_status,summary,limitation,coverage,summary_evidence_ids FROM teaching.report_dimensions WHERE report_id=$1 ORDER BY array_position(ARRAY['content','pace','thinking','expression','management','technology'],dimension_code)`, id)
	if err != nil {
		return Report{}, err
	}
	defer dRows.Close()
	for dRows.Next() {
		var d Dimension
		var coverage []byte
		if err = dRows.Scan(&d.DimensionCode, &d.CoverageStatus, &d.Summary, &d.Limitation, &coverage, &d.SummaryEvidenceIDs); err != nil {
			return Report{}, err
		}
		if err = json.Unmarshal(coverage, &d.Coverage); err != nil {
			return Report{}, err
		}
		if d.Coverage == nil {
			d.Coverage = []Interval{}
		}
		if d.SummaryEvidenceIDs == nil {
			d.SummaryEvidenceIDs = []string{}
		}
		out.Dimensions = append(out.Dimensions, d)
	}
	oRows, err := s.pool.Query(ctx, `SELECT ro.id::text,ro.dimension_code,ro.observation_type,ro.observation_text,ro.suggestion,ro.review_status,COALESCE(array_agg(oe.evidence_id::text ORDER BY oe.evidence_id) FILTER(WHERE oe.evidence_id IS NOT NULL),'{}') FROM teaching.report_observations ro LEFT JOIN teaching.observation_evidence oe ON oe.observation_id=ro.id WHERE ro.report_id=$1 AND ro.removed_at IS NULL GROUP BY ro.id ORDER BY ro.sort_order,ro.id`, id)
	if err != nil {
		return Report{}, err
	}
	defer oRows.Close()
	for oRows.Next() {
		var o Observation
		if err = oRows.Scan(&o.ID, &o.DimensionCode, &o.ObservationType, &o.ObservationText, &o.Suggestion, &o.ReviewStatus, &o.EvidenceIDs); err != nil {
			return Report{}, err
		}
		if o.EvidenceIDs == nil {
			o.EvidenceIDs = []string{}
		}
		out.Observations = append(out.Observations, o)
	}
	if out.Dimensions == nil {
		out.Dimensions = []Dimension{}
	}
	if out.Observations == nil {
		out.Observations = []Observation{}
	}
	if out.SummaryEvidenceIDs == nil {
		out.SummaryEvidenceIDs = []string{}
	}
	out.AllowedActions = []string{}
	if out.ContentSHA256 == nil || out.Provenance == nil {
		return out, nil
	}
	if out.Status == "draft" && canAnalyze(p, teacher) {
		out.AllowedActions = []string{"edit", "submit"}
	}
	return out, nil
}

func (s *Service) ListReports(ctx context.Context, p identity.Principal, sessionID, after string, limit int) ([]Report, string, error) {
	var teacher, college string
	err := s.pool.QueryRow(ctx, `SELECT o.teacher_id::text,teaching.college_of(o.org_unit_id)::text FROM teaching.lesson_sessions ls JOIN teaching.course_offerings o ON o.id=ls.offering_id WHERE ls.id=$1`, sessionID).Scan(&teacher, &college)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !canView(p, teacher, college) {
		return nil, "", notFound()
	}
	if err != nil {
		return nil, "", err
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM teaching.reports WHERE session_id=$1 AND ($2='' OR (created_at,id)>(SELECT created_at,id FROM teaching.reports WHERE id=$2::uuid)) ORDER BY created_at,id LIMIT $3`, sessionID, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, "", err
		}
		ids = append(ids, id)
	}
	next := ""
	if len(ids) > limit {
		next = ids[limit-1]
		ids = ids[:limit]
	}
	items := make([]Report, 0, len(ids))
	for _, id := range ids {
		v, err := s.GetReport(ctx, p, id)
		if err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	return items, next, rows.Err()
}
