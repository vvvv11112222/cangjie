import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getVisualObservations, getVisualPeople } from './visualApi';
import type { VisualObservation } from './visualApi';
import { loadVisualOverlay, sampledTimeAt } from './visualOverlay';

vi.mock('./visualApi', () => ({ getVisualPeople: vi.fn(), getVisualObservations: vi.fn() }));
const people = vi.mocked(getVisualPeople); const observations = vi.mocked(getVisualObservations);
const row = (timestamp_ms: number): VisualObservation => ({ timestamp_ms, bbox: [1, 2, 3, 4], frame_asset_id: null,
  behaviors: { hand_raise: true, head_down: null, leave_seat: null, possible_phone: null } });
const options = () => ({ cancelled: () => false, progress: vi.fn() });
beforeEach(() => { vi.resetAllMocks(); });

describe('全班画框读取', () => {
  it('读全人物及个人分页，按同一实际时间建立索引', async () => {
    people.mockImplementation(async (_run, cursor) => cursor
      ? { items: [{ track_id: 'T0002', sample_count: 1 }], next_cursor: null }
      : { items: [{ track_id: 'T0001', sample_count: 2 }], next_cursor: 'people-next' });
    observations.mockImplementation(async (_run, track, cursor) => track === 'T0002'
      ? { items: [row(0)], next_cursor: null }
      : cursor ? { items: [row(500)], next_cursor: null } : { items: [row(0)], next_cursor: 'rows-next' });
    const opts = options(); const data = await loadVisualOverlay('run', opts);
    expect(data.people).toHaveLength(2);
    expect(data.frames.get(0)?.map(p => p.track_id)).toEqual(['T0001', 'T0002']);
    expect(data.frames.get(500)?.map(p => p.track_id)).toEqual(['T0001']);
    expect(data.byTrack.get('T0001')).toHaveLength(2);
    expect(observations).toHaveBeenCalledWith('run', 'T0001', 'rows-next');
    expect(opts.progress).toHaveBeenLastCalledWith(2, 2);
  });
  it('最多同时读取三个人，不一次发出整班请求', async () => {
    people.mockResolvedValue({ items: [1, 2, 3, 4].map(n => ({ track_id: `T${n}`, sample_count: 1 })), next_cursor: null });
    const release: (() => void)[] = []; let active = 0; let peak = 0;
    observations.mockImplementation(async (_run, track) => {
      active++; peak = Math.max(peak, active);
      if (track !== 'T4') await new Promise<void>(resolve => release.push(resolve));
      active--; return { items: [row(0)], next_cursor: null };
    });
    const loading = loadVisualOverlay('run', options());
    await vi.waitFor(() => expect(observations).toHaveBeenCalledTimes(3));
    release.forEach(resolve => resolve());
    expect((await loading).people).toHaveLength(4); expect(peak).toBe(3);
  });
  it('重复游标或空分页不能被当成已读取全班', async () => {
    people.mockResolvedValueOnce({ items: [{ track_id: 'T1', sample_count: 1 }], next_cursor: 'same' })
      .mockResolvedValueOnce({ items: [{ track_id: 'T2', sample_count: 1 }], next_cursor: 'same' });
    await expect(loadVisualOverlay('run', options())).rejects.toThrow('分页没有前进');
    people.mockResolvedValue({ items: [], next_cursor: 'empty-next' });
    await expect(loadVisualOverlay('run', options())).rejects.toThrow('空页');
    expect(observations).not.toHaveBeenCalled();
  });
  it('任意人物读取失败或数量不全，不返回不完整的全班结果', async () => {
    people.mockResolvedValue({ items: [{ track_id: 'T1', sample_count: 2 }], next_cursor: null });
    observations.mockRejectedValueOnce(new Error('读取失败'));
    await expect(loadVisualOverlay('run', options())).rejects.toThrow('读取失败');
    observations.mockResolvedValue({ items: [row(0)], next_cursor: null });
    await expect(loadVisualOverlay('run', options())).rejects.toThrow('尚未读全');
  });
  it('关闭或切换批次后，不继续读取后续分页', async () => {
    let cancelled = false;
    people.mockResolvedValue({ items: [{ track_id: 'T1', sample_count: 2 }], next_cursor: null });
    observations.mockImplementation(async () => { cancelled = true; return { items: [row(0)], next_cursor: 'next' }; });
    await expect(loadVisualOverlay('run', { ...options(), cancelled: () => cancelled })).rejects.toThrow('已停止');
    expect(observations).toHaveBeenCalledTimes(1);
  });
});
describe('整班同一画面时间匹配', () => {
  it('就近匹配，时间中断和范围外不延长画框', () => {
    expect(sampledTimeAt([0, 520, 1000], 760, 500)).toBe(520);
    expect(sampledTimeAt([0, 520, 1000], 780, 500)).toBe(1000);
    expect(sampledTimeAt([0, 5000], 2500, 500)).toBeNull();
    expect(sampledTimeAt([0, 520, 1000], 1400, 500)).toBeNull();
    expect(sampledTimeAt([], 0, 500)).toBeNull();
  });
});
