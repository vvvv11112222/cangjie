// Temporary in-memory database only. Usage: node tools/check_schema.mjs <pglite-package-dir>
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const packageDir = process.argv[2];
if (!packageDir) throw new Error('Pass the installed @electric-sql/pglite package directory. No network install is performed.');
const { PGlite } = await import(pathToFileURL(path.resolve(packageDir, 'dist/index.js')).href);
const { btree_gist } = await import(pathToFileURL(path.resolve(packageDir, 'dist/contrib/btree_gist.js')).href);
const db = new PGlite({ extensions: { btree_gist } });
try {
  for (const name of ['001_initial_schema.sql', '002_review_baseline.sql', 'check_schema.sql']) {
    await db.exec(await fs.readFile(path.join(root, 'database', name), 'utf8'));
    process.stdout.write(`PASS ${name}\n`);
  }
  const tables = await db.query("SELECT count(*)::int AS count FROM information_schema.tables WHERE table_schema='teaching'");
  if (tables.rows[0].count !== 29) throw new Error('Expected 29 business tables');
  const rows = await db.query('SELECT count(*)::int AS count FROM teaching.lesson_sessions');
  if (rows.rows[0].count !== 0) throw new Error('Check fixtures were not rolled back');
  const version = await db.query('SELECT version()');
  process.stdout.write(`29 tables; fixtures rolled back; ${version.rows[0].version}\n`);
} finally {
  await db.close();
}
