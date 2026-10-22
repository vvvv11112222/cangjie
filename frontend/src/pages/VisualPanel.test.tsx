// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import summary from '../../../contracts/examples/visual-summary.json';
import sampleRun from '../../../contracts/examples/run-partial.json';
import { parseRun } from '../data/samples';
import { VisualPanel } from './VisualPanel';
const api = vi.hoisted(() => ({ summary: vi.fn(), people: vi.fn(), observations: vi.fn(), runs: vi.fn(), media: vi.fn() }));
vi.mock('../data/hooks', () => ({ useRunPolling: () => ({ run: null, error: null }) }));
vi.mock('../data/m2Api', () => ({ listSessionRuns: (...args: unknown[]) => api.runs(...args) }));
vi.mock('../data/sources', () => ({ isApiMode: () => true, getMedia: (...args: unknown[]) => api.media(...args) }));
vi.mock('../data/visualApi', async importOriginal => ({ ...(await importOriginal<typeof import('../data/visualApi')>()),
  getVisualSummary: (...args: unknown[]) => api.summary(...args), getVisualPeople: (...args: unknown[]) => api.people(...args),
  getVisualObservations: (...args: unknown[]) => api.observations(...args) }));
let root: Root; let container: HTMLDivElement;
const originalScroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView');
const run = { ...parseRun(sampleRun), id: summary.run_id, media_asset_id: summary.media_asset_id, mode: 'visual_only' as const, status: 'succeeded' as const, report_id: null };
const row = { timestamp_ms: 0, bbox: [10, 10, 40, 50], behaviors: { hand_raise: true, head_down: null, leave_seat: false, possible_phone: null }, frame_asset_id: null };
beforeEach(() => {
  vi.clearAllMocks();
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined);
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() });
  api.runs.mockResolvedValue({ items: [run], next_cursor: null }); api.summary.mockResolvedValue(summary);
  api.people.mockResolvedValue({ items: [{ track_id: 'T0001', sample_count: 1 }, { track_id: 'T0002', sample_count: 1 }], next_cursor: null });
  api.observations.mockImplementation(async (_run, track) => ({ items: [{ ...row, behaviors: { ...row.behaviors, hand_raise: track === 'T0001' } }], next_cursor: null }));
  api.media.mockResolvedValue({ id: run.media_asset_id, allowed_actions: ['playback'], playback_url: `/api/v1/media/${run.media_asset_id}/content` });
  container = document.createElement('div'); document.body.append(container); root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount()); container.remove(); vi.restoreAllMocks();
  if (originalScroll) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', originalScroll);
  else Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView');
});
async function mount(): Promise<void> { await act(async () => { root.render(<VisualPanel sessionId={run.session_id} currentRun={run} />); }); }
async function toggleAll(): Promise<void> { await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click()); }
describe('视觉观察页面', () => {
  it('a history failure is not an empty result and can be retried', async () => {
    api.runs.mockRejectedValueOnce(new Error('temporary history failure'));
    await act(async () => root.render(<VisualPanel sessionId={run.session_id} currentRun={null}/>));
    expect(container.textContent).not.toContain('还没有视觉分析记录');
    const retry = [...container.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === '重试读取分析记录');
    expect(retry).toBeDefined();
    await act(async () => retry!.click());
    expect(container.querySelector('.visual-metrics')).not.toBeNull();
    expect(container.textContent).not.toContain('temporary history failure');
  });
  it('clearly names the running behavior analysis stage', async () => {
    const running = { ...run, status: 'running' as const, stage_plan: ['probe', 'video_analysis'] as const };
    await act(async () => root.render(<VisualPanel sessionId={run.session_id} currentRun={{ ...running, stage_plan: [...running.stage_plan] }}/>));
    expect(container.querySelector('.stage-flow')?.textContent).toContain('举手与低头识别');
    expect(container.querySelector('.stage-flow')?.textContent).not.toContain('视频采样');
  });
  it('全班画框读完后，侧栏同步完整人物，选择后续页人物不重复请求', async () => {
    api.people.mockImplementation(async (_run, cursor) => cursor
      ? { items: [{ track_id: 'T0003', sample_count: 1 }], next_cursor: null }
      : { items: [{ track_id: 'T0001', sample_count: 1 }], next_cursor: 'second' });
    await mount();
    expect(container.querySelectorAll('.visual-person')).toHaveLength(1);
    await toggleAll();
    expect(container.querySelectorAll('.visual-person')).toHaveLength(2);
    expect(container.textContent).not.toContain('加载更多人物');
    const requests = api.observations.mock.calls.length;
    await act(async () => [...container.querySelectorAll<HTMLButtonElement>('.visual-person')].find(b => b.textContent?.includes('T0003'))!.click());
    expect(container.textContent).toContain('T0003 的画面记录');
    expect(api.observations).toHaveBeenCalledTimes(requests);
  });
  it('opens the exact recording belonging to the selected visual run', async () => {
    const open = vi.fn();
    await act(async () => root.render(<VisualPanel sessionId={run.session_id} currentRun={run} onReplayMedia={open}/>));
    await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === '在回放中打开此录像')!.click());
    expect(open).toHaveBeenCalledWith(run.media_asset_id);
  });
  it('显示全部人物框，选人不改变颜色，切换显示范围复用缓存', async () => {
    await mount(); await toggleAll();
    const colors = () => [...container.querySelectorAll('.visual-overlay g')].map(g => [g.getAttribute('data-track-id'), g.querySelector('rect')?.getAttribute('stroke')]);
    expect(colors()).toEqual([['T0001', 'var(--danger)'], ['T0002', 'var(--muted)']]);
    expect(container.textContent).toContain('已读取全部 2 个编号');
    const requests = api.observations.mock.calls.length;
    const person = [...container.querySelectorAll<HTMLButtonElement>('.visual-person')].find(b => b.textContent?.includes('T0002'))!;
    await act(async () => person.click());
    expect(colors()).toEqual([['T0001', 'var(--danger)'], ['T0002', 'var(--muted)']]);
    await toggleAll(); expect(container.querySelectorAll('.visual-overlay rect')).toHaveLength(1);
    await toggleAll(); expect(container.querySelectorAll('.visual-overlay rect')).toHaveLength(2);
    expect(api.observations).toHaveBeenCalledTimes(requests);
    const player = container.querySelector('video')!;
    await act(async () => { player.currentTime = 10; player.dispatchEvent(new Event('timeupdate')); });
    expect(container.querySelector('.visual-overlay')).toBeNull();
    expect(container.textContent).toContain('当前画面显示 0 个人的框');
  });
  it('全班读取失败不把部分结果说成全部，仍能看个人并重试', async () => {
    await mount();
    api.observations.mockRejectedValueOnce(new Error('暂时读取失败'));
    await toggleAll();
    expect(container.textContent).toContain('全班画框读取失败');
    expect(container.textContent).not.toContain('已读取全部');
    expect(container.querySelectorAll('.visual-overlay rect')).toHaveLength(1);
    const retry = [...container.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === '重试读取全班画框')!;
    await act(async () => retry.click());
    expect(container.querySelectorAll('.visual-overlay rect')).toHaveLength(2);
    expect(container.textContent).toContain('已读取全部 2 个编号');
  });
  it('展示右侧人物与三态记录，切换人物不改变原始结果', async () => {
    await mount();
    expect(container.textContent).toContain('看不清'); expect(container.textContent).toContain('T0001 的画面记录');
    expect(container.querySelector('.visual-overlay rect')?.getAttribute('stroke')).toBe('var(--danger)');
    const person = [...container.querySelectorAll<HTMLButtonElement>('.visual-person')].find(button => button.textContent?.includes('T0002'))!;
    await act(async () => person.click());
    expect(container.textContent).toContain('T0002 的画面记录');
    expect(container.querySelector('.visual-overlay rect')?.getAttribute('stroke')).toBe('var(--muted)');
  });
  it('新人物尚在读取时不显示上一个人的观察', async () => {
    await mount(); api.observations.mockReturnValue(new Promise(() => undefined));
    const person = [...container.querySelectorAll<HTMLButtonElement>('.visual-person')].find(button => button.textContent?.includes('T0002'))!;
    await act(async () => person.click());
    expect(container.querySelector('.visual-overlay rect')).toBeNull();
    expect(container.textContent).toContain('这个时刻没有已加载的观察');
  });
  it('结果接口出错时显示失败，不伪装成没有动作', async () => {
    api.summary.mockRejectedValue(new Error('暂时不可读取'));
    await mount(); expect(container.querySelector('[role=alert]')).not.toBeNull();
    expect(container.querySelector('.visual-metrics')).toBeNull();
  });
  it('只允许分析时直接展示记录，不创建播放器或伪造播放地址', async () => {
    api.media.mockResolvedValue({ id: run.media_asset_id, allowed_actions: ['analyze'], playback_url: null });
    api.observations.mockResolvedValue({ items: [{ ...row, timestamp_ms: 1520, frame_asset_id: 'frame-id' }], next_cursor: null });
    await mount();
    expect(container.textContent).toContain('分析结果已保存');
    expect(container.querySelector('video')).toBeNull();
    expect(container.querySelector('.visual-evidence')).toBeNull();
    expect(container.querySelector('.visual-workspace table')).not.toBeNull();
    const inspect = [...container.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === '查看记录')!;
    await act(async () => inspect.click());
    expect(container.textContent).toContain('00:01.520');
    expect(container.textContent).toContain('举手：发现');
  });
  it('播放信息请求失败仍能读取分析，重新检查回放不重新分析或清空人物', async () => {
    api.media.mockRejectedValueOnce(new Error('播放暂不可用'));
    await mount();
    expect(container.textContent).toContain('T0001 的画面记录');
    expect(container.textContent).toContain('播放信息读取失败');
    const retry = [...container.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === '重新检查回放')!;
    await act(async () => retry.click());
    expect(container.querySelector('video')).not.toBeNull();
    expect(container.textContent).toContain('T0001 的画面记录');
    expect(api.summary).toHaveBeenCalledTimes(1);
    expect(api.observations).toHaveBeenCalledTimes(1);
    expect(api.media).toHaveBeenCalledTimes(2);
  });
  it('定位记录时跳到原录像时间并把播放器带回视野', async () => {
    api.observations.mockResolvedValue({ items: [{ ...row, timestamp_ms: 1520 }], next_cursor: null });
    await mount();
    const player = container.querySelector('video')!;
    const locate = [...container.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === '定位录像')!;
    await act(async () => locate.click());
    expect(player.currentTime).toBe(1.52);
    expect(player.scrollIntoView).toHaveBeenCalledWith({ block: 'center', behavior: 'smooth' });
    expect(container.querySelector('.visual-overlay rect')?.getAttribute('stroke')).toBe('var(--danger)');
    expect(container.textContent).toContain('00:01.520');
  });
});
