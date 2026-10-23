import { useEffect,useRef,useState } from 'react';
import { describeError } from '../data/errors';
import type { SourceRecord,SourceType,SourceUse } from '../data/m2Api';
import { createSource,verifySource } from '../data/m2Api';
import { ErrorMessage } from './ui';

const useNames: Record<SourceUse,string> = { playback: '回放（播放录像与核对证据）', analysis: '分析（识别语音或画面）', export: '导出（下载报告等成果）' };

// Deliberately no nested form: these fields can live inside the existing upload
// form without submitting it. A source registration never verifies its rights.
export function SourceSetup({ sessionId, source, disabled = false, onSaved, onClose }: { sessionId: string; source?: SourceRecord; disabled?: boolean; onSaved(source: SourceRecord): void; onClose(): void }) {
  const [title,setTitle] = useState('');
  const [attribution,setAttribution] = useState('');
  const [note,setNote] = useState('');
  const [url,setUrl] = useState('');
  const [type,setType] = useState<SourceType>('self_recorded');
  const [uses,setUses] = useState<SourceUse[]>([]);
  const [external,setExternal] = useState(false);
  const [decision,setDecision] = useState<'verified'|'rejected'>('verified');
  const [busy,setBusy] = useState(false);
  const [error,setError] = useState<string|null>(null);
  const alive = useRef(true);
  useEffect(() => { alive.current=true; return () => { alive.current=false; }; }, []);
  const eligible = source?.requested_uses ?? ['playback','analysis','export'] as SourceUse[];
  async function save() {
    if (disabled || busy) return;
    if (!note.trim() || (!source && (!title.trim() || !attribution.trim() || !uses.length)) || (source && decision === 'verified' && !uses.length)) {
      setError(source ? '请按实际许可选择允许用途，并填写核验理由。' : '请填写录像名称、来源方、许可说明，并选择实际需要的用途。'); return;
    }
    if (!source && url && !/^https?:\/\/[^\s]+$/i.test(url)) { setError('来源链接请填写完整的 http 或 https 地址，也可以留空。'); return; }
    setBusy(true);setError(null);
    try {
      const saved = source ? await verifySource(source.id, { rights_status: decision, allowed_uses: decision === 'verified' ? uses : [], external_processing_allowed: decision === 'verified' && external, reason: note.trim() })
        : await createSource({ session_id: sessionId, source_type: type, title: title.trim(), attribution: attribution.trim(), authorization_note: note.trim(), source_url: url.trim() || null, requested_uses: uses, external_processing_requested: external });
      if (alive.current) onSaved(saved);
    } catch (cause) { if (alive.current) setError(describeError(cause)); }
    finally { if (alive.current) setBusy(false); }
  }
  return <fieldset className="source-setup" disabled={busy || disabled} onKeyDown={event=>{if(event.key==='Enter' && (event.target as HTMLElement).tagName==='INPUT')event.preventDefault();}}><legend>{source ? `核验录像来源：${source.title}` : '登记这段录像的来源'}</legend>
        {!source && <><label className="account-field"><span>录像名称</span><input value={title} onChange={event=>setTitle(event.target.value)} placeholder="例如：数据结构第三讲课堂录像" /></label><label className="account-field"><span>来源类型</span><select value={type} onChange={event=>setType(event.target.value as SourceType)}><option value="self_recorded">本校录制</option><option value="school_authorized">学校授权</option><option value="open_course">公开课程</option></select></label><label className="account-field"><span>来源方 / 署名</span><input value={attribution} onChange={event=>setAttribution(event.target.value)} placeholder="例如：本校课程组或实际提供者" /></label><label className="account-field"><span>来源链接（选填）</span><input value={url} onChange={event=>setUrl(event.target.value)} placeholder="公开课程原始地址；本地录像可留空" /></label></>}
    {source && <label className="account-field"><span>核验结论</span><select value={decision} onChange={event=>setDecision(event.target.value as 'verified'|'rejected')}><option value="verified">按实际许可通过</option><option value="rejected">拒绝</option></select></label>}
    <div className="source-uses"><strong>{source ? '按实际许可选择允许用途' : '选择实际需要申请的用途'}</strong>{eligible.map(use=><label className="check-row" key={use}><input type="checkbox" checked={uses.includes(use)} onChange={event=>setUses(current=>event.target.checked ? [...current,use] : current.filter(item=>item!==use))}/>{useNames[use]}</label>)}</div>
    {(!source || source.external_processing_requested) && <label className="check-row"><input type="checkbox" checked={external} onChange={event=>setExternal(event.target.checked)}/>{source ? '按实际许可允许' : '申请'}将音频和文本发送给系统配置的云端模型进行识别与报告分析</label>}
    <label className="account-field"><span>{source ? '核验理由' : '许可说明'}</span><textarea value={note} onChange={event=>setNote(event.target.value)} placeholder={source ? '说明已核对的许可依据与允许范围' : '例如：课程组已同意用于本次课堂回放和教学分析；请按真实情况填写'} /></label>
    {source && <p className="field-hint">请核对许可依据后再保存。选择云端处理表示允许向外部模型发送获准的音频和文本。</p>}
    <div className="action-row"><button type="button" className="btn btn-primary" onClick={()=>void save()}>{busy ? '保存中…' : source ? '保存来源核验' : '保存来源登记'}</button><button type="button" className="btn btn-secondary" onClick={onClose}>返回上传步骤</button></div>
    {error && <div role="alert" className="load-note-error"><ErrorMessage text={error}/> 请补齐后重试，已填写内容会保留。</div>}
  </fieldset>;
}
