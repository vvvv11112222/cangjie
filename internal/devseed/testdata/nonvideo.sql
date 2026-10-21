-- Synthetic fixtures only. Run inside devseed.Apply, never as a migration.
-- Stable UUIDs use MD5 solely as a namespaced identifier, not for security.
CREATE TEMP TABLE nonvideo_seed_manifest(kind text, id uuid, PRIMARY KEY(kind,id)) ON COMMIT DROP;
CREATE FUNCTION pg_temp.nv_id(key text) RETURNS uuid LANGUAGE sql IMMUTABLE
AS $$ SELECT md5('cangjie:nonvideo-v1:' || key)::uuid $$;

-- One independent school, two populated colleges, one empty college, two departments.
INSERT INTO teaching.org_units(id,code,name,kind)
VALUES(pg_temp.nv_id('school'),'TESTDATA_SCHOOL','[合成测试]教学联调学校','school')
ON CONFLICT(id) DO NOTHING;
INSERT INTO teaching.org_units(id,parent_id,code,name,kind)
SELECT pg_temp.nv_id('college-'||n),pg_temp.nv_id('school'),'TESTDATA_COLLEGE_'||n,
       '[合成测试]'||(ARRAY['计算机学院','人文学院','空数据学院'])[n],'college'
FROM generate_series(1,3) n WHERE true ON CONFLICT(id) DO NOTHING;
INSERT INTO teaching.org_units(id,parent_id,code,name,kind)
SELECT pg_temp.nv_id('department-'||n),pg_temp.nv_id('college-'||n),'TESTDATA_DEPT_'||n,
       '[合成测试]'||(ARRAY['软件工程系','语言文化系'])[n],'department'
FROM generate_series(1,2) n WHERE true ON CONFLICT(id) DO NOTHING;
INSERT INTO nonvideo_seed_manifest
SELECT 'org_units',pg_temp.nv_id(key) FROM (
  SELECT 'school' key UNION ALL SELECT 'college-'||n FROM generate_series(1,3) n
  UNION ALL SELECT 'department-'||n FROM generate_series(1,2) n
) d;

CREATE TEMP TABLE nonvideo_seed_users(username text PRIMARY KEY, display_name text, org_key text,
                                     role_code text, scope_key text, account_status text) ON COMMIT DROP;
INSERT INTO nonvideo_seed_users VALUES
 ('testdata_admin','[合成测试]系统管理员','school','sys_admin',NULL,'active'),
 ('testdata_academic_1','[合成测试]计算机教务','college-1','academic_admin','college-1','active'),
 ('testdata_academic_2','[合成测试]人文教务','college-2','academic_admin','college-2','active'),
 ('testdata_supervisor_1','[合成测试]计算机督导','college-1','supervisor','college-1','active'),
 ('testdata_supervisor_2','[合成测试]人文督导','college-2','supervisor','college-2','active'),
 ('testdata_disabled','[合成测试]禁用教师','college-1','teacher',NULL,'disabled');
INSERT INTO nonvideo_seed_users
SELECT 'testdata_teacher_'||n,'[合成测试]教师'||n,'college-'||CASE WHEN n<=4 THEN 1 ELSE 2 END,
       'teacher',NULL,'active' FROM generate_series(1,8) n;
INSERT INTO teaching.user_accounts(id,org_unit_id,username,password_hash,display_name,status)
SELECT pg_temp.nv_id('user-'||u.username),pg_temp.nv_id(u.org_key),u.username,c.password_hash,u.display_name,u.account_status
FROM nonvideo_seed_users u CROSS JOIN nonvideo_seed_context c WHERE true ON CONFLICT(id) DO NOTHING;
-- Refuse incompatible identities rather than granting fixture roles to changed accounts.
DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM nonvideo_seed_users f JOIN teaching.user_accounts u ON u.id=pg_temp.nv_id('user-'||f.username)
            WHERE u.username<>f.username OR u.org_unit_id<>pg_temp.nv_id(f.org_key)) THEN
    RAISE EXCEPTION 'nonvideo-v1 account identity has changed; resolve explicitly before seeding';
  END IF;
END $$;
INSERT INTO teaching.role_bindings(id,user_id,role_code,scope_org_id)
SELECT pg_temp.nv_id('role-'||username),pg_temp.nv_id('user-'||username),role_code,
       CASE WHEN scope_key IS NULL THEN NULL ELSE pg_temp.nv_id(scope_key) END
FROM nonvideo_seed_users WHERE true ON CONFLICT(id) DO NOTHING;
INSERT INTO nonvideo_seed_manifest SELECT 'user_accounts',pg_temp.nv_id('user-'||username) FROM nonvideo_seed_users;
INSERT INTO nonvideo_seed_manifest SELECT 'role_bindings',pg_temp.nv_id('role-'||username) FROM nonvideo_seed_users;

