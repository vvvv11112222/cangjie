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
import { ApiError, describeError } from './errors';
import { sameId } from './ids';
import { loadingState, readyState, signedOutState } from './loadState';
import type { LoadState } from './loadState';
import { loadReportView, loadSessionTasks as loadSessionTasksApi, loadTranscriptView } from './loaders';
import {
  isRunActive,
  loadReportForSession,
  loadSessionDetail,
  newSessionDetailSearch,
  loadTranscriptForSession,
} from './loaders';
import { FIXTURES, fixtureMediaFor, fixtures } from './samples';
import type { AuthStatus } from './sessionFlow';
import { DATA_SOURCE_LABEL, getMedia, getSessionMedia, isApiMode } from './sources';
import { probePlaybackAddress } from './playbackProbe';
import type { DataSourceKind, MediaLite, SessionQuery } from './sources';
import { dataSource } from './sources';
import { SearchPaused, newSearch, checkNextCursor } from './search';
import { buildReportView, buildSessionTasks, buildTranscriptView } from './viewModel';
import type { ReportView, SessionTaskView, TranscriptView } from './viewModel';
import type { Media, Run, RunStatus, Session } from '../types';

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
  /** 丢弃当前进度、从头重新加载；用于动作完成或轮询到终态后刷新数据。 */
  reload(): void;
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
  }, reload: () => {
    if (!enabled || !API || !canLoad) return;
    // 评审意见 N03：刷新也是副作用，必须确认这个闭包仍属于当前会话。
    // 旧会话的迟到动作调用 reload 时，不能关掉新会话正在进行的加载。
    if (currentKey.current !== key) return;
    // 从头重新加载：新建检查点，但保留已有数据，刷新期间页面不会闪空。
    if (context.current !== null) context.current.active = false;
    const ctx: Context = {
      key,
      progress: optionsRef.current.newProgress(),
      data: context.current?.data ?? initialData,
      hasData: context.current?.hasData ?? false,
      busy: false,
      active: true,
    };
    context.current = ctx;
    void run(ctx);
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

/** 协议第 1 节：活动批次每 2 秒轮询一次，进入终态即停止。 */
const POLL_INTERVAL_MS = 2000;

export interface PollState {
  /** 轮询得到的最新批次；还没取到或遇到不可恢复错误时为 null。 */
  run: Run | null;
  /** 最近一次轮询失败的可读提示；成功一次后清空。 */
  error: string | null;
  /** 是否仍在轮询：进入终态或遇到不可恢复错误后为 false。 */
  polling: boolean;
}

/** 失效、无权限、超范围、已过期这类错误重试也不会好：停止轮询并交给页面提示。 */
function isFatalPollError(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.code === 'UNAUTHENTICATED' ||
      error.code === 'FORBIDDEN' ||
      error.code === 'NOT_FOUND' ||
      error.code === 'SOURCE_NOT_VERIFIED' ||
      error.code === 'RESOURCE_EXPIRED')
  );
}

/**
 * 轮询单个批次。
 *
 * 评审意见 F05、F07：
 *   - **串行**：上一次请求返回后才调度下一次，不会出现两个在途请求互相覆盖
 *     （原来用 setInterval，慢响应时后发的 succeeded 会被先发的 running 覆盖）；
 *   - **错误不静默**：失败原因交给页面；不可恢复的错误停止轮询，
 *     临时故障保留上次结果并提示“数据未更新”，下一轮继续尝试。
 */
