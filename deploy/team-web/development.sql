-- Fictional classroom for this dedicated LOCAL development database only.
-- Never deletes records or grants/re-grants any media source permissions.
BEGIN;
SET search_path TO teaching, public;
SELECT pg_advisory_xact_lock(hashtext('team_web_fixture_v1'));
INSERT INTO academic_terms(code,name,start_date,end_date)
VALUES ('TEAM-WEB-DEV','本机视觉验收学期','2026-01-01','2027-12-31') ON CONFLICT DO NOTHING;
INSERT INTO courses(org_unit_id,code,name)
SELECT id,'TEAM-WEB-DEV','课堂视觉分析试用' FROM org_units WHERE code='SOFTWARE' ON CONFLICT DO NOTHING;
INSERT INTO class_groups(org_unit_id,code,name,enrollment_year)
SELECT id,'TEAM-WEB-DEV','本机验收班级',2026 FROM org_units WHERE code='SOFTWARE' ON CONFLICT DO NOTHING;
INSERT INTO course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id)
SELECT o.id,'TEAM-WEB-DEV',t.id,c.id,u.id,g.id FROM org_units o,academic_terms t,courses c,user_accounts u,class_groups g
WHERE o.code='SOFTWARE' AND t.code='TEAM-WEB-DEV' AND c.code='TEAM-WEB-DEV' AND u.username='teacher_demo' AND g.code='TEAM-WEB-DEV'
ON CONFLICT DO NOTHING;
INSERT INTO lesson_sessions(id,offering_id,title,planned_start_at,planned_end_at,is_demo,created_by)
SELECT 'db000000-0000-4000-8000-000000000001',o.id,'视觉分析试用课堂',now(),now()+interval '1 hour',true,u.id
FROM course_offerings o,user_accounts u WHERE o.code='TEAM-WEB-DEV' AND u.username='academic_demo' ON CONFLICT DO NOTHING;
COMMIT;