INSERT INTO teaching.academic_terms(id,code,name,start_date,end_date) VALUES
 (pg_temp.nv_id('term-1'),'TESTDATA_2025_FALL','[合成测试]2025秋季','2025-09-01','2026-01-25'),
 (pg_temp.nv_id('term-2'),'TESTDATA_2026_SPRING','[合成测试]2026春季','2026-02-23','2026-07-12'),
 (pg_temp.nv_id('term-3'),'TESTDATA_2026_FALL','[合成测试]2026秋季','2026-09-01','2027-01-24')
ON CONFLICT(id) DO NOTHING;
INSERT INTO nonvideo_seed_manifest SELECT 'academic_terms',pg_temp.nv_id('term-'||n) FROM generate_series(1,3) n;

CREATE TEMP TABLE nonvideo_seed_courses(n integer PRIMARY KEY,name text,topic text,college integer) ON COMMIT DROP;
INSERT INTO nonvideo_seed_courses VALUES
 (1,'仓颉程序设计','变量与类型',1),(2,'数据结构','栈与队列',1),
 (3,'数据库原理','关系模型',1),(4,'软件工程','需求分析',1),
 (5,'大学英语','情境对话',2),(6,'中国文化','文字与文化',2),
 (7,'学术写作','论点与证据',2),(8,'教育心理学','学习动机',2);
INSERT INTO teaching.courses(id,org_unit_id,code,name,description)
SELECT pg_temp.nv_id('course-'||n),pg_temp.nv_id('department-'||college),'TESTDATA_COURSE_'||n,
       '[合成测试]'||name,'纯合成教务样例；主题：'||topic||'；无真实师生、媒体或分析结果。'
FROM nonvideo_seed_courses WHERE true ON CONFLICT(id) DO NOTHING;
INSERT INTO teaching.class_groups(id,org_unit_id,code,name,enrollment_year,expected_size)
SELECT pg_temp.nv_id('group-'||n),pg_temp.nv_id('department-'||college),'TESTDATA_GROUP_'||n,
       '[合成测试]'||name||'班',2024+((n-1)%3),
       CASE WHEN n=8 THEN NULL WHEN n=7 THEN 0 ELSE 30+n*2 END
FROM nonvideo_seed_courses WHERE true ON CONFLICT(id) DO NOTHING;
INSERT INTO teaching.classrooms(id,code,name,capacity)
SELECT pg_temp.nv_id('room-'||n),'TESTDATA_ROOM_'||n,
       '[合成测试]'||(ARRAY['小班教室','标准教室','研讨教室','阶梯教室','待登记容量教室'])[n],
       (ARRAY[30,50,80,120,NULL]::integer[])[n]
FROM generate_series(1,5) n WHERE true ON CONFLICT(id) DO NOTHING;
INSERT INTO nonvideo_seed_manifest SELECT 'courses',pg_temp.nv_id('course-'||n) FROM generate_series(1,8) n;
INSERT INTO nonvideo_seed_manifest SELECT 'class_groups',pg_temp.nv_id('group-'||n) FROM generate_series(1,8) n;
INSERT INTO nonvideo_seed_manifest SELECT 'classrooms',pg_temp.nv_id('room-'||n) FROM generate_series(1,5) n;

CREATE TEMP TABLE nonvideo_seed_offerings(n integer PRIMARY KEY,course_n integer,term_n integer) ON COMMIT DROP;
INSERT INTO nonvideo_seed_offerings SELECT n,n,3 FROM generate_series(1,8) n;
INSERT INTO nonvideo_seed_offerings SELECT n+8,n,CASE WHEN n<=2 THEN 1 ELSE 2 END FROM generate_series(1,4) n;
INSERT INTO teaching.course_offerings(id,org_unit_id,code,term_id,course_id,teacher_id,class_group_id,status)
SELECT pg_temp.nv_id('offering-'||o.n),pg_temp.nv_id('department-'||c.college),'TESTDATA_OFFERING_'||o.n,
       pg_temp.nv_id('term-'||o.term_n),pg_temp.nv_id('course-'||c.n),
       pg_temp.nv_id('user-testdata_teacher_'||c.n),pg_temp.nv_id('group-'||c.n),
       CASE WHEN o.term_n=3 THEN 'active' ELSE 'archived' END
FROM nonvideo_seed_offerings o JOIN nonvideo_seed_courses c ON c.n=o.course_n WHERE true ON CONFLICT(id) DO NOTHING;
INSERT INTO nonvideo_seed_manifest SELECT 'course_offerings',pg_temp.nv_id('offering-'||n) FROM generate_series(1,12) n;

