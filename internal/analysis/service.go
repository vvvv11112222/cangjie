package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

const pipelineVersion = "p0-v1"

type snapshot struct {
	StagePlan   []string             `json:"stage_plan"`
	Parameters  Parameters           `json:"parameters"`
	Executions  map[string]Execution `json:"executions"`
	MediaLimits mediaLimits          `json:"media_limits"`
}

type mediaLimits struct {
	MaxDurationMS  int64 `json:"max_duration_ms"`
	MaxVideoHeight int   `json:"max_video_height"`
}

func NewService(pool *pgxpool.Pool, store storage.Backend, cfg Config) *Service {
	return &Service{pool: pool, store: store, cfg: cfg}
}

func (s *Service) Create(ctx context.Context, p identity.Principal, sessionID, key string, in CreateRun) (Run, bool, error) {
	if sessionID == "" || in.MediaAssetID == "" || len(key) < 1 || len(key) > 128 || !printableASCII(key) || in.ConfigProfile != pipelineVersion {
		return Run{}, false, invalid("invalid analysis request")
	}
	if in.Mode != "full" && in.Mode != "media_prepare" {
		return Run{}, false, invalid("phase 4 supports full and media_prepare runs")
	}
	if in.InputTranscriptRevision != nil {
		return Run{}, false, invalid("input_transcript_revision_id must be null")
	}
	requestDigest := digestJSON(in)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var teacher, college, sessionStatus, mediaStatus, mediaKind, rights string
	var contentExpires, mediaExpires time.Time
	var inputSHA string
	var uses []byte
	err = tx.QueryRow(ctx, `SELECT o.teacher_id::text,teaching.college_of(o.org_unit_id)::text,ls.status,
		ls.content_expires_at,m.status,m.kind,m.expires_at,m.sha256,src.rights_status,src.allowed_uses
		FROM teaching.lesson_sessions ls JOIN teaching.course_offerings o ON o.id=ls.offering_id
		JOIN teaching.media_assets m ON m.id=$2 AND m.session_id=ls.id
		JOIN teaching.source_records src ON src.id=m.source_record_id AND src.session_id=ls.id
		WHERE ls.id=$1 FOR UPDATE OF ls`, sessionID, in.MediaAssetID).
		Scan(&teacher, &college, &sessionStatus, &contentExpires, &mediaStatus, &mediaKind, &mediaExpires, &inputSHA, &rights, &uses)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, notFound()
	}
	if err != nil {
		return Run{}, false, err
	}
	if !canAnalyze(p, teacher) {
		return Run{}, false, forbidden()
	}
	if sessionStatus == "deleting" || sessionStatus == "deleted" {
		return Run{}, false, state("classroom is not active")
	}
	if time.Now().After(contentExpires) || time.Now().After(mediaExpires) {
		return Run{}, false, expired()
	}
	if mediaKind != "source" || (mediaStatus != "pending" && mediaStatus != "ready") {
		return Run{}, false, state("media is not eligible for analysis")
	}
	needed := "analysis"
	if in.Mode == "media_prepare" {
		needed = "playback"
	}
	if rights != "verified" || !jsonArrayContains(uses, needed) {
		return Run{}, false, apperror.New(http.StatusForbidden, "SOURCE_NOT_VERIFIED", "source does not permit this operation")
	}

	var priorID, priorDigest string
	err = tx.QueryRow(ctx, `SELECT id::text,COALESCE(request_sha256,'') FROM teaching.analysis_runs
		WHERE requested_by=$1 AND session_id=$2 AND idempotency_key=$3`, p.UserID, sessionID, key).Scan(&priorID, &priorDigest)
	if err == nil {
		if priorDigest != requestDigest {
			return Run{}, false, apperror.New(http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key was used with different parameters")
		}
		if err := tx.Commit(ctx); err != nil {
			return Run{}, false, err
		}
		run, err := s.Get(ctx, p, priorID)
		return run, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, err
	}

	snap := s.newSnapshot(in.Mode)
	snapshotJSON, _ := json.Marshal(snap)
	configHash := digestBytes(snapshotJSON)
	var runID string
	err = tx.QueryRow(ctx, `INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,
		input_sha256,config_hash,pipeline_version,config_snapshot,mode,request_sha256)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`, sessionID, in.MediaAssetID, p.UserID, key,
		inputSHA, configHash, pipelineVersion, snapshotJSON, in.Mode, requestDigest).Scan(&runID)
	if err != nil {
		if isUnique(err) {
			return Run{}, false, state("an equivalent analysis is already active")
		}
		return Run{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,max_attempts) VALUES($1,'probe',$2)`, runID, s.cfg.MediaJobAttempts)
	if err != nil {
		return Run{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, false, err
	}
	run, err := s.Get(ctx, p, runID)
	return run, false, err
}

func (s *Service) Get(ctx context.Context, p identity.Principal, id string) (Run, error) {
	var run Run
	var snapshotJSON []byte
	var teacher, college string
	err := s.pool.QueryRow(ctx, `SELECT r.id::text,r.session_id::text,r.media_asset_id::text,r.mode,
		r.input_transcript_revision_id::text,r.status,r.error_code,r.config_snapshot,
		o.teacher_id::text,teaching.college_of(o.org_unit_id)::text
		FROM teaching.analysis_runs r JOIN teaching.lesson_sessions ls ON ls.id=r.session_id
		JOIN teaching.course_offerings o ON o.id=ls.offering_id WHERE r.id=$1`, id).
		Scan(&run.ID, &run.SessionID, &run.MediaAssetID, &run.Mode, &run.InputTranscriptRevisionID, &run.Status, &run.ErrorCode, &snapshotJSON, &teacher, &college)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, notFound()
	}
	if err != nil {
		return Run{}, err
	}
	if !canView(p, teacher, college) {
		return Run{}, notFound()
	}
	var snap snapshot
	if err := json.Unmarshal(snapshotJSON, &snap); err != nil {
		return Run{}, err
	}
	run.StagePlan = nonNilStrings(snap.StagePlan)
	rows, err := s.pool.Query(ctx, `SELECT id::text,stage,status,progress,error_code,attempts,max_attempts,execution_deadline_at
		FROM teaching.analysis_jobs WHERE run_id=$1 ORDER BY created_at,id`, id)
	if err != nil {
		return Run{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.Stage, &job.Status, &job.Progress, &job.ErrorCode, &job.Attempts, &job.MaxAttempts, &job.ExecutionDeadlineAt); err != nil {
			return Run{}, err
		}
		run.Jobs = append(run.Jobs, job)
		if job.ErrorCode != nil {
			run.Limitations = appendUnique(run.Limitations, *job.ErrorCode)
		}
	}
	if err := rows.Err(); err != nil {
		return Run{}, err
	}
	if run.Jobs == nil {
		run.Jobs = []Job{}
	}
	if run.Limitations == nil {
		run.Limitations = []string{}
	}
	run.AllowedActions = []string{"view_results"}
	if (run.Status == "queued" || run.Status == "running") && canAnalyze(p, teacher) {
		run.AllowedActions = append(run.AllowedActions, "cancel")
	}
	if run.Status == "failed" || run.Status == "partial" || run.Status == "cancelled" {
		run.AllowedActions = append(run.AllowedActions, "retry")
	}
	return run, nil
}

func (s *Service) List(ctx context.Context, p identity.Principal, sessionID, after string, limit int) ([]Run, string, error) {
	var teacher, college string
	err := s.pool.QueryRow(ctx, `SELECT o.teacher_id::text,teaching.college_of(o.org_unit_id)::text FROM teaching.lesson_sessions ls JOIN teaching.course_offerings o ON o.id=ls.offering_id WHERE ls.id=$1`, sessionID).Scan(&teacher, &college)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !canView(p, teacher, college) {
		return nil, "", notFound()
	}
	if err != nil {
		return nil, "", err
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM teaching.analysis_runs WHERE session_id=$1
		AND ($2='' OR (created_at,id)>(SELECT created_at,id FROM teaching.analysis_runs WHERE id=$2::uuid))
		ORDER BY created_at,id LIMIT $3`, sessionID, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(ids) > limit {
		next = ids[limit-1]
		ids = ids[:limit]
	}
	items := make([]Run, 0, len(ids))
	for _, id := range ids {
		v, err := s.Get(ctx, p, id)
		if err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	return items, next, nil
}

func (s *Service) Cancel(ctx context.Context, p identity.Principal, runID string) (Run, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var sessionID, teacher, status string
	var planJSON []byte
	err = tx.QueryRow(ctx, `SELECT r.session_id::text,o.teacher_id::text,r.status,r.config_snapshot FROM teaching.analysis_runs r
		JOIN teaching.lesson_sessions ls ON ls.id=r.session_id JOIN teaching.course_offerings o ON o.id=ls.offering_id
		WHERE r.id=$1 FOR UPDATE OF ls,r`, runID).Scan(&sessionID, &teacher, &status, &planJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, notFound()
	}
	if err != nil {
		return Run{}, err
	}
	if !canAnalyze(p, teacher) {
		return Run{}, forbidden()
	}
	if terminalRun(status) {
		return Run{}, state("analysis run is already terminal")
	}
	var snap snapshot
	if err = json.Unmarshal(planJSON, &snap); err != nil {
		return Run{}, err
	}
	objectKeys, err := s.removePendingRunArtifacts(ctx, tx, runID)
	if err != nil {
		return Run{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='cancelled',worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,
		execution_deadline_at=NULL,expected_execution_sha256=NULL,error_code='CANCELLED',updated_at=now()
		WHERE run_id=$1 AND status IN ('queued','running')`, runID)
	if err != nil {
		return Run{}, err
	}
	for _, stage := range snap.StagePlan {
		_, err = tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,error_code)
		VALUES($1,$2,'cancelled',$3,'CANCELLED') ON CONFLICT(run_id,stage) DO NOTHING`, runID, stage, s.maxAttempts(stage))
		if err != nil {
			return Run{}, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='cancelled',error_code='CANCELLED',finished_at=now() WHERE id=$1`, runID)
	if err != nil {
		return Run{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	for _, key := range objectKeys {
		_ = s.store.Remove(key)
	}
	return s.Get(ctx, p, runID)
}

func (s *Service) Claim(ctx context.Context, workerID string, capabilities []string) (*Claim, error) {
	capabilities = validCapabilities(capabilities)
	if workerID == "" || len(capabilities) == 0 {
		return nil, invalid("worker_id and capabilities are required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var c Claim
	var snapshotJSON []byte
	err = tx.QueryRow(ctx, `SELECT j.id::text,r.id::text,r.session_id::text,r.media_asset_id::text,j.stage,r.input_sha256,m.duration_ms,
		r.config_snapshot,src.rights_version,ls.storage_generation
		FROM teaching.analysis_jobs j JOIN teaching.analysis_runs r ON r.id=j.run_id
		JOIN teaching.lesson_sessions ls ON ls.id=r.session_id JOIN teaching.media_assets m ON m.id=r.media_asset_id AND m.session_id=ls.id
		JOIN teaching.source_records src ON src.id=m.source_record_id AND src.session_id=ls.id
		WHERE j.id=(SELECT j2.id FROM teaching.analysis_jobs j2 JOIN teaching.analysis_runs r2 ON r2.id=j2.run_id
		 JOIN teaching.lesson_sessions ls2 ON ls2.id=r2.session_id JOIN teaching.media_assets m2 ON m2.id=r2.media_asset_id AND m2.session_id=ls2.id
		 JOIN teaching.source_records src2 ON src2.id=m2.source_record_id AND src2.session_id=ls2.id
		 WHERE j2.status='queued' AND j2.stage=ANY($1::text[]) AND j2.available_at<=now() AND j2.attempts<j2.max_attempts
		 AND r2.status IN('queued','running') AND ls2.status NOT IN('deleting','deleted') AND ls2.content_expires_at>now()
		 AND m2.expires_at>now() AND m2.status IN('pending','ready') AND src2.rights_status='verified'
		 AND src2.allowed_uses ? CASE WHEN r2.mode='media_prepare' THEN 'playback' ELSE 'analysis' END
		 ORDER BY j2.priority DESC,j2.available_at,j2.created_at LIMIT 1 FOR UPDATE OF j2 SKIP LOCKED)`, capabilities).
		Scan(&c.JobID, &c.RunID, &c.SessionID, &c.MediaAssetID, &c.Stage, &c.InputSHA256, &c.DurationMS, &snapshotJSON, &c.RightsVersion, &c.StorageGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snap snapshot
	if err = json.Unmarshal(snapshotJSON, &snap); err != nil {
		return nil, err
	}
	execution, ok := snap.Executions[c.Stage]
	if !ok {
		return nil, fmt.Errorf("missing execution for %s", c.Stage)
	}
	now := time.Now().UTC()
	c.ExecutionDeadlineAt = now.Add(s.timeout(c.Stage))
	c.LeaseExpiresAt = now.Add(s.cfg.Lease)
	if c.LeaseExpiresAt.After(c.ExecutionDeadlineAt) {
		c.LeaseExpiresAt = c.ExecutionDeadlineAt
	}
	c.SchemaVersion = SchemaVersion
	c.InputURL = "/internal/v1/jobs/" + c.JobID + "/input"
	c.Parameters = snap.Parameters
	c.Execution = execution
	err = tx.QueryRow(ctx, `UPDATE teaching.analysis_jobs SET status='running',attempts=attempts+1,worker_id=$2,lease_token=gen_random_uuid(),
		lease_expires_at=$3,execution_deadline_at=$4,expected_execution_sha256=$5,progress=0,error_code=NULL,error_detail=NULL,updated_at=now()
		WHERE id=$1 RETURNING lease_token::text`, c.JobID, workerID, c.LeaseExpiresAt, c.ExecutionDeadlineAt, digestJSON(execution)).Scan(&c.LeaseToken)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE teaching.analysis_runs SET status='running' WHERE id=$1 AND status='queued'`, c.RunID)
	return &c, nil
}

func (s *Service) Heartbeat(ctx context.Context, jobID, token string, progress int) (HeartbeatResult, error) {
	if progress < 0 || progress > 99 {
		return HeartbeatResult{}, invalid("progress must be between 0 and 99")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return HeartbeatResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	state, err := s.lockActiveJob(ctx, tx, jobID, token)
	if err != nil {
		return HeartbeatResult{}, err
	}
	next := time.Now().UTC().Add(s.cfg.Lease)
	if next.After(state.Deadline) {
		next = state.Deadline
	}
	if !next.After(time.Now()) {
		return HeartbeatResult{}, leaseLost()
	}
	_, err = tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET progress=GREATEST(progress,$2),lease_expires_at=$3,updated_at=now() WHERE id=$1`, jobID, progress, next)
	if err != nil {
		return HeartbeatResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return HeartbeatResult{}, err
	}
	return HeartbeatResult{LeaseExpiresAt: next, CancelRequested: false}, nil
}

func (s *Service) OpenInput(ctx context.Context, jobID, token string) (InputContent, error) {
	var objectKey, mime string
	var size int64
	err := s.pool.QueryRow(ctx, `SELECT m.object_key,m.mime_type,m.byte_size FROM teaching.analysis_jobs j JOIN teaching.analysis_runs r ON r.id=j.run_id
		JOIN teaching.lesson_sessions ls ON ls.id=r.session_id JOIN teaching.media_assets m ON m.id=r.media_asset_id
		JOIN teaching.source_records src ON src.id=m.source_record_id
		WHERE j.id=$1 AND j.status='running' AND j.lease_token=$2::uuid AND j.lease_expires_at>now() AND j.execution_deadline_at>now()
		AND r.status='running' AND ls.status NOT IN('deleting','deleted') AND ls.content_expires_at>now() AND m.expires_at>now()
		AND src.rights_status='verified' AND src.allowed_uses ? CASE WHEN r.mode='media_prepare' THEN 'playback' ELSE 'analysis' END`, jobID, token).Scan(&objectKey, &mime, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		return InputContent{}, leaseLost()
	}
	if err != nil {
		return InputContent{}, err
	}
	file, err := s.store.Open(objectKey)
	if err != nil {
		return InputContent{}, err
	}
	return InputContent{File: file, Size: size, MIMEType: mime}, nil
}

type lockedJob struct {
	RunID, SessionID, MediaID, Stage, Mode, Token string
	Deadline                                      time.Time
	Generation                                    int64
	SourceID                                      string
	Snapshot                                      snapshot
}

func (s *Service) lockActiveJob(ctx context.Context, tx pgx.Tx, jobID, token string) (lockedJob, error) {
	v := lockedJob{Token: token}
	var status, runStatus, sessionStatus, rights string
	var lease, timeContent, timeMedia time.Time
	var uses, snapshotJSON []byte
	err := tx.QueryRow(ctx, `SELECT r.id::text,r.session_id::text,r.media_asset_id::text,j.stage,r.mode,j.status,r.status,ls.status,
		j.lease_expires_at,j.execution_deadline_at,ls.content_expires_at,m.expires_at,ls.storage_generation,src.id::text,src.rights_status,src.allowed_uses,r.config_snapshot
		FROM teaching.analysis_jobs j JOIN teaching.analysis_runs r ON r.id=j.run_id JOIN teaching.lesson_sessions ls ON ls.id=r.session_id
		JOIN teaching.media_assets m ON m.id=r.media_asset_id JOIN teaching.source_records src ON src.id=m.source_record_id
		WHERE j.id=$1 AND j.lease_token=$2::uuid FOR UPDATE OF ls,r,j`, jobID, token).
		Scan(&v.RunID, &v.SessionID, &v.MediaID, &v.Stage, &v.Mode, &status, &runStatus, &sessionStatus, &lease, &v.Deadline, &timeContent, &timeMedia, &v.Generation, &v.SourceID, &rights, &uses, &snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, leaseLost()
	}
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(snapshotJSON, &v.Snapshot); err != nil {
		return v, err
	}
	needed := "analysis"
	if v.Mode == "media_prepare" {
		needed = "playback"
	}
	if status != "running" || runStatus != "running" || sessionStatus == "deleting" || sessionStatus == "deleted" || time.Now().After(lease) || time.Now().After(v.Deadline) || time.Now().After(timeContent) || time.Now().After(timeMedia) || rights != "verified" || !jsonArrayContains(uses, needed) {
		return v, leaseLost()
	}
	return v, nil
}

func (s *Service) UploadArtifact(ctx context.Context, jobID, token string, in ArtifactInput, reader io.Reader) (Artifact, bool, error) {
	if in.ArtifactKey == "" || !strings.HasPrefix(in.ArtifactKey, token+"/") || in.OriginOffsetMS != 0 {
		return Artifact{}, false, invalid("artifact metadata is invalid")
	}
	staged, err := s.store.Stage(ctx, reader, s.cfg.MaxArtifactBytes)
	if err != nil {
		return Artifact{}, false, err
	}
	defer s.store.Discard(staged) //nolint:errcheck
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Artifact{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	state, err := s.lockActiveJob(ctx, tx, jobID, token)
	if err != nil {
		return Artifact{}, false, err
	}
	if !artifactAllowed(state.Stage, in.Kind, in.TimestampMS) {
		return Artifact{}, false, invalid("artifact kind is not valid for this stage")
	}
	var existing Artifact
	var metadata []byte
	err = tx.QueryRow(ctx, `SELECT ja.media_asset_id::text,ja.metadata FROM teaching.job_artifacts ja WHERE ja.job_id=$1 AND ja.artifact_key=$2`, jobID, in.ArtifactKey).Scan(&existing.AssetID, &metadata)
	if err == nil {
		var m struct {
			SHA256   string `json:"sha256"`
			ByteSize int64  `json:"byte_size"`
		}
		_ = json.Unmarshal(metadata, &m)
		if m.SHA256 != staged.SHA256 || m.ByteSize != staged.Size {
			return Artifact{}, false, apperror.New(http.StatusConflict, "COMPLETION_CONFLICT", "artifact key has different content")
		}
		existing.SHA256 = m.SHA256
		existing.ByteSize = m.ByteSize
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Artifact{}, false, err
	}
	var assetID string
	if err = tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&assetID); err != nil {
		return Artifact{}, false, err
	}
	objectKey := fmt.Sprintf("derived/%s/%s/%s", state.SessionID, jobID, assetID)
	if err = s.store.Commit(staged, objectKey); err != nil {
		return Artifact{}, false, err
	}
	committed := true
	defer func() {
		if committed {
			_ = s.store.Remove(objectKey)
		}
	}()
	mime := artifactMIME(in.Kind)
	status := "pending"
	var duration *int64
	_, err = tx.Exec(ctx, `INSERT INTO teaching.media_assets(id,session_id,parent_asset_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,duration_ms,origin_offset_ms,status,expires_at)
		SELECT $1,r.session_id,r.media_asset_id,m.source_record_id,$2,'filesystem',$3,$4,$5,$6,$7,0,$8,LEAST(m.expires_at,ls.content_expires_at)
		FROM teaching.analysis_runs r JOIN teaching.media_assets m ON m.id=r.media_asset_id JOIN teaching.lesson_sessions ls ON ls.id=r.session_id WHERE r.id=$9`, assetID, in.Kind, objectKey, staged.SHA256, mime, staged.Size, duration, status, state.RunID)
	if err != nil {
		return Artifact{}, false, err
	}
	meta := map[string]any{"lease_token": token, "sha256": staged.SHA256, "byte_size": staged.Size, "kind": in.Kind, "timestamp_ms": in.TimestampMS, "storage_generation": state.Generation}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.job_artifacts(job_id,run_id,session_id,lease_token,artifact_key,media_asset_id,metadata) VALUES($1,$2,$3,$4,$5,$6,$7)`, jobID, state.RunID, state.SessionID, token, in.ArtifactKey, assetID, meta)
	if err != nil {
		return Artifact{}, false, err
	}
	// Once COMMIT starts its outcome can be uncertain. Preserve the object on a
	// commit error so a committed row never points at a file we deleted.
	committed = false
	if err = tx.Commit(ctx); err != nil {
		return Artifact{}, false, err
	}
	return Artifact{AssetID: assetID, SHA256: staged.SHA256, ByteSize: staged.Size}, false, nil
}

func (s *Service) Complete(ctx context.Context, jobID, token string, raw []byte) (CompleteResult, error) {
	digest := digestBytes(raw)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CompleteResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	state, duplicate, err := s.lockJobForCompletion(ctx, tx, jobID, token, digest)
	if err != nil {
		return CompleteResult{}, err
	}
	if duplicate {
		return CompleteResult{JobID: jobID, Status: "succeeded", Duplicate: true}, nil
	}
	var expected string
	if err = tx.QueryRow(ctx, `SELECT expected_execution_sha256 FROM teaching.analysis_jobs WHERE id=$1`, jobID).Scan(&expected); err != nil {
		return CompleteResult{}, err
	}
	envelope, result, err := s.validateResult(ctx, tx, state, jobID, raw, expected)
	if err != nil {
		return CompleteResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.job_artifacts(job_id,run_id,session_id,lease_token,artifact_key,metadata) VALUES($1,$2,$3,$4,$5,jsonb_build_object('result',$6::jsonb))
		ON CONFLICT(job_id,artifact_key) DO UPDATE SET metadata=EXCLUDED.metadata`, jobID, state.RunID, state.SessionID, token, "__result/"+state.Stage, raw)
	if err != nil {
		return CompleteResult{}, err
	}
	if state.Stage == "probe" {
		if err = s.applyProbe(ctx, tx, state, result.(ProbeResult)); err != nil {
			return CompleteResult{}, err
		}
	}
	referenced := resultArtifactIDs(state, result)
	if len(referenced) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE teaching.media_assets SET status='ready' WHERE id=ANY($1::uuid[])`, referenced); err != nil {
			return CompleteResult{}, err
		}
	}
	unusedKeys, err := s.removeUnreferencedLeaseArtifacts(ctx, tx, jobID, token, referenced)
	if err != nil {
		return CompleteResult{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='succeeded',progress=100,last_completed_token=$2::uuid,completion_digest=$3,
		worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,execution_deadline_at=NULL,expected_execution_sha256=NULL,error_code=NULL,error_detail=NULL,updated_at=now() WHERE id=$1`, jobID, token, digest)
	if err != nil {
		return CompleteResult{}, err
	}
	if err = s.advance(ctx, tx, state, envelope); err != nil {
		return CompleteResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CompleteResult{}, err
	}
	for _, key := range unusedKeys {
		_ = s.store.Remove(key)
	}
	return CompleteResult{JobID: jobID, Status: "succeeded", Duplicate: false}, nil
}

func (s *Service) lockJobForCompletion(ctx context.Context, tx pgx.Tx, jobID, token, digest string) (lockedJob, bool, error) {
	v := lockedJob{Token: token}
	var jobStatus, runStatus, sessionStatus, rights string
	var leaseToken, lastToken, lastDigest *string
	var leaseExpires, deadline, contentExpires, mediaExpires *time.Time
	var uses, snapshotJSON []byte
	err := tx.QueryRow(ctx, `SELECT r.id::text,r.session_id::text,m.source_record_id::text
		FROM teaching.analysis_jobs j JOIN teaching.analysis_runs r ON r.id=j.run_id
		JOIN teaching.media_assets m ON m.id=r.media_asset_id WHERE j.id=$1`, jobID).
		Scan(&v.RunID, &v.SessionID, &v.SourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, notFound()
	}
	if err != nil {
		return v, false, err
	}
	if err = tx.QueryRow(ctx, `SELECT status,content_expires_at,storage_generation FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, v.SessionID).
		Scan(&sessionStatus, &contentExpires, &v.Generation); err != nil {
		return v, false, err
	}
	if err = tx.QueryRow(ctx, `SELECT rights_status,allowed_uses FROM teaching.source_records WHERE id=$1 FOR UPDATE`, v.SourceID).
		Scan(&rights, &uses); err != nil {
		return v, false, err
	}
	if err = tx.QueryRow(ctx, `SELECT r.media_asset_id::text,r.mode,r.status,r.config_snapshot,m.expires_at
		FROM teaching.analysis_runs r JOIN teaching.media_assets m ON m.id=r.media_asset_id
		WHERE r.id=$1 AND r.session_id=$2 AND m.source_record_id=$3 FOR UPDATE OF r`, v.RunID, v.SessionID, v.SourceID).
		Scan(&v.MediaID, &v.Mode, &runStatus, &snapshotJSON, &mediaExpires); err != nil {
		return v, false, err
	}
	if err = tx.QueryRow(ctx, `SELECT stage,status,lease_token::text,last_completed_token::text,completion_digest,lease_expires_at,execution_deadline_at
		FROM teaching.analysis_jobs WHERE id=$1 AND run_id=$2 FOR UPDATE`, jobID, v.RunID).
		Scan(&v.Stage, &jobStatus, &leaseToken, &lastToken, &lastDigest, &leaseExpires, &deadline); err != nil {
		return v, false, err
	}
	if err = json.Unmarshal(snapshotJSON, &v.Snapshot); err != nil {
		return v, false, err
	}
	if v.Snapshot.MediaLimits.MaxDurationMS <= 0 || v.Snapshot.MediaLimits.MaxVideoHeight <= 0 {
		return v, false, state("analysis run is missing pinned media limits")
	}
	needed := "analysis"
	if v.Mode == "media_prepare" {
		needed = "playback"
	}
	now := time.Now()
	if sessionStatus == "deleting" || sessionStatus == "deleted" || contentExpires == nil || mediaExpires == nil || now.After(*contentExpires) || now.After(*mediaExpires) || rights != "verified" || !jsonArrayContains(uses, needed) {
		return v, false, leaseLost()
	}
	if jobStatus == "succeeded" {
		if runStatus == "cancelled" || runStatus == "failed" {
			return v, false, leaseLost()
		}
		if lastToken != nil && lastDigest != nil && *lastToken == token && *lastDigest == digest {
			return v, true, nil
		}
		return v, false, apperror.New(http.StatusConflict, "COMPLETION_CONFLICT", "completed job has different content or token")
	}
	if runStatus != "running" || jobStatus != "running" || leaseToken == nil || *leaseToken != token || leaseExpires == nil || deadline == nil || now.After(*leaseExpires) || now.After(*deadline) {
		return v, false, leaseLost()
	}
	v.Deadline = *deadline
	return v, false, nil
}

func resultArtifactIDs(state lockedJob, result any) []string {
	ids := []string{}
	switch value := result.(type) {
	case ProbeResult:
		if value.PlaybackAssetID != state.MediaID {
			ids = append(ids, value.PlaybackAssetID)
		}
	case AudioResult:
		if value.AudioAssetID != nil {
			ids = append(ids, *value.AudioAssetID)
		}
	case VideoResult:
		for _, frame := range value.Frames {
			ids = append(ids, frame.AssetID)
		}
	}
	return ids
}

func (s *Service) removeUnreferencedLeaseArtifacts(ctx context.Context, tx pgx.Tx, jobID, token string, referenced []string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT m.id::text,m.object_key FROM teaching.job_artifacts ja JOIN teaching.media_assets m ON m.id=ja.media_asset_id
		WHERE ja.job_id=$1 AND ja.lease_token=$2::uuid AND ja.media_asset_id IS NOT NULL
		AND NOT (ja.media_asset_id=ANY($3::uuid[]))`, jobID, token, referenced)
	if err != nil {
		return nil, err
	}
	var ids, keys []string
	for rows.Next() {
		var id, key string
		if err = rows.Scan(&id, &key); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		keys = append(keys, key)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM teaching.job_artifacts WHERE job_id=$1 AND media_asset_id=ANY($2::uuid[])`, jobID, ids); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM teaching.media_assets WHERE id=ANY($1::uuid[])`, ids); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func (s *Service) Fail(ctx context.Context, jobID, token string, in Fail) (FailResult, error) {
	if strings.TrimSpace(in.ErrorCode) == "" || strings.TrimSpace(in.Message) == "" {
		return FailResult{}, invalid("error_code and message are required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return FailResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	state, err := s.lockActiveJob(ctx, tx, jobID, token)
	if err != nil {
		return FailResult{}, err
	}
	var attempts, max int
	if err = tx.QueryRow(ctx, `SELECT attempts,max_attempts FROM teaching.analysis_jobs WHERE id=$1`, jobID).Scan(&attempts, &max); err != nil {
		return FailResult{}, err
	}
	objectKeys, err := s.removeLeaseArtifacts(ctx, tx, jobID, token)
	if err != nil {
		return FailResult{}, err
	}
	result := FailResult{JobID: jobID}
	if in.Retryable && attempts < max {
		delay := 5 * time.Second
		if attempts > 1 {
			delay = 30 * time.Second
		}
		next := time.Now().UTC().Add(delay)
		result.Status = "queued"
		result.NextRetryAt = &next
		_, err = tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='queued',available_at=$2,worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,execution_deadline_at=NULL,expected_execution_sha256=NULL,progress=0,error_code=$3,error_detail=$4,updated_at=now() WHERE id=$1`, jobID, next, in.ErrorCode, in.Message)
	} else {
		result.Status = "failed"
		_, err = tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='failed',worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,execution_deadline_at=NULL,expected_execution_sha256=NULL,error_code=$2,error_detail=$3,updated_at=now() WHERE id=$1`, jobID, in.ErrorCode, in.Message)
		if err == nil {
			if state.Stage == "probe" {
				err = s.failAfterProbe(ctx, tx, state.RunID, in.ErrorCode)
			} else {
				err = s.advance(ctx, tx, state, resultEnvelope{Stage: state.Stage})
			}
		}
	}
	if err != nil {
		return FailResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return FailResult{}, err
	}
	for _, key := range objectKeys {
		_ = s.store.Remove(key)
	}
	return result, nil
}

func (s *Service) Results(ctx context.Context, p identity.Principal, runID string) (Results, error) {
	run, err := s.Get(ctx, p, runID)
	if err != nil {
		return Results{}, err
	}
	if err = s.requireRunContentReadable(ctx, runID, true); err != nil {
		return Results{}, err
	}
	out := Results{Segments: []Segment{}, Evidence: []json.RawMessage{}, Frames: []Frame{}, Events: []json.RawMessage{}, Limitations: nonNilStrings(run.Limitations), RunStatus: run.Status}
	rows, err := s.pool.Query(ctx, `SELECT j.stage,ja.metadata->'result' FROM teaching.analysis_jobs j JOIN teaching.job_artifacts ja ON ja.job_id=j.id WHERE j.run_id=$1 AND ja.artifact_key LIKE '__result/%'`, runID)
	if err != nil {
		return Results{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var stage string
		var raw []byte
		if err = rows.Scan(&stage, &raw); err != nil {
			return Results{}, err
		}
		switch stage {
		case "audio_analysis":
			var v AudioResult
			if json.Unmarshal(raw, &v) == nil {
				out.Segments = nonNilSegments(v.Segments)
				out.Limitations = appendUniqueAll(out.Limitations, v.Limitations)
			}
		case "video_analysis":
			var v VideoResult
			if json.Unmarshal(raw, &v) == nil {
				out.Frames = nonNilFrames(v.Frames)
				out.Events = v.Events
				if out.Events == nil {
					out.Events = []json.RawMessage{}
				}
				out.Limitations = appendUniqueAll(out.Limitations, v.Limitations)
			}
		case "probe":
			var v ProbeResult
			if json.Unmarshal(raw, &v) == nil {
				out.Limitations = appendUniqueAll(out.Limitations, v.Limitations)
			}
		}
	}
	return out, rows.Err()
}

func (s *Service) requireRunContentReadable(ctx context.Context, runID string, requireMediaCurrent bool) error {
	var sessionStatus, rights string
	var contentExpires, mediaExpires time.Time
	var uses []byte
	err := s.pool.QueryRow(ctx, `SELECT ls.status,ls.content_expires_at,m.expires_at,src.rights_status,src.allowed_uses
		FROM teaching.analysis_runs r JOIN teaching.lesson_sessions ls ON ls.id=r.session_id
		JOIN teaching.media_assets m ON m.id=r.media_asset_id JOIN teaching.source_records src ON src.id=m.source_record_id
		WHERE r.id=$1`, runID).Scan(&sessionStatus, &contentExpires, &mediaExpires, &rights, &uses)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound()
	}
	if err != nil {
		return err
	}
	return contentLifecycleError(sessionStatus, contentExpires, mediaExpires, rights, uses, requireMediaCurrent)
}

func contentLifecycleError(sessionStatus string, contentExpires, mediaExpires time.Time, rights string, uses []byte, requireMediaCurrent bool) error {
	if sessionStatus == "deleting" || sessionStatus == "deleted" {
		return state("classroom content is unavailable")
	}
	if !contentExpires.After(time.Now()) || requireMediaCurrent && !mediaExpires.After(time.Now()) {
		return expired()
	}
	if rights != "verified" || !jsonArrayContains(uses, "analysis") {
		return apperror.New(http.StatusForbidden, "SOURCE_NOT_VERIFIED", "source no longer permits analysis content access")
	}
	return nil
}

func (s *Service) ReapExpired(ctx context.Context) (int, error) {
	ineligible, err := s.finalizeIneligible(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,lease_token::text FROM teaching.analysis_jobs WHERE status='running' AND (lease_expires_at<=now() OR execution_deadline_at<=now()) ORDER BY updated_at LIMIT 100`)
	if err != nil {
		return 0, err
	}
	var pairs [][2]string
	for rows.Next() {
		var p [2]string
		if err = rows.Scan(&p[0], &p[1]); err != nil {
			rows.Close()
			return 0, err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	count := ineligible
	for _, p := range pairs {
		if err = s.reapOne(ctx, p[0], p[1]); err != nil {
			var app *apperror.Error
			if errors.As(err, &app) && app.Code == "LEASE_LOST" {
				continue
			}
			return count, err
		}
		count++
	}
	return count, nil
}

func (s *Service) finalizeIneligible(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.id::text,CASE
		WHEN ls.status IN('deleting','deleted') THEN 'INVALID_STATE'
		WHEN ls.content_expires_at<=now() OR m.expires_at<=now() THEN 'RESOURCE_EXPIRED'
		ELSE 'SOURCE_NOT_VERIFIED' END
		FROM teaching.analysis_runs r JOIN teaching.lesson_sessions ls ON ls.id=r.session_id
		JOIN teaching.media_assets m ON m.id=r.media_asset_id JOIN teaching.source_records src ON src.id=m.source_record_id
		WHERE r.status IN('queued','running') AND (ls.status IN('deleting','deleted') OR ls.content_expires_at<=now() OR m.expires_at<=now()
		 OR src.rights_status<>'verified' OR NOT (src.allowed_uses ? CASE WHEN r.mode='media_prepare' THEN 'playback' ELSE 'analysis' END)) LIMIT 100`)
	if err != nil {
		return 0, err
	}
	var candidates [][2]string
	for rows.Next() {
		var item [2]string
		if err := rows.Scan(&item[0], &item[1]); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	count := 0
	for _, item := range candidates {
		if err := s.finalizeIneligibleRun(ctx, item[0], item[1]); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (s *Service) finalizeIneligibleRun(ctx context.Context, runID, code string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var status string
	var snapshotJSON []byte
	err = tx.QueryRow(ctx, `SELECT r.status,r.config_snapshot FROM teaching.analysis_runs r JOIN teaching.lesson_sessions ls ON ls.id=r.session_id WHERE r.id=$1 FOR UPDATE OF ls,r`, runID).Scan(&status, &snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if terminalRun(status) {
		return tx.Commit(ctx)
	}
	var snap snapshot
	if err = json.Unmarshal(snapshotJSON, &snap); err != nil {
		return err
	}
	keys, err := s.removePendingRunArtifacts(ctx, tx, runID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='cancelled',worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,execution_deadline_at=NULL,expected_execution_sha256=NULL,error_code=$2,updated_at=now() WHERE run_id=$1 AND status IN('queued','running')`, runID, code)
	if err != nil {
		return err
	}
	for _, stage := range snap.StagePlan {
		if _, err = tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,error_code) VALUES($1,$2,'skipped',$3,$4) ON CONFLICT DO NOTHING`, runID, stage, s.maxAttempts(stage), code); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='failed',error_code=$2,finished_at=now() WHERE id=$1`, runID, code); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	for _, key := range keys {
		_ = s.store.Remove(key)
	}
	return nil
}