export function useRunPolling(runId: string | null, status: RunStatus | null, enabled: boolean): PollState {
  const [state, setState] = useState<PollState>({ run: null, error: null, polling: false });
  useEffect(() => {
    setState({ run: null, error: null, polling: false });
    if (!enabled || runId === null || status === null) return;
    if (status !== 'queued' && status !== 'running') return;

    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    setState({ run: null, error: null, polling: true });

    function schedule(): void {
      if (!cancelled) {
        timer = setTimeout(() => void tick(), POLL_INTERVAL_MS);
      }
    }

    async function tick(): Promise<void> {
      try {
        const next = await dataSource.getRun(runId as string);
        if (cancelled) return;
        if (!isRunActive(next)) {
          setState({ run: next, error: null, polling: false });
          return;
        }
        setState({ run: next, error: null, polling: true });
      } catch (error) {
        if (cancelled) return;
        const message = describeError(error);
        if (isFatalPollError(error)) {
          setState((current) => ({ run: current.run, error: message, polling: false }));
          return;
        }
        setState((current) => ({ run: current.run, error: message, polling: true }));
      }
      schedule();
    }

    schedule();
    return () => {
      cancelled = true;
      if (timer !== null) clearTimeout(timer);
    };
  }, [runId, status, enabled]);
  return state;
}

export interface SessionDetailData {
  session: Session | null;
  run: Run | null;
  /** 批次读取失败的可读原因；没有引用批次时为 null（评审意见 F06）。 */
  runError: string | null;
}

export type SessionDetailResult = LoadState<SessionDetailData> &
  LoadActions & {
    origin: DataOrigin;
    /** 轮询期间的最新批次；轮询没启动时等于 data.run。 */
    run: Run | null;
    runError: string | null;
  };

/**
 * 指定课堂的详情：从列表按 id 找到课堂（找不到时支持"继续查找"越过单轮预算），
 * 取最新批次，并对活动批次每 2 秒轮询。
 */
export function useSessionDetail(sessionId: string | null, enabled = true): SessionDetailResult {
  const { sessionKey, authStatus } = useSessionContext();
  const initial = useMemo<SessionDetailData>(() => ({ session: null, run: null, runError: null }), []);
  const state = useLoaded({
    enabled: enabled && sessionId !== null,
    sessionKey: sessionId === null ? sessionKey : `${sessionKey}:${sessionId}`,
    authStatus,
    initialData: initial,
    newProgress: newSessionDetailSearch,
    load: async (progress) => {
      const loaded = await loadSessionDetail(dataSource, sessionId as string, progress);
      return { session: loaded.session, run: loaded.run, runError: loaded.runError };
    },
  });
  return {
    ...state,
    run: state.status === 'ready' ? state.data.run : null,
    runError: state.status === 'ready' ? state.data.runError : null,
    origin: originFor(
      API ? 'api' : 'fixtures',
      API ? ['GET /api/v1/sessions', 'GET /api/v1/analysis-runs/{id}'] : [FIXTURES.sessionPage, FIXTURES.run],
    ),
  };
}

/** 指定课堂的最新批次转写视图；在样例模式下按 id 同步取固定样例。 */
export function useSessionTranscript(
  session: Session | null,
  enabled = true,
): LoadState<TranscriptView> & LoadActions & { origin: DataOrigin } {
  const { sessionKey, authStatus } = useSessionContext();
  const initial = useMemo<TranscriptView>(
    () =>
      API
        ? buildTranscriptView({ results: emptyResults(), revision: null, run: null, kind: 'api' })
        : fixtureSessionTranscript(session),
    [session],
  );
  const state = useLoaded({
    enabled: enabled && session !== null,
    // 加载标识带上实际批次：新批次完成后（评审意见 F04）会重新取转写，而不是继续用旧结果。
    sessionKey:
      session === null
        ? sessionKey
        : `${sessionKey}:transcript:${session.id}:${session.latest_run_id ?? 'none'}`,
    authStatus,
    initialData: initial,
    newProgress: () => undefined,
    load: async () => loadTranscriptForSession(dataSource, session as Session),
  });
  return {
    ...state,
    origin: originFor(
      API ? 'api' : 'fixtures',
      API
        ? [
            'GET /api/v1/analysis-runs/{id}',
            'GET /api/v1/analysis-runs/{id}/results',
            'GET /api/v1/transcript-revisions/{id}',
          ]
        : [FIXTURES.results, FIXTURES.revision],
    ),
  };
}

