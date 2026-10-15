BEGIN;
SET search_path TO teaching, public;
CREATE TABLE live_captures (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 session_id uuid NOT NULL REFERENCES lesson_sessions(id),
 source_record_id uuid NOT NULL REFERENCES source_records(id),
 created_by uuid NOT NULL REFERENCES user_accounts(id),
 profile text NOT NULL CHECK (profile='phone-demo-v1'),
 state text NOT NULL DEFAULT 'waiting' CHECK(state IN('waiting','recording','finalizing','ready','failed')),
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
 request_sha256 text NOT NULL,
 stream_path text NOT NULL UNIQUE,
 storage_generation bigint NOT NULL,
 rights_version integer NOT NULL,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 started_at timestamptz,
 stop_requested_at timestamptz,
 disconnected_at timestamptz,
 ended_at timestamptz,
 connection_id text,
 connection_state text NOT NULL DEFAULT 'unknown' CHECK(connection_state IN('unknown','connected','disconnected')),
 recording_state text NOT NULL DEFAULT 'waiting',
 audio_track text NOT NULL DEFAULT 'unknown',
 video_track text NOT NULL DEFAULT 'unknown',
 duration_ms bigint CHECK(duration_ms IS NULL OR duration_ms>=0),
 media_asset_id uuid REFERENCES media_assets(id) ON DELETE SET NULL,
 media_prepare_run_id uuid REFERENCES analysis_runs(id) ON DELETE SET NULL,
 end_reason text,
 error_code text,
 finalize_token uuid,
 finalize_until timestamptz,
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(created_by,session_id,idempotency_key)
);
CREATE UNIQUE INDEX uq_live_capture_active ON live_captures(session_id) WHERE state IN('waiting','recording','finalizing');
CREATE UNIQUE INDEX uq_live_capture_media ON live_captures(media_asset_id) WHERE media_asset_id IS NOT NULL;
CREATE TABLE capture_pairings (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 capture_id uuid NOT NULL REFERENCES live_captures(id) ON DELETE CASCADE,
 token_sha256 text NOT NULL UNIQUE,
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz,
 revoked_at timestamptz
);
CREATE TABLE capture_grants (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 capture_id uuid NOT NULL REFERENCES live_captures(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES user_accounts(id),
 purpose text NOT NULL CHECK(purpose IN('publish','control','read')),
 token_sha256 text NOT NULL UNIQUE,
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz
);
CREATE UNIQUE INDEX uq_live_capture_publisher ON capture_grants(capture_id) WHERE purpose='publish' AND revoked_at IS NULL;
CREATE TABLE capture_connections (
 connection_id uuid PRIMARY KEY,
 grant_id uuid NOT NULL REFERENCES capture_grants(id) ON DELETE CASCADE,
 capture_id uuid NOT NULL REFERENCES live_captures(id) ON DELETE CASCADE
);
CREATE TABLE live_capture_events (
 event_id text PRIMARY KEY CHECK(length(event_id) BETWEEN 1 AND 128),
 capture_id uuid NOT NULL REFERENCES live_captures(id) ON DELETE CASCADE,
 request_sha256 text NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE live_capture_segments (
 capture_id uuid NOT NULL REFERENCES live_captures(id) ON DELETE CASCADE,
 storage_key text NOT NULL,
 complete boolean NOT NULL DEFAULT false,
 byte_size bigint,
 sha256 text,
 PRIMARY KEY(capture_id,storage_key)
);
CREATE INDEX ix_capture_work ON live_captures(available_at) WHERE state IN('waiting','recording','finalizing');
COMMIT;