func (s *Service) reapOne(ctx context.Context, jobID, token string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var state lockedJob
	var attempts, max int
	var status string
	err = tx.QueryRow(ctx, `SELECT r.id::text,r.session_id::text,r.media_asset_id::text,j.stage,r.mode,j.attempts,j.max_attempts,j.status
		FROM teaching.analysis_jobs j JOIN teaching.analysis_runs r ON r.id=j.run_id JOIN teaching.lesson_sessions ls ON ls.id=r.session_id
		WHERE j.id=$1 AND j.lease_token=$2::uuid AND j.status='running'
		AND (j.lease_expires_at<=now() OR j.execution_deadline_at<=now())
		FOR UPDATE OF ls,r,j`, jobID, token).Scan(&state.RunID, &state.SessionID, &state.MediaID, &state.Stage, &state.Mode, &attempts, &max, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return leaseLost()
	}
	objectKeys, err := s.removeLeaseArtifacts(ctx, tx, jobID, token)
	if err != nil {
		return err
	}
	if err != nil {
		return err
	}
	if attempts < max {
		delay := 5 * time.Second
		if attempts > 1 {
			delay = 30 * time.Second
		}
		_, err = tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='queued',available_at=now()+$2::interval,worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,execution_deadline_at=NULL,expected_execution_sha256=NULL,progress=0,error_code='JOB_TIMEOUT',error_detail='lease or execution deadline expired',updated_at=now() WHERE id=$1`, jobID, delay.String())
	} else {
		_, err = tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='failed',worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,execution_deadline_at=NULL,expected_execution_sha256=NULL,error_code='JOB_TIMEOUT',error_detail='final attempt expired',updated_at=now() WHERE id=$1`, jobID)
		if err == nil {
			if state.Stage == "probe" {
				err = s.failAfterProbe(ctx, tx, state.RunID, "JOB_TIMEOUT")
			} else {
				err = s.advance(ctx, tx, state, resultEnvelope{Stage: state.Stage})
			}
		}
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, key := range objectKeys {
		_ = s.store.Remove(key)
	}
	return nil
}

