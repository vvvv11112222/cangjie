import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { NAV_ROUTES, NavLinks } from './App';

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

describe('页面导航入口', () => {
  it('三个页面都能从导航到达', () => {
    expect(NAV_ROUTES.map((route) => route.path)).toEqual([
      '#/sessions',
      '#/transcript',
      '#/reports',
    ]);

    const html = renderToStaticMarkup(<NavLinks current="#/transcript" />);
    for (const route of NAV_ROUTES) {
      expect(html).toContain(`href="${route.path}"`);
    }
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
