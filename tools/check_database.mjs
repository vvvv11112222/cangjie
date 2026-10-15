// Native PostgreSQL multi-connection acceptance. No media/model/network calls.
// Usage: node tools/check_database.mjs <installed pg package directory>
import fs from 'node:fs/promises';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import assert from 'node:assert/strict';
import { checkUpgrade } from './upgrade_cases.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
if (!process.argv[2]) throw new Error('Pass an existing pg package directory; no installation is performed.');
const require = createRequire(path.resolve(process.argv[2], 'package.json'));
const { Pool } = require(path.resolve(process.argv[2]));
const url = process.env.DATABASE_ACCEPTANCE_URL;
if (!url || new URL(url).pathname !== '/cangjie_database_acceptance' || process.env.DATABASE_ACCEPTANCE_ALLOW_RESET !== '1') {
  throw new Error('Requires disposable cangjie_database_acceptance and DATABASE_ACCEPTANCE_ALLOW_RESET=1');
}
const pool = new Pool({ connectionString: url, max: 6, statement_timeout: 10000 });
const scalar = async (sql, values=[]) => Object.values((await pool.query(sql,values)).rows[0])[0];
const id = async (sql, values=[]) => scalar(sql+' RETURNING id::text', values);
const result = { checks: [], boundary: 'native PostgreSQL SQL transactions; HTTP integration is a separate Go suite' };
async function check(name, fn) { await fn(); result.checks.push(name); process.stdout.write(`PASS ${name}\n`); }
async function waitForLock(pid) {
  const end = Date.now()+3000;
  while(Date.now()<end) {
    if(await scalar("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')",[pid])) return;
    await new Promise(resolve=>setTimeout(resolve,10));
  }
  throw new Error('Competing transaction did not wait on the expected database lock');
}
async function begin(client) { await client.query('BEGIN'); await client.query("SET LOCAL lock_timeout='8s'; SET LOCAL statement_timeout='10s'"); }
const lock = await pool.connect();
try {
  await lock.query("SELECT pg_advisory_lock(hashtext('database_acceptance'))");
  assert.equal(await scalar("SELECT current_setting('server_version_num')::int/10000"),17);
  result.postgres = await scalar('SELECT version()');
  // Reuse the historical-data cases against a native server, not WASM.
  class NativeUpgradeDatabase {
    constructor() {
      this.ready = pool.connect().then(async client=>{
        await client.query('DROP SCHEMA IF EXISTS teaching CASCADE; DROP TABLE IF EXISTS public.schema_migrations');
        return client;
      });
    }
    async exec(sql) { return (await this.ready).query(sql); }
    async query(sql) { return (await this.ready).query(sql); }
    async close() {
      const client=await this.ready;
      try {await client.query('ROLLBACK');} finally {client.release();}
    }
  }
  await check('native legacy 001 to 007 upgrade preserves historical facts',async()=>{
    await checkUpgrade(NativeUpgradeDatabase,null,root);
    for(const name of (await fs.readdir(path.join(root,'database'))).filter(n=>/^00[4-7]_.+\.sql$/.test(n)).sort()) {
      await pool.query(await fs.readFile(path.join(root,'database',name),'utf8'));
    }
    assert.equal(await scalar("SELECT count(*)::int FROM teaching.source_records WHERE rights_status='pending'"),2);
    assert.equal(await scalar("SELECT count(*)::int FROM teaching.reports WHERE status='published' AND reviewed_by IS NULL"),1);
  });
  await pool.query('DROP SCHEMA IF EXISTS teaching CASCADE; DROP TABLE IF EXISTS public.schema_migrations');
  const migrations = (await fs.readdir(path.join(root,'database'))).filter(n=>/^\d{3}_.+\.sql$/.test(n)).sort();
  await pool.query('CREATE TABLE public.schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())');
  for(const name of migrations) {
    const body = await fs.readFile(path.join(root,'database',name),'utf8');
    await pool.query(body);
    await pool.query('INSERT INTO public.schema_migrations(name,checksum) VALUES($1,$2)',[name,createHash('sha256').update(body).digest('hex')]);
  }
  await check('fresh migrations and existing SQL constraints', async()=>{
    assert.equal(await scalar("SELECT count(*)::int FROM information_schema.tables WHERE table_schema='teaching'"),37);
    await pool.query(await fs.readFile(path.join(root,'database/check_schema.sql'),'utf8'));
    assert.equal(await scalar("SELECT count(*)::int FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname='teaching' AND NOT convalidated"),0);
  });
  const college = await id("INSERT INTO teaching.org_units(code,name,kind) VALUES('DB-COL','Synthetic college','college')");
  const term = await id("INSERT INTO teaching.academic_terms(code,name,start_date,end_date) VALUES('DB-TERM','Synthetic term','2026-09-01','2027-01-01')");
  const course = await id("INSERT INTO teaching.courses(org_unit_id,code,name) VALUES($1,'DB-COURSE','Synthetic course')",[college]);
  async function academic(code, reuse={}) {
    const teacher = reuse.teacher ?? await id("INSERT INTO teaching.user_accounts(org_unit_id,username,password_hash,display_name) VALUES($1,$2,'synthetic-unused','Synthetic teacher')",[college,code]);
    await pool.query("INSERT INTO teaching.role_bindings(user_id,role_code) VALUES($1,'teacher') ON CONFLICT DO NOTHING",[teacher]);
    const group = reuse.group ?? await id("INSERT INTO teaching.class_groups(org_unit_id,code,name,enrollment_year) VALUES($1,$2,'Synthetic class',2026)",[college,code]);
    const room = reuse.room ?? await id("INSERT INTO teaching.classrooms(code,name) VALUES($1,'Synthetic room')",[code]);
    const offering = await id("INSERT INTO teaching.course_offerings(org_unit_id,code,term_id,course_id,teacher_id,class_group_id) VALUES($1,$2,$3,$4,$5,$6)",[college,code,term,course,teacher,group]);
    return {teacher,group,room,offering};
  }
  for(const resource of ['teacher','group','room']) await check(`FR-03 concurrent ${resource} exclusion and adjacent slot`, async()=>{
    const a=await academic(`DB-${resource}-A`), b=await academic(`DB-${resource}-B`,{[resource]:a[resource]});
    const first=await pool.connect(), second=await pool.connect();
    try {
      await begin(first); await begin(second);
      const sql="INSERT INTO teaching.schedule_entries(offering_id,teacher_id,class_group_id,classroom_id,starts_at,ends_at) VALUES($1,$2,$3,$4,'2030-01-01 09:00+08','2030-01-01 10:00+08')";
      await first.query(sql,[a.offering,a.teacher,a.group,a.room]);
      const pid=(await second.query('SELECT pg_backend_pid() AS pid')).rows[0].pid;
      const competing=second.query(sql,[b.offering,b.teacher,b.group,b.room]).then(()=>null,e=>e);
      await waitForLock(pid); await first.query('COMMIT');
      assert.equal((await competing)?.code,'23P01'); await second.query('ROLLBACK');
      await pool.query(sql.replace("09:00+08","10:00+08").replace("10:00+08')","11:00+08')"),[b.offering,b.teacher,b.group,b.room]);
    } finally { await first.query('ROLLBACK');await second.query('ROLLBACK');first.release();second.release(); }
  });
  const a=await academic('DB-FLOW');
  const session=await id("INSERT INTO teaching.lesson_sessions(offering_id,title,planned_start_at,planned_end_at,created_by) VALUES($1,'Synthetic database acceptance',now(),now()+interval '1 hour',$2)",[a.offering,a.teacher]);
  const source=await id("INSERT INTO teaching.source_records(session_id,source_type,title,attribution,registered_by) VALUES($1,'self_recorded','Synthetic source','Synthetic only',$2)",[session,a.teacher]);
  const media=await id("INSERT INTO teaching.media_assets(session_id,source_record_id,kind,storage_backend,object_key,sha256,mime_type,byte_size) VALUES($1,$2,'source','filesystem','synthetic/db.mp4',repeat('a',64),'video/mp4',1)",[session,source]);
  const run=await id("INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,pipeline_version,config_snapshot,status) VALUES($1,$2,$3,'db-run',repeat('a',64),repeat('b',64),'p0-v1','{}','succeeded')",[session,media,a.teacher]);
  const report=async revision=>id("INSERT INTO teaching.reports(run_id,session_id,revision,status,created_by,content_sha256,reviewed_content_sha256,reviewed_by,reviewed_at) VALUES($1,$2,$3,'in_review',$4,repeat('c',64),repeat('c',64),$4,now())",[run,session,revision,a.teacher]);
  const r1=await report(1),r2=await report(2);
  await check('FR-09 competing publication serializes and stale expectation fails', async()=>{
    const first=await pool.connect(),second=await pool.connect();
    try {
      await begin(first);await begin(second);
      await first.query('SELECT id FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE',[session]);
      const pid=(await second.query('SELECT pg_backend_pid() AS pid')).rows[0].pid;
      const waiting=second.query('SELECT id FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE',[session]);
      await waitForLock(pid);
      await first.query("UPDATE teaching.reports SET status='published',published_by=$2,published_at=now() WHERE id=$1",[r1,a.teacher]);
      await first.query('COMMIT');await waiting;
      const current=(await second.query("SELECT id::text FROM teaching.reports WHERE session_id=$1 AND status='published'",[session])).rows[0].id;
      assert.equal(current,r1); // Expected null is stale: Go must return REVISION_CONFLICT.
      await second.query('ROLLBACK');
      assert.equal(await scalar('SELECT status FROM teaching.reports WHERE id=$1',[r2]),'in_review');
      await assert.rejects(pool.query("UPDATE teaching.reports SET status='published',published_by=$2,published_at=now() WHERE id=$1",[r2,a.teacher]),e=>e.code==='23505');
    } finally {await first.query('ROLLBACK');await second.query('ROLLBACK');first.release();second.release();}
  });
  await check('FR-11 concurrent budget reservation and unknown retention',async()=>{
    const first=await pool.connect(),second=await pool.connect();
    try {
      await begin(first);await begin(second);
      const budgetLock="SELECT pg_advisory_xact_lock(hashtextextended('CNY:2026-10-01',0))";
      await first.query(budgetLock);
      const pid=(await second.query('SELECT pg_backend_pid() AS pid')).rows[0].pid;
      const waiting=second.query(budgetLock);await waitForLock(pid);
      await first.query("INSERT INTO teaching.model_calls(call_key,provider,model_name,prompt_version,status,reserved_cost,billing_period) VALUES('db-budget','synthetic','synthetic','p0-v1','reserved',0.6,'2026-10-01')");
      await first.query('COMMIT');await waiting;
      const used=(await second.query("SELECT sum(CASE WHEN status IN('reserved','unknown') THEN reserved_cost ELSE actual_cost END)::text AS used FROM teaching.model_calls WHERE currency='CNY' AND billing_period='2026-10-01'")).rows[0].used;
      assert.equal(used,'0.600000');assert.ok(Number(used)>1-0.6);await second.query('ROLLBACK');
      await pool.query("UPDATE teaching.model_calls SET status='unknown' WHERE call_key='db-budget'");
      assert.equal(await scalar("SELECT reserved_cost::text FROM teaching.model_calls WHERE call_key='db-budget' AND status='unknown'"),'0.600000');
    } finally {await first.query('ROLLBACK');await second.query('ROLLBACK');first.release();second.release();}
  });
  await check('FR-06 claim exclusion and stale lease completion rejection',async()=>{
    const job=await id("INSERT INTO teaching.analysis_jobs(run_id,stage,max_attempts) VALUES($1,'probe',3)",[run]);
    const claim="WITH candidate AS (SELECT id FROM teaching.analysis_jobs WHERE id=$1 AND status='queued' FOR UPDATE SKIP LOCKED) UPDATE teaching.analysis_jobs j SET status='running',worker_id=$2,lease_token=gen_random_uuid(),lease_expires_at=now()+interval '1 minute',execution_deadline_at=now()+interval '2 minutes',expected_execution_sha256=repeat('d',64),attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.lease_token::text";
    const first=await pool.connect(),second=await pool.connect();
    try {
      await begin(first);await begin(second);
      const active=(await first.query(claim,[job,'worker-a'])).rows[0].lease_token;
      assert.equal((await second.query(claim,[job,'worker-b'])).rowCount,0);
      await first.query('COMMIT');await second.query('COMMIT');
      await pool.query("UPDATE teaching.analysis_jobs SET status='queued',worker_id=NULL,lease_token=NULL,lease_expires_at=NULL WHERE id=$1",[job]);
      const newer=(await pool.query(claim,[job,'worker-b'])).rows[0].lease_token;
      assert.notEqual(active,newer);
      assert.equal((await pool.query("UPDATE teaching.analysis_jobs SET status='succeeded',worker_id=NULL,lease_token=NULL,lease_expires_at=NULL WHERE id=$1 AND status='running' AND lease_token=$2::uuid",[job,active])).rowCount,0);
    } finally {await first.query('ROLLBACK');await second.query('ROLLBACK');first.release();second.release();}
  });
  await check('FR-11 deletion rollback and retained linked accounting',async()=>{
    const job=await id("INSERT INTO teaching.analysis_jobs(run_id,stage) VALUES($1,'report')",[run]);
    await pool.query("INSERT INTO teaching.model_calls(run_id,job_id,call_key,provider,model_name,prompt_version,status,reserved_cost,billing_period) VALUES($1,$2,'db-linked','synthetic','synthetic','p0-v1','unknown',0.25,'2026-10-01')",[run,job]);
    const revision=await id("INSERT INTO teaching.transcript_revisions(session_id,media_asset_id,source_run_id,revision_no,source_type,content_sha256,created_by,reason) VALUES($1,$2,$3,1,'asr',repeat('f',64),$4,'synthetic fixture')",[session,media,run,a.teacher]);
    await pool.query("INSERT INTO teaching.transcript_revision_segments(revision_id,segment_no,start_ms,end_ms,text_content) VALUES($1,0,0,1000,'synthetic')",[revision]);
    await pool.query("INSERT INTO teaching.analysis_runs(session_id,media_asset_id,requested_by,idempotency_key,input_sha256,config_hash,pipeline_version,config_snapshot,status,mode,input_transcript_revision_id) VALUES($1,$2,$3,'db-revision-run',repeat('a',64),repeat('e',64),'p0-v1','{}','succeeded','report_only',$4)",[session,media,a.teacher,revision]);
    const client=await pool.connect();
    const remove=async()=>{
      await client.query('SET CONSTRAINTS ALL DEFERRED');
      await client.query('SELECT id FROM teaching.lesson_sessions WHERE id=$1 FOR UPDATE',[session]);
      await client.query('UPDATE teaching.model_calls SET run_id=NULL,job_id=NULL WHERE run_id IN(SELECT id FROM teaching.analysis_runs WHERE session_id=$1)',[session]);
      for(const table of ['reports','evidence_items','transcript_segments','job_artifacts']) await client.query(`DELETE FROM teaching.${table} WHERE session_id=$1`,[session]);
      await client.query('DELETE FROM teaching.analysis_jobs WHERE run_id IN(SELECT id FROM teaching.analysis_runs WHERE session_id=$1)',[session]);
      for(const table of ['transcript_revisions','analysis_runs','media_assets','source_records']) await client.query(`DELETE FROM teaching.${table} WHERE session_id=$1`,[session]);
      await client.query("UPDATE teaching.lesson_sessions SET status='deleted',title='[deleted]' WHERE id=$1",[session]);
    };
    try {
      await begin(client);await remove();await client.query('ROLLBACK');
      assert.equal(await scalar('SELECT count(*)::int FROM teaching.reports WHERE session_id=$1',[session]),2);
      assert.equal(await scalar("SELECT run_id IS NOT NULL FROM teaching.model_calls WHERE call_key='db-linked'"),true);
      await begin(client);await remove();await client.query('COMMIT');
      assert.equal(await scalar('SELECT count(*)::int FROM teaching.analysis_runs WHERE session_id=$1',[session]),0);
      assert.equal(await scalar("SELECT run_id IS NULL AND job_id IS NULL AND status='unknown' AND reserved_cost=0.25 FROM teaching.model_calls WHERE call_key='db-linked'"),true);
    } finally {await client.query('ROLLBACK');client.release();}
  });
  await check('FR-11 application role can use business data but cannot perform DDL or modify migration history',async()=>{
    const admin=await pool.connect();
    try {
      await admin.query("DO $$ BEGIN CREATE ROLE cangjie_db_acceptance_migrator NOLOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END $$; DO $$ BEGIN CREATE ROLE cangjie_db_acceptance_app NOLOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END $$");
      await admin.query("SELECT set_config('cangjie.migration_role','cangjie_db_acceptance_migrator',false),set_config('cangjie.app_role','cangjie_db_acceptance_app',false)");
      await admin.query(await fs.readFile(path.join(root,'database/configure_roles.sql'),'utf8'));
      await admin.query('SET ROLE cangjie_db_acceptance_app');
      assert.equal((await admin.query('SELECT count(*)::int AS n FROM public.schema_migrations')).rows[0].n,migrations.length);
      await admin.query("INSERT INTO teaching.org_units(code,name,kind) VALUES('DB-ROLE','Synthetic app write','college')");
      // audit_logs has an identity sequence; usage must remain available.
      await admin.query("INSERT INTO teaching.audit_logs(action,resource_type,resource_id,result,request_id) VALUES('db.test','org_unit',$1,'success','synthetic-db-test')",[college]);
      for(const sql of ['CREATE TABLE teaching.forbidden(id int)','CREATE TABLE public.forbidden(id int)','DROP TABLE teaching.org_units','DELETE FROM public.schema_migrations']) {
        await assert.rejects(admin.query(sql),e=>e.code==='42501');
      }
      await admin.query('RESET ROLE');
    } finally {await admin.query('RESET ROLE');admin.release();}
  });
  const out=path.join(root,'var/database-acceptance');await fs.mkdir(out,{recursive:true});
  await fs.writeFile(path.join(out,'sql-results.json'),JSON.stringify(result,null,2)+'\n');
} finally { await lock.query("SELECT pg_advisory_unlock(hashtext('database_acceptance'))");lock.release();await pool.end(); }
