import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { clearCsrfToken, setCsrfToken } from './http';
import {
  createRoleBinding,
  createUser,
  listUsers,
  listRoleBindings,
  deleteRoleBinding,
  parseManagedUser,
  parseRoleBinding,
  patchUser,
} from './identityApi';

/**
 * 账号接口的字段与 Go 的 internal/identity/service.go 对齐：
 * User = id/org_unit_id/username/display_name/status，RoleBinding = id/role_code/scope_org_id。
 * 请求体的字段名（snake_case）和初始密码这类硬规则一并钉住。
 */

interface Call {
  url: string;
  init: RequestInit | undefined;
}

let calls: Call[] = [];

function respond(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => (body === undefined ? '' : JSON.stringify(body)),
  } as unknown as Response;
}

function envelope(data: unknown): Response {
  return respond(200, { data, request_id: 'req-1' });
}

function stub(handler: (url: string) => Response): void {
  vi.stubGlobal('fetch', async (url: unknown, init?: RequestInit) => {
    const target = String(url);
    calls.push({ url: target, init });
    return handler(target);
  });
}

const USER = {
  id: '00000000-0000-4000-8000-0000000000a1',
  org_unit_id: '00000000-0000-4000-8000-0000000000b1',
  username: 'chen.supervisor',
  display_name: '陈知远',
  status: 'active',
};

beforeEach(() => {
  calls = [];
  clearCsrfToken();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('账号 DTO 解析', () => {
  it('状态的取值只有 active / disabled，org_unit_id 允许为 null', () => {
    expect(parseManagedUser(USER).displayName).toBe('陈知远');
    expect(parseManagedUser({ ...USER, org_unit_id: null }).orgUnitId).toBeNull();
    expect(() => parseManagedUser({ ...USER, status: 'locked' })).toThrow(/status/);
  });

  it('角色绑定只接受四种固定角色', () => {
    expect(parseRoleBinding({ id: 'r1', role_code: 'supervisor', scope_org_id: null }).roleCode).toBe(
      'supervisor',
    );
    expect(() =>
      parseRoleBinding({ id: 'r1', role_code: 'principal', scope_org_id: null }),
    ).toThrow(/role_code/);
  });
});

describe('账号读写', () => {
  it('读取已有绑定按页解析，撤销携带 CSRF 并接受204', async () => {
    setCsrfToken('role-csrf');
    stub(url => url.includes('/role-bindings/') ? respond(204, undefined) : envelope({items: [{id: 'binding', role_code: 'teacher', scope_org_id: null}], next_cursor: 'next-binding'}));
    const page = await listRoleBindings('user A', 'previous');
    expect(page.items[0]?.roleCode).toBe('teacher');
    expect(page.nextCursor).toBe('next-binding');
    expect(calls[0]?.url).toContain('/users/user%20A/role-bindings?');
    expect(calls[0]?.url).toContain('cursor=previous');
    await deleteRoleBinding('user A', 'binding');
    expect(calls[1]?.init?.method).toBe('DELETE');
    expect((calls[1]?.init?.headers as Record<string,string>)['X-CSRF-Token']).toBe('role-csrf');
  });
  it('列表带游标与 limit，解析 items 与 next_cursor', async () => {
    stub((url) => {
      expect(url).toContain('/api/v1/users');
      expect(url).toContain('limit=100');
      return envelope({ items: [USER], next_cursor: null });
    });
    const page = await listUsers();
    expect(page.items[0]?.username).toBe('chen.supervisor');
    expect(page.nextCursor).toBeNull();
  });

  it('新增账号把初始密码放在 initial_password，并携带 CSRF', async () => {
    setCsrfToken('csrf-9');
    stub(() => respond(201, { data: USER, request_id: 'req-2' }));
    await createUser({
      orgUnitId: USER.org_unit_id,
      username: 'chen.supervisor',
      displayName: '陈知远',
      status: 'active',
      initialPassword: 'local-password-2026',
    });
    expect(calls[0]?.init?.method).toBe('POST');
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({
      org_unit_id: USER.org_unit_id,
      username: 'chen.supervisor',
      display_name: '陈知远',
      status: 'active',
      initial_password: 'local-password-2026',
    });
    expect((calls[0]?.init?.headers as Record<string, string>)['X-CSRF-Token']).toBe('csrf-9');
  });

  it('编辑账号只发显示名与状态，不动所属学院', async () => {
    setCsrfToken('csrf-9');
    stub(() => envelope({ ...USER, status: 'disabled' }));
    const updated = await patchUser(USER.id, { displayName: '陈知远', status: 'disabled' });
    expect(calls[0]?.init?.method).toBe('PATCH');
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({
      display_name: '陈知远',
      status: 'disabled',
    });
    expect(updated.status).toBe('disabled');
  });

  it('授予角色用 POST .../role-bindings，scope_org_id 可为 null', async () => {
    setCsrfToken('csrf-9');
    stub(() => respond(201, { data: { id: 'b1', role_code: 'supervisor', scope_org_id: null }, request_id: 'r3' }));
    const binding = await createRoleBinding(USER.id, 'supervisor', null);
    expect(calls[0]?.url).toContain(`/api/v1/users/${USER.id}/role-bindings`);
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({ role_code: 'supervisor', scope_org_id: null });
    expect(binding.roleCode).toBe('supervisor');
  });

  it('服务端 403 原样变成可显示的 ApiError', async () => {
    stub(() =>
      respond(403, {
        error: { code: 'FORBIDDEN', message: 'user management is not allowed', details: {} },
        request_id: 'r4',
      }),
    );
    await expect(listUsers()).rejects.toThrow(/user management is not allowed/);
  });
});
