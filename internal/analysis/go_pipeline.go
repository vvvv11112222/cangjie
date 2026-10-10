package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var dimensionCodes = []string{"content", "pace", "thinking", "expression", "management", "technology"}

var errBudgetExhausted = errors.New("model budget is exhausted or requires reconciliation")

type goJob struct{ ID, RunID, SessionID, MediaID, Stage, Token string }

func (s *Service) ProcessNextGoJob(ctx context.Context) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var j goJob
	err = tx.QueryRow(ctx, `WITH candidate AS (
		SELECT j.id FROM teaching.analysis_jobs j JOIN teaching.analysis_runs r ON r.id=j.run_id
		WHERE j.status='queued' AND j.stage IN('evidence','report','validate') AND j.available_at<=now()
		AND r.status IN('queued','running') ORDER BY j.priority DESC,j.available_at,j.created_at LIMIT 1 FOR UPDATE OF j SKIP LOCKED)
		UPDATE teaching.analysis_jobs j SET status='running',attempts=attempts+1,worker_id='go:reporting',lease_token=gen_random_uuid(),
		lease_expires_at=now()+$1::interval,execution_deadline_at=now()+$2::interval,expected_execution_sha256=$3,updated_at=now()
		FROM candidate c, teaching.analysis_runs r WHERE j.id=c.id AND r.id=j.run_id
		RETURNING j.id::text,j.run_id::text,r.session_id::text,r.media_asset_id::text,j.stage,j.lease_token::text`,
		s.cfg.Lease.String(), s.cfg.GoStageTimeout.String(), digestJSON(map[string]string{"executor": "go-reporting-v1"})).
		Scan(&j.ID, &j.RunID, &j.SessionID, &j.MediaID, &j.Stage, &j.Token)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='running' WHERE id=$1 AND status='queued'`, j.RunID); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	switch j.Stage {
	case "evidence":
		err = s.executeEvidence(ctx, j)
	case "report":
		err = s.executeReport(ctx, j)
	case "validate":
		err = s.executeValidate(ctx, j)
	}
	if err != nil {
		if errors.Is(err, errBudgetExhausted) {
			return true, s.skipGoJob(ctx, j, "BUDGET_EXHAUSTED")
		}
		return true, s.failGoJob(ctx, j, err)
	}
	return true, nil
}

func (s *Service) RunGoWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		for {
			did, _ := s.ProcessNextGoJob(ctx)
			if !did {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) keepGoLease(ctx context.Context, j goJob, stop <-chan struct{}) {
	interval := s.cfg.Lease / 3
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-t.C:
			_, _ = s.pool.Exec(ctx, `UPDATE teaching.analysis_jobs SET lease_expires_at=LEAST(now()+$3::interval,execution_deadline_at),updated_at=now() WHERE id=$1 AND status='running' AND lease_token=$2::uuid`, j.ID, j.Token, s.cfg.Lease.String())
		}
	}
}

func (s *Service) executeEvidence(ctx context.Context, j goJob) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `SELECT 1 FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, j.SessionID); err != nil {
		return err
	}
	if err = s.assertGoLease(ctx, tx, j); err != nil {
		return err
	}
	if err = s.createFrameEvidence(ctx, tx, j); err != nil {
		return err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT ja.metadata->'result' FROM teaching.analysis_jobs aj JOIN teaching.job_artifacts ja ON ja.job_id=aj.id
		WHERE aj.run_id=$1 AND aj.stage='audio_analysis' AND aj.status='succeeded' AND ja.artifact_key='__result/audio_analysis'`, j.RunID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.finishWithoutReport(ctx, tx, j, "NO_TRANSCRIPT")
	}
	if err != nil {
		return err
	}
	var audio AudioResult
	if err = json.Unmarshal(raw, &audio); err != nil {
		return err
	}
	if len(audio.Segments) == 0 {
		return s.finishWithoutReport(ctx, tx, j, "NO_SPEECH")
	}
	var requester string
	if err = tx.QueryRow(ctx, `SELECT requested_by::text FROM teaching.analysis_runs WHERE id=$1`, j.RunID).Scan(&requester); err != nil {
		return err
	}
	for _, seg := range audio.Segments {
		if _, err = tx.Exec(ctx, `INSERT INTO teaching.transcript_segments(run_id,session_id,segment_no,start_ms,end_ms,speaker_label,text_content)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(run_id,segment_no) DO NOTHING`, j.RunID, j.SessionID, seg.SegmentNo, seg.StartMS, seg.EndMS, seg.Speaker, seg.Text); err != nil {
			return err
		}
	}
	var revisionID string
	err = tx.QueryRow(ctx, `INSERT INTO teaching.transcript_revisions(session_id,media_asset_id,source_run_id,revision_no,source_type,content_sha256,created_by,reason)
		VALUES($1,$2,$3,(SELECT COALESCE(max(revision_no),0)+1 FROM teaching.transcript_revisions WHERE session_id=$1 AND media_asset_id=$2),'asr',$4,$5,'ASR analysis') RETURNING id::text`,
		j.SessionID, j.MediaID, j.RunID, canonicalDigest(audio.Segments), requester).Scan(&revisionID)
	if err != nil {
		return err
	}
	for _, seg := range audio.Segments {
		if _, err = tx.Exec(ctx, `INSERT INTO teaching.transcript_revision_segments(revision_id,segment_no,start_ms,end_ms,text_content,speaker_label) VALUES($1,$2,$3,$4,$5,$6)`, revisionID, seg.SegmentNo, seg.StartMS, seg.EndMS, seg.Text, seg.Speaker); err != nil {
			return err
		}
		var segmentID string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM teaching.transcript_segments WHERE run_id=$1 AND segment_no=$2`, j.RunID, seg.SegmentNo).Scan(&segmentID); err != nil {
			return err
		}
		prov := map[string]any{"source_revision_id": revisionID, "source_segment_no": seg.SegmentNo, "asr_model": audio.Model}
		if _, err = tx.Exec(ctx, `INSERT INTO teaching.evidence_items(run_id,session_id,media_asset_id,transcript_segment_id,kind,start_ms,end_ms,description,provenance) VALUES($1,$2,$3,$4,'transcript',$5,$6,$7,$8)`, j.RunID, j.SessionID, j.MediaID, segmentID, seg.StartMS, seg.EndMS, seg.Text, prov); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.lesson_sessions SET transcript_lock_version=transcript_lock_version+1 WHERE id=$1`, j.SessionID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.job_artifacts(job_id,run_id,session_id,lease_token,artifact_key,metadata) VALUES($1,$2,$3,$4,'__result/evidence',jsonb_build_object('transcript_revision_id',$5::text))`, j.ID, j.RunID, j.SessionID, j.Token, revisionID)
	if err != nil {
		return err
	}
	if err = s.completeGoJob(ctx, tx, j); err != nil {
		return err
	}
	reportConfig, err := s.reportConfigForRun(ctx, j.RunID)
	if err != nil {
		return err
	}
	if reportConfig.Enabled {
		_, err = tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,max_attempts) VALUES($1,'report',1) ON CONFLICT DO NOTHING`, j.RunID)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,error_code) VALUES($1,'report','skipped',1,'REPORT_DISABLED'),($1,'validate','skipped',$2,'REPORT_DISABLED') ON CONFLICT DO NOTHING`, j.RunID, s.maxAttempts("validate"))
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='partial',error_code='REPORT_DISABLED',finished_at=now() WHERE id=$1`, j.RunID)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) createFrameEvidence(ctx context.Context, tx pgx.Tx, j goJob) error {
	var videoRaw []byte
	err := tx.QueryRow(ctx, `SELECT ja.metadata->'result' FROM teaching.analysis_jobs aj JOIN teaching.job_artifacts ja ON ja.job_id=aj.id
		WHERE aj.run_id=$1 AND aj.stage='video_analysis' AND aj.status='succeeded' AND ja.artifact_key='__result/video_analysis'`, j.RunID).Scan(&videoRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var video VideoResult
	if err = json.Unmarshal(videoRaw, &video); err != nil {
		return err
	}
	for _, frame := range video.Frames {
		prov := map[string]any{"sampling": video.Sampling, "processor_version": video.Execution.ProcessorVersion, "limitations": video.Limitations}
		if _, err = tx.Exec(ctx, `INSERT INTO teaching.evidence_items(run_id,session_id,media_asset_id,frame_asset_id,kind,start_ms,end_ms,description,provenance) VALUES($1,$2,$3,$4,'frame',$5,$6,'课堂关键帧',$7)`, j.RunID, j.SessionID, j.MediaID, frame.AssetID, frame.TimestampMS, frame.TimestampMS+1, prov); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) finishWithoutReport(ctx context.Context, tx pgx.Tx, j goJob, code string) error {
	if err := s.completeGoJob(ctx, tx, j); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,error_code) VALUES($1,'report','skipped',1,$2),($1,'validate','skipped',$3,$2) ON CONFLICT DO NOTHING`, j.RunID, code, s.maxAttempts("validate"))
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='partial',error_code=$2,finished_at=now() WHERE id=$1`, j.RunID, code)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) executeReport(ctx context.Context, j goJob) error {
	input, err := s.buildModelInput(ctx, j)
	if err != nil {
		return err
	}
	manifestRaw, _ := json.Marshal(input)
	manifestSHA := digestBytes(manifestRaw)
	reportConfig, err := s.reportConfigForRun(ctx, j.RunID)
	if err != nil {
		return err
	}
	requestBody, err := buildReportRequest(input, reportConfig)
	if err != nil {
		return err
	}
	requestSHA := digestBytes(requestBody)
	reservedMicros, err := tokenCostMicros(int64(reportConfig.MaxInputTokens), int64(reportConfig.MaxOutputTokens), reportConfig.InputPriceMicros, reportConfig.OutputPriceMicros)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err = s.lockReportDispatch(ctx, tx, j); err != nil {
		return err
	}
	period := billingPeriod(time.Now(), s.cfg.ReportBudgetTimezone)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, reportConfig.Currency+":"+period); err != nil {
		return err
	}
	var usedMicros int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(round(sum(CASE WHEN status IN('reserved','unknown') THEN reserved_cost ELSE actual_cost END)*1000000),0)::bigint
		FROM teaching.model_calls WHERE currency=$1 AND billing_period=$2::date`, reportConfig.Currency, period).Scan(&usedMicros); err != nil {
		return err
	}
	var overReservationPending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.model_calls WHERE currency=$1 AND billing_period=$2::date
		AND status IN('succeeded','failed') AND actual_cost>reserved_cost AND reconciled_by IS NULL)`, reportConfig.Currency, period).Scan(&overReservationPending); err != nil {
		return err
	}
	if overReservationPending {
		return errBudgetExhausted
	}
	if s.cfg.ReportMonthlyBudgetMicros <= 0 || usedMicros > s.cfg.ReportMonthlyBudgetMicros-reservedMicros {
		return errBudgetExhausted
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.job_artifacts(job_id,run_id,session_id,lease_token,artifact_key,metadata) VALUES($1,$2,$3,$4,'__model_input',jsonb_build_object('model_input',$5::jsonb,'manifest_sha256',$6::text))`, j.ID, j.RunID, j.SessionID, j.Token, manifestRaw, manifestSHA)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.model_calls(run_id,job_id,call_key,provider,model_name,prompt_version,status,currency,reserved_cost,billing_period,price_version,request_sha256,dispatch_started_at)
		VALUES($1,$2,$3,'openai-compatible',$4,$5,'reserved',$6,$7::numeric/1000000,$8::date,$9,$10,now())`,
		j.RunID, j.ID, "report:"+j.ID, reportConfig.Model.Name, reportConfig.PromptVersion, reportConfig.Currency, reservedMicros, period, reportConfig.PriceVersion, requestSHA)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	stopHeartbeat := make(chan struct{})
	go s.keepGoLease(ctx, j, stopHeartbeat)
	defer close(stopHeartbeat)
	callResult, err := s.callReportModel(ctx, requestBody)
	if err != nil {
		_ = s.markModelCallUnknown(ctx, j.ID)
		return err
	}
	if !callResult.UsageKnown {
		_ = s.markModelCallUnknown(ctx, j.ID)
		return fmt.Errorf("report provider omitted billable token usage")
	}
	actualMicros, err := tokenCostMicros(callResult.InputTokens, callResult.OutputTokens, reportConfig.InputPriceMicros, reportConfig.OutputPriceMicros)
	if err != nil {
		_ = s.markModelCallUnknown(ctx, j.ID)
		return err
	}
	if err = s.settleModelCall(ctx, j.ID, callResult, actualMicros); err != nil {
		return err
	}
	candidateRaw, _ := json.Marshal(callResult.Candidate)
	tx, err = s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	active, err := s.lockReportResultAcceptance(ctx, tx, j)
	if err != nil {
		return err
	}
	if !active {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.job_artifacts(job_id,run_id,session_id,lease_token,artifact_key,metadata) VALUES($1,$2,$3,$4,'__report_candidate',jsonb_build_object('report_candidate',$5::jsonb))`, j.ID, j.RunID, j.SessionID, j.Token, candidateRaw)
	if err != nil {
		return err
	}
	if err = s.completeGoJob(ctx, tx, j); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,max_attempts) VALUES($1,'validate',$2) ON CONFLICT DO NOTHING`, j.RunID, s.maxAttempts("validate"))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) lockReportDispatch(ctx context.Context, tx pgx.Tx, j goJob) error {
	var sessionStatus string
	var contentExpires time.Time
	if err := tx.QueryRow(ctx, `SELECT status,content_expires_at FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, j.SessionID).Scan(&sessionStatus, &contentExpires); err != nil {
		return err
	}
	var sourceID, runStatus string
	var mediaExpires time.Time
	err := tx.QueryRow(ctx, `SELECT m.source_record_id::text,m.expires_at,r.status FROM teaching.analysis_runs r
		JOIN teaching.media_assets m ON m.id=r.media_asset_id WHERE r.id=$1 AND r.session_id=$2`, j.RunID, j.SessionID).
		Scan(&sourceID, &mediaExpires, &runStatus)
	if err != nil {
		return err
	}
	var rights string
	var uses []byte
	var external bool
	if err = tx.QueryRow(ctx, `SELECT rights_status,allowed_uses,external_processing_allowed FROM teaching.source_records WHERE id=$1 FOR UPDATE`, sourceID).Scan(&rights, &uses, &external); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT status FROM teaching.analysis_runs WHERE id=$1 FOR UPDATE`, j.RunID).Scan(&runStatus); err != nil {
		return err
	}
	if sessionStatus == "deleting" || sessionStatus == "deleted" || !contentExpires.After(time.Now()) || !mediaExpires.After(time.Now()) || runStatus != "running" || rights != "verified" || !jsonArrayContains(uses, "analysis") || !external {
		return fmt.Errorf("source no longer permits external report processing")
	}
	if err = s.assertGoLease(ctx, tx, j); err != nil {
		return err
	}
	var unresolved bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.model_calls mc JOIN teaching.analysis_runs r ON r.id=mc.run_id
		WHERE r.session_id=$1 AND mc.job_id<>$2 AND mc.dispatch_started_at IS NOT NULL AND mc.status IN('reserved','unknown'))`, j.SessionID, j.ID).Scan(&unresolved); err != nil {
		return err
	}
	if unresolved {
		return fmt.Errorf("classroom has an unresolved model call requiring reconciliation")
	}
	return nil
}

