import { describe, expect, it } from 'vitest';
import type { Evidence, Segment, Session } from '../types';
import {
  PLAYBACK_SPEEDS,
  activeTargetId,
  formatSpeed,
  isJumpable,
  jumpTargets,
  msToSeconds,
  pickPlaybackMediaId,
  secondsToMs,
} from './playback';
import type { MediaLite } from './sources';

const SESSION: Session = {
  id: '00000000-0000-4000-8000-000000000001',
  offering_id: '00000000-0000-4000-8000-000000000014',
  schedule_entry_id: null,
  title: '合成课堂',
  planned_start_at: '2026-10-02T09:00:00+08:00',
  planned_end_at: '2026-10-02T10:00:00+08:00',
  is_demo: true,
  status: 'ready',
  transcript_lock_version: 2,
  primary_media_asset_id: '00000000-0000-4000-8000-000000000002',
  latest_run_id: null,
  current_report_id: null,
  allowed_actions: [],
};

const SEGMENTS: Segment[] = [
  { segment_no: 1, start_ms: 8000, end_ms: 12000, text_content: '第二段', speaker_label: 'unknown' },
  { segment_no: 0, start_ms: 1000, end_ms: 5000, text_content: '第一段', speaker_label: 'unknown' },
];

function evidence(overrides: Partial<Evidence>): Evidence {
  return {
    id: '00000000-0000-4000-8000-000000000005',
    run_id: '00000000-0000-4000-8000-000000000003',
    session_id: SESSION.id,
    media_asset_id: '00000000-0000-4000-8000-000000000002',
    kind: 'transcript',
    start_ms: 1000,
    end_ms: 5000,
    transcript_segment_id: null,
    frame_asset_id: null,
    availability: 'available',
    description: '证据说明',
    provenance: {},
    ...overrides,
  };
}

const MEDIA: MediaLite[] = [
  { id: '00000000-0000-4000-8000-000000000004', kind: 'proxy', status: 'ready', allowed_actions: ['playback'] },
];

describe('回放目标媒体', () => {
  it('优先课堂主媒体', () => {
    expect(pickPlaybackMediaId(SESSION, MEDIA)).toBe(SESSION.primary_media_asset_id);
  });

  it('没有主媒体时退回到就绪且允许 playback 的媒体', () => {
    const session: Session = { ...SESSION, primary_media_asset_id: null };
    expect(pickPlaybackMediaId(session, MEDIA)).toBe(MEDIA[0]?.id);
  });

  it('既没有主媒体也没有可播放媒体时返回 null，不拿别的媒体顶替', () => {
    const session: Session = { ...SESSION, primary_media_asset_id: null };
    const pending: MediaLite[] = [{ ...MEDIA[0]!, status: 'pending' }];
    const noPlayback: MediaLite[] = [{ ...MEDIA[0]!, allowed_actions: ['analyze'] }];
    expect(pickPlaybackMediaId(session, [])).toBeNull();
    expect(pickPlaybackMediaId(session, pending)).toBeNull();
    expect(pickPlaybackMediaId(session, noPlayback)).toBeNull();
  });
});

describe('跳转点', () => {
  it('片段与可用证据合并后按时间排序', () => {
    const targets = jumpTargets(SEGMENTS, [evidence({ start_ms: 6000, end_ms: 7000, description: '画面证据', kind: 'frame' })]);
    expect(targets.map((item) => item.at_ms)).toEqual([1000, 6000, 8000]);
    expect(targets[0]).toMatchObject({ kind: 'segment', segment_no: 0, text: '第一段' });
    expect(targets[1]).toMatchObject({ kind: 'evidence', evidence_kind: 'frame', text: '画面证据' });
  });

  it('过期或已移除的证据不参与跳转', () => {
    const expired = evidence({ id: '00000000-0000-4000-8000-000000000006', availability: 'expired' });
    const removed = evidence({ id: '00000000-0000-4000-8000-000000000007', availability: 'removed' });
    expect(jumpTargets([], [expired, removed])).toHaveLength(0);
  });

  it('命中区间是左闭右开 [start_ms,end_ms)', () => {
    const targets = jumpTargets(SEGMENTS, []);
    expect(activeTargetId(targets, 1000)).toBe('segment:0');
    expect(activeTargetId(targets, 4999)).toBe('segment:0');
    // 5000 已经落在 [1000,5000) 之外，且还没到 8000，所以没有命中。
    expect(activeTargetId(targets, 5000)).toBeNull();
    expect(activeTargetId(targets, 8000)).toBe('segment:1');
  });

  it('超出媒体时长的目标不允许跳转，时长未知时不拦截', () => {
    const targets = jumpTargets(SEGMENTS, []);
    // targets[1] 是 8000ms 起的那段：媒体只有 8 秒时它已经落在时长之外。
    expect(isJumpable(targets[1]!, 8000)).toBe(false);
    expect(isJumpable(targets[1]!, 8001)).toBe(true);
    expect(isJumpable(targets[1]!, null)).toBe(true);
  });
});

describe('播放器辅助换算', () => {
  it('毫秒与秒互转不会丢精度', () => {
    expect(msToSeconds(1500)).toBe(1.5);
    expect(secondsToMs(1.5)).toBe(1500);
    expect(secondsToMs(Number.NaN)).toBe(0);
    expect(secondsToMs(-1)).toBe(0);
  });

  it('倍速档位包含 1× 且文案带乘号', () => {
    expect(PLAYBACK_SPEEDS).toContain(1);
    expect(formatSpeed(1)).toBe('1×');
    expect(formatSpeed(0.75)).toBe('0.75×');
  });
});
