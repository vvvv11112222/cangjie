-- v0.6. Apply after 001 and 002 while application writers are stopped.
-- Preserve historical migrations and audit facts; split legacy sources without granting rights.
BEGIN;
SET search_path TO teaching, public;

-- A non-empty legacy coverage object has no documented lossless conversion.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM report_dimensions WHERE coverage <> '{}'::jsonb) THEN
        RAISE EXCEPTION '003 blocked: inspect legacy report_dimensions.coverage and approve an explicit interval mapping';
    END IF;
    IF EXISTS (SELECT 1 FROM analysis_jobs WHERE stage='report' AND attempts>1) THEN
        RAISE EXCEPTION '003 blocked: reconcile historical report jobs with attempts > 1 before migration';
    END IF;
    IF EXISTS (SELECT 1 FROM model_calls WHERE job_id IS NOT NULL GROUP BY job_id HAVING count(*)>1) THEN
        RAISE EXCEPTION '003 blocked: multiple model calls for one job require accounting reconciliation';
    END IF;
END $$;

ALTER TABLE report_dimensions DROP CONSTRAINT report_dimensions_coverage_check;
UPDATE report_dimensions SET coverage='[]'::jsonb WHERE coverage='{}'::jsonb;
ALTER TABLE report_dimensions ALTER COLUMN coverage SET DEFAULT '[]'::jsonb,
    ADD CONSTRAINT ck_dimension_coverage_array CHECK (jsonb_typeof(coverage)='array'),
    ADD COLUMN summary_evidence_ids uuid[] NOT NULL DEFAULT '{}';

ALTER TABLE analysis_runs DROP CONSTRAINT analysis_runs_requested_by_idempotency_key_key,
    ADD CONSTRAINT ux_run_request UNIQUE (requested_by,session_id,idempotency_key),
    ADD COLUMN request_sha256 text CHECK (request_sha256 ~ '^[a-f0-9]{64}$'),
    DROP CONSTRAINT analysis_runs_mode_check,
    DROP CONSTRAINT ck_run_mode_revision,
    ADD CONSTRAINT ck_run_mode CHECK (mode IN ('full','report_only','media_prepare')),
    ADD CONSTRAINT ck_run_mode_revision CHECK (
        (mode IN ('full','media_prepare') AND input_transcript_revision_id IS NULL)
        OR (mode='report_only' AND input_transcript_revision_id IS NOT NULL));
ALTER TABLE export_jobs DROP CONSTRAINT ux_export_request,
    ADD CONSTRAINT ux_export_request UNIQUE (requested_by,report_id,idempotency_key);
ALTER TABLE data_deletion_requests DROP CONSTRAINT ux_deletion_request,
    ADD CONSTRAINT ux_deletion_request UNIQUE (requested_by,session_id,idempotency_key);

-- Historical unassigned sources stay quarantined. New/changed media cannot borrow them.
ALTER TABLE source_records ADD CONSTRAINT ux_source_session UNIQUE (id,session_id),
    ADD COLUMN rights_version integer NOT NULL DEFAULT 1 CHECK (rights_version>0),
    ADD COLUMN legacy_verification jsonb CHECK (legacy_verification IS NULL OR jsonb_typeof(legacy_verification)='object');
