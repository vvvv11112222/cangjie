// @vitest-environment jsdom
import { act } from 'react';
import type { ReactElement } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ApiError } from '../data/errors';
import type { AuthContextValue } from '../auth';
import type { SessionTasksResult } from '../data/hooks';
import type { Run, Session } from '../types';

const h = vi.hoisted(() => ({
  auth: {} as AuthContextValue, loaded: {} as SessionTasksResult,
  getRun: vi.fn(), listRuns: vi.fn(), listSources: vi.fn(), createSource: vi.fn(), verifySource: vi.fn(), upload: vi.fn(),
  users: vi.fn(), units: vi.fn(), createUser: vi.fn(), patchUser: vi.fn(), grant: vi.fn(),
  m1: vi.fn(), writeAcademic: vi.fn(), importAcademic: vi.fn(),
}));
vi.mock('../auth', () => ({ useAuth: () => h.auth }));
vi.mock('../data/hooks', () => ({ useSessionTasks: () => h.loaded }));
vi.mock('../data/sources', () => ({ isApiMode: () => true, dataSource: { getRun: h.getRun } }));
vi.mock('../data/m2Api', () => ({ listSessionRuns: h.listRuns, listSessionSources: h.listSources, createSource: h.createSource, verifySource: h.verifySource }));
vi.mock('../data/sessionOps', () => ({ uploadSessionMedia: h.upload }));
vi.mock('../data/identityApi', async importOriginal => ({ ...await importOriginal<typeof import('../data/identityApi')>(), listUsers: h.users, createUser: h.createUser, patchUser: h.patchUser, createRoleBinding: h.grant }));
vi.mock('../data/m1Api', async importOriginal => ({ ...await importOriginal<typeof import('../data/m1Api')>(),
  createTerm: (input: unknown) => h.writeAcademic('term', input), patchTerm: (id: string, input: unknown) => h.writeAcademic('term', input, id),
  createCourse: (input: unknown) => h.writeAcademic('course', input), patchCourse: (id: string, input: unknown) => h.writeAcademic('course', input, id),
  createClassroom: (input: unknown) => h.writeAcademic('classroom', input), patchClassroom: (id: string, input: unknown) => h.writeAcademic('classroom', input, id),
  createOffering: (input: unknown) => h.writeAcademic('offering', input), patchOffering: (id: string, input: unknown) => h.writeAcademic('offering', input, id),
  patchSchedule: (id: string, input: unknown) => h.writeAcademic('schedule', input, id), importSchedules: h.importAcademic,
  listTerms: (cursor?: string) => h.m1('terms', cursor), listCourses: (cursor?: string) => h.m1('courses', cursor),
  listClassrooms: (cursor?: string) => h.m1('classrooms', cursor), listOfferings: (cursor?: string) => h.m1('offerings', cursor),
  listSchedules: (cursor?: string, range?: unknown) => h.m1('schedules', cursor, range),
}));
vi.mock('../data/academicApi', async importOriginal => ({ ...await importOriginal<typeof import('../data/academicApi')>(), listOrgUnits: h.units, listClassGroups: async () => ({ items: [], nextCursor: null }) }));
vi.mock('../components/NotificationBell', () => ({ NotificationBell: () => null }));
vi.mock('../components/AccountBar', () => ({ AccountBar: () => null }));
import { TasksPage } from './TasksPage';
import { SourcesPage } from './SourcesPage';
import { AccountsPage } from './AccountsPage';
import { App } from '../App';
import { AcademicPage } from './AcademicPage';

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;
const run = { id: 'run-a', session_id: 'A', mode: 'full', status: 'running', jobs: [], stage_plan: [], error_code: null, limitations: [], allowed_actions: [] } as unknown as Run;
const user = { id: 'user-1', orgUnitId: 'department', username: 'teacher1', displayName: '测试教师', status: 'active' };
function session(id: string): Session { return { id, title: `课堂 ${id}`, planned_start_at: '2026-10-16T00:00:00Z', allowed_actions: ['upload'], primary_media_asset_id: null } as unknown as Session; }
function source(id: string) { return { id: `${id}-source`, session_id: id, title: `来源 ${id}`, attribution: `授权人 ${id}`, source_type: 'self_recorded', rights_status: 'verified', requested_uses: ['playback'], allowed_uses: ['playback'], allowed_actions: ['upload', 'verify'] }; }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }
async function mount(element: ReactElement) { await act(async () => root.render(element)); }
function button(text: string): HTMLButtonElement { const found = [...container.querySelectorAll('button')].find(node => node.textContent === text); if (!found) throw new Error(`Missing button ${text}`); return found; }
async function click(text: string) { await act(async () => button(text).click()); }
async function select(node: HTMLSelectElement, value: string) { await act(async () => { node.value = value; node.dispatchEvent(new Event('change', { bubbles: true })); }); }
async function fill(node: HTMLInputElement, value: string) { await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(node, value); node.dispatchEvent(new Event('input', { bubbles: true })); }); }
async function submit() { await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); }); }
function classroomSelect() { return container.querySelector('select')!; }