func (s *Service) removeLeaseArtifacts(ctx context.Context, tx pgx.Tx, jobID, token string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT m.id::text,m.object_key FROM teaching.job_artifacts ja JOIN teaching.media_assets m ON m.id=ja.media_asset_id
		WHERE ja.job_id=$1 AND ja.lease_token=$2::uuid AND ja.artifact_key NOT LIKE '__result/%'`, jobID, token)
	if err != nil {
		return nil, err
	}
	var ids, keys []string
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		keys = append(keys, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM teaching.job_artifacts WHERE job_id=$1 AND lease_token=$2::uuid AND media_asset_id=ANY($3::uuid[])`, jobID, token, ids); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM teaching.media_assets WHERE id=ANY($1::uuid[])`, ids); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func (s *Service) removePendingRunArtifacts(ctx context.Context, tx pgx.Tx, runID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT m.id::text,m.object_key FROM teaching.job_artifacts ja JOIN teaching.media_assets m ON m.id=ja.media_asset_id WHERE ja.run_id=$1 AND m.status='pending'`, runID)
	if err != nil {
		return nil, err
	}
	var ids, keys []string
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		keys = append(keys, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM teaching.job_artifacts WHERE run_id=$1 AND media_asset_id=ANY($2::uuid[])`, runID, ids); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM teaching.media_assets WHERE id=ANY($1::uuid[])`, ids); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func (s *Service) RunReaper(ctx context.Context, interval time.Duration) {
	_, _ = s.ReapExpired(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.ReapExpired(ctx)
		}
	}
}

