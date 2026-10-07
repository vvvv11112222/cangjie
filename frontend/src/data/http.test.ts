import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './errors';
import { apiPath, clearCsrfToken, currentCsrfToken, requestJson, setCsrfToken } from './http';

interface Call {
  url: string;
  init: RequestInit | undefined;
}

let calls: Call[] = [];

function respond(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(body),
  } as unknown as Response;
}

function stubFetch(handler: (url: string, init: RequestInit | undefined) => Response): void {
  vi.stubGlobal('fetch', async (url: unknown, init?: RequestInit) => {
    const target = String(url);
    calls.push({ url: target, init });
    return handler(target, init);
  });
}

beforeEach(() => {
  calls = [];
  clearCsrfToken();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('统一响应处理', () => {
  it('成功响应取出 data，并保留 request_id', async () => {
    stubFetch(() => respond(200, { data: { ok: 1 }, request_id: 'req-1' }));
    const envelope = await requestJson<{ ok: number }>('/sessions');
    expect(envelope.data).toEqual({ ok: 1 });
    expect(envelope.requestId).toBe('req-1');
    expect(calls[0]?.url).toBe('/api/v1/sessions');
  });

  it('错误信封转成 ApiError，保留 code、details 和 request_id', async () => {
    stubFetch(() =>
      respond(409, {
        error: { code: 'REVISION_CONFLICT', message: 'synthetic', details: { current_lock_version: 2 } },
        request_id: 'req-2',
      }),
    );
    const error = await requestJson('/transcript-revisions/x').catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ApiError);
    const apiError = error as ApiError;
    expect(apiError.code).toBe('REVISION_CONFLICT');
    expect(apiError.status).toBe(409);
    expect(apiError.details).toEqual({ current_lock_version: 2 });
    expect(apiError.requestId).toBe('req-2');
  });

  it('响应不是 JSON 时判为 MALFORMED_RESPONSE，不把原文当数据用', async () => {
    stubFetch(
      () =>
        ({
          ok: true,
          status: 200,
          text: async () => '<html>网关错误页</html>',
        }) as unknown as Response,
    );
    const error = (await requestJson('/sessions').catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('MALFORMED_RESPONSE');
  });

  it('连不上服务端时判为 NETWORK_ERROR', async () => {
    vi.stubGlobal('fetch', async () => {
      throw new TypeError('fetch failed');
    });
    const error = (await requestJson('/sessions').catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('NETWORK_ERROR');
  });

  it('204 等无 JSON 包装的响应返回空 data', async () => {
    stubFetch(
      () =>
        ({
          ok: true,
          status: 204,
          text: async () => '',
        }) as unknown as Response,
    );
    const envelope = await requestJson('/auth/logout', { method: 'POST', body: {} });
    expect(envelope.data).toBeUndefined();
  });
});

describe('CSRF 与查询参数', () => {
  it('没有令牌时不发送写请求，直接判为 FORBIDDEN', async () => {
    stubFetch(() => respond(200, { data: {}, request_id: 'req' }));
    const error = (await requestJson('/auth/login', { method: 'POST', csrf: true }).catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('FORBIDDEN');
    expect(calls).toHaveLength(0);
  });

  it('写请求带上 X-CSRF-Token 和 JSON Content-Type', async () => {
    setCsrfToken('token-1');
    stubFetch(() => respond(200, { data: {}, request_id: 'req' }));
    await requestJson('/auth/login', { method: 'POST', body: { username: 'a', password: 'b' }, csrf: true });
    const headers = calls[0]?.init?.headers as Record<string, string>;
    expect(headers['X-CSRF-Token']).toBe('token-1');
    expect(headers['Content-Type']).toBe('application/json');
    expect(currentCsrfToken()).toBe('token-1');
  });

  it('apiPath 跳过空的筛选参数，其余按原值编码', () => {
    expect(apiPath('/sessions', { college_id: 'c1', cursor: undefined, status: '', limit: 20 })).toBe(
      '/api/v1/sessions?college_id=c1&limit=20',
    );
    expect(apiPath('sessions')).toBe('/api/v1/sessions');
  });
});
