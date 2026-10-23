import type { FormEvent,ReactElement } from 'react';
import { useEffect,useRef,useState } from 'react';
import { useAuth } from '../auth';
import { BadgeTag,Card,EmptyState,ErrorMessage,LoadControls,LoadStateNote,Pill } from '../components/ui';
import { describeError } from '../data/errors';
import { useSessionTasks } from '../data/hooks';
import { isUuid } from '../data/ids';
import type { RightsStatus,SourceRecord,SourceUse } from '../data/m2Api';
import { createSource,listSessionSources,verifySource } from '../data/m2Api';
import { routeQuery,sessionDetailHash } from '../data/route';
import { uploadSessionMedia } from '../data/sessionOps';
import { isApiMode } from '../data/sources';
import type { Badge } from '../format';

const useLabel: Record<SourceUse, string> = { playback: '回放', analysis: '分析', export: '导出' };
const sourceTypeLabel = { self_recorded: '本校录制', open_course: '公开课程', school_authorized: '学校授权' } as const;
const statusBadge: Record<RightsStatus, Badge> = {
  pending: { label: '待核验', tone: 'amber' }, verified: { label: '已核验', tone: 'green' }, rejected: { label: '已拒绝', tone: 'red' },
};

export function SourcesPage(): ReactElement {
  const sessions = useSessionTasks();
  const { capabilities } = useAuth();
  const [sessionId, setSessionId] = useState(()=>{const id=routeQuery().get('session');return id && isUuid(id) ? id : '';});
  const [items, setItems] = useState<SourceRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [uploadedMedia, setUploadedMedia] = useState<string | null>(null);
  const [form, setForm] = useState<'create' | 'verify' | 'upload' | null>(null);
  const [target, setTarget] = useState<SourceRecord | null>(null);
  const requestGeneration = useRef(0);
  const selection = useRef({ id: sessionId });
  if (selection.current.id !== sessionId) selection.current = { id: sessionId };
  const selectionVersion = selection.current;
  const isCurrent = () => selection.current === selectionVersion;
  useEffect(() => () => { selection.current = { id: '' }; requestGeneration.current += 1; }, []);
  useEffect(()=>{
    const change=()=>{
      if(window.location.hash.split('?')[0]!=='#/sources')return;
      const id=routeQuery().get('session');
      if(id && isUuid(id)){setSessionId(id);setForm(null);setTarget(null);setUploadedMedia(null);setMessage(null);setError(null);}
    };
    window.addEventListener('hashchange',change);return()=>window.removeEventListener('hashchange',change);
  },[]);
  const selected = sessions.data.tasks.find((task) => task.session.id === sessionId)?.session ?? null;
  const canRegister = isApiMode() && capabilities?.includes('upload') === true;

  async function loadPage(cursor: string | undefined, replace: boolean): Promise<void> {
    if (!isCurrent()) return;
    if (!isApiMode() || sessionId === '') { setItems([]); setNextCursor(null); return; }
    const generation = ++requestGeneration.current;
    setLoading(true); setError(null);
    try {
      const page = await listSessionSources(sessionId, cursor);
      if (!isCurrent() || generation !== requestGeneration.current) return;
      setItems((current) => replace ? page.items : [...current, ...page.items.filter((item) => !current.some((old) => old.id === item.id))]);
      setNextCursor(page.next_cursor);
    }
    catch (cause) { if (isCurrent() && generation === requestGeneration.current) setError(describeError(cause)); }
    finally { if (isCurrent() && generation === requestGeneration.current) setLoading(false); }
  }
  async function reload(): Promise<void> { await loadPage(undefined, true); }
  useEffect(() => {
    requestGeneration.current += 1;
    setItems([]); setNextCursor(null); setError(null); setMessage(null); setUploadedMedia(null); setLoading(false);
    if (sessionId !== '') void loadPage(undefined, true);
  }, [sessionId]);

  async function submitCreate(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault(); if (loading) return; const data = new FormData(event.currentTarget); setLoading(true); setError(null);
    const requested = (['playback', 'analysis', 'export'] as SourceUse[]).filter((use) => data.get(use) === 'on');
    try {
      await createSource({ session_id: sessionId, source_type: String(data.get('source_type')) as 'self_recorded', title: String(data.get('title')), attribution: String(data.get('attribution')), source_url: String(data.get('source_url')) || null, authorization_note: String(data.get('authorization_note')), requested_uses: requested, external_processing_requested: data.get('external') === 'on' });
      if (!isCurrent()) return;
      setForm(null); setMessage('来源已登记。下一步由学院教务或系统管理员根据真实许可核验；核验前可上传文件，不能播放或分析。'); await reload();
    } catch (cause) { if (isCurrent()) setError(describeError(cause)); } finally { if (isCurrent()) setLoading(false); }
  }

  async function submitVerify(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault(); if (target === null || loading) return; const data = new FormData(event.currentTarget); setLoading(true); setError(null);
    const allowed = (['playback', 'analysis', 'export'] as SourceUse[]).filter((use) => data.get(use) === 'on');
    try { await verifySource(target.id, { rights_status: data.get('decision') as 'verified' | 'rejected', allowed_uses: data.get('decision') === 'verified' ? allowed : [], external_processing_allowed: data.get('decision') === 'verified' && data.get('external') === 'on', reason: String(data.get('reason')) }); if (!isCurrent()) return; setForm(null); setTarget(null); setMessage('核验结果已保存。下一步返回这节课堂，上传或选择录像并选择获准的分析方式。'); await reload(); }
    catch (cause) { if (isCurrent()) setError(describeError(cause)); } finally { if (isCurrent()) setLoading(false); }
  }

  async function submitUpload(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault(); if (target === null || loading) return; const data = new FormData(event.currentTarget); const file = data.get('file');
    if (!(file instanceof File) || file.size === 0) { setError('请选择 MP4、MOV 或 MKV 录像文件。'); return; }
    setLoading(true); setError(null);
    try { const media = await uploadSessionMedia(sessionId, file, target.id); if (!isCurrent()) return; setUploadedMedia(media.id); setMessage('录像已上传，尚未开始分析。前往课堂详情选择“举手与低头识别”或“语音转写与教学报告”；只需回放时选择“媒体准备”。'); setForm(null); setTarget(null); }
    catch (cause) { if (isCurrent()) setError(describeError(cause)); } finally { if (isCurrent()) setLoading(false); }
  }

  return <div className="page">
    <header className="page-head"><div><h1>素材与来源</h1></div>
      {canRegister && sessionId !== '' ? <button className="btn btn-primary" type="button" disabled={loading} onClick={() => setForm('create')}>登记新素材</button> : null}
    </header>
    {!isApiMode() ? <p className="notice-strip">固定样例不伪造来源记录；切换到接口模式后可登记、核验并上传真实录像。</p> : null}
    <Card title="选择课堂" ><label className="account-field">课堂<select value={sessionId} onChange={(event) => { setSessionId(event.target.value); setForm(null); setTarget(null); setUploadedMedia(null); setMessage(null); setError(null); window.history.replaceState(null,'',`#/sources?session=${encodeURIComponent(event.target.value)}`); }}><option value="">请选择课堂</option>{sessions.data.tasks.map((task) => <option key={task.session.id} value={task.session.id}>{task.session.title}</option>)}</select></label><LoadStateNote status={sessions.status} error={sessions.error}/><LoadControls actionLabel={sessions.actionLabel} loadMore={sessions.loadMore}/></Card>
    {form === 'create' ? <Card title="登记新素材" ><form className="source-form" onSubmit={(event) => void submitCreate(event)}><label className="account-field">素材类型<select name="source_type"><option value="self_recorded">本校录制</option><option value="school_authorized">学校授权</option><option value="open_course">公开课程</option></select></label><label className="account-field">素材标题<input name="title" required/></label><label className="account-field">署名 / 来源方<input name="attribution" required/></label><label className="account-field">来源链接<input name="source_url" type="url"/></label><label className="account-field">授权说明<input name="authorization_note" placeholder="按实际情况填写：谁授权、允许什么用途、适用范围" required/></label><fieldset className="source-uses"><legend>申请用途</legend>{(['playback','analysis','export'] as SourceUse[]).map((use) => <label className="check-row" key={use}><input type="checkbox" name={use}/>{useLabel[use]}</label>)}</fieldset><label className="check-row"><input type="checkbox" name="external"/>申请云端音频识别与文本分析</label><div className="form-actions"><button className="btn btn-secondary" type="button" disabled={loading} onClick={() => setForm(null)}>取消</button><button className="btn btn-primary" disabled={loading}>{loading ? '登记中…' : '登记素材'}</button></div></form></Card> : null}
    {form === 'verify' && target !== null ? <Card key={`verify:${target.id}`} title={`核验：${target.title}`}><form className="source-form" onSubmit={(event) => void submitVerify(event)}><label className="account-field">核验结论<select name="decision"><option value="verified">通过</option><option value="rejected">拒绝</option></select></label><fieldset className="source-uses"><legend>允许用途</legend>{target.requested_uses.map((use) => <label className="check-row" key={use}><input type="checkbox" name={use} defaultChecked/>{useLabel[use]}</label>)}</fieldset>{target.external_processing_requested && <label className="check-row"><input type="checkbox" name="external"/>允许云端音频识别与文本分析</label>}<label className="account-field">核验理由<input name="reason" required/></label><div className="form-actions"><button className="btn btn-secondary" type="button" disabled={loading} onClick={() => setForm(null)}>取消</button><button className="btn btn-primary" disabled={loading}>{loading ? '保存中…' : '保存核验'}</button></div></form></Card> : null}
    {form === 'upload' && target !== null ? <Card key={`upload:${target.id}`} title={`上传录像：${target.title}`} ><form className="source-form" onSubmit={(event) => void submitUpload(event)}><label className="account-field">录像文件<input name="file" type="file" accept="video/mp4,video/quicktime,video/x-matroska,.mp4,.mov,.mkv" required/></label><div className="form-actions"><button className="btn btn-secondary" type="button" disabled={loading} onClick={() => setForm(null)}>取消</button><button className="btn btn-primary" disabled={loading}>{loading ? '上传中…' : '开始上传'}</button></div></form></Card> : null}
    {error ? <div className="notice-strip"><ErrorMessage text={error}/>{sessionId !== '' ? <button type="button" className="btn btn-secondary btn-sm" disabled={loading} onClick={() => void reload()}>{loading ? '重新读取中…' : '重新读取素材'}</button> : null}</div> : null}{message ? <p className="notice-strip" role="status">{message}</p> : null}
    {sessionId && <a className="text-button" href={sessionDetailHash(sessionId, uploadedMedia ? {mediaId:uploadedMedia} : {})}>{uploadedMedia ? '继续：为这段录像选择分析方式' : '返回这节课堂继续操作'}</a>}
    <Card guideId="source-list" title="课堂素材登记" subtitle={sessionId === '' ? '先选择课堂' : `${items.length} 条已加载记录`}>
      {loading && items.length === 0 ? <p className="load-note">正在读取数据…</p> : null}
      {sessionId !== '' && !loading && !error && items.length === 0 ? <EmptyState text="该课堂尚未登记素材。"/> : null}
      {items.length > 0 ? <div className="table-wrap"><table className="table"><thead><tr><th>素材</th><th>来源</th><th>申请 / 允许用途</th><th>核验</th><th>外部处理</th><th>操作</th></tr></thead><tbody>{items.map((item) => <tr key={item.id}><td><strong>{item.title}</strong><br/><span className="muted small">{item.attribution}</span></td><td>{sourceTypeLabel[item.source_type]}</td><td><div className="pill-row">{item.requested_uses.map((use) => <Pill key={`r-${use}`} label={`申请${useLabel[use]}`}/>)}</div><div className="pill-row">{item.allowed_uses.map((use) => <Pill key={`a-${use}`} label={`允许${useLabel[use]}`} tone="green"/>)}</div></td><td><BadgeTag badge={statusBadge[item.rights_status]}/></td><td><span className="muted small">申请：{item.external_processing_requested ? '是' : '否'}</span><br/>{item.external_processing_allowed ? <Pill label="已允许" tone="green"/> : <Pill label="未允许"/>}</td><td><div className="page-actions">{item.allowed_actions.includes('verify') ? <button className="text-button" type="button" disabled={loading} onClick={() => { setTarget(item); setForm('verify'); }}>核验</button> : null}{item.allowed_actions.includes('upload') && selected?.allowed_actions.includes('upload') ? <button className="text-button" type="button" disabled={loading} onClick={() => { setTarget(item); setForm('upload'); }}>上传录像</button> : null}</div></td></tr>)}</tbody></table></div> : null}
      {nextCursor === null ? null : <div className="table-footer"><span className="muted">还有更多来源记录</span><button className="btn btn-secondary btn-sm" type="button" disabled={loading} onClick={() => void loadPage(nextCursor, false)}>{loading ? '加载中…' : '加载更多'}</button></div>}
    </Card>
  </div>;
}