func (s *Service) validateResult(ctx context.Context, tx pgx.Tx, state lockedJob, jobID string, raw []byte, expected string) (resultEnvelope, any, error) {
	var envelope resultEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return envelope, nil, invalidResult("result envelope is invalid")
	}
	if envelope.SchemaVersion != SchemaVersion || envelope.JobID != jobID || envelope.RunID != state.RunID || envelope.SessionID != state.SessionID || envelope.MediaAssetID != state.MediaID || envelope.Stage != state.Stage || digestJSON(envelope.Execution) != expected {
		return envelope, nil, invalidResult("result identity or execution does not match the claim")
	}
	switch state.Stage {
	case "probe":
		var v ProbeResult
		if err := decodeStrict(raw, &v); err != nil {
			return envelope, nil, invalidResult("probe result is invalid")
		}
		if v.DurationMS < 1 || v.DurationMS > state.Snapshot.MediaLimits.MaxDurationMS || v.Width < 1 || v.Height < 1 || v.Height > state.Snapshot.MediaLimits.MaxVideoHeight || (v.VideoCodec != "h264" && v.VideoCodec != "hevc") || v.OriginOffsetMS != 0 {
			return envelope, nil, invalidResult("probe values are outside configured limits")
		}
		if err := s.requireArtifact(ctx, tx, state, jobID, v.PlaybackAssetID, "proxy", true); err != nil {
			return envelope, nil, err
		}
		return envelope, v, nil
	case "audio_analysis":
		var v AudioResult
		if err := decodeStrict(raw, &v); err != nil {
			return envelope, nil, invalidResult("audio result is invalid")
		}
		expectedModel := state.Snapshot.Executions["audio_analysis"].ASRModel
		if expectedModel == nil || v.Model.Name != expectedModel.Name || v.Model.Revision != expectedModel.Revision {
			return envelope, nil, invalidResult("ASR model does not match the claim")
		}
		duration, err := s.mediaDuration(ctx, tx, state.MediaID)
		if err != nil {
			return envelope, nil, err
		}
		if err := validateIntervals(v.Coverage, duration); err != nil {
			return envelope, nil, err
		}
		var previousStart int64 = -1
		for i, seg := range v.Segments {
			if seg.SegmentNo != i || strings.TrimSpace(seg.Text) == "" || strings.TrimSpace(seg.Speaker) == "" || seg.StartMS < previousStart || seg.StartMS < 0 || seg.EndMS <= seg.StartMS || seg.EndMS > duration || !covered(seg.StartMS, seg.EndMS, v.Coverage) {
				return envelope, nil, invalidResult("audio segment timeline is invalid")
			}
			previousStart = seg.StartMS
		}
		if v.AudioAssetID != nil {
			if err := s.requireArtifact(ctx, tx, state, jobID, *v.AudioAssetID, "audio", false); err != nil {
				return envelope, nil, err
			}
		}
		return envelope, v, nil
	case "video_analysis":
		var v VideoResult
		if err := decodeStrict(raw, &v); err != nil {
			return envelope, nil, invalidResult("video result is invalid")
		}
		params := state.Snapshot.Parameters
		if len(v.Events) != 0 || v.Model != nil || v.Sampling.IntervalMS != params.KeyframeIntervalMS || v.Sampling.MaxFrames != params.MaxKeyframes || len(v.Frames) > params.MaxKeyframes {
			return envelope, nil, invalidResult("video result does not match P0 sampling")
		}
		duration, err := s.mediaDuration(ctx, tx, state.MediaID)
		if err != nil {
			return envelope, nil, err
		}
		if err := validateIntervals(v.Coverage, duration); err != nil {
			return envelope, nil, err
		}
		for _, f := range v.Frames {
			if f.TimestampMS < 0 || f.TimestampMS >= duration || !pointCovered(f.TimestampMS, v.Coverage) {
				return envelope, nil, invalidResult("frame timeline is invalid")
			}
			if err := s.requireArtifact(ctx, tx, state, jobID, f.AssetID, "keyframe", false); err != nil {
				return envelope, nil, err
			}
		}
		return envelope, v, nil
	default:
		return envelope, nil, invalidResult("worker cannot complete this stage")
	}
}