-- Approved migration policy: split shared historical registrations, never copy grants.
-- The complete registration is copied; the former verification is retained as history.
DO $$ DECLARE old_source source_records%ROWTYPE; classroom record; new_id uuid; mapping jsonb;
BEGIN
    FOR old_source IN SELECT s.* FROM source_records s WHERE s.id IN (
        SELECT m.source_record_id FROM media_assets m JOIN source_records sr ON sr.id=m.source_record_id
        GROUP BY m.source_record_id HAVING count(DISTINCT m.session_id)>1
            OR bool_or(m.session_id IS DISTINCT FROM sr.session_id)) LOOP
        mapping:='[]'::jsonb;
        FOR classroom IN SELECT DISTINCT session_id FROM media_assets WHERE source_record_id=old_source.id LOOP
            INSERT INTO source_records(session_id,source_type,title,source_url,attribution,license_name,license_url,
                authorization_note,registered_by,registered_at,requested_uses,external_processing_requested,legacy_verification)
            VALUES(classroom.session_id,old_source.source_type,old_source.title,old_source.source_url,old_source.attribution,
                old_source.license_name,old_source.license_url,old_source.authorization_note,old_source.registered_by,
                old_source.registered_at,old_source.allowed_uses,old_source.external_processing_allowed,
                jsonb_build_object('source_id',old_source.id,'rights_status',old_source.rights_status,
                    'allowed_uses',old_source.allowed_uses,'external_processing_allowed',old_source.external_processing_allowed,
                    'redistribution_allowed',old_source.redistribution_allowed,'verified_by',old_source.verified_by,
                    'verified_at',old_source.verified_at,'requested_uses',old_source.requested_uses,
                    'external_processing_requested',old_source.external_processing_requested)) RETURNING id INTO new_id;
            UPDATE media_assets SET source_record_id=new_id WHERE source_record_id=old_source.id AND session_id=classroom.session_id;
            mapping:=mapping||jsonb_build_array(jsonb_build_object('session_id',classroom.session_id,'source_id',new_id));
        END LOOP;
        INSERT INTO audit_logs(action,resource_type,resource_id,result,request_id,metadata)
            VALUES('source.split','source',old_source.id,'success','migration-003',jsonb_build_object('replacements',mapping));
        DELETE FROM source_records WHERE id=old_source.id;
    END LOOP;
END $$;
ALTER TABLE media_assets ADD CONSTRAINT fk_media_source_session
    FOREIGN KEY (source_record_id,session_id) REFERENCES source_records(id,session_id),
    ADD COLUMN playback_asset_id uuid,
    ADD CONSTRAINT fk_media_playback_session FOREIGN KEY(playback_asset_id,session_id) REFERENCES media_assets(id,session_id);

CREATE FUNCTION college_of(unit_id uuid) RETURNS uuid LANGUAGE sql STABLE AS $$
    WITH RECURSIVE parents AS (
        SELECT id,parent_id,kind,ARRAY[id] AS visited FROM teaching.org_units WHERE id=unit_id
        UNION ALL
        SELECT o.id,o.parent_id,o.kind,p.visited||o.id FROM teaching.org_units o
        JOIN parents p ON o.id=p.parent_id WHERE NOT o.id=ANY(p.visited)
    ) SELECT id FROM parents WHERE kind='college' LIMIT 1
$$;
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM course_offerings o
        JOIN courses c ON c.id=o.course_id JOIN class_groups g ON g.id=o.class_group_id
        WHERE college_of(o.org_unit_id) IS NULL
            OR college_of(o.org_unit_id) IS DISTINCT FROM college_of(c.org_unit_id)
            OR college_of(o.org_unit_id) IS DISTINCT FROM college_of(g.org_unit_id)) THEN
        RAISE EXCEPTION '003 blocked: legacy offering/course/class college ownership requires explicit correction';
    END IF;
END $$;
CREATE FUNCTION enforce_offering_scope() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE college uuid;
BEGIN
    IF TG_OP='UPDATE' AND (NEW.org_unit_id,NEW.term_id,NEW.course_id,NEW.teacher_id,NEW.class_group_id)
        IS DISTINCT FROM (OLD.org_unit_id,OLD.term_id,OLD.course_id,OLD.teacher_id,OLD.class_group_id)
        AND EXISTS(SELECT 1 FROM teaching.lesson_sessions WHERE offering_id=OLD.id) THEN
        RAISE EXCEPTION 'Offering ownership is immutable after a lesson exists' USING ERRCODE='23514';
    END IF;
    college:=teaching.college_of(NEW.org_unit_id);
    IF college IS NULL OR college IS DISTINCT FROM
        (SELECT teaching.college_of(org_unit_id) FROM teaching.courses WHERE id=NEW.course_id)
        OR college IS DISTINCT FROM
        (SELECT teaching.college_of(org_unit_id) FROM teaching.class_groups WHERE id=NEW.class_group_id) THEN
        RAISE EXCEPTION 'Offering, course and class must belong to the same college' USING ERRCODE='23514';
    END IF;
    IF NOT EXISTS(SELECT 1 FROM teaching.user_accounts u JOIN teaching.role_bindings r ON r.user_id=u.id
        WHERE u.id=NEW.teacher_id AND u.status='active' AND r.role_code='teacher') THEN
        RAISE EXCEPTION 'Offering teacher must be an active teacher account' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER ck_offering_scope BEFORE INSERT OR UPDATE OF org_unit_id,term_id,course_id,class_group_id,teacher_id
    ON course_offerings FOR EACH ROW EXECUTE FUNCTION enforce_offering_scope();
