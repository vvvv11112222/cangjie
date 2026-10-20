// No models, credentials or existing job directories. Works with a native binary
// on Windows or Linux; this is process/media smoke, not full project acceptance.
const fs = require('node:fs');
const path = require('node:path');
const net = require('node:net');
const { spawn, spawnSync } = require('node:child_process');
const assert = require('node:assert/strict');

async function smoke(binary) {
  binary = fs.realpathSync(binary);
  const root = path.resolve(__dirname, '..');
  const parent = path.join(root, 'bin', 'verification');
  fs.mkdirSync(parent, { recursive: true });
  const work = fs.mkdtempSync(path.join(parent, 'deploy-smoke-'));
  const server = net.createServer();
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  const env = { ...process.env, DEMO_HOST: '127.0.0.1', DEMO_PORT: String(port),
    DEMO_DATA_DIR: path.join(work, 'data'), DASHSCOPE_API_KEY: '', BAILIAN_BASE_URL: '',
    BAILIAN_REPORT_AUDIT: '0', BAILIAN_LIVE_READING_TEST: '0', BAILIAN_LIVE_AUDIT_TEST: '0',
    BAILIAN_LIVE_PUBLIC_COURSE_TEST: '0' };
  const ffmpeg = env.FFMPEG_PATH || 'ffmpeg', ffprobe = env.FFPROBE_PATH || 'ffprobe';
  const generated = path.join(work, 'synthetic.wav');
  function command(executable, args) {
    const result = spawnSync(executable, args, { env, cwd: work, encoding: 'utf8', timeout: 20000, windowsHide: true });
    if (result.error || result.status !== 0) throw new Error('Media smoke command failed: ' + path.basename(executable));
    return result.stdout;
  }
  command(ffmpeg, ['-nostdin', '-v', 'error', '-f', 'lavfi', '-i', 'sine=frequency=440:duration=1', '-ar', '16000', '-ac', '1', generated]);
  const probe = JSON.parse(command(ffprobe, ['-v', 'error', '-show_format', '-show_streams', '-of', 'json', generated]));
  assert.equal(probe.streams[0].sample_rate, '16000');
  assert.equal(probe.streams[0].channels, 1);
  assert.ok(Math.abs(Number(probe.format.duration) - 1) < 0.01);
  const child = spawn(binary, [], { env, cwd: work, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
  const log = fs.createWriteStream(path.join(work, 'server.log'), { flags: 'wx' });
  child.stdout.pipe(log, { end: false }); child.stderr.pipe(log, { end: false });
  let spawnError;
  child.on('error', error => { spawnError = error; });
  const closed = new Promise(resolve => child.once('close', resolve));
  const base = 'http://127.0.0.1:' + port;
  const get = route => fetch(base + route, { signal: AbortSignal.timeout(2000) });
  try {
    let health;
    const deadline = Date.now() + 15000;
    while (Date.now() < deadline) {
      if (spawnError || child.exitCode !== null) throw new Error('Isolated backend exited; see ' + work);
      try { const response = await get('/api/health'); if (response.ok) { health = await response.json(); break; } } catch {}
      await new Promise(resolve => setTimeout(resolve, 150));
    }
    assert.equal(health?.status, 'ok', 'backend health timeout');
    assert.equal(health.analysis_version, 'six-dim-coaching-v8');
    const index = await get('/'); assert.equal(index.status, 200); assert.match(await index.text(), /<html/i);
    for (const route of ['/report.css', '/analysis.js', '/jobs.js', '/transcript.js', '/recovery.js', '/events.js']) {
      const response = await get(route); assert.equal(response.status, 200, route); assert.ok((await response.text()).length > 100, route);
    }
    const jobs = await get('/api/jobs'); assert.equal(jobs.status, 200);
    const listing = await jobs.json();
    assert.ok(Array.isArray(listing.jobs) && listing.jobs.length === 0, 'isolated store must have no jobs');
    const missing = await get('/does-not-exist'); assert.equal(missing.status, 404);
    const result = { status: 'passed', platform: process.platform, arch: process.arch,
      analysis_version: health.analysis_version, checks: ['isolated-start', 'health', 'embedded-web-assets', 'empty-job-store', '404', 'ffmpeg-generated-audio', 'ffprobe-16khz-mono'],
      real_model_calls: 0, real_classroom_acceptance: false, work };
    fs.writeFileSync(path.join(work, 'result.json'), JSON.stringify(result, null, 2) + '\n', { flag: 'wx' });
    return result;
  } finally {
    if (child.exitCode === null) child.kill('SIGTERM');
    let timer;
    await Promise.race([closed, new Promise(resolve => { timer = setTimeout(resolve, 12000); })]);
    clearTimeout(timer);
    if (child.exitCode === null && child.signalCode === null) { child.kill('SIGKILL'); await closed; }
    log.end();
  }
}

if (require.main === module) {
  if (process.argv.length !== 3) { console.error('Usage: node scripts/deploy-smoke.cjs <native-demo-binary>'); process.exitCode = 2; }
  else smoke(process.argv[2]).then(result => console.log(JSON.stringify(result, null, 2))).catch(error => { console.error(error.message); process.exitCode = 1; });
}

module.exports = { smoke };
