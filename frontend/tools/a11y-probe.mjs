/**
 * 键盘 / 焦点 / 折叠面板探针（零依赖）。
 *
 * 为什么单独写一个：仓库已有的 frontend/tools/browser-regression.mjs 依赖 Playwright，
 * 而审查环境与本机都没有可用的 Playwright（子进程被拒）。这个脚本只用 Node 内置的
 * WebSocket 直接跟 Edge 的 DevTools 协议对话，不新增任何依赖。
 *
 * 它检查的是 jsdom 证明不了的东西：真实布局下的 Tab 顺序、焦点是否可见、
 * <details> 能否用键盘展开，以及设计 Token 在真实渲染里的落点。
 *
 * 用法（先构建 frontend 并起一个静态服务）：
 *   node frontend/tools/a11y-probe.mjs http://127.0.0.1:18767
 * 可用环境变量 EDGE_PATH 指定浏览器可执行文件。
 */
import { spawn } from 'node:child_process';
import { existsSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';

const baseURL = process.argv[2] ?? 'http://127.0.0.1:18767';
const PORT = Number(process.env.A11Y_PORT ?? 9333);

const BROWSER_CANDIDATES = [
  process.env.EDGE_PATH,
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
  '/usr/bin/microsoft-edge',
].filter(Boolean);

const failures = [];
const notes = [];
const check = (ok, label, detail = '') => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${label}${detail ? `  ${detail}` : ''}`);
  if (!ok) failures.push(label);
};

let browser = null;
let ws = null;

async function main() {
  const edge = BROWSER_CANDIDATES.find((p) => p && existsSync(p));
  if (!edge) throw new Error('找不到浏览器可执行文件，请用 EDGE_PATH 指定');

  browser = spawn(edge, [
    '--headless=new',
    '--disable-gpu',
    '--hide-scrollbars',
    `--remote-debugging-port=${PORT}`,
    `--user-data-dir=${mkdtempSync(join(tmpdir(), 'a11y-probe-'))}`,
    'about:blank',
  ], { stdio: 'ignore' });

  const target = await waitForTarget();
  ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', reject, { once: true });
  });
  ws.addEventListener('message', (event) => {
    const message = JSON.parse(event.data);
    if (message.id === undefined) return; // 事件通知，不在这里处理
    const entry = pending.get(message.id);
    if (!entry) return;
    pending.delete(message.id);
    if (message.error) entry.reject(new Error(message.error.message));
    else entry.resolve(message.result);
  });

  await send('Page.enable');
  await send('Runtime.enable');

  // ---- 1. 列表页：Tab 顺序与焦点可见性 ----
  await goto('#/sessions');
  const tabbed = await collectTabOrder(60);
  const navReached = ['#/sessions', '#/tasks', '#/reports'].every(
    (href) => tabbed.some((item) => item.href === href),
  );
  check(navReached, '模板核心导航入口都能用 Tab 到达', tabbed.filter((i) => i.href).map((i) => i.href).join(' '));
  check(
    tabbed.some((item) => item.text.includes('详情')),
    '课堂行的「详情」入口能用 Tab 到达',
  );
  check(
    tabbed.some((item) => item.cls.includes('account-trigger')),
    '顶栏右上角的账号入口能用 Tab 到达',
  );

  const noRing = tabbed.filter((item) => item.tag !== 'body' && !item.hasVisibleFocus);
  check(noRing.length === 0, '每个可聚焦元素都有可见的焦点指示', noRing.length ? `缺: ${noRing.map((i) => `${i.tag}.${i.cls}`).join(', ')}` : '');

  // ---- 2. <details> 能否用键盘展开 ----
  await goto('#/sessions');
  const detailsResult = await probeDetailsByKeyboard(30);
  check(detailsResult.reachable, '「技术详情」summary 能用 Tab 到达');
  check(detailsResult.toggledOpen && detailsResult.toggledClosed, '「技术详情」能用 Enter 展开并收起', JSON.stringify(detailsResult));

  // ---- 3. 导航往返：列表 → 详情 → 分析任务 → 报告 ----
  await goto('#/sessions');
  const roundTrip = await probeNavigation();
  check(roundTrip.listTitle === '课堂档案', '列表页标题正确', roundTrip.listTitle);
  check(roundTrip.detailTitle === '合成课堂', '点「详情」进入课堂详情', roundTrip.detailTitle);
  check(roundTrip.backTitle === '课堂档案', '详情页「返回课堂列表」可用', roundTrip.backTitle);
  check(roundTrip.tasksTitle === '分析任务', '导航到分析任务页', roundTrip.tasksTitle);
  check(roundTrip.reportTitle === '报告', '导航到报告页', roundTrip.reportTitle);

  // ---- 4. 设计 Token 在真实渲染里的落点 ----
  await goto('#/sessions');
  const styles = await probeTokens();
  check(styles.input.height === styles.select.height, '下拉框与输入框等高', `${styles.input.height} / ${styles.select.height}`);
  check(styles.input.radius === styles.select.radius, '下拉框与输入框同圆角', `${styles.input.radius} / ${styles.select.radius}`);
  check(styles.input.border === styles.select.border, '下拉框与输入框同边框', `${styles.input.border} / ${styles.select.border}`);
  check(styles.accountTrigger !== null, '顶栏右上角渲染出账号入口', styles.accountTrigger ? styles.accountTrigger.radius : '');
  notes.push(`顶栏头像直径 ${styles.accountAvatar ? styles.accountAvatar.height : '未渲染'}；页面输入框高度 ${styles.input.height}`);
  // 危险操作按设计是"白底 + 红字 + 红描边"，和次按钮同底色，所以按整体签名比较。
  const signature = (s) => `${s.bg} / ${s.color} / ${s.border}`;
  const signatures = new Set([signature(styles.primary), signature(styles.secondary), signature(styles.danger)]);
  check(signatures.size === 3, '主/次/危险三种按钮在真实渲染里可区分', [...signatures].join('  |  '));
  check(styles.primary.bg === 'rgb(22, 136, 121)', '主按钮使用系统草图强调色 #168879', styles.primary.bg);
  check(styles.notice.bg === 'rgb(253, 236, 234)', '提示条用的是浅红底 #FDECEA', styles.notice.bg);
  check(styles.noticeText !== styles.notice.bg, '提示条文字与底色不同', `${styles.noticeText} / ${styles.notice.bg}`);

  if (notes.length) {
    console.log('\n说明：');
    for (const note of notes) console.log(`- ${note}`);
  }
  console.log(`\n结论：${failures.length === 0 ? '全部通过' : `${failures.length} 项未通过`}`);
}

async function waitForTarget() {
  for (let i = 0; i < 60; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
      const page = list.find((item) => item.type === 'page');
      if (page?.webSocketDebuggerUrl) return page;
    } catch {
      // 端口还没起来
    }
    await sleep(250);
  }
  throw new Error('浏览器 DevTools 端口没有就绪');
}

let nextId = 0;
const pending = new Map();
function send(method, params = {}) {
  const id = ++nextId;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    setTimeout(() => {
      if (pending.delete(id)) reject(new Error(`${method} 超时`));
    }, 20000);
  });
}

function evaluate(expression) {
  return send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }).then((msg) => {
    if (msg.exceptionDetails) throw new Error(msg.exceptionDetails.exception?.description ?? '页面脚本异常');
    return msg.result.value;
  });
}

async function goto(hash) {
  await send('Page.navigate', { url: `${baseURL}/${hash}` });
  for (let i = 0; i < 40; i++) {
    await sleep(150);
    const ready = await evaluate("document.querySelector('main h1') ? document.querySelector('main h1').textContent : ''");
    if (ready) {
      await sleep(150);
      return;
    }
  }
  throw new Error(`页面没有渲染出内容：${hash}`);
}

async function pressKey(key, code, virtualKeyCode, text) {
  const base = { key, code, windowsVirtualKeyCode: virtualKeyCode, nativeVirtualKeyCode: virtualKeyCode };
  // 激活类按键（Enter/空格）要用 keyDown 并带 text，浏览器才会当成"字符输入"触发默认行为；
  // rawKeyDown 只送 keydown，<summary> 不会因此展开。
  await send('Input.dispatchKeyEvent', text === undefined ? { type: 'rawKeyDown', ...base } : { type: 'keyDown', ...base, text, unmodifiedText: text });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', ...base });
}

const FOCUS_INFO = `(() => {
  const el = document.activeElement;
  if (!el || el === document.body) return { tag: 'body', cls: '', text: '', href: null, hasVisibleFocus: true };
  const cs = getComputedStyle(el);
  const outlineVisible = cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0;
  return {
    tag: el.tagName.toLowerCase(),
    cls: typeof el.className === 'string' ? el.className : '',
    text: (el.textContent || '').trim().slice(0, 20),
    href: el.getAttribute('href'),
    hasVisibleFocus: outlineVisible || cs.boxShadow !== 'none',
    detailOpen: el.closest('details') ? el.closest('details').open : null,
  };
})()`;

/** 从页面顶部连续按 Tab，记录焦点落点。 */
async function collectTabOrder(times) {
  await evaluate('document.activeElement.blur(); window.scrollTo(0, 0)');
  const visited = [];
  for (let i = 0; i < times; i++) {
    await pressKey('Tab', 'Tab', 9);
    await sleep(40);
    const info = await evaluate(FOCUS_INFO);
    if (info.tag === 'body') continue;
    visited.push(info);
  }
  return visited;
}

async function probeDetailsByKeyboard(maxTabs) {
  await evaluate('document.activeElement.blur()');
  for (let i = 0; i < maxTabs; i++) {
    await pressKey('Tab', 'Tab', 9);
    await sleep(40);
    const info = await evaluate(FOCUS_INFO);
    if (info.tag !== 'summary') continue;
    await pressKey('Enter', 'Enter', 13, '\r');
    await sleep(120);
    const open = await evaluate(FOCUS_INFO);
    await pressKey('Enter', 'Enter', 13, '\r');
    await sleep(120);
    const closed = await evaluate(FOCUS_INFO);
    return { reachable: true, toggledOpen: open.detailOpen === true, toggledClosed: closed.detailOpen === false };
  }
  return { reachable: false, toggledOpen: false, toggledClosed: false };
}

async function probeNavigation() {
  const title = () => evaluate("document.querySelector('main h1').textContent");
  const clickByText = (selector, text) => evaluate(`(() => {
    const el = [...document.querySelectorAll(${JSON.stringify(selector)})]
      .find((node) => (node.textContent || '').trim().includes(${JSON.stringify(text)}));
    if (!el) return false;
    el.click();
    return true;
  })()`);

  const listTitle = await title();
  await clickByText('a', '详情');
  await sleep(400);
  const detailTitle = await title();
  await clickByText('a', '返回课堂列表');
  await sleep(400);
  const backTitle = await title();
  await clickByText('nav a', '分析任务');
  await sleep(400);
  const tasksTitle = await title();
  await clickByText('nav a', '教学报告');
  await sleep(400);
  const reportTitle = await title();
  return { listTitle, detailTitle, backTitle, tasksTitle, reportTitle };
}

/** 往页面里临时插入要素，量一下设计 Token 是否真的落到了真实渲染上（用完即删）。 */
async function probeTokens() {
  return evaluate(`(() => {
    const host = document.querySelector('main') || document.body;
    const box = document.createElement('div');
    host.prepend(box);
    const label = document.createElement('label');
    label.className = 'account-field';
    const input = document.createElement('input');
    const select = document.createElement('select');
    label.append(input, select);
    // 账号入口是真实渲染出来的（顶栏右上角），直接量它，不再注入一份模拟结构。
    const accountTrigger = document.querySelector('.account-trigger');
    const accountAvatar = document.querySelector('.account-avatar');
    const notice = document.createElement('p');
    notice.className = 'notice-strip';
    const p = document.createElement('button');
    p.className = 'btn btn-primary';
    const s = document.createElement('button');
    s.className = 'btn btn-secondary';
    const d = document.createElement('button');
    d.className = 'btn btn-danger';
    box.append(label, notice, p, s, d);
    const read = (el) => {
      const cs = getComputedStyle(el);
      return {
        height: cs.height,
        radius: cs.borderRadius,
        border: cs.borderTopWidth + ' ' + cs.borderTopStyle + ' ' + cs.borderTopColor,
        bg: cs.backgroundColor,
        color: cs.color,
      };
    };
    const result = {
      input: read(input),
      select: read(select),
      accountTrigger: accountTrigger ? read(accountTrigger) : null,
      accountAvatar: accountAvatar ? read(accountAvatar) : null,
      notice: read(notice),
      noticeText: getComputedStyle(notice).color,
      primary: read(p),
      secondary: read(s),
      danger: read(d),
    };
    box.remove();
    return result;
  })()`);
}

main()
  .then(() => {
    ws?.close();
    browser?.kill();
    process.exit(failures.length === 0 ? 0 : 1);
  })
  .catch((error) => {
    console.error('探针执行失败：', error.message);
    ws?.close();
    browser?.kill();
    process.exit(2);
  });
