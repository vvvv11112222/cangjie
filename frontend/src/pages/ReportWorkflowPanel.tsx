import { useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import type { Dimension, ExportFormat, Report, ReportExport, Session, WritableObservation } from '../types';
import { newIdempotencyKey } from '../data/actions';
import { copyReport, createExport, getExport, listReports, patchReport, publishReport, reviewReport, withdrawReport } from '../data/m3Api';
import { describeError } from '../data/errors';
import { BadgeTag, Card, IdChip } from '../components/ui';
import { formatDateTime, reportStatusBadge } from '../format';

function writable(report: Report): WritableObservation[] {
  return report.observations.map(({ id, dimension_code, observation_type, observation_text, suggestion, evidence_ids }) => ({ id, dimension_code, observation_type, observation_text, suggestion, evidence_ids: [...evidence_ids] }));
}
async function reportHistory(sessionId: string): Promise<Report[]> {
  const collected = new Map<string, Report>();
  const seen = new Set<string>();
  let cursor: string | undefined;
  for (let pageNo = 0; pageNo < 10; pageNo += 1) {
    const page = await listReports(sessionId, cursor);
    for (const item of page.items) collected.set(item.id, item);
    if (page.next_cursor === null) break;
    if (seen.has(page.next_cursor)) throw new Error('报告分页游标重复，已停止加载');
    seen.add(page.next_cursor); cursor = page.next_cursor;
  }
  return [...collected.values()].sort((a, b) => b.revision - a.revision);
}

export function ReportWorkflowPanel({ session, initial, onChange }: { session: Session; initial: Report; onChange?(report: Report): void }): ReactElement {
  const [report, setReport] = useState(initial);
  const [history, setHistory] = useState<Report[]>([initial]);
  const [summary, setSummary] = useState(initial.summary);
  const [observations, setObservations] = useState(() => writable(initial));
  const [dimensions, setDimensions] = useState<Dimension[]>(() => initial.dimensions.map((item) => ({ ...item })));
  const [reason, setReason] = useState('');
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [format, setFormat] = useState<ExportFormat>('pdf');
  const [exported, setExported] = useState<ReportExport | null>(null);
  const [expectedCurrent, setExpectedCurrent] = useState(session.current_report_id);
  const exportKey = useRef(newIdempotencyKey());

  const adopt = (next: Report): void => {
    setReport(next); setSummary(next.summary); setObservations(writable(next)); setDimensions(next.dimensions.map((item) => ({ ...item }))); setEditing(false);
    setHistory((items) => [next, ...items.filter((item) => item.id !== next.id)]); onChange?.(next);
  };

  useEffect(() => {
    let active = true;
    void reportHistory(session.id).then((items) => { if (active) setHistory(items); }).catch((value: unknown) => { if (active) setError(`报告历史读取失败：${describeError(value)}`); });
    return () => { active = false; };
  }, [session.id]);

  useEffect(() => {
    if (exported === null || !['queued', 'running'].includes(exported.status)) return;
    let active = true;
    const timer = window.setInterval(() => void getExport(exported.id).then((next) => { if (active) setExported(next); }).catch((value: unknown) => { if (active) setError(describeError(value)); }), 1500);
    return () => { active = false; window.clearInterval(timer); };
  }, [exported?.id, exported?.status]);

  async function act(operation: () => Promise<Report>, success: string, after?: (next: Report) => void): Promise<void> {
    setBusy(true); setError(null); setMessage(null);
    try { const next = await operation(); adopt(next); after?.(next); setReason(''); setMessage(success); } catch (value) { setError(describeError(value)); } finally { setBusy(false); }
  }
  const explanation = reason.trim() || '前端人工复核操作';

  return <Card title="复核、发布与导出" subtitle="所有写入携带锁版本；按钮只按报告 allowed_actions 显示，服务端仍逐次鉴权。" actions={<BadgeTag badge={reportStatusBadge(report.status)} />}>
    <div className="workflow-toolbar">
      <label className="account-field"><span>报告版本</span><select value={report.id} onChange={(event) => {
        const selected = history.find((item) => item.id === event.target.value); if (selected !== undefined) adopt(selected);
      }}>{history.map((item) => <option key={item.id} value={item.id}>v{item.revision} · {item.status} · {item.id.slice(0, 8)}</option>)}</select></label>
      <label className="account-field"><span>操作说明（写操作必填）</span><input value={reason} onChange={(event) => setReason(event.target.value)} placeholder="说明修改或复核原因" /></label>
    </div>
    <div className="action-row">
      {report.allowed_actions.includes('edit') ? <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => setEditing((value) => !value)}>{editing ? '取消编辑' : '编辑草稿'}</button> : null}
      {report.allowed_actions.includes('revise') ? <button type="button" className="btn btn-secondary" disabled={busy || reason.trim() === ''} onClick={() => void act(() => copyReport(report.id, report.lock_version, explanation), '已复制为新的可编辑草稿。')}>新建修订</button> : null}
      {report.allowed_actions.includes('submit') ? <button type="button" className="btn btn-primary" disabled={busy || reason.trim() === ''} onClick={() => void act(() => reviewReport(report.id, { lock_version: report.lock_version, action: 'submit', observation_id: null, content_sha256: null, reason: explanation }), '报告已提交复核。')}>提交复核</button> : null}
      {report.allowed_actions.includes('confirm_report') ? <button type="button" className="btn btn-primary" disabled={busy || reason.trim() === '' || report.content_sha256 === null} onClick={() => void act(() => reviewReport(report.id, { lock_version: report.lock_version, action: 'confirm_report', observation_id: null, content_sha256: report.content_sha256, reason: explanation }), '整篇内容已确认。')}>确认整篇内容</button> : null}
      {report.allowed_actions.includes('publish') ? <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void act(() => publishReport(report.id, { lock_version: report.lock_version, expected_current_report_id: expectedCurrent }), '报告已发布为课堂当前版本。', (next) => setExpectedCurrent(next.id))}>发布报告</button> : null}
      {report.allowed_actions.includes('withdraw') ? <button type="button" className="btn btn-danger" disabled={busy || reason.trim() === ''} onClick={() => void act(() => withdrawReport(report.id, report.lock_version, explanation), '当前报告已撤回；不会自动恢复旧版本。', () => setExpectedCurrent(null))}>撤回报告</button> : null}
    </div>
    {editing ? <div className="report-editor">
      <label className="account-field"><span>总摘要</span><textarea value={summary} onChange={(event) => setSummary(event.target.value)} /></label>
      {dimensions.map((item, index) => <label className="account-field" key={item.dimension_code}><span>{item.dimension_code} · 六维摘要</span><textarea value={item.summary} disabled={item.coverage_status !== 'observed'} onChange={(event) => setDimensions((items) => items.map((candidate, position) => position === index ? { ...candidate, summary: event.target.value } : candidate))}/></label>)}
      {observations.map((item, index) => <div className="report-observation-editor" key={item.id ?? index}><label className="account-field"><span>{item.dimension_code} · 观察 {index + 1}</span><textarea value={item.observation_text} onChange={(event) => setObservations((items) => items.map((candidate, position) => position === index ? { ...candidate, observation_text: event.target.value } : candidate))}/></label><label className="account-field"><span>改进建议</span><textarea value={item.suggestion} onChange={(event) => setObservations((items) => items.map((candidate, position) => position === index ? { ...candidate, suggestion: event.target.value } : candidate))}/></label></div>)}
      <button type="button" className="btn btn-primary" disabled={busy || reason.trim() === ''} onClick={() => void act(() => patchReport(report.id, { lock_version: report.lock_version, summary, summary_evidence_ids: report.summary_evidence_ids, dimensions, observations, reason: explanation }), '草稿已保存；内容变化后需要重新提交和确认。')}>保存草稿</button>
    </div> : null}
    {report.allowed_actions.includes('review') ? <div className="review-list">{report.observations.map((item) => <div className="review-row" key={item.id}><span><IdChip id={item.id} /> {item.observation_text}</span><div className="action-row"><button type="button" className="btn btn-secondary" disabled={busy || reason.trim() === ''} onClick={() => void act(() => reviewReport(report.id, { lock_version: report.lock_version, action: 'accept', observation_id: item.id, content_sha256: null, reason: explanation }), '观察已确认。')}>确认</button><button type="button" className="btn btn-secondary" disabled={busy || reason.trim() === ''} onClick={() => void act(() => reviewReport(report.id, { lock_version: report.lock_version, action: 'revise', observation_id: item.id, content_sha256: null, reason: explanation }), '已确认该观察经过修正。')}>确认已修正</button><button type="button" className="btn btn-danger" disabled={busy || reason.trim() === ''} onClick={() => void act(() => reviewReport(report.id, { lock_version: report.lock_version, action: 'reject', observation_id: item.id, content_sha256: null, reason: explanation }), '观察已驳回，发布内容将排除该项。')}>驳回</button></div></div>)}</div> : null}
    {report.allowed_actions.includes('export') ? <div className="export-row"><label className="account-field"><span>导出格式</span><select value={format} onChange={(event) => setFormat(event.target.value as ExportFormat)}><option value="pdf">PDF</option><option value="json">JSON</option></select></label><button type="button" className="btn btn-secondary" disabled={busy} onClick={() => { setBusy(true); setError(null); void createExport(report.id, format, exportKey.current).then((value) => { setExported(value); exportKey.current = newIdempotencyKey(); setMessage('导出任务已创建。'); }).catch((value: unknown) => setError(describeError(value))).finally(() => setBusy(false)); }}>导出当前发布版</button></div> : null}
    {exported === null ? null : <p className="notice-strip">导出状态：{exported.status}{exported.error_code === null ? '' : ` · ${exported.error_code}`}{exported.download_url === null ? '' : <> · <a href={exported.download_url}>下载文件</a>（有效至 {formatDateTime(exported.expires_at)}）</>}</p>}
    {message === null ? null : <p className="notice-strip">{message}</p>}
    {error === null ? null : <p className="notice-strip load-note-error">{error}</p>}
  </Card>;
}
