import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, describeError } from './errors';
import { clearCsrfToken } from './http';
import {
  isRunActive,
  MAX_DRAFT_RUN_LOOKUPS,
  loadReportForSession,
  loadReportView,
  loadSessionDetail,
  loadSessionTasks,
  loadTranscriptForSession,
  loadTranscriptView,
} from './loaders';
import { apiSource } from './sources';
import { SearchPaused, newSearch } from './search';
import reportDraft from '../../../contracts/examples/report-draft.json';
import resultsJson from '../../../contracts/examples/results.json';
import revisionJson from '../../../contracts/examples/revision.json';
import runPartialJson from '../../../contracts/examples/run-partial.json';
import sessionPageJson from '../../../contracts/examples/session-page.json';
import type { Run, Session } from '../types';

/**
 * 把协议样例当作"后端返回的响应"喂给接口实现，验证取数组合顺序、分页和错误处理。
 * 这样即使本机没有 Go 服务，接口模式的整条链路也能被自动检查。
 */
/** 返回 undefined 表示"这条路由不处理该 URL"，交给后面的路由。 */
type Route = (url: string) => Response | undefined;

const BASE_SESSION: Session = sessionPageJson.items[0] as unknown as Session;
const RUN_ID = BASE_SESSION.latest_run_id!;

function json(status: number, data: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () =>
      status >= 200 && status < 300
        ? JSON.stringify({ data, request_id: `req-${status}` })
        : JSON.stringify(data),
  } as unknown as Response;
}

function notFound(): Response {
  return json(404, { error: { code: 'NOT_FOUND', message: '未实现', details: {} }, request_id: 'r-404' });
}

function stubRoutes(routes: Route[]): string[] {
  const urls: string[] = [];
  vi.stubGlobal('fetch', async (url: unknown) => {
    const target = String(url);
    urls.push(target);
    for (const route of routes) {
      const response = route(target);
      if (response !== undefined) {
        return response;
      }
    }
    return notFound();
  });
  return urls;
}

/** 命中路径时返回 data，否则交给后面的路由。 */
function route(path: string, data: unknown): Route {
  return (url) => (url === path ? json(200, data) : undefined);
}

/** 命中路径时返回 404 错误信封。 */
function failRoute(path: string, code = 'NOT_FOUND'): Route {
  return (url) =>
    url === path
      ? json(404, { error: { code, message: '合成失败', details: {} }, request_id: 'r-fail' })
      : undefined;
}

function defaultRoutes(overrides: Route[] = []): Route[] {
  return [
    ...overrides,
    route('/api/v1/sessions', sessionPageJson),
    route(`/api/v1/analysis-runs/${RUN_ID}`, runPartialJson),
    route(`/api/v1/analysis-runs/${RUN_ID}/results`, resultsJson),
    route(`/api/v1/transcript-revisions/${resultsJson.transcript_revision_id}`, revisionJson),
    route(`/api/v1/reports/${reportDraft.id}`, reportDraft),
  ];
}

afterEach(() => {
  vi.unstubAllGlobals();
  clearCsrfToken();
});

