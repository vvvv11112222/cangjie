/**
 * 读到的 DTO 形状，逐字段对应 contracts/v1.schema.json 的 $defs。
 * 字段名保持 JSON 的 snake_case，前端不做驼峰改写，避免与协议产生第二套字段。
 * 结构校验由仓库的 `python tools/check_docs.py` 负责；这里只声明类型并让 UI 的
 * 枚举分支可被 TypeScript 检查。运行期守卫见 src/data/samples.ts。
 */

export type SessionStatus = 'planned' | 'ready' | 'archived' | 'deleting' | 'deleted';

export type SessionAction =
  | 'upload'
  | 'prepare_media'
  | 'analyze'
  | 'edit_transcript'
  | 'view_results'
  | 'view_report'
  | 'archive'
  | 'delete';

export interface Session {
  id: string;
  offering_id: string;
  schedule_entry_id: string | null;
  title: string;
  planned_start_at: string;
  planned_end_at: string;
  is_demo: boolean;
  status: SessionStatus;
  transcript_lock_version: number;
  primary_media_asset_id: string | null;
  latest_run_id: string | null;
  current_report_id: string | null;
  allowed_actions: SessionAction[];
}

export interface SessionPage {
  items: Session[];
  next_cursor: string | null;
}

export type RunMode = 'full' | 'report_only' | 'media_prepare';
export type RunStatus = 'queued' | 'running' | 'succeeded' | 'partial' | 'failed' | 'cancelled';
export type RunAction = 'view_results' | 'cancel' | 'retry';
export type JobStage =
  | 'probe'
  | 'audio_analysis'
  | 'video_analysis'
  | 'evidence'
  | 'report'
  | 'validate';
export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'skipped';

export interface Job {
  id: string;
  stage: JobStage;
  status: JobStatus;
  progress: number;
  error_code: string | null;
  attempts: number;
  max_attempts: number;
  execution_deadline_at: string | null;
}

export interface Run {
  id: string;
  session_id: string;
  media_asset_id: string;
  mode: RunMode;
  input_transcript_revision_id: string | null;
  status: RunStatus;
  jobs: Job[];
  stage_plan: JobStage[];
  report_id: string | null;
  error_code: string | null;
  limitations: string[];
  allowed_actions: RunAction[];
}

export interface Interval {
  start_ms: number;
  end_ms: number;
}

export interface Segment {
  segment_no: number;
  start_ms: number;
  end_ms: number;
  text_content: string;
  speaker_label: string;
}

export type EvidenceKind = 'transcript' | 'frame' | 'manual' | 'behavior';
export type EvidenceAvailability = 'available' | 'expired' | 'removed';

export interface Evidence {
  id: string;
  run_id: string;
  session_id: string;
  media_asset_id: string;
  kind: EvidenceKind;
  start_ms: number;
  end_ms: number;
  transcript_segment_id: string | null;
  frame_asset_id: string | null;
  availability: EvidenceAvailability;
  description: string;
  provenance: Record<string, unknown>;
}

export interface Results {
  run_status: RunStatus;
  transcript_revision_id: string | null;
  segments: Segment[];
  evidence: Evidence[];
  frames: { asset_id: string; timestamp_ms: number }[];
  events: unknown[];
  limitations: string[];
}

export type RevisionSourceType = 'asr' | 'manual';

export interface Revision {
  id: string;
  session_id: string;
  media_asset_id: string;
  source_run_id: string;
  parent_revision_id: string | null;
  revision_no: number;
  source_type: RevisionSourceType;
  content_sha256: string;
  created_by: string;
  created_at: string;
  reason: string;
  transcript_lock_version: number;
  segments: Segment[];
}

export type ReportStatus = 'draft' | 'in_review' | 'published' | 'superseded' | 'withdrawn';
export type ReportAction =
  | 'edit'
  | 'submit'
  | 'review'
  | 'confirm_report'
  | 'publish'
  | 'withdraw'
  | 'revise'
  | 'export';
export type DimensionCode =
  | 'content'
  | 'pace'
  | 'thinking'
  | 'expression'
  | 'management'
  | 'technology';
export type CoverageStatus = 'observed' | 'insufficient' | 'not_applicable';
export type ObservationType = 'highlight' | 'issue' | 'observation';
export type ReviewStatus = 'unreviewed' | 'accepted' | 'revised' | 'rejected';

export interface Dimension {
  dimension_code: DimensionCode;
  coverage_status: CoverageStatus;
  summary: string;
  limitation: string;
  coverage: Interval[];
  summary_evidence_ids: string[];
}

export interface Observation {
  id: string;
  dimension_code: DimensionCode;
  observation_type: ObservationType;
  observation_text: string;
  suggestion: string;
  review_status: ReviewStatus;
  evidence_ids: string[];
}

export interface ReportProvenance {
  media_asset_id: string;
  input_sha256: string;
  transcript_revision_id: string | null;
  model: { name: string; revision: string };
  prompt_version: string;
  prompt_sha256: string;
  manifest_sha256: string;
  coverage: Interval[];
  limitations: string[];
  source: {
    id: string;
    title: string;
    attribution: string;
    source_url: string | null;
    rights_version: number;
  };
}

export interface Report {
  id: string;
  run_id: string;
  session_id: string;
  revision: number;
  lock_version: number;
  status: ReportStatus;
  summary: string;
  dimensions: Dimension[];
  observations: Observation[];
  summary_evidence_ids: string[];
  content_sha256: string | null;
  reviewed_content_sha256: string | null;
  reviewed_by: string | null;
  reviewed_at: string | null;
  provenance: ReportProvenance | null;
  allowed_actions: ReportAction[];
}