func (s *Service) mediaDuration(ctx context.Context, tx pgx.Tx, mediaID string) (int64, error) {
	var duration int64
	if err := tx.QueryRow(ctx, `SELECT duration_ms FROM teaching.media_assets WHERE id=$1`, mediaID).Scan(&duration); err != nil {
		return 0, err
	}
	return duration, nil
}

func (s *Service) requireArtifact(ctx context.Context, tx pgx.Tx, state lockedJob, jobID, assetID, kind string, allowSource bool) error {
	if allowSource && assetID == state.MediaID {
		return nil
	}
	var storedKind, lease string
	var generation int64
	err := tx.QueryRow(ctx, `SELECT m.kind,ja.lease_token::text,(ja.metadata->>'storage_generation')::bigint FROM teaching.job_artifacts ja JOIN teaching.media_assets m ON m.id=ja.media_asset_id WHERE ja.job_id=$1 AND ja.media_asset_id=$2 AND m.session_id=$3`, jobID, assetID, state.SessionID).Scan(&storedKind, &lease, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return invalidResult("result references an artifact outside the current job")
	}
	if err != nil {
		return err
	}
	if storedKind != kind || generation != state.Generation || lease != state.Token {
		return invalidResult("artifact kind or storage generation does not match")
	}
	return nil
}