func (s *Service) lockReportResultAcceptance(ctx context.Context, tx pgx.Tx, j goJob) (bool, error) {
	var sessionStatus string
	var contentExpires time.Time
	if err := tx.QueryRow(ctx, `SELECT status,content_expires_at FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, j.SessionID).Scan(&sessionStatus, &contentExpires); err != nil {
		return false, err
	}
	var sourceID string
	var mediaExpires time.Time
	if err := tx.QueryRow(ctx, `SELECT m.source_record_id::text,m.expires_at FROM teaching.analysis_runs r JOIN teaching.media_assets m ON m.id=r.media_asset_id WHERE r.id=$1`, j.RunID).Scan(&sourceID, &mediaExpires); err != nil {
		return false, err
	}
	var rights string
	var uses []byte
	var external bool
	if err := tx.QueryRow(ctx, `SELECT rights_status,allowed_uses,external_processing_allowed FROM teaching.source_records WHERE id=$1 FOR UPDATE`, sourceID).Scan(&rights, &uses, &external); err != nil {
		return false, err
	}
	var runStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM teaching.analysis_runs WHERE id=$1 FOR UPDATE`, j.RunID).Scan(&runStatus); err != nil {
		return false, err
	}
	var jobActive bool
	if err := tx.QueryRow(ctx, `SELECT status='running' AND lease_token=$2::uuid AND lease_expires_at>now() FROM teaching.analysis_jobs WHERE id=$1 FOR UPDATE`, j.ID, j.Token).Scan(&jobActive); err != nil {
		return false, err
	}
	active := jobActive && runStatus == "running" && sessionStatus != "deleting" && sessionStatus != "deleted" && contentExpires.After(time.Now()) && mediaExpires.After(time.Now()) && rights == "verified" && jsonArrayContains(uses, "analysis") && external
	return active, nil
}

