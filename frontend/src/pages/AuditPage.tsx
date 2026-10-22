import { useEffect, useRef, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { useAuth } from '../auth';
import { BadgeTag, Card, IdChip, TechDetails } from '../components/ui';
import { listAudit } from '../data/auditApi';
import type { AuditRecord } from '../data/auditApi';
import { describeError } from '../data/errors';
import { isApiMode } from '../data/sources';
import { formatDateTime } from '../format';

const ACTIONS: Record<string, string> = {
  'academic.delete': '删除未使用资料', 'password.change': '修改本人密码', 'password.reset': '重置账号密码',
  'model.asr.settle': '语音费用结算',
  'report.publish': '发布报告', 'report.withdraw': '撤回报告', 'report.edit': '编辑报告',
  'report.review': '复核报告', 'report.revise': '创建报告修订', 'source.verify': '核验录像来源',
  'report.export.request': '申请导出报告', 'report.export.complete': '报告导出完成', 'report.export.download': '下载报告',
  'classroom.delete.request': '申请课堂清理', 'classroom.delete.complete': '课堂清理完成', 'model_call.reconcile': '核对模型费用',
};
const RESOURCES: Record<string, string> = { analysis_run: '分析记录', user: '账号', source: '录像来源', lesson_session: '课堂', report: '报告', report_export: '报告导出', model_call: '模型调用', 'org-units': '组织', terms: '学期', courses: '课程', 'class-groups': '班级', classrooms: '教室', offerings: '开课', schedules: '课表' };
export function AuditPage(): ReactElement {
  const { status, me, sessionKey } = useAuth();
  const api = isApiMode();
  const allowed = me?.roles.some(role => ['sys_admin', 'academic_admin', 'supervisor'].includes(role.role_code)) ?? false;
  const [action, setAction] = useState(''); const [result, setResult] = useState('');
  const [filters, setFilters] = useState({ action: '', result: '' });
  const [reload, setReload] = useState(0);
  const scope = `${sessionKey}:${filters.action}:${filters.result}:${reload}`;
  const [data, setData] = useState<{ scope: string; rows: AuditRecord[]; next: string | null } | null>(null);
  const [busy, setBusy] = useState(false); const [error, setError] = useState<string | null>(null);
  const generation = useRef(0); const pending = useRef(false);
  const current = data?.scope === scope ? data : null;
  useEffect(() => {
    const id = ++generation.current; pending.current = false; setData(null); setError(null);
    if (!api || status !== 'authenticated' || !allowed) { setBusy(false); return; }
    pending.current = true; setBusy(true);
    void listAudit(filters).then(page => { if (id === generation.current) setData({ scope, rows: page.items, next: page.next_cursor }); })
      .catch(reason => { if (id === generation.current) setError(describeError(reason)); })
      .finally(() => { if (id === generation.current) { pending.current = false; setBusy(false); } });
    return () => { generation.current += 1; };
  }, [scope, status, allowed, api]);
  async function more(): Promise<void> {
    if (!current?.next || pending.current) return;
    const id = generation.current; pending.current = true; setBusy(true); setError(null);
    try {
      const page = await listAudit({ ...filters, cursor: current.next });
      if (id !== generation.current) return;
      if (page.next_cursor === current.next) throw new Error('分页没有继续，请刷新后重试。');
      setData({ scope, rows: [...current.rows, ...page.items.filter(row => !current.rows.some(old => old.id === row.id))], next: page.next_cursor });
    } catch (reason) { if (id === generation.current) setError(describeError(reason)); }
    finally { if (id === generation.current) { pending.current = false; setBusy(false); } }
  }
  function search(event: FormEvent): void { event.preventDefault(); setFilters({ action: action.trim(), result }); setReload(value => value + 1); }
  return <div className="page"><header className="page-head"><div><p className="eyebrow">ACTIVITY LOG</p><h1>操作记录</h1><p className="muted">查看系统已保存的管理、来源核验、报告复核与清理记录。记录只读，不能在这里删除。</p></div></header>
    {!api ? <p className="notice-strip">演示模式不显示真实操作记录，请使用团队系统登录查看。</p> : status !== 'authenticated' ? <p className="notice-strip">{status === 'checking' ? '正在检查登录状态…' : '请先登录后查看操作记录。'}</p> : !allowed ? <p className="notice-strip">当前账号没有操作记录查询权限。</p> : <Card title="已保存的操作记录" subtitle={me?.roles.some(role => role.role_code === 'sys_admin') ? '系统管理员可查看全校记录。未纳入记录的操作不会补造。' : '当前只显示你本人执行的操作，不显示其他人员或学院的记录。'}>
      <form className="field-grid" onSubmit={search}><label className="account-field"><span>操作类型</span><select value={action} onChange={e => setAction(e.target.value)}><option value="">全部类型</option>{Object.entries(ACTIONS).map(([code, label]) => <option key={code} value={code}>{label}</option>)}</select></label><label className="account-field"><span>处理结果</span><select value={result} onChange={e => setResult(e.target.value)}><option value="">全部结果</option><option value="success">成功</option><option value="denied">已拒绝</option><option value="failure">失败</option></select></label><div className="form-actions"><button className="btn btn-primary" disabled={busy}>查询</button><button type="button" className="btn btn-secondary" disabled={busy} onClick={() => setReload(value => value + 1)}>刷新记录</button></div></form>
      {error && <p role="alert" className="load-note load-note-error">读取失败：{error}</p>}
      {current?.rows.length ? <div className="table-wrap"><table className="table"><thead><tr><th>时间</th><th>操作人</th><th>操作</th><th>对象</th><th>结果</th></tr></thead><tbody>{current.rows.map(row => <tr key={row.id}><td>{formatDateTime(row.created_at)}</td><td>{row.actor_name ?? '系统任务'}</td><td>{ACTIONS[row.action] ?? '其他操作'}</td><td>{RESOURCES[row.resource_type] ?? '系统资源'} {row.resource_id && <IdChip id={row.resource_id}/>}<TechDetails><span>记录 {row.id} · {row.action}</span></TechDetails></td><td><BadgeTag badge={{ label: { success: '成功', denied: '已拒绝', failure: '失败' }[row.result], tone: row.result === 'success' ? 'green' : 'red' }}/></td></tr>)}</tbody></table></div> : current && !error && !busy ? <p className="empty">没有符合条件的已保存记录。</p> : null}
      {busy && <p role="status">正在读取操作记录…</p>}
      {current?.next && <button className="btn btn-secondary" disabled={busy} onClick={() => void more()}>加载更早记录</button>}
      {error && !current && <button className="btn btn-secondary" disabled={busy} onClick={() => setReload(value => value + 1)}>重试读取记录</button>}
    </Card>}
  </div>;
}