func (s *Service) applyProbe(ctx context.Context, tx pgx.Tx, state lockedJob, v ProbeResult) error {
	_, err := tx.Exec(ctx, `UPDATE teaching.media_assets SET duration_ms=$2,status='ready',playback_asset_id=$3 WHERE id=$1`, state.MediaID, v.DurationMS, v.PlaybackAssetID)
	if err != nil {
		return err
	}
	if v.PlaybackAssetID != state.MediaID {
		_, err = tx.Exec(ctx, `UPDATE teaching.media_assets SET duration_ms=$2,status='ready' WHERE id=$1`, v.PlaybackAssetID, v.DurationMS)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE teaching.lesson_sessions SET status='ready' WHERE id=$1 AND status='planned'`, state.SessionID)
	return err
}

func (s *Service) advance(ctx context.Context, tx pgx.Tx, state lockedJob, envelope resultEnvelope) error {
	if state.Stage == "probe" {
		if state.Mode == "media_prepare" {
			_, err := tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='succeeded',error_code=NULL,finished_at=now() WHERE id=$1`, state.RunID)
			return err
		}
		var hasAudio bool
		if envelope.Stage == "probe" {
			var raw []byte
			err := tx.QueryRow(ctx, `SELECT metadata->'result' FROM teaching.job_artifacts WHERE job_id=(SELECT id FROM teaching.analysis_jobs WHERE run_id=$1 AND stage='probe') AND artifact_key='__result/probe'`, state.RunID).Scan(&raw)
			if err != nil {
				return err
			}
			var v ProbeResult
			if err = json.Unmarshal(raw, &v); err != nil {
				return err
			}
			hasAudio = v.HasAudio
		}
		if hasAudio {
			_, err := tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,max_attempts) VALUES($1,'audio_analysis',$2) ON CONFLICT DO NOTHING`, state.RunID, s.cfg.MediaJobAttempts)
			if err != nil {
				return err
			}
		} else {
			_, err := tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,error_code) VALUES($1,'audio_analysis','skipped',$2,'NO_AUDIO') ON CONFLICT DO NOTHING`, state.RunID, s.cfg.MediaJobAttempts)
			if err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,max_attempts) VALUES($1,'video_analysis',$2) ON CONFLICT DO NOTHING`, state.RunID, s.cfg.MediaJobAttempts)
		return err
	}
	if state.Stage == "audio_analysis" || state.Stage == "video_analysis" {
		var remaining int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM teaching.analysis_jobs WHERE run_id=$1 AND stage IN('audio_analysis','video_analysis') AND status NOT IN('succeeded','failed','skipped','cancelled')`, state.RunID).Scan(&remaining); err != nil {
			return err
		}
		if remaining > 0 {
			return nil
		}
		return s.finishPhaseFour(ctx, tx, state.RunID)
	}
	return nil
}