func (s *Service) markModelCallUnknown(ctx context.Context, jobID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE teaching.model_calls SET status='unknown' WHERE job_id=$1 AND dispatch_started_at IS NOT NULL AND status='reserved'`, jobID)
	return err
}

func (s *Service) settleModelCall(ctx context.Context, jobID string, result modelCallResult, actualMicros int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE teaching.model_calls SET status='succeeded',actual_cost=$2::numeric/1000000,
		input_tokens=$3,output_tokens=$4,provider_request_id=NULLIF($5,''),settled_at=now() WHERE job_id=$1 AND status='reserved'`,
		jobID, actualMicros, result.InputTokens, result.OutputTokens, result.ProviderRequestID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("model call is no longer reservable")
	}
	return nil
}

func tokenCostMicros(inputTokens, outputTokens, inputPriceMicros, outputPriceMicros int64) (int64, error) {
	if inputTokens < 0 || outputTokens < 0 || inputPriceMicros < 0 || outputPriceMicros < 0 {
		return 0, fmt.Errorf("invalid model price or usage")
	}
	total := new(big.Int).Mul(big.NewInt(inputTokens), big.NewInt(inputPriceMicros))
	total.Add(total, new(big.Int).Mul(big.NewInt(outputTokens), big.NewInt(outputPriceMicros)))
	total.Add(total, big.NewInt(999_999))
	total.Div(total, big.NewInt(1_000_000))
	if !total.IsInt64() {
		return 0, fmt.Errorf("model cost exceeds supported range")
	}
	return total.Int64(), nil
}

