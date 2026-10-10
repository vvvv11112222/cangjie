import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cancelRun, createRun, newIdempotencyKey, uploadMedia } from './actions';
import { ApiError } from './errors';
import { clearCsrfToken, setCsrfToken } from './http';
import runPartialJson from '../../../contracts/examples/run-partial.json';

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

const SESSION_ID = runPartialJson.session_id;
const RUN_ID = runPartialJson.id;

beforeEach(() => {
  calls = [];
  clearCsrfToken();
  setCsrfToken('session-token');
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('动作请求', () => {
  it('提交分析发送 CreateRun 并带 CSRF 与 Idempotency-Key', async () => {
    stubFetch((url, init) => {
      expect(url).toBe(`/api/v1/sessions/${SESSION_ID}/analysis-runs`);
      const headers = init?.headers as Record<string, string>;
      expect(headers['X-CSRF-Token']).toBe('session-token');
      expect(headers['Idempotency-Key']).toBeTruthy();
      expect(init?.body).toBe(
        JSON.stringify({
          media_asset_id: runPartialJson.media_asset_id,
          mode: 'full',
          input_transcript_revision_id: null,
          config_profile: 'p0-v1',
        }),
      );
      return respond(202, { data: runPartialJson, request_id: 'r' });
    });

    const run = await createRun(SESSION_ID, {
      media_asset_id: runPartialJson.media_asset_id,
      mode: 'full',
      input_transcript_revision_id: null,
      config_profile: 'p0-v1',
    });

    expect(run.id).toBe(RUN_ID);
  });

  it('每次提交分析都生成新的幂等键', () => {
    expect(newIdempotencyKey()).not.toBe(newIdempotencyKey());
  });

  it('取消批次发送空请求体并解析 Run', async () => {
    stubFetch((url, init) => {
      expect(url).toBe(`/api/v1/analysis-runs/${RUN_ID}/cancel`);
      expect(init?.body).toBe('{}');
      return respond(200, { data: runPartialJson, request_id: 'r' });
    });
    await expect(cancelRun(RUN_ID)).resolves.toMatchObject({ id: RUN_ID });
  });

  it('上传媒体用 multipart，并带上文件与来源 id', async () => {
    const file = new File(['x'], 'a.mp4', { type: 'video/mp4' });
    stubFetch((url, init) => {
      expect(url).toBe(`/api/v1/sessions/${SESSION_ID}/media`);
      expect(init?.body).toBeInstanceOf(FormData);
      const form = init?.body as FormData;
      expect(form.get('source_record_id')).toBe('src-1');
      expect(form.get('file')).toBeInstanceOf(File);
      return respond(201, { data: { id: 'media-1' }, request_id: 'r' });
    });
    await expect(uploadMedia(SESSION_ID, file, 'src-1')).resolves.toEqual({ mediaId: 'media-1' });
  });

  it('后端返回错误时保持错误类别', async () => {
    stubFetch(() =>
      respond(404, { error: { code: 'NOT_FOUND', message: '未实现', details: {} }, request_id: 'r' }),
    );
    const error = (await cancelRun(RUN_ID).catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('NOT_FOUND');
    expect(error.status).toBe(404);
  });
});
