// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { AccountMe } from '../data/authApi';

/**
 * 账号入口（顶栏右上角的头像按钮）。
 *
 * 三种状态必须分得清：
 *   固定样例模式 → 只说明当前模式，不渲染一个点了必然失败的登录表单；
 *   接口模式未登录 → 点头像展开登录表单；
 *   接口模式已登录 → 显示姓名、角色与账号可用操作，提供退出与切换账号。
 *
 * 这里用可切换的假状态替代构建变量 VITE_DATA_SOURCE：真实取值在模块加载时
 * 就已固定（data/sources.ts），测试里换不了，所以只替换 isApiMode 这一个函数。
 */
const env = vi.hoisted(() => ({ api: false, me: null as unknown }));

vi.mock('../data/sources', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/sources')>()),
  isApiMode: () => env.api,
}));

vi.mock('../auth', () => ({
  useAuth: () => ({
    status: env.me === null ? 'offline' : 'authenticated',
    me: env.me,
    error: null,
    capabilities: null,
    sessionKey: 'test#1',
    busy: false,
    login: async () => false,
    logout: async () => {},
    clearError: () => {},
  }),
}));

import { AccountBar } from './AccountBar';

let root: Root | null = null;
let container: HTMLDivElement;

beforeEach(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  env.api = false;
  env.me = null;
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  env.api = false;
  env.me = null;
});

async function renderBar(me: AccountMe | null, api: boolean): Promise<void> {
  env.api = api;
  env.me = me;
  await act(async () => {
    root?.render(<AccountBar />);
  });
}

function avatar(): HTMLElement {
  const element = container.querySelector<HTMLElement>('.account-trigger');
  if (element === null) {
    throw new Error('找不到账号入口按钮');
  }
  return element;
}

async function click(element: Element): Promise<void> {
  await act(async () => {
    element.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
}

const supervisor: AccountMe = {
  user_id: 'u-1',
  display_name: '陈知远',
  roles: [{ id: 'r-1', role_code: 'supervisor', scope_org_id: null }],
  allowed_actions: ['review', 'publish'],
};

describe('账号入口（固定样例模式）', () => {
  it('头像按钮写明当前模式，不渲染登录表单', async () => {
    await renderBar(null, false);
    expect(container.textContent).toContain('样例模式');
    expect(container.querySelector('input[name="password"]')).toBeNull();
    expect(container.querySelector('input[name="username"]')).toBeNull();
  });
});

describe('账号入口（接口模式）', () => {
  it('未登录：点头像展开登录表单，取消后收起', async () => {
    await renderBar(null, true);
    expect(avatar().getAttribute('aria-expanded')).toBe('false');
    expect(avatar().textContent).toContain('登录');

    await click(avatar());
    expect(avatar().getAttribute('aria-expanded')).toBe('true');
    // 第一步：模板式的选择账号列表（还没有密码框）。
    const demoRow = [...container.querySelectorAll<HTMLButtonElement>('.account-option')].find((row) =>
      row.textContent?.includes('supervisor_demo'),
    );
    expect(demoRow).toBeDefined();
    expect(container.querySelector('input[name="password"]')).toBeNull();

    // 第二步：选中账号后才出现密码输入。
    await click(demoRow as Element);
    expect(container.querySelector<HTMLInputElement>('input[name="username"]')?.value).toBe(
      'supervisor_demo',
    );
    expect(container.querySelector('input[name="password"]')).not.toBeNull();

    // 可以退回上一步重新选账号。
    const back = [...container.querySelectorAll('button')].find((button) => button.textContent === '上一步');
    expect(back).toBeDefined();
    await click(back as Element);
    expect(container.querySelector('input[name="password"]')).toBeNull();
    expect(container.querySelectorAll('.account-option').length).toBeGreaterThan(1);

    const cancel = [...container.querySelectorAll('button')].find((button) => button.textContent === '取消');
    expect(cancel).toBeDefined();
    await click(cancel as Element);
    expect(avatar().getAttribute('aria-expanded')).toBe('false');
  });

  it('已登录：头像显示姓名首字，面板给出角色、可用操作与退出入口', async () => {
    await renderBar(supervisor, true);
    expect(avatar().textContent).toContain('陈');
    expect(avatar().textContent).toContain('陈知远');

    await click(avatar());
    const panel = container.querySelector('.account-menu');
    expect(panel).not.toBeNull();
    const text = panel?.textContent ?? '';
    expect(text).toContain('陈知远');
    expect(text).toContain('教学督导');
    expect(text).toContain('复核');
    expect(text).toContain('退出登录');
    expect(text).toContain('切换账号');
  });

  it('按 Esc 关闭面板，不会把人留在浮层里', async () => {
    await renderBar(supervisor, true);
    await click(avatar());
    expect(avatar().getAttribute('aria-expanded')).toBe('true');

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    });
    expect(avatar().getAttribute('aria-expanded')).toBe('false');
    expect(container.querySelector('.account-menu')).toBeNull();
  });
});
