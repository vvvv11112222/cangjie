// @vitest-environment jsdom
import { act } from 'react';
import type { ReactElement } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ApiError } from '../data/errors';
import { report as reportFixture, revision as revisionFixture, results as resultsFixture, run as runFixture, sessionPage } from '../data/samples';
import { buildReportView, buildTranscriptView } from '../data/viewModel';
import type { Report, Revision, Session } from '../types';
import type { LiveCapture } from '../captureTypes';
import { pendingIntent } from './pendingIntent';

const h = vi.hoisted(() => ({
  account: 'test', createRun: vi.fn(), listRevisions: vi.fn(), createRevision: vi.fn(), listReports: vi.fn(),
  copyReport: vi.fn(), patchReport: vi.fn(), reviewReport: vi.fn(), publishReport: vi.fn(), withdrawReport: vi.fn(),
  createExport: vi.fn(), getExport: vi.fn(), listSources: vi.fn(), active: vi.fn(), capture: vi.fn(), viewer: vi.fn(),
  connect: vi.fn(), source: { getResults: vi.fn(), getRun: vi.fn(), getRevision: vi.fn() },
}));
vi.mock('../auth', () => ({ useAuth: () => ({ me: { user_id: h.account }, sessionKey: `${h.account}#1` }) }));
vi.mock('../data/actions', async (original) => ({ ...await original<typeof import('../data/actions')>(), createRun: h.createRun }));
vi.mock('../data/m3Api', () => ({ ...h }));
vi.mock('../data/m2Api', () => ({ listSessionSources: h.listSources }));
vi.mock('../data/liveCaptureApi', () => ({ getActiveLiveCapture: h.active, getLiveCapture: h.capture, createViewer: h.viewer, createPairing: vi.fn(), createLiveCapture: vi.fn(), stopLiveCapture: vi.fn() }));
vi.mock('../capture/whep', () => ({ connectWHEP: h.connect }));
vi.mock('../data/sources', () => ({ isApiMode: () => true, dataSource: h.source }));
vi.mock('../data/hooks', () => {
  const loaded = () => ({ data: { view: {}, selection: null }, origin: { sources: [], label: 'API', note: '' }, status: 'ready', hasData: true });
  return { useReportData: loaded, useTranscriptData: loaded };
});

import { TranscriptPage } from './TranscriptPage';
import { TranscriptRevisionPanel } from './TranscriptRevisionPanel';
import { ReportPage } from './ReportPage';
import { ReportWorkflowPanel } from './ReportWorkflowPanel';
import { LiveCapturePanel } from './LiveCapturePanel';

let root: Root;
let container: HTMLDivElement;
let accountNo = 0;
let reports: Report[];
let revisions: Revision[];
const session: Session = { ...sessionPage.items[0]!, current_report_id: null, allowed_actions: ['upload', 'analyze', 'edit_transcript'] };
const firstRevision: Revision = { ...revisionFixture, revision_no: 1, transcript_lock_version: 1, segments: [{ ...revisionFixture.segments[0]!, text_content: '第一版正文' }] };
const secondRevision: Revision = { ...firstRevision, id: '20000000-0000-4000-8000-000000000002', revision_no: 2, segments: [{ ...firstRevision.segments[0]!, text_content: '第二版正文' }] };
const capture: LiveCapture = { id: 'capture-one', session_id: session.id, state: 'recording', connection_state: 'connected', recording_state: 'recording', audio_track: 'present', video_track: 'present', created_at: '2026-10-16T00:00:00Z', expires_at: '2026-10-17T00:00:00Z', started_at: null, ended_at: null, duration_ms: null, media_asset_id: null, media_prepare_run_id: null, end_reason: null, error_code: null, allowed_actions: ['read', 'watch', 'stop'] };
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }
const viewer = () => ({ viewer_session_id: 'viewer', whep_url: 'https://example.test/whep', view_token: 'synthetic', expires_at: new Date(Date.now() + 60000).toISOString() });
const connection = () => ({ stream: new MediaStreamStub(), connection: { onconnectionstatechange: null, connectionState: 'connected' }, close: vi.fn(async () => {}) });
class MediaStreamStub {}
function page<T>(items: T[], cursor?: string) { const offset = Number(cursor ?? 0); return { items: items.slice(offset, offset + 20), next_cursor: offset + 20 < items.length ? String(offset + 20) : null }; }
async function mount(element: ReactElement) { await act(async () => root.render(element)); }
async function remount(element: ReactElement) { await act(async () => root.unmount()); root = createRoot(container); await mount(element); }
function button(label: string) { const node = [...container.querySelectorAll('button')].find(item => item.textContent === label); if (!node) throw new Error(`Missing button: ${label}`); return node; }
async function click(label: string) { await act(async () => button(label).click()); }
async function select(value: string) { await act(async () => { const node = container.querySelector('select')!; node.value = value; node.dispatchEvent(new Event('change', { bubbles: true })); }); }
async function reason(value = '修订理由') { await act(async () => { const node = container.querySelector('.revision-editor textarea')!; Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(node, value); node.dispatchEvent(new Event('input', { bubbles: true })); }); }
const reportView = (report = reportFixture, currentSession = session) => buildReportView({ report, results: resultsFixture, run: runFixture, revision: revisionFixture, sessionPage: { items: [currentSession], next_cursor: null }, kind: 'api' });

