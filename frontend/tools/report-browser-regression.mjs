// Run against an API-mode build with a local synthetic video and intercepted API.
// Usage: node frontend/tools/report-browser-regression.mjs <playwright/index.mjs> <baseURL> <output-dir>
import assert from 'node:assert/strict';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const [playwrightPath, baseURL = 'http://127.0.0.1:18769', output = 'output/playwright/report-integration'] = process.argv.slice(2);
if (!playwrightPath) throw new Error('Pass the path to an installed playwright/index.mjs');
const { chromium } = await import(pathToFileURL(playwrightPath).href);
const fixture = (name) => JSON.parse(readFileSync(new URL(`../../contracts/examples/${name}.json`, import.meta.url), 'utf8'));
const report = fixture('report-draft');
const run = fixture('run-partial');
const results = fixture('results');
const session = fixture('session-page').items[0];
const mediaID = report.provenance.media_asset_id;
const evidence = { ...results.evidence[0], run_id: report.run_id, session_id: report.session_id, media_asset_id: mediaID, start_ms: 1000, end_ms: 2500, description: '教师比较两个概念后直接给出结论', availability: 'available' };
report.summary_evidence_ids = [evidence.id];
report.allowed_actions = [];
report.dimensions[0].summary_evidence_ids = [evidence.id];
report.observations = [{ ...report.observations[0], evidence_ids: [evidence.id], observation_type: 'issue', observation_text: '教师作出比较后直接给出结论，未解释比较依据。', suggestion: '做法：先请学生指出两个概念的共同点与区别。\n课堂示例：你依据什么判断它们相同或不同？\n观察效果：听学生能否用准确术语解释判断依据。' }];
results.evidence = [evidence]; results.transcript_revision_id = null;
run.report_id = report.id;
session.current_report_id = null; session.latest_run_id = report.run_id;
// The current classroom primary is deliberately different from the report media.
session.primary_media_asset_id = '00000000-0000-4000-8000-000000009999';
const video = readFileSync(new URL('../public/samples/m2-playback-demo.mp4', import.meta.url));
const media = { id: mediaID, session_id: session.id, source_record_id: null, parent_asset_id: null, kind: 'source', status: 'ready', sha256: 'a'.repeat(64), mime_type: 'video/mp4', byte_size: video.length, duration_ms: 6000, expires_at: '2099-01-01T00:00:00Z', playback_asset_id: mediaID, playback_url: `/api/v1/media/${mediaID}/content`, allowed_actions: ['playback'] };
let denyPlayback = false;
const requestedMedia = [];
const errors = [];
const browser = await chromium.launch({ executablePath: process.env.EDGE_PATH, ...(process.env.EDGE_PATH ? {} : { channel: 'msedge' }) });
try {
  const page = await browser.newPage();
  page.setDefaultTimeout(10000);
  page.on('pageerror', (error) => errors.push(error.message));
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '');
    const send = (data) => route.fulfill({ json: { data, request_id: 'synthetic-report-browser' } });
    const fail = (status) => route.fulfill({ status, json: { error: { code: status === 403 ? 'FORBIDDEN' : 'NOT_FOUND', message: 'Synthetic test', details: {} }, request_id: 'synthetic-report-browser' } });
    if (path === '/auth/me') return send({ user_id: '00000000-0000-4000-8000-000000000101', display_name: '合成验收账号', roles: [], allowed_actions: [] });
    if (path === '/auth/csrf') return send({ csrf_token: 'synthetic-csrf' });
    if (path === '/sessions') return send({ items: [session], next_cursor: null });
    if (path === `/sessions/${session.id}/reports`) return send({ items: [report], next_cursor: null });
    if (path === `/analysis-runs/${report.run_id}/results`) return send(results);
    if (path === `/analysis-runs/${report.run_id}`) return send(run);
    if (path === `/reports/${report.id}`) return send(report);
    if (path === `/media/${mediaID}`) { requestedMedia.push(mediaID); return send(media); }
    if (path === `/media/${mediaID}/content`) {
      if (denyPlayback) return fail(403);
      const range = /^bytes=(\d+)-(\d*)$/.exec(route.request().headers().range ?? '');
      const start = Number(range?.[1] ?? 0), end = Math.min(Number(range?.[2] || video.length - 1), video.length - 1);
      return route.fulfill({ status: range ? 206 : 200, headers: { 'Content-Type': 'video/mp4', 'Accept-Ranges': 'bytes', ...(range ? { 'Content-Range': `bytes ${start}-${end}/${video.length}` } : {}) }, body: video.subarray(start, end + 1) });
    }
    return fail(404);
  });
  await page.goto(`${baseURL}/#/reports`);
  await page.getByRole('tabpanel').getByText('课堂示例：', { exact: false }).waitFor();
  assert.equal(await page.getByRole('tabpanel').locator('details .report-prose').count(), 0);
  mkdirSync(output, { recursive: true });
  for (const width of [1280, 1024, 820]) {
    await page.setViewportSize({ width, height: 900 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, `overflow at ${width}`);
    await page.screenshot({ path: join(output, `report-${width}.png`), fullPage: true });
  }
  const tabs = page.getByRole('tab', { name: /教学内容/ });
  await tabs.focus(); await page.keyboard.press('End');
  assert.equal(await page.getByRole('tab', { selected: true }).innerText().then(text => text.includes('教学技术')), true);
  await page.keyboard.press('Home');
  await page.getByRole('button', { name: /查看依据/ }).first().click();
  const player = page.locator('.report-evidence video');
  await page.waitForFunction(() => { const v = document.querySelector('.report-evidence video'); return v && v.readyState >= 1 && Math.abs(v.currentTime - 1) < 0.2; });
  assert.equal(await player.getAttribute('src').then(src => src.includes(mediaID)), true);
  assert.ok(requestedMedia.length > 0);
  assert.ok(requestedMedia.every(id => id !== session.primary_media_asset_id));
  await page.screenshot({ path: join(output, 'evidence-playback.png'), fullPage: true });
  await page.getByRole('button', { name: '关闭依据', exact: true }).click();
  assert.equal(await page.locator('.report-evidence video').count(), 0);
  denyPlayback = true;
  await page.getByRole('button', { name: /查看依据/ }).first().click();
  await page.getByRole('alert').filter({ hasText: /FORBIDDEN|403/ }).waitFor();
  assert.equal(await page.locator('.report-evidence video').count(), 0);
  assert.deepEqual(errors, []);
  const result = { synthetic: true, passed: true, widths: [1280, 1024, 820], checks: ['visible analysis and actionable advice', 'dimension keyboard navigation', 'no horizontal overflow', 'original media rather than current primary', 'original millisecond seek in real video element', 'close unmounts player', 'revoked playback blocks player'], pageErrors: errors };
  writeFileSync(join(output, 'result.json'), JSON.stringify(result, null, 2));
  console.log(JSON.stringify(result, null, 2));
} finally { await browser.close(); }
