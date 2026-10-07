/**
 * 接口模式下的取数组合逻辑（纯异步函数，不依赖 React）。
 *
 * 为什么单独拆出来：页面需要一个"组合"——先列表、再按 id 取批次、再取结果与修订。
 * 把组合放在这里，hooks 只负责加载状态，测试也可以直接按调用顺序验证，
 * 不用引入 DOM 环境或渲染整棵组件树。
 *
 * M1 骨架暂时按"当前账号可见的数据里挑一条"展示；后端实现课堂列表后，
 * 改成从列表点进详情即可，这里的返回类型不用变。
 */

import type { Run } from '../types';
import { ApiError, describeError } from './errors';
import { dataSource } from './sources';
import type { DataSource } from './sources';
import { buildReportView, buildSessionTasks, buildTranscriptView } from './viewModel';
import type { ReportView, SessionTaskView, TranscriptView } from './viewModel';

/** 当前账号可见的第一个带批次的课堂；没有就返回 null。 */
function firstSessionWithRun(sessionPage: Awaited<ReturnType<DataSource['getSessions']>>): { title: string; runId: string } | null {
  const session = sessionPage.items.find((item) => item.latest_run_id !== null) ?? null;
  if (session === null || session.latest_run_id === null) {
    return null;
  }
  return { title: session.title, runId: session.latest_run_id };
}

/** 课堂任务页：列表 + 每个课堂最新批次（批次读取失败不阻断整页）。 */
export async function loadSessionTasks(source: DataSource = dataSource): Promise<SessionTaskView[]> {
  const sessionPage = await source.getSessions();
  const runs = new Map<string, Run | null>();
  const runErrors = new Map<string, string>();

  await Promise.all(
    sessionPage.items.map(async (session) => {
      const runId = session.latest_run_id;
      if (runId === null) {
        return;
      }
      try {
        runs.set(runId, await source.getRun(runId));
      } catch (error) {
        runs.set(runId, null);
        runErrors.set(runId, describeError(error));
      }
    }),
  );

  return buildSessionTasks({ sessionPage, runs, kind: 'api', draftReport: null, runErrors });
}

export interface TranscriptLoad {
  view: TranscriptView;
  /** 说明当前展示的是哪条数据，显示在页面来源说明下方。 */
  selection: string;
}

export async function loadTranscriptView(source: DataSource = dataSource): Promise<TranscriptLoad> {
  const sessionPage = await source.getSessions();
  const chosen = firstSessionWithRun(sessionPage);
  if (chosen === null) {
    throw new ApiError({
      code: 'NOT_FOUND',
      status: 0,
      message: '当前账号没有可见的分析批次，暂时没有转写可展示。',
    });
  }

  const run = await source.getRun(chosen.runId);
  const results = await source.getResults(chosen.runId);

  let revision = null;
  if (results.transcript_revision_id !== null) {
    try {
      revision = await source.getRevision(results.transcript_revision_id);
    } catch {
      // 修订取不到不阻断整页：buildTranscriptView 会把"读取失败"写进核对提示。
      revision = null;
    }
  }

  return {
    view: buildTranscriptView({ results, revision, run, kind: 'api' }),
    selection: `当前展示：课堂「${chosen.title}」的最新批次 ${chosen.runId}`,
  };
}

export interface ReportLoad {
  view: ReportView;
  selection: string;
}

export async function loadReportView(source: DataSource = dataSource): Promise<ReportLoad> {
  const sessionPage = await source.getSessions();
  const session = sessionPage.items.find((item) => item.current_report_id !== null) ?? null;
  if (session === null || session.current_report_id === null) {
    throw new ApiError({
      code: 'NOT_FOUND',
      status: 0,
      message: '当前账号可见的课堂里没有已发布的当前版报告。',
    });
  }

  const report = await source.getReport(session.current_report_id);
  const results = await source.getResults(report.run_id);
  const run = await source.getRun(report.run_id).catch(() => null);
  const revision =
    results.transcript_revision_id === null
      ? null
      : await source.getRevision(results.transcript_revision_id).catch(() => null);

  return {
    view: buildReportView({ report, results, sessionPage, run, revision, kind: 'api' }),
    selection: `当前展示：课堂「${session.title}」的当前发布版报告`,
  };
}