beforeEach(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  vi.resetAllMocks(); h.account = `pr29-${++accountNo}`;
  reports = [reportFixture]; revisions = [firstRevision, secondRevision];
  h.listRevisions.mockImplementation(async (_session, _media, cursor) => page(revisions, cursor));
  h.listReports.mockImplementation(async (_session, cursor) => page(reports, cursor));
  h.createRun.mockResolvedValue(runFixture);
  h.listSources.mockResolvedValue({ items: [], next_cursor: null }); h.active.mockResolvedValue(capture); h.capture.mockResolvedValue(capture);
  h.viewer.mockImplementation(async () => viewer()); h.connect.mockImplementation(async () => connection());
  h.source.getResults.mockResolvedValue({ ...resultsFixture, transcript_revision_id: null });
  h.source.getRun.mockImplementation(async (id) => ({ ...runFixture, id, status: 'succeeded' }));
  h.source.getRevision.mockResolvedValue(revisionFixture);
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
  container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount()); container.remove();
  for (const call of h.createRun.mock.calls) pendingIntent.resolve(h.account, session.id, call[2]);
  vi.useRealTimers(); vi.restoreAllMocks();
});

it('switching transcript history updates the visible revision and its text', async () => {
  const view = buildTranscriptView({ results: resultsFixture, revision: firstRevision, run: null, kind: 'api' });
  await mount(<TranscriptPage embedded session={session} view={view}/>);
  await select(secondRevision.id);
  expect(container.textContent).toContain('第二版正文'); expect(container.textContent).not.toContain('第一版正文');
});

it('a saved revision advances the classroom lock even when branching from an older version', async () => {
  let lock = 1;
  h.createRevision.mockImplementation(async (_session, input) => {
    if (input.transcript_lock_version !== lock) throw new Error('stale classroom lock');
    return { ...firstRevision, id: `saved-${++lock}`, transcript_lock_version: lock, revision_no: lock + 1, segments: input.segments };
  });
  await mount(<TranscriptRevisionPanel session={session} initial={firstRevision}/>);
  await click('修订转写'); await reason(); await click('保存为新版本');
  await select(firstRevision.id); await click('修订转写'); await reason(); await click('保存为新版本');
  expect(h.createRevision.mock.calls.map(call => call[1].transcript_lock_version)).toEqual([1, 2]);
  expect(container.textContent).not.toContain('stale classroom lock');
});

it('keeps the report-only intent across remounts and separates revision payloads', async () => {
  h.createRun.mockRejectedValue(new ApiError({ code: 'NETWORK_ERROR', status: 0 }));
  const panel = <TranscriptRevisionPanel session={session} initial={firstRevision}/>;
  await mount(panel); await click('用此版本重新生成报告');
  await remount(panel); await click('用此版本重新生成报告');
  expect(h.createRun.mock.calls[1]![2]).toBe(h.createRun.mock.calls[0]![2]);
  await select(secondRevision.id); await click('用此版本重新生成报告');
  expect(h.createRun.mock.calls[2]![2]).not.toBe(h.createRun.mock.calls[0]![2]);
  h.createRun.mockResolvedValue(runFixture); await click('用此版本重新生成报告');
  expect(h.createRun.mock.calls[3]![2]).toBe(h.createRun.mock.calls[2]![2]);
  await click('用此版本重新生成报告'); expect(h.createRun.mock.calls[4]![2]).not.toBe(h.createRun.mock.calls[3]![2]);
});

