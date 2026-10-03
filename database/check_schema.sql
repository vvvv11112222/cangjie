-- 仅在隔离测试数据库执行；所有演示数据与临时校验函数随事务回滚。
-- 先执行 001、002、003，再执行本文件。不是生产迁移。
BEGIN;
SET search_path TO teaching, public;
SET CONSTRAINTS ALL IMMEDIATE;

CREATE FUNCTION pg_temp.expect_sqlstate(statement text, expected text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE caught text;
BEGIN
    BEGIN
        EXECUTE statement;
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS caught = RETURNED_SQLSTATE;
        IF caught <> expected THEN
            RAISE EXCEPTION 'Expected %, got % for %', expected, caught, statement;
        END IF;
    END;
    IF caught IS NULL THEN
        RAISE EXCEPTION 'Expected failure %, statement succeeded: %', expected, statement;
    END IF;
END;
$$;

DO $$
DECLARE
    org_id uuid;
    user_id uuid;
    term_id uuid;
    course_id uuid;
    group_id uuid;
    room_id uuid;
    offering_id uuid;
    session_a uuid;
    session_b uuid;
    source_id uuid;
    media_id uuid;
    run_a uuid;
    run_b uuid;
    job_id uuid;
    evidence_a uuid;
    evidence_b uuid;
    report_id uuid;
    observation_id uuid;
    claimed_token uuid;
    affected integer;
    user_other uuid;
    group_other uuid;
    room_other uuid;
    offering_same_teacher uuid;
    offering_same_class uuid;
    offering_other uuid;
    revision_a uuid;
    revision_new uuid;
    media_other uuid;
    source_other uuid;
    claimed_stage text;
    budget_call_id uuid;
    report_job_id uuid;
    org_other uuid;
    course_other uuid;
BEGIN
    INSERT INTO org_units(code,name,kind) VALUES ('__check_college','Demo college','college') RETURNING id INTO org_id;
    INSERT INTO user_accounts(org_unit_id,username,password_hash,display_name)
        VALUES (org_id,'__check_user','not-a-real-password-hash','Demo teacher') RETURNING id INTO user_id;
    INSERT INTO role_bindings(user_id,role_code) VALUES (user_id,'teacher');
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO role_bindings(user_id,role_code) VALUES (%L,''teacher'')',user_id),'23505');
    INSERT INTO academic_terms(code,name,start_date,end_date)
        VALUES ('__check_term','Demo term','2026-09-01','2027-01-31') RETURNING id INTO term_id;
    INSERT INTO courses(org_unit_id,code,name) VALUES (org_id,'__check_course','Demo course') RETURNING id INTO course_id;
    INSERT INTO class_groups(org_unit_id,code,name,enrollment_year) VALUES (org_id,'__check_group','Demo class',2026) RETURNING id INTO group_id;
    INSERT INTO classrooms(code,name) VALUES ('__check_room','Demo room') RETURNING id INTO room_id;
    INSERT INTO course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id)
        VALUES (org_id,'__check_offering',term_id,course_id,user_id,group_id) RETURNING id INTO offering_id;
    INSERT INTO schedule_entries(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at)
        VALUES (offering_id,user_id,group_id,room_id,'2026-09-30 09:00+08','2026-09-30 10:00+08');
    -- 三类资源分别隔离验证，避免只验证到第一个排斥约束。
    INSERT INTO user_accounts(org_unit_id,username,password_hash,display_name)
        VALUES (org_id,'__check_other_user','not-a-real-password-hash','Other teacher') RETURNING id INTO user_other;
    INSERT INTO role_bindings(user_id,role_code) VALUES (user_other,'teacher');
    INSERT INTO class_groups(org_unit_id,code,name,enrollment_year)
        VALUES (org_id,'__check_other_group','Other class',2025) RETURNING id INTO group_other;
    INSERT INTO classrooms(code,name) VALUES ('__check_other_room','Other room') RETURNING id INTO room_other;
    INSERT INTO course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id)
        VALUES (org_id,'__check_same_teacher',term_id,course_id,user_id,group_other) RETURNING id INTO offering_same_teacher;
    INSERT INTO course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id)
        VALUES (org_id,'__check_same_class',term_id,course_id,user_other,group_id) RETURNING id INTO offering_same_class;
    INSERT INTO course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id)
        VALUES (org_id,'__check_other_offering',term_id,course_id,user_other,group_other) RETURNING id INTO offering_other;
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO schedule_entries(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at)
        VALUES (%L,%L,%L,%L,''2026-09-30 09:30+08'',''2026-09-30 10:30+08'')',
        offering_same_teacher,user_id,group_other,room_other),'23P01');
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO schedule_entries(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at)
        VALUES (%L,%L,%L,%L,''2026-09-30 09:30+08'',''2026-09-30 10:30+08'')',
        offering_same_class,user_other,group_id,room_other),'23P01');
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO schedule_entries(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at)
        VALUES (%L,%L,%L,%L,''2026-09-30 09:30+08'',''2026-09-30 10:30+08'')',
        offering_other,user_other,group_other,room_id),'23P01');
    -- 所有资源不同时，同一时段允许。
    INSERT INTO schedule_entries(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at)
        VALUES (offering_other,user_other,group_other,room_other,'2026-09-30 09:00+08','2026-09-30 10:00+08');
    -- 相邻时段同样允许。
    INSERT INTO schedule_entries(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at)
        VALUES (offering_id,user_id,group_id,room_id,'2026-09-30 10:00+08','2026-09-30 11:00+08');
    -- 已取消和有效课表可以在同一时段并存。
    INSERT INTO schedule_entries(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at,status)
        VALUES (offering_id,user_id,group_id,room_id,'2026-09-30 09:30+08','2026-09-30 10:30+08','cancelled');
    INSERT INTO lesson_sessions(offering_id,title,planned_start_at,planned_end_at,is_demo,created_by)
        VALUES (offering_id,'Demo A','2026-09-30 09:00+08','2026-09-30 10:00+08',true,user_id)
        RETURNING id INTO session_a;
    INSERT INTO lesson_sessions(offering_id,title,planned_start_at,planned_end_at,is_demo,created_by)
        VALUES (offering_id,'Demo B','2026-09-30 10:00+08','2026-09-30 11:00+08',true,user_id)
        RETURNING id INTO session_b;
    INSERT INTO source_records(session_id,source_type,title,attribution,rights_status,allowed_uses,
        registered_by,verified_by,verified_at)
        VALUES (session_a,'self_recorded','Self recorded fixture','Demo team','verified','["playback","analysis"]',
        user_id,user_id,now()) RETURNING id INTO source_id;
    INSERT INTO media_assets(session_id,source_record_id,kind,is_primary,storage_backend,
        object_key,sha256,mime_type,byte_size,duration_ms,status)
        VALUES (session_a,source_id,'source',true,'filesystem','fixture/source.mp4',repeat('a',64),
        'video/mp4',100,600000,'ready') RETURNING id INTO media_id;
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO media_assets(session_id,source_record_id,kind,is_primary,storage_backend,
        object_key,sha256,mime_type,byte_size) VALUES (%L,%L,''source'',true,''filesystem'',
        ''fixture/duplicate.mp4'',repeat(''b'',64),''video/mp4'',100)',session_a,source_id),'23505');
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO media_assets(session_id,parent_asset_id,kind,storage_backend,object_key,sha256,mime_type,byte_size)
        VALUES (%L,%L,''proxy'',''filesystem'',''fixture/cross.mp4'',repeat(''c'',64),''video/mp4'',100)',
        session_b,media_id),'23503');
    INSERT INTO analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,
        config_hash,pipeline_version,config_snapshot,status)
        VALUES (session_a,media_id,user_id,'__check_run_a',repeat('a',64),repeat('b',64),'v0.3','{}','succeeded')
        RETURNING id INTO run_a;
    INSERT INTO analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,
        config_hash,pipeline_version,config_snapshot,status)
        VALUES (session_a,media_id,user_id,'__check_run_b',repeat('a',64),repeat('b',64),'v0.3','{}','succeeded')
        RETURNING id INTO run_b;
    UPDATE analysis_runs SET status='running' WHERE id=run_a;
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,
        config_hash,pipeline_version,config_snapshot) VALUES (%L,%L,%L,''__check_duplicate_active'',
        repeat(''a'',64),repeat(''b'',64),''v0.3'',''{}'')',session_a,media_id,user_id),'23505');
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,
        config_hash,pipeline_version,config_snapshot) VALUES (%L,%L,%L,''__check_cross'',
        repeat(''a'',64),repeat(''b'',64),''v0.3'',''{}'')',session_b,media_id,user_id),'23503');
    INSERT INTO analysis_jobs(run_id,stage) VALUES (run_a,'probe') RETURNING id INTO job_id;
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO analysis_jobs(run_id,stage) VALUES (%L,''probe'')',run_a),'23505');
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE analysis_jobs SET status=''running'' WHERE id=%L',job_id),'23514');
    -- 领取必须设置完整租约；本检查不模拟多连接并发。
    WITH candidate AS (
        SELECT j.id FROM analysis_jobs j
        JOIN analysis_runs r ON r.id=j.run_id
        JOIN lesson_sessions s ON s.id=r.session_id
        WHERE j.id = job_id AND j.status = 'queued' AND j.available_at <= now() AND j.attempts < j.max_attempts
            AND j.stage IN ('probe','audio_analysis','video_analysis')
            AND r.status IN ('queued','running') AND s.status NOT IN ('deleting','deleted')
        FOR UPDATE OF j SKIP LOCKED
    )
    UPDATE analysis_jobs j
        SET status='running',attempts=attempts+1,worker_id='check-worker',
            execution_deadline_at=now()+interval '30 minutes',expected_execution_sha256=repeat('a',64),
            lease_token=gen_random_uuid(),lease_expires_at=now()+interval '120 seconds'
        FROM candidate c WHERE j.id=c.id
        RETURNING j.lease_token INTO claimed_token;
    IF claimed_token IS NULL THEN RAISE EXCEPTION 'Task was not claimed'; END IF;
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE analysis_jobs SET lease_expires_at=execution_deadline_at+interval ''1 second'' WHERE id=%L',job_id),'23514');
    UPDATE analysis_jobs SET progress=99 WHERE id=job_id AND lease_token=gen_random_uuid()
        AND status='running' AND lease_expires_at>now();
    GET DIAGNOSTICS affected = ROW_COUNT;
    IF affected <> 0 THEN RAISE EXCEPTION 'Stale token was accepted'; END IF;
    UPDATE analysis_jobs SET status='succeeded',progress=100,last_completed_token=claimed_token,
        worker_id=NULL,lease_token=NULL,lease_expires_at=NULL
        WHERE id=job_id AND lease_token=claimed_token AND lease_expires_at>now();
    GET DIAGNOSTICS affected = ROW_COUNT;
    IF affected <> 1 THEN RAISE EXCEPTION 'Valid token was rejected'; END IF;
    UPDATE analysis_runs SET status='succeeded' WHERE id=run_a;
    INSERT INTO evidence_items(run_id,session_id,media_asset_id,kind,start_ms,end_ms,description)
        VALUES (run_a,session_a,media_id,'manual',1000,5000,'Observed fixture') RETURNING id INTO evidence_a;
    INSERT INTO evidence_items(run_id,session_id,media_asset_id,kind,start_ms,end_ms,description)
        VALUES (run_b,session_a,media_id,'manual',1000,5000,'Another run fixture') RETURNING id INTO evidence_b;
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO evidence_items(run_id,session_id,media_asset_id,kind,start_ms,end_ms,description)
        VALUES (%L,%L,%L,''manual'',5000,1000,''Invalid time'')',run_a,session_a,media_id),'23514');
    INSERT INTO reports(run_id,session_id,revision,created_by)
        VALUES (run_a,session_a,1,user_id) RETURNING id INTO report_id;
    INSERT INTO report_dimensions(report_id,dimension_code,coverage_status,limitation)
        SELECT report_id,d,CASE WHEN d='content' THEN 'observed' ELSE 'insufficient' END,
        CASE WHEN d='content' THEN '' ELSE 'Fixture has limited coverage' END
        FROM unnest(ARRAY['content','pace','thinking','expression','management','technology']) AS t(d);
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE report_dimensions SET coverage_status=''insufficient'',limitation=''''
        WHERE report_id=%L AND dimension_code=''content''',report_id),'23514');
    INSERT INTO report_observations(report_id,run_id,session_id,dimension_code,observation_type,
        observation_text,review_status)
        VALUES (report_id,run_a,session_a,'content','observation','Fixture fact','accepted')
        RETURNING id INTO observation_id;
    INSERT INTO observation_evidence(observation_id,report_id,evidence_id,run_id,session_id)
        VALUES (observation_id,report_id,evidence_a,run_a,session_a);
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO observation_evidence(observation_id,report_id,evidence_id,run_id,session_id)
        VALUES (%L,%L,%L,%L,%L)',observation_id,report_id,evidence_b,run_a,session_a),'23503');
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE reports SET status=''published'' WHERE id=%L',report_id),'23514');
    UPDATE reports SET status='published',published_by=user_id,published_at=now(),
        content_sha256=repeat('a',64),reviewed_content_sha256=repeat('a',64),reviewed_by=user_id,reviewed_at=now()
        WHERE id=report_id;
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO reports(run_id,session_id,revision,created_by,status,published_by,published_at,
        content_sha256,reviewed_content_sha256,reviewed_by,reviewed_at)
        VALUES (%L,%L,1,%L,''published'',%L,now(),repeat(''a'',64),repeat(''a'',64),%L,now())',
        run_b,session_a,user_id,user_id,user_id),'23505');
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO model_calls(run_id,job_id,call_key,provider,model_name,prompt_version,reserved_cost)
        VALUES (%L,%L,''__check_call'',''fixture'',''fixture'',''v0.3'',0)',run_b,job_id),'23503');
    -- 不依赖 reports 的转写全量快照，以及 report_only 的固定修订输入。
    INSERT INTO transcript_revisions(session_id,media_asset_id,source_run_id,revision_no,source_type,content_sha256,created_by,reason)
        VALUES (session_a,media_id,run_a,1,'asr',repeat('d',64),user_id,'ASR output') RETURNING id INTO revision_a;
    INSERT INTO transcript_revision_segments(revision_id,segment_no,start_ms,end_ms,text_content)
        VALUES (revision_a,0,1000,5000,'Original transcript');
    INSERT INTO transcript_revisions(session_id,media_asset_id,source_run_id,parent_revision_id,revision_no,source_type,content_sha256,created_by,reason)
        VALUES (session_a,media_id,run_a,revision_a,2,'manual',repeat('e',64),user_id,'Correct terminology') RETURNING id INTO revision_new;
    INSERT INTO transcript_revision_segments(revision_id,segment_no,start_ms,end_ms,text_content)
        VALUES (revision_new,0,1000,5000,'Corrected transcript');
    UPDATE analysis_runs SET mode='report_only',input_transcript_revision_id=revision_new,config_hash=repeat('f',64)
        WHERE id=run_b;
    IF (SELECT text_content FROM transcript_revision_segments WHERE revision_id=revision_a) <> 'Original transcript'
        THEN RAISE EXCEPTION 'Original snapshot changed'; END IF;
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE analysis_runs SET mode=''report_only'',input_transcript_revision_id=NULL WHERE id=%L',run_b),'23514');
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO transcript_revision_segments(revision_id,segment_no,start_ms,end_ms,text_content)
        VALUES (%L,1,5000,1000,''bad'')',revision_new),'23514');
    INSERT INTO source_records(session_id,source_type,title,attribution,registered_by)
        VALUES (session_b,'self_recorded','Other source','Demo team',user_id) RETURNING id INTO source_other;
    INSERT INTO media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size)
        VALUES (session_b,source_other,'source','filesystem','fixture/other.mp4',repeat('c',64),'video/mp4',100)
        RETURNING id INTO media_other;
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,
        pipeline_version,config_snapshot,mode,input_transcript_revision_id)
        VALUES (%L,%L,%L,''__cross_revision'',repeat(''c'',64),repeat(''d'',64),''v0.4'',''{}'',''report_only'',%L)',
        session_b,media_other,user_id,revision_new),'23503');
    -- Go 专属报告任务不得落入 Python 的领取集合。
    UPDATE analysis_runs SET status='running' WHERE id=run_a;
    INSERT INTO analysis_jobs(run_id,stage,priority,max_attempts) VALUES (run_a,'report',999,1);
    INSERT INTO analysis_jobs(run_id,stage,priority) VALUES (run_a,'audio_analysis',0);
    SELECT j.stage INTO claimed_stage FROM analysis_jobs j
        JOIN analysis_runs r ON r.id=j.run_id JOIN lesson_sessions s ON s.id=r.session_id
        WHERE j.status='queued' AND j.stage IN ('probe','audio_analysis','video_analysis')
          AND j.available_at<=now() AND j.attempts<j.max_attempts
          AND r.status IN ('queued','running') AND s.status NOT IN ('deleting','deleted')
        ORDER BY j.priority DESC,j.available_at,j.created_at LIMIT 1;
    IF claimed_stage IS DISTINCT FROM 'audio_analysis' THEN
        RAISE EXCEPTION 'Worker did not select media job ahead of Go-only report: %',claimed_stage;
    END IF;
    SELECT id INTO report_job_id FROM analysis_jobs WHERE run_id=run_a AND stage='report';
    INSERT INTO model_calls(run_id,job_id,call_key,provider,model_name,prompt_version,reserved_cost,actual_cost,status,settled_at)
        VALUES (run_a,report_job_id,'__retained_accounting','fixture','fixture','v0.6',1,1,'succeeded',now())
        RETURNING id INTO budget_call_id;
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE model_calls SET run_id=NULL WHERE id=%L',budget_call_id),'23514');
    UPDATE model_calls SET run_id=NULL,job_id=NULL WHERE id=budget_call_id;
    IF (SELECT actual_cost FROM model_calls WHERE id=budget_call_id) IS DISTINCT FROM 1::numeric THEN
        RAISE EXCEPTION 'Detaching content erased settled budget';
    END IF;
    -- Review regressions: each assertion reproduces a previously accepted/rejected bad boundary.
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE course_offerings SET teacher_id=%L WHERE id=%L',user_other,offering_id),'23514');
    UPDATE lesson_sessions SET schedule_entry_id=(SELECT id FROM schedule_entries
        WHERE starts_at='2026-09-30 09:00+08' AND classroom_id=room_id) WHERE id=session_a;
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE schedule_entries SET ends_at=''2026-09-30 09:59+08'' WHERE id=(SELECT schedule_entry_id FROM lesson_sessions WHERE id=%L)',session_a),'23514');
    -- No lesson yet: deferred resource FK permits an atomic change in both tables.
    SET CONSTRAINTS fk_schedule_offering_resources DEFERRED;
    UPDATE course_offerings SET teacher_id=user_id WHERE id=offering_other;
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE schedule_entries SET teacher_id=%L WHERE offering_id=%L',user_id,offering_other),'23P01');
    UPDATE course_offerings SET teacher_id=user_other WHERE id=offering_other;
    UPDATE schedule_entries SET starts_at='2026-09-30 12:00+08',ends_at='2026-09-30 13:00+08' WHERE schedule_entries.offering_id=offering_other;
    UPDATE course_offerings SET teacher_id=user_id WHERE id=offering_other;
    UPDATE schedule_entries SET teacher_id=user_id WHERE schedule_entries.offering_id=offering_other;
    SET CONSTRAINTS fk_schedule_offering_resources IMMEDIATE;
    INSERT INTO org_units(code,name,kind) VALUES('__check_other_college','Other college','college') RETURNING id INTO org_other;
    INSERT INTO courses(org_unit_id,code,name) VALUES(org_other,'__check_other_course','Other college course') RETURNING id INTO course_other;
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE course_offerings SET course_id=%L WHERE id=%L',course_other,offering_other),'23514');
    UPDATE user_accounts SET status='disabled' WHERE id=user_other;
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE course_offerings SET teacher_id=%L WHERE id=%L',user_other,offering_other),'23514');
    EXECUTE format('UPDATE report_dimensions SET coverage=''[{"start_ms":1000,"end_ms":5000}]''::jsonb WHERE report_id=%L',report_id);
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE report_dimensions SET coverage=''{}''::jsonb WHERE report_id=%L',report_id),'23514');
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE media_assets SET source_record_id=%L WHERE id=%L',source_id,media_other),'23503');
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO job_artifacts(job_id,run_id,session_id,artifact_key,media_asset_id) VALUES (%L,%L,%L,''cross-scope'',%L)',
        job_id,run_a,session_a,media_other),'23503');
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE analysis_jobs SET max_attempts=2 WHERE id=%L',report_job_id),'23514');
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE model_calls SET actual_cost=NULL WHERE id=%L',budget_call_id),'23514');
    -- The same raw key belongs to a different namespace in a different classroom.
    INSERT INTO analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,
        pipeline_version,config_snapshot,mode,request_sha256)
        VALUES(session_b,media_other,user_id,'__check_run_a',repeat('c',64),repeat('d',64),'v0.6','{}','media_prepare',repeat('e',64));
    PERFORM pg_temp.expect_sqlstate(format(
        'UPDATE analysis_runs SET input_transcript_revision_id=%L WHERE session_id=%L AND mode=''media_prepare''',
        revision_new,session_b),'23514');
    INSERT INTO review_actions(report_id,run_id,session_id,observation_id,reviewer_id,action,reason)
        VALUES(report_id,run_a,session_a,observation_id,user_id,'confirm','synthetic review history');
    PERFORM pg_temp.expect_sqlstate(format('DELETE FROM report_observations WHERE id=%L',observation_id),'23503');
    UPDATE report_observations SET removed_at=now() WHERE id=observation_id;
    IF NOT EXISTS(SELECT 1 FROM review_actions WHERE reason='synthetic review history') THEN
        RAISE EXCEPTION 'Review history was removed';
    END IF;
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO export_jobs(report_id,run_id,session_id,requested_by,format,status)
        VALUES(%L,%L,%L,%L,''json'',''running'')',report_id,run_a,session_a,user_id),'23514');
    PERFORM pg_temp.expect_sqlstate(format(
        'INSERT INTO data_deletion_requests(session_id,requested_by,reason,status)
        VALUES(%L,%L,''synthetic'',''processing'')',session_a,user_id),'23514');
    -- Full classroom cleanup may remove the report and its history together.
    DELETE FROM reports WHERE id=report_id;
    IF EXISTS(SELECT 1 FROM review_actions WHERE reason='synthetic review history') THEN
        RAISE EXCEPTION 'Complete report cleanup left review content';
    END IF;
    RAISE NOTICE 'PASS: v0.6 constraints, leases, snapshots, namespaces, scope, coverage, review history and accounting';
END;
$$;
ROLLBACK;
