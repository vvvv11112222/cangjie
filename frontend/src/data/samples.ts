/**
 * M0 固定样例入口。数据直接引用 contracts/examples 下的 JSON，
 * 不在前端复制副本，样例更新后页面立即使用新内容。
 * M1 接入后端时，替换本文件的具体实现，保持下面导出的 DTO 类型不变。
 */

import type {
  Dimension,
  Evidence,
  Interval,
  Job,
  Observation,
  Report,
  ReportProvenance,
  Results,
  Revision,
  Run,
  Segment,
  Session,
  SessionPage,
} from '../types';
import {
  asArray,
  asBoolean,
  asEnum,
  asInteger,
  asNullableString,
  asRecord,
  asString,
  enumList,
  field,
  guardFail,
  objectList,
  stringList,
} from './guards';

import reportDraftJson from '../../../contracts/examples/report-draft.json';
import resultsJson from '../../../contracts/examples/results.json';
import revisionJson from '../../../contracts/examples/revision.json';
import runPartialJson from '../../../contracts/examples/run-partial.json';
import sessionPageJson from '../../../contracts/examples/session-page.json';

/** 三页用到的固定样例；展示数据来源时直接使用该相对路径。 */
export const FIXTURES = {
  sessionPage: 'contracts/examples/session-page.json',
  run: 'contracts/examples/run-partial.json',
  results: 'contracts/examples/results.json',
  revision: 'contracts/examples/revision.json',
  report: 'contracts/examples/report-draft.json',
} as const;

const SESSION_STATUSES = ['planned', 'ready', 'archived', 'deleting', 'deleted'] as const;
const SESSION_ACTIONS = [
  'upload',
  'prepare_media',
  'analyze',
  'edit_transcript',
  'view_results',
  'view_report',
  'archive',
  'delete',
] as const;
const RUN_MODES = ['full', 'report_only', 'media_prepare'] as const;
const RUN_STATUSES = ['queued', 'running', 'succeeded', 'partial', 'failed', 'cancelled'] as const;
const RUN_ACTIONS = ['view_results', 'cancel', 'retry'] as const;
const JOB_STAGES = [
  'probe',
  'audio_analysis',
  'video_analysis',
  'evidence',
  'report',
  'validate',
] as const;
const JOB_STATUSES = ['queued', 'running', 'succeeded', 'failed', 'cancelled', 'skipped'] as const;
const EVIDENCE_KINDS = ['transcript', 'frame', 'manual', 'behavior'] as const;
const EVIDENCE_AVAILABILITY = ['available', 'expired', 'removed'] as const;
const REVISION_SOURCE_TYPES = ['asr', 'manual'] as const;
const REPORT_STATUSES = ['draft', 'in_review', 'published', 'superseded', 'withdrawn'] as const;
const REPORT_ACTIONS = [
  'edit',
  'submit',
  'review',
  'confirm_report',
  'publish',
  'withdraw',
  'revise',
  'export',
] as const;
const DIMENSION_CODES = [
  'content',
  'pace',
  'thinking',
  'expression',
  'management',
  'technology',
] as const;
const COVERAGE_STATUSES = ['observed', 'insufficient', 'not_applicable'] as const;
const OBSERVATION_TYPES = ['highlight', 'issue', 'observation'] as const;
const REVIEW_STATUSES = ['unreviewed', 'accepted', 'revised', 'rejected'] as const;

function parseInterval(source: Record<string, unknown>, path: string): Interval {
  return { start_ms: asInteger(source, 'start_ms', path), end_ms: asInteger(source, 'end_ms', path) };
}

function parseIntervalList(
  source: Record<string, unknown>,
  key: string,
  path: string,
): Interval[] {
  return objectList(source, key, path).map((item, index) =>
    parseInterval(item, `${path}.${key}[${index}]`),
  );
}

function parseSession(source: Record<string, unknown>, path: string): Session {
  return {
    id: asString(source, 'id', path),
    offering_id: asString(source, 'offering_id', path),
    schedule_entry_id: asNullableString(source, 'schedule_entry_id', path),
    title: asString(source, 'title', path),
    planned_start_at: asString(source, 'planned_start_at', path),
    planned_end_at: asString(source, 'planned_end_at', path),
    is_demo: asBoolean(source, 'is_demo', path),
    status: asEnum(source, 'status', path, SESSION_STATUSES),
    transcript_lock_version: asInteger(source, 'transcript_lock_version', path),
    primary_media_asset_id: asNullableString(source, 'primary_media_asset_id', path),
    latest_run_id: asNullableString(source, 'latest_run_id', path),
    current_report_id: asNullableString(source, 'current_report_id', path),
    allowed_actions: enumList(source, 'allowed_actions', path, SESSION_ACTIONS),
  };
}

