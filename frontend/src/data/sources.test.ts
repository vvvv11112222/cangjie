import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './errors';
import { fixtures } from './samples';
import {
  apiSource,
  dataSource,
  fixtureSource,
  getResults,
  getRun,
  getSessions,
  isApiMode,
  resolveDataSourceKind,
} from './sources';

function respond(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(body),
  } as unknown as Response;
}

function stubFetch(handler: (url: string) => Response): string[] {
  const urls: string[] = [];
  vi.stubGlobal('fetch', async (url: unknown) => {
    const target = String(url);
    urls.push(target);
    return handler(target);
  });
  return urls;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('数据源开关', () => {
  it('只有明确写成 api 才走接口，其余一律回落到固定样例', () => {
    expect(resolveDataSourceKind('api')).toBe('api');
    expect(resolveDataSourceKind('fixtures')).toBe('fixtures');
    expect(resolveDataSourceKind(undefined)).toBe('fixtures');
    expect(resolveDataSourceKind('API')).toBe('fixtures');
    expect(resolveDataSourceKind('')).toBe('fixtures');
  });

  it('默认（测试环境未设 VITE_DATA_SOURCE）走样例，五个入口和 dataSource 一致', async () => {
    expect(isApiMode()).toBe(false);
    await expect(getSessions()).resolves.toEqual(fixtures.sessionPage);
    await expect(getRun(fixtures.run.id)).resolves.toEqual(fixtures.run);
    await expect(getResults(fixtures.run.id)).resolves.toEqual(fixtures.results);
    expect(dataSource.kind).toBe('fixtures');
    expect(dataSource.getSessions).toBe(getSessions);
  });
});

describe('固定样例实现', () => {
  it('按 id 返回样例，五个入口都可用', async () => {
    await expect(fixtureSource.getSessions()).resolves.toEqual(fixtures.sessionPage);
    await expect(fixtureSource.getRun(fixtures.run.id)).resolves.toEqual(fixtures.run);
    await expect(fixtureSource.getResults(fixtures.run.id)).resolves.toEqual(fixtures.results);
    await expect(fixtureSource.getRevision(fixtures.revision.id)).resolves.toEqual(fixtures.revision);
    await expect(fixtureSource.getReport(fixtures.report.id)).resolves.toEqual(fixtures.report);
  });

  it('id 对不上时给 NOT_FOUND，不返回别的样例充数', async () => {
    const error = (await fixtureSource.getRun('不存在的批次').catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('NOT_FOUND');
  });

  it('样例模式下媒体列表为空（不使用媒体选择器，评审意见 F02）', async () => {
    await expect(fixtureSource.getSessionMedia(fixtures.sessionPage.items[0]!.id)).resolves.toEqual({
      items: [],
      nextCursor: null,
    });
  });

  it('results 引用的修订 id 与 revision.json 不同，两个 id 都指向同一份修订样例', async () => {
    expect(fixtures.results.transcript_revision_id).not.toBe(fixtures.revision.id);
    await expect(fixtureSource.getRevision(fixtures.results.transcript_revision_id!)).resolves.toEqual(
      fixtures.revision,
    );
  });
});

describe('接口实现', () => {
  it('按协议路径请求并解析响应', async () => {
    const urls = stubFetch((url) => {
      if (url.startsWith('/api/v1/sessions?')) {
        return respond(200, { data: fixtures.sessionPage, request_id: 'r1' });
      }
      if (url === `/api/v1/analysis-runs/${fixtures.run.id}`) {
        return respond(200, { data: fixtures.run, request_id: 'r2' });
      }
      return respond(200, { data: fixtures.report, request_id: 'r3' });
    });

    await apiSource.getSessions({ college_id: 'c1', limit: 20, status: 'ready' });
    await apiSource.getRun(fixtures.run.id);

    expect(urls[0]).toBe('/api/v1/sessions?college_id=c1&limit=20&status=ready');
    expect(urls[1]).toBe(`/api/v1/analysis-runs/${fixtures.run.id}`);
  });

  it('后端返回的结构不符合协议时判为 MALFORMED_RESPONSE', async () => {
    stubFetch(() => respond(200, { data: { id: '只有 id' }, request_id: 'r1' }));
    const error = (await apiSource.getReport(fixtures.report.id).catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('MALFORMED_RESPONSE');
  });

  it('后端尚未实现的接口返回 404 时，错误码保持 NOT_FOUND', async () => {
    stubFetch(() =>
      respond(404, {
        error: { code: 'NOT_FOUND', message: 'not implemented', details: {} },
        request_id: 'r1',
      }),
    );
    const error = (await apiSource.getResults(fixtures.run.id).catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('NOT_FOUND');
    expect(error.status).toBe(404);
  });

  it('列表信封形状不对（next_cursor 不是字符串/null）时判为 MALFORMED_RESPONSE', async () => {
    stubFetch(() => respond(200, { data: { items: [], next_cursor: 3 }, request_id: 'r1' }));
    const error = (await apiSource.getSessions().catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('MALFORMED_RESPONSE');
    expect(error.message).toContain('next_cursor');
  });

  it('媒体列表保留 allowed_actions 与游标（评审意见 N04/N05）', async () => {
    const urls = stubFetch(() =>
      respond(200, {
        data: {
          items: [
            { id: 'media-1', kind: 'source', status: 'ready', allowed_actions: ['analyze', 'select_primary'] },
            { id: 'media-2', kind: 'proxy', status: 'pending', allowed_actions: ['playback'] },
          ],
          next_cursor: 'cursor-2',
        },
        request_id: 'r1',
      }),
    );

    await expect(apiSource.getSessionMedia('session-1')).resolves.toEqual({
      items: [
        { id: 'media-1', kind: 'source', status: 'ready', allowed_actions: ['analyze', 'select_primary'] },
        { id: 'media-2', kind: 'proxy', status: 'pending', allowed_actions: ['playback'] },
      ],
      nextCursor: 'cursor-2',
    });
    expect(urls[0]).toBe('/api/v1/sessions/session-1/media');

    // 继续读取时游标要带进查询参数（N04）。
    await apiSource.getSessionMedia('session-1', 'cursor-2');
    expect(urls[1]).toBe('/api/v1/sessions/session-1/media?cursor=cursor-2');
  });

  it('媒体 kind/status 不在协议枚举里时判为 MALFORMED_RESPONSE', async () => {
    stubFetch(() =>
      respond(200, {
        data: { items: [{ id: 'm', kind: 'unknown_kind', status: 'ready', allowed_actions: [] }], next_cursor: null },
        request_id: 'r',
      }),
    );
    const error = (await apiSource.getSessionMedia('session-1').catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('MALFORMED_RESPONSE');
    expect(error.message).toContain('kind');
  });

  it('媒体 allowed_actions 出现未收录取值时判为 MALFORMED_RESPONSE（评审意见 N05）', async () => {
    stubFetch(() =>
      respond(200, {
        data: {
          items: [{ id: 'm', kind: 'source', status: 'ready', allowed_actions: ['delete_everything'] }],
          next_cursor: null,
        },
        request_id: 'r',
      }),
    );
    const error = (await apiSource.getSessionMedia('session-1').catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('MALFORMED_RESPONSE');
    expect(error.message).toContain('allowed_actions');
  });
});
