package analysis

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

func (s *Service) CreateRevision(ctx context.Context, p identity.Principal, sessionID string, in CreateRevision) (Revision, error) {
	if sessionID == "" || in.MediaAssetID == "" || in.BaseRevisionID == "" || in.TranscriptLockVersion < 0 || strings.TrimSpace(in.Reason) == "" || len(in.Segments) == 0 {
		return Revision{}, invalid("invalid transcript revision")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Revision{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var teacher, college, status string
	var lockVersion int
	var duration *int64
	err = tx.QueryRow(ctx, `SELECT o.teacher_id::text,teaching.college_of(o.org_unit_id)::text,ls.status,
		ls.transcript_lock_version,m.duration_ms FROM teaching.lesson_sessions ls
		JOIN teaching.course_offerings o ON o.id=ls.offering_id
		JOIN teaching.media_assets m ON m.id=$2 AND m.session_id=ls.id
		WHERE ls.id=$1 FOR UPDATE OF ls`, sessionID, in.MediaAssetID).
		Scan(&teacher, &college, &status, &lockVersion, &duration)
	if errors.Is(err, pgx.ErrNoRows) {
		return Revision{}, notFound()
	}
	if err != nil {
		return Revision{}, err
	}
	if !canEdit(p, teacher, college) {
		return Revision{}, forbidden()
	}
	if status == "deleting" || status == "deleted" {
		return Revision{}, state("classroom is not active")
	}
	if lockVersion != in.TranscriptLockVersion {
		return Revision{}, revisionConflict()
	}
	var sourceRunID string
	err = tx.QueryRow(ctx, `SELECT source_run_id::text FROM teaching.transcript_revisions
		WHERE id=$1 AND session_id=$2 AND media_asset_id=$3`, in.BaseRevisionID, sessionID, in.MediaAssetID).Scan(&sourceRunID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Revision{}, invalid("base revision does not belong to this classroom media")
	}
	if err != nil {
		return Revision{}, err
	}
	for i, seg := range in.Segments {
		if seg.SegmentNo != i || seg.StartMS < 0 || seg.EndMS <= seg.StartMS || strings.TrimSpace(seg.Text) == "" || strings.TrimSpace(seg.Speaker) == "" || duration == nil || seg.EndMS > *duration {
			return Revision{}, invalid("transcript segments must be consecutive and within the media timeline")
		}
		in.Segments[i].Text = strings.TrimSpace(seg.Text)
		in.Segments[i].Speaker = strings.TrimSpace(seg.Speaker)
	}
	var revisionID string
	err = tx.QueryRow(ctx, `INSERT INTO teaching.transcript_revisions(session_id,media_asset_id,source_run_id,parent_revision_id,
		revision_no,source_type,content_sha256,created_by,reason)
		VALUES($1,$2,$3,$4,(SELECT COALESCE(max(revision_no),0)+1 FROM teaching.transcript_revisions WHERE session_id=$1 AND media_asset_id=$2),
		'manual',$5,$6,$7) RETURNING id::text`, sessionID, in.MediaAssetID, sourceRunID, in.BaseRevisionID,
		canonicalDigest(in.Segments), p.UserID, strings.TrimSpace(in.Reason)).Scan(&revisionID)
	if err != nil {
		return Revision{}, err
	}
	for _, seg := range in.Segments {
		if _, err = tx.Exec(ctx, `INSERT INTO teaching.transcript_revision_segments(revision_id,segment_no,start_ms,end_ms,text_content,speaker_label)
			VALUES($1,$2,$3,$4,$5,$6)`, revisionID, seg.SegmentNo, seg.StartMS, seg.EndMS, seg.Text, seg.Speaker); err != nil {
			return Revision{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.lesson_sessions SET transcript_lock_version=transcript_lock_version+1 WHERE id=$1`, sessionID); err != nil {
		return Revision{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Revision{}, err
	}
	return s.GetRevision(ctx, p, revisionID)
}

func (s *Service) GetRevision(ctx context.Context, p identity.Principal, id string) (Revision, error) {
	var out Revision
	var teacher, college string
	err := s.pool.QueryRow(ctx, `SELECT tr.id::text,tr.session_id::text,tr.media_asset_id::text,tr.source_run_id::text,tr.parent_revision_id::text,tr.revision_no,tr.source_type,tr.content_sha256,tr.created_by::text,tr.created_at,tr.reason,ls.transcript_lock_version,o.teacher_id::text,teaching.college_of(o.org_unit_id)::text FROM teaching.transcript_revisions tr JOIN teaching.lesson_sessions ls ON ls.id=tr.session_id JOIN teaching.course_offerings o ON o.id=ls.offering_id WHERE tr.id=$1`, id).Scan(&out.ID, &out.SessionID, &out.MediaAssetID, &out.SourceRunID, &out.ParentRevisionID, &out.RevisionNo, &out.SourceType, &out.ContentSHA256, &out.CreatedBy, &out.CreatedAt, &out.Reason, &out.TranscriptLockVersion, &teacher, &college)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !canView(p, teacher, college) {
		return Revision{}, notFound()
	}
	if err != nil {
		return Revision{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT segment_no,start_ms,end_ms,text_content,speaker_label FROM teaching.transcript_revision_segments WHERE revision_id=$1 ORDER BY segment_no`, id)
	if err != nil {
		return Revision{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var seg Segment
		if err = rows.Scan(&seg.SegmentNo, &seg.StartMS, &seg.EndMS, &seg.Text, &seg.Speaker); err != nil {
			return Revision{}, err
		}
		out.Segments = append(out.Segments, seg)
	}
	if out.Segments == nil {
		out.Segments = []Segment{}
	}
	return out, rows.Err()
}

func (s *Service) ListRevisions(ctx context.Context, p identity.Principal, sessionID, mediaID, after string, limit int) ([]Revision, string, error) {
	if mediaID == "" {
		return nil, "", invalid("media_asset_id is required")
	}
	var teacher, college string
	err := s.pool.QueryRow(ctx, `SELECT o.teacher_id::text,teaching.college_of(o.org_unit_id)::text FROM teaching.lesson_sessions ls JOIN teaching.course_offerings o ON o.id=ls.offering_id JOIN teaching.media_assets m ON m.session_id=ls.id AND m.id=$2 WHERE ls.id=$1`, sessionID, mediaID).Scan(&teacher, &college)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !canView(p, teacher, college) {
		return nil, "", notFound()
	}
	if err != nil {
		return nil, "", err
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM teaching.transcript_revisions WHERE session_id=$1 AND media_asset_id=$2 AND ($3='' OR (created_at,id)>(SELECT created_at,id FROM teaching.transcript_revisions WHERE id=$3::uuid)) ORDER BY created_at,id LIMIT $4`, sessionID, mediaID, after, limit+1)
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
	items := make([]Revision, 0, len(ids))
	for _, id := range ids {
		v, err := s.GetRevision(ctx, p, id)
		if err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	return items, next, rows.Err()
}
