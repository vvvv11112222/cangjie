// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { buildSessionTasks } from '../data/viewModel';
import { SessionListPage } from './SessionListPage';
const api = vi.hoisted(() => ({ sources: vi.fn(), upload: vi.fn(), reload: vi.fn() }));
vi.mock('../data/hooks', () => ({ useSessionTasks: () => ({ status: 'ready', error: null, hasData: true, data: { tasks: [], nextCursor: null }, origin: { sources: [], label: '接口数据' }, reload: api.reload }) }));
vi.mock('../data/sources', async original => ({ ...await original<typeof import('../data/sources')>(), isApiMode: () => true }));
vi.mock('../data/m2Api', () => ({ listSessionSources: (...args: unknown[]) => api.sources(...args) }));
vi.mock('../data/sessionOps', () => ({ createSession: vi.fn(), uploadSessionMedia: (...args: unknown[]) => api.upload(...args) }));
const original = buildSessionTasks()[0]!;
const tasks = [original, { ...original, session: { ...original.session, id: '10000000-0000-4000-8000-000000000001', title: '课堂B' } }]
  .map(task => ({ ...task, session: { ...task.session, allowed_actions: ['upload' as const] } }));
const sourceIds = ['20000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000002'];
const source = (i: number) => ({ id: sourceIds[i], session_id: tasks[i]!.session.id, title: `录像来源${i}`, rights_status: 'verified', allowed_uses: ['analysis'], allowed_actions: ['upload'] });
let container: HTMLDivElement; let root: ReturnType<typeof createRoot>;
const button = (label: string) => [...container.querySelectorAll('button')].find(b => b.textContent?.trim() === label)!;
async function select(node: HTMLSelectElement, value: string) { await act(async () => { node.value = value; node.dispatchEvent(new Event('change', { bubbles: true })); }); }
async function file() {
  const picked = new File(['video'], 'test.mp4', { type: 'video/mp4' });
  await act(async () => { const node = container.querySelector<HTMLInputElement>('input[type=file]')!; Object.defineProperty(node, 'files', { configurable: true, value: [picked] }); node.dispatchEvent(new Event('change', { bubbles: true })); });
  return picked;
}
async function submit() { await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); }); }
beforeEach(async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); vi.clearAllMocks();
  api.sources.mockImplementation(async (session: string) => ({ items: [source(tasks.findIndex(t => t.session.id === session))], next_cursor: null }));
  api.upload.mockResolvedValue({ id: '30000000-0000-4000-8000-000000000001', status: 'pending' });
  container = document.createElement('div'); document.body.append(container); root = createRoot(container);
  await act(async () => root.render(<SessionListPage tasks={tasks} />));
  await act(async () => button('上传课堂录像').click());
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); });
it('列表上传选择来源标题，提交系统编号，不要求手填编号', async () => {
  expect(container.querySelector('input[placeholder="source_record_id"]')).toBeNull();
  const picked = await file();
  await select(container.querySelectorAll('select')[1]!, sourceIds[0]!); await submit();
  expect(api.upload).toHaveBeenCalledWith(tasks[0]!.session.id, picked, sourceIds[0]);
  expect(container.querySelector('[role=status]')?.textContent).toContain('录像已上传');
});
it('未选择来源时明确提示，并阻止发起上传请求', async () => {
  await file(); await submit();
  expect(container.textContent).toContain('请选择这节课堂已登记的录像来源');
  expect(api.upload).not.toHaveBeenCalled();
});
it('切换课堂后清空前一个课堂的来源，不能串用', async () => {
  await file(); await select(container.querySelectorAll('select')[1]!, sourceIds[0]!);
  await select(container.querySelectorAll('select')[0]!, tasks[1]!.session.id);
  expect(container.querySelectorAll('select')[1]!.value).toBe(''); await submit();
  expect(api.upload).not.toHaveBeenCalled();
  await select(container.querySelectorAll('select')[1]!, sourceIds[1]!); await submit();
  expect(api.upload.mock.calls[0]?.[0]).toBe(tasks[1]!.session.id);
  expect(api.upload.mock.calls[0]?.[2]).toBe(sourceIds[1]);
});