describe('接口模式取数组合', () => {
  it('课堂任务页先取列表，再按 latest_run_id 取批次', async () => {
    const urls = stubRoutes(defaultRoutes());
    const loaded = await loadSessionTasks(apiSource);

    expect(urls).toEqual(['/api/v1/sessions', `/api/v1/analysis-runs/${RUN_ID}`]);
    expect(loaded.tasks).toHaveLength(1);
    expect(loaded.tasks[0]?.run?.id).toBe(RUN_ID);
    expect(loaded.tasks[0]?.stages).toMatchObject({ total: 6, succeeded: 4, failed: 1, skipped: 1 });
    expect(loaded.nextCursor).toBeNull();
  });

  it('批次接口失败时保留课堂行，并把原因写进核对提示', async () => {
    stubRoutes(defaultRoutes([failRoute(`/api/v1/analysis-runs/${RUN_ID}`)]));
    const loaded = await loadSessionTasks(apiSource);
    expect(loaded.tasks[0]?.session.id).toBe(BASE_SESSION.id);
    expect(loaded.tasks[0]?.run).toBeNull();
    expect(loaded.tasks[0]?.disclosures.some((item) => item.text.includes('读取失败'))).toBe(true);
  });

  it('列表页保留下一页游标，但不会自己把后续页全取回来（口径交给页面）', async () => {
    const urls = stubRoutes(
      defaultRoutes([
        route('/api/v1/sessions', { items: [BASE_SESSION], next_cursor: 'cursor-page-2' }),
      ]),
    );

    const loaded = await loadSessionTasks(apiSource);

    expect(loaded.nextCursor).toBe('cursor-page-2');
    // 只请求列表页 + 本页每个课堂的批次，没有请求第二页。
    expect(urls).toEqual(['/api/v1/sessions', `/api/v1/analysis-runs/${RUN_ID}`]);
  });

  it('转写页按 列表 → 批次 → 结果 → 修订 的顺序取数', async () => {
    const urls = stubRoutes(defaultRoutes());
    const loaded = await loadTranscriptView(apiSource);

    expect(urls).toEqual([
      '/api/v1/sessions',
      `/api/v1/analysis-runs/${RUN_ID}`,
      `/api/v1/analysis-runs/${RUN_ID}/results`,
      `/api/v1/transcript-revisions/${resultsJson.transcript_revision_id}`,
    ]);
    expect(loaded.view.segments).toHaveLength(1);
    expect(loaded.selection).toContain('合成课堂');
  });

  it('列表带 next_cursor 时继续翻页，直到找到带批次的课堂（评审意见 P2）', async () => {
    const firstPageSession = {
      ...BASE_SESSION,
      id: '00000000-0000-4000-8000-0000000000f1',
      title: '第一页课堂',
      latest_run_id: null,
    };
    const urls = stubRoutes(
      defaultRoutes([
        (url) =>
          url === '/api/v1/sessions'
            ? json(200, { items: [firstPageSession], next_cursor: 'cursor-page-2' })
            : undefined,
        (url) =>
          url === '/api/v1/sessions?cursor=cursor-page-2'
            ? json(200, { items: [BASE_SESSION], next_cursor: null })
            : undefined,
      ]),
    );

    const loaded = await loadTranscriptView(apiSource);

    expect(urls[0]).toBe('/api/v1/sessions');
    expect(urls[1]).toBe('/api/v1/sessions?cursor=cursor-page-2');
    expect(loaded.selection).toContain('合成课堂');
  });

  it('重复游标立即报协议错误，不能当成合法数量上限', async () => {
    const endlessSession = { ...BASE_SESSION, latest_run_id: null };
    const urls = stubRoutes([
      (url) =>
        url.startsWith('/api/v1/sessions')
          ? json(200, { items: [endlessSession], next_cursor: 'next' })
          : undefined,
    ]);

    const error = (await loadTranscriptView(apiSource).catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('MALFORMED_RESPONSE');
    expect(error.message).toContain('重复');
    expect(urls).toHaveLength(2);
  });

  it('报告页取当前发布版报告及其结果', async () => {
    const session = { ...BASE_SESSION, current_report_id: reportDraft.id };
    stubRoutes(defaultRoutes([route('/api/v1/sessions', { items: [session], next_cursor: null })]));
    const loaded = await loadReportView(apiSource);
    expect(loaded.view.report.id).toBe(reportDraft.id);
    expect(loaded.view.unresolvedEvidenceIds).toEqual([]);
    expect(loaded.selection).toContain('当前发布版');
  });

  it('还没有发布版时，用批次 report_id 展示草稿（评审意见 P2）', async () => {
    // 协议：首次分析生成草稿时 Session.current_report_id 仍为 null，草稿 id 在 Run.report_id。
    const draftRun = { ...runPartialJson, report_id: reportDraft.id };
    stubRoutes(defaultRoutes([route(`/api/v1/analysis-runs/${RUN_ID}`, draftRun)]));

    const loaded = await loadReportView(apiSource);

    expect(loaded.view.report.id).toBe(reportDraft.id);
    expect(loaded.view.report.status).toBe('draft');
    expect(loaded.selection).toContain('草稿');
  });

  it('既没有发布版也没有草稿时给出可读提示', async () => {
    const session = { ...BASE_SESSION, latest_run_id: null, current_report_id: null };
    stubRoutes(defaultRoutes([route('/api/v1/sessions', { items: [session], next_cursor: null })]));
    const error = (await loadReportView(apiSource).catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('NOT_FOUND');
    expect(describeError(error)).toContain('报告');
  });

  it('查找草稿时的批次请求有上限，并如实说明只查了前几个课堂', async () => {
    // 21 个课堂都有批次，但批次都没有 report_id → 查完上限（20）就停下并说明。
    const sessions = Array.from({ length: MAX_DRAFT_RUN_LOOKUPS + 1 }, (_, index) => ({
      ...BASE_SESSION,
      id: `00000000-0000-4000-8000-0000000001${String(index).padStart(2, '0')}`,
      title: `课堂 ${index}`,
      latest_run_id: `00000000-0000-4000-8000-0000000002${String(index).padStart(2, '0')}`,
    }));
    const urls = stubRoutes([
      route('/api/v1/sessions', { items: sessions, next_cursor: null }),
      (url) =>
        url.startsWith('/api/v1/analysis-runs/')
          ? json(200, { ...runPartialJson, report_id: null })
          : undefined,
    ]);

    const checkpoint = newSearch();
    const error = await loadReportView(apiSource, checkpoint).catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(SearchPaused);
    expect(checkpoint.index).toBe(MAX_DRAFT_RUN_LOOKUPS);
    // 1 次列表 + 上限次数的批次请求，没有把 21 个课堂全查一遍。
    expect(urls).toHaveLength(MAX_DRAFT_RUN_LOOKUPS + 1);
  });

  it('后端未实现 /sessions 时给出可读提示，而不是空列表', async () => {
    stubRoutes([
      (url) =>
        url === '/api/v1/sessions'
          ? json(404, { error: { code: 'NOT_FOUND', message: 'not implemented', details: {} }, request_id: 'r' })
          : undefined,
    ]);
    const error = (await loadSessionTasks(apiSource).catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('NOT_FOUND');
    expect(describeError(error)).toContain('不存在，或超出了当前账号的可见范围');
  });
});

describe('指定课堂的详情取数（评审意见：真实导航）', () => {
  it('isRunActive 只把 queued/running 当作活动批次', () => {
    expect(isRunActive({ ...runPartialJson, status: 'queued' } as unknown as Run)).toBe(true);
    expect(isRunActive({ ...runPartialJson, status: 'running' } as unknown as Run)).toBe(true);
    for (const status of ['succeeded', 'partial', 'failed', 'cancelled'] as const) {
      expect(isRunActive({ ...runPartialJson, status } as unknown as Run)).toBe(false);
    }
  });

  it('按 id 找到课堂并取最新批次', async () => {
    stubRoutes(defaultRoutes());
    const loaded = await loadSessionDetail(apiSource, BASE_SESSION.id);
    expect(loaded.session.id).toBe(BASE_SESSION.id);
    expect(loaded.run?.id).toBe(RUN_ID);
  });

  it('id 不在可见范围时返回 NOT_FOUND', async () => {
    stubRoutes(defaultRoutes());
    const error = (await loadSessionDetail(apiSource, '不存在的课堂').catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('NOT_FOUND');
  });

  it('按指定课堂取转写（不再全局挑第一条）', async () => {
    stubRoutes(defaultRoutes());
    const view = await loadTranscriptForSession(apiSource, BASE_SESSION);
    expect(view.segments).toHaveLength(1);
    expect(view.run?.id).toBe(RUN_ID);
  });

  it('课堂没有批次时转写给出明确提示', async () => {
    stubRoutes(defaultRoutes([route('/api/v1/sessions', { items: [{ ...BASE_SESSION, latest_run_id: null }], next_cursor: null })]));
    const error = (await loadTranscriptForSession(apiSource, { ...BASE_SESSION, latest_run_id: null }).catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('NOT_FOUND');
    expect(error.message).toContain('还没有分析批次');
  });

  it('按指定课堂取报告：没有发布版时用批次 report_id 取草稿', async () => {
    const draftRun = { ...runPartialJson, report_id: reportDraft.id } as unknown as Run;
    stubRoutes(defaultRoutes([route(`/api/v1/analysis-runs/${RUN_ID}`, draftRun)]));
    const view = await loadReportForSession(apiSource, BASE_SESSION, draftRun);
    expect(view.report.id).toBe(reportDraft.id);
    expect(view.report.status).toBe('draft');
  });
});
