import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, describeError } from './errors';
import { clearCsrfToken } from './http';
import { loadReportView, loadSessionTasks, loadTranscriptView } from './loaders';
import { apiSource } from './sources';
import reportDraft from '../../../contracts/examples/report-draft.json';
import resultsJson from '../../../contracts/examples/results.json';
import revisionJson from '../../../contracts/examples/revision.json';
import runPartialJson from '../../../contracts/examples/run-partial.json';
import sessionPageJson from '../../../contracts/examples/session-page.json';

/**
 * 把协议样例当作"后端返回的响应"喂给接口实现，验证取数组合顺序和错误处理。
 * 这样即使本机没有 Go 服务，接口模式的整条链路也能被自动检查。
 */
function stubBackend(overrides: Record<string, () => Response> = {}): string[] {
  const urls: string[] = [];
  const session = sessionPageJson.items[0]!;
  const defaults: Record<string, () => Response> = {
    '/api/v1/sessions': () => json(200, sessionPageJson),
    [`/api/v1/analysis-runs/${session.latest_run_id}`]: () => json(200, runPartialJson),
    [`/api/v1/analysis-runs/${session.latest_run_id}/results`]: () => json(200, resultsJson),
    [`/api/v1/transcript-revisions/${resultsJson.transcript_revision_id}`]: () => json(200, revisionJson),
    [`/api/v1/reports/${reportDraft.id}`]: () => json(200, reportDraft),
  };
  vi.stubGlobal('fetch', async (url: unknown) => {
    const target = String(url);
    urls.push(target);
    const route = overrides[target] ?? defaults[target];
    if (route === undefined) {
      return json(404, { error: { code: 'NOT_FOUND', message: '未实现', details: {} }, request_id: 'r-404' });
    }
    return route();
  });
  return urls;
}

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

afterEach(() => {
  vi.unstubAllGlobals();
  clearCsrfToken();
});

describe('接口模式取数组合', () => {
  it('课堂任务页先取列表，再按 latest_run_id 取批次', async () => {
    const urls = stubBackend();
    const tasks = await loadSessionTasks(apiSource);
    const session = sessionPageJson.items[0]!;

    expect(urls).toEqual([
      '/api/v1/sessions',
      `/api/v1/analysis-runs/${session.latest_run_id}`,
    ]);
    expect(tasks).toHaveLength(1);
    expect(tasks[0]?.run?.id).toBe(session.latest_run_id);
    expect(tasks[0]?.stages).toMatchObject({ total: 6, succeeded: 4, failed: 1, skipped: 1 });
  });

  it('批次接口失败时保留课堂行，并把原因写进核对提示', async () => {
    const session = sessionPageJson.items[0]!;
    stubBackend({
      [`/api/v1/analysis-runs/${session.latest_run_id}`]: () =>
        json(404, { error: { code: 'NOT_FOUND', message: '未实现', details: {} }, request_id: 'r' }),
    });
    const tasks = await loadSessionTasks(apiSource);
    expect(tasks[0]?.session.id).toBe(session.id);
    expect(tasks[0]?.run).toBeNull();
    expect(tasks[0]?.disclosures.some((item) => item.text.includes('读取失败'))).toBe(true);
  });

  it('转写页按 列表 → 批次 → 结果 → 修订 的顺序取数', async () => {
    const urls = stubBackend();
    const loaded = await loadTranscriptView(apiSource);
    const session = sessionPageJson.items[0]!;

    expect(urls).toEqual([
      '/api/v1/sessions',
      `/api/v1/analysis-runs/${session.latest_run_id}`,
      `/api/v1/analysis-runs/${session.latest_run_id}/results`,
      `/api/v1/transcript-revisions/${resultsJson.transcript_revision_id}`,
    ]);
    expect(loaded.view.segments).toHaveLength(1);
    expect(loaded.selection).toContain('合成课堂');
  });

  it('报告页取当前发布版报告及其结果', async () => {
    const session = { ...sessionPageJson.items[0]!, current_report_id: reportDraft.id };
    stubBackend({ '/api/v1/sessions': () => json(200, { items: [session], next_cursor: null }) });
    const loaded = await loadReportView(apiSource);
    expect(loaded.view.report.id).toBe(reportDraft.id);
    expect(loaded.view.unresolvedEvidenceIds).toEqual([]);
    expect(loaded.selection).toContain('合成课堂');
  });

  it('后端未实现 /sessions 时给出可读提示，而不是空列表', async () => {
    stubBackend({
      '/api/v1/sessions': () =>
        json(404, { error: { code: 'NOT_FOUND', message: 'not implemented', details: {} }, request_id: 'r' }),
    });
    const error = (await loadSessionTasks(apiSource).catch((reason: unknown) => reason)) as ApiError;
    expect(error.code).toBe('NOT_FOUND');
    expect(describeError(error)).toContain('不存在，或超出了当前账号的可见范围');
  });
});
