// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { NotificationBell } from './NotificationBell';

/**
 * 待办提醒（草图的 notification-button）。
 * 固定样例里那个课堂"有主媒体、批次已结束、还没有当前报告"，
 * 所以应该算出一条"分析已结束但还没有当前报告"的提醒。
 */
let root: Root | null = null;
let container: HTMLDivElement;

beforeEach(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
  act(() => {
    root?.render(<NotificationBell />);
  });
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
});

describe('待办提醒', () => {
  it('按钮上标出条数，展开后列出真实口径的条目', async () => {
    const button = container.querySelector<HTMLButtonElement>('.icon-button');
    expect(button).not.toBeNull();
    expect(button?.getAttribute('aria-label')).toContain('1 条');
    expect(button?.getAttribute('aria-expanded')).toBe('false');

    await act(async () => {
      button?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });

    const panel = container.querySelector('#notification-panel');
    expect(panel).not.toBeNull();
    expect(panel?.textContent).toContain('待办提醒');
    expect(panel?.textContent).toContain('个课堂的分析已结束，还没有当前报告');
    expect(button?.getAttribute('aria-expanded')).toBe('true');
  });

  it('按 Esc 关闭面板', async () => {
    const button = container.querySelector<HTMLButtonElement>('.icon-button');
    await act(async () => {
      button?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    expect(container.querySelector('#notification-panel')).not.toBeNull();

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    });
    expect(container.querySelector('#notification-panel')).toBeNull();
  });
});
