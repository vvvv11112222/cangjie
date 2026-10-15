import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { NavLinks } from './App';
import type { RoleCode } from './data/authApi';
import { NAV_ITEMS, SKETCH_ROLE_ALIAS, visibleGroups } from './roleNav';

// Vitest 会把 CSS 导入替换成空字符串，所以这里直接读取源文件：
// 检查的就是真正会被打包的那份 styles.css。
const stylesheet = readFileSync(fileURLToPath(new URL('./styles.css', import.meta.url)), 'utf8');

/** 取出 @media (max-width: Npx) 的完整块内容，包含嵌套花括号。 */
function mediaBlock(maxWidth: number): string {
  const header = new RegExp(`@media \\(max-width:\\s*${maxWidth}px\\)\\s*\\{`);
  const match = header.exec(stylesheet);
  if (match === null) {
    throw new Error(`styles.css 中找不到 @media (max-width: ${maxWidth}px)`);
  }
  let depth = 1;
  let index = match.index + match[0].length;
  const start = index;
  while (index < stylesheet.length && depth > 0) {
    const char = stylesheet[index];
    if (char === '{') {
      depth += 1;
    } else if (char === '}') {
      depth -= 1;
    }
    index += 1;
  }
  return stylesheet.slice(start, index - 1);
}

function visiblePaths(roles: readonly RoleCode[]): string[] {
  return visibleGroups(roles)
    .flatMap((group) => group.items.map((item) => item.path))
    .sort();
}

/**
 * 期望值直接对着《系统草图.html》第 107～110 行的 roles[].pages 抄：
 *   admin    management, organizations, accounts, recordings, academic,
 *            dashboard, sessions, tasks, reports, sources, settings, audit, devices
 *   academic dashboard, sessions, academic, accounts, sources, audit
 *   supervisor dashboard, sessions, tasks, reports, sources, audit, devices
 *   teacher  dashboard, sessions, tasks, reports, sources
 * 转写属于课堂复核流程，保留辅助路由，但不增加模板以外的侧栏入口。
 */
const EXPECTED_BY_ROLE: Record<RoleCode, string[]> = {
  sys_admin: [
    '#/management',
    '#/organizations',
    '#/accounts',
    '#/recordings',
    '#/dashboard',
    '#/sessions',
    '#/academic',
    '#/tasks',
    '#/reports',
    '#/sources',
    '#/devices',
    '#/settings',
    '#/audit',
  ].sort(),
  academic_admin: ['#/dashboard', '#/sessions', '#/academic', '#/accounts', '#/sources', '#/audit'].sort(),
  supervisor: [
    '#/dashboard',
    '#/sessions',
    '#/tasks',
    '#/reports',
    '#/sources',
    '#/audit',
    '#/devices',
  ].sort(),
  teacher: ['#/dashboard', '#/sessions', '#/tasks', '#/reports', '#/sources'].sort(),
};

describe('侧栏导航与角色差异', () => {
  it('四种固定角色都写了期望值，新增角色不会悄悄漏掉', () => {
    expect(Object.keys(SKETCH_ROLE_ALIAS).sort()).toEqual(Object.keys(EXPECTED_BY_ROLE).sort());
  });

  it('四个角色看到的入口与《系统草图.html》一致', () => {
    for (const [role, expected] of Object.entries(EXPECTED_BY_ROLE)) {
      expect(visiblePaths([role as RoleCode]), role).toEqual(expected);
    }
  });

  it('未登录时显示全部入口，不假装用户已经有某个角色', () => {
    expect(visiblePaths([])).toEqual(NAV_ITEMS.map((item) => item.path).sort());
  });

  it('兼任多个角色时取并集', () => {
    const paths = visiblePaths(['teacher', 'supervisor']);
    expect(paths).toContain('#/devices'); // 只有督导能看
    expect(paths).toContain('#/tasks');
    expect(paths).not.toContain('#/accounts'); // 教师和督导都没有这个入口
  });

  it('学院教务看到的菜单名是"学院人员与权限"，看不到管理中心的其它入口', () => {
    const html = renderToStaticMarkup(<NavLinks current="#/accounts" roles={['academic_admin']} />);
    expect(html).toContain('学院人员与权限');
    expect(html).not.toContain('全校管理工作台');
    expect(html).not.toContain('课堂录像管理');
  });

  it('模板核心页面能从导航到达，并标出当前页', () => {
    const html = renderToStaticMarkup(<NavLinks current="#/sessions" roles={[]} />);
    for (const path of ['#/sessions', '#/tasks', '#/reports']) {
      expect(html).toContain(`href="${path}"`);
    }
    expect(html).not.toContain('href="#/transcript"');
    expect(html).toContain('aria-current="page"');
  });

  /**
   * 回归检查：窄屏曾用 `.sidebar { display: none }` 隐藏侧栏，而导航只存在于侧栏内，
   * 导致手机/平板竖屏下无法切换页面。窄屏必须保留紧凑导航。
   */
  it('窄屏不隐藏侧栏导航，而是换成顶部紧凑导航条', () => {
    expect(stylesheet).not.toMatch(/\.sidebar\s*\{[^}]*display:\s*none/s);

    const narrow = mediaBlock(900);
    expect(narrow).toMatch(/\.sidebar\s*\{[^}]*flex-direction:\s*row/s);
    expect(narrow).toMatch(/\.sidebar nav\s*\{[^}]*display:\s*flex/s);
    expect(narrow).not.toMatch(/\.nav-item\s*\{[^}]*display:\s*none/s);
  });
});