DO $$ DECLARE fk record; BEGIN
    FOR fk IN SELECT conname FROM pg_constraint WHERE conrelid='teaching.schedule_entries'::regclass
        AND confrelid='teaching.course_offerings'::regclass LOOP
        EXECUTE format('ALTER TABLE teaching.schedule_entries DROP CONSTRAINT %I',fk.conname);
    END LOOP;
END $$;
ALTER TABLE schedule_entries ADD CONSTRAINT fk_schedule_offering_resources FOREIGN KEY(offering_id,teacher_id,class_group_id)
    REFERENCES course_offerings(id,teacher_id,class_group_id) DEFERRABLE INITIALLY IMMEDIATE;
CREATE FUNCTION freeze_linked_schedule() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.offering_id,NEW.classroom_id,NEW.starts_at,NEW.ends_at)
        IS DISTINCT FROM (OLD.offering_id,OLD.classroom_id,OLD.starts_at,OLD.ends_at)
        AND EXISTS(SELECT 1 FROM teaching.lesson_sessions WHERE schedule_entry_id=OLD.id) THEN
        RAISE EXCEPTION 'A linked lesson retains its original schedule' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER ck_linked_schedule BEFORE UPDATE ON schedule_entries FOR EACH ROW EXECUTE FUNCTION freeze_linked_schedule();

-- Audit events survive removal from an editable report. Complete classroom deletion
-- still removes both events and observations through their report foreign keys.
ALTER TABLE report_observations ADD COLUMN removed_at timestamptz;
DO $$ DECLARE fk record; BEGIN
    FOR fk IN SELECT conname FROM pg_constraint WHERE conrelid='teaching.review_actions'::regclass
        AND confrelid='teaching.report_observations'::regclass LOOP
        EXECUTE format('ALTER TABLE teaching.review_actions DROP CONSTRAINT %I',fk.conname);
    END LOOP;
END $$;
ALTER TABLE review_actions ADD CONSTRAINT fk_review_observation_history
    FOREIGN KEY (observation_id,report_id,run_id,session_id)
    REFERENCES report_observations(id,report_id,run_id,session_id) DEFERRABLE INITIALLY IMMEDIATE;
ALTER TABLE review_actions DROP CONSTRAINT review_actions_action_check,
    ADD CONSTRAINT ck_review_action CHECK (action IN ('confirm','revise','reject','submit','confirm_report','publish','withdraw'));
ALTER TABLE reports
    ADD COLUMN summary_evidence_ids uuid[] NOT NULL DEFAULT '{}',
    ADD COLUMN content_sha256 text CHECK (content_sha256 ~ '^[a-f0-9]{64}$'),
    ADD COLUMN reviewed_content_sha256 text CHECK (reviewed_content_sha256 ~ '^[a-f0-9]{64}$'),
    ADD COLUMN reviewed_by uuid REFERENCES user_accounts(id),
    ADD COLUMN reviewed_at timestamptz,
    ADD CONSTRAINT ck_report_attestation CHECK (
        (reviewed_content_sha256 IS NULL AND reviewed_by IS NULL AND reviewed_at IS NULL)
        OR (reviewed_content_sha256 IS NOT NULL AND content_sha256 IS NOT NULL
            AND reviewed_content_sha256=content_sha256 AND reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL));
-- Existing publications remain historical facts; every new publication must be attested.
CREATE FUNCTION enforce_publication_attestation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status='published' AND (TG_OP='INSERT' OR OLD.status IS DISTINCT FROM 'published') THEN
        IF NEW.content_sha256 IS NULL OR NEW.reviewed_content_sha256 IS DISTINCT FROM NEW.content_sha256
            OR NEW.reviewed_by IS NULL OR NEW.reviewed_at IS NULL THEN
            RAISE EXCEPTION 'Publication requires confirmation of the complete content' USING ERRCODE='23514';
        END IF;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER ck_publication_attestation BEFORE INSERT OR UPDATE OF status ON reports
    FOR EACH ROW EXECUTE FUNCTION enforce_publication_attestation();

