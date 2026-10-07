/**
 * 页面取数钩子：三个页面只经这里取数，不直接接触样例文件、数据源实现或 fetch。
 *
 * 两种模式的行为差异：
 *   - 固定样例模式：数据在本地，首帧同步可用（不闪烁），也不发起任何请求；
 *   - 接口模式：登录后才请求，期间页面进入 loading，失败时给可读提示；未登录不发请求，
 *     提示先登录；退出或换账号会立即清空上一个账号的数据，并丢弃迟到的响应。
 *
 * M1 骨架暂时按"当前账号可见的数据里挑一条"来展示，等后端实现课堂列表后，
 * 改成从列表点进详情即可，钩子返回的视图模型不用改。
 */

import { useEffect, useMemo, useRef, useState } from 'react';
import { useAuth } from '../auth';
import { describeError } from './errors';
import { loadingState, readyState, signedOutState } from './loadState';
import type { LoadState } from './loadState';
import { loadReportView, loadSessionTasks as loadSessionTasksApi, loadTranscriptView } from './loaders';
import { FIXTURES } from './samples';
import type { AuthStatus } from './sessionFlow';
import { DATA_SOURCE_LABEL, isApiMode } from './sources';
import type { DataSourceKind, SessionQuery } from './sources';
import { dataSource } from './sources';
import { SearchPaused, newSearch, checkNextCursor } from './search';
import { buildReportView, buildSessionTasks, buildTranscriptView } from './viewModel';
import type { ReportView, SessionTaskView, TranscriptView } from './viewModel';

/** 页面顶部"数据来源"一行需要的信息。 */
export interface DataOrigin {
  kind: DataSourceKind;
  label: string;
  sources: string[];
  note: string;
}

const API = isApiMode();

/** 样例模式/外部注入数据时用的固定 key：与任何登录会话都无关。 */
const LOCAL_KEY = 'local';

function originFor(kind: DataSourceKind, sources: string[]): DataOrigin {
  return {
    kind,
    label: DATA_SOURCE_LABEL[kind],
    sources,
    note: kind === 'api' ? '。' : '，未接入后端。',
  };
}

/**
 * 统一的加载逻辑，按会话标识（key）区分：
 *   - enabled 为 false：页面已拿到外部传入的数据（单元测试注入路径），不请求；
 *   - 样例模式：同步返回样例数据，不请求；
 *   - 接口模式 + 正在确认登录状态：显示加载中，不请求；
 *   - 接口模式 + 未登录：不发请求，状态为 signed_out（提示先登录）；
 *   - 接口模式 + 已登录：请求数据；会话切换时立即清空并丢弃旧会话的迟到响应。
 */
interface LoadActions {
  hasData: boolean;
  actionLabel: string | null;
  loadMore(): void;
}

function useLoaded<T, P>(options: {
  enabled: boolean;
  sessionKey: string;
  authStatus: AuthStatus;
  initialData: T;
  newProgress(): P;
  load(progress: P, previous: T): Promise<T>;
  hasMore?: (data: T) => boolean;
}): LoadState<T> & LoadActions {
  const { enabled, sessionKey, authStatus, initialData } = options;
  const key = API && enabled ? sessionKey : LOCAL_KEY;
  const canLoad = authStatus === 'authenticated';
  const fallback = (): LoadState<T> & { hasData: boolean } => {
    if (!API || !enabled) return { ...readyState(key, initialData), hasData: true };
    return { ...(authStatus === 'anonymous' ? signedOutState(key, initialData) : loadingState(key, initialData)), hasData: false };
  };
  const [state, setState] = useState(fallback);
  const optionsRef = useRef(options);
  optionsRef.current = options;
  const currentKey = useRef(key);
  currentKey.current = key;
  type Context = { key: string; progress: P; busy: boolean; active: boolean; data: T; hasData: boolean };
  const context = useRef<Context | null>(null);

  async function run(ctx: Context): Promise<void> {
    if (!ctx.active || ctx.busy || currentKey.current !== ctx.key) return;
    ctx.busy = true;
    setState({ ...loadingState(ctx.key, ctx.data), hasData: ctx.hasData });
    const valid = () => ctx.active && context.current === ctx && currentKey.current === ctx.key;
    try {
      const data = await optionsRef.current.load(ctx.progress, ctx.data);
      if (valid()) {
        ctx.data = data;
        ctx.hasData = true;
        setState({ ...readyState(ctx.key, data), hasData: true });
      }
    } catch (error) {
      if (valid()) setState({
        key: ctx.key, data: ctx.data, hasData: ctx.hasData,
        status: error instanceof SearchPaused ? 'paused' : 'error',
        error: error instanceof SearchPaused ? error.message : describeError(error),
      });
    } finally {
      ctx.busy = false;
    }
  }

  useEffect(() => {
    if (!enabled || !API || !canLoad) return;
    const ctx: Context = { key, progress: optionsRef.current.newProgress(), data: initialData, hasData: false, busy: false, active: true };
    context.current = ctx;
    void run(ctx);
    return () => { ctx.active = false; };
  }, [key, enabled, canLoad, initialData]);

  const visible = state.key === key ? state : fallback();
  const actionLabel = !API || !enabled || !canLoad ? null
    : visible.status === 'paused' ? '继续查找'
    : visible.status === 'error' ? '重试'
    : visible.status === 'ready' && options.hasMore?.(visible.data) ? '加载更多' : null;
  return { ...visible, actionLabel, loadMore: () => {
    if (actionLabel !== null && context.current !== null) void run(context.current);
  } };
}

function useSessionContext(): { sessionKey: string; authStatus: AuthStatus } {
  const { sessionKey, status } = useAuth();
  return { sessionKey, authStatus: status };
}