it('does not start WHEP when viewer authorization arrives after leaving the panel', async () => {
  vi.useFakeTimers(); const response = deferred<ReturnType<typeof viewer>>(); h.viewer.mockReturnValue(response.promise);
  await mount(<LiveCapturePanel session={session} enabled onUseMedia={() => {}}/>); await click('开始实时观看');
  await mount(<div/>); await act(async () => response.resolve(viewer()));
  await act(async () => { await vi.advanceTimersByTimeAsync(120000); });
  expect(h.connect).not.toHaveBeenCalled(); expect(h.viewer).toHaveBeenCalledTimes(1);
});

it('closes a late WHEP connection without scheduling renewal after unmount', async () => {
  vi.useFakeTimers(); const result = connection(); const response = deferred<typeof result>(); h.connect.mockReturnValue(response.promise);
  await mount(<LiveCapturePanel session={session} enabled onUseMedia={() => {}}/>); await click('开始实时观看');
  const signal = h.connect.mock.calls[0]![2] as AbortSignal;
  await mount(<div/>); expect(signal.aborted).toBe(true);
  await act(async () => response.resolve(result)); await act(async () => { await vi.advanceTimersByTimeAsync(120000); });
  expect(result.close).toHaveBeenCalled(); expect(h.viewer).toHaveBeenCalledTimes(1);
});

it('does not renew if the video play promise resolves after leaving', async () => {
  vi.useFakeTimers(); const played = deferred<void>(); vi.mocked(HTMLMediaElement.prototype.play).mockReturnValue(played.promise);
  await mount(<LiveCapturePanel session={session} enabled onUseMedia={() => {}}/>); await click('开始实时观看');
  await mount(<div/>); await act(async () => played.resolve()); await act(async () => { await vi.advanceTimersByTimeAsync(120000); });
  expect(h.viewer).toHaveBeenCalledTimes(1);
});

it('loads evidence and disclosures for the selected report run and discards late selections', async () => {
  const next = { ...reportFixture, id: 'report-other', run_id: 'run-other', summary: '另一批次报告', summary_evidence_ids: ['evidence-other'] };
  reports = [reportFixture, next];
  const result = { ...resultsFixture, transcript_revision_id: null, evidence: [{ ...resultsFixture.evidence[0]!, id: 'evidence-other', description: '所选批次的证据' }] };
  const delayed = deferred<typeof result>(); h.source.getResults.mockReturnValue(delayed.promise);
  await mount(<ReportPage embedded view={reportView()}/>); await select(next.id);
  expect(container.textContent).toContain('正在加载所选报告'); expect(button('编辑草稿').disabled).toBe(true);
  await select(reportFixture.id); await act(async () => delayed.resolve(result));
  expect(container.textContent).not.toContain('另一批次报告');
  h.source.getResults.mockResolvedValue(result); await select(next.id);
  expect(h.source.getResults).toHaveBeenLastCalledWith(next.run_id);
  expect(container.textContent).toContain('所选批次的证据'); expect(container.textContent).not.toContain('报告的 run_id=');
});

it('refreshes publication state across tab reentry and after withdrawal', async () => {
  const a: Report = { ...reportFixture, status: 'in_review', allowed_actions: ['publish'] };
  const b: Report = { ...a, id: 'report-b', revision: 2 };
  reports = [a, b]; let current: string | null = null;
  h.publishReport.mockImplementation(async (id, input) => {
    if (input.expected_current_report_id !== current) throw new Error('stale publication');
    reports = reports.map(item => ({ ...item, status: item.id === id ? 'published' : item.id === current ? 'superseded' : item.status, allowed_actions: item.id === id ? ['withdraw', 'export'] : item.allowed_actions }));
    current = id; return reports.find(item => item.id === id)!;
  });
  h.withdrawReport.mockImplementation(async (id) => { current = null; reports = reports.map(item => item.id === id ? { ...item, status: 'withdrawn', allowed_actions: [] } : item); return reports.find(item => item.id === id)!; });
  const refresh = vi.fn();
  await mount(<ReportPage embedded view={reportView(a)} onPublicationChanged={refresh}/>); await click('发布报告');
  expect(refresh).toHaveBeenCalledTimes(1);
  await remount(<ReportPage embedded view={reportView(reports[0]!)} onPublicationChanged={refresh}/>);
  await select(b.id); await click('发布报告');
  expect(h.publishReport.mock.calls[1]![1].expected_current_report_id).toBe(a.id);
  expect(refresh).toHaveBeenCalledTimes(2);
  expect(container.textContent).not.toContain('stale publication');
  const input = container.querySelector('.workflow-toolbar input')!;
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '撤回测试'); input.dispatchEvent(new Event('input', { bubbles: true })); });
  await click('撤回报告'); expect(refresh).toHaveBeenCalledTimes(3);
  expect(container.textContent).not.toContain('导出当前发布版');
});

