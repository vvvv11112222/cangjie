/** 展示层格式化与枚举文案。文案只做中文呈现，不改变协议字段和取值。 */

import type {
  CoverageStatus,
  DimensionCode,
  EvidenceAvailability,
  EvidenceKind,
  Interval,
  JobStage,
  JobStatus,
  ObservationType,
  ReportAction,
  ReportStatus,
  ReviewStatus,
  RevisionSourceType,
  RunAction,
  RunMode,
  RunStatus,
  SessionAction,
  SessionStatus,
} from './types';

export type Tone = 'green' | 'blue' | 'amber' | 'red' | 'gray';

export interface Badge {
  label: string;
  tone: Tone;
}

/** 播放位置统一为原始录像起点的整数毫秒，展示时保留毫秒以免丢精度。 */
export function formatTimestamp(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000);
  const milliseconds = ms % 1000;
  const seconds = totalSeconds % 60;
  const minutes = Math.floor(totalSeconds / 60) % 60;
  const hours = Math.floor(totalSeconds / 3600);
  const pad = (value: number, width = 2) => String(value).padStart(width, '0');
  const core = `${pad(minutes)}:${pad(seconds)}.${pad(milliseconds, 3)}`;
  return hours > 0 ? `${pad(hours)}:${core}` : core;
}

export function formatInterval(interval: Interval): string {
  return `${formatTimestamp(interval.start_ms)} – ${formatTimestamp(interval.end_ms)}`;
}

export function formatIntervals(intervals: Interval[]): string {
  return intervals.length === 0 ? '无覆盖区间' : intervals.map(formatInterval).join('，');
}

/**
 * 按样例书写时的时区显示业务时间，避免因开发机时区不同而改变展示值。
 * 输入为 RFC3339（带偏移），无法解析时原样返回并标记。
 */
export function formatDateTime(value: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?(Z|[+-]\d{2}:?\d{2})?$/.exec(
    value,
  );
  if (!match) {
    return `${value}（时间格式无法解析）`;
  }
  const [, year, month, day, hour, minute, second, offset] = match;
  const clock = second ? `${hour}:${minute}:${second}` : `${hour}:${minute}`;
  const zone = offset === 'Z' ? 'UTC+00:00' : offset ? `UTC${offset}` : '无时区';
  return `${year}-${month}-${day} ${clock} (${zone})`;
}

export function truncateHash(hash: string | null): string {
  if (hash === null) {
    return 'null';
  }
  return hash.length <= 16 ? hash : `${hash.slice(0, 12)}…`;
}

/**
 * 合成样例的 UUID 前缀相同，展示时用末四位区分，完整值放在 title 中。
 * 这只是展示缩写，不改变任何 ID 语义。
 */
export function shortId(id: string): string {
  return id.length <= 8 ? id : `…${id.slice(-4)}`;
}

const SESSION_STATUS: Record<SessionStatus, Badge> = {
  planned: { label: '已计划', tone: 'gray' },
  ready: { label: '就绪', tone: 'green' },
  archived: { label: '已归档', tone: 'gray' },
  deleting: { label: '清理中', tone: 'amber' },
  deleted: { label: '已删除', tone: 'red' },
};

const RUN_STATUS: Record<RunStatus, Badge> = {
  queued: { label: '排队中', tone: 'gray' },
  running: { label: '分析中', tone: 'blue' },
  succeeded: { label: '已完成', tone: 'green' },
  partial: { label: '部分完成', tone: 'amber' },
  failed: { label: '已失败', tone: 'red' },
  cancelled: { label: '已取消', tone: 'gray' },
};

const JOB_STATUS: Record<JobStatus, Badge> = {
  queued: { label: '排队', tone: 'gray' },
  running: { label: '进行中', tone: 'blue' },
  succeeded: { label: '成功', tone: 'green' },
  failed: { label: '失败', tone: 'red' },
  cancelled: { label: '已取消', tone: 'gray' },
  skipped: { label: '跳过', tone: 'gray' },
};

const JOB_STAGE: Record<JobStage, string> = {
  probe: '媒体检查',
  audio_analysis: '音频转写',
  video_analysis: '视频采样',
  evidence: '证据建立',
  report: '报告生成',
  validate: '结果校验',
};

const RUN_MODE: Record<RunMode, string> = {
  full: '完整分析',
  report_only: '仅重新生成报告',
  media_prepare: '仅媒体准备',
};

