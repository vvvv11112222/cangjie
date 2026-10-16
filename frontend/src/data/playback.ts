/**
 * 回放与证据跳转的纯计算：挑播放目标、把片段/证据变成跳转点、算当前高亮。
 * 不碰 DOM，方便单元测试；组件只负责把它接到 <video> 上。
 *
 * 时间口径来自 docs/开发协议.md 第 1、2 节：片段与证据的起止时间都是
 * "原录像起点算起的整数毫秒"，播放代理不裁剪、不变速（origin_offset_ms=0），
 * 因此代理时间与原录像时间一致，跳转直接按毫秒定位，不做偏移换算。
 */

import type { Evidence, EvidenceKind, Media, Results, Segment, Session } from '../types';
import type { MediaLite } from './sources';
import type { TranscriptView } from './viewModel';
import { isUuid, sameId } from './ids';

/** 对照原媒体归属，而不是 proxy 的播放地址；无法核对时不叠加旧结果。 */
export function playbackTranscript(media: Media | null, view: TranscriptView): {
  segments: Segment[];
  evidence: Evidence[];
  frames: Results['frames'];
  note: string | null;
} {
  const empty = { segments: [], evidence: [], frames: [], note: null };
  if (media === null) return empty;
  const equal = (a: string | null, b: string | null) =>
    a !== null && b !== null && isUuid(a) && isUuid(b) && sameId(a, b);
  const originalId = media.kind === 'proxy' ? media.parent_asset_id : media.kind === 'source' ? media.id : null;
  const run = view.run;
  if (run === null || !equal(originalId, run.media_asset_id) || !equal(media.session_id, run.session_id)) {
    return { ...empty, note: '当前录像与所选分析结果不同，或结果归属尚未确认；不叠加该结果的字幕与定位。' };
  }
  const revision = view.revision;
  if ((view.results.transcript_revision_id !== null && revision === null) || (revision !== null && (
    !equal(originalId, revision.media_asset_id) || !equal(media.session_id, revision.session_id) ||
    (!equal(run.id, revision.source_run_id) && !equal(run.input_transcript_revision_id, revision.id))
  ))) {
    return { ...empty, note: '转写修订与当前录像或批次归属不一致，无法装入字幕与定位。' };
  }
  const evidence = view.evidence.filter(item =>
    equal(originalId, item.media_asset_id) && equal(media.session_id, item.session_id) && equal(run.id, item.run_id),
  );
  return {
    segments: view.segments,
    evidence,
    frames: view.results.frames,
    note: evidence.length === view.evidence.length ? null : '部分证据与当前录像或批次不匹配，已停止这些证据的定位。',
  };
}

/** 倍速档位。浏览器 <video> 支持的常用档位，保持整数与半档，避免"看不懂的自定义速度"。 */
export const PLAYBACK_SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2] as const;
export type PlaybackSpeed = (typeof PLAYBACK_SPEEDS)[number];

export interface JumpTarget {
  /** 目标自身 id：证据是 evidence.id，片段是 `segment:{segment_no}`。 */
  id: string;
  kind: 'segment' | 'evidence' | 'frame';
  at_ms: number;
  end_ms: number;
  text: string;
  /** 片段序号；证据目标为 null。 */
  segment_no: number | null;
  /** 证据类型；片段目标为 null。 */
  evidence_kind: EvidenceKind | null;
  /** 关键帧媒体资产；片段和证据目标为 null。 */
  frame_asset_id: string | null;
}

/**
 * 回放目标媒体：优先课堂主媒体（协议里 Session.primary_media_asset_id 就是当前主媒体），
 * 没有主媒体时退回到媒体列表里第一个"就绪且允许 playback"的媒体。
 * 都没有就返回 null，页面显示空态而不是拿别的媒体冒充。
 */
export function pickPlaybackMediaId(session: Session, items: readonly MediaLite[]): string | null {
  if (session.primary_media_asset_id !== null) {
    return session.primary_media_asset_id;
  }
  const playable = items.find(
    (item) => item.status === 'ready' && item.allowed_actions.includes('playback'),
  );
  return playable?.id ?? null;
}

/**
 * 由批次结果的片段与证据生成跳转点。
 * 只收 available 的证据：过期（expired）和已移除（removed）的证据不能再定位到原录像，
 * 这与协议里 __EVIDENCE_UNAVAILABLE__ 的语义一致（见开发协议第 1.2 节错误码表）。
 */
export function jumpTargets(
  segments: readonly Segment[],
  evidence: readonly Evidence[],
  frames: readonly Results['frames'][number][] = [],
): JumpTarget[] {
  const fromSegments: JumpTarget[] = segments.map((segment) => ({
    id: `segment:${segment.segment_no}`,
    kind: 'segment',
    at_ms: segment.start_ms,
    end_ms: segment.end_ms,
    text: segment.text_content,
    segment_no: segment.segment_no,
    evidence_kind: null,
    frame_asset_id: null,
  }));
  const fromEvidence: JumpTarget[] = evidence
    .filter((item) => item.availability === 'available')
    .map((item) => ({
      id: `evidence:${item.id}`,
      kind: 'evidence',
      at_ms: item.start_ms,
      end_ms: item.end_ms,
      text: item.description,
      segment_no: null,
      evidence_kind: item.kind,
      frame_asset_id: null,
    }));
  const fromFrames: JumpTarget[] = frames.map((frame) => ({
    id: `frame:${frame.asset_id}`,
    kind: 'frame',
    at_ms: frame.timestamp_ms,
    // 关键帧是瞬时采样，给高亮区间 1 秒；跳转仍使用准确的 timestamp_ms。
    end_ms: frame.timestamp_ms + 1_000,
    text: `关键帧 ${frame.asset_id.slice(-4)}`,
    segment_no: null,
    evidence_kind: null,
    frame_asset_id: frame.asset_id,
  }));
  return [...fromSegments, ...fromEvidence, ...fromFrames].sort(
    (left, right) => left.at_ms - right.at_ms || left.id.localeCompare(right.id),
  );
}

/** 当前播放位置命中的跳转点（区间是 [start_ms,end_ms)）；没有命中返回 null。 */
export function activeTargetId(targets: readonly JumpTarget[], timeMs: number): string | null {
  const hit = targets.find((target) => timeMs >= target.at_ms && timeMs < target.end_ms);
  return hit?.id ?? null;
}

/**
 * 跳转点是否落在媒体时长内。
 * 时长未知（duration_ms=null）时不做判断，交给浏览器；越界时页面禁用该跳转并说明原因，
 * 不能跳到一个不存在的时刻还装作成功。
 */
export function isJumpable(target: JumpTarget, durationMs: number | null): boolean {
  return durationMs === null || target.at_ms < durationMs;
}

/** 倍速显示文案：1 → 1×，0.75 → 0.75×。 */
export function formatSpeed(speed: number): string {
  return `${speed}×`;
}

/** 毫秒 → <video> 的秒（协议是整数毫秒，播放器 API 是秒）。 */
export function msToSeconds(ms: number): number {
  return ms / 1000;
}

/** <video> 的秒 → 毫秒，用于回写"当前播放位置"。 */
export function secondsToMs(seconds: number): number {
  if (!Number.isFinite(seconds) || seconds < 0) {
    return 0;
  }
  return Math.round(seconds * 1000);
}
