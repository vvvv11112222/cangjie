import { ErrorMessage } from '../components/ui';
import { useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import type { Dimension, ExportFormat, Report, ReportExport, Session, WritableObservation } from '../types';
import { newIdempotencyKey } from '../data/actions';
import { copyReport, createExport, getExport, listReports, patchReport, publishReport, reviewReport, withdrawReport } from '../data/m3Api';
import { describeError, isTerminalReadError } from '../data/errors';
import { collectPages } from '../data/pagination';
import { BadgeTag, Card } from '../components/ui';
import { dimensionLabel, formatDateTime, reportStatusBadge } from '../format';
import { ReportProgress } from '../components/ReportProgress';

function writable(report: Report): WritableObservation[] {
  return report.observations.map(({ id, dimension_code, observation_type, observation_text, suggestion, evidence_ids }) => ({ id, dimension_code, observation_type, observation_text, suggestion, evidence_ids: [...evidence_ids] }));
}
async function reportHistory(sessionId: string): Promise<Report[]> {
  const items = await collectPages((cursor) => listReports(sessionId, cursor));
  return [...new Map(items.map((item) => [item.id, item])).values()].sort((a, b) => b.revision - a.revision);
}

export function ReportWorkflowPanel({ session, initial, onChange, onPublicationChanged, contextReady = true }: {
  session: Session; initial: Report; onChange?(report: Report): void;
  onPublicationChanged?(currentId: string | null): void; contextReady?: boolean;
}): ReactElement {
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
  const exportKeys = useRef(new Map<string, string>());
  const [historyReload, setHistoryReload] = useState(0);
  const [historyReady, setHistoryReady] = useState(false);
  const historyGeneration = useRef(0);
  const [exportStopped, setExportStopped] = useState(false);
  const [exportReadError, setExportReadError] = useState<{ id: string; message: string } | null>(null);
  const blocked = busy || !contextReady;
  const shownExport = exported?.report_id === report.id && report.allowed_actions.includes('export') ? exported : null;

  useEffect(() => {
    setReport(initial); setSummary(initial.summary); setObservations(writable(initial));
    setDimensions(initial.dimensions.map((item) => ({ ...item }))); setEditing(false);
  }, [initial]);

  const adopt = (next: Report): void => {
    setReport(next); setSummary(next.summary); setObservations(writable(next)); setDimensions(next.dimensions.map((item) => ({ ...item }))); setEditing(false);
    setHistory((items) => [next, ...items.filter((item) => item.id !== next.id)]); onChange?.(next);
  };

  useEffect(() => {
    let active = true;
    const generation = ++historyGeneration.current;
    setHistoryReady(false);
    void reportHistory(session.id).then((items) => {
      if (!active || generation !== historyGeneration.current) return;
      setHistory(items);
      setExpectedCurrent(items.find((item) => item.status === 'published')?.id ?? null);
      setHistoryReady(true);
    }).catch((value: unknown) => { if (active && generation === historyGeneration.current) setError(`报告历史读取失败：${describeError(value)}`); });
    return () => { active = false; };
  }, [session.id, historyReload]);

  function publicationChanged(currentId: string | null): void {
    historyGeneration.current += 1;
    setExpectedCurrent(currentId); setHistoryReady(false);
    setHistoryReload((value) => value + 1);
    onPublicationChanged?.(currentId);
  }

  useEffect(() => {
    if (exportStopped || shownExport === null || !['queued', 'running'].includes(shownExport.status)) return;
    let active = true;
    let pending = false;
    const timer = window.setInterval(() => {
      if (pending) return;
      pending = true;
      void getExport(shownExport.id).then((next) => { if (active) { setExported(next); setExportReadError(null); } }).catch((value: unknown) => {
        if (!active) return;
        const terminal = isTerminalReadError(value);
        if (terminal) { active = false; window.clearInterval(timer); setExportStopped(true); setExported(null); }
        if (terminal) setError(`${describeError(value)} 已停止自动刷新，请重新进入页面。`);
        else setExportReadError({ id: shownExport.id, message: describeError(value) });
      }).finally(() => { pending = false; });
    }, 1500);
    return () => { active = false; window.clearInterval(timer); };
  }, [shownExport?.id, shownExport?.status, exportStopped]);

  async function act(operation: () => Promise<Report>, success: string, after?: (next: Report) => void): Promise<void> {
    setBusy(true); setError(null); setMessage(null);
    try {
      const next = await operation();
      adopt(next);
      historyGeneration.current += 1; setHistoryReady(false); setHistoryReload((value) => value + 1);
      after?.(next); setReason(''); setMessage(success);
    } catch (value) { setError(describeError(value)); } finally { setBusy(false); }
  }
  const explanation = reason.trim() || '前端人工复核操作';

  async function exportReport(): Promise<void> {
    if (blocked) return;
    // A lost response must reuse its key; a different report/format is a new intent.
    const intent = `${report.id}:${format}`;
    const key = exportKeys.current.get(intent) ?? newIdempotencyKey();
    exportKeys.current.set(intent, key);
    setBusy(true); setError(null);
    try {
      const value = await createExport(report.id, format, key);
      setExported(value); setExportStopped(false); exportKeys.current.delete(intent);
      setMessage('导出任务已创建。');
    } catch (cause) { setError(describeError(cause)); }
    finally { setBusy(false); }
  }

  return <Card title="复核、发布与导出" subtitle="可查看历史报告；编辑或复核前请填写操作原因。发布前须完成整篇确认。" actions={<BadgeTag badge={reportStatusBadge(report.status)} />}>
    <ReportProgress report={report}/>
    <div className="workflow-toolbar">
      <label className="account-field"><span>报告版本</span><select disabled={busy} value={report.id} onChange={(event) => {
        const selected = history.find((item) => item.id === event.target.value); if (selected !== undefined) adopt(selected);
      }}>{history.map((item, index) => <option key={item.id} value={item.id}>记录 {index + 1} · 第 {item.revision} 版 · {reportStatusBadge(item.status).label}</option>)}</select></label>
      <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => setHistoryReload((value) => value + 1)}>刷新报告版本</button>
      {report.allowed_actions.some(action => ['edit', 'revise', 'submit', 'confirm_report', 'review', 'withdraw'].includes(action)) ? <label className="account-field"><span>操作说明（写操作必填）</span><input value={reason} onChange={(event) => setReason(event.target.value)} placeholder="说明修改或复核原因" /></label> : null}
    </div>
    <div className="action-row">
      {report.allowed_actions.includes('edit') ? <button type="button" className="btn btn-secondary" disabled={blocked} onClick={() => { setSummary(report.summary); setObservations(writable(report)); setDimensions(report.dimensions.map(item => ({ ...item }))); setEditing(value => !value); }}>{editing ? '取消编辑' : '编辑草稿'}</button> : null}
      {report.allowed_actions.includes('revise') ? <button type="button" className="btn btn-secondary" disabled={blocked || reason.trim() === ''} onClick={() => void act(() => copyReport(report.id, report.lock_version, explanation), '已复制为新的可编辑草稿。')}>新建修订</button> : null}
      {report.allowed_actions.includes('submit') ? <button type="button" className="btn btn-primary" disabled={blocked || reason.trim() === ''} onClick={() => void act(() => reviewReport(report.id, { lock_version: report.lock_version, action: 'submit', observation_id: null, content_sha256: null, reason: explanation }), '报告已提交复核。')}>提交复核</button> : null}
      {report.allowed_actions.includes('confirm_report') ? <button type="button" className="btn btn-primary" disabled={blocked || reason.trim() === '' || report.content_sha256 === null} onClick={() => void act(() => reviewReport(report.id, { lock_version: report.lock_version, action: 'confirm_report', observation_id: null, content_sha256: report.content_sha256, reason: explanation }), '整篇内容已确认。')}>确认整篇内容</button> : null}
      {report.allowed_actions.includes('publish') ? <button type="button" className="btn btn-primary" disabled={blocked || !historyReady} onClick={() => void act(() => publishReport(report.id, { lock_version: report.lock_version, expected_current_report_id: expectedCurrent }), '报告已发布为课堂当前版本。', (next) => publicationChanged(next.id))}>发布报告</button> : null}
      {report.allowed_actions.includes('withdraw') ? <button type="button" className="btn btn-danger" disabled={blocked || reason.trim() === ''} onClick={() => void act(() => withdrawReport(report.id, report.lock_version, explanation), '当前报告已撤回；不会自动恢复旧版本。', () => publicationChanged(null))}>撤回报告</button> : null}
    </div>
    {editing ? <div className="report-editor">
      <label className="account-field"><span>总摘要</span><textarea value={summary} onChange={(event) => setSummary(event.target.value)} /></label>
      {dimensions.map((item, index) => <label className="account-field" key={item.dimension_code}><span>{dimensionLabel(item.dimension_code)} · 六维摘要</span><textarea value={item.summary} disabled={item.coverage_status !== 'observed'} onChange={(event) => setDimensions((items) => items.map((candidate, position) => position === index ? { ...candidate, summary: event.target.value } : candidate))}/></label>)}
      {observations.map((item, index) => <div className="report-observation-editor" key={item.id ?? index}><label className="account-field"><span>{dimensionLabel(item.dimension_code)} · 观察 {index + 1}</span><textarea value={item.observation_text} onChange={(event) => setObservations((items) => items.map((candidate, position) => position === index ? { ...candidate, observation_text: event.target.value } : candidate))}/></label><label className="account-field"><span>改进建议</span><textarea value={item.suggestion} onChange={(event) => setObservations((items) => items.map((candidate, position) => position === index ? { ...candidate, suggestion: event.target.value } : candidate))}/></label></div>)}
      <button type="button" className="btn btn-primary" disabled={blocked || reason.trim() === ''} onClick={() => void act(() => patchReport(report.id, { lock_version: report.lock_version, summary, summary_evidence_ids: report.summary_evidence_ids, dimensions, observations, reason: explanation }), '草稿已保存；内容变化后需要重新提交和确认。')}>保存草稿</button>
    </div> : null}
    {report.allowed_actions.includes('review') ? <div className="review-list">{report.observations.map((item, index) => <div className="review-row" key={item.id}><span>观察 {index + 1}：{item.observation_text}</span><div className="action-row"><button type="button" className="btn btn-secondary" disabled={blocked || reason.trim() === ''} onClick={() => void act(() => reviewReport(report.id, { lock_version: report.lock_version, action: 'accept', observation_id: item.id, content_sha256: null, reason: explanation }), '观察已确认。')}>确认</button><button type="button" className="btn btn-secondary" disabled={blocked || reason.trim() === ''} onClick={() => void act(() => reviewReport(report.id, { lock_version: report.lock_version, action: 'revise', observation_id: item.id, content_sha256: null, reason: explanation }), '已确认该观察经过修正。')}>确认已修正</button><button type="button" className="btn btn-danger" disabled={blocked || reason.trim() === ''} onClick={() => void act(() => reviewReport(report.id, { lock_version: report.lock_version, action: 'reject', observation_id: item.id, content_sha256: null, reason: explanation }), '观察已驳回，发布内容将排除该项。')}>驳回</button></div></div>)}</div> : null}
    {report.allowed_actions.includes('export') ? <div className="export-row"><label className="account-field"><span>导出格式</span><select disabled={blocked} value={format} onChange={(event) => setFormat(event.target.value as ExportFormat)}><option value="pdf">PDF</option><option value="json">JSON</option></select></label><button type="button" className="btn btn-secondary" disabled={blocked} onClick={() => void exportReport()}>导出当前发布版</button></div> : null}
    {shownExport === null ? null : <p className="notice-strip">导出状态：{({ queued: '排队中', running: '生成中', succeeded: '已生成', failed: '生成失败', expired: '已过期' })[shownExport.status]}{shownExport.error_code === null ? '' : ` · ${shownExport.error_code}`}{shownExport.download_url === null ? '' : <> · <a href={shownExport.download_url}>下载文件</a>（有效至 {formatDateTime(shownExport.expires_at)}）</>}</p>}
    {message === null ? null : <p className="notice-strip">{message}</p>}
    {error === null ? null : <div className="notice-strip load-note-error"><ErrorMessage text={error}/></div>}
    {shownExport && exportReadError?.id === shownExport.id && <div className="notice-strip load-note-error"><ErrorMessage text={exportReadError.message}/> 正在重试读取导出状态。</div>}
  </Card>;
}
