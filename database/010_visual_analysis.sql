-- Opt-in P1 visual analysis reuses immutable job_artifacts and existing evidence.
ALTER TABLE teaching.analysis_runs DROP CONSTRAINT ck_run_mode;
ALTER TABLE teaching.analysis_runs ADD CONSTRAINT ck_run_mode
 CHECK (mode IN ('full','report_only','media_prepare','visual_only'));
ALTER TABLE teaching.analysis_runs DROP CONSTRAINT ck_run_mode_revision;
ALTER TABLE teaching.analysis_runs ADD CONSTRAINT ck_run_mode_revision CHECK (
 (mode IN ('full','media_prepare','visual_only') AND input_transcript_revision_id IS NULL)
 OR (mode='report_only' AND input_transcript_revision_id IS NOT NULL));
