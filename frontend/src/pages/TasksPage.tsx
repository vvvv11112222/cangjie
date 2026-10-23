import type { ReactElement } from 'react';
import { useEffect,useMemo,useRef,useState } from 'react';
import { useAuth } from '../auth';
import { IconUpload } from '../components/icons';
import { BadgeTag,EmptyState,LoadControls,LoadStateNote,RunErrorBox,StageFlow,TechDetails } from '../components/ui';
import { cancelRun } from '../data/actions';
import { ApiError,describeError } from '../data/errors';
import { useSessionTasks } from '../data/hooks';
import { listSessionRuns } from '../data/m2Api';
import { dataSource,isApiMode } from '../data/sources';
import { formatDateTime,runModeLabel,runStatusBadge } from '../format';
import type { Run,Session } from '../types';

interface HistoryState {
  items: Run[];
  nextCursor: string | null;
  loading: boolean;
  error: string | null;
}

function mergeRuns(current: Run[], incoming: Run[]): Run[] {
  const merged = new Map(current.map((run) => [run.id, run]));
  for (const run of incoming) merged.set(run.id, run);
  return [...merged.values()];
}

function isActive(run: Run): boolean {
  return run.status === 'queued' || run.status === 'running';
}

export function TasksPage(): ReactElement {
  const loaded = useSessionTasks();
  const { sessionKey, status: authStatus } = useAuth();
  const apiMode = isApiMode();
  const [history, setHistory] = useState<Record<string, HistoryState>>({});
  const [busyId, setBusyId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [pollFailures, setPollFailures] = useState<Record<string, { message: string; terminal: boolean }>>({});
  const historyGeneration = useRef(0);
  const sessionIds = loaded.data.tasks.map(({ session }) => session.id).join('|');

  // 每个课堂各自读取 RunPage。游标是服务端不透明值，页面只原样传回，刷新不再只剩 latest_run_id。
  useEffect(() => {
    const generation = ++historyGeneration.current;
    setHistory({});
    setPollFailures({});
    if (!apiMode || authStatus !== 'authenticated') return undefined;
    for (const { session } of loaded.data.tasks) {
      setHistory((current) => ({ ...current, [session.id]: { items: [], nextCursor: null, loading: true, error: null } }));
      void listSessionRuns(session.id).then((page) => {
        if (generation !== historyGeneration.current) return;
        setHistory((current) => ({ ...current, [session.id]: { items: mergeRuns(page.items, current[session.id]?.items ?? []), nextCursor: page.next_cursor, loading: false, error: null } }));
      }).catch((error: unknown) => {
        if (generation !== historyGeneration.current) return;
        setHistory((current) => ({ ...current, [session.id]: { items: current[session.id]?.items ?? [], nextCursor: null, loading: false, error: describeError(error) } }));
      });
    }
    return () => { if (historyGeneration.current === generation) historyGeneration.current += 1; };
  }, [apiMode, authStatus, sessionKey, sessionIds]);

  const visibleGroups = useMemo(() => loaded.data.tasks.map(({ session, run }) => {
    if (!apiMode) return { session, runs: run === null ? [] : [run], state: null };
    const state = history[session.id];
    return { session, runs: mergeRuns(run === null ? [] : [run], state?.items ?? []), state: state ?? null };
  }), [apiMode, history, loaded.data.tasks]);
  const activeRunIds = visibleGroups.flatMap(({ runs }) => runs.filter(run => isActive(run) && !pollFailures[run.id]?.terminal).map((run) => run.id));
  const activeKey = activeRunIds.join('|');

  // 串行轮询所有活动批次；本轮请求结束后才安排下一轮，避免慢响应互相覆盖。
  useEffect(() => {
    if (!apiMode || authStatus !== 'authenticated' || activeRunIds.length === 0) return undefined;
    let cancelled = false;
    let timer: number | null = null;
    async function tick(): Promise<void> {
      const results = await Promise.allSettled(activeRunIds.map((id) => dataSource.getRun(id)));
      if (cancelled) return;
      const updates = results.flatMap((result) => result.status === 'fulfilled' ? [result.value] : []);
      if (updates.length > 0) {
        setHistory((current) => {
          const next = { ...current };
          for (const run of updates) {
            const state = next[run.session_id];
            if (state !== undefined) next[run.session_id] = { ...state, items: mergeRuns(state.items, [run]) };
          }
          return next;
        });
      }
      setPollFailures(current => {
        const next = { ...current };
        results.forEach((result, index) => {
          const id = activeRunIds[index]!;
          if (result.status === 'fulfilled') delete next[id];
          else next[id] = {
            message: describeError(result.reason),
            terminal: result.reason instanceof ApiError && [401, 403, 404, 410].includes(result.reason.status ?? 0),
          };
        });
        return next;
      });
      timer = window.setTimeout(() => void tick(), 2_000);
    }
    timer = window.setTimeout(() => void tick(), 2_000);
    return () => { cancelled = true; if (timer !== null) window.clearTimeout(timer); };
  }, [apiMode, activeKey, authStatus, sessionKey]);

  async function loadMoreRuns(sessionId: string): Promise<void> {
    const current = history[sessionId];
    if (current === undefined || current.nextCursor === null || current.loading) return;
    const generation = historyGeneration.current;
    setHistory((all) => ({ ...all, [sessionId]: { ...current, loading: true, error: null } }));
    try {
      const page = await listSessionRuns(sessionId, current.nextCursor);
      if (generation !== historyGeneration.current) return;
      if (page.next_cursor === current.nextCursor) throw new Error('历史分页没有继续，请重试读取分析历史。');
      setHistory((all) => ({ ...all, [sessionId]: { items: mergeRuns(page.items, all[sessionId]?.items ?? []), nextCursor: page.next_cursor, loading: false, error: null } }));
    } catch (error) {
      if (generation !== historyGeneration.current) return;
      setHistory((all) => ({ ...all, [sessionId]: { ...(all[sessionId] ?? current), loading: false, error: describeError(error) } }));
    }
  }

  async function retryHistory(sessionId: string): Promise<void> {
    const current = history[sessionId];
    if (!current || current.loading) return;
    const generation = historyGeneration.current;
    setHistory(all => ({ ...all, [sessionId]: { ...current, loading: true, error: null } }));
    try {
      const page = await listSessionRuns(sessionId);
      if (generation !== historyGeneration.current) return;
      setHistory(all => ({ ...all, [sessionId]: { items: mergeRuns(page.items, all[sessionId]?.items ?? []), nextCursor: page.next_cursor, loading: false, error: null } }));
    } catch (cause) {
      if (generation !== historyGeneration.current) return;
      setHistory(all => ({ ...all, [sessionId]: { ...(all[sessionId] ?? current), loading: false, error: describeError(cause) } }));
    }
  }

  function replaceRun(run: Run): void {
    setHistory((current) => {
      const state = current[run.session_id];
      if (state === undefined) return current;
      return { ...current, [run.session_id]: { ...state, items: mergeRuns(state.items, [run]) } };
    });
  }

  async function cancel(runId: string): Promise<void> {
    setBusyId(runId); setActionError(null);
    try { replaceRun(await cancelRun(runId)); loaded.reload(); }
    catch (error) { setActionError(describeError(error)); }
    finally { setBusyId(null); }
  }

  function runCard(session: Session, run: Run): ReactElement {
    return <section className="card task-card" key={run.id}>
      <div className="task-head"><div><h2>{session.title}</h2><p className="muted small">{formatDateTime(session.planned_start_at)} · {runModeLabel(run.mode)}</p></div><BadgeTag badge={runStatusBadge(run.status)}/></div>
      <StageFlow run={run}/>
      {run.error_code === null ? null : <RunErrorBox run={run}/>}
      {run.limitations.length === 0 ? null : <TechDetails summary="查看本次分析范围与限制"><p>{run.limitations.join('；')}</p></TechDetails>}
      <div className="task-footer"><span>阶段完成 {run.jobs.filter((job) => job.status === 'succeeded').length}/{run.stage_plan.length}</span><div className="page-actions">
        <a className="btn btn-secondary btn-sm" href={`#/sessions/${session.id}`}>查看课堂</a>
        {run.allowed_actions.includes('cancel') ? <button className="btn btn-secondary btn-sm" type="button" disabled={busyId === run.id} onClick={() => void cancel(run.id)}>{busyId === run.id ? '取消中…' : '取消任务'}</button> : null}
      </div></div>
    </section>;
  }

  const runCount = visibleGroups.reduce((count, group) => count + group.runs.length, 0);
  return <div className="page">
    <header className="page-head"><div><h1>分析任务</h1>
      </div>
      <a className="btn btn-primary" href="#/sessions"><IconUpload size={16}/>上传并分析</a>
    </header>
    <p className="notice-strip">分析历史按课堂分页恢复；活动批次每 2 秒自动刷新，全部进入终态后停止。</p>
    <LoadStateNote status={loaded.status} error={loaded.error}/>
    {actionError ? <p className="load-note load-note-error">{actionError}</p> : null}
    {Object.entries(pollFailures).map(([id, failure]) => <p className="load-note load-note-error" key={id}>任务状态未更新：{failure.message}{failure.terminal ? '；已停止自动刷新，请确认权限后重新进入页面。' : ''}</p>)}
    <div className="task-list">
      {visibleGroups.map(({ session, runs, state }) => <div className="task-group" key={session.id}>
        {runs.map((run) => runCard(session, run))}
        {state?.error ? <p className="load-note load-note-error">{session.title} 的历史批次读取失败：{state.error} <button type="button" className="btn btn-secondary btn-sm" disabled={state.loading} onClick={() => void retryHistory(session.id)}>重试读取分析历史</button></p> : null}
        {state?.loading && runs.length === 0 ? <p className="load-note">正在读取“{session.title}”的分析历史…</p> : null}
        {state?.nextCursor ? <button className="btn btn-secondary btn-sm" type="button" disabled={state.loading} onClick={() => void loadMoreRuns(session.id)}>{state.loading ? '加载中…' : `加载“${session.title}”更早批次`}</button> : null}
      </div>)}
    </div>
    {loaded.status === 'ready' && runCount === 0 && !visibleGroups.some(({ state }) => state?.loading || state?.error || state?.nextCursor) ? <section className="card"><EmptyState text="当前没有分析任务，可以从课堂档案上传录像并发起分析。"/></section> : null}
    <LoadControls actionLabel={loaded.actionLabel} loadMore={loaded.loadMore}/>
  </div>;
}