/** 样例模式的课堂任务视图（同步，等价于 M0 的默认值）。 */
function fixtureTasks(): SessionTasksData {
  return { tasks: buildSessionTasks(), nextCursor: null };
}

export interface SessionTasksData {
  tasks: SessionTaskView[];
  /** 非 null 表示服务端还有下一页课堂（协议 limit 默认 20，游标不透明）。 */
  nextCursor: string | null;
}

export type SessionTasksResult = LoadState<SessionTasksData> & LoadActions & { origin: DataOrigin };

export function useSessionTasks(enabled = true, query: Omit<SessionQuery, 'cursor'> = {}): SessionTasksResult {
  const { sessionKey, authStatus } = useSessionContext();
  const initial = useMemo<SessionTasksData>(() => (API ? { tasks: [], nextCursor: null } : fixtureTasks()), []);
  const state = useLoaded({
    enabled, sessionKey: `${sessionKey}:${JSON.stringify(query)}`, authStatus, initialData: initial,
    newProgress: () => ({ cursor: undefined as string | undefined, seen: [] as string[] }),
    load: async (progress, previous) => {
      const loaded = await loadSessionTasksApi(dataSource, { ...query, cursor: progress.cursor });
      const seen = [...progress.seen, progress.cursor ?? ''];
      checkNextCursor(loaded.nextCursor, seen);
      const tasks = new Map(previous.tasks.map(task => [task.session.id, task]));
      for (const task of loaded.tasks) tasks.set(task.session.id, task);
      progress.cursor = loaded.nextCursor ?? undefined;
      progress.seen = seen;
      return { tasks: [...tasks.values()], nextCursor: loaded.nextCursor };
    },
    hasMore: data => data.nextCursor !== null,
  });

  return {
    ...state,
    origin: originFor(
      API ? 'api' : 'fixtures',
      API
        ? ['GET /api/v1/sessions', 'GET /api/v1/analysis-runs/{id}']
        : [FIXTURES.sessionPage, FIXTURES.run],
    ),
  };
}

/** 接口模式还没有数据时的空结果：不编造片段和证据，只给页面一个空壳。 */
function emptyResults(): TranscriptView['results'] {
  return {
    run_status: 'queued',
    transcript_revision_id: null,
    segments: [],
    evidence: [],
    frames: [],
    events: [],
    limitations: [],
  };
}

export interface TranscriptData {
  view: TranscriptView;
  /** 接口模式说明当前展示的是哪条数据；样例模式为 null。 */
  selection: string | null;
}

export type TranscriptResult = LoadState<TranscriptData> & LoadActions & { origin: DataOrigin };

export function useTranscriptData(enabled = true): TranscriptResult {
  const { sessionKey, authStatus } = useSessionContext();
  const initial = useMemo<TranscriptData>(
    () =>
      API
        ? {
            view: buildTranscriptView({ results: emptyResults(), revision: null, run: null, kind: 'api' }),
            selection: null,
          }
        : { view: buildTranscriptView(), selection: null },
    [],
  );
  const state = useLoaded({
    enabled,
    sessionKey,
    authStatus,
    initialData: initial,
    newProgress: newSearch,
    load: async (progress) => {
      const loaded = await loadTranscriptView(dataSource, progress);
      return { view: loaded.view, selection: loaded.selection };
    },
  });

  return {
    ...state,
    origin: originFor(
      API ? 'api' : 'fixtures',
      API
        ? [
            'GET /api/v1/sessions',
            'GET /api/v1/analysis-runs/{id}',
            'GET /api/v1/analysis-runs/{id}/results',
            'GET /api/v1/transcript-revisions/{id}',
          ]
        : [FIXTURES.results, FIXTURES.revision],
    ),
  };
}

/** 接口模式加载失败时页面用的空报告视图：只用于占位，不参与任何结论展示。 */
function emptyReportView(): ReportView {
  return {
    report: {
      id: '',
      run_id: '',
      session_id: '',
      revision: 0,
      lock_version: 0,
      status: 'draft',
      summary: '',
      dimensions: [],
      observations: [],
      summary_evidence_ids: [],
      content_sha256: null,
      reviewed_content_sha256: null,
      reviewed_by: null,
      reviewed_at: null,
      provenance: null,
      allowed_actions: [],
    },
    session: null,
    run: null,
    evidenceById: new Map(),
    unresolvedEvidenceIds: [],
    disclosures: [],
  };
}

export interface ReportData {
  view: ReportView;
  selection: string | null;
}

export type ReportResult = LoadState<ReportData> & LoadActions & { origin: DataOrigin };

export function useReportData(enabled = true): ReportResult {
  const { sessionKey, authStatus } = useSessionContext();
  const initial = useMemo<ReportData>(
    () => (API ? { view: emptyReportView(), selection: null } : { view: buildReportView(), selection: null }),
    [],
  );
  const state = useLoaded({
    enabled,
    sessionKey,
    authStatus,
    initialData: initial,
    newProgress: newSearch,
    load: async (progress) => {
      const loaded = await loadReportView(dataSource, progress);
      return { view: loaded.view, selection: loaded.selection };
    },
  });

  return {
    ...state,
    origin: originFor(
      API ? 'api' : 'fixtures',
      API
        ? [
            'GET /api/v1/sessions',
            'GET /api/v1/reports/{id}',
            'GET /api/v1/analysis-runs/{id}',
            'GET /api/v1/analysis-runs/{id}/results',
            'GET /api/v1/transcript-revisions/{id}',
          ]
        : [FIXTURES.report, FIXTURES.results, FIXTURES.sessionPage, FIXTURES.run],
    ),
  };
}
