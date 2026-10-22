import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { listSessionSources } from '../data/m2Api';
import type { SourceRecord } from '../data/m2Api';
import { describeError } from '../data/errors';
import { SourceSetup } from '../components/SourceSetup';
import { useAuth } from '../auth';

export function UploadSourcePicker({ sessionId, value, onChange, manualFallback = true, disabled = false }: {
  sessionId: string; value: string; onChange(value: string): void;
  manualFallback?: boolean; disabled?: boolean;
}): ReactElement {
  const [rows, setRows] = useState<SourceRecord[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [reload, setReload] = useState(0);
  const { capabilities } = useAuth();
  const [setup, setSetup] = useState<'register' | 'verify' | null>(null);
  const selected = rows.find(row => row.id === value);
  useEffect(() => {
    let live = true; setRows([]); setCursor(null); setError(null); setLoading(true);
    void listSessionSources(sessionId).then(page => {
      if (live) { setRows(page.items); setCursor(page.next_cursor); }
    }).catch(e => { if (live) setError(describeError(e)); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [sessionId, reload]);
  const eligible = rows.filter(row => row.session_id === sessionId && row.allowed_actions.includes('upload'));
  async function more(): Promise<void> {
    if (!cursor || busy || disabled) return;
    setBusy(true); setError(null);
    try {
      const page = await listSessionSources(sessionId, cursor);
      if (page.next_cursor === cursor) throw new Error('来源分页没有前进，请重新读取。');
      setRows(old => [...old, ...page.items.filter(p => !old.some(row => row.id === p.id))]); setCursor(page.next_cursor);
    } catch (e) { setError(describeError(e)); } finally { setBusy(false); }
  }
  return <div>
    <label className="account-field"><span>选择已登记的录像来源</span>
      <select disabled={disabled || loading} value={eligible.some(row => row.id === value) ? value : ''} onChange={e => {setSetup(null);onChange(e.target.value);}}>
        <option value="">{loading ? '正在读取录像来源…' : manualFallback ? '请选择来源，也可在下面填写来源编号' : '请选择录像来源（无需填写编号）'}</option>
        {eligible.map(row => <option key={row.id} value={row.id}>{row.title} · {row.rights_status === 'verified' ? row.allowed_uses.includes('analysis') ? '已核验，可分析' : '已核验，仅获准用途' : '待核验，暂不可分析'}</option>)}
      </select>
    </label>
    {cursor && <button type="button" className="btn btn-secondary" disabled={disabled || busy} onClick={() => void more()}>读取更多来源</button>}
    {error && <><p role="alert" className="load-note">来源列表读取失败：{error}。{manualFallback ? '可重新读取，或填写已知来源编号。' : '请重新读取后选择来源。'}</p><button type="button" className="btn btn-secondary" disabled={disabled || busy || loading} onClick={() => setReload(n => n + 1)}>重新读取来源</button></>}
    {!loading && !error && !eligible.length && !cursor && <p className="field-hint">这节课堂还没有可上传的来源，请先到“素材与来源”登记。</p>}
    <p className="field-hint">已有来源直接选择，也可以在这里登记；文件和已填写的上传信息会保留。</p>
    <div className="action-row">{capabilities?.includes('upload') && <button type="button" className="btn btn-secondary" disabled={disabled || loading} onClick={()=>{onChange('');setSetup('register');}}>在此登记新来源</button>}{selected?.allowed_actions.includes('verify') && <button type="button" className="btn btn-secondary" disabled={disabled || loading} onClick={()=>setSetup('verify')}>在此核验来源</button>}</div>
    {selected && selected.rights_status !== 'verified' && <p className="onboarding-next">{selected.rights_status === 'rejected' ? '该来源核验未通过。' : '来源待核验：上传后仍需等待学院教务或系统管理员核验，才可分析或回放。'}{!selected.allowed_actions.includes('verify') && '请联系本课堂所属学院教务或系统管理员处理。'}</p>}
    {setup && <SourceSetup key={`${sessionId}:${setup}:${setup === 'verify' ? selected?.id : ''}`} sessionId={sessionId} source={setup === 'verify' ? selected : undefined} disabled={disabled} onClose={()=>setSetup(null)} onSaved={saved=>{setRows(current=>[saved,...current.filter(row=>row.id!==saved.id)]);onChange(saved.id);setSetup(null);}}/>}
  </div>;
}
