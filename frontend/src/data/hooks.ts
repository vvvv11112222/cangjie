/**
 * 页面取数钩子：三个页面只经这里取数，不直接接触样例文件、数据源实现或 fetch
 * （账号能力另外经 useAuth 读取，操作过滤在页面里做）。
 *
 * 两种模式的行为差异：
 *   - 固定样例模式：数据在本地，首帧同步可用（不闪烁），也不发起任何请求；
 *   - 接口模式：挂载后按协议顺序请求，期间页面进入 loading，失败时给可读提示。
 *
 * M1 骨架暂时按"当前账号可见的数据里挑一条"来展示，等后端实现课堂列表后，
 * 改成从列表点进详情即可，钩子返回的视图模型不用改。
 */

import { useEffect, useMemo, useRef, useState } from 'react';
import { describeError } from './errors';
import { loadReportView, loadSessionTasks as loadSessionTasksApi, loadTranscriptView } from './loaders';
import { FIXTURES } from './samples';
import { DATA_SOURCE_LABEL, isApiMode } from './sources';
import type { DataSourceKind } from './sources';
import { buildReportView, buildSessionTasks, buildTranscriptView } from './viewModel';
import type { ReportView, SessionTaskView, TranscriptView } from './viewModel';

export type LoadStatus = 'ready' | 'loading' | 'error';

/** 页面顶部"数据来源"一行需要的信息。 */
export interface DataOrigin {
  kind: DataSourceKind;
  label: string;
  sources: string[];
  note: string;
}

interface Loaded<T> {
  status: LoadStatus;
  error: string | null;
  data: T;
}

const API = isApiMode();

function originFor(kind: DataSourceKind, sources: string[]): DataOrigin {
  return {
    kind,
    label: DATA_SOURCE_LABEL[kind],
    sources,
    note: kind === 'api' ? '。' : '，未接入后端。',
  };
}

/**
 * 统一的"加载一次"逻辑。
 * enabled 为 false 表示页面已经拿到外部传入的数据（单元测试的注入路径），此时不做任何请求。
 */
function useLoaded<T>(enabled: boolean, initialData: T, load: () => Promise<T>, deps: readonly unknown[]): Loaded<T> {
  // 接口模式首帧就是 loading：不能先用空数据渲染一帧，否则页面会短暂显示"没有数据"。
  const [state, setState] = useState<Loaded<T>>(() => ({
    status: enabled && API ? 'loading' : 'ready',
    error: null,
    data: initialData,
  }));
  const loadRef = useRef(load);
  loadRef.current = load;

  useEffect(() => {
    if (!enabled || !API) {
      return;
    }
    let cancelled = false;
    setState({ status: 'loading', error: null, data: initialData });
    void (async () => {
      try {
        const data = await loadRef.current();
        if (!cancelled) {
          setState({ status: 'ready', error: null, data });
        }
      } catch (error) {
        if (!cancelled) {
          setState({ status: 'error', error: describeError(error), data: initialData });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
    // initialData 每次渲染都是新对象，故意不放进依赖；deps 由调用方给出真正会变的条件。
  }, [enabled, ...deps]);

  return state;
}

/** 样例模式的课堂任务视图（同步，等价于 M0 的默认值）。 */
function fixtureTasks(): SessionTaskView[] {
  return buildSessionTasks();
}

export interface SessionTasksResult extends Loaded<SessionTaskView[]> {
  origin: DataOrigin;
}

export function useSessionTasks(enabled = true): SessionTasksResult {
  const initial = useMemo(() => (API ? [] : fixtureTasks()), []);
  const state = useLoaded(
    enabled,
    initial,
    loadSessionTasksApi,
    [],
  );

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

/** 样例模式的转写视图（同步，等价于 M0 的默认值）。 */
function fixtureTranscript(): TranscriptView {
  return buildTranscriptView();
}

export interface TranscriptResult extends Loaded<TranscriptView> {
  origin: DataOrigin;
  /** 接口模式说明当前展示的是哪条数据；样例模式为 null。 */
  selection: string | null;
}

export function useTranscriptData(enabled = true): TranscriptResult {
  const initial = useMemo(() => (API ? buildTranscriptView({ results: emptyResults(), revision: null, run: null, kind: 'api' }) : fixtureTranscript()), []);
  const [selection, setSelection] = useState<string | null>(null);
  const state = useLoaded(
    enabled,
    initial,
    async () => {
      const loaded = await loadTranscriptView();
      setSelection(loaded.selection);
      return loaded.view;
    },
    [],
  );

  return {
    ...state,
    selection: API ? selection : null,
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

/** 样例模式的报告视图（同步，等价于 M0 的默认值）。 */
function fixtureReport(): ReportView {
  return buildReportView();
}

export interface ReportResult extends Loaded<ReportView> {
  origin: DataOrigin;
  selection: string | null;
}

export function useReportData(enabled = true): ReportResult {
  const initial = useMemo(() => (API ? emptyReportView() : fixtureReport()), []);
  const [selection, setSelection] = useState<string | null>(null);
  const state = useLoaded(
    enabled,
    initial,
    async () => {
      const loaded = await loadReportView();
      setSelection(loaded.selection);
      return loaded.view;
    },
    [],
  );

  return {
    ...state,
    selection: API ? selection : null,
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
