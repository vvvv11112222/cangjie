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
import { applyIfSameSession, errorState, forSession, loadingState, readyState, signedOutState } from './loadState';
import type { LoadState } from './loadState';
import { loadReportView, loadSessionTasks as loadSessionTasksApi, loadTranscriptView } from './loaders';
import { FIXTURES } from './samples';
import type { AuthStatus } from './sessionFlow';
import { DATA_SOURCE_LABEL, isApiMode } from './sources';
import type { DataSourceKind } from './sources';
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
function useLoaded<T>(options: {
  enabled: boolean;
  /** 会话标识，来自 useAuth（登录/退出/换账号会变化）。 */
  sessionKey: string;
  /** 登录状态：只有 authenticated 才取数；checking 显示加载中，anonymous 提示先登录。 */
  authStatus: AuthStatus;
  initialData: T;
  load: () => Promise<T>;
}): LoadState<T> {
  const { enabled, sessionKey, authStatus, initialData, load } = options;
  const key = API && enabled ? sessionKey : LOCAL_KEY;
  const canLoad = authStatus === 'authenticated';

  const fallback = (): LoadState<T> => {
    if (!API || !enabled) {
      return readyState(key, initialData);
    }
    // 还没确认完登录状态时按"加载中"呈现，不能先说"未登录"。
    return authStatus === 'anonymous' ? signedOutState(key, initialData) : loadingState(key, initialData);
  };

  const [state, setState] = useState<LoadState<T>>(fallback);
  const loadRef = useRef(load);
  loadRef.current = load;

  useEffect(() => {
    if (!enabled || !API || !canLoad) {
      return;
    }
    let cancelled = false;
    setState(loadingState(key, initialData));
    void (async () => {
      try {
        const data = await loadRef.current();
        if (!cancelled) {
          setState((current) => applyIfSameSession(current, key, readyState(key, data)));
        }
      } catch (error) {
        if (!cancelled) {
          setState((current) =>
            applyIfSameSession(current, key, errorState(key, describeError(error), initialData)),
          );
        }
      }
    })();
    return () => {
      // 依赖变化（换账号）或卸载时，旧请求的结果一律作废。
      cancelled = true;
    };
  }, [key, enabled, canLoad, initialData]);

  // 状态还停留在旧会话时，立即按新会话的初始状态呈现，不闪出上一个账号的数据。
  return forSession(state, key, fallback);
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

export type SessionTasksResult = LoadState<SessionTasksData> & { origin: DataOrigin };

export function useSessionTasks(enabled = true): SessionTasksResult {
  const { sessionKey, authStatus } = useSessionContext();
  const initial = useMemo<SessionTasksData>(() => (API ? { tasks: [], nextCursor: null } : fixtureTasks()), []);
  const state = useLoaded({ enabled, sessionKey, authStatus, initialData: initial, load: loadSessionTasksApi });

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

export type TranscriptResult = LoadState<TranscriptData> & { origin: DataOrigin };

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
    load: async () => {
      const loaded = await loadTranscriptView();
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

export type ReportResult = LoadState<ReportData> & { origin: DataOrigin };

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
    load: async () => {
      const loaded = await loadReportView();
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