func billingPeriod(now time.Time, timezone string) string {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location).Format("2006-01-02")
}

func (s *Service) buildModelInput(ctx context.Context, j goJob) (modelInput, error) {
	reportConfig, err := s.reportConfigForRun(ctx, j.RunID)
	if err != nil {
		return modelInput{}, err
	}
	input := modelInput{SchemaVersion: SchemaVersion, RunID: j.RunID, SessionID: j.SessionID, MediaAssetID: j.MediaID,
		Model: reportConfig.Model, PromptVersion: reportConfig.PromptVersion, PromptSHA256: reportConfig.PromptSHA256,
		SelectionVersion: reportConfig.SelectionVersion, Limitations: []string{}}
	var revision, course, title string
	err = s.pool.QueryRow(ctx, `SELECT r.input_sha256,c.name,ls.title,(SELECT metadata->>'transcript_revision_id' FROM teaching.job_artifacts WHERE run_id=r.id AND artifact_key='__result/evidence')
		FROM teaching.analysis_runs r JOIN teaching.lesson_sessions ls ON ls.id=r.session_id JOIN teaching.course_offerings o ON o.id=ls.offering_id JOIN teaching.courses c ON c.id=o.course_id
		JOIN teaching.media_assets m ON m.id=r.media_asset_id JOIN teaching.source_records src ON src.id=m.source_record_id
		WHERE r.id=$1 AND ls.status NOT IN('deleting','deleted') AND ls.content_expires_at>now() AND m.expires_at>now()
		AND src.rights_status='verified' AND src.allowed_uses ? 'analysis' AND src.external_processing_allowed`, j.RunID).Scan(&input.InputSHA256, &course, &title, &revision)
	if err != nil {
		return input, err
	}
	input.CourseContext = map[string]string{"course_name": course, "session_title": title}
	input.TranscriptRevisionID = &revision
	rows, err := s.pool.Query(ctx, `SELECT id::text,kind,start_ms,end_ms,description FROM teaching.evidence_items WHERE run_id=$1 AND availability='available' AND kind IN('transcript','manual') ORDER BY start_ms,id`, j.RunID)
	if err != nil {
		return input, err
	}
	defer rows.Close()
	maxChars := reportConfig.MaxInputTokens * 2
	if maxChars <= 0 {
		maxChars = 16000
	}
	usedChars := 0
	skippedEvidence := 0
	for rows.Next() {
		var e modelEvidence
		if err = rows.Scan(&e.ID, &e.Kind, &e.StartMS, &e.EndMS, &e.TextContent); err != nil {
			return input, err
		}
		chars := utf8.RuneCountInString(e.TextContent)
		remaining := maxChars - usedChars
		if remaining <= 0 {
			skippedEvidence++
			continue
		}
		if chars > remaining {
			e.TextContent = truncateRunes(e.TextContent, remaining)
			chars = remaining
			input.Limitations = append(input.Limitations, "MODEL_INPUT_TRUNCATED:"+e.ID)
		}
		e.TextSHA256 = digestBytes([]byte(e.TextContent))
		usedChars += chars
		input.Evidence = append(input.Evidence, e)
	}
	if err = rows.Err(); err != nil {
		return input, err
	}
	if skippedEvidence > 0 {
		input.Limitations = append(input.Limitations, fmt.Sprintf("MODEL_INPUT_SKIPPED_COUNT:%d", skippedEvidence))
	}
	if len(input.Evidence) == 0 {
		return input, fmt.Errorf("no reportable evidence")
	}
	for _, evidence := range input.Evidence {
		window := Interval{StartMS: evidence.StartMS, EndMS: evidence.EndMS}
		if len(input.Coverage) == 0 || window.StartMS > input.Coverage[len(input.Coverage)-1].EndMS {
			input.Coverage = append(input.Coverage, window)
			continue
		}
		if window.EndMS > input.Coverage[len(input.Coverage)-1].EndMS {
			input.Coverage[len(input.Coverage)-1].EndMS = window.EndMS
		}
	}
	workerLimitations, limitErr := s.workerLimitations(ctx, j.RunID)
	if limitErr != nil {
		return input, limitErr
	}
	input.Limitations = boundedModelLimitations(appendUniqueAll(input.Limitations, workerLimitations))
	return input, nil
}

