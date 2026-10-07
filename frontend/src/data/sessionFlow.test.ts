import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { clearCsrfToken, currentCsrfToken, setCsrfToken } from './http';
import { SIGNED_OUT, bootstrapSession, sessionKeyOf, signIn, signOut } from './sessionFlow';
import type { SessionState } from './sessionFlow';
import type { AccountMe } from './authApi';

interface Call {
  url: string;
  init: RequestInit | undefined;
}

let calls: Call[] = [];

const ME: AccountMe = {
  user_id: '00000000-0000-4000-8000-000000000001',
  display_name: '任课教师示例',
  roles: [{ id: '00000000-0000-4000-8000-000000000002', role_code: 'teacher', scope_org_id: null }],
  allowed_actions: ['upload', 'analyze'],
};

function respond(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => (body === undefined ? '' : JSON.stringify(body)),
  } as unknown as Response;
}

function ok(data: unknown): Response {
  return respond(200, { data, request_id: 'req' });
}

function fail(status: number, code: string): Response {
  return respond(status, { error: { code, message: 'synthetic', details: {} }, request_id: 'req' });
}

function stubRoutes(routes: Record<string, () => Response>): void {
  vi.stubGlobal('fetch', async (url: unknown, init?: RequestInit) => {
    const target = String(url);
    calls.push({ url: target, init });
    const handler = routes[target];
    if (handler === undefined) {
      throw new Error(`测试未定义的路由：${target}`);
    }
    return handler();
  });
}

const SIGNED_IN: SessionState = { status: 'authenticated', me: ME, error: null };

beforeEach(() => {
  calls = [];
  clearCsrfToken();
  // 已登录状态下内存里本来就有令牌；下面个别用例会覆盖成"过期令牌"。
  setCsrfToken('session-token');
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('会话标识', () => {
  it('登录后按用户区分，退出/未登录按状态区分', () => {
    expect(sessionKeyOf(SIGNED_IN)).toBe(`user:${ME.user_id}`);
    expect(sessionKeyOf(SIGNED_OUT)).toBe('anonymous');
    expect(sessionKeyOf({ status: 'checking', me: null, error: null })).toBe('checking');
    expect(sessionKeyOf({ status: 'offline', me: null, error: null })).toBe('offline');
  });

  it('换账号会得到不同的 key，页面据此重新取数', () => {
    const other: SessionState = { status: 'authenticated', me: { ...ME, user_id: 'other-user' }, error: null };
    expect(sessionKeyOf(other)).not.toBe(sessionKeyOf(SIGNED_IN));
  });
});

describe('登录（评审意见 P2：令牌失效后要重新获取）', () => {
  it('每次登录前都重新取 CSRF，即使内存里已经有令牌', async () => {
    stubRoutes({
      '/api/v1/auth/csrf': () => ok({ csrf_token: 'fresh-token' }),
      '/api/v1/auth/login': () => ok({ user_id: ME.user_id, roles: ME.roles, csrf_token: 'session-token' }),
      '/api/v1/auth/me': () => ok(ME),
    });

    // 预先放一个"看起来有效"的旧令牌，模拟临时 Cookie 过期/被其它标签页轮换过。
    setCsrfToken('stale-token');

    const next = await signIn('teacher_demo', 'password');

    expect(next.status).toBe('authenticated');
    expect(calls.map((call) => call.url)).toEqual([
      '/api/v1/auth/csrf',
      '/api/v1/auth/login',
      '/api/v1/auth/me',
    ]);
    const headers = calls[1]?.init?.headers as Record<string, string>;
    expect(headers['X-CSRF-Token']).toBe('fresh-token');
  });

  it('登录失败（含 403）会清掉旧令牌，下一次点击能重新获取', async () => {
    stubRoutes({
      '/api/v1/auth/csrf': () => ok({ csrf_token: 'fresh-token' }),
      '/api/v1/auth/login': () => fail(403, 'FORBIDDEN'),
    });

    const next = await signIn('teacher_demo', 'password');

    expect(next.status).toBe('anonymous');
    expect(next.error).toContain('权限');
    expect(currentCsrfToken()).toBeNull();
  });
});

describe('退出登录（评审意见 P2：撤销失败不能显示成已退出）', () => {
  it('撤销成功才回到未登录状态', async () => {
    stubRoutes({
      '/api/v1/auth/logout': () => respond(204, undefined),
      '/api/v1/auth/csrf': () => ok({ csrf_token: 'pre-auth' }),
    });

    const next = await signOut(SIGNED_IN);

    expect(next.status).toBe('anonymous');
    expect(next.me).toBeNull();
    expect(calls.map((call) => call.url)).toEqual(['/api/v1/auth/logout', '/api/v1/auth/csrf']);
  });

  it('服务端 500 时保留已登录状态与可重试的错误提示', async () => {
    stubRoutes({
      '/api/v1/auth/logout': () => fail(500, 'SERVICE_UNAVAILABLE'),
    });

    const next = await signOut(SIGNED_IN);

    expect(next.status).toBe('authenticated');
    expect(next.me).toEqual(ME);
    expect(next.error).toContain('服务暂时不可用');
    // 没有去清理令牌：会话可能仍然有效，不能假装退出完成。
    expect(calls.map((call) => call.url)).toEqual(['/api/v1/auth/logout']);
  });

  it('网络错误同样保留已登录状态', async () => {
    vi.stubGlobal('fetch', async () => {
      throw new TypeError('fetch failed');
    });

    const next = await signOut(SIGNED_IN);

    expect(next.status).toBe('authenticated');
    expect(next.error).toContain('连接不上服务端');
  });

  it('服务端明确回应会话已失效时，按已退出处理', async () => {
    stubRoutes({
      '/api/v1/auth/logout': () => fail(401, 'UNAUTHENTICATED'),
      '/api/v1/auth/csrf': () => ok({ csrf_token: 'pre-auth' }),
    });

    const next = await signOut(SIGNED_IN);

    expect(next.status).toBe('anonymous');
    expect(next.me).toBeNull();
  });
});

describe('启动时恢复会话', () => {
  it('已登录时返回 authenticated 与账号信息', async () => {
    stubRoutes({
      '/api/v1/auth/csrf': () => ok({ csrf_token: 'token' }),
      '/api/v1/auth/me': () => ok(ME),
    });
    const state = await bootstrapSession();
    expect(state.status).toBe('authenticated');
    expect(state.me?.allowed_actions).toEqual(['upload', 'analyze']);
  });

  it('未登录是正常状态，不当作错误提示', async () => {
    stubRoutes({
      '/api/v1/auth/csrf': () => ok({ csrf_token: 'token' }),
      '/api/v1/auth/me': () => fail(401, 'UNAUTHENTICATED'),
    });
    const state = await bootstrapSession();
    expect(state.status).toBe('anonymous');
    expect(state.error).toBeNull();
  });

  it('其它失败（例如网络）会给出可读提示', async () => {
    stubRoutes({
      '/api/v1/auth/csrf': () => ok({ csrf_token: 'token' }),
      '/api/v1/auth/me': () => fail(503, 'SERVICE_UNAVAILABLE'),
    });
    const state = await bootstrapSession();
    expect(state.status).toBe('anonymous');
    expect(state.error).toContain('服务暂时不可用');
  });
});