it.each(['report', 'revision', 'source'])('loads beyond ten pages of %s data', async (kind) => {
  if (kind === 'report') {
    reports = Array.from({ length: 201 }, (_, i) => ({ ...reportFixture, id: `report-${i}`, revision: i + 1 }));
    await mount(<ReportWorkflowPanel session={session} initial={reports[200]!}/>); expect(h.listReports).toHaveBeenCalledTimes(11);
  } else if (kind === 'revision') {
    revisions = Array.from({ length: 201 }, (_, i) => ({ ...firstRevision, id: `revision-${i}`, revision_no: i + 1 }));
    await mount(<TranscriptRevisionPanel session={session} initial={revisions[200]!}/>); expect(h.listRevisions).toHaveBeenCalledTimes(11);
  } else {
    const sources = Array.from({ length: 201 }, (_, i) => ({ id: `source-${i}`, title: `来源 ${i}`, rights_status: 'verified', allowed_uses: ['playback'] }));
    h.listSources.mockImplementation(async (_id, cursor) => page(sources, cursor));
    h.active.mockRejectedValue(new ApiError({ code: 'NOT_FOUND', status: 404 }));
    await mount(<LiveCapturePanel session={session} enabled onUseMedia={() => {}}/>); expect(h.listSources).toHaveBeenCalledTimes(11);
  }
  expect(container.querySelectorAll('option')).toHaveLength(201);
});

it.each([401, 403, 404, 410])('stops capture polling and closes watching on HTTP %s', async (status) => {
  vi.useFakeTimers(); const connected = connection(); h.connect.mockResolvedValue(connected);
  h.capture.mockRejectedValue(new ApiError({ code: 'FORBIDDEN', status }));
  await mount(<LiveCapturePanel session={session} enabled onUseMedia={() => {}}/>); await click('开始实时观看');
  await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
  expect(h.capture).toHaveBeenCalledTimes(1); expect(connected.close).toHaveBeenCalled();
  expect(button('创建实时采集').disabled).toBe(true); expect(container.textContent).toContain('已停止自动刷新');
  await act(async () => { await vi.advanceTimersByTimeAsync(60000); }); expect(h.viewer).toHaveBeenCalledTimes(1);
});

it.each([401, 403, 404, 410])('stops export polling and clears download data on HTTP %s', async (status) => {
  vi.useFakeTimers(); const report: Report = { ...reportFixture, status: 'published', allowed_actions: ['export'] }; reports = [report];
  h.createExport.mockResolvedValue({ id: 'export', report_id: report.id, status: 'queued', error_code: null, download_url: null, expires_at: '2026-10-17T00:00:00Z' });
  h.getExport.mockRejectedValue(new ApiError({ code: 'FORBIDDEN', status }));
  await mount(<ReportWorkflowPanel session={session} initial={report}/>); await click('导出当前发布版');
  await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
  expect(h.getExport).toHaveBeenCalledTimes(1); expect(container.textContent).toContain('已停止自动刷新');
  expect(container.textContent).not.toContain('导出状态');
});

it('recovers capture polling after a temporary server failure', async () => {
  vi.useFakeTimers(); h.capture.mockRejectedValueOnce(new ApiError({ code: 'SERVICE_UNAVAILABLE', status: 503 })).mockResolvedValue({ ...capture, state: 'ready', allowed_actions: ['read'] });
  await mount(<LiveCapturePanel session={session} enabled onUseMedia={() => {}}/>);
  await act(async () => { await vi.advanceTimersByTimeAsync(6000); }); expect(h.capture).toHaveBeenCalledTimes(2); expect(container.textContent).toContain('录像就绪');
});
