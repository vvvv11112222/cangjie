-- 教学质量管理系统历史初始结构 v0.3；当前 v0.4 必须继续执行 002_review_baseline.sql。
-- PostgreSQL 17+；仅用于新建空数据库的 teaching schema，不覆盖已有数据。
-- 执行前备份；迁移工具应记录版本。配置及密钥不写入本脚本。
BEGIN;
CREATE SCHEMA teaching;
SET search_path TO teaching, public;

CREATE TABLE org_units (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id uuid REFERENCES org_units(id),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('school','college','department')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (parent_id IS NULL OR parent_id <> id)
);

CREATE TABLE user_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_unit_id uuid NOT NULL REFERENCES org_units(id),
    username text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    display_name text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE role_bindings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES user_accounts(id),
    role_code text NOT NULL CHECK (role_code IN ('sys_admin','academic_admin','supervisor','teacher')),
    scope_org_id uuid REFERENCES org_units(id),
    UNIQUE NULLS NOT DISTINCT (user_id, role_code, scope_org_id),
    CHECK (
        (role_code IN ('sys_admin','teacher') AND scope_org_id IS NULL)
        OR (role_code IN ('academic_admin','supervisor') AND scope_org_id IS NOT NULL)
    )
);

CREATE TABLE auth_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES user_accounts(id),
    token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[a-f0-9]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (expires_at > created_at)
);

CREATE TABLE academic_terms (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    start_date date NOT NULL,
    end_date date NOT NULL,
    CHECK (end_date > start_date)
);

CREATE TABLE courses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_unit_id uuid NOT NULL REFERENCES org_units(id),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    description text NOT NULL DEFAULT ''
);

CREATE TABLE class_groups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_unit_id uuid NOT NULL REFERENCES org_units(id),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    expected_size integer CHECK (expected_size IS NULL OR expected_size >= 0)
);

CREATE TABLE classrooms (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    capacity integer CHECK (capacity IS NULL OR capacity > 0)
);

CREATE TABLE course_offerings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_unit_id uuid NOT NULL REFERENCES org_units(id),
    code text NOT NULL UNIQUE,
    term_id uuid NOT NULL REFERENCES academic_terms(id),
    course_id uuid NOT NULL REFERENCES courses(id),
    teacher_id uuid NOT NULL REFERENCES user_accounts(id),
    class_group_id uuid NOT NULL REFERENCES class_groups(id),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
    UNIQUE (id, teacher_id, class_group_id)
);

CREATE TABLE schedule_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    offering_id uuid NOT NULL,
    teacher_id uuid NOT NULL,
    class_group_id uuid NOT NULL,
    classroom_id uuid NOT NULL REFERENCES classrooms(id),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','cancelled')),
    FOREIGN KEY (offering_id, teacher_id, class_group_id)
        REFERENCES course_offerings(id, teacher_id, class_group_id),
    UNIQUE (id, offering_id),
    CHECK (ends_at > starts_at)
);

CREATE TABLE lesson_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    offering_id uuid NOT NULL REFERENCES course_offerings(id),
    schedule_entry_id uuid UNIQUE,
    title text NOT NULL,
    planned_start_at timestamptz NOT NULL,
    planned_end_at timestamptz NOT NULL,
    occurred_start_at timestamptz,
    is_demo boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'planned'
        CHECK (status IN ('planned','ready','archived','deleting','deleted')),
    created_by uuid NOT NULL REFERENCES user_accounts(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (schedule_entry_id, offering_id) REFERENCES schedule_entries(id, offering_id),
    CHECK (planned_end_at > planned_start_at)
);

