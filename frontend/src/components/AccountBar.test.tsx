import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { AccountBar } from './AccountBar';

/**
 * 固定样例模式（VITE_DATA_SOURCE 未设为 api）没有后端，
 * 账号区只说明当前模式，不能渲染一个点了必然失败的登录表单。
 */
describe('账号区（固定样例模式）', () => {
  it('只说明样例模式，不渲染登录表单', () => {
    const html = renderToStaticMarkup(<AccountBar />);
    expect(html).toContain('样例模式');
    expect(html).toContain('VITE_DATA_SOURCE');
    expect(html).not.toContain('用户名');
    expect(html).not.toContain('name="password"');
  });
});
