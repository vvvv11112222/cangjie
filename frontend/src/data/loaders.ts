/** 页面取数组合；查找检查点由 hook 按会话持有，不写入持久存储。 */
import type { Run } from '../types';
import { ApiError, describeError } from './errors';
import { dataSource } from './sources';
import type { DataSource, SessionQuery } from './sources';
import { buildReportView, buildSessionTasks, buildTranscriptView } from './viewModel';
import type { ReportView, SessionTaskView, TranscriptView } from './viewModel';
import { MAX_DRAFT_RUN_LOOKUPS, SearchPaused, newSearch, searchSessions } from './search';
import type { SearchCheckpoint } from './search';
export { MAX_SESSION_PAGES, MAX_DRAFT_RUN_LOOKUPS } from './search';

export interface SessionTasksLoad {
  tasks: SessionTaskView[];
  nextCursor: string | null;
}

/** 加载一页，游标由界面显式传入；批次失败保留课堂行及失败说明。 */
export async function loadSessionTasks(source: DataSource = dataSource, query: SessionQuery = {}): Promise<SessionTasksLoad> {
  const sessionPage = await source.getSessions(query);
  const runs = new Map<string, Run | null>();
  const runErrors = new Map<string, string>();
  await Promise.all(sessionPage.items.map(async (session) => {
    const runId = session.latest_run_id;
    if (runId === null) return;
    try {
      runs.set(runId, await source.getRun(runId));
    } catch (error) {
      runs.set(runId, null);
      runErrors.set(runId, describeError(error));
    }
  }));
  return {
    tasks: buildSessionTasks({ sessionPage, runs, kind: 'api', draftReport: null, runErrors }),
    nextCursor: sessionPage.next_cursor,
  };
}

export interface TranscriptLoad { view: TranscriptView; selection: string }
export async function loadTranscriptView(
  source: DataSource = dataSource,
  checkpoint: SearchCheckpoint = newSearch(),
): Promise<TranscriptLoad> {
  const result = await searchSessions(source, checkpoint, async (chosen) => {
    if (chosen.latest_run_id === null) return null;
    const runId = chosen.latest_run_id;
    const run = await source.getRun(runId);
    const results = await source.getResults(runId);
    const revision = results.transcript_revision_id === null ? null : await source.getRevision(results.transcript_revision_id);
    return {
      view: buildTranscriptView({ results, revision, run, kind: 'api' }),
      selection: `当前展示：课堂「${chosen.title}」的最新批次 ${runId}`,
    };
  });
  if (result !== null) return result;
  throw new ApiError({ code: 'NOT_FOUND', status: 0, message: '当前账号没有可见的分析批次，暂时没有转写可展示。' });
}

export interface ReportLoad { view: ReportView; selection: string }
export async function loadReportView(
  source: DataSource = dataSource,
  checkpoint: SearchCheckpoint = newSearch(),
): Promise<ReportLoad> {
  let lookups = 0;
  const result = await searchSessions(source, checkpoint, async (session) => {
    let reportId = session.current_report_id;
    let draftRun: Run | null = null;
    if (reportId === null) {
      if (session.latest_run_id === null) return null;
      if (lookups >= MAX_DRAFT_RUN_LOOKUPS) throw new SearchPaused();
      lookups += 1;
      // 所有接口错误原样传播；没有契约依据时，不把缺失或权限拒绝当成“没有报告”。
      draftRun = await source.getRun(session.latest_run_id);
      reportId = draftRun.report_id;
    }
    if (reportId === null) return null;
    const report = await source.getReport(reportId);
    const results = await source.getResults(report.run_id);
    const run = draftRun?.id === report.run_id ? draftRun : await source.getRun(report.run_id);
    const revision = results.transcript_revision_id === null ? null : await source.getRevision(results.transcript_revision_id);
    return {
      view: buildReportView({
        report, results, sessionPage: { items: [session], next_cursor: null }, run, revision, kind: 'api',
      }),
      selection: `当前展示：课堂「${session.title}」的${session.current_report_id === reportId ? '当前发布版报告' : '最新报告草稿（尚未发布）'}`,
    };
  }, true);
  if (result !== null) return result;
  throw new ApiError({ code: 'NOT_FOUND', status: 0, message: '当前账号可见的课堂里没有报告，或报告还没有生成。' });
}