CREATE TABLE source_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_type text NOT NULL CHECK (source_type IN ('self_recorded','open_course','school_authorized')),
    title text NOT NULL,
    source_url text,
    attribution text NOT NULL,
    license_name text,
    license_url text,
    rights_status text NOT NULL DEFAULT 'pending' CHECK (rights_status IN ('pending','verified','rejected')),
    allowed_uses jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(allowed_uses) = 'array'),
    external_processing_allowed boolean NOT NULL DEFAULT false,
    redistribution_allowed boolean NOT NULL DEFAULT false,
    authorization_note text NOT NULL DEFAULT '',
    registered_by uuid NOT NULL REFERENCES user_accounts(id),
    verified_by uuid REFERENCES user_accounts(id),
    registered_at timestamptz NOT NULL DEFAULT now(),
    verified_at timestamptz,
    CHECK (rights_status <> 'verified' OR (verified_by IS NOT NULL AND verified_at IS NOT NULL))
);

CREATE TABLE media_assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES lesson_sessions(id),
    parent_asset_id uuid,
    source_record_id uuid REFERENCES source_records(id),
    kind text NOT NULL CHECK (kind IN ('source','proxy','audio','keyframe','report_export')),
    is_primary boolean NOT NULL DEFAULT false,
    storage_backend text NOT NULL CHECK (storage_backend IN ('filesystem','s3')),
    bucket text,
    object_key text NOT NULL CHECK (object_key <> ''),
    sha256 text NOT NULL CHECK (sha256 ~ '^[a-f0-9]{64}$'),
    mime_type text NOT NULL,
    byte_size bigint NOT NULL CHECK (byte_size > 0),
    duration_ms bigint CHECK (duration_ms IS NULL OR duration_ms > 0),
    origin_offset_ms bigint NOT NULL DEFAULT 0 CHECK (origin_offset_ms >= 0),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','ready','failed','deleting','deleted')),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '14 days'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, session_id),
    UNIQUE NULLS NOT DISTINCT (storage_backend, bucket, object_key),
    FOREIGN KEY (parent_asset_id, session_id) REFERENCES media_assets(id, session_id),
    CHECK (parent_asset_id IS NULL OR parent_asset_id <> id),
    CHECK ((storage_backend = 'filesystem' AND bucket IS NULL)
        OR (storage_backend = 's3' AND bucket IS NOT NULL)),
    CHECK (NOT is_primary OR kind = 'source'),
    CHECK (kind <> 'source' OR (source_record_id IS NOT NULL AND parent_asset_id IS NULL)),
    CHECK (kind NOT IN ('proxy','audio','keyframe') OR parent_asset_id IS NOT NULL)
);
CREATE UNIQUE INDEX ux_media_primary ON media_assets(session_id)
    WHERE is_primary AND status <> 'deleted';

CREATE TABLE analysis_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES lesson_sessions(id),
    media_asset_id uuid NOT NULL,
    requested_by uuid NOT NULL REFERENCES user_accounts(id),
    idempotency_key text NOT NULL,
    input_sha256 text NOT NULL CHECK (input_sha256 ~ '^[a-f0-9]{64}$'),
    config_hash text NOT NULL CHECK (config_hash ~ '^[a-f0-9]{64}$'),
    pipeline_version text NOT NULL,
    config_snapshot jsonb NOT NULL CHECK (jsonb_typeof(config_snapshot) = 'object'),
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','running','succeeded','partial','failed','cancelled')),
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    UNIQUE (requested_by, idempotency_key),
    UNIQUE (id, session_id),
    UNIQUE (id, session_id, media_asset_id),
    FOREIGN KEY (media_asset_id, session_id) REFERENCES media_assets(id, session_id)
);
CREATE UNIQUE INDEX ux_runs_active_input
    ON analysis_runs(session_id, media_asset_id, pipeline_version, config_hash)
    WHERE status IN ('queued','running');