beforeEach(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  vi.clearAllMocks();
  h.auth = { status: 'authenticated', sessionKey: 'admin#1', capabilities: ['upload'], me: { user_id: 'admin', display_name: '管理员', roles: [{ id: 'binding', role_code: 'sys_admin', scope_org_id: null }], allowed_actions: ['upload'] }, error: null, busy: false, login: vi.fn(), logout: vi.fn(), clearError: vi.fn() };
  h.loaded = { data: { tasks: [{ session: session('A'), run }, { session: session('B'), run: null }], nextCursor: null }, status: 'ready', error: null, actionLabel: null, loadMore: vi.fn(), reload: vi.fn() } as unknown as SessionTasksResult;
  h.listRuns.mockImplementation(async (id: string) => ({ items: id === 'A' ? [run] : [], next_cursor: null }));
  h.listSources.mockImplementation(async (id: string) => ({ items: [source(id)], next_cursor: null }));
  h.createSource.mockResolvedValue(source('A')); h.verifySource.mockResolvedValue(source('A')); h.upload.mockResolvedValue({ id: 'media-a', status: 'pending' });
  h.units.mockResolvedValue({ items: [{ id: 'department', kind: 'department', name: '院系', parentId: 'college' }, { id: 'college', kind: 'college', name: '学院', parentId: null }], nextCursor: null });
  h.users.mockResolvedValue({ items: [user], nextCursor: null });
  h.createUser.mockResolvedValue(user); h.patchUser.mockResolvedValue(user); h.grant.mockResolvedValue({});
  h.m1.mockResolvedValue({ items: [], next_cursor: null });
  h.writeAcademic.mockReset().mockResolvedValue({}); h.importAcademic.mockReset().mockResolvedValue([]);
  container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container);
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); vi.useRealTimers(); });

it('latest task reaches terminal state and stops polling despite the original classroom snapshot', async () => {
  vi.useFakeTimers(); h.getRun.mockResolvedValue({ ...run, status: 'succeeded' });
  await mount(<TasksPage/>);
  await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
  expect(container.textContent).toContain('已完成');
  await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
  expect(h.getRun).toHaveBeenCalledTimes(1);
});

it.each([401, 403, 410])('stops polling after HTTP %s and keeps the reason visible', async status => {
  vi.useFakeTimers(); h.getRun.mockRejectedValue(new ApiError({ code: 'FORBIDDEN', status, message: 'forbidden' }));
  await mount(<TasksPage/>);
  await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
  await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
  expect(h.getRun).toHaveBeenCalledTimes(1);
  expect(container.textContent).toContain('已停止自动刷新');
});

