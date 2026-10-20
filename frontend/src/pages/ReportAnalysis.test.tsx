// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ReportPage } from './ReportPage';
import { ReportAnalysis, reportLimitation } from './ReportAnalysis';
import { buildReportView } from '../data/viewModel';
import type { Evidence } from '../types';

const h = vi.hoisted(() => ({ media: vi.fn(), probe: vi.fn(), player: vi.fn() }));
vi.mock('../data/hooks', async (original) => ({ ...await original<typeof import('../data/hooks')>(), useMediaDetail: h.media, usePlaybackProbe: h.probe }));
vi.mock('../components/MediaPlayer', () => ({ MediaPlayer: (props: unknown) => { h.player(props); return <p>原录像播放器</p>; } }));

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;
function viewWithAdvice() {
  const view = structuredClone(buildReportView());
  const evidence: Evidence = {
    id: '00000000-0000-4000-8000-000000000777', run_id: view.report.run_id, session_id: view.report.session_id,
    media_asset_id: view.report.provenance!.media_asset_id, kind: 'transcript', start_ms: 17250, end_ms: 20900,
    description: '教师比较一级结构与空间结构', availability: 'available', transcript_segment_id: null, frame_asset_id: null, provenance: {},
  };
  view.evidenceById = new Map([[evidence.id, evidence]]);
  view.report.summary_evidence_ids = [evidence.id];
  view.report.observations = [{ id: 'observation-internal-id', dimension_code: 'content', observation_type: 'issue', observation_text: '此处两个术语的界限未解释。', suggestion: '做法：请学生比较两种结构。\n课堂示例：变性会切断肽键吗？\n观察效果：听学生是否能解释差别。', evidence_ids: [evidence.id], review_status: 'unreviewed' }];
  const content = view.report.dimensions.find(d => d.dimension_code === 'content')!;
  content.summary = '教师由结构层次引出变性条件。';
  content.summary_evidence_ids = [evidence.id];
  return { view, evidence };
}
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement('div'); document.body.append(container); root = createRoot(container);
  h.media.mockReset(); h.probe.mockReset(); h.player.mockReset();
  const { evidence } = viewWithAdvice();
  h.media.mockReturnValue({ status: 'ready', data: { media: { id: evidence.media_asset_id, session_id: evidence.session_id, status: 'ready', duration_ms: 60000, playback_url: '/authorized-original' } }, error: null, reload: vi.fn() });
  h.probe.mockReturnValue({ status: 'ok', note: null, recheck: vi.fn() });
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); });
async function click(text: string) {
  const button = [...container.querySelectorAll('button')].find(node => node.textContent?.includes(text));
  expect(button).toBeDefined(); await act(async () => button!.click());
}

it('重点分析和详细建议直接可见，切换维度不会重复展示其他维度建议', async () => {
  const { view } = viewWithAdvice();
  await act(async () => root.render(<ReportAnalysis report={view.report} evidenceById={view.evidenceById} onSelect={vi.fn()} />));
  const panel = container.querySelector('[role="tabpanel"]')!;
  expect(panel.textContent).toContain('课堂分析');
  expect(panel.textContent).toContain('存在的问题与原因');
  expect(panel.textContent).toContain('修改方向与实施建议');
  expect(panel.textContent).toContain('变性会切断肽键吗');
  expect(panel.querySelector('.report-prose')?.closest('details')).toBeNull();
  const cite = container.querySelector('.report-citation')!;
  expect(cite.textContent).toContain('00:17.250');
  expect(cite.textContent).not.toContain('00000000');
  await click('教学节奏');
  expect(panel.textContent).not.toContain('变性会切断肽键吗');
  expect(container.querySelectorAll('[role="tab"][aria-selected="true"]')).toHaveLength(1);
});

it('六维导航支持方向键、首尾键及对应面板标注', async () => {
  const { view } = viewWithAdvice();
  await act(async () => root.render(<ReportAnalysis report={view.report} evidenceById={view.evidenceById} onSelect={vi.fn()} />));
  const tabs = [...container.querySelectorAll<HTMLButtonElement>('[role="tab"]')];
  await act(async () => tabs[0]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true })));
  expect(document.activeElement).toBe(tabs[5]);
  expect(tabs[5]?.getAttribute('aria-selected')).toBe('true');
  expect(container.querySelector('[role="tabpanel"]')?.getAttribute('aria-labelledby')).toBe(tabs[5]?.id);
  await act(async () => tabs[5]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })));
  expect(document.activeElement).toBe(tabs[0]);
});

it('依据按原媒体和原毫秒回放，换报告立即卸载旧播放器', async () => {
  const { view, evidence } = viewWithAdvice();
  await act(async () => root.render(<ReportPage view={view} />));
  await click('查看依据');
  expect(h.media).toHaveBeenCalledWith(evidence.media_asset_id);
  expect(h.player).toHaveBeenLastCalledWith(expect.objectContaining({ src: '/authorized-original', seekRequest: { at_ms: 17250, nonce: 1 } }));
  const changed = structuredClone(view); changed.report.id = 'another-report';
  await act(async () => root.render(<ReportPage view={changed} />));
  expect(container.textContent).not.toContain('原录像播放器');
  expect(container.querySelector('[aria-label="课堂依据与回放"]')).toBeNull();
});

it('已过期依据保留文字，不请求回放', async () => {
  const { view, evidence } = viewWithAdvice(); evidence.availability = 'expired';
  await act(async () => root.render(<ReportPage view={view} />)); await click('查看依据');
  expect(container.textContent).toContain('原始依据已过期');
  expect(h.media).not.toHaveBeenCalled(); expect(h.player).not.toHaveBeenCalled();
});

it('跨批次证据不会提供可回放入口', async () => {
  const { view, evidence } = viewWithAdvice(); evidence.run_id = 'different-run';
  await act(async () => root.render(<ReportPage view={view} />));
  expect(container.querySelector('.report-citation')).toBeNull(); expect(h.media).not.toHaveBeenCalled();
});

it('授权失败或媒体不属于原课堂时不挂载播放器', async () => {
  const { view } = viewWithAdvice();
  h.probe.mockReturnValue({ status: 'failed', note: '播放许可已撤销', recheck: vi.fn() });
  await act(async () => root.render(<ReportPage view={view} />)); await click('查看依据');
  expect(container.textContent).toContain('播放许可已撤销'); expect(h.player).not.toHaveBeenCalled();
  h.media.mockReturnValue({ status: 'ready', data: { media: { id: 'wrong-media', session_id: 'wrong-session', status: 'ready', playback_url: '/wrong' } }, reload: vi.fn() });
  h.probe.mockReturnValue({ status: 'ok', note: null, recheck: vi.fn() });
  await click('关闭依据'); await click('查看依据');
  expect(h.player).not.toHaveBeenCalled(); expect(container.textContent).toContain('这段原录像目前不可回放');
});

it('输入截短与跳过记录用中文说明分析边界', () => {
  expect(reportLimitation('MODEL_INPUT_TRUNCATED:internal-id')).not.toContain('internal-id');
  expect(reportLimitation('MODEL_INPUT_SKIPPED_COUNT:12')).toContain('12 条课堂记录未送入模型');
});
