import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, describeError } from './errors';
import { clearCsrfToken, currentCsrfToken } from './http';
import { fetchCsrfToken, fetchMe, login, logout } from './authApi';

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

/** 按路径返回响应，模拟本地 Go 服务的登录链路。 */
function stubRoutes(routes: Record<string, () => Response>): void {
  vi.stubGlobal('fetch', async (url: unknown, init?: RequestInit) => {
    const target = String(url);
    calls.push({ url: target, init });
    const route = routes[target];
    if (route === undefined) {
      throw new Error(`测试未定义的路由：${target}`);
    }
    return route();
  });
}

const ME = {
  user_id: '00000000-0000-4000-8000-000000000001',
  display_name: '任课教师示例',
  roles: [{ id: '00000000-0000-4000-8000-000000000002', role_code: 'teacher', scope_org_id: null }],
  allowed_actions: ['upload', 'analyze'],
};

beforeEach(() => {
  calls = [];
  clearCsrfToken();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('登录鉴权链路', () => {
  it('csrf → 带 X-CSRF-Token 登录 → me，并使用轮换后的令牌', async () => {
    stubRoutes({
      '/api/v1/auth/csrf': () => respond(200, { data: { csrf_token: 'pre-auth' }, request_id: 'r1' }),
      '/api/v1/auth/login': () =>
        respond(200, {
          data: { user_id: ME.user_id, roles: ME.roles, csrf_token: 'session-token' },
          request_id: 'r2',
        }),
      '/api/v1/auth/me': () => respond(200, { data: ME, request_id: 'r3' }),
    });

    const token = await fetchCsrfToken();
    expect(token).toBe('pre-auth');

    const outcome = await login('teacher_demo', 'password');
    expect(outcome.userId).toBe(ME.user_id);

    const me = await fetchMe();
    expect(me.display_name).toBe('任课教师示例');
    expect(me.allowed_actions).toEqual(['upload', 'analyze']);

    // 调用顺序必须是 csrf → login → me。
    expect(calls.map((call) => call.url)).toEqual([
      '/api/v1/auth/csrf',
      '/api/v1/auth/login',
      '/api/v1/auth/me',
    ]);

    const loginHeaders = calls[1]?.init?.headers as Record<string, string>;
    expect(loginHeaders['X-CSRF-Token']).toBe('pre-auth');
    expect(calls[1]?.init?.body).toBe(JSON.stringify({ username: 'teacher_demo', password: 'password' }));
    // 登录成功会轮换令牌，后续写请求必须用新的。
    expect(currentCsrfToken()).toBe('session-token');
  });

  it('账号密码不对时返回 UNAUTHENTICATED，并给出中文提示', async () => {
    stubRoutes({
      '/api/v1/auth/csrf': () => respond(200, { data: { csrf_token: 'pre-auth' }, request_id: 'r1' }),
      '/api/v1/auth/login': () =>
        respond(401, {
          error: { code: 'UNAUTHENTICATED', message: 'invalid credentials', details: {} },
          request_id: 'r2',
        }),
    });
    await fetchCsrfToken();
    const error = (await login('teacher_demo', 'wrong').catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('UNAUTHENTICATED');
    expect(describeError(error)).toContain('登录状态已失效');
    expect(describeError(error)).toContain('UNAUTHENTICATED');
  });

  it('Origin 或 CSRF 校验失败时返回 FORBIDDEN，提示里说明是权限问题', async () => {
    stubRoutes({
      '/api/v1/auth/csrf': () => respond(200, { data: { csrf_token: 'pre-auth' }, request_id: 'r1' }),
      '/api/v1/auth/login': () =>
        respond(403, {
          error: { code: 'FORBIDDEN', message: 'request origin is not allowed', details: {} },
          request_id: 'r2',
        }),
    });
    await fetchCsrfToken();
    const error = (await login('teacher_demo', 'password').catch((reason: unknown) => reason)) as ApiError;
    expect(describeError(error)).toContain('没有执行该操作的权限');
  });

  it('me 响应缺字段时判为 MALFORMED_RESPONSE，并指出字段位置', async () => {
    stubRoutes({
      '/api/v1/auth/me': () =>
        respond(200, { data: { ...ME, allowed_actions: ['not_a_real_action'] }, request_id: 'r3' }),
    });
    const error = (await fetchMe().catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('MALFORMED_RESPONSE');
    expect(error.message).toContain('allowed_actions');
  });

  it('退出登录发送 204 空响应也不报错', async () => {
    stubRoutes({
      '/api/v1/auth/csrf': () => respond(200, { data: { csrf_token: 'pre-auth' }, request_id: 'r1' }),
      '/api/v1/auth/logout': () => respond(204, undefined),
    });
    await fetchCsrfToken();
    await expect(logout()).resolves.toBeUndefined();
  });
});
