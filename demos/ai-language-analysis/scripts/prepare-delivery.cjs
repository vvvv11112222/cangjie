// Builds a reviewable source-only snapshot. Never reads .env, data or evaluation.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

const rootFiles = [
  '.gitignore', '.gitattributes', '.dockerignore', '.env.example',
  'go.mod', 'go.sum', 'Dockerfile', 'compose.yaml',
  'prompt.txt', 'reading_prompt.txt', 'events_prompt.txt', 'audit_prompt.txt',
  'rubric.md', 'README.md', 'HANDOFF.md', 'TEST-REPORT.md', 'COMPETITION.md',
  'EVALUATION.md', 'SIX-DIMENSIONS.md', 'SEGMENTATION.md', 'PHASES-1-2.md', 'PHASES-3-5.md',
];
const sourceGroups = {
  web: ['index.html', 'analysis.js', 'events.js', 'jobs.js', 'recovery.js', 'transcript.js', 'report.css'],
  scripts: ['start.ps1', 'start.sh', 'test.ps1', 'test.sh', 'test-audit-calibration.ps1',
    'test-public-courses.ps1', 'prepare-delivery.cjs', 'test-integration.ps1', 'test-integration.sh',
    'deploy-smoke.cjs', 'docker-smoke.py'],
  tests: ['analysis.test.cjs', 'recovery.test.cjs', 'transcript.test.cjs', 'delivery.test.cjs',
    'analysis.browser.cjs', 'recovery.browser.cjs', 'jobs.browser.cjs'],
  'testdata/public-courses': ['README.md', 'AI辅助验收记录.json',
    'results/retest-guided-review-v2-job-ead802c499b539da-v2.json',
    'results/retest-guided-review-v2-job-7b33d23c8ce0e4f1-v2.json',
    'sources/mit-physics.vtt', 'sources/mit-biology.vtt',
    'replays/physics-review.json', 'replays/physics-events.json'],
  integration: ['README.md', 'quality.py', 'requirements.txt', 'test_quality.py', 'test_contract.py'],
  'integration/reportadapter': ['adapter.go', 'adapter_test.go'],
  'integration/testdata': ['adapter-input.json', 'quality-draft.json'],
  'integration/upstream': ['provenance.json', 'contracts/v1.schema.json', 'contracts/quality-gates.json',
    'tools/check_quality.py', 'tools/test_quality.py'],
  'cmd/adapt-report': ['main.go'],
};
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');

function checkedFile(root, name) {
  if (typeof name !== 'string' || name.includes('\\') || path.posix.isAbsolute(name) ||
      name.split('/').some(part => !part || part === '.' || part === '..' || part.includes(':'))) {
    throw new Error('Unsafe delivery path: ' + name);
  }
  let current = root;
  for (const part of name.split('/')) {
    current = path.join(current, part);
    if (fs.lstatSync(current).isSymbolicLink()) throw new Error('Linked delivery path: ' + name);
  }
  if (!fs.statSync(current).isFile()) throw new Error('Not a source file: ' + name);
  return current;
}

function inspectFiles(root, names) {
  if (new Set(names).size !== names.length) throw new Error('Duplicate delivery paths');
  return [...names].sort().map(name => {
    // Also protect explicit callers: never accept configuration variants or local stores.
    if (name !== '.env.example' && /(?:^|\/)(?:\.env(?:[./]|$)|本地凭据\.txt$|data\/|bin\/|evaluation\/|node_modules\/)/i.test(name)) {
      throw new Error('Local-only delivery path: ' + name);
    }
    const bytes = fs.readFileSync(checkedFile(root, name));
    const text = bytes.toString('utf8');
    const patterns = [
      /sk-[A-Za-z0-9_-]{24,}/,
      /gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}/,
      /-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/,
      /\b(?:AKIA|ASIA)[A-Z0-9]{16}\b/,
    ];
    if (patterns.some(pattern => pattern.test(text))) throw new Error('Possible credential in ' + name + '; content omitted');
    if (name === '.env.example' && !/^DASHSCOPE_API_KEY=[ \t]*\r?$/m.test(text)) {
      throw new Error('Example API key must be empty');
    }
    return { path: name, bytes: bytes.length, sha256: hash(bytes) };
  });
}

