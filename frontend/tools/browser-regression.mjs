// 使用生产 API 构建和受控响应；命令行参数指定现有 Playwright 包的入口。
// 不依赖 jsdom，不向真实 Go 服务写入账号或业务数据。
import assert from 'node:assert/strict';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
const fixture = name => JSON.parse(readFileSync(new URL(`../../contracts/examples/${name}.json`, import.meta.url), 'utf8'));
const session = fixture('session-page').items[0];
const run = fixture('run-partial');
const results = fixture('results');
const revision = fixture('revision');
const report = fixture('report-draft');
const uuid = n => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
function gate() {
  let release;
  const promise = new Promise(resolve => { release = resolve; });
  return { promise, release };
}

export async function runChecks(page, baseURL = 'http://127.0.0.1:18767') {
  const checks = [];
  const failures = [];
  const calls = [];
  let user = null;
  let scenario = 'list';
  let emptyFirst = false;
  let secondFailure = false;
  let secondGate = null;
  let logoutGate = null;
  let logoutStatus = 204;
  let runError = null;
  let csrf = 0;
  page.on('pageerror', error => failures.push(error.message));
  await page.route('**/api/v1/**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace('/api/v1', '');
    const who = user;
    calls.push({ path, cursor: url.searchParams.get('cursor'), who, token: request.headers()['x-csrf-token'] });
    const send = data => route.fulfill({ json: { data, request_id: 'browser-regression' } });
    const fail = (status, code) => route.fulfill({ status, json: { error: { code, message: 'synthetic', details: {} }, request_id: 'browser-regression' } });
    if (path === '/auth/csrf') return send({ csrf_token: `csrf-${++csrf}` });
    if (path === '/auth/me') return who === null ? fail(401, 'UNAUTHENTICATED') : send({
      user_id: uuid(who === 'A' ? 101 : 102), display_name: `账号${who}`, roles: [], allowed_actions: ['upload', 'analyze'],
    });
    if (path === '/auth/login') {
      user = request.postDataJSON().username;
      return send({ user_id: uuid(user === 'A' ? 101 : 102), roles: [], csrf_token: `login-${++csrf}` });
    }
    if (path === '/auth/logout') {
      const status = logoutStatus;
      if (logoutGate !== null) await logoutGate.promise;
      if (status === 204 || status === 401) user = null;
      return status === 204 ? route.fulfill({ status: 204 }) : fail(status, status === 401 ? 'UNAUTHENTICATED' : status === 403 ? 'FORBIDDEN' : 'SERVICE_UNAVAILABLE');
    }
    if (path === '/sessions') {
      const cursor = url.searchParams.get('cursor');
      if (scenario === 'list') {
        if (cursor !== null && secondGate !== null) await secondGate.promise;
        if (cursor !== null && secondFailure) return fail(503, 'SERVICE_UNAVAILABLE');
        const title = `${who}的第${cursor === null ? '一' : '二'}页课堂`;
        return send({ items: cursor === null && emptyFirst ? [] : [{ ...session, id: uuid((who === 'A' ? 200 : 300) + (cursor === null ? 1 : 2)), title, latest_run_id: null, current_report_id: null }], next_cursor: cursor === null ? 'second' : null });
      }
      if (scenario === 'pages') {
        const index = Number(cursor ?? 0);
        return send({ items: index === 10 ? [{ ...session, title: '第11页课堂' }] : [], next_cursor: index === 10 ? null : String(index + 1) });
      }
      if (scenario === 'loop') return send({ items: [], next_cursor: 'loop' });
      if (scenario === 'drafts') {
        const items = Array.from({ length: 21 }, (_, index) => ({ ...session, id: uuid(500 + index), title: `草稿课堂${index + 1}`, current_report_id: null, latest_run_id: uuid(600 + index) }));
        return send({ items: cursor === null ? items.slice(0, 15) : items.slice(15), next_cursor: cursor === null ? 'draft-page-2' : null });
      }
      return send({ items: [{ ...session, current_report_id: null }], next_cursor: null });
    }
    if (path.endsWith('/results')) return send({ ...results, transcript_revision_id: null });
    if (path.startsWith('/analysis-runs/')) {
      if (runError === 'network') return route.abort('failed');
      if (runError !== null) return fail(runError, ({ 401: 'UNAUTHENTICATED', 403: 'FORBIDDEN', 503: 'SERVICE_UNAVAILABLE' })[runError]);
      const id = path.split('/').at(-1);
      return send({ ...run, id, report_id: scenario === 'drafts' && id !== uuid(620) ? null : report.id });
    }
    if (path.startsWith('/reports/')) return send({ ...report, session_id: scenario === 'drafts' ? uuid(520) : session.id, run_id: scenario === 'drafts' ? uuid(620) : run.id });
    if (path.startsWith('/transcript-revisions/')) return send(revision);
    return fail(404, 'NOT_FOUND');
  });

  const button = label => page.getByRole('button', { name: label, exact: true });
  const text = async () => page.locator('body').innerText();
  const waitText = value => page.getByText(value, { exact: false }).first().waitFor({ state: 'visible' });
  const waitCalls = async predicate => {
    for (let i = 0; i < 100; i++) {
      if (predicate()) return;
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    assert.fail('Expected request was not issued');
  };
  const login = async name => {
    await button('登录').click();
    await page.getByLabel('用户名').fill(name);
    await page.getByLabel('密码', { exact: true }).fill('synthetic-password');
    await button('登录').click();
    await waitText(`账号${name}`);
  };
  const anonymous = async () => {
    await waitText('当前未登录');
    assert.equal(await page.locator('.account-title').count(), 0);
    assert.equal(await page.locator('tbody tr').count(), 0);
  };
  await page.goto(baseURL);
  await anonymous();
  assert.equal(calls.filter(call => call.path === '/sessions').length, 0);
  await login('A');
  await waitText('A的第一页课堂');
  checks.push('anonymous-to-login reload');

  // 同一事件循环内两次 DOM click，绕过“只看到按钮禁用”的浅层验证。
  for (const status of [500, 204]) {
    logoutStatus = status;
    logoutGate = gate();
    const before = calls.filter(call => call.path === '/auth/logout').length;
    await button('退出').evaluate(el => { el.click(); el.click(); });
    await waitCalls(() => calls.filter(call => call.path === '/auth/logout').length === before + 1);
    assert.equal(await button('正在退出…').isDisabled(), true);
    assert.equal(calls.filter(call => call.path === '/auth/logout').length, before + 1);
    logoutGate.release();
    logoutGate = null;
    if (status === 500) {
      await waitText('服务暂时不可用');
      await waitText('账号A');
    } else await anonymous();
    assert.equal(calls.filter(call => call.path === '/auth/logout').length, before + 1);
  }
  checks.push('double logout serialized for failure and success');

  await login('A');
  await waitText('A的第一页课堂');
  logoutStatus = 403;
  await button('退出').click();
  await waitText('没有执行该操作的权限');
  const badToken = calls.filter(call => call.path === '/auth/logout').at(-1).token;
  logoutStatus = 204;
  const csrfBefore = csrf;
  await button('退出').click();
  await anonymous();
  assert.notEqual(calls.filter(call => call.path === '/auth/logout').at(-1).token, badToken);
  assert.equal(csrf, csrfBefore + 1); // 只在重试前获取，退出成功后没有预取。
  checks.push('403 logout retry refreshes CSRF and clears UI immediately');

  await login('A');
  await waitText('A的第一页课堂');
  logoutStatus = 401;
  const beforeExpiredLogout = csrf;
  await button('退出').click();
  await anonymous();
  assert.equal(csrf, beforeExpiredLogout);
  logoutStatus = 204;
  checks.push('expired session logout clears UI without CSRF prefetch');

  await login('A');
  await waitText('A的第一页课堂');
  secondFailure = true;
  await button('加载更多').click();
  await waitText('服务暂时不可用');
  await waitText('A的第一页课堂');
  secondFailure = false;
  await button('重试').click();
  await waitText('A的第二页课堂');
  assert.equal(await page.locator('tbody tr').count(), 2);
  checks.push('list next page failure retains rows and retries');

  await page.reload();
  await waitText('A的第一页课堂');
  secondGate = gate();
  const secondBefore = calls.filter(call => call.path === '/sessions' && call.cursor === 'second').length;
  await button('加载更多').click();
  await waitCalls(() => calls.filter(call => call.path === '/sessions' && call.cursor === 'second').length > secondBefore);
  await button('退出').click();
  await anonymous();
  await login('B');
  await waitText('B的第一页课堂');
  const delayed = page.waitForResponse(response => response.url().includes('cursor=second'));
  secondGate.release();
  await delayed;
  secondGate = null;
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  assert.equal((await text()).includes('A的第二页课堂'), false);
  await waitText('账号B');
  checks.push('late account A pagination cannot enter account B DOM');

  emptyFirst = true;
  await page.reload();
  await waitText('本页没有课堂');
  assert.equal((await text()).includes('当前账号没有可见的课堂'), false);
  await button('加载更多').click();
  await waitText('B的第二页课堂');
  emptyFirst = false;
  checks.push('empty first page can load next page');

  scenario = 'drafts';
  await page.goto(`${baseURL}/#/reports`);
  await waitText('本轮尚未找到结果');
  const checked = calls.filter(call => call.path.startsWith('/analysis-runs/') && !call.path.endsWith('/results')).length;
  await button('继续查找').click();
  await waitText('草稿课堂21');
  assert.equal(calls.filter(call => call.path.startsWith('/analysis-runs/') && !call.path.endsWith('/results')).length, checked + 1);
  checks.push('21st draft reachable via continuation');

  for (const path of ['transcript', 'reports']) {
    scenario = 'pages';
    await page.goto(`${baseURL}/#/${path}`);
    await page.reload();
    await waitText('本轮尚未找到结果');
    const before = calls.filter(call => call.path === '/sessions').length;
    await button('继续查找').click();
    await waitText('第11页课堂');
    assert.equal(calls.filter(call => call.path === '/sessions').length, before + 1);
    checks.push(`11th page ${path} reachable without rescanning`);
  }

  scenario = 'loop';
  await page.reload();
  await waitText('重复的分页游标');
  assert.equal(await button('继续查找').count(), 0);
  checks.push('cursor loop fails explicitly');

  scenario = 'errors';
  const messages = { 401: '登录状态已失效', 403: '没有执行该操作的权限', 503: '服务暂时不可用', network: '连接不上服务端' };
  for (const error of [401, 403, 503, 'network']) {
    runError = error;
    await page.reload();
    await waitText(messages[error]);
    assert.equal((await text()).includes('没有报告，或报告还没有生成'), false);
    runError = null;
    await button('重试').click();
    await waitText('最新报告草稿');
  }
  checks.push('401/403/503/network draft errors remain distinct and retryable');

  scenario = 'list';
  for (const width of [320, 375, 900, 1280]) {
    await page.setViewportSize({ width, height: 812 });
    for (const index of [0, 1, 2, 0]) {
      await page.getByRole('navigation').getByRole('link').nth(index).click();
      await page.locator('main h1').waitFor({ state: 'visible' });
    }
  }
  checks.push('narrow navigation remains operable');
  assert.deepEqual(failures, []);
  mkdirSync('output/playwright', { recursive: true });
  await page.setViewportSize({ width: 375, height: 812 });
  await page.screenshot({ path: 'output/playwright/pr7-remediation.png' });
  const result = { passed: checks.length, checks, pageErrors: failures };
  writeFileSync('output/playwright/pr7-remediation.json', JSON.stringify(result, null, 2));
  return result;
}

if (process.argv[2]) {
  const { chromium } = await import(pathToFileURL(process.argv[2]).href);
  const browser = await chromium.launch({ channel: 'msedge' });
  try {
    const page = await browser.newPage();
    page.setDefaultTimeout(10_000);
    console.log(JSON.stringify(await runChecks(page, process.argv[3]), null, 2));
  } finally {
    await browser.close();
  }
}