it('a slow history response cannot overwrite a completed poll', async () => {
  vi.useFakeTimers(); const history = deferred<{ items: Run[]; next_cursor: null }>();
  h.listRuns.mockImplementation((id: string) => id === 'A' ? history.promise : Promise.resolve({ items: [], next_cursor: null })); h.getRun.mockResolvedValue({ ...run, status: 'succeeded' });
  await mount(<TasksPage/>); await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
  await act(async () => history.resolve({ items: [run], next_cursor: null }));
  await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
  expect(container.textContent).toContain('已完成'); expect(h.getRun).toHaveBeenCalledTimes(1);
});

it('loads additional classrooms for source registration', async () => {
  h.loaded.actionLabel = '加载更多';
  h.loaded.loadMore = vi.fn(() => { h.loaded = { ...h.loaded, data: { ...h.loaded.data, tasks: [...h.loaded.data.tasks, { ...h.loaded.data.tasks[0]!, session: session('21'), run: null }] } }; root.render(<SourcesPage/>); });
  await mount(<SourcesPage/>); await click('加载更多');
  expect([...classroomSelect().options].some(option => option.value === '21')).toBe(true);
});

it.each(['create', 'verify'] as const)('ignores a late %s after switching classroom A to B', async action => {
  const pending = deferred<ReturnType<typeof source>>();
  (action === 'create' ? h.createSource : h.verifySource).mockReturnValueOnce(pending.promise);
  await mount(<SourcesPage/>); await select(classroomSelect(), 'A');
  await click(action === 'create' ? '登记新素材' : '核验'); await submit();
  await select(classroomSelect(), 'B');
  await act(async () => pending.resolve(source('A')));
  expect(container.textContent).toContain('来源 B'); expect(container.textContent).not.toContain('来源 A');
  expect(h.listSources.mock.calls.map(call => call[0])).toEqual(['A', 'B']);
});

it('offers the real media preparation entry after uploading instead of claiming a queued check', async () => {
  await mount(<SourcesPage/>); await select(classroomSelect(), 'A'); await click('上传录像');
  const input = container.querySelector<HTMLInputElement>('input[type=file]')!;
  Object.defineProperty(input, 'files', { value: [new File(['video'], 'test.mp4', { type: 'video/mp4' })] });
  // jsdom FormData does not read an overridden FileList; stub only this browser boundary.
  const NativeFormData = globalThis.FormData;
  vi.stubGlobal('FormData', class extends NativeFormData { override get(key: string) { return key === 'file' ? input.files![0]! : super.get(key); } });
  try { await submit(); } finally { vi.unstubAllGlobals(); }
  expect(h.upload).toHaveBeenCalledWith('A', expect.any(File), 'A-source');
  expect(container.textContent).toContain('尚未开始媒体检查');
  expect(container.querySelector('a')?.getAttribute('href')).toBe('#/sessions/A');
});

it.each([['teacher', null], ['supervisor', 'college'], ['academic_admin', 'college'], ['sys_admin', null]])('grants %s with the backend scope', async (role, scope) => {
  await mount(<AccountsPage/>); await click('编辑权限');
  const selects = container.querySelectorAll<HTMLSelectElement>('form select');
  await select(selects[2]!, role!); await submit();
  expect(h.grant).toHaveBeenCalledWith(user.id, role, scope);
  await click('编辑权限'); await submit(); expect(h.grant).toHaveBeenCalledTimes(1);
});

it('centers the account empty state inside the table card', async () => {
  h.users.mockResolvedValue({ items: [], nextCursor: null });
  await mount(<AccountsPage/>);
  const empty = container.querySelector('.accounts-empty-state');
  expect(empty).not.toBeNull();
  expect(empty?.textContent).toContain('没有符合条件的账号');
});

it('centers empty states across every academic table', async () => {
  await mount(<AcademicPage/>);
  for (const tab of ['本周课表', '开课实例', '课程', '学期', '教室']) {
    await click(tab);
    expect(container.querySelector('.academic-empty-state')).not.toBeNull();
  }
});