function parseJob(source: Record<string, unknown>, path: string): Job {
  return {
    id: asString(source, 'id', path),
    stage: asEnum(source, 'stage', path, JOB_STAGES),
    status: asEnum(source, 'status', path, JOB_STATUSES),
    progress: asInteger(source, 'progress', path),
    error_code: asNullableString(source, 'error_code', path),
    attempts: asInteger(source, 'attempts', path),
    max_attempts: asInteger(source, 'max_attempts', path),
    execution_deadline_at: asNullableString(source, 'execution_deadline_at', path),
  };
}

function parseSegment(source: Record<string, unknown>, path: string): Segment {
  return {
    segment_no: asInteger(source, 'segment_no', path),
    start_ms: asInteger(source, 'start_ms', path),
    end_ms: asInteger(source, 'end_ms', path),
    text_content: asString(source, 'text_content', path),
    speaker_label: asString(source, 'speaker_label', path),
  };
}

function parseEvidence(source: Record<string, unknown>, path: string): Evidence {
  return {
    id: asString(source, 'id', path),
    run_id: asString(source, 'run_id', path),
    session_id: asString(source, 'session_id', path),
    media_asset_id: asString(source, 'media_asset_id', path),
    kind: asEnum(source, 'kind', path, EVIDENCE_KINDS),
    start_ms: asInteger(source, 'start_ms', path),
    end_ms: asInteger(source, 'end_ms', path),
    transcript_segment_id: asNullableString(source, 'transcript_segment_id', path),
    frame_asset_id: asNullableString(source, 'frame_asset_id', path),
    availability: asEnum(source, 'availability', path, EVIDENCE_AVAILABILITY),
    description: asString(source, 'description', path),
    provenance: asRecord(source['provenance'], `${path}.provenance`),
  };
}

function parseDimension(source: Record<string, unknown>, path: string): Dimension {
  return {
    dimension_code: asEnum(source, 'dimension_code', path, DIMENSION_CODES),
    coverage_status: asEnum(source, 'coverage_status', path, COVERAGE_STATUSES),
    summary: asString(source, 'summary', path),
    limitation: asString(source, 'limitation', path),
    coverage: parseIntervalList(source, 'coverage', path),
    summary_evidence_ids: stringList(source, 'summary_evidence_ids', path),
  };
}

function parseObservation(source: Record<string, unknown>, path: string): Observation {
  return {
    id: asString(source, 'id', path),
    dimension_code: asEnum(source, 'dimension_code', path, DIMENSION_CODES),
    observation_type: asEnum(source, 'observation_type', path, OBSERVATION_TYPES),
    observation_text: asString(source, 'observation_text', path),
    suggestion: asString(source, 'suggestion', path),
    review_status: asEnum(source, 'review_status', path, REVIEW_STATUSES),
    evidence_ids: stringList(source, 'evidence_ids', path),
  };
}

function parseProvenance(source: Record<string, unknown>, path: string): ReportProvenance {
  const model = asRecord(source['model'], `${path}.model`);
  const origin = asRecord(source['source'], `${path}.source`);
  return {
    media_asset_id: asString(source, 'media_asset_id', path),
    input_sha256: asString(source, 'input_sha256', path),
    transcript_revision_id: asNullableString(source, 'transcript_revision_id', path),
    model: { name: asString(model, 'name', `${path}.model`), revision: asString(model, 'revision', `${path}.model`) },
    prompt_version: asString(source, 'prompt_version', path),
    prompt_sha256: asString(source, 'prompt_sha256', path),
    manifest_sha256: asString(source, 'manifest_sha256', path),
    coverage: parseIntervalList(source, 'coverage', path),
    limitations: stringList(source, 'limitations', path),
    source: {
      id: asString(origin, 'id', `${path}.source`),
      title: asString(origin, 'title', `${path}.source`),
      attribution: asString(origin, 'attribution', `${path}.source`),
      source_url: asNullableString(origin, 'source_url', `${path}.source`),
      rights_version: asInteger(origin, 'rights_version', `${path}.source`),
    },
  };
}

export function parseSessionPage(value: unknown, path = 'SessionPage'): SessionPage {
  const source = asRecord(value, path);
  return {
    items: objectList(source, 'items', path).map((item, index) =>
      parseSession(item, `${path}.items[${index}]`),
    ),
    next_cursor: asNullableString(source, 'next_cursor', path),
  };
}