CREATE TABLE analysis_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES analysis_runs(id),
    stage text NOT NULL CHECK (stage IN ('probe','audio_analysis','video_analysis','evidence','report','validate')),
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','running','succeeded','failed','cancelled','skipped')),
    priority integer NOT NULL DEFAULT 0,
    attempts integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 1,
    available_at timestamptz NOT NULL DEFAULT now(),
    worker_id text,
    lease_token uuid,
    lease_expires_at timestamptz,
    last_completed_token uuid,
    completion_digest text,
    progress smallint NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    error_code text,
    error_detail text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, stage),
    UNIQUE (id, run_id),
    CHECK (max_attempts > 0 AND attempts BETWEEN 0 AND max_attempts),
    CHECK ((status = 'running' AND worker_id IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (status <> 'running' AND worker_id IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL)),
    CHECK (stage <> 'report' OR max_attempts <= 2)
);
CREATE INDEX ix_jobs_claim ON analysis_jobs(priority DESC, available_at, created_at)
    WHERE status = 'queued';
CREATE INDEX ix_jobs_lease ON analysis_jobs(lease_expires_at) WHERE status = 'running';

CREATE TABLE job_artifacts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id uuid NOT NULL,
    run_id uuid NOT NULL,
    artifact_key text NOT NULL,
    media_asset_id uuid REFERENCES media_assets(id),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (job_id, artifact_key),
    FOREIGN KEY (job_id, run_id) REFERENCES analysis_jobs(id, run_id)
);

CREATE TABLE transcript_segments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL,
    session_id uuid NOT NULL,
    segment_no integer NOT NULL CHECK (segment_no >= 0),
    start_ms bigint NOT NULL CHECK (start_ms >= 0),
    end_ms bigint NOT NULL,
    speaker_label text NOT NULL DEFAULT 'unknown',
    text_content text NOT NULL,
    language_code text,
    quality_metrics jsonb NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (run_id, segment_no),
    UNIQUE (id, run_id, session_id),
    FOREIGN KEY (run_id, session_id) REFERENCES analysis_runs(id, session_id),
    CHECK (end_ms > start_ms)
);

CREATE TABLE evidence_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL,
    session_id uuid NOT NULL,
    media_asset_id uuid NOT NULL,
    transcript_segment_id uuid,
    frame_asset_id uuid,
    kind text NOT NULL CHECK (kind IN ('transcript','frame','manual','behavior')),
    start_ms bigint NOT NULL CHECK (start_ms >= 0),
    end_ms bigint NOT NULL,
    description text NOT NULL,
    detection_confidence numeric(6,5) CHECK (detection_confidence BETWEEN 0 AND 1),
    provenance jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(provenance) = 'object'),
    created_by uuid REFERENCES user_accounts(id),
    availability text NOT NULL DEFAULT 'available' CHECK (availability IN ('available','expired','removed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, run_id, session_id),
    FOREIGN KEY (run_id, session_id, media_asset_id)
        REFERENCES analysis_runs(id, session_id, media_asset_id),
    FOREIGN KEY (transcript_segment_id, run_id, session_id)
        REFERENCES transcript_segments(id, run_id, session_id),
    FOREIGN KEY (frame_asset_id, session_id) REFERENCES media_assets(id, session_id),
    CHECK (end_ms > start_ms),
    CHECK ((kind = 'transcript' AND transcript_segment_id IS NOT NULL)
        OR (kind <> 'transcript' AND transcript_segment_id IS NULL)),
    CHECK (kind <> 'frame' OR frame_asset_id IS NOT NULL)
);

CREATE TABLE reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL,
    session_id uuid NOT NULL,
    revision integer NOT NULL CHECK (revision > 0),
    parent_report_id uuid,
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft','in_review','published','superseded','withdrawn')),
    summary text NOT NULL DEFAULT '',
    limitations text NOT NULL DEFAULT '',
    lock_version integer NOT NULL DEFAULT 1 CHECK (lock_version > 0),
    created_by uuid NOT NULL REFERENCES user_accounts(id),
    published_by uuid REFERENCES user_accounts(id),
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '30 days'),
    UNIQUE (run_id, revision),
    UNIQUE (id, run_id, session_id),
    FOREIGN KEY (run_id, session_id) REFERENCES analysis_runs(id, session_id),
    FOREIGN KEY (parent_report_id, run_id, session_id) REFERENCES reports(id, run_id, session_id),
    CHECK (parent_report_id IS NULL OR parent_report_id <> id),
    CHECK (status NOT IN ('published','superseded') OR (published_by IS NOT NULL AND published_at IS NOT NULL))
);
CREATE UNIQUE INDEX ux_reports_published ON reports(session_id) WHERE status = 'published';

