// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
const h = vi.hoisted(() => ({ offerings: vi.fn(), courses: vi.fn(), groups: vi.fn() }));
vi.mock('../data/sources', () => ({ isApiMode: () => true }));
vi.mock('../data/hooks', () => ({ useSessionTasks: () => ({ origin: { kind: 'api', sources: [], label: '接口数据' }, hasData: true, data: { tasks: [], nextCursor: null }, status: 'ready' }) }));
vi.mock('../data/academicApi', async original => ({ ...await original<typeof import('../data/academicApi')>(), listOfferings: h.offerings, listCourses: h.courses, listClassGroups: h.groups }));
import { SessionListPage } from './SessionListPage';
let node: HTMLDivElement, root: ReturnType<typeof createRoot>;
const offering = (id: string, status = 'active') => ({ id, status, courseId: 'course', classGroupId: 'class', code: id, orgUnitId: 'college' });
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  window.history.replaceState(null, '', '#/sessions');
  vi.resetAllMocks();
  h.offerings.mockResolvedValue({ items: [offering('first')], nextCursor: null });
  h.courses.mockResolvedValue({ items: [{ id: 'course', name: '操作系统', code: 'OS' }], nextCursor: null });
  h.groups.mockResolvedValue({ items: [{ id: 'class', name: '软件2301' }], nextCursor: null });
  node = document.createElement('div'); document.body.append(node); root = createRoot(node);
});
afterEach(async () => { await act(async () => root.unmount()); node.remove(); });
async function click(text: string) { await act(async () => [...node.querySelectorAll('button')].find(b => b.textContent?.trim() === text)!.click()); }
async function open() { await act(async () => root.render(<SessionListPage tasks={[]}/>)); await click('创建课堂'); }
it('loads every offering/course/class page and excludes archived offerings', async () => {
  h.offerings.mockImplementation(async (cursor?: string) => cursor ? { items: [offering('second')], nextCursor: null } : { items: [offering('archived', 'archived')], nextCursor: 'next' });
  h.courses.mockImplementation(async (cursor?: string) => cursor ? { items: [{ id: 'course', name: '操作系统', code: 'OS' }], nextCursor: null } : { items: [], nextCursor: 'courses-2' });
  h.groups.mockImplementation(async (cursor?: string) => cursor ? { items: [{ id: 'class', name: '软件2301' }], nextCursor: null } : { items: [], nextCursor: 'classes-2' });
  await open();
  const options = [...node.querySelectorAll<HTMLOptionElement>('form option')];
  expect(options).toHaveLength(1); expect(options[0]?.value).toBe('second');
  expect(options[0]?.textContent).toContain('操作系统'); expect(options[0]?.textContent).toContain('软件2301');
  expect(h.offerings).toHaveBeenCalledWith('next'); expect(h.courses).toHaveBeenCalledWith('courses-2'); expect(h.groups).toHaveBeenCalledWith('classes-2');
  expect(node.querySelector('a[href="#/academic?tab=offerings"]')).not.toBeNull();
});
it('shows a failed later page as an error and retries without claiming no offerings exist', async () => {
  h.offerings.mockRejectedValueOnce(new Error('合成分页故障'));
  await open(); expect(node.textContent).toContain('开课资料读取失败'); expect(node.textContent).not.toContain('当前范围内没有');
  await click('重新读取开课资料'); expect(node.querySelector('form option')?.textContent).toContain('操作系统');
  expect(node.textContent).not.toContain('合成分页故障');
});
