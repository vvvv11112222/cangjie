// Read-only verification after cmd/seed-dev, using an already installed pg package.
import assert from 'node:assert/strict';
import path from 'node:path';
import { createRequire } from 'node:module';
if (!process.argv[2] || !process.env.DATABASE_URL) throw new Error('Requires pg package directory and DATABASE_URL');
const require = createRequire(path.resolve(process.argv[2], 'package.json'));
const { Client } = require(path.resolve(process.argv[2]));
const client = new Client({ connectionString: process.env.DATABASE_URL });
await client.connect();
try {
  const counts = {};
  for (const [table, prefix, expected] of [
    ['academic_terms','term',1], ['courses','course',2], ['class_groups','group',2],
    ['classrooms','room',2], ['course_offerings','offering',2],
    ['schedule_entries','schedule',6], ['lesson_sessions','session',6], ['source_records','source',6],
  ]) {
    const ids = prefix === 'term' ? ['cangjie-dev-v1:term'] :
      Array.from({length:2},(_,i)=>i+1).flatMap(n=>expected===6 ?
        [0,1,2].map(w=>`cangjie-dev-v1:${prefix}:${n}:${w}`) : [`cangjie-dev-v1:${prefix}:${n}`]);
    const {rows} = await client.query(`SELECT count(*)::int n FROM teaching.${table} WHERE id IN (SELECT md5(x)::uuid FROM unnest($1::text[]) x)`,[ids]);
    assert.equal(rows[0].n,expected,table);
    counts[table]=rows[0].n;
  }
  const {rows} = await client.query(`SELECT s.is_demo,s.status,r.rights_status,r.external_processing_allowed,r.allowed_uses,
    (SELECT count(*)::int FROM teaching.media_assets WHERE session_id=s.id) media_count,
    (SELECT count(*)::int FROM teaching.analysis_runs WHERE session_id=s.id) run_count
    FROM teaching.lesson_sessions s JOIN teaching.source_records r ON r.session_id=s.id
    WHERE s.id IN (SELECT md5('cangjie-dev-v1:session:'||n||':'||w)::uuid FROM generate_series(1,2)n CROSS JOIN generate_series(0,2)w)`);
  assert.equal(rows.length,6);
  assert.equal(rows.filter(r=>r.status==='archived').length,2);
  for(const state of ['pending','verified','rejected']) assert.equal(rows.filter(r=>r.rights_status===state).length,2);
  for(const row of rows) {
    assert.equal(row.is_demo,true); assert.equal(row.external_processing_allowed,false);
    assert.deepEqual(row.allowed_uses,[]); assert.equal(row.media_count,0); assert.equal(row.run_count,0);
  }
  console.log('PASS fictional development fixtures, relationships and authorization boundaries:',JSON.stringify(counts));
} finally { await client.end(); }