function collectFiles(root) {
  const names = [...rootFiles, ...fs.readdirSync(root).filter(name => name.endsWith('.go'))];
  for (const [dir, files] of Object.entries(sourceGroups)) {
    names.push(...files.map(file => dir + '/' + file));
  }
  return inspectFiles(root, names);
}

function verifyPublicFixtures(root) {
  const fixtureRoot = 'testdata/public-courses/';
  const read = name => fs.readFileSync(checkedFile(root, fixtureRoot + name));
  const record = JSON.parse(read('AI辅助验收记录.json').toString('utf8'));
  if (record.human_verified !== false || record.sources.length !== 4 || record.evidence.length !== 15) {
    throw new Error('Unexpected public fixture provenance');
  }
  for (const source of record.sources) {
    if (hash(read(source.path)) !== source.sha256) throw new Error('Public fixture changed: ' + source.key);
  }
  const replays = {
    'replays/physics-review.json': '3089282b74cfb36c200eea5990858f3cc5fca02a661338e6d2be25fc42db4b8a',
    'replays/physics-events.json': '5899415bbfbefe75fe4779e355652d50f30a2ecf7086e171c0f980cbb73f3875',
  };
  for (const [name, expected] of Object.entries(replays)) {
    if (hash(read(name)) !== expected) throw new Error('Public replay changed: ' + name);
  }
  const provenance = JSON.parse(fs.readFileSync(checkedFile(root, 'integration/upstream/provenance.json')));
  for (const item of provenance.files) {
    const bytes = fs.readFileSync(checkedFile(root, 'integration/upstream/' + item.path));
    const actual = crypto.createHash('sha1').update(Buffer.from('blob ' + bytes.length + '\0')).update(bytes).digest('hex');
    if (actual !== item.blob_sha) throw new Error('Pinned upstream resource changed: ' + item.path);
  }
}

function exportSource(root, entries) {
  // A fresh directory per run avoids overwriting an earlier reviewed snapshot.
  let parent = root;
  for (const part of ['bin', 'delivery']) {
    parent = path.join(parent, part);
    fs.mkdirSync(parent, { recursive: true });
    if (fs.lstatSync(parent).isSymbolicLink()) throw new Error('Linked delivery output directory');
  }
  const id = new Date().toISOString().replace(/[:.]/g, '-') + '-' + crypto.randomBytes(3).toString('hex');
  const output = path.join(parent, id);
  fs.mkdirSync(output);
  const source = path.join(output, 'source');
  fs.mkdirSync(source);
  for (const entry of entries) {
    const from = checkedFile(root, entry.path);
    const dest = path.join(source, entry.path);
    fs.mkdirSync(path.dirname(dest), { recursive: true });
    fs.copyFileSync(from, dest, fs.constants.COPYFILE_EXCL);
    if (hash(fs.readFileSync(dest)) !== entry.sha256) throw new Error('Source changed during export: ' + entry.path);
  }
  const manifest = {
    format: 'cangjie-demo-source-delivery-v1', created_at: new Date().toISOString(),
    purpose: 'Independent module for team review; integration and real-classroom acceptance pending.',
    file_count: entries.length, total_bytes: entries.reduce((n, item) => n + item.bytes, 0), files: entries,
  };
  const manifestFile = path.join(output, 'manifest.json');
  fs.writeFileSync(manifestFile, JSON.stringify(manifest, null, 2) + '\n', { flag: 'wx' });
  return { source, manifest: manifestFile, file_count: manifest.file_count, total_bytes: manifest.total_bytes };
}

if (require.main === module) {
  try {
    const args = process.argv.slice(2);
    if (args.length > 1 || (args.length === 1 && !['--check', '--export'].includes(args[0]))) {
      throw new Error('Usage: node scripts/prepare-delivery.cjs [--check|--export]');
    }
    const root = path.resolve(__dirname, '..');
    verifyPublicFixtures(root);
    const files = collectFiles(root);
    const result = args[0] === '--export' ? exportSource(root, files) : {
      mode: 'check', file_count: files.length, total_bytes: files.reduce((n, item) => n + item.bytes, 0), files,
    };
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}

module.exports = { inspectFiles, collectFiles, verifyPublicFixtures, exportSource };