CREATE TABLE report_dimensions (
    report_id uuid NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    dimension_code text NOT NULL CHECK (dimension_code IN ('content','pace','thinking','expression','management','technology')),
    coverage_status text NOT NULL CHECK (coverage_status IN ('observed','insufficient','not_applicable')),
    summary text NOT NULL DEFAULT '',
    limitation text NOT NULL DEFAULT '',
    coverage jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(coverage) = 'object'),
    PRIMARY KEY (report_id, dimension_code),
    CHECK (coverage_status = 'observed' OR limitation <> '')
);

CREATE TABLE report_observations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id uuid NOT NULL,
    run_id uuid NOT NULL,
    session_id uuid NOT NULL,
    dimension_code text NOT NULL,
    observation_type text NOT NULL CHECK (observation_type IN ('highlight','issue','observation')),
    observation_text text NOT NULL CHECK (observation_text <> ''),
    suggestion text NOT NULL DEFAULT '',
    review_status text NOT NULL DEFAULT 'unreviewed' CHECK (review_status IN ('unreviewed','accepted','revised','rejected')),
    sort_order integer NOT NULL DEFAULT 0,
    UNIQUE (id, report_id, run_id, session_id),
    FOREIGN KEY (report_id, run_id, session_id) REFERENCES reports(id, run_id, session_id) ON DELETE CASCADE,
    FOREIGN KEY (report_id, dimension_code) REFERENCES report_dimensions(report_id, dimension_code) ON DELETE CASCADE
);

CREATE TABLE observation_evidence (
    observation_id uuid NOT NULL,
    report_id uuid NOT NULL,
    evidence_id uuid NOT NULL,
    run_id uuid NOT NULL,
    session_id uuid NOT NULL,
    PRIMARY KEY (observation_id, evidence_id),
    FOREIGN KEY (observation_id, report_id, run_id, session_id)
        REFERENCES report_observations(id, report_id, run_id, session_id) ON DELETE CASCADE,
    FOREIGN KEY (evidence_id, run_id, session_id)
        REFERENCES evidence_items(id, run_id, session_id)
);

CREATE TABLE review_actions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id uuid NOT NULL,
    run_id uuid NOT NULL,
    session_id uuid NOT NULL,
    observation_id uuid,
    reviewer_id uuid NOT NULL REFERENCES user_accounts(id),
    action text NOT NULL CHECK (action IN ('confirm','revise','reject','publish','withdraw')),
    reason text NOT NULL DEFAULT '',
    changes jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(changes) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (report_id, run_id, session_id) REFERENCES reports(id, run_id, session_id) ON DELETE CASCADE,
    FOREIGN KEY (observation_id, report_id, run_id, session_id)
        REFERENCES report_observations(id, report_id, run_id, session_id) ON DELETE CASCADE,
    CHECK (action NOT IN ('revise','reject','withdraw') OR reason <> '')
);

CREATE TABLE model_calls (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES analysis_runs(id),
    job_id uuid NOT NULL,
    call_key text NOT NULL UNIQUE,
    provider text NOT NULL,
    model_name text NOT NULL,
    prompt_version text NOT NULL,
    provider_request_id text,
    status text NOT NULL DEFAULT 'reserved'
        CHECK (status IN ('reserved','succeeded','failed','unknown')),
    currency text NOT NULL DEFAULT 'CNY',
    reserved_cost numeric(12,6) NOT NULL CHECK (reserved_cost >= 0),
    actual_cost numeric(12,6) CHECK (actual_cost IS NULL OR actual_cost >= 0),
    input_tokens bigint CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens IS NULL OR output_tokens >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    settled_at timestamptz,
    FOREIGN KEY (job_id, run_id) REFERENCES analysis_jobs(id, run_id)
);

