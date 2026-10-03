// Real migration checks against an isolated legacy database, not application mocks.
import fs from 'node:fs/promises';
import path from 'node:path';

export async function checkUpgrade(PGlite, btree_gist, root) {
  const db = new PGlite({ extensions: { btree_gist } });
  const sql = name => fs.readFile(path.join(root, 'database', name), 'utf8');
  const require = (ok, message) => { if (!ok) throw new Error(message); };
  try {
    await db.exec(await sql('001_initial_schema.sql'));
    await db.exec(`SET search_path TO teaching,public;
      DO $$ DECLARE org uuid; usr uuid; term uuid; course uuid; grp uuid; offering uuid;
        sa uuid; sb uuid; src uuid; ma uuid; ra uuid; rpt uuid;
      BEGIN
        INSERT INTO org_units(code,name,kind) VALUES('legacy','Synthetic college','college') RETURNING id INTO org;
        INSERT INTO user_accounts(org_unit_id,username,password_hash,display_name)
          VALUES(org,'legacy','synthetic-hash','Synthetic teacher') RETURNING id INTO usr;
        INSERT INTO role_bindings(user_id,role_code) VALUES(usr,'teacher');
        INSERT INTO academic_terms(code,name,start_date,end_date)
          VALUES('legacy','Synthetic term','2026-09-01','2027-01-01') RETURNING id INTO term;
        INSERT INTO courses(org_unit_id,code,name) VALUES(org,'legacy','Synthetic course') RETURNING id INTO course;
        INSERT INTO class_groups(org_unit_id,code,name) VALUES(org,'legacy','Synthetic class') RETURNING id INTO grp;
        INSERT INTO course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id)
          VALUES(org,'legacy',term,course,usr,grp) RETURNING id INTO offering;
        INSERT INTO lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by)
          VALUES(offering,'Synthetic A','2026-09-01 09:00+08','2026-09-01 10:00+08',usr) RETURNING id INTO sa;
        INSERT INTO lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by)
          VALUES(offering,'Synthetic B','2026-09-02 09:00+08','2026-09-02 10:00+08',usr) RETURNING id INTO sb;
        INSERT INTO source_records(source_type,title,attribution,rights_status,allowed_uses,
          external_processing_allowed,registered_by,verified_by,verified_at)
          VALUES('school_authorized','Synthetic shared registration','Synthetic school','verified',
            '["playback","analysis"]',true,usr,usr,now()) RETURNING id INTO src;
        INSERT INTO media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size)
          VALUES(sa,src,'source','filesystem','synthetic/a.mp4',repeat('a',64),'video/mp4',1) RETURNING id INTO ma;
        INSERT INTO media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size)
          VALUES(sb,src,'source','filesystem','synthetic/b.mp4',repeat('b',64),'video/mp4',1);
        INSERT INTO analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,
          config_hash,pipeline_version,config_snapshot,status)
          VALUES(sa,ma,usr,'legacy',repeat('a',64),repeat('b',64),'legacy','{}','succeeded') RETURNING id INTO ra;
        INSERT INTO analysis_jobs(run_id,stage) VALUES(ra,'report');
        INSERT INTO reports(run_id,session_id,revision,created_by,status,published_by,published_at)
          VALUES(ra,sa,1,usr,'published',usr,now()) RETURNING id INTO rpt;
        INSERT INTO report_dimensions(report_id,dimension_code,coverage_status,coverage,limitation)
          VALUES(rpt,'content','insufficient','{"legacy_window":1}','Synthetic legacy limitation');
      END $$;`);
    await db.exec(await sql('002_review_baseline.sql'));
    require((await db.query('SELECT session_id FROM teaching.source_records')).rows[0].session_id === null,
      '002 fixture must reproduce a shared unassigned legacy source');
    const migration = await sql('003_review_fixes.sql');
    async function blocked(expectedMessage) {
      let error;
      try { await db.exec(migration); } catch (caught) { error = caught; }
      await db.exec('ROLLBACK');
      require(error?.message.includes(expectedMessage), `Expected migration preflight: ${expectedMessage}`);
    }
    await blocked('legacy report_dimensions.coverage');
    require((await db.query('SELECT coverage FROM teaching.report_dimensions')).rows[0].coverage.legacy_window === 1,
      'Blocked upgrade changed legacy coverage');
    await db.exec("UPDATE teaching.report_dimensions SET coverage='{}'; UPDATE teaching.analysis_jobs SET attempts=2,max_attempts=2");
    await blocked('attempts > 1');
    await db.exec('UPDATE teaching.analysis_jobs SET attempts=1,max_attempts=2');
    await db.exec(migration);
    const sources = (await db.query('SELECT * FROM teaching.source_records')).rows;
    require(sources.length === 2 && new Set(sources.map(s => s.session_id)).size === 2, 'Shared source was not split');
    require(sources.every(s => s.rights_status === 'pending' && s.allowed_uses.length === 0 && !s.external_processing_allowed
      && s.verified_by === null && s.legacy_verification.rights_status === 'verified'
      && s.legacy_verification.allowed_uses.includes('analysis')), 'Upgrade inherited permission or lost verification history');
    require((await db.query(`SELECT count(*)::int AS n FROM teaching.media_assets m JOIN teaching.source_records s
      ON m.source_record_id=s.id WHERE m.session_id IS DISTINCT FROM s.session_id`)).rows[0].n === 0, 'Media source mismatch');
    require((await db.query('SELECT coverage FROM teaching.report_dimensions')).rows[0].coverage.length === 0, 'Coverage not converted');
    const report = (await db.query('SELECT status,reviewed_by FROM teaching.reports')).rows[0];
    require(report.status === 'published' && report.reviewed_by === null, 'Upgrade fabricated historical attestation');
    require((await db.query('SELECT max_attempts FROM teaching.analysis_jobs')).rows[0].max_attempts === 1, 'Report retry default not repaired');
    require((await db.query("SELECT count(*)::int AS n FROM teaching.audit_logs WHERE action='source.split'")).rows[0].n === 1, 'Split mapping missing');
    process.stdout.write('PASS legacy upgrade: fail-safe preflights, shared-source split, no inherited grants, preserved history\n');
  } finally { await db.close(); }
}
