/** 页面取数组合；查找检查点由 hook 按会话持有，不写入持久存储。 */
import type { Run, Session } from '../types';
import { ApiError, describeError } from './errors';
import { dataSource } from './sources';
import type { DataSource, SessionQuery } from './sources';
import { buildReportView, buildSessionTasks, buildTranscriptView } from './viewModel';
import type { ReportView, SessionTaskView, TranscriptView } from './viewModel';
import { MAX_DRAFT_RUN_LOOKUPS, SearchPaused, newSearch, searchSessions } from './search';
import type { SearchCheckpoint } from './search';
export { MAX_SESSION_PAGES, MAX_DRAFT_RUN_LOOKUPS } from './search';

/** 批次是否仍在执行（协议第 1 节：活动批次每 2 秒轮询，进入终态即停止）。 */
export function isRunActive(run: Run): boolean {
  return run.status === 'queued' || run.status === 'running';
}

export interface SessionTasksLoad {
  tasks: SessionTaskView[];
  nextCursor: string | null;
}

export interface SessionDetailLoad {
  session: Session;
  run: Run | null;
}

/** 从课堂列表里按 id 找到课堂并取它的最新批次。找不到就在可见范围外（NOT_FOUND）。 */
export async function loadSessionDetail(
  source: DataSource = dataSource,
  sessionId: string,
  checkpoint: SearchCheckpoint = newSearch(),
): Promise<SessionDetailLoad> {
  const session = await searchSessions(source, checkpoint, async (candidate) =>
    candidate.id === sessionId ? candidate : null,
  );
  if (session === null) {
    throw new ApiError({ code: 'NOT_FOUND', status: 0, message: '找不到该课堂，或不在当前账号可见范围。' });
  }
  const run = session.latest_run_id === null ? null : await source.getRun(session.latest_run_id).catch(() => null);
  return { session, run };
}

/** 指定课堂的最新批次转写视图（不再自动挑第一条）。 */
export async function loadTranscriptForSession(
  source: DataSource = dataSource,
  session: Session,
): Promise<TranscriptView> {
  const runId = session.latest_run_id;
  if (runId === null) {
    throw new ApiError({ code: 'NOT_FOUND', status: 0, message: '该课堂还没有分析批次，暂无转写。' });
  }
  const run = await source.getRun(runId);
  const results = await source.getResults(runId);
  const revision =
    results.transcript_revision_id === null ? null : await source.getRevision(results.transcript_revision_id);
  return buildTranscriptView({ results, revision, run, kind: 'api' });
}

/** 指定课堂的当前发布版报告，或尚未发布时按批次 report_id 取草稿。 */
export async function loadReportForSession(
  source: DataSource = dataSource,
  session: Session,
  run: Run | null,
): Promise<ReportView> {
  const reportId = session.current_report_id ?? run?.report_id ?? null;
  if (reportId === null) {
    throw new ApiError({ code: 'NOT_FOUND', status: 0, message: '该课堂还没有报告。' });
  }
  const report = await source.getReport(reportId);
  const results = await source.getResults(report.run_id);
  const runForReport = run !== null && run.id === report.run_id ? run : await source.getRun(report.run_id);
  const revision =
    results.transcript_revision_id === null ? null : await source.getRevision(results.transcript_revision_id);
  return buildReportView({
    report,
    results,
    sessionPage: { items: [session], next_cursor: null },
    run: runForReport,
    revision,
    kind: 'api',
  });
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
