-- v0.4, 2026-10-02. Run once AFTER 001_initial_schema.sql.
-- Existing overlapping active schedules must be corrected before this migration.
-- Failure rolls back this migration; no business rows are automatically removed.
BEGIN;
CREATE EXTENSION IF NOT EXISTS btree_gist;
SET search_path TO teaching, public;

ALTER TABLE schedule_entries
    ADD CONSTRAINT ex_schedule_teacher EXCLUDE USING gist
        (teacher_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&) WHERE (status = 'active'),
    ADD CONSTRAINT ex_schedule_class EXCLUDE USING gist
        (class_group_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&) WHERE (status = 'active'),
    ADD CONSTRAINT ex_schedule_room EXCLUDE USING gist
        (classroom_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&) WHERE (status = 'active');

ALTER TABLE class_groups ADD COLUMN enrollment_year integer
    CHECK (enrollment_year BETWEEN 1900 AND 2200);
CREATE INDEX ix_groups_college_year ON class_groups(org_unit_id, enrollment_year);
-- Historical NULL means unknown. Go requires a year for newly created groups.

ALTER TABLE lesson_sessions
    ADD COLUMN transcript_lock_version integer NOT NULL DEFAULT 0 CHECK (transcript_lock_version >= 0),
    ADD COLUMN content_expires_at timestamptz NOT NULL DEFAULT (now() + interval '180 days');

-- New reports follow the content-retention baseline. Existing expiry dates are not extended.
ALTER TABLE reports ALTER COLUMN expires_at SET DEFAULT (now() + interval '180 days');

ALTER TABLE source_records
    ADD COLUMN session_id uuid REFERENCES lesson_sessions(id),
    ADD COLUMN requested_uses jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(requested_uses) = 'array'),
    ADD COLUMN external_processing_requested boolean NOT NULL DEFAULT false;
-- Historical ambiguous/unlinked sources remain NULL and require admin reassignment.
UPDATE source_records s SET session_id = u.session_id
FROM (
    SELECT source_record_id, (array_agg(DISTINCT session_id))[1] AS session_id
    FROM media_assets WHERE source_record_id IS NOT NULL
    GROUP BY source_record_id HAVING count(DISTINCT session_id) = 1
) u WHERE s.id = u.source_record_id;

CREATE TABLE transcript_revisions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL,
    media_asset_id uuid NOT NULL,
    source_run_id uuid NOT NULL,
    parent_revision_id uuid,
    revision_no integer NOT NULL CHECK (revision_no > 0),
    source_type text NOT NULL CHECK (source_type IN ('asr', 'manual')),
    content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[a-f0-9]{64}$'),
    created_by uuid NOT NULL REFERENCES user_accounts(id),
    reason text NOT NULL CHECK (reason <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, session_id, media_asset_id),
    UNIQUE (session_id, media_asset_id, revision_no),
    FOREIGN KEY (source_run_id, session_id, media_asset_id)
        REFERENCES analysis_runs(id, session_id, media_asset_id) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (parent_revision_id, session_id, media_asset_id)
        REFERENCES transcript_revisions(id, session_id, media_asset_id) DEFERRABLE INITIALLY DEFERRED,
    CHECK (parent_revision_id IS NULL OR parent_revision_id <> id),
    CHECK (source_type <> 'manual' OR parent_revision_id IS NOT NULL)
);

CREATE TABLE transcript_revision_segments (
    revision_id uuid NOT NULL REFERENCES transcript_revisions(id) ON DELETE CASCADE,
    segment_no integer NOT NULL CHECK (segment_no >= 0),
    start_ms bigint NOT NULL CHECK (start_ms >= 0),
    end_ms bigint NOT NULL,
    text_content text NOT NULL CHECK (text_content <> ''),
    speaker_label text NOT NULL DEFAULT 'unknown',
    PRIMARY KEY (revision_id, segment_no),
    CHECK (end_ms > start_ms)
);

ALTER TABLE analysis_runs
    ADD COLUMN mode text NOT NULL DEFAULT 'full' CHECK (mode IN ('full', 'report_only')),
    ADD COLUMN input_transcript_revision_id uuid,
    ADD CONSTRAINT fk_run_input_revision FOREIGN KEY (input_transcript_revision_id, session_id, media_asset_id)
        REFERENCES transcript_revisions(id, session_id, media_asset_id) DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT ck_run_mode_revision CHECK (
        (mode = 'full' AND input_transcript_revision_id IS NULL)
        OR (mode = 'report_only' AND input_transcript_revision_id IS NOT NULL));

-- Go enforces immutability of revisions and referenced batch segments.
-- Full classroom deletion removes revisions and runs together in one transaction;
-- deferred foreign keys allow these cross-references to be removed atomically.
CREATE INDEX ix_revisions_media ON transcript_revisions(session_id, media_asset_id, revision_no DESC);
CREATE INDEX ix_sessions_content_expiry ON lesson_sessions(content_expires_at);

ALTER TABLE export_jobs
    ADD COLUMN idempotency_key text,
    ADD COLUMN request_sha256 text CHECK (request_sha256 ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT ck_export_request_pair CHECK ((idempotency_key IS NULL) = (request_sha256 IS NULL)),
    ADD CONSTRAINT ux_export_request UNIQUE (requested_by, idempotency_key);
ALTER TABLE data_deletion_requests
    ADD COLUMN idempotency_key text,
    ADD COLUMN request_sha256 text CHECK (request_sha256 ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT ck_deletion_request_pair CHECK ((idempotency_key IS NULL) = (request_sha256 IS NULL)),
    ADD CONSTRAINT ux_deletion_request UNIQUE (requested_by, idempotency_key);

-- Keep settled accounting after content deletion so deletion cannot reset budget usage.
ALTER TABLE model_calls
    ALTER COLUMN run_id DROP NOT NULL,
    ALTER COLUMN job_id DROP NOT NULL,
    ADD CONSTRAINT ck_call_content_link CHECK ((run_id IS NULL) = (job_id IS NULL));

COMMENT ON COLUMN class_groups.enrollment_year IS 'Entry year, not a new org-tree level; NULL only for legacy unknown data';
COMMENT ON TABLE transcript_revisions IS 'Immutable complete transcript snapshots; writes and optimistic concurrency belong to Go evidence module';
COMMENT ON COLUMN analysis_runs.input_transcript_revision_id IS 'Exact immutable input for report_only; included in config_hash';
COMMIT;