const DIMENSION: Record<DimensionCode, string> = {
  content: '教学内容',
  pace: '教学节奏',
  thinking: '思维启发',
  expression: '教学表达',
  management: '课堂管理',
  technology: '教学技术',
};

const COVERAGE_STATUS: Record<CoverageStatus, Badge> = {
  observed: { label: '已观察', tone: 'green' },
  insufficient: { label: '证据不足', tone: 'amber' },
  not_applicable: { label: '不适用', tone: 'gray' },
};

const OBSERVATION_TYPE: Record<ObservationType, Badge> = {
  highlight: { label: '亮点', tone: 'green' },
  issue: { label: '问题', tone: 'red' },
  observation: { label: '观察', tone: 'blue' },
};

const REVIEW_STATUS: Record<ReviewStatus, Badge> = {
  unreviewed: { label: '待复核', tone: 'amber' },
  accepted: { label: '已确认', tone: 'green' },
  revised: { label: '已修正', tone: 'blue' },
  rejected: { label: '已驳回', tone: 'red' },
};

const REPORT_STATUS: Record<ReportStatus, Badge> = {
  draft: { label: '草稿', tone: 'gray' },
  in_review: { label: '复核中', tone: 'amber' },
  published: { label: '已发布', tone: 'green' },
  superseded: { label: '已被替代', tone: 'gray' },
  withdrawn: { label: '已撤回', tone: 'red' },
};

const EVIDENCE_KIND: Record<EvidenceKind, string> = {
  transcript: '转写',
  frame: '关键帧',
  manual: '人工',
  behavior: '行为',
};

const EVIDENCE_AVAILABILITY: Record<EvidenceAvailability, Badge> = {
  available: { label: '可回看', tone: 'green' },
  expired: { label: '已过期', tone: 'amber' },
  removed: { label: '已移除', tone: 'red' },
};

const SOURCE_TYPE: Record<RevisionSourceType, Badge> = {
  asr: { label: 'ASR 生成', tone: 'blue' },
  manual: { label: '人工修订', tone: 'green' },
};

const SESSION_ACTION: Record<SessionAction, string> = {
  upload: '上传录像',
  prepare_media: '媒体准备',
  analyze: '提交分析',
  edit_transcript: '修改转写',
  view_results: '查看结果',
  view_report: '查看报告',
  archive: '归档',
  delete: '删除',
};

const RUN_ACTION: Record<RunAction, string> = {
  view_results: '查看结果',
  cancel: '取消批次',
  retry: '重试',
};

const REPORT_ACTION: Record<ReportAction, string> = {
  edit: '编辑草稿',
  submit: '提交复核',
  review: '复核观察',
  confirm_report: '确认整篇',
  publish: '发布',
  withdraw: '撤回',
  revise: '修订',
  export: '导出',
};

export const sessionStatusBadge = (value: SessionStatus): Badge => SESSION_STATUS[value];
export const runStatusBadge = (value: RunStatus): Badge => RUN_STATUS[value];
export const jobStatusBadge = (value: JobStatus): Badge => JOB_STATUS[value];
export const jobStageLabel = (value: JobStage): string => JOB_STAGE[value];
export const runModeLabel = (value: RunMode): string => RUN_MODE[value];
export const dimensionLabel = (value: DimensionCode): string => DIMENSION[value];
export const coverageStatusBadge = (value: CoverageStatus): Badge => COVERAGE_STATUS[value];
export const observationTypeBadge = (value: ObservationType): Badge => OBSERVATION_TYPE[value];
export const reviewStatusBadge = (value: ReviewStatus): Badge => REVIEW_STATUS[value];
export const reportStatusBadge = (value: ReportStatus): Badge => REPORT_STATUS[value];
export const evidenceKindLabel = (value: EvidenceKind): string => EVIDENCE_KIND[value];
export const evidenceAvailabilityBadge = (value: EvidenceAvailability): Badge =>
  EVIDENCE_AVAILABILITY[value];
export const sourceTypeBadge = (value: RevisionSourceType): Badge => SOURCE_TYPE[value];
export const sessionActionLabel = (value: SessionAction): string => SESSION_ACTION[value];
export const runActionLabel = (value: RunAction): string => RUN_ACTION[value];
export const reportActionLabel = (value: ReportAction): string => REPORT_ACTION[value];

/** unknown 表示未知说话人，不是空值；只有 null 才表示缺失。 */
export function speakerLabel(value: string): string {
  return value === 'unknown' ? '未知说话人' : value;
}
