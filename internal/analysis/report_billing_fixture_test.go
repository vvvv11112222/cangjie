package analysis

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/database"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type reportFixture struct {
	pool                                               *pgxpool.Pool
	store                                              *storage.Local
	admin                                              identity.Principal
	session, source, media, run, report, job, evidence string
}

func setupReportFixture(t *testing.T) reportFixture {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	lock, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('cangjie_integration_test_fixture',0))`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close(context.Background()) })
	migration, _ := filepath.Abs("../../database")
	if err := database.RunMigrations(ctx, dsn, migration); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Exec(ctx, `TRUNCATE teaching.academic_terms,teaching.classrooms,teaching.org_units CASCADE`); err != nil {
		t.Fatal(err)
	}
	password := "synthetic-review-password"
	if _, err = identity.BootstrapAdmin(ctx, pool, identity.BootstrapRequest{SchoolCode: "SCHOOL", SchoolName: "Synthetic", Username: "admin", DisplayName: "Admin", Password: password}); err != nil {
		t.Fatal(err)
	}
	if _, err = identity.BootstrapDevelopmentUsers(ctx, pool, identity.DevelopmentUsersRequest{SchoolCode: "SCHOOL", SchoolName: "Synthetic", CollegeCode: "COL-A", CollegeName: "Synthetic", AcademicUsername: "academic", AcademicDisplayName: "Academic", SupervisorUsername: "supervisor", SupervisorDisplayName: "Supervisor", TeacherUsername: "teacher", TeacherDisplayName: "Teacher", Password: password}); err != nil {
		t.Fatal(err)
	}
	f := reportFixture{pool: pool}
	f.store, err = storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := func(sql string, args ...any) string {
		var v string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	college := id(`SELECT id::text FROM teaching.org_units WHERE code='COL-A'`)
	teacher := id(`SELECT id::text FROM teaching.user_accounts WHERE username='teacher'`)
	f.admin = identity.Principal{UserID: id(`SELECT id::text FROM teaching.user_accounts WHERE username='admin'`), Roles: []identity.RoleBinding{{RoleCode: "sys_admin"}}}
	term := id(`INSERT INTO teaching.academic_terms(code,name,start_date,end_date) VALUES('T','Synthetic','2026-09-01','2027-01-01') RETURNING id::text`)
	course := id(`INSERT INTO teaching.courses(org_unit_id,code,name) VALUES($1,'C','Synthetic') RETURNING id::text`, college)
	group := id(`INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES($1,'G','Synthetic',2026) RETURNING id::text`, college)
	offering := id(`INSERT INTO teaching.course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id) VALUES($1,'O',$2,$3,$4,$5) RETURNING id::text`, college, term, course, teacher, group)
	f.session = id(`INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by,status) VALUES($1,'Synthetic classroom',now(),now()+interval '1 hour',$2,'ready') RETURNING id::text`, offering, teacher)
	f.source = id(`INSERT INTO teaching.source_records(session_id,source_type,title,attribution,rights_status,allowed_uses,external_processing_allowed,registered_by,verified_by,verified_at) VALUES($1,'self_recorded','Synthetic source','Generated review fixture','verified','["playback","analysis","export"]',true,$2,$3,now()) RETURNING id::text`, f.session, teacher, f.admin.UserID)
	f.media = id(`INSERT INTO teaching.media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size,duration_ms,status) VALUES($1,$2,'source','filesystem','synthetic.mp4',$3,'video/mp4',12,60000,'ready') RETURNING id::text`, f.session, f.source, strings.Repeat("a", 64))
	f.run = id(`INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,pipeline_version,config_snapshot,status,request_sha256,finished_at) VALUES($1,$2,$3,'run',$4,$4,'p0-v1','{}','succeeded',$4,now()) RETURNING id::text`, f.session, f.media, teacher, strings.Repeat("a", 64))
	segment := id(`INSERT INTO teaching.transcript_segments(run_id,session_id,segment_no,start_ms,end_ms,text_content) VALUES($1,$2,0,0,1000,'Synthetic speech') RETURNING id::text`, f.run, f.session)
	f.evidence = id(`INSERT INTO teaching.evidence_items(run_id,session_id,media_asset_id,transcript_segment_id,kind,start_ms,end_ms,description) VALUES($1,$2,$3,$4,'transcript',0,1000,'Synthetic speech') RETURNING id::text`, f.run, f.session, f.media, segment)
	f.report = id(`INSERT INTO teaching.reports(run_id,session_id,revision,status,summary,content_sha256,reviewed_content_sha256,reviewed_by,reviewed_at,created_by,published_by,published_at,provenance) VALUES($1,$2,1,'published','Synthetic summary',$3,$3,$4,now(),$4,$4,now(),'{}') RETURNING id::text`, f.run, f.session, strings.Repeat("d", 64), f.admin.UserID)
	f.job = id(`INSERT INTO teaching.analysis_jobs(run_id,stage,status,max_attempts,progress) VALUES($1,'report','succeeded',1,100) RETURNING id::text`, f.run)
	return f
}
