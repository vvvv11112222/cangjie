import { describe, expect, it, vi } from 'vitest';
import { ApiError } from './errors';
import { loadReportView, loadSessionTasks, loadTranscriptView } from './loaders';
import { newSearch, SearchPaused } from './search';
import { fixtureSource } from './sources';
import { fixtures } from './samples';

const base = fixtures.sessionPage.items[0]!;
const noResult = { ...base, latest_run_id: null, current_report_id: null };

describe('可恢复的分页和查找', () => {
  it('显式游标获取第二页，空第一页也保留下一页入口', async () => {
    const getSessions = vi.fn(async (query?: { cursor?: string }) => query?.cursor === 'next'
      ? { items: [noResult], next_cursor: null } : { items: [], next_cursor: 'next' });
    const source = { ...fixtureSource, kind: 'api' as const, getSessions };
    expect(await loadSessionTasks(source)).toMatchObject({ tasks: [], nextCursor: 'next' });
    expect((await loadSessionTasks(source, { cursor: 'next' })).tasks).toHaveLength(1);
    expect(getSessions.mock.calls).toEqual([[{}], [{ cursor: 'next' }]]);
  });

  it('第11页的转写可续查到，不重新请求前10页', async () => {
    const getSessions = vi.fn(async (query?: { cursor?: string }) => {
      const page = Number(query?.cursor ?? 0);
      return { items: [page === 10 ? base : noResult], next_cursor: page === 10 ? null : String(page + 1) };
    });
    const source = { ...fixtureSource, getSessions };
    const checkpoint = newSearch();
    await expect(loadTranscriptView(source, checkpoint)).rejects.toBeInstanceOf(SearchPaused);
    expect(getSessions).toHaveBeenCalledTimes(10);
    expect((await loadTranscriptView(source, checkpoint)).view.run?.id).toBe(fixtures.run.id);
    expect(getSessions).toHaveBeenCalledTimes(11);
    expect(getSessions.mock.calls[10]).toEqual([{ cursor: '10' }]);
  });

  it('第21个草稿可继续读取，页内未检查记录不会丢失', async () => {
    const sessions = Array.from({ length: 21 }, (_, index) => ({ ...base, latest_run_id: String(index) }));
    const getRun = vi.fn(async (id: string) => ({ ...fixtures.run, id, report_id: id === '20' ? fixtures.report.id : null }));
    const source = {
      ...fixtureSource,
      getSessions: vi.fn(async (query?: { cursor?: string }) => query?.cursor === 'next'
        ? { items: sessions.slice(15), next_cursor: null } : { items: sessions.slice(0, 15), next_cursor: 'next' }),
      getRun,
      getReport: async () => ({ ...fixtures.report, run_id: '20' }),
      getResults: async () => fixtures.results,
    };
    const checkpoint = newSearch();
    await expect(loadReportView(source, checkpoint)).rejects.toBeInstanceOf(SearchPaused);
    expect(getRun).toHaveBeenCalledTimes(20);
    expect(checkpoint.index).toBe(5);
    expect((await loadReportView(source, checkpoint)).view.report.id).toBe(fixtures.report.id);
    expect(getRun).toHaveBeenCalledTimes(21);
    expect(source.getSessions).toHaveBeenCalledTimes(2);
  });

  it.each([
    ['UNAUTHENTICATED', 401], ['FORBIDDEN', 403], ['SERVICE_UNAVAILABLE', 503], ['NETWORK_ERROR', 0], ['NOT_FOUND', 404],
  ] as const)('草稿批次 %s 原样传播，重试停在失败记录', async (code, status) => {
    const error = new ApiError({ code, status });
    let failed = true;
    const getSessions = vi.fn(async () => ({ items: [base], next_cursor: null }));
    const getRun = vi.fn(async () => {
      if (failed) throw error;
      return { ...fixtures.run, report_id: fixtures.report.id };
    });
    const source = { ...fixtureSource, getSessions, getRun };
    const checkpoint = newSearch();
    await expect(loadReportView(source, checkpoint)).rejects.toBe(error);
    expect(checkpoint.index).toBe(0);
    failed = false;
    expect((await loadReportView(source, checkpoint)).view.report.id).toBe(fixtures.report.id);
    expect(getSessions).toHaveBeenCalledTimes(1);
  });

  it('续查中下一页失败后从失败页重试，而非重新扫描前缀', async () => {
    let fail = true;
    const getSessions = vi.fn(async (query?: { cursor?: string }) => {
      if (query?.cursor === 'next' && fail) throw new ApiError({ code: 'NETWORK_ERROR', status: 0 });
      return query?.cursor === 'next' ? { items: [base], next_cursor: null } : { items: [], next_cursor: 'next' };
    });
    const source = { ...fixtureSource, getSessions };
    const checkpoint = newSearch();
    await expect(loadTranscriptView(source, checkpoint)).rejects.toMatchObject({ code: 'NETWORK_ERROR' });
    fail = false;
    await loadTranscriptView(source, checkpoint);
    expect(getSessions.mock.calls).toEqual([[{ cursor: undefined }], [{ cursor: 'next' }], [{ cursor: 'next' }]]);
  });

  it('循环游标立即报错，不能靠预算反复继续', async () => {
    const getSessions = vi.fn(async (query?: { cursor?: string }) => ({ items: [], next_cursor: query?.cursor === 'a' ? 'b' : 'a' }));
    await expect(loadTranscriptView({ ...fixtureSource, getSessions })).rejects.toMatchObject({ code: 'MALFORMED_RESPONSE' });
    expect(getSessions).toHaveBeenCalledTimes(3);
  });
});
