import { ErrorMessage } from '../components/ui';
import { useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import type { CreateRun, Revision, Run, Segment, Session } from '../types';
import { createRun } from '../data/actions';
import { useAuth } from '../auth';
import { collectPages } from '../data/pagination';
import { pendingIntent } from './pendingIntent';
import { intentSignature } from './sessionActions';
import { createRevision, listRevisions } from '../data/m3Api';
import { describeError } from '../data/errors';
import { Card, IdChip } from '../components/ui';
import { formatDateTime, formatTimestamp } from '../format';
import { getServiceCapabilities, useServiceCapabilities, featureProblem } from '../data/serviceCapabilities';

function cloneSegments(segments: Segment[]): Segment[] { return segments.map((item) => ({ ...item })); }
async function revisionHistory(sessionId: string, mediaAssetId: string): Promise<Revision[]> {
  return collectPages((cursor) => listRevisions(sessionId, mediaAssetId, cursor));
}

export function TranscriptRevisionPanel({ session, initial, onChange, onRunCreated }: { session: Session; initial: Revision; onChange?(revision: Revision): void; onRunCreated?(run: Run): void }): ReactElement {
  const { me, sessionKey } = useAuth();
  const services = useServiceCapabilities();
  const accountScope = me?.user_id ?? sessionKey;
  const [current, setCurrent] = useState(initial);
  const [history, setHistory] = useState<Revision[]>([initial]);
  const [segments, setSegments] = useState(() => cloneSegments(initial.segments));
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const generation = useRef(0);
  const knownLock = useRef(initial.transcript_lock_version);
  useEffect(() => () => { generation.current += 1; }, []);

  function mergeHistory(items: Revision[]): void {
    knownLock.current = Math.max(knownLock.current, ...items.map((item) => item.transcript_lock_version));
    setHistory((previous) => [...new Map([...previous, ...items].map((item) => [item.id, item])).values()]
      .map((item) => ({ ...item, transcript_lock_version: knownLock.current }))
      .sort((a, b) => b.revision_no - a.revision_no));
  }

  useEffect(() => {
    let active = true;
    void revisionHistory(session.id, initial.media_asset_id).then((items) => {
      if (active) mergeHistory(items);
    }).catch((value: unknown) => { if (active) setError(`修订历史读取失败：${describeError(value)}`); });
    return () => { active = false; };
  }, [initial.media_asset_id, session.id]);

  async function save(): Promise<void> {
    const requestGeneration = generation.current;
    setBusy(true); setError(null); setMessage(null);
    try {
      const saved = await createRevision(session.id, {
        media_asset_id: current.media_asset_id,
        base_revision_id: current.id,
        transcript_lock_version: current.transcript_lock_version,
        reason: reason.trim(), segments,
      });
      if (requestGeneration !== generation.current) return;
      setCurrent(saved); setSegments(cloneSegments(saved.segments)); setEditing(false); setReason('');
      onChange?.(saved);
      mergeHistory([saved]);
      setMessage(`已保存第 ${saved.revision_no} 版；旧版本仍保留。`);
    } catch (value) { setError(describeError(value)); } finally { setBusy(false); }
  }

  async function regenerate(): Promise<void> {
    const requestGeneration = generation.current;
    const input: CreateRun = {
      media_asset_id: current.media_asset_id,
      mode: 'report_only', input_transcript_revision_id: current.id, config_profile: 'p0-v1',
    };
    const key = pendingIntent.keyFor(accountScope, session.id, intentSignature(session.id, input));
    setBusy(true); setError(null); setMessage(null);
    try {
      const latest=await getServiceCapabilities();
      if(requestGeneration!==generation.current)return;
      if(!latest.report_generation.available) {setError(featureProblem(latest.report_generation));services.reload();return;}
      const run = await createRun(session.id, input, key);
      pendingIntent.resolve(accountScope, session.id, key);
      if (requestGeneration !== generation.current) return;
      onRunCreated?.(run);
      setMessage(`报告重跑已创建：${run.id}。它固定使用第 ${current.revision_no} 版转写。`);
    } catch (value) { setError(describeError(value)); } finally { setBusy(false); }
  }

  return (
    <Card title="转写修订" subtitle="每次保存都创建新快照；并发版本冲突时不会覆盖别人刚保存的内容。">
      <p className="field-hint">{services.data ? services.data.report_generation.available ? "报告生成服务前置检查通过；重跑仍会检查本条转写和来源许可。" : featureProblem(services.data.report_generation) : "正在确认报告生成服务；未确认前暂停新建报告。"}<button type="button" className="text-button" onClick={services.reload}>重新检查报告服务</button></p>
      <div className="revision-toolbar">
        <label className="account-field"><span>查看版本</span><select value={current.id} disabled={busy} onChange={(event) => {
          const selected = history.find((item) => item.id === event.target.value);
          if (selected !== undefined) { setCurrent(selected); setSegments(cloneSegments(selected.segments)); setEditing(false); onChange?.(selected); }
        }}>{history.map((item) => <option key={item.id} value={item.id}>第 {item.revision_no} 版 · {item.source_type} · {formatDateTime(item.created_at)}</option>)}</select></label>
        <div className="action-row">
          {session.allowed_actions.includes('edit_transcript') ? <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => { setSegments(cloneSegments(current.segments)); setReason(''); setEditing(value => !value); }}>{editing ? '取消编辑' : '修订转写'}</button> : null}
          <button type="button" className="btn btn-primary" disabled={busy || editing || !session.allowed_actions.includes('analyze') || !services.data?.report_generation.available} onClick={() => void regenerate()}>用此版本重新生成报告</button>
        </div>
      </div>
      {editing ? <div className="revision-editor">
        <label className="account-field"><span>修订原因（必填）</span><textarea value={reason} onChange={(event) => setReason(event.target.value)} placeholder="说明为什么要修订" /></label>
        <div className="table-wrap"><table className="table"><thead><tr><th>#</th><th>时间</th><th>说话人</th><th>文本</th></tr></thead><tbody>{segments.map((segment, index) => <tr key={segment.segment_no}><td>{segment.segment_no}</td><td className="mono nowrap">{formatTimestamp(segment.start_ms)}–{formatTimestamp(segment.end_ms)}</td><td><input aria-label={`片段 ${segment.segment_no} 说话人`} value={segment.speaker_label} onChange={(event) => setSegments((items) => items.map((item, position) => position === index ? { ...item, speaker_label: event.target.value } : item))}/></td><td><textarea aria-label={`片段 ${segment.segment_no} 文本`} value={segment.text_content} onChange={(event) => setSegments((items) => items.map((item, position) => position === index ? { ...item, text_content: event.target.value } : item))}/></td></tr>)}</tbody></table></div>
        <button type="button" className="btn btn-primary" disabled={busy || reason.trim() === '' || segments.some((item) => item.text_content.trim() === '')} onClick={() => void save()}>保存为新版本</button>
      </div> : <p className="muted">当前查看第 {current.revision_no} 版，父版本 {current.parent_revision_id === null ? '无' : <IdChip id={current.parent_revision_id} />}。</p>}
      {message === null ? null : <p className="notice-strip">{message}</p>}
      {error === null ? null : <div className="notice-strip load-note-error"><ErrorMessage text={error}/></div>}
    </Card>
  );
}
