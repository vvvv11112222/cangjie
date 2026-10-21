-- Cloud ASR has one independently billed call per immutable audio chunk.
-- Historical report jobs retain their one-call-per-job safety constraint.
DROP INDEX teaching.ux_call_job;
CREATE UNIQUE INDEX ux_call_job ON teaching.model_calls(job_id)
    WHERE job_id IS NOT NULL AND provider <> 'openai-compatible-asr';
CREATE INDEX ix_asr_call_job ON teaching.model_calls(job_id)
    WHERE job_id IS NOT NULL AND provider = 'openai-compatible-asr';
