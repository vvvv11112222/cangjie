import { useEffect, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { BadgeTag, Card, EmptyState, IdChip, TechDetails } from '../components/ui';
import { IconAlert, IconInfo, IconSettings } from '../components/icons';
import { describeError } from '../data/errors';
import { getOperations, listModelCalls, reconcileModelCall } from '../data/m4Api';
import { isApiMode } from '../data/sources';
import {
  formatDateTime,
  modelCallStatusBadge,
  workerCapabilityLabel,
} from '../format';
import type { ModelCall, Operations, ReconcileCall } from '../types';
import { useAuth } from '../auth';
import { ModelSettingsPanel } from './ModelSettingsPanel';

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unit = 'B';
  for (const candidate of units) {
    value /= 1024;
    unit = candidate;
    if (value < 1024) break;
  }
  return `${value.toFixed(value >= 100 ? 0 : 1)} ${unit}`;
}

function money(value: string): string {
  return `¥ ${value}`;
}

function BudgetPanel({ operations }: { operations: Operations }): ReactElement {
  const { budget } = operations;
  const limit = Number(budget.limit);
  const committed = Number(budget.charged) + Number(budget.reserved) + Number(budget.unknown);
  const percent = limit <= 0 ? 100 : Math.min(100, (committed / limit) * 100);
  return (
    <Card title="模型预算" subtitle={`${budget.billing_period} 起算 · ${budget.currency}`}>
      <div className="operations-budget-total">
        <strong>{money(budget.charged)}</strong>
        <span>已结算 / 限额 {money(budget.limit)}</span>
      </div>
      <div className="progress" aria-label={`预算占用约 ${Math.round(percent)}%`}>
        <span className="progress-fill" style={{ width: `${percent}%` }} />
      </div>
      <div className="operations-budget-breakdown">
        <div><span>已结算</span><strong>{money(budget.charged)}</strong></div>
        <div><span>已预留</span><strong>{money(budget.reserved)}</strong></div>
        <div><span>计费未知</span><strong>{money(budget.unknown)}</strong></div>
      </div>
      {Number(budget.unknown) > 0 ? (
        <p className="notice-strip">
          <IconAlert size={14} aria-hidden="true" />
          存在计费状态未知的调用；这部分仍占用额度，应先核对账单，不要直接重试模型任务。
        </p>
      ) : null}
    </Card>
  );
}

function ReconcileForm({ call, busy, onCancel, onSubmit }: {
  call: ModelCall;
  busy: boolean;
  onCancel(): void;
  onSubmit(input: ReconcileCall): Promise<void>;
}): ReactElement {
  const [status, setStatus] = useState<ReconcileCall['status']>('failed');
  const [actualCost, setActualCost] = useState(call.actual_cost ?? '0.000000');
  const [providerRequestId, setProviderRequestId] = useState('');
  const [reason, setReason] = useState('');

  function submit(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    void onSubmit({
      lock_version: call.lock_version,
      status,
      actual_cost: actualCost,
      provider_request_id: providerRequestId.trim() === '' ? null : providerRequestId.trim(),
      reason: reason.trim(),
    });
  }

  return (
    <form className="m1-form operations-reconcile" onSubmit={submit}>
      <label className="account-field"><span>核对结果</span><select value={status} onChange={(event) => setStatus(event.target.value as ReconcileCall['status'])}><option value="failed">调用失败</option><option value="succeeded">调用成功</option></select></label>
      <label className="account-field"><span>实际费用（CNY，6 位小数）</span><input value={actualCost} onChange={(event) => setActualCost(event.target.value)} inputMode="decimal" pattern="[0-9]+\.[0-9]{6}" required /></label>
      <label className="account-field"><span>供应商请求编号（可空）</span><input value={providerRequestId} onChange={(event) => setProviderRequestId(event.target.value)} /></label>
      <label className="account-field"><span>核对说明</span><input value={reason} onChange={(event) => setReason(event.target.value)} required /></label>
      <div className="form-actions">
        <button type="button" className="btn btn-secondary" disabled={busy} onClick={onCancel}>取消</button>
        <button type="submit" className="btn btn-primary" disabled={busy || reason.trim() === ''}>{busy ? '提交中…' : '提交核对'}</button>
      </div>
    </form>
  );
}