it('retries only the role grant after account creation succeeds', async () => {
  h.users.mockResolvedValue({ items: [], nextCursor: null }); h.grant.mockRejectedValueOnce(new Error('temporary failure'));
  await mount(<AccountsPage/>); await click('新增账号');
  const inputs = container.querySelectorAll<HTMLInputElement>('form input');
  await fill(inputs[0]!, '测试教师'); await fill(inputs[1]!, 'teacher1'); await fill(inputs[2]!, 'test-password-123');
  await select(container.querySelectorAll<HTMLSelectElement>('form select')[2]!, 'teacher'); await submit();
  expect(container.textContent).toContain('账号已创建'); await submit();
  expect(h.createUser).toHaveBeenCalledTimes(1); expect(h.grant).toHaveBeenCalledTimes(2);
});

it.each(['#/sources', '#/accounts'])('App remounts private page state on logout and account replacement at %s', async route => {
  window.location.hash = route;
  await mount(<App/>);
  if (route === '#/sources') await select(classroomSelect(), 'A');
  const privateText = route === '#/sources' ? '授权人 A' : '测试教师';
  expect(container.textContent).toContain(privateText);
  h.auth = { ...h.auth, status: 'anonymous', sessionKey: 'anonymous#1', me: null, capabilities: [] };
  h.users.mockResolvedValue({ items: [], nextCursor: null });
  await mount(<App/>); expect(container.textContent).not.toContain(privateText);
  h.auth = { ...h.auth, status: 'authenticated', sessionKey: 'other#2' };
  await mount(<App/>); expect(container.textContent).not.toContain(privateText);
});

it.each([
  ['学期', '新增学期', 'term', { code: 'T1', name: '秋季', start_date: '2026-09-01', end_date: '2027-01-01' }],
  ['课程', '新增课程', 'course', { org_unit_id: 'college', code: 'C1', name: '课程', description: '课程说明' }],
  ['教室', '新增教室', 'classroom', { code: 'R1', name: '教室', capacity: 30 }],
  ['开课实例', '新增开课实例', 'offering', { org_unit_id: 'college', term_id: 'term', course_id: 'course', teacher_id: 'teacher', class_group_id: 'group', code: 'O1', status: 'active' }],
] as const)('creates %s through the real form submit boundary', async (tab, action, kind, input) => {
  await mount(<AcademicPage/>); await click(tab); await click(action);
  const Native = globalThis.FormData;
  vi.stubGlobal('FormData', class extends Native { override get(key: string) { return String((input as Record<string, unknown>)[key] ?? ''); } });
  try { await submit(); } finally { vi.unstubAllGlobals(); }
  expect(h.writeAcademic).toHaveBeenCalledWith(kind, input);
  expect(container.textContent).toContain('资料已保存');
});

it('requires confirmation before cancelling and preserves the schedule fields', async () => {
  const row = { id: 's1', offering_id: 'o1', classroom_id: 'r1', starts_at: '2026-10-20T00:00:00Z', ends_at: '2026-10-20T01:00:00Z', status: 'active', class_group_id: 'g1', teacher_id: 'u1' };
  h.m1.mockImplementation(async kind => ({ items: kind === 'schedules' ? [row] : [], next_cursor: null }));
  await mount(<AcademicPage/>); await click('取消排课');
  expect(h.writeAcademic).not.toHaveBeenCalled();
  await click('确认取消排课');
  expect(h.writeAcademic).toHaveBeenCalledWith('schedule', { offering_id: row.offering_id, classroom_id: row.classroom_id, starts_at: row.starts_at, ends_at: row.ends_at, status: 'cancelled' }, 's1');
});

it('keeps failed import input and displays the conflicting resource and row', async () => {
  h.importAcademic.mockRejectedValue(new ApiError({ code: 'SCHEDULE_CONFLICT', status: 409, details: { resource_type: 'teacher', row_numbers: [2] } }));
  await mount(<AcademicPage/>); await click('批量导入');
  const textarea = container.querySelector('textarea')!;
  textarea.value = 'o1,r1,2026-10-20T08:00:00+08:00,2026-10-20T09:00:00+08:00';
  await submit();
  expect(h.importAcademic).toHaveBeenCalledWith([{ offering_id: 'o1', classroom_id: 'r1', starts_at: '2026-10-20T00:00:00.000Z', ends_at: '2026-10-20T01:00:00.000Z' }]);
  expect(container.textContent).toContain('冲突资源：教师'); expect(container.textContent).toContain('导入行：2');
  expect(container.querySelector('textarea')?.value).toBe(textarea.value);
});

