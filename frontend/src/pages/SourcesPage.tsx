import { useEffect, useRef, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { BadgeTag, Card, EmptyState, LoadStateNote, Pill, TechDetails } from '../components/ui';
import { useSessionTasks } from '../data/hooks';
import { describeError } from '../data/errors';
import { isApiMode } from '../data/sources';
import { createSource, listSessionSources, verifySource } from '../data/m2Api';
import type { RightsStatus, SourceRecord, SourceUse } from '../data/m2Api';
import { uploadSessionMedia } from '../data/sessionOps';
import type { Badge } from '../format';
import { useAuth } from '../auth';

const useLabel: Record<SourceUse, string> = { playback: '回放', analysis: '分析', export: '导出' };
const sourceTypeLabel = { self_recorded: '本校录制', open_course: '公开课程', school_authorized: '学校授权' } as const;
const statusBadge: Record<RightsStatus, Badge> = {
  pending: { label: '待核验', tone: 'amber' }, verified: { label: '已核验', tone: 'green' }, rejected: { label: '已拒绝', tone: 'red' },
};

export function SourcesPage(): ReactElement {
  const sessions = useSessionTasks();
  const { capabilities } = useAuth();
  const [sessionId, setSessionId] = useState('');
  const [items, setItems] = useState<SourceRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [form, setForm] = useState<'create' | 'verify' | 'upload' | null>(null);
  const [target, setTarget] = useState<SourceRecord | null>(null);
  const requestGeneration = useRef(0);
  const selected = sessions.data.tasks.find((task) => task.session.id === sessionId)?.session ?? null;
  const canRegister = isApiMode() && capabilities?.includes('upload') === true;

  async function loadPage(cursor: string | undefined, replace: boolean): Promise<void> {
    if (!isApiMode() || sessionId === '') { setItems([]); setNextCursor(null); return; }
    const generation = ++requestGeneration.current;
    setLoading(true); setError(null);
    try {
      const page = await listSessionSources(sessionId, cursor);
      if (generation !== requestGeneration.current) return;
      setItems((current) => replace ? page.items : [...current, ...page.items.filter((item) => !current.some((old) => old.id === item.id))]);
      setNextCursor(page.next_cursor);
    }
    catch (cause) { if (generation === requestGeneration.current) setError(describeError(cause)); }
    finally { if (generation === requestGeneration.current) setLoading(false); }
  }
  async function reload(): Promise<void> { await loadPage(undefined, true); }
  useEffect(() => {
    requestGeneration.current += 1;
    setItems([]); setNextCursor(null); setError(null); setMessage(null);
    if (sessionId !== '') void loadPage(undefined, true);
  }, [sessionId]);

  async function submitCreate(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault(); const data = new FormData(event.currentTarget); setLoading(true); setError(null);
    const requested = (['playback', 'analysis', 'export'] as SourceUse[]).filter((use) => data.get(use) === 'on');
    try {
      await createSource({ session_id: sessionId, source_type: String(data.get('source_type')) as 'self_recorded', title: String(data.get('title')), attribution: String(data.get('attribution')), source_url: String(data.get('source_url')) || null, authorization_note: String(data.get('authorization_note')), requested_uses: requested, external_processing_requested: data.get('external') === 'on' });
      setForm(null); setMessage('素材来源已登记，核验前只能查看元信息和上传。'); await reload();
    } catch (cause) { setError(describeError(cause)); } finally { setLoading(false); }
  }

  async function submitVerify(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault(); if (target === null) return; const data = new FormData(event.currentTarget); setLoading(true); setError(null);
    const allowed = (['playback', 'analysis', 'export'] as SourceUse[]).filter((use) => data.get(use) === 'on');
    try { await verifySource(target.id, { rights_status: data.get('decision') as 'verified' | 'rejected', allowed_uses: data.get('decision') === 'verified' ? allowed : [], external_processing_allowed: data.get('decision') === 'verified' && data.get('external') === 'on', reason: String(data.get('reason')) }); setForm(null); setTarget(null); await reload(); }
    catch (cause) { setError(describeError(cause)); } finally { setLoading(false); }
  }

  async function submitUpload(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault(); if (target === null) return; const data = new FormData(event.currentTarget); const file = data.get('file');
    if (!(file instanceof File) || file.size === 0) { setError('请选择 MP4、MOV 或 MKV 录像文件。'); return; }
    setLoading(true); setError(null);
    try { const media = await uploadSessionMedia(sessionId, file, target.id); setMessage(`录像已上传，媒体 ${media.id} 正在等待检查。`); setForm(null); setTarget(null); }
    catch (cause) { setError(describeError(cause)); } finally { setLoading(false); }
  }

  return <div className="page">
    <header className="page-head"><div><p className="eyebrow">MEDIA &amp; SOURCES</p><h1>素材与来源</h1><p className="muted">为课堂素材保留来处、用途与使用限制。</p></div>
      {canRegister && sessionId !== '' ? <button className="btn btn-primary" type="button" onClick={() => setForm('create')}>登记新素材</button> : null}
    </header>
    {!isApiMode() ? <p className="notice-strip">固定样例不伪造来源记录；切换到接口模式后可登记、核验并上传真实录像。</p> : null}
    <Card title="选择课堂" subtitle="来源必须绑定课堂，不能用来源 URL 代替媒体上传。"><label className="account-field">课堂<select value={sessionId} onChange={(event) => { setSessionId(event.target.value); setForm(null); setTarget(null); }}><option value="">请选择课堂</option>{sessions.data.tasks.map((task) => <option key={task.session.id} value={task.session.id}>{task.session.title}</option>)}</select></label><LoadStateNote status={sessions.status} error={sessions.error}/></Card>
    {form === 'create' ? <Card title="登记新素材" subtitle="用途是申请范围，最终允许范围由核验结果决定。"><form className="source-form" onSubmit={(event) => void submitCreate(event)}><label className="account-field">素材类型<select name="source_type"><option value="self_recorded">本校录制</option><option value="school_authorized">学校授权</option><option value="open_course">公开课程</option></select></label><label className="account-field">素材标题<input name="title" required/></label><label className="account-field">署名 / 来源方<input name="attribution" required/></label><label className="account-field">来源链接<input name="source_url" type="url"/></label><label className="account-field">授权说明<input name="authorization_note" required/></label><fieldset className="source-uses"><legend>申请用途</legend>{(['playback','analysis','export'] as SourceUse[]).map((use) => <label className="check-row" key={use}><input type="checkbox" name={use}/>{useLabel[use]}</label>)}</fieldset><label className="check-row"><input type="checkbox" name="external"/>申请发送外部文本模型</label><div className="form-actions"><button className="btn btn-secondary" type="button" onClick={() => setForm(null)}>取消</button><button className="btn btn-primary" disabled={loading}>登记素材</button></div></form></Card> : null}
    {form === 'verify' && target !== null ? <Card title={`核验：${target.title}`}><form className="source-form" onSubmit={(event) => void submitVerify(event)}><label className="account-field">核验结论<select name="decision"><option value="verified">通过</option><option value="rejected">拒绝</option></select></label><fieldset className="source-uses"><legend>允许用途</legend>{target.requested_uses.map((use) => <label className="check-row" key={use}><input type="checkbox" name={use} defaultChecked/>{useLabel[use]}</label>)}</fieldset><label className="check-row"><input type="checkbox" name="external"/>允许发送外部文本模型</label><label className="account-field">核验理由<input name="reason" required/></label><div className="form-actions"><button className="btn btn-secondary" type="button" onClick={() => setForm(null)}>取消</button><button className="btn btn-primary" disabled={loading}>保存核验</button></div></form></Card> : null}
    {form === 'upload' && target !== null ? <Card title={`上传录像：${target.title}`} subtitle="上传后先进入 pending，媒体检查完成后才可播放或分析。"><form className="source-form" onSubmit={(event) => void submitUpload(event)}><label className="account-field">录像文件<input name="file" type="file" accept="video/mp4,video/quicktime,video/x-matroska,.mp4,.mov,.mkv" required/></label><div className="form-actions"><button className="btn btn-secondary" type="button" onClick={() => setForm(null)}>取消</button><button className="btn btn-primary" disabled={loading}>开始上传</button></div></form></Card> : null}
    {error ? <p className="load-note load-note-error">{error}</p> : null}{message ? <p className="notice-strip">{message}</p> : null}
    <Card title="课堂素材登记" subtitle={sessionId === '' ? '先选择课堂' : `${items.length} 条已加载记录`}>
      {loading && items.length === 0 ? <p className="load-note">正在读取数据…</p> : null}
      {sessionId !== '' && !loading && items.length === 0 ? <EmptyState text="该课堂尚未登记素材。"/> : null}
      {items.length > 0 ? <div className="table-wrap"><table className="table"><thead><tr><th>素材</th><th>来源</th><th>申请 / 允许用途</th><th>核验</th><th>外部处理</th><th>操作</th></tr></thead><tbody>{items.map((item) => <tr key={item.id}><td><strong>{item.title}</strong><br/><span className="muted small">{item.attribution}</span></td><td>{sourceTypeLabel[item.source_type]}<TechDetails summary="来源技术详情"><ul className="tech-list mono"><li>id = {item.id}</li><li>session_id = {item.session_id ?? 'null'}</li><li>source_type = {item.source_type}</li><li>source_url = {item.source_url ?? 'null'}</li><li>authorization_note = {item.authorization_note}</li><li>requested_uses = [{item.requested_uses.join(', ')}]</li><li>external_processing_requested = {String(item.external_processing_requested)}</li><li>rights_status = {item.rights_status}</li><li>allowed_uses = [{item.allowed_uses.join(', ')}]</li><li>external_processing_allowed = {String(item.external_processing_allowed)}</li><li>rights_version = {item.rights_version}</li><li>verified_by = {item.verified_by ?? 'null'}</li><li>verified_at = {item.verified_at ?? 'null'}</li><li>allowed_actions = [{item.allowed_actions.join(', ')}]</li></ul></TechDetails></td><td><div className="pill-row">{item.requested_uses.map((use) => <Pill key={`r-${use}`} label={`申请${useLabel[use]}`}/>)}</div><div className="pill-row">{item.allowed_uses.map((use) => <Pill key={`a-${use}`} label={`允许${useLabel[use]}`} tone="green"/>)}</div></td><td><BadgeTag badge={statusBadge[item.rights_status]}/></td><td><span className="muted small">申请：{item.external_processing_requested ? '是' : '否'}</span><br/>{item.external_processing_allowed ? <Pill label="已允许" tone="green"/> : <Pill label="未允许"/>}</td><td><div className="page-actions">{item.allowed_actions.includes('verify') ? <button className="text-button" type="button" onClick={() => { setTarget(item); setForm('verify'); }}>核验</button> : null}{item.allowed_actions.includes('upload') && selected?.allowed_actions.includes('upload') ? <button className="text-button" type="button" onClick={() => { setTarget(item); setForm('upload'); }}>上传录像</button> : null}</div></td></tr>)}</tbody></table></div> : null}
      {nextCursor === null ? null : <div className="table-footer"><span className="muted">还有更多来源记录</span><button className="btn btn-secondary btn-sm" type="button" disabled={loading} onClick={() => void loadPage(nextCursor, false)}>{loading ? '加载中…' : '加载更多'}</button></div>}
    </Card>
  </div>;
}