/** 指定课堂的当前发布版/草稿报告视图；在样例模式下按 id 同步取固定样例。 */
export function useSessionReport(
  session: Session | null,
  run: Run | null,
  enabled = true,
): LoadState<ReportView> & LoadActions & { origin: DataOrigin } {
  const { sessionKey, authStatus } = useSessionContext();
  const initial = useMemo<ReportView>(
    () => (API ? emptyReportView() : fixtureSessionReport(session)),
    [session],
  );
  const state = useLoaded({
    enabled: enabled && session !== null,
    // 加载标识带上实际报告引用：发布版/草稿变化后重新取报告（评审意见 F04）。
    sessionKey:
      session === null
        ? sessionKey
        : `${sessionKey}:report:${session.id}:${session.current_report_id ?? run?.report_id ?? 'none'}`,
    authStatus,
    initialData: initial,
    newProgress: () => undefined,
    load: async () => loadReportForSession(dataSource, session as Session, run),
  });
  return {
    ...state,
    origin: originFor(
      API ? 'api' : 'fixtures',
      API
        ? ['GET /api/v1/reports/{id}', 'GET /api/v1/analysis-runs/{id}', 'GET /api/v1/analysis-runs/{id}/results']
        : [FIXTURES.report, FIXTURES.results, FIXTURES.sessionPage, FIXTURES.run],
    ),
  };
}

/** 样例模式下同步找到固定样例里的课堂。 */
export function fixtureSession(sessionId: string): Session | null {
  // UUID 按值比较，大小写不同也是同一个课堂（评审意见 F10）。
  return fixtures.sessionPage.items.find((session) => sameId(session.id, sessionId)) ?? null;
}

/**
 * 指定课堂的媒体列表（可继续翻页）。
 * 评审意见 F02：首次上传后 Session 还没有主媒体，需要能从"当前/历史原媒体"里选处理目标；
 * 评审意见 N04：保留并消费 MediaPage 游标，第 21 条之后的合法原媒体也能取到。
 */
export interface SessionMediaData {
  items: MediaLite[];
  /** 非 null 表示还有下一页原媒体（协议 limit 默认 20）。 */
  nextCursor: string | null;
}

export function useSessionMedia(
  session: Session | null,
  enabled = true,
): LoadState<SessionMediaData> & LoadActions & { origin: DataOrigin } {
  const { sessionKey, authStatus } = useSessionContext();
  const initial = useMemo<SessionMediaData>(() => ({ items: [], nextCursor: null }), []);
  const state = useLoaded({
    enabled: enabled && session !== null,
    sessionKey: session === null ? sessionKey : `${sessionKey}:media:${session.id}`,
    authStatus,
    initialData: initial,
    newProgress: () => ({ cursor: undefined as string | undefined, seen: [] as string[] }),
    load: async (progress, previous) => {
      const page = await getSessionMedia((session as Session).id, progress.cursor);
      // 重复游标（服务端异常循环）要单独报错，不能无限翻页（N04）。
      const seen = [...progress.seen, progress.cursor ?? ''];
      checkNextCursor(page.nextCursor, seen);
      const merged = new Map(previous.items.map((item) => [item.id, item]));
      for (const item of page.items) {
        merged.set(item.id, item);
      }
      progress.cursor = page.nextCursor ?? undefined;
      progress.seen = seen;
      return { items: [...merged.values()], nextCursor: page.nextCursor };
    },
    hasMore: (data) => data.nextCursor !== null,
  });
  return {
    ...state,
    origin: originFor(
      API ? 'api' : 'fixtures',
      API ? ['GET /api/v1/sessions/{id}/media'] : [FIXTURES.sessionPage],
    ),
  };
}

export interface PlaybackData {
  /** 当前回放媒体；没有主媒体、也没有可播放媒体时为 null（页面显示空态，不拿别的媒体顶替）。 */
  media: Media | null;
}