export function OperationsPage(): ReactElement {
  const {me,sessionKey}=useAuth();
  const api = isApiMode();
  const [operations, setOperations] = useState<Operations | null>(null);
  const [calls, setCalls] = useState<ModelCall[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [operationsLoading, setOperationsLoading] = useState(api);
  const [callsLoading, setCallsLoading] = useState(api);
  const loading = operationsLoading || callsLoading;
  const [loadingMore, setLoadingMore] = useState(false);
  const [operationsError, setOperationsError] = useState<string | null>(null);
  const [callsError, setCallsError] = useState<string | null>(null);
  const [selected, setSelected] = useState<ModelCall | null>(null);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const refreshDisabled = loading || saving || loadingMore;

  useEffect(() => {
    if (!api) return;
    let active = true;
    setOperationsLoading(true); setCallsLoading(true);
    setOperationsError(null); setCallsError(null); setMessage(null); setSelected(null);
    void getOperations().then((value) => { if (active) setOperations(value); })
      .catch((error: unknown) => { if (active) setOperationsError(describeError(error)); })
      .finally(() => { if (active) setOperationsLoading(false); });
    void listModelCalls(undefined, 20).then((page) => {
      if (active) { setCalls(page.items); setNextCursor(page.next_cursor); }
    }).catch((error: unknown) => {
      if (active) setCallsError(describeError(error));
    }).finally(() => { if (active) setCallsLoading(false); });
    return () => { active = false; };
  }, [api, reloadKey]);

  async function loadMore(): Promise<void> {
    if (nextCursor === null || loadingMore) return;
    setLoadingMore(true); setCallsError(null);
    try {
      const page = await listModelCalls(nextCursor, 20);
      setCalls((items) => [...new Map([...items, ...page.items].map((item) => [item.id, item])).values()]);
      setNextCursor(page.next_cursor);
    } catch (error) {
      setCallsError(describeError(error));
    } finally {
      setLoadingMore(false);
    }
  }

  async function reconcile(input: ReconcileCall): Promise<void> {
    if (selected === null) return;
    setSaving(true); setCallsError(null); setMessage(null);
    try {
      const next = await reconcileModelCall(selected.id, input);
      setCalls((items) => items.map((item) => item.id === next.id ? next : item));
      setSelected(null);
      setMessage('模型调用账目已核对，预算汇总需要重新读取。');
      try {
        setOperations(await getOperations()); setOperationsError(null);
      } catch (error) {
        setOperationsError(describeError(error));
      }
    } catch (error) {
      setCallsError(describeError(error));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <p className="eyebrow">WORKSPACE SETTINGS</p>
          <h1>运行与设置</h1>
          <p className="muted">查看工作空间的任务、存储、Worker 与模型预算状态。</p>
        </div>
        {api ? <button type="button" className="btn btn-secondary" disabled={refreshDisabled} onClick={() => setReloadKey((value) => value + 1)}>刷新状态</button> : null}
      </header>

      <p className="notice-strip">
        <IconInfo size={14} aria-hidden="true" />
        模型与共享预算可在下方配置，空闲时保存并生效。数据保留期仍由部署配置管理。
      </p>

      {!api ? (
        <Card title="需要接口模式" subtitle="固定样例没有运维数据，不能伪造 Worker、存储或真实费用。">
          <EmptyState text="设置 VITE_DATA_SOURCE=api 并登录系统管理员账号后查看运行状态。" />
        </Card>
      ) : null}

      {api && me?.roles?.some(role=>role.role_code==='sys_admin') && <ModelSettingsPanel key={sessionKey} onApplied={()=>setReloadKey(value=>value+1)}/>}
      {operationsLoading ? <p className="load-note">正在读取运行状态……</p> : null}
      {operationsError ? <div className="notice-strip"><span className="load-note-error">运行状态读取失败：{operationsError}</span><button type="button" className="btn btn-secondary btn-sm" disabled={refreshDisabled} onClick={() => setReloadKey((value) => value + 1)}>重新读取</button></div> : null}
      {operations !== null ? (
        <>
          <div className="operations-stats">
            <Card title="排队任务"><strong className="operations-stat-value">{operations.queued_jobs}</strong><p className="muted small">等待执行的分析阶段</p></Card>
            <Card title="Worker 记录"><strong className="operations-stat-value">{operations.workers.length}</strong><p className="muted small">服务端登记的处理节点</p></Card>
            <Card title="可用存储"><strong className="operations-stat-value">{formatBytes(operations.storage_free_bytes)}</strong><p className="muted small">由存储后端实时报告</p></Card>
          </div>
          <div className="operations-grid">
            <BudgetPanel operations={operations} />
            <Card title="Worker 状态" subtitle="只展示最后活动时间，不自行推断在线或离线。" icon={<IconSettings size={18} />}>
              {operations.workers.length === 0 ? <EmptyState text="尚无 Worker 活动记录。" /> : (
                <div className="table-wrap"><table className="table"><thead><tr><th>Worker</th><th>能力</th><th>最后活动</th></tr></thead><tbody>{operations.workers.map((worker) => <tr key={worker.worker_id}><td><strong>{worker.worker_id}</strong></td><td>{worker.capabilities.map(workerCapabilityLabel).join('、')}</td><td>{formatDateTime(worker.last_seen_at)}</td></tr>)}</tbody></table></div>
              )}
            </Card>
          </div>
        </>
      ) : null}

      {api ? (
        <Card title="模型调用账目" subtitle="unknown 表示费用结果不确定，必须由系统管理员查账后处理。">
          {callsLoading ? <p className="load-note">正在读取模型账目……</p> : null}
          {message ? <p className="notice-strip">{message}</p> : null}
          {callsError ? <div className="notice-strip"><span className="load-note-error">{callsError}</span><button type="button" className="btn btn-secondary btn-sm" disabled={refreshDisabled} onClick={() => setReloadKey((value) => value + 1)}>重新读取账目</button></div> : null}
          <div className="table-wrap"><table className="table"><thead><tr><th>调用</th><th>状态</th><th>预留 / 实际费用</th><th>账期 / 价格版本</th><th>发送 / 结算</th><th>操作</th></tr></thead><tbody>{calls.map((call) => <tr key={call.id}><td><IdChip id={call.id} /></td><td><BadgeTag badge={modelCallStatusBadge(call.status)} /></td><td>{money(call.reserved_cost)}<br/><span className="muted small">实际 {call.actual_cost === null ? '待确认' : money(call.actual_cost)}</span></td><td>{call.billing_period}<br/><span className="muted small">{call.price_version ?? '未记录价格版本'}</span></td><td>{call.dispatch_started_at === null ? '未发送' : formatDateTime(call.dispatch_started_at)}<br/><span className="muted small">{call.settled_at === null ? '尚未结算' : formatDateTime(call.settled_at)}</span></td><td>{call.status === 'unknown' ? <button type="button" className="btn btn-secondary btn-sm" disabled={saving} onClick={() => setSelected(call)}>核对账目</button> : '—'}</td></tr>)}</tbody></table></div>
          {calls.length === 0 && !callsLoading && callsError === null ? <EmptyState text="当前没有模型调用记录。" /> : null}
          {nextCursor !== null ? <button type="button" className="btn btn-secondary btn-sm" disabled={loadingMore || callsLoading} onClick={() => void loadMore()}>{loadingMore ? '加载中…' : '加载更多账目'}</button> : null}
          {selected === null ? null : (
            <div className="operations-reconcile-shell">
              <h3>核对调用 <IdChip id={selected.id} /></h3>
              <p className="muted small">提交携带当前 lock_version；若后台记录已变化，服务端返回版本冲突，页面不会覆盖新数据。</p>
              <ReconcileForm key={selected.id} call={selected} busy={saving} onCancel={() => setSelected(null)} onSubmit={reconcile} />
              <TechDetails><p className="mono">model_call.lock_version = {selected.lock_version}</p></TechDetails>
            </div>
          )}
        </Card>
      ) : null}
    </div>
  );
}