ALTER TABLE analysis_jobs DROP CONSTRAINT analysis_jobs_check2;
UPDATE analysis_jobs SET max_attempts=1 WHERE stage='report';
ALTER TABLE analysis_jobs ADD CONSTRAINT ck_report_once CHECK (stage<>'report' OR max_attempts=1),
    ADD COLUMN execution_deadline_at timestamptz,
    ADD COLUMN expected_execution_sha256 text CHECK (expected_execution_sha256 ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT ck_job_execution CHECK (status<>'running' OR
        (execution_deadline_at IS NOT NULL AND expected_execution_sha256 IS NOT NULL
            AND lease_expires_at<=execution_deadline_at)) NOT VALID;
ALTER TABLE job_artifacts ADD COLUMN session_id uuid,
    ADD COLUMN lease_token uuid;
UPDATE job_artifacts a SET session_id=r.session_id FROM analysis_runs r WHERE a.run_id=r.id;
ALTER TABLE job_artifacts ALTER COLUMN session_id SET NOT NULL,
    ADD CONSTRAINT fk_artifact_run_session FOREIGN KEY (run_id,session_id) REFERENCES analysis_runs(id,session_id),
    ADD CONSTRAINT fk_artifact_media_session FOREIGN KEY (media_asset_id,session_id) REFERENCES media_assets(id,session_id);

ALTER TABLE model_calls ADD COLUMN billing_period date,
    ADD COLUMN price_version text,
    ADD COLUMN request_sha256 text CHECK (request_sha256 ~ '^[a-f0-9]{64}$'),
    ADD COLUMN dispatch_started_at timestamptz,
    ADD COLUMN reconciled_by uuid REFERENCES user_accounts(id),
    ADD COLUMN reconciliation_reason text,
    ADD COLUMN lock_version integer NOT NULL DEFAULT 1 CHECK (lock_version>0);
UPDATE model_calls SET billing_period=date_trunc('month',created_at AT TIME ZONE 'Asia/Shanghai')::date;
ALTER TABLE model_calls ALTER COLUMN billing_period SET NOT NULL,
    ALTER COLUMN billing_period SET DEFAULT date_trunc('month',now() AT TIME ZONE 'Asia/Shanghai')::date,
    ADD CONSTRAINT ck_billing_month CHECK (extract(day FROM billing_period)=1),
    ADD CONSTRAINT ck_call_settlement CHECK (
        (status IN ('reserved','unknown') AND actual_cost IS NULL AND settled_at IS NULL)
        OR (status IN ('succeeded','failed') AND actual_cost IS NOT NULL AND settled_at IS NOT NULL)) NOT VALID;
CREATE UNIQUE INDEX ux_call_job ON model_calls(job_id) WHERE job_id IS NOT NULL;
CREATE INDEX ix_budget_period ON model_calls(currency,billing_period);

-- Short leases for governance jobs; they do not enter the Python claim API.
ALTER TABLE export_jobs ADD COLUMN executor_id text, ADD COLUMN lease_token uuid,
    ADD COLUMN lease_expires_at timestamptz, ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts>=0),
    ADD COLUMN available_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
    ADD CONSTRAINT ck_export_lease CHECK (
        (status='running' AND executor_id IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (status<>'running' AND executor_id IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL)) NOT VALID;
ALTER TABLE data_deletion_requests ADD COLUMN executor_id text, ADD COLUMN lease_token uuid,
    ADD COLUMN lease_expires_at timestamptz, ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts>=0),
    ADD COLUMN available_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN tombstone_id uuid UNIQUE,
    ADD CONSTRAINT ck_deletion_lease CHECK (
        (status='processing' AND executor_id IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (status<>'processing' AND executor_id IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL)) NOT VALID;
ALTER TABLE lesson_sessions ADD COLUMN storage_generation bigint NOT NULL DEFAULT 0 CHECK (storage_generation>=0);
CREATE INDEX ix_export_recovery ON export_jobs(status,available_at,lease_expires_at);
CREATE INDEX ix_deletion_recovery ON data_deletion_requests(status,available_at,lease_expires_at);
COMMIT;
