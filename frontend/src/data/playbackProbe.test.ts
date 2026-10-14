import { afterEach, describe, expect, it, vi } from 'vitest';
import { probePlaybackAddress } from './playbackProbe';

interface Call {
  url: string;
  range: string | null;
  credentials: RequestCredentials | undefined;
}

let calls: Call[] = [];

function stubFetch(handler: () => Response): void {
  vi.stubGlobal('fetch', async (url: unknown, init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    calls.push({ url: String(url), range: headers.get('Range'), credentials: init?.credentials });
    return handler();
  });
}

function respond(status: number, body: string): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => body,
  } as unknown as Response;
}

afterEach(() => {
  calls = [];
  vi.unstubAllGlobals();
});

const URL = '/api/v1/media/00000000-0000-4000-8000-000000000004/content';

describe('播放地址预检', () => {
  it('206 表示服务端支持 Range，可播放且能跳转', async () => {
    stubFetch(() => respond(206, ''));
    const result = await probePlaybackAddress(URL);
    expect(result).toEqual({ kind: 'ok', status: 206 });
    // 只取 1 个字节，不去读整段媒体。
    expect(calls[0]).toEqual({ url: URL, range: 'bytes=0-0', credentials: 'same-origin' });
  });

  it('200 也算可播放（静态服务不支持 Range 时会这样返回）', async () => {
    stubFetch(() => respond(200, ''));
    expect(await probePlaybackAddress(URL)).toEqual({ kind: 'ok', status: 200 });
  });

  it('416 按协议错误码转成 RANGE_NOT_SATISFIABLE，并保留 HTTP 状态', async () => {
    stubFetch(() =>
      respond(
        416,
        JSON.stringify({
          error: { code: 'RANGE_NOT_SATISFIABLE', message: 'invalid range', details: {} },
          request_id: 'req-416',
        }),
      ),
    );
    const result = await probePlaybackAddress(URL);
    expect(result.kind).toBe('failed');
    if (result.kind === 'failed') {
      expect(result.error.code).toBe('RANGE_NOT_SATISFIABLE');
      expect(result.error.status).toBe(416);
      expect(result.error.requestId).toBe('req-416');
    }
  });

  it('403/410 这类权限与过期错误原样带回错误码', async () => {
    stubFetch(() =>
      respond(410, JSON.stringify({ error: { code: 'RESOURCE_EXPIRED', message: 'gone', details: {} } })),
    );
    const result = await probePlaybackAddress(URL);
    expect(result.kind === 'failed' ? result.error.code : null).toBe('RESOURCE_EXPIRED');
  });

  it('响应不是协议错误信封时降级为 MALFORMED_RESPONSE，但保留状态码', async () => {
    stubFetch(() => respond(503, '<html>gateway</html>'));
    const result = await probePlaybackAddress(URL);
    expect(result.kind === 'failed' ? result.error.code : null).toBe('MALFORMED_RESPONSE');
    expect(result.kind === 'failed' ? result.error.status : null).toBe(503);
  });

  it('请求根本没发出去时是 NETWORK_ERROR', async () => {
    vi.stubGlobal('fetch', async () => {
      throw new Error('offline');
    });
    const result = await probePlaybackAddress(URL);
    expect(result.kind === 'failed' ? result.error.code : null).toBe('NETWORK_ERROR');
  });
});
