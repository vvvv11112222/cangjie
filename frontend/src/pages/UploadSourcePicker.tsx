import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { listSessionSources } from '../data/m2Api';
import type { SourceRecord } from '../data/m2Api';
import { describeError } from '../data/errors';

export function UploadSourcePicker({ sessionId, value, onChange }: { sessionId: string; value: string; onChange(value: string): void }): ReactElement {
  const [rows, setRows] = useState<SourceRecord[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { let live = true; void listSessionSources(sessionId).then(page => { if (live) { setRows(page.items); setCursor(page.next_cursor); } }).catch(e => { if (live) setError(describeError(e)); }); return () => { live = false; }; }, [sessionId]);
  const eligible = rows.filter(row => row.allowed_actions.includes('upload'));
  async function more(): Promise<void> { if (!cursor || busy) return; setBusy(true); try { const page = await listSessionSources(sessionId, cursor); setRows(old => [...old, ...page.items.filter(p => !old.some(row => row.id === p.id))]); setCursor(page.next_cursor); } catch (e) { setError(describeError(e)); } finally { setBusy(false); } }
  return <div><label className="account-field"><span>选择已登记的录像来源</span><select value={eligible.some(row => row.id === value) ? value : ''} onChange={e => onChange(e.target.value)}><option value="">请选择来源，也可在下面填写来源编号</option>{eligible.map(row => <option key={row.id} value={row.id}>{row.title} · {row.rights_status === 'verified' ? row.allowed_uses.includes('analysis') ? '已核验，可分析' : '已核验，仅获准用途' : '待核验，暂不可分析'}</option>)}</select></label>{cursor && <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void more()}>读取更多来源</button>}{error && <p role="alert" className="load-note">来源列表读取失败：{error}。可重开上传窗口重试，或填写已知来源编号。</p>}<p className="field-hint">没有来源时，请先到“素材与来源”登记并完成核验。</p></div>;
}
