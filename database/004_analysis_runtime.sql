BEGIN;
SET search_path TO teaching, public;

-- 001 used a global requester/key constraint. The v1 contract scopes run
-- idempotency to a classroom, so different classrooms may reuse a client key.
ALTER TABLE analysis_runs
    DROP CONSTRAINT IF EXISTS analysis_runs_requested_by_idempotency_key_key;

ALTER TABLE analysis_jobs
    ADD CONSTRAINT ck_completion_digest CHECK (
        completion_digest IS NULL OR completion_digest ~ '^[a-f0-9]{64}$'
    );

CREATE INDEX ix_runs_session_created
    ON analysis_runs(session_id, created_at DESC, id DESC);

COMMIT;
