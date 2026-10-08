package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

type reportScope struct {
	ID, RunID, SessionID, Status, Teacher, College string
	LockVersion                                    int
	Provenance                                     map[string]any
}

func (s *Service) lockReport(ctx context.Context, tx pgx.Tx, id string) (reportScope, error) {
	var v reportScope
	err := tx.QueryRow(ctx, `SELECT r.id::text,r.run_id::text,r.session_id::text,r.status,r.lock_version,r.provenance,
		o.teacher_id::text,teaching.college_of(o.org_unit_id)::text FROM teaching.reports r
		JOIN teaching.lesson_sessions ls ON ls.id=r.session_id JOIN teaching.course_offerings o ON o.id=ls.offering_id
		WHERE r.id=$1 FOR UPDATE OF ls,r`, id).
		Scan(&v.ID, &v.RunID, &v.SessionID, &v.Status, &v.LockVersion, &v.Provenance, &v.Teacher, &v.College)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, notFound()
	}
	return v, err
}

func (s *Service) CopyReport(ctx context.Context, p identity.Principal, id string, in VersionReason) (Report, error) {
	if in.LockVersion < 1 || strings.TrimSpace(in.Reason) == "" {
		return Report{}, invalid("lock_version and reason are required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	v, err := s.lockReport(ctx, tx, id)
	if err != nil {
		return Report{}, err
	}
	if !canEdit(p, v.Teacher, v.College) {
		return Report{}, forbidden()
	}
	if v.LockVersion != in.LockVersion {
		return Report{}, revisionConflict()
	}
	if v.Status == "draft" || v.Status == "in_review" {
		return Report{}, state("editable reports do not require a copied revision")
	}
	var newID string
	err = tx.QueryRow(ctx, `INSERT INTO teaching.reports(run_id,session_id,revision,parent_report_id,status,summary,limitations,
		created_by,expires_at,summary_evidence_ids,provenance)
		SELECT run_id,session_id,(SELECT max(revision)+1 FROM teaching.reports WHERE run_id=r.run_id),r.id,'draft',summary,limitations,
		$2,expires_at,summary_evidence_ids,provenance FROM teaching.reports r WHERE id=$1 RETURNING id::text`, id, p.UserID).Scan(&newID)
	if err != nil {
		return Report{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO teaching.report_dimensions(report_id,dimension_code,coverage_status,summary,limitation,coverage,summary_evidence_ids)
		SELECT $2,dimension_code,coverage_status,summary,limitation,coverage,summary_evidence_ids FROM teaching.report_dimensions WHERE report_id=$1`, id, newID); err != nil {
		return Report{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,dimension_code,observation_type,observation_text,suggestion,sort_order
		FROM teaching.report_observations WHERE report_id=$1 AND removed_at IS NULL AND review_status<>'rejected' ORDER BY sort_order,id`, id)
	if err != nil {
		return Report{}, err
	}
	type copiedObservation struct {
		oldID, dimension, observationType, text, suggestion string
		order                                               int
	}
	var copied []copiedObservation
	for rows.Next() {
		var item copiedObservation
		if err = rows.Scan(&item.oldID, &item.dimension, &item.observationType, &item.text, &item.suggestion, &item.order); err != nil {
			rows.Close()
			return Report{}, err
		}
		copied = append(copied, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Report{}, err
	}
	for _, item := range copied {
		var newObservationID string
		if err = tx.QueryRow(ctx, `INSERT INTO teaching.report_observations(report_id,run_id,session_id,dimension_code,observation_type,observation_text,suggestion,sort_order)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, newID, v.RunID, v.SessionID, item.dimension, item.observationType, item.text, item.suggestion, item.order).Scan(&newObservationID); err != nil {
			return Report{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO teaching.observation_evidence(observation_id,report_id,evidence_id,run_id,session_id)
			SELECT $2,$3,evidence_id,run_id,session_id FROM teaching.observation_evidence WHERE observation_id=$1`, item.oldID, newObservationID, newID); err != nil {
			return Report{}, err
		}
	}
	if err = s.refreshReportDigest(ctx, tx, newID); err != nil {
		return Report{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,resource_id,result,request_id,metadata)
		VALUES($1,'report.revise','report',$2,'success','service',jsonb_build_object('parent_report_id',$3::text,'reason',$4::text))`, p.UserID, newID, id, strings.TrimSpace(in.Reason)); err != nil {
		return Report{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return s.GetReport(ctx, p, newID)
}

func (s *Service) PatchReport(ctx context.Context, p identity.Principal, id string, in PatchReport) (Report, error) {
	if in.LockVersion < 1 || strings.TrimSpace(in.Reason) == "" {
		return Report{}, invalid("lock_version and reason are required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	v, err := s.lockReport(ctx, tx, id)
	if err != nil {
		return Report{}, err
	}
	if !canEdit(p, v.Teacher, v.College) {
		return Report{}, forbidden()
	}
	if v.LockVersion != in.LockVersion {
		return Report{}, revisionConflict()
	}
	if v.Status != "draft" && v.Status != "in_review" {
		return Report{}, state("published report versions are read-only")
	}
	if err = s.validateReportContent(ctx, tx, v, in); err != nil {
		return Report{}, err
	}
	for _, d := range in.Dimensions {
		coverage, _ := json.Marshal(d.Coverage)
		_, err = tx.Exec(ctx, `UPDATE teaching.report_dimensions SET coverage_status=$3,summary=$4,limitation=$5,coverage=$6,summary_evidence_ids=$7
			WHERE report_id=$1 AND dimension_code=$2`, id, d.DimensionCode, d.CoverageStatus, d.Summary, d.Limitation, coverage, d.SummaryEvidenceIDs)
		if err != nil {
			return Report{}, err
		}
	}
	existing, err := s.activeObservationMap(ctx, tx, id)
	if err != nil {
		return Report{}, err
	}
	kept := map[string]bool{}
	for order, item := range in.Observations {
		observationID := ""
		status := "unreviewed"
		if item.ID != nil {
			observationID = *item.ID
			old, ok := existing[observationID]
			if !ok {
				return Report{}, invalid("observation id does not belong to this report")
			}
			if sameWritable(old, item) {
				status = old.ReviewStatus
			}
			_, err = tx.Exec(ctx, `UPDATE teaching.report_observations SET dimension_code=$2,observation_type=$3,observation_text=$4,
				suggestion=$5,review_status=$6,sort_order=$7 WHERE id=$1`, observationID, item.DimensionCode, item.ObservationType,
				item.ObservationText, item.Suggestion, status, order)
		} else {
			err = tx.QueryRow(ctx, `INSERT INTO teaching.report_observations(report_id,run_id,session_id,dimension_code,observation_type,
				observation_text,suggestion,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, id, v.RunID, v.SessionID,
				item.DimensionCode, item.ObservationType, item.ObservationText, item.Suggestion, order).Scan(&observationID)
		}
		if err != nil {
			return Report{}, err
		}
		kept[observationID] = true
		if _, err = tx.Exec(ctx, `DELETE FROM teaching.observation_evidence WHERE observation_id=$1`, observationID); err != nil {
			return Report{}, err
		}
		for _, evidenceID := range item.EvidenceIDs {
			if _, err = tx.Exec(ctx, `INSERT INTO teaching.observation_evidence(observation_id,report_id,evidence_id,run_id,session_id)
				VALUES($1,$2,$3,$4,$5)`, observationID, id, evidenceID, v.RunID, v.SessionID); err != nil {
				return Report{}, err
			}
		}
	}
	for observationID := range existing {
		if !kept[observationID] {
			if _, err = tx.Exec(ctx, `UPDATE teaching.report_observations SET removed_at=now() WHERE id=$1`, observationID); err != nil {
				return Report{}, err
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.reports SET summary=$2,summary_evidence_ids=$3,status='draft',
		reviewed_content_sha256=NULL,reviewed_by=NULL,reviewed_at=NULL,lock_version=lock_version+1 WHERE id=$1`, id, in.Summary, in.SummaryEvidenceIDs); err != nil {
		return Report{}, err
	}
	if err = s.refreshReportDigest(ctx, tx, id); err != nil {
		return Report{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,resource_id,result,request_id,metadata)
		VALUES($1,'report.edit','report',$2,'success','service',jsonb_build_object('reason',$3::text))`, p.UserID, id, strings.TrimSpace(in.Reason)); err != nil {
		return Report{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return s.GetReport(ctx, p, id)
}

func (s *Service) Review(ctx context.Context, p identity.Principal, id string, in ReviewReport) (Report, error) {
	if in.LockVersion < 1 || strings.TrimSpace(in.Reason) == "" {
		return Report{}, invalid("lock_version and reason are required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	v, err := s.lockReport(ctx, tx, id)
	if err != nil {
		return Report{}, err
	}
	if v.LockVersion != in.LockVersion {
		return Report{}, revisionConflict()
	}
	dbAction := in.Action
	switch in.Action {
	case "submit":
		if !canEdit(p, v.Teacher, v.College) || v.Status != "draft" || in.ObservationID != nil || in.ContentSHA256 != nil {
			return Report{}, forbiddenOrState(canEdit(p, v.Teacher, v.College), "only a draft can be submitted")
		}
		_, err = tx.Exec(ctx, `UPDATE teaching.reports SET status='in_review',lock_version=lock_version+1 WHERE id=$1`, id)
	case "accept", "revise", "reject":
		if !canReview(p, v.College) {
			return Report{}, forbidden()
		}
		if v.Status != "in_review" || in.ObservationID == nil || in.ContentSHA256 != nil {
			return Report{}, state("observation review requires an in-review report")
		}
		status := map[string]string{"accept": "accepted", "revise": "revised", "reject": "rejected"}[in.Action]
		tag, updateErr := tx.Exec(ctx, `UPDATE teaching.report_observations SET review_status=$3 WHERE id=$1 AND report_id=$2 AND removed_at IS NULL`, *in.ObservationID, id, status)
		if updateErr != nil {
			return Report{}, updateErr
		}
		if tag.RowsAffected() != 1 {
			return Report{}, invalid("observation does not belong to this report")
		}
		_, err = tx.Exec(ctx, `UPDATE teaching.reports SET reviewed_content_sha256=NULL,reviewed_by=NULL,reviewed_at=NULL,lock_version=lock_version+1 WHERE id=$1`, id)
		if in.Action == "accept" {
			dbAction = "confirm"
		}
	case "confirm_report":
		if !canReview(p, v.College) {
			return Report{}, forbidden()
		}
		if v.Status != "in_review" || in.ObservationID != nil || in.ContentSHA256 == nil {
			return Report{}, state("complete confirmation requires an in-review report")
		}
		var digest string
		var unreviewed int
		if err = tx.QueryRow(ctx, `SELECT content_sha256,(SELECT count(*) FROM teaching.report_observations WHERE report_id=$1 AND removed_at IS NULL AND review_status='unreviewed') FROM teaching.reports WHERE id=$1`, id).Scan(&digest, &unreviewed); err != nil {
			return Report{}, err
		}
		if digest != *in.ContentSHA256 || unreviewed != 0 {
			return Report{}, apperror.New(http.StatusUnprocessableEntity, "INVALID_REPORT", "report content changed or observations remain unreviewed")
		}
		_, err = tx.Exec(ctx, `UPDATE teaching.reports SET reviewed_content_sha256=content_sha256,reviewed_by=$2,reviewed_at=now(),lock_version=lock_version+1 WHERE id=$1`, id, p.UserID)
	default:
		return Report{}, invalid("review action is invalid")
	}
	if err != nil {
		return Report{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.review_actions(report_id,run_id,session_id,observation_id,reviewer_id,action,reason)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, id, v.RunID, v.SessionID, in.ObservationID, p.UserID, dbAction, strings.TrimSpace(in.Reason))
	if err != nil {
		return Report{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return s.GetReport(ctx, p, id)
}

func (s *Service) Publish(ctx context.Context, p identity.Principal, id string, in PublishReport) (Report, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	v, err := s.lockReport(ctx, tx, id)
	if err != nil {
		return Report{}, err
	}
	if !canReview(p, v.College) {
		return Report{}, forbidden()
	}
	if v.LockVersion != in.LockVersion {
		return Report{}, revisionConflict()
	}
	if v.Status != "in_review" {
		return Report{}, state("only an in-review report can be published")
	}
	var currentID *string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM teaching.reports WHERE session_id=$1 AND status='published'`, v.SessionID).Scan(&currentID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Report{}, err
	}
	if !sameOptionalString(currentID, in.ExpectedCurrentReportID) {
		return Report{}, revisionConflict()
	}
	if err = s.validatePublication(ctx, tx, v); err != nil {
		return Report{}, err
	}
	if currentID != nil {
		if _, err = tx.Exec(ctx, `UPDATE teaching.reports SET status='superseded',lock_version=lock_version+1 WHERE id=$1 AND status='published'`, *currentID); err != nil {
			return Report{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.reports SET status='published',published_by=$2,published_at=now(),lock_version=lock_version+1 WHERE id=$1`, id, p.UserID); err != nil {
		return Report{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO teaching.review_actions(report_id,run_id,session_id,reviewer_id,action,reason)
		VALUES($1,$2,$3,$4,'publish','publication requirements satisfied')`, id, v.RunID, v.SessionID, p.UserID); err != nil {
		return Report{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return s.GetReport(ctx, p, id)
}

func (s *Service) Withdraw(ctx context.Context, p identity.Principal, id string, in VersionReason) (Report, error) {
	if in.LockVersion < 1 || strings.TrimSpace(in.Reason) == "" {
		return Report{}, invalid("lock_version and reason are required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	v, err := s.lockReport(ctx, tx, id)
	if err != nil {
		return Report{}, err
	}
	if !canReview(p, v.College) {
		return Report{}, forbidden()
	}
	if v.LockVersion != in.LockVersion {
		return Report{}, revisionConflict()
	}
	if v.Status != "published" {
		return Report{}, state("only the current published report can be withdrawn")
	}
	var currentID *string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM teaching.reports WHERE session_id=$1 AND status='published'`, v.SessionID).Scan(&currentID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Report{}, err
	}
	if currentID == nil || *currentID != id {
		return Report{}, revisionConflict()
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.reports SET status='withdrawn',lock_version=lock_version+1 WHERE id=$1`, id); err != nil {
		return Report{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO teaching.review_actions(report_id,run_id,session_id,reviewer_id,action,reason)
		VALUES($1,$2,$3,$4,'withdraw',$5)`, id, v.RunID, v.SessionID, p.UserID, strings.TrimSpace(in.Reason)); err != nil {
		return Report{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return s.GetReport(ctx, p, id)
}

func (s *Service) validateReportContent(ctx context.Context, tx pgx.Tx, v reportScope, in PatchReport) error {
	if len(in.Dimensions) != len(dimensionCodes) {
		return invalid("report must contain six dimensions")
	}
	seenDimensions := map[string]bool{}
	if (strings.TrimSpace(in.Summary) != "") != (len(in.SummaryEvidenceIDs) > 0) {
		return invalid("summary and evidence references must agree")
	}
	if err := s.validateEvidenceIDs(ctx, tx, v, in.SummaryEvidenceIDs); err != nil {
		return err
	}
	var duration int64
	if err := tx.QueryRow(ctx, `SELECT duration_ms FROM teaching.media_assets WHERE id=(SELECT media_asset_id FROM teaching.analysis_runs WHERE id=$1)`, v.RunID).Scan(&duration); err != nil {
		return err
	}
	for _, d := range in.Dimensions {
		if !containsString(dimensionCodes, d.DimensionCode) || seenDimensions[d.DimensionCode] {
			return invalid("report dimension set is invalid")
		}
		seenDimensions[d.DimensionCode] = true
		if d.CoverageStatus != "observed" && d.CoverageStatus != "insufficient" && d.CoverageStatus != "not_applicable" {
			return invalid("coverage status is invalid")
		}
		if d.CoverageStatus != "observed" && strings.TrimSpace(d.Limitation) == "" {
			return invalid("limited dimensions require a limitation")
		}
		if (strings.TrimSpace(d.Summary) != "") != (len(d.SummaryEvidenceIDs) > 0) {
			return invalid("dimension summary and evidence references must agree")
		}
		if err := validateIntervals(d.Coverage, duration); err != nil {
			return invalid("dimension coverage is invalid")
		}
		if err := s.validateEvidenceIDs(ctx, tx, v, d.SummaryEvidenceIDs); err != nil {
			return err
		}
	}
	seenObservationIDs := map[string]bool{}
	for _, o := range in.Observations {
		if !containsString(dimensionCodes, o.DimensionCode) || !containsString([]string{"highlight", "issue", "observation"}, o.ObservationType) || strings.TrimSpace(o.ObservationText) == "" || len(o.EvidenceIDs) == 0 {
			return invalid("observation is invalid")
		}
		if o.ID != nil {
			if seenObservationIDs[*o.ID] {
				return invalid("observation ids must be unique")
			}
			seenObservationIDs[*o.ID] = true
		}
		if err := s.validateEvidenceIDs(ctx, tx, v, o.EvidenceIDs); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateEvidenceIDs(ctx context.Context, tx pgx.Tx, v reportScope, ids []string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return invalid("evidence references must be unique")
		}
		seen[id] = true
		var ok bool
		err := tx.QueryRow(ctx, `SELECT run_id=$2 AND session_id=$3 AND availability='available' FROM teaching.evidence_items WHERE id=$1`, id, v.RunID, v.SessionID).Scan(&ok)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !ok {
			return apperror.New(http.StatusUnprocessableEntity, "INVALID_REPORT", "report references unavailable or foreign evidence")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) activeObservationMap(ctx context.Context, tx pgx.Tx, reportID string) (map[string]Observation, error) {
	rows, err := tx.Query(ctx, `SELECT ro.id::text,ro.dimension_code,ro.observation_type,ro.observation_text,ro.suggestion,ro.review_status,
		COALESCE(array_agg(oe.evidence_id::text ORDER BY oe.evidence_id) FILTER(WHERE oe.evidence_id IS NOT NULL),'{}')
		FROM teaching.report_observations ro LEFT JOIN teaching.observation_evidence oe ON oe.observation_id=ro.id
		WHERE ro.report_id=$1 AND ro.removed_at IS NULL GROUP BY ro.id`, reportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Observation{}
	for rows.Next() {
		var o Observation
		if err = rows.Scan(&o.ID, &o.DimensionCode, &o.ObservationType, &o.ObservationText, &o.Suggestion, &o.ReviewStatus, &o.EvidenceIDs); err != nil {
			return nil, err
		}
		out[o.ID] = o
	}
	return out, rows.Err()
}

func sameWritable(old Observation, item WritableObservation) bool {
	a := append([]string(nil), old.EvidenceIDs...)
	b := append([]string(nil), item.EvidenceIDs...)
	slices.Sort(a)
	slices.Sort(b)
	return old.DimensionCode == item.DimensionCode && old.ObservationType == item.ObservationType && old.ObservationText == item.ObservationText && old.Suggestion == item.Suggestion && slices.Equal(a, b)
}

func (s *Service) refreshReportDigest(ctx context.Context, tx pgx.Tx, reportID string) error {
	var summary string
	var summaryIDs []string
	var provenance map[string]any
	if err := tx.QueryRow(ctx, `SELECT summary,summary_evidence_ids,provenance FROM teaching.reports WHERE id=$1`, reportID).Scan(&summary, &summaryIDs, &provenance); err != nil {
		return err
	}
	dRows, err := tx.Query(ctx, `SELECT dimension_code,coverage_status,summary,limitation,coverage,summary_evidence_ids
		FROM teaching.report_dimensions WHERE report_id=$1 ORDER BY array_position(ARRAY['content','pace','thinking','expression','management','technology'],dimension_code)`, reportID)
	if err != nil {
		return err
	}
	var dimensions []Dimension
	for dRows.Next() {
		var d Dimension
		var coverage []byte
		if err = dRows.Scan(&d.DimensionCode, &d.CoverageStatus, &d.Summary, &d.Limitation, &coverage, &d.SummaryEvidenceIDs); err != nil {
			dRows.Close()
			return err
		}
		if err = json.Unmarshal(coverage, &d.Coverage); err != nil {
			dRows.Close()
			return err
		}
		dimensions = append(dimensions, d)
	}
	dRows.Close()
	if err = dRows.Err(); err != nil {
		return err
	}
	observations, err := s.activeObservationMap(ctx, tx, reportID)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM teaching.report_observations WHERE report_id=$1 AND removed_at IS NULL ORDER BY sort_order,id`, reportID)
	if err != nil {
		return err
	}
	digestObservations := []map[string]any{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		o := observations[id]
		digestObservations = append(digestObservations, map[string]any{"id": o.ID, "dimension_code": o.DimensionCode, "observation_type": o.ObservationType, "observation_text": o.ObservationText, "suggestion": o.Suggestion, "evidence_ids": o.EvidenceIDs})
	}
	rows.Close()
	digest := canonicalDigest(map[string]any{"summary": summary, "summary_evidence_ids": summaryIDs, "dimensions": dimensions, "observations": digestObservations, "provenance": provenance})
	_, err = tx.Exec(ctx, `UPDATE teaching.reports SET content_sha256=$2 WHERE id=$1`, reportID, digest)
	return err
}

func (s *Service) validatePublication(ctx context.Context, tx pgx.Tx, v reportScope) error {
	var contentSHA string
	var reviewedSHA *string
	var reviewedBy *string
	var reviewedAt any
	var sourceOK bool
	err := tx.QueryRow(ctx, `SELECT r.content_sha256,r.reviewed_content_sha256,r.reviewed_by::text,r.reviewed_at,
		(ls.status NOT IN('deleting','deleted') AND ls.content_expires_at>now() AND m.expires_at>now() AND src.rights_status='verified' AND src.allowed_uses ? 'analysis')
		FROM teaching.reports r JOIN teaching.analysis_runs ar ON ar.id=r.run_id JOIN teaching.lesson_sessions ls ON ls.id=r.session_id
		JOIN teaching.media_assets m ON m.id=ar.media_asset_id JOIN teaching.source_records src ON src.id=m.source_record_id WHERE r.id=$1`, v.ID).
		Scan(&contentSHA, &reviewedSHA, &reviewedBy, &reviewedAt, &sourceOK)
	if err != nil {
		return err
	}
	if reviewedSHA == nil || *reviewedSHA != contentSHA || reviewedBy == nil || reviewedAt == nil {
		return apperror.New(http.StatusUnprocessableEntity, "INVALID_REPORT", "complete report confirmation is missing or stale")
	}
	if !sourceOK {
		return apperror.New(http.StatusForbidden, "SOURCE_NOT_VERIFIED", "source no longer permits publication")
	}
	var dimensions, invalidDimensions, facts, invalidObservations int
	err = tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM teaching.report_dimensions WHERE report_id=$1),
		(SELECT count(*) FROM teaching.report_dimensions d WHERE d.report_id=$1 AND ((d.coverage_status<>'observed' AND btrim(d.limitation)='') OR (btrim(d.summary)<>'')<>(cardinality(d.summary_evidence_ids)>0) OR EXISTS(SELECT 1 FROM unnest(d.summary_evidence_ids) x(id) LEFT JOIN teaching.evidence_items e ON e.id=x.id WHERE e.id IS NULL OR e.run_id=$2::uuid IS NOT TRUE OR e.availability<>'available'))),
		(SELECT count(*) FROM teaching.report_observations WHERE report_id=$1 AND removed_at IS NULL AND review_status IN('accepted','revised')),
		(SELECT count(*) FROM teaching.report_observations ro WHERE ro.report_id=$1 AND ro.removed_at IS NULL AND ro.review_status NOT IN('accepted','revised','rejected') OR ro.report_id=$1 AND ro.removed_at IS NULL AND ro.review_status IN('accepted','revised') AND NOT EXISTS(SELECT 1 FROM teaching.observation_evidence oe JOIN teaching.evidence_items e ON e.id=oe.evidence_id WHERE oe.observation_id=ro.id AND e.run_id=$2::uuid AND e.availability='available'))`, v.ID, v.RunID).
		Scan(&dimensions, &invalidDimensions, &facts, &invalidObservations)
	if err != nil {
		return err
	}
	var invalidSummary int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM teaching.reports r WHERE r.id=$1 AND ((btrim(r.summary)<>'')<>(cardinality(r.summary_evidence_ids)>0) OR EXISTS(SELECT 1 FROM unnest(r.summary_evidence_ids) x(id) LEFT JOIN teaching.evidence_items e ON e.id=x.id WHERE e.id IS NULL OR e.run_id=$2::uuid IS NOT TRUE OR e.availability<>'available'))`, v.ID, v.RunID).Scan(&invalidSummary); err != nil {
		return err
	}
	if dimensions != len(dimensionCodes) || invalidDimensions != 0 || invalidSummary != 0 || facts == 0 || invalidObservations != 0 {
		return apperror.New(http.StatusUnprocessableEntity, "INVALID_REPORT", "report does not satisfy publication requirements")
	}
	return nil
}

func canEdit(p identity.Principal, teacher, college string) bool {
	return p.Has("sys_admin") || p.Scoped("supervisor", college) || (p.Has("teacher") && p.UserID == teacher)
}

func canReview(p identity.Principal, college string) bool {
	return p.Has("sys_admin") || p.Scoped("supervisor", college)
}

func sameOptionalString(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func forbiddenOrState(authorized bool, message string) error {
	if !authorized {
		return forbidden()
	}
	return state(message)
}