CREATE TEMP TABLE nonvideo_seed_schedules ON COMMIT DROP AS
SELECT o.n offering_n,w.week_no,
       pg_temp.nv_id('schedule-'||o.n||'-'||w.week_no) id,
       pg_temp.nv_id('offering-'||o.n) offering_id,
       pg_temp.nv_id('user-testdata_teacher_'||o.course_n) teacher_id,
       pg_temp.nv_id('group-'||o.course_n) class_group_id,
       pg_temp.nv_id('room-'||(1+(o.course_n-1)%5)) classroom_id,
       ((CASE o.term_n WHEN 1 THEN '2025-10-13T08:00:00+08:00'::timestamptz
                      WHEN 2 THEN '2026-03-16T08:00:00+08:00'::timestamptz
                      ELSE '2026-10-19T08:00:00+08:00'::timestamptz END)
         + (o.course_n-1)*interval '1 day' + w.week_no*interval '7 days') starts_at
FROM nonvideo_seed_offerings o CROSS JOIN generate_series(0,3) w(week_no);
INSERT INTO teaching.schedule_entries(id,offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at,status)
SELECT id,offering_id,teacher_id,class_group_id,classroom_id,starts_at,starts_at+interval '45 minutes',
       CASE WHEN week_no=3 THEN 'cancelled' ELSE 'active' END
FROM nonvideo_seed_schedules WHERE true ON CONFLICT(id) DO NOTHING;
INSERT INTO nonvideo_seed_manifest SELECT 'schedule_entries',id FROM nonvideo_seed_schedules;

INSERT INTO teaching.lesson_sessions(id,offering_id,schedule_entry_id,title,planned_start_at,planned_end_at,is_demo,status,created_by)
SELECT pg_temp.nv_id('lesson-'||s.offering_n||'-'||s.week_no),s.offering_id,s.id,
       '[合成测试]'||c.name||' · 第'||(s.week_no+1)||'课：'||c.topic,
       s.starts_at,s.starts_at+interval '45 minutes',true,
       CASE WHEN o.term_n<3 OR s.week_no=0 THEN 'archived' ELSE 'planned' END,
       pg_temp.nv_id('user-testdata_admin')
FROM nonvideo_seed_schedules s JOIN nonvideo_seed_offerings o ON o.n=s.offering_n
JOIN nonvideo_seed_courses c ON c.n=o.course_n WHERE s.week_no<3 ON CONFLICT(id) DO NOTHING;
INSERT INTO nonvideo_seed_manifest SELECT 'lesson_sessions',pg_temp.nv_id('lesson-'||offering_n||'-'||week_no)
FROM nonvideo_seed_schedules WHERE week_no<3;
-- Unscheduled lessons exercise the nullable schedule link, without fake media-ready state.
INSERT INTO teaching.lesson_sessions(id,offering_id,title,planned_start_at,planned_end_at,is_demo,status,created_by)
SELECT pg_temp.nv_id('lesson-unscheduled-'||n),pg_temp.nv_id('offering-'||n),
       '[合成测试]'||name||' · 自主教研场次（无课表）',
       '2026-11-20T14:00:00+08:00'::timestamptz,'2026-11-20T14:45:00+08:00'::timestamptz,
       true,'planned',pg_temp.nv_id('user-testdata_admin')
FROM nonvideo_seed_courses WHERE true ON CONFLICT(id) DO NOTHING;
INSERT INTO nonvideo_seed_manifest SELECT 'lesson_sessions',pg_temp.nv_id('lesson-unscheduled-'||n) FROM generate_series(1,8) n;

-- Metadata for the source-registration form only. No permission is approved here.
INSERT INTO teaching.source_records(id,session_id,source_type,title,attribution,rights_status,allowed_uses,
    external_processing_allowed,redistribution_allowed,authorization_note,registered_by,requested_uses,external_processing_requested)
SELECT pg_temp.nv_id('source-'||n),pg_temp.nv_id('lesson-'||n||'-1'),'self_recorded',
       '[合成测试]来源登记表单样例 '||n,'程序生成的元数据；无真实师生、无音视频文件','pending','[]'::jsonb,
       false,false,'仅供表单和权限联调；不代表任何真实媒体已获授权。',pg_temp.nv_id('user-testdata_admin'),
       '["playback"]'::jsonb,false
FROM generate_series(1,12) n WHERE true ON CONFLICT(id) DO NOTHING;
INSERT INTO nonvideo_seed_manifest SELECT 'source_records',pg_temp.nv_id('source-'||n) FROM generate_series(1,12) n;

DROP FUNCTION pg_temp.nv_id(text);
