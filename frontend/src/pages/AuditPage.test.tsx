// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import sample from '../../../contracts/examples/audit-page.json';
const h = vi.hoisted(() => ({ list: vi.fn(), key: 'A#1', status: 'authenticated' }));
vi.mock('../auth', () => ({ useAuth: () => ({ status: h.status, sessionKey: h.key, me: h.status === 'authenticated' ? { roles: [{ role_code: 'sys_admin' }] } : null }) }));
vi.mock('../data/sources', () => ({ isApiMode: () => true }));
vi.mock('../data/auditApi', () => ({ listAudit: (...args: unknown[]) => h.list(...args) }));
import { AuditPage } from './AuditPage';
let root: Root; let container: HTMLDivElement;
const button = (label: string) => [...container.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === label)!;
async function render() { await act(async () => root.render(<AuditPage/>)); }
beforeEach(() => { Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); h.key = 'A#1'; h.status = 'authenticated'; h.list.mockReset().mockResolvedValue(sample); container = document.createElement('div'); document.body.append(container); root = createRoot(container); });
afterEach(() => { act(() => root.unmount()); container.remove(); });
it('shows saved actions, preserves bigint strings and appends older pages', async () => {
  h.list.mockResolvedValueOnce({ ...sample, next_cursor: 'older' }).mockResolvedValueOnce({ items: [{ ...sample.items[0], id: '2' }], next_cursor: null });
  await render(); expect(container.textContent).toContain('合成测试管理员'); expect(container.textContent).toContain('9007199254740993');
  await act(async () => button('加载更早记录').click());
  expect(container.querySelectorAll('tbody tr')).toHaveLength(2);
  expect(h.list.mock.calls[1]?.[0]).toEqual({ action: '', result: '', cursor: 'older' });
});
it('does not claim empty records on server failure and can retry', async () => {
  h.list.mockRejectedValueOnce(new Error('HTTP503'));
  await render(); expect(container.textContent).toContain('HTTP503'); expect(container.textContent).not.toContain('没有符合条件');
  await act(async () => button('重试读取记录').click()); expect(container.querySelectorAll('tbody tr')).toHaveLength(1);
});
it('discards another session response after sign out', async () => {
  let resolve!: (value: typeof sample) => void;
  h.list.mockReturnValueOnce(new Promise(r => { resolve = r; })); await render();
  h.key = 'anonymous#2'; h.status = 'anonymous'; await render();
  await act(async () => resolve(sample));
  expect(container.textContent).toContain('请先登录'); expect(container.textContent).not.toContain('合成测试管理员'); expect(container.querySelector('tbody')).toBeNull();
});