func (s *Service) finishPhaseFour(ctx context.Context, tx pgx.Tx, runID string) error {
	for _, stage := range []string{"evidence", "report", "validate"} {
		_, err := tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,error_code) VALUES($1,$2,'skipped',$3,'PHASE_5_PENDING') ON CONFLICT DO NOTHING`, runID, stage, s.maxAttempts(stage))
		if err != nil {
			return err
		}
	}
	var probeStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM teaching.analysis_jobs WHERE run_id=$1 AND stage='probe'`, runID).Scan(&probeStatus); err != nil {
		return err
	}
	status := "partial"
	code := "PHASE_5_PENDING"
	if probeStatus == "failed" {
		status = "failed"
		code = "PROBE_FAILED"
	}
	_, err := tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status=$2,error_code=$3,finished_at=now() WHERE id=$1`, runID, status, code)
	return err
}

func (s *Service) failAfterProbe(ctx context.Context, tx pgx.Tx, runID, code string) error {
	for _, stage := range []string{"audio_analysis", "video_analysis", "evidence", "report", "validate"} {
		if _, err := tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,error_code)
			VALUES($1,$2,'skipped',$3,'DEPENDENCY_FAILED') ON CONFLICT DO NOTHING`, runID, stage, s.maxAttempts(stage)); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='failed',error_code=$2,finished_at=now() WHERE id=$1`, runID, code)
	return err
}

func (s *Service) newSnapshot(mode string) snapshot {
	params := Parameters{ConfigProfile: pipelineVersion, KeyframeIntervalMS: s.cfg.KeyframeIntervalMS, MaxKeyframes: s.cfg.MaxKeyframes, ASRDevice: s.cfg.ASRDevice}
	// Match the contract's sorted-key, compact JSON used by Python workers.
	// Keep execution/result digests unchanged for already persisted leases.
	paramHash := digestJSON(map[string]any{
		"asr_device": params.ASRDevice, "config_profile": params.ConfigProfile,
		"keyframe_interval_ms": params.KeyframeIntervalMS, "max_keyframes": params.MaxKeyframes,
	})
	base := Execution{ProcessorVersion: s.cfg.ProcessorVersion, FFmpegBuildSHA256: s.cfg.FFmpegSHA256, ParametersSHA256: paramHash}
	probe := base
	video := base
	audio := base
	audio.ASRModel = &Model{Name: s.cfg.ASRModelName, Revision: s.cfg.ASRModelRevision}
	plan := []string{"probe", "audio_analysis", "video_analysis", "evidence", "report", "validate"}
	if mode == "media_prepare" {
		plan = []string{"probe"}
	}
	return snapshot{StagePlan: plan, Parameters: params, Executions: map[string]Execution{"probe": probe, "audio_analysis": audio, "video_analysis": video},
		MediaLimits: mediaLimits{MaxDurationMS: s.cfg.MaxMediaDurationMS, MaxVideoHeight: s.cfg.MaxVideoHeight}}
}

func (s *Service) timeout(stage string) time.Duration {
	switch stage {
	case "probe":
		return s.cfg.ProbeTimeout
	case "audio_analysis":
		return s.cfg.ASRTimeout
	default:
		return s.cfg.VideoTimeout
	}
}
func (s *Service) maxAttempts(stage string) int {
	if mediaStages[stage] || stage == "evidence" || stage == "validate" {
		return s.cfg.MediaJobAttempts
	}
	return 1
}

func decodeStrict(raw []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	err := d.Decode(&struct{}{})
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
func digestJSON(value any) string {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return digestBytes(bytes.TrimSuffix(body.Bytes(), []byte{'\n'}))
}
func digestBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func printableASCII(v string) bool {
	for _, c := range v {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
func jsonArrayContains(raw []byte, wanted string) bool {
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	for _, v := range values {
		if v == wanted {
			return true
		}
	}
	return false
}
func validCapabilities(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if mediaStages[v] && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func artifactAllowed(stage, kind string, timestamp *int64) bool {
	switch stage {
	case "probe":
		return kind == "proxy" && timestamp == nil
	case "audio_analysis":
		return kind == "audio" && timestamp == nil
	case "video_analysis":
		return kind == "keyframe" && timestamp != nil && *timestamp >= 0
	}
	return false
}
func artifactMIME(kind string) string {
	switch kind {
	case "proxy":
		return "video/mp4"
	case "audio":
		return "audio/wav"
	default:
		return "image/jpeg"
	}
}
func validateIntervals(values []Interval, duration int64) error {
	var end int64 = -1
	for _, v := range values {
		if v.StartMS < 0 || v.EndMS <= v.StartMS || v.EndMS > duration || v.StartMS < end {
			return invalidResult("coverage intervals are invalid")
		}
		end = v.EndMS
	}
	return nil
}
func covered(start, end int64, values []Interval) bool {
	for _, v := range values {
		if start >= v.StartMS && end <= v.EndMS {
			return true
		}
	}
	return false
}
func pointCovered(point int64, values []Interval) bool {
	for _, v := range values {
		if point >= v.StartMS && point < v.EndMS {
			return true
		}
	}
	return false
}
func canAnalyze(p identity.Principal, teacher string) bool {
	return p.Has("sys_admin") || (p.Has("teacher") && p.UserID == teacher)
}
func canView(p identity.Principal, teacher, college string) bool {
	return p.Has("sys_admin") || p.Scoped("supervisor", college) || (p.Has("teacher") && p.UserID == teacher)
}
func terminalRun(status string) bool {
	return status == "succeeded" || status == "partial" || status == "failed" || status == "cancelled"
}
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
func nonNilSegments(v []Segment) []Segment {
	if v == nil {
		return []Segment{}
	}
	return v
}
func nonNilFrames(v []Frame) []Frame {
	if v == nil {
		return []Frame{}
	}
	return v
}
func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}
func appendUniqueAll(values, extra []string) []string {
	for _, v := range extra {
		values = appendUnique(values, v)
	}
	return values
}
func invalid(message string) error {
	return apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", message)
}
func invalidResult(message string) error {
	return apperror.New(http.StatusUnprocessableEntity, "INVALID_RESULT", message)
}
func notFound() error { return apperror.New(http.StatusNotFound, "NOT_FOUND", "resource not found") }
func forbidden() error {
	return apperror.New(http.StatusForbidden, "FORBIDDEN", "operation is not permitted")
}
func state(message string) error { return apperror.New(http.StatusConflict, "INVALID_STATE", message) }
func expired() error {
	return apperror.New(http.StatusGone, "RESOURCE_EXPIRED", "resource has expired")
}
func leaseLost() error {
	return apperror.New(http.StatusConflict, "LEASE_LOST", "job lease is no longer valid")
}
func isUnique(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}
