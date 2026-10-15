package identity

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapDevelopmentData adds fictional classroom fixtures without scheduling
// jobs, granting external processing rights, or inventing media/model results.
func BootstrapDevelopmentData(ctx context.Context, pool *pgxpool.Pool, request DevelopmentUsersRequest) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('bootstrap_development_data'))`); err != nil {
		return err
	}
	var college, teacher, admin string
	err = tx.QueryRow(ctx, `SELECT o.id::text,t.id::text,a.id::text FROM teaching.org_units o
	 JOIN teaching.user_accounts t ON t.username=$2 AND t.org_unit_id=o.id
	 JOIN teaching.user_accounts a ON a.username=$3
	 WHERE o.code=$1 AND o.kind='college'
	 AND EXISTS(SELECT 1 FROM teaching.role_bindings WHERE user_id=t.id AND role_code='teacher')
	 AND EXISTS(SELECT 1 FROM teaching.role_bindings WHERE user_id=a.id AND role_code='sys_admin')`, request.CollegeCode, request.TeacherUsername, request.AdminUsername).Scan(&college, &teacher, &admin)
	if err != nil {
		return fmt.Errorf("development identities must be initialized first: %w", err)
	}
	// Stable IDs are private to this fixture namespace. Repeated imports never
	// overwrite edits, passwords, authorization decisions, or classroom status.
	queries := []string{
		`INSERT INTO teaching.academic_terms(id,code,name,start_date,end_date)
	 VALUES(md5('cangjie-dev-v1:term')::uuid,'DEV-2026-AUTUMN','测试·2026秋季学期','2026-09-01','2027-01-31') ON CONFLICT(id) DO NOTHING`,
		`INSERT INTO teaching.courses(id,org_unit_id,code,name,description)
	 SELECT md5('cangjie-dev-v1:course:'||n)::uuid,$1,'DEV-COURSE-0'||n,
	 CASE n WHEN 1 THEN '测试·数据库原理' ELSE '测试·软件工程' END,'完全虚构的联调课程，无真实师生信息'
	 FROM generate_series(1,2) n ON CONFLICT(id) DO NOTHING`,
		`INSERT INTO teaching.class_groups(id,org_unit_id,code,name,expected_size,enrollment_year)
	 SELECT md5('cangjie-dev-v1:group:'||n)::uuid,$1,'DEV-GROUP-0'||n,'测试·软件260'||n||'班',30+n*5,2026
	 FROM generate_series(1,2) n ON CONFLICT(id) DO NOTHING`,
		`INSERT INTO teaching.classrooms(id,code,name,capacity)
	 SELECT md5('cangjie-dev-v1:room:'||n)::uuid,'DEV-ROOM-0'||n,'测试·教学楼10'||n,60
	 FROM generate_series(1,2) n ON CONFLICT(id) DO NOTHING`,
		`INSERT INTO teaching.course_offerings(id,org_unit_id,code,term_id,course_id,teacher_id,class_group_id)
	 SELECT md5('cangjie-dev-v1:offering:'||n)::uuid,$1,'DEV-OFFERING-0'||n,
	 md5('cangjie-dev-v1:term')::uuid,md5('cangjie-dev-v1:course:'||n)::uuid,$2,md5('cangjie-dev-v1:group:'||n)::uuid
	 FROM generate_series(1,2) n ON CONFLICT(id) DO NOTHING`,
		`INSERT INTO teaching.schedule_entries(id,offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at)
	 SELECT md5('cangjie-dev-v1:schedule:'||n||':'||week)::uuid,md5('cangjie-dev-v1:offering:'||n)::uuid,$2,
	 md5('cangjie-dev-v1:group:'||n)::uuid,md5('cangjie-dev-v1:room:'||n)::uuid,
	 '2026-10-19 08:00:00+08'::timestamptz+(week*7)*interval '1 day'+((n-1)*2)*interval '1 hour',
	 '2026-10-19 09:30:00+08'::timestamptz+(week*7)*interval '1 day'+((n-1)*2)*interval '1 hour'
	 FROM generate_series(1,2) n CROSS JOIN generate_series(0,2) week ON CONFLICT(id) DO NOTHING`,
		`INSERT INTO teaching.lesson_sessions(id,offering_id,schedule_entry_id,title,planned_start_at,planned_end_at,is_demo,status,created_by)
	 SELECT md5('cangjie-dev-v1:session:'||n||':'||week)::uuid,s.offering_id,s.id,
	 '测试·'||CASE n WHEN 1 THEN '数据库原理' ELSE '软件工程' END||'第'||(week+1)||'讲',
	 s.starts_at,s.ends_at,true,CASE WHEN week=2 THEN 'archived' ELSE 'planned' END,$3
	 FROM generate_series(1,2) n CROSS JOIN generate_series(0,2) week
	 JOIN teaching.schedule_entries s ON s.id=md5('cangjie-dev-v1:schedule:'||n||':'||week)::uuid
	 ON CONFLICT(id) DO NOTHING`,
		`INSERT INTO teaching.source_records(id,session_id,source_type,title,attribution,rights_status,registered_by,verified_by,verified_at,authorization_note)
	 SELECT md5('cangjie-dev-v1:source:'||n||':'||week)::uuid,md5('cangjie-dev-v1:session:'||n||':'||week)::uuid,
	 'self_recorded','测试·虚构来源登记','开发团队合成样例，无对应真实录像',
	 CASE week WHEN 0 THEN 'pending' WHEN 1 THEN 'verified' ELSE 'rejected' END,$3,
	 CASE WHEN week=1 THEN $3::uuid ELSE NULL END,CASE WHEN week=1 THEN now() ELSE NULL END,
	 '仅测试来源审核状态；未授予媒体处理、外部模型或再分发用途'
	 FROM generate_series(1,2) n CROSS JOIN generate_series(0,2) week ON CONFLICT(id) DO NOTHING`,
	}
	for i, query := range queries {
		query = "WITH fixture_context AS (SELECT $1::uuid college,$2::uuid teacher,$3::uuid admin) " + query
		args := []any{college, teacher, admin}
		if _, err = tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("seed development data step %d: %w", i+1, err)
		}
	}
	return tx.Commit(ctx)
}