CREATE TABLE export_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id uuid NOT NULL,
    run_id uuid NOT NULL,
    session_id uuid NOT NULL,
    requested_by uuid NOT NULL REFERENCES user_accounts(id),
    format text NOT NULL CHECK (format IN ('pdf','json')),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed','expired')),
    output_asset_id uuid,
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    FOREIGN KEY (report_id, run_id, session_id) REFERENCES reports(id, run_id, session_id),
    FOREIGN KEY (output_asset_id, session_id) REFERENCES media_assets(id, session_id),
    CHECK (status <> 'succeeded' OR output_asset_id IS NOT NULL)
);

CREATE TABLE audit_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_user_id uuid REFERENCES user_accounts(id),
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id uuid,
    result text NOT NULL CHECK (result IN ('success','denied','failure')),
    request_id text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE data_deletion_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES lesson_sessions(id),
    requested_by uuid NOT NULL REFERENCES user_accounts(id),
    reason text NOT NULL CHECK (reason <> ''),
    status text NOT NULL DEFAULT 'requested'
        CHECK (status IN ('requested','processing','blocked','succeeded','failed')),
    cleanup_manifest jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(cleanup_manifest) = 'object'),
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (status <> 'succeeded' OR completed_at IS NOT NULL)
);
CREATE UNIQUE INDEX ux_deletion_active ON data_deletion_requests(session_id)
    WHERE status IN ('requested','processing','blocked');

CREATE INDEX ix_org_parent ON org_units(parent_id);
CREATE INDEX ix_users_org ON user_accounts(org_unit_id);
CREATE INDEX ix_auth_sessions_user ON auth_sessions(user_id, expires_at);
CREATE INDEX ix_offerings_teacher ON course_offerings(teacher_id, term_id);
CREATE INDEX ix_offerings_org ON course_offerings(org_unit_id, term_id);
CREATE INDEX ix_sessions_offering_time ON lesson_sessions(offering_id, planned_start_at DESC);
CREATE INDEX ix_media_session ON media_assets(session_id, kind);
CREATE INDEX ix_media_expiration ON media_assets(expires_at) WHERE status = 'ready';
CREATE INDEX ix_runs_session ON analysis_runs(session_id, created_at DESC);
CREATE INDEX ix_transcript_timeline ON transcript_segments(run_id, start_ms);
CREATE INDEX ix_evidence_timeline ON evidence_items(run_id, start_ms);
CREATE INDEX ix_reports_session ON reports(session_id, created_at DESC);
CREATE INDEX ix_observation_report ON report_observations(report_id, dimension_code, sort_order);
CREATE INDEX ix_observation_evidence_reverse ON observation_evidence(evidence_id);
CREATE INDEX ix_review_report ON review_actions(report_id, created_at);
CREATE INDEX ix_model_calls_budget ON model_calls(currency, created_at);
CREATE INDEX ix_exports_requester ON export_jobs(requested_by, created_at DESC);
CREATE INDEX ix_audit_time ON audit_logs(created_at);
CREATE INDEX ix_audit_resource ON audit_logs(resource_type, resource_id, created_at);

COMMENT ON SCHEMA teaching IS 'P0 教学质量管理系统；业务授权与跨行发布校验由 Go 服务实现';
COMMENT ON TABLE source_records IS 'verified 表示完成用途登记核验，不表示自动获得所有版权或隐私授权';
COMMENT ON TABLE analysis_jobs IS '至少一次执行；完成时必须校验当前租约并原子写入结果';
COMMENT ON TABLE report_dimensions IS '固定六维；无自动总分，insufficient 不等于低分';
COMMENT ON TABLE observation_evidence IS '复合外键确保结论引用同课堂、同批次证据';
COMMENT ON TABLE model_calls IS '仅用量与费用元数据；不存密钥或完整提示词';
COMMENT ON TABLE data_deletion_requests IS '保留最小删除清单，备份恢复后须重放，不恢复已撤销内容';
COMMIT;