it('blocks reversed import intervals before making a request', async () => {
  await mount(<AcademicPage/>); await click('批量导入');
  container.querySelector('textarea')!.value = 'o1,r1,2026-10-20T09:00:00+08:00,2026-10-20T08:00:00+08:00';
  await submit(); expect(h.importAcademic).not.toHaveBeenCalled(); expect(container.textContent).toContain('结束时间必须晚于开始时间');
});

it('does not offer school-wide term or classroom writes to academic admins', async () => {
  h.auth.me!.roles = [{ id: 'binding', role_code: 'academic_admin', scope_org_id: 'college' }];
  h.m1.mockImplementation(async kind => ({ items: kind === 'terms' ? [{ id: 't1', code: 'T1', name: '学期', start_date: '2026-09-01', end_date: '2027-01-01' }] : [], next_cursor: null }));
  await mount(<AcademicPage/>); await click('学期');
  expect(container.textContent).not.toContain('新增学期'); expect([...container.querySelectorAll('button')].some(b => b.textContent === '编辑')).toBe(false);
  await click('课程'); expect(container.textContent).toContain('新增课程');
});

it('loads all academic pages, exports the final row, filters the week, and clears them on logout', async () => {
  const rows = Array.from({ length: 101 }, (_, index) => ({ id: `schedule-${index}`, offering_id: 'offering', classroom_id: 'room', teacher_id: user.id, class_group_id: 'group', starts_at: '2026-10-16T00:00:00Z', ends_at: '2026-10-16T01:00:00Z', status: index === 100 ? 'last-row' : 'active' }));
  h.m1.mockImplementation(async (kind: string, cursor?: string) => {
    if (kind === 'schedules') return { items: cursor ? rows.slice(100) : rows.slice(0, 100), next_cursor: cursor ? null : 'page-2' };
    if (kind === 'courses') return { items: [{ id: 'course', name: '私有课程' }], next_cursor: null };
    if (kind === 'offerings') return { items: [{ id: 'offering', course_id: 'course', code: 'C1' }], next_cursor: null };
    return { items: [], next_cursor: null };
  });
  window.location.hash = '#/academic'; await mount(<App/>);
  expect(container.querySelectorAll('tbody tr')).toHaveLength(101);
  const calls = h.m1.mock.calls.filter(call => call[0] === 'schedules');
  expect(calls).toHaveLength(2); expect(calls[1]?.[1]).toBe('page-2');
  const range = calls[0]![2] as { from: string; to: string };
  expect(new Date(range.from).getDay()).toBe(1); expect(new Date(range.to).getDay()).toBe(1);
  expect(calls[1]?.[2]).toEqual(range);
  let csvBlob: Blob | undefined;
  const create = vi.fn((blob: Blob) => { csvBlob = blob; return 'blob:export-test'; });
  Object.defineProperty(URL, 'createObjectURL', { value: create, configurable: true });
  Object.defineProperty(URL, 'revokeObjectURL', { value: vi.fn(), configurable: true });
  const clickAnchor = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  try {
    await click('导出课表 CSV');
    const csv = await new Promise<string>((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.onerror = reject; reader.readAsText(csvBlob!); });
    expect(csv).toContain('last-row'); expect(csv.split('\r\n')).toHaveLength(102);
  } finally { clickAnchor.mockRestore(); }
  h.auth = { ...h.auth, status: 'anonymous', me: null, capabilities: [], sessionKey: 'anonymous#2' };
  await mount(<App/>); expect(container.textContent).not.toContain('私有课程'); expect(container.querySelectorAll('tbody tr')).toHaveLength(0);
});
