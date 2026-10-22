// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ApiError } from '../data/errors';
const request = vi.hoisted(() => vi.fn());
vi.mock('../data/http', () => ({ requestJson: request }));
import { DeleteUnusedButton, PasswordForm } from './ManagementActions';

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;
beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true); request.mockReset(); request.mockResolvedValue({ data: null });
  container = document.createElement('div'); document.body.append(container); root = createRoot(container);
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function fill(label: string, value: string): Promise<void> {
  const input = [...container.querySelectorAll('label')].find(l => l.textContent?.startsWith(label))!.querySelector('input')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
async function submit(): Promise<void> { await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); }); }

it('取消删除不发送请求，被引用失败保留记录并显示原因', async () => {
  const done = vi.fn(); const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
  await act(async () => root.render(<DeleteUnusedButton resource="class-groups" id="group" name="班级" onDeleted={done}/>));
  await act(async () => container.querySelector('button')!.click()); expect(request).not.toHaveBeenCalled();
  confirm.mockReturnValue(true); request.mockRejectedValue(new ApiError({ code: 'INVALID_STATE', status: 409, details: { reason: '班级已有课堂，不能删除。' } }));
  await act(async () => container.querySelector('button')!.click());
  expect(request).toHaveBeenCalledWith('/class-groups/group', { method: 'DELETE', csrf: true });
  expect(done).not.toHaveBeenCalled(); expect(container.textContent).toContain('班级已有课堂，不能删除。');
});

it('删除成功后刷新列表', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true); const done = vi.fn();
  await act(async () => root.render(<DeleteUnusedButton resource="org-units" id="college" name="学院" onDeleted={done}/>));
  await act(async () => container.querySelector('button')!.click()); expect(done).toHaveBeenCalledOnce();
});

it('改密须两次一致，提交旧密码，成功后退出账号', async () => {
  const done = vi.fn();
  await act(async () => root.render(<PasswordForm onDone={done} onCancel={() => {}}/>));
  await fill('当前密码', 'old-password-123'); await fill('新密码', 'new-password-456'); await fill('再次输入新密码', 'not-the-same'); await submit();
  expect(request).not.toHaveBeenCalled(); expect(container.textContent).toContain('不一致');
  await fill('再次输入新密码', 'new-password-456'); await submit();
  expect(request).toHaveBeenCalledWith('/auth/password', { method: 'POST', csrf: true, body: { current_password: 'old-password-123', new_password: 'new-password-456' } });
  expect(done).toHaveBeenCalledOnce();
});

it('管理员重置他人不提交旧密码，失败保持表单', async () => {
  request.mockRejectedValue(new ApiError({ code: 'FORBIDDEN', status: 403 })); const done = vi.fn();
  await act(async () => root.render(<PasswordForm userId="teacher" onDone={done} onCancel={() => {}}/>));
  expect(container.textContent).not.toContain('当前密码');
  await fill('新密码', 'reset-password-123'); await fill('再次输入新密码', 'reset-password-123'); await submit();
  expect(request).toHaveBeenCalledWith('/users/teacher/password', { method: 'POST', csrf: true, body: { new_password: 'reset-password-123' } });
  expect(done).not.toHaveBeenCalled(); expect(container.textContent).toContain('权限');
});

it.each([
  ['中文密码', true], ['中'.repeat(24), true], ['🙂'.repeat(3), true], ['🙂'.repeat(18), true],
  ['a'.repeat(12), true], ['a'.repeat(72), true], ['中文密', false], ['🙂'.repeat(2), false],
  ['中'.repeat(25), false], ['🙂'.repeat(19), false], ['a'.repeat(11), false], ['a'.repeat(73), false],
])('新密码按 UTF-8 字节校验：%s', async (password, valid) => {
  await act(async () => root.render(<PasswordForm userId="teacher" onDone={() => {}} onCancel={() => {}}/>));
  await fill('新密码', password); await fill('再次输入新密码', password); await submit();
  expect(request).toHaveBeenCalledTimes(valid ? 1 : 0);
  if (!valid) expect(container.textContent).toContain('12至72字节');
});