/**
 * 指定媒体的详情（GET /media/{id}）。
 * 回放页的播放地址、时长、有效期与 allowed_actions 全部以这条响应为准：
 * 协议规定媒体未 ready、来源未核验、缺 playback 用途或已过期时 playback_url 为 null。
 * 固定样例模式同步返回仓库内的合成样例媒体；接口模式登录后请求，失败保留错误并可重试。
 */
export function useMediaDetail(
  mediaId: string | null,
  enabled = true,
): LoadState<PlaybackData> & LoadActions & { origin: DataOrigin } {
  const { sessionKey, authStatus } = useSessionContext();
  const initial = useMemo<PlaybackData>(
    () => (API ? { media: null } : { media: mediaId === null ? null : fixtureMediaFor(mediaId) }),
    [mediaId],
  );
  const state = useLoaded({
    enabled: enabled && mediaId !== null,
    // 加载标识带上媒体 id：切换回放目标后不能继续显示上一个媒体的播放地址。
    sessionKey: mediaId === null ? sessionKey : `${sessionKey}:media-detail:${mediaId}`,
    authStatus,
    initialData: initial,
    newProgress: () => undefined,
    load: async () => ({ media: await getMedia(mediaId as string) }),
  });
  return {
    ...state,
    origin: originFor(
      API ? 'api' : 'fixtures',
      API ? ['GET /api/v1/media/{id}'] : [FIXTURES.sessionPage, FIXTURES.playbackSample],
    ),
  };
}

export interface PlaybackProbeState {
  status: 'idle' | 'checking' | 'ok' | 'failed';
  /** 失败时的中文提示（含错误码与 HTTP 状态）；没有问题可显示时为 null。 */
  note: string | null;
  recheck(): void;
}

/**
 * 播放地址预检，实现见 data/playbackProbe.ts。
 * 固定样例模式播放的是仓库内本地文件，没有服务端语义，因此不做预检（status 保持 idle）。
 */
export function usePlaybackProbe(url: string | null, enabled = true): PlaybackProbeState {
  const { sessionKey, authStatus } = useSessionContext();
  const [state, setState] = useState<{ key: string; status: PlaybackProbeState['status']; note: string | null }>({
    key: '',
    status: 'idle',
    note: null,
  });
  const [nonce, setNonce] = useState(0);
  const key = `${sessionKey}:${url}:${enabled}:${authStatus}:${nonce}`;
  const checking = API && enabled && url !== null && authStatus === 'authenticated';

  useEffect(() => {
    if (!checking || url === null) {
      setState({ key, status: 'idle', note: null });
      return;
    }
    // 换账号后重查：播放地址的权限跟着会话走，旧会话的结果不能继续显示（同其它取数钩子）。
    let active = true;
    const controller = new AbortController();
    setState({ key, status: 'checking', note: null });
    void probePlaybackAddress(url, { signal: controller.signal }).then((result) => {
      if (!active || result.kind === 'cancelled') return;
      setState(
        result.kind === 'ok'
          ? { key, status: 'ok', note: null }
          : { key, status: 'failed', note: `播放地址不可用：${describeError(result.error)}` },
      );
    });
    return () => {
      active = false;
      controller.abort();
    };
  }, [key, checking, url]);

  const visible = state.key === key ? state : { status: checking ? 'checking' as const : 'idle' as const, note: null };
  return { ...visible, recheck: () => setNonce((current) => current + 1) };
}

function fixtureSessionTranscript(session: Session | null): TranscriptView {
  if (session === null || session.latest_run_id !== fixtures.run.id) {
    return buildTranscriptView({ results: emptyResults(), revision: null, run: null, kind: 'fixtures' });
  }
  return buildTranscriptView();
}

function fixtureSessionReport(session: Session | null): ReportView {
  if (session === null || session.id !== fixtures.report.session_id) {
    return emptyReportView();
  }
  return buildReportView();
}
