-- Only used by tools/capture_dev.py in its dedicated, disposable development database.
-- Fictional classroom; permission is for consenting developers' test recordings only.
-- Re-running never re-grants a revoked source or replaces existing business data.
BEGIN;
SET search_path TO teaching, public;
SELECT pg_advisory_xact_lock(hashtext('capture_dev_fixture_v1'));
INSERT INTO academic_terms(code,name,start_date,end_date)
VALUES ('CAPTURE-DEV','Capture development','2026-01-01','2027-12-31') ON CONFLICT DO NOTHING;
INSERT INTO courses(org_unit_id,code,name)
SELECT id,'CAPTURE-DEV','Capture development' FROM org_units WHERE code='SOFTWARE' ON CONFLICT DO NOTHING;
INSERT INTO class_groups(org_unit_id,code,name,expected_size,enrollment_year)
SELECT id,'CAPTURE-DEV','Consenting developers',1,2026 FROM org_units WHERE code='SOFTWARE' ON CONFLICT DO NOTHING;
INSERT INTO course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id)
SELECT o.id,'CAPTURE-DEV',t.id,c.id,u.id,g.id FROM org_units o,academic_terms t,courses c,user_accounts u,class_groups g
WHERE o.code='SOFTWARE' AND t.code='CAPTURE-DEV' AND c.code='CAPTURE-DEV' AND u.username='teacher_demo' AND g.code='CAPTURE-DEV'
ON CONFLICT DO NOTHING;
INSERT INTO lesson_sessions(id,offering_id,title,planned_start_at,planned_end_at,is_demo,created_by)
SELECT 'cb000000-0000-4000-8000-000000000001',o.id,'B0 capture development',now(),now()+interval '2 hours',true,u.id
FROM course_offerings o,user_accounts u WHERE o.code='CAPTURE-DEV' AND u.username='academic_demo' ON CONFLICT DO NOTHING;
INSERT INTO source_records(id,session_id,source_type,title,attribution,rights_status,requested_uses,allowed_uses,
    external_processing_requested,external_processing_allowed,authorization_note,registered_by,verified_by,verified_at)
SELECT 'cb000000-0000-4000-8000-000000000002','cb000000-0000-4000-8000-000000000001','self_recorded',
    'Consenting developers capture test','Development team','verified','["playback","analysis"]','["playback","analysis"]',
    false,false,'Development recordings only; no real classroom or external model authorization.',t.id,a.id,now()
FROM user_accounts t,user_accounts a WHERE t.username='teacher_demo' AND a.username='academic_demo' ON CONFLICT DO NOTHING;
COMMIT;
SELECT json_build_object('session_id',s.id,'source_record_id',r.id,'rights_status',r.rights_status,
    'allowed_uses',r.allowed_uses,'external_processing_allowed',r.external_processing_allowed)
FROM lesson_sessions s JOIN source_records r ON r.session_id=s.id
WHERE s.id='cb000000-0000-4000-8000-000000000001' AND r.id='cb000000-0000-4000-8000-000000000002';
