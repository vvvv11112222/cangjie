-- Explicit LOCAL development seed. Fictional registration data only:
-- no recordings, recognized behavior, speech, report, source approval or paid jobs.
BEGIN;
SET LOCAL search_path TO teaching, public;
SELECT pg_advisory_xact_lock(hashtext('team_web_demo_classrooms_v1'));
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM course_offerings WHERE code='TEAM-WEB-DEV')
     OR NOT EXISTS (SELECT 1 FROM user_accounts WHERE username='teacher_demo' AND status='active') THEN
    RAISE EXCEPTION 'Dedicated team-web development fixture is required';
  END IF;
END $$;
INSERT INTO courses(org_unit_id,code,name)
SELECT o.org_unit_id,'TEAM-DEMO-'||n, name||'（演示）'
FROM course_offerings o CROSS JOIN (VALUES (1,'操作系统'),(2,'数据结构'),(3,'数据库原理'),(4,'计算机网络'),(5,'软件工程')) AS c(n,name)
WHERE o.code='TEAM-WEB-DEV' ON CONFLICT DO NOTHING;
INSERT INTO course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id)
SELECT o.org_unit_id,c.code,o.term_id,c.id,o.teacher_id,o.class_group_id
FROM course_offerings o JOIN courses c ON c.org_unit_id=o.org_unit_id AND c.code LIKE 'TEAM-DEMO-%'
WHERE o.code='TEAM-WEB-DEV' ON CONFLICT DO NOTHING;
INSERT INTO classrooms(code,name) VALUES ('TEAM-DEMO-ROOM','演示教室（虚构）') ON CONFLICT DO NOTHING;
INSERT INTO schedule_entries(id,offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at)
SELECT md5('team-demo-schedule-v1-'||n)::uuid,o.id,o.teacher_id,o.class_group_id,r.id,
  timestamptz '2026-01-05 08:00:00+08' + ((n-1)/5)*interval '1 day' + ((n-1)%5)*interval '2 hours',
  timestamptz '2026-01-05 09:30:00+08' + ((n-1)/5)*interval '1 day' + ((n-1)%5)*interval '2 hours'
FROM generate_series(1,50) n JOIN course_offerings o ON o.code='TEAM-DEMO-'||((n-1)%5+1)
CROSS JOIN classrooms r WHERE r.code='TEAM-DEMO-ROOM' ON CONFLICT DO NOTHING;
INSERT INTO lesson_sessions(id,offering_id,schedule_entry_id,title,planned_start_at,planned_end_at,is_demo,created_by)
SELECT md5('team-demo-session-v1-'||n)::uuid,o.id,s.id,
  c.name||' · 第'||((n-1)/5+1)||'次课堂',s.starts_at,s.ends_at,true,u.id
FROM generate_series(1,50) n JOIN schedule_entries s ON s.id=md5('team-demo-schedule-v1-'||n)::uuid
JOIN course_offerings o ON o.id=s.offering_id JOIN courses c ON c.id=o.course_id
CROSS JOIN user_accounts u WHERE u.username='academic_demo' ON CONFLICT DO NOTHING;
-- Fail instead of silently treating a colliding or previously altered real row as a demo.
DO $$ BEGIN
  IF (SELECT count(*) FROM lesson_sessions WHERE id IN
      (SELECT md5('team-demo-session-v1-'||n)::uuid FROM generate_series(1,50) n)
      AND is_demo AND title LIKE '%（演示）%') <> 50 THEN
    RAISE EXCEPTION 'Expected 50 distinctly marked demo classrooms; transaction rolled back';
  END IF;
END $$;
COMMIT;