export function parseRun(value: unknown, path = 'Run'): Run {
  const source = asRecord(value, path);
  return {
    id: asString(source, 'id', path),
    session_id: asString(source, 'session_id', path),
    media_asset_id: asString(source, 'media_asset_id', path),
    mode: asEnum(source, 'mode', path, RUN_MODES),
    input_transcript_revision_id: asNullableString(source, 'input_transcript_revision_id', path),
    status: asEnum(source, 'status', path, RUN_STATUSES),
    jobs: objectList(source, 'jobs', path).map((item, index) =>
      parseJob(item, `${path}.jobs[${index}]`),
    ),
    stage_plan: enumList(source, 'stage_plan', path, JOB_STAGES),
    report_id: asNullableString(source, 'report_id', path),
    error_code: asNullableString(source, 'error_code', path),
    limitations: stringList(source, 'limitations', path),
    allowed_actions: enumList(source, 'allowed_actions', path, RUN_ACTIONS),
  };
}

export function parseResults(value: unknown, path = 'Results'): Results {
  const source = asRecord(value, path);
  return {
    run_status: asEnum(source, 'run_status', path, RUN_STATUSES),
    transcript_revision_id: asNullableString(source, 'transcript_revision_id', path),
    segments: objectList(source, 'segments', path).map((item, index) =>
      parseSegment(item, `${path}.segments[${index}]`),
    ),
    evidence: objectList(source, 'evidence', path).map((item, index) =>
      parseEvidence(item, `${path}.evidence[${index}]`),
    ),
    frames: objectList(source, 'frames', path).map((item, index) => ({
      asset_id: asString(item, 'asset_id', `${path}.frames[${index}]`),
      timestamp_ms: asInteger(item, 'timestamp_ms', `${path}.frames[${index}]`),
    })),
    events: asArray(source['events'], `${path}.events`),
    limitations: stringList(source, 'limitations', path),
  };
}

export function parseRevision(value: unknown, path = 'Revision'): Revision {
  const source = asRecord(value, path);
  const segments = objectList(source, 'segments', path).map((item, index) =>
    parseSegment(item, `${path}.segments[${index}]`),
  );
  if (segments.length === 0) {
    guardFail(`${path}.segments`, '至少需要一个片段');
  }
  return {
    id: asString(source, 'id', path),
    session_id: asString(source, 'session_id', path),
    media_asset_id: asString(source, 'media_asset_id', path),
    source_run_id: asString(source, 'source_run_id', path),
    parent_revision_id: asNullableString(source, 'parent_revision_id', path),
    revision_no: asInteger(source, 'revision_no', path),
    source_type: asEnum(source, 'source_type', path, REVISION_SOURCE_TYPES),
    content_sha256: asString(source, 'content_sha256', path),
    created_by: asString(source, 'created_by', path),
    created_at: asString(source, 'created_at', path),
    reason: asString(source, 'reason', path),
    transcript_lock_version: asInteger(source, 'transcript_lock_version', path),
    segments,
  };
}

export function parseReport(value: unknown, path = 'Report'): Report {
  const source = asRecord(value, path);
  const provenance = field(source, 'provenance', path);
  return {
    id: asString(source, 'id', path),
    run_id: asString(source, 'run_id', path),
    session_id: asString(source, 'session_id', path),
    revision: asInteger(source, 'revision', path),
    lock_version: asInteger(source, 'lock_version', path),
    status: asEnum(source, 'status', path, REPORT_STATUSES),
    summary: asString(source, 'summary', path),
    dimensions: objectList(source, 'dimensions', path).map((item, index) =>
      parseDimension(item, `${path}.dimensions[${index}]`),
    ),
    observations: objectList(source, 'observations', path).map((item, index) =>
      parseObservation(item, `${path}.observations[${index}]`),
    ),
    summary_evidence_ids: stringList(source, 'summary_evidence_ids', path),
    content_sha256: asNullableString(source, 'content_sha256', path),
    reviewed_content_sha256: asNullableString(source, 'reviewed_content_sha256', path),
    reviewed_by: asNullableString(source, 'reviewed_by', path),
    reviewed_at: asNullableString(source, 'reviewed_at', path),
    provenance:
      provenance === null
        ? null
        : parseProvenance(asRecord(provenance, `${path}.provenance`), `${path}.provenance`),
    allowed_actions: enumList(source, 'allowed_actions', path, REPORT_ACTIONS),
  };
}

export const sessionPage: SessionPage = parseSessionPage(sessionPageJson);
export const run: Run = parseRun(runPartialJson);
export const results: Results = parseResults(resultsJson);
export const revision: Revision = parseRevision(revisionJson);
export const report: Report = parseReport(reportDraftJson);
