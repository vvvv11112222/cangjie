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

/**
 * 自动选择数据时最多翻多少页。协议默认每页 20 条，10 页约 200 条，
 * 既能找到后面的课堂，又不会退化成无界请求。
 * 注意：课堂列表页本身不翻页，只取一页并把 next_cursor 交给页面（见 loadSessionTasks）。
 */
export const MAX_SESSION_PAGES = 10;

/**
 * 查找草稿时最多查看多少个课堂的批次。
 * 草稿的 id 只在批次（Run.report_id）上，所以必须逐个课堂取批次；
 * 课堂数量 × 翻页会放大成上百次请求，这里限制在一页课堂（协议默认 20 条）以内，
 * 并如实告诉用户"只检查了前 N 个课堂"。后端提供课堂报告列表端点后可放宽。
 */
export const MAX_DRAFT_RUN_LOOKUPS = 20;

interface SessionWalk {
  items: Awaited<ReturnType<DataSource['getSessions']>>['items'];
  /** 还有没取到的页（达到上限或提前停下）时为非 null；为 null 表示已经取完。 */
  nextCursor: string | null;
}

/**
 * 逐页取课堂列表。
 * match 命中时立即停止翻页（自动选择不需要为一条数据翻完整库），
 * 否则一直取到没有下一页或达到页数上限，并把"是否还有未取的页"告诉调用方。
 */
async function walkSessions(
  source: DataSource,
  match?: (session: SessionWalk['items'][number]) => boolean,
): Promise<SessionWalk> {
  const items: SessionWalk['items'] = [];
  let cursor: string | undefined;

  for (let page = 0; page < MAX_SESSION_PAGES; page += 1) {
    const result = await source.getSessions({ cursor });
    items.push(...result.items);
    if (match !== undefined && result.items.some(match)) {
      return { items, nextCursor: result.next_cursor };
    }
    if (result.next_cursor === null) {
      return { items, nextCursor: null };
    }
    cursor = result.next_cursor;
  }

  // 到达页数上限：如实告诉调用方后面还有页没取。
  return { items, nextCursor: cursor ?? null };
}

export interface SessionTasksLoad {
  tasks: SessionTaskView[];
  /** 非 null 表示服务端还有下一页课堂。 */
  nextCursor: string | null;
}

/**
 * 课堂任务页：取**一页**列表 + 页面内每个课堂的最新批次（批次读取失败不阻断整页）。
 * 只取一页有两层考虑：一是控制请求量（每个课堂还要单独取批次），
 * 二是把 next_cursor 交回页面，分页浏览由后续 UI 决定，不在这里静默预取全部。
 */
export async function loadSessionTasks(source: DataSource = dataSource): Promise<SessionTasksLoad> {
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

  return {
    tasks: buildSessionTasks({ sessionPage, runs, kind: 'api', draftReport: null, runErrors }),
    nextCursor: sessionPage.next_cursor,
  };
}

export interface TranscriptLoad {
  view: TranscriptView;
  /** 说明当前展示的是哪条数据，显示在页面来源说明下方。 */
  selection: string;
}

export async function loadTranscriptView(source: DataSource = dataSource): Promise<TranscriptLoad> {
  // 逐页找第一个带批次的课堂：第一页没有时继续用 next_cursor 往后找。
  const walk = await walkSessions(source, (session) => session.latest_run_id !== null);
  const chosen = walk.items.find((session) => session.latest_run_id !== null) ?? null;
  if (chosen === null) {
    throw new ApiError({
      code: 'NOT_FOUND',
      status: 0,
      message:
        walk.nextCursor === null
          ? '当前账号没有可见的分析批次，暂时没有转写可展示。'
          : `已翻查前 ${MAX_SESSION_PAGES} 页课堂，没有找到带批次的课堂，后面可能还有更多。`,
    });
  }
  if (chosen.latest_run_id === null) {
    // match 已保证非 null，这里只是为了类型收窄。
    throw new ApiError({ code: 'NOT_FOUND', status: 0, message: '该课堂没有最新批次。' });
  }
  const runId = chosen.latest_run_id;

  const run = await source.getRun(runId);
  const results = await source.getResults(runId);

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
    selection: `当前展示：课堂「${chosen.title}」的最新批次 ${runId}`,
  };
}

export interface ReportLoad {
  view: ReportView;
  selection: string;
}

export async function loadReportView(source: DataSource = dataSource): Promise<ReportLoad> {
  // 1) 优先当前发布版：Session.current_report_id 只有发布后才写入。
  const walk = await walkSessions(source, (session) => session.current_report_id !== null);
  const sessionPage = { items: walk.items, next_cursor: walk.nextCursor };
  const published = walk.items.find((item) => item.current_report_id !== null) ?? null;

  // 2) 还没有发布版时，草稿的 id 在批次的 report_id 上（协议：Session.current_report_id 为 null）。
  let reportId: string | null = null;
  let selection = '';
  if (published !== null && published.current_report_id !== null) {
    reportId = published.current_report_id;
    selection = `当前展示：课堂「${published.title}」的当前发布版报告`;
  } else {
    let lookedUp = 0;
    let lookupLimitReached = false;
    for (const session of walk.items) {
      if (session.latest_run_id === null) {
        continue;
      }
      if (lookedUp >= MAX_DRAFT_RUN_LOOKUPS) {
        lookupLimitReached = true;
        break;
      }
      lookedUp += 1;
      const run = await source.getRun(session.latest_run_id).catch(() => null);
      if (run !== null && run.report_id !== null) {
        reportId = run.report_id;
        selection = `当前展示：课堂「${session.title}」的最新报告草稿（尚未发布）`;
        break;
      }
    }
    if (reportId === null && lookupLimitReached) {
      throw new ApiError({
        code: 'NOT_FOUND',
        status: 0,
        message: `已检查前 ${MAX_DRAFT_RUN_LOOKUPS} 个课堂的批次，没有找到报告；后面可能还有更多。`,
      });
    }
  }

  if (reportId === null) {
    throw new ApiError({
      code: 'NOT_FOUND',
      status: 0,
      message:
        walk.nextCursor === null
          ? '当前账号可见的课堂里没有报告，或报告还没有生成。'
          : `已翻查前 ${MAX_SESSION_PAGES} 页课堂，没有找到报告，后面可能还有更多。`,
    });
  }

  const report = await source.getReport(reportId);
  const results = await source.getResults(report.run_id);
  const run = await source.getRun(report.run_id).catch(() => null);
  const revision =
    results.transcript_revision_id === null
      ? null
      : await source.getRevision(results.transcript_revision_id).catch(() => null);

  return {
    view: buildReportView({ report, results, sessionPage, run, revision, kind: 'api' }),
    selection,
  };
}