func boundedModelLimitations(values []string) []string {
	const maxItems = 32
	const maxRunes = 256
	out := make([]string, 0, min(len(values), maxItems))
	skipped := 0
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(out) >= maxItems-1 {
			skipped++
			continue
		}
		out = appendUnique(out, truncateRunes(value, maxRunes))
	}
	if skipped > 0 {
		out = append(out, fmt.Sprintf("LIMITATIONS_SKIPPED_COUNT:%d", skipped))
	}
	return out
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func (s *Service) workerLimitations(ctx context.Context, runID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT j.stage,ja.metadata->'result' FROM teaching.analysis_jobs j JOIN teaching.job_artifacts ja ON ja.job_id=j.id
		WHERE j.run_id=$1 AND j.stage IN('probe','audio_analysis','video_analysis') AND ja.artifact_key='__result/'||j.stage`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	limitations := []string{}
	for rows.Next() {
		var stage string
		var raw []byte
		if err = rows.Scan(&stage, &raw); err != nil {
			return nil, err
		}
		var envelope struct {
			Limitations []string `json:"limitations"`
		}
		if json.Unmarshal(raw, &envelope) == nil {
			for _, limitation := range envelope.Limitations {
				limitations = appendUnique(limitations, stage+":"+limitation)
			}
		}
	}
	return limitations, rows.Err()
}

func (s *Service) executeValidate(ctx context.Context, j goJob) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `SELECT 1 FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, j.SessionID); err != nil {
		return err
	}
	if err = s.assertGoLease(ctx, tx, j); err != nil {
		return err
	}
	var candidateRaw, inputRaw []byte
	var manifestSHA string
	err = tx.QueryRow(ctx, `SELECT rc.metadata->'report_candidate',mi.metadata->'model_input',mi.metadata->>'manifest_sha256' FROM teaching.job_artifacts rc JOIN teaching.analysis_jobs rj ON rj.id=rc.job_id JOIN teaching.analysis_jobs vj ON vj.run_id=rj.run_id AND vj.id=$1 JOIN teaching.job_artifacts mi ON mi.job_id=rj.id AND mi.artifact_key='__model_input' WHERE rc.artifact_key='__report_candidate'`, j.ID).Scan(&candidateRaw, &inputRaw, &manifestSHA)
	if err != nil {
		return err
	}
	var candidate ReportCandidate
	var input modelInput
	if err = json.Unmarshal(candidateRaw, &candidate); err != nil {
		return err
	}
	if err = json.Unmarshal(inputRaw, &input); err != nil {
		return err
	}
	if err = validateCandidate(candidate, input); err != nil {
		return err
	}
	for _, e := range input.Evidence {
		var available bool
		if err = tx.QueryRow(ctx, `SELECT availability='available' AND run_id=$2 FROM teaching.evidence_items WHERE id=$1`, e.ID, j.RunID).Scan(&available); err != nil || !available {
			if err != nil {
				return err
			}
			return fmt.Errorf("model input evidence is no longer available")
		}
	}
	var sourceID, title, attribution string
	var sourceURL *string
	var rightsVersion int
	err = tx.QueryRow(ctx, `SELECT src.id::text,src.title,src.attribution,src.source_url,src.rights_version FROM teaching.media_assets m JOIN teaching.source_records src ON src.id=m.source_record_id JOIN teaching.lesson_sessions ls ON ls.id=m.session_id WHERE m.id=$1 AND ls.status NOT IN('deleting','deleted') AND ls.content_expires_at>now() AND m.expires_at>now() AND src.rights_status='verified' AND src.allowed_uses ? 'analysis' AND src.external_processing_allowed`, j.MediaID).Scan(&sourceID, &title, &attribution, &sourceURL, &rightsVersion)
	if err != nil {
		return err
	}
	prov := map[string]any{"media_asset_id": j.MediaID, "input_sha256": input.InputSHA256, "transcript_revision_id": input.TranscriptRevisionID, "model": input.Model, "prompt_version": input.PromptVersion, "prompt_sha256": input.PromptSHA256, "selection_version": input.SelectionVersion, "manifest_sha256": manifestSHA, "coverage": input.Coverage, "limitations": input.Limitations, "source": map[string]any{"id": sourceID, "title": title, "attribution": attribution, "source_url": sourceURL, "rights_version": rightsVersion}}
	plannedObservations := make([]Observation, 0, len(candidate.Observations))
	digestObservations := make([]map[string]any, 0, len(candidate.Observations))
	for _, o := range candidate.Observations {
		var id string
		if err = tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
			return err
		}
		plannedObservations = append(plannedObservations, Observation{ID: id, DimensionCode: o.DimensionCode, ObservationType: o.ObservationType, ObservationText: o.ObservationText, Suggestion: o.Suggestion, ReviewStatus: "unreviewed", EvidenceIDs: o.EvidenceIDs})
		digestObservations = append(digestObservations, map[string]any{"id": id, "dimension_code": o.DimensionCode, "observation_type": o.ObservationType, "observation_text": o.ObservationText, "suggestion": o.Suggestion, "evidence_ids": o.EvidenceIDs})
	}
	contentSHA := canonicalDigest(map[string]any{"summary": candidate.Summary, "summary_evidence_ids": candidate.SummaryEvidenceIDs, "dimensions": candidate.Dimensions, "observations": digestObservations, "provenance": prov})
	var requester, reportID string
	if err = tx.QueryRow(ctx, `SELECT requested_by::text FROM teaching.analysis_runs WHERE id=$1`, j.RunID).Scan(&requester); err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `INSERT INTO teaching.reports(run_id,session_id,revision,status,summary,created_by,summary_evidence_ids,content_sha256,provenance) VALUES($1,$2,1,'draft',$3,$4,$5,$6,$7) RETURNING id::text`, j.RunID, j.SessionID, candidate.Summary, requester, candidate.SummaryEvidenceIDs, contentSHA, prov).Scan(&reportID)
	if err != nil {
		return err
	}
	for _, d := range candidate.Dimensions {
		coverage, _ := json.Marshal(d.Coverage)
		if _, err = tx.Exec(ctx, `INSERT INTO teaching.report_dimensions(report_id,dimension_code,coverage_status,summary,limitation,coverage,summary_evidence_ids) VALUES($1,$2,$3,$4,$5,$6,$7)`, reportID, d.DimensionCode, d.CoverageStatus, d.Summary, d.Limitation, coverage, d.SummaryEvidenceIDs); err != nil {
			return err
		}
	}
	for i, o := range plannedObservations {
		if _, err = tx.Exec(ctx, `INSERT INTO teaching.report_observations(id,report_id,run_id,session_id,dimension_code,observation_type,observation_text,suggestion,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, o.ID, reportID, j.RunID, j.SessionID, o.DimensionCode, o.ObservationType, o.ObservationText, o.Suggestion, i); err != nil {
			return err
		}
		for _, eid := range o.EvidenceIDs {
			if _, err = tx.Exec(ctx, `INSERT INTO teaching.observation_evidence(observation_id,report_id,evidence_id,run_id,session_id) VALUES($1,$2,$3,$4,$5)`, o.ID, reportID, eid, j.RunID, j.SessionID); err != nil {
				return err
			}
		}
	}
	if err = s.completeGoJob(ctx, tx, j); err != nil {
		return err
	}
	if err = s.finalizeValidatedRun(ctx, tx, j.RunID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) finalizeValidatedRun(ctx context.Context, tx pgx.Tx, runID string) error {
	var mode string
	if err := tx.QueryRow(ctx, `SELECT mode FROM teaching.analysis_runs WHERE id=$1`, runID).Scan(&mode); err != nil {
		return err
	}
	required := []string{"evidence", "report", "validate"}
	if mode == "full" {
		required = []string{"probe", "audio_analysis", "video_analysis", "evidence", "report", "validate"}
	}
	rows, err := tx.Query(ctx, `SELECT stage,status,error_code FROM teaching.analysis_jobs WHERE run_id=$1 AND stage=ANY($2::text[]) ORDER BY created_at`, runID, required)
	if err != nil {
		return err
	}
	incomplete := 0
	errorCode := ""
	for rows.Next() {
		var stage, status string
		var code *string
		if err = rows.Scan(&stage, &status, &code); err != nil {
			rows.Close()
			return err
		}
		if status != "succeeded" {
			incomplete++
			if errorCode == "" && code != nil {
				errorCode = *code
			}
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if incomplete == 0 {
		_, err = tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='succeeded',error_code=NULL,finished_at=now() WHERE id=$1`, runID)
		return err
	}
	if errorCode == "" {
		errorCode = "ANALYSIS_INCOMPLETE"
	}
	_, err = tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='partial',error_code=$2,finished_at=now() WHERE id=$1`, runID, errorCode)
	return err
}

func validateCandidate(c ReportCandidate, input modelInput) error {
	allowed := map[string]bool{}
	for _, e := range input.Evidence {
		allowed[e.ID] = true
	}
	if len(input.Coverage) == 0 || validateIntervals(input.Coverage, input.Coverage[len(input.Coverage)-1].EndMS) != nil {
		return fmt.Errorf("model input coverage is invalid")
	}
	if len(c.Dimensions) != 6 {
		return fmt.Errorf("candidate must contain six dimensions")
	}
	seen := map[string]bool{}
	check := func(ids []string) error {
		unique := map[string]bool{}
		for _, id := range ids {
			if unique[id] {
				return fmt.Errorf("candidate contains duplicate evidence references")
			}
			unique[id] = true
			if !allowed[id] {
				return fmt.Errorf("candidate references evidence outside the actual model input")
			}
		}
		return nil
	}
	if (strings.TrimSpace(c.Summary) != "") != (len(c.SummaryEvidenceIDs) > 0) {
		return fmt.Errorf("summary and evidence references must agree")
	}
	if err := check(c.SummaryEvidenceIDs); err != nil {
		return err
	}
	for _, d := range c.Dimensions {
		if !containsString(dimensionCodes, d.DimensionCode) || seen[d.DimensionCode] {
			return fmt.Errorf("candidate dimension set is invalid")
		}
		if d.CoverageStatus != "observed" && d.CoverageStatus != "insufficient" && d.CoverageStatus != "not_applicable" {
			return fmt.Errorf("candidate coverage status is invalid")
		}
		seen[d.DimensionCode] = true
		if d.CoverageStatus != "observed" && (strings.TrimSpace(d.Limitation) == "" || strings.TrimSpace(d.Summary) != "" || len(d.SummaryEvidenceIDs) > 0) {
			return fmt.Errorf("limited dimension contains unsupported facts")
		}
		if (strings.TrimSpace(d.Summary) != "") != (len(d.SummaryEvidenceIDs) > 0) {
			return fmt.Errorf("dimension summary and evidence references must agree")
		}
		if err := validateIntervals(d.Coverage, input.Coverage[len(input.Coverage)-1].EndMS); err != nil {
			return err
		}
		for _, window := range d.Coverage {
			if !covered(window.StartMS, window.EndMS, input.Coverage) {
				return fmt.Errorf("dimension coverage falls outside processed input")
			}
		}
		if err := check(d.SummaryEvidenceIDs); err != nil {
			return err
		}
	}
	if len(c.Observations) == 0 {
		return fmt.Errorf("candidate requires at least one evidence-backed observation")
	}
	for _, o := range c.Observations {
		if !seen[o.DimensionCode] || (o.ObservationType != "highlight" && o.ObservationType != "issue" && o.ObservationType != "observation") || strings.TrimSpace(o.ObservationText) == "" || len(o.EvidenceIDs) == 0 {
			return fmt.Errorf("candidate observation is invalid")
		}
		if err := check(o.EvidenceIDs); err != nil {
			return err
		}
	}
	return nil
}

func containsString(v []string, w string) bool {
	for _, x := range v {
		if x == w {
			return true
		}
	}
	return false
}
func (s *Service) assertGoLease(ctx context.Context, tx pgx.Tx, j goJob) error {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT status='running' AND lease_token=$2::uuid AND lease_expires_at>now() FROM teaching.analysis_jobs WHERE id=$1 FOR UPDATE`, j.ID, j.Token).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("go stage lease lost")
	}
	return nil
}
func (s *Service) completeGoJob(ctx context.Context, tx pgx.Tx, j goJob) error {
	_, err := tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='succeeded',progress=100,last_completed_token=lease_token,completion_digest=$2,worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,execution_deadline_at=NULL,expected_execution_sha256=NULL,error_code=NULL,error_detail=NULL,updated_at=now() WHERE id=$1`, j.ID, digestJSON(map[string]string{"stage": j.Stage}))
	return err
}
func (s *Service) failGoJob(ctx context.Context, j goJob, cause error) error {
	if j.Stage == "report" {
		if err := s.markModelCallUnknown(ctx, j.ID); err != nil {
			return err
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `SELECT 1 FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, j.SessionID); err != nil {
		return err
	}
	if err = s.assertGoLease(ctx, tx, j); err != nil {
		var status string
		if queryErr := tx.QueryRow(ctx, `SELECT status FROM teaching.analysis_jobs WHERE id=$1`, j.ID).Scan(&status); queryErr == nil && status != "running" {
			return nil
		}
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='failed',worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,execution_deadline_at=NULL,expected_execution_sha256=NULL,error_code='INVALID_RESULT',error_detail=$2,updated_at=now() WHERE id=$1`, j.ID, cause.Error())
	if err != nil {
		return err
	}
	for _, stage := range []string{"report", "validate"} {
		if stage == j.Stage {
			continue
		}
		_, _ = tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,error_code) VALUES($1,$2,'skipped',$3,'DEPENDENCY_FAILED') ON CONFLICT DO NOTHING`, j.RunID, stage, s.maxAttempts(stage))
	}
	_, err = tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='partial',error_code='INVALID_RESULT',finished_at=now() WHERE id=$1`, j.RunID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) skipGoJob(ctx context.Context, j goJob, code string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `SELECT 1 FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE`, j.SessionID); err != nil {
		return err
	}
	if err = s.assertGoLease(ctx, tx, j); err != nil {
		var status string
		if queryErr := tx.QueryRow(ctx, `SELECT status FROM teaching.analysis_jobs WHERE id=$1`, j.ID).Scan(&status); queryErr == nil && status != "running" {
			return nil
		}
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.analysis_jobs SET status='skipped',worker_id=NULL,lease_token=NULL,lease_expires_at=NULL,
		execution_deadline_at=NULL,expected_execution_sha256=NULL,error_code=$2,error_detail=NULL,updated_at=now() WHERE id=$1`, j.ID, code); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,error_code)
		VALUES($1,'validate','skipped',$2,$3) ON CONFLICT DO NOTHING`, j.RunID, s.maxAttempts("validate"), code); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.analysis_runs SET status='partial',error_code=$2,finished_at=now() WHERE id=$1`, j.RunID, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) recoverOrphanedModelCalls(ctx context.Context) (int64, error) {
	grace := s.cfg.ReportTimeout
	if grace <= 0 {
		grace = 180 * time.Second
	}
	tag, err := s.pool.Exec(ctx, `UPDATE teaching.model_calls mc SET status='unknown',lock_version=lock_version+1
		FROM teaching.analysis_jobs j,teaching.analysis_runs r
		WHERE mc.job_id=j.id AND mc.run_id=r.id AND j.run_id=r.id AND mc.status='reserved' AND mc.dispatch_started_at IS NOT NULL
		AND mc.dispatch_started_at<=now()-$1::interval
		AND (j.status<>'running' OR j.lease_token IS NULL OR j.lease_expires_at<=now() OR j.execution_deadline_at<=now()
			OR r.status NOT IN('queued','running'))`, grace.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
