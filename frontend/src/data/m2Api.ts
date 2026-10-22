/** M2 来源接口：页面只经 Go HTTP API 读写，不直接接触存储。 */
import { requestJson } from './http';
import { asArray, asBoolean, asEnum, asInteger, asNullableString, asRecord, asString } from './guards';
import { parseRun } from './samples';
import type { Run } from '../types';

export type SourceUse = 'playback' | 'analysis' | 'export';
export type SourceType = 'self_recorded' | 'open_course' | 'school_authorized';
export type RightsStatus = 'pending' | 'verified' | 'rejected';
export type SourceAction = 'verify' | 'upload' | 'assign_session';

export interface SourceRecord {
  id: string;
  session_id: string | null;
  source_type: SourceType;
  title: string;
  attribution: string;
  source_url: string | null;
  authorization_note: string;
  requested_uses: SourceUse[];
  external_processing_requested: boolean;
  rights_status: RightsStatus;
  allowed_uses: SourceUse[];
  external_processing_allowed: boolean;
  rights_version: number;
  verified_by: string | null;
  verified_at: string | null;
  allowed_actions: SourceAction[];
}

export interface SourcePage {
  items: SourceRecord[];
  next_cursor: string | null;
}

export interface RunPage {
  items: Run[];
  next_cursor: string | null;
}

export interface CreateSourceInput {
  session_id: string;
  source_type: SourceType;
  title: string;
  attribution: string;
  source_url: string | null;
  authorization_note: string;
  requested_uses: SourceUse[];
  external_processing_requested: boolean;
}

export interface VerifySourceInput {
  rights_status: Exclude<RightsStatus, 'pending'>;
  allowed_uses: SourceUse[];
  external_processing_allowed: boolean;
  reason: string;
}

const SOURCE_TYPES: readonly SourceType[] = ['self_recorded', 'open_course', 'school_authorized'];
const SOURCE_USES: readonly SourceUse[] = ['playback', 'analysis', 'export'];
const RIGHTS_STATUSES: readonly RightsStatus[] = ['pending', 'verified', 'rejected'];
const SOURCE_ACTIONS: readonly SourceAction[] = ['verify', 'upload', 'assign_session'];

function enumList<T extends string>(value: unknown, path: string, allowed: readonly T[]): T[] {
  return asArray(value, path).map((item, index) => asEnum({ value: item }, 'value', `${path}[${index}]`, allowed));
}

function parseSource(value: unknown, path: string): SourceRecord {
  const item = asRecord(value, path);
  return {
    id: asString(item, 'id', path),
    session_id: asNullableString(item, 'session_id', path),
    source_type: asEnum(item, 'source_type', path, SOURCE_TYPES),
    title: asString(item, 'title', path),
    attribution: asString(item, 'attribution', path),
    source_url: asNullableString(item, 'source_url', path),
    authorization_note: asString(item, 'authorization_note', path),
    requested_uses: enumList(item['requested_uses'], `${path}.requested_uses`, SOURCE_USES),
    external_processing_requested: asBoolean(item, 'external_processing_requested', path),
    rights_status: asEnum(item, 'rights_status', path, RIGHTS_STATUSES),
    allowed_uses: enumList(item['allowed_uses'], `${path}.allowed_uses`, SOURCE_USES),
    external_processing_allowed: asBoolean(item, 'external_processing_allowed', path),
    rights_version: asInteger(item, 'rights_version', path),
    verified_by: asNullableString(item, 'verified_by', path),
    verified_at: asNullableString(item, 'verified_at', path),
    allowed_actions: enumList(item['allowed_actions'], `${path}.allowed_actions`, SOURCE_ACTIONS),
  };
}

export async function listSessionSources(sessionId: string, cursor?: string): Promise<SourcePage> {
  const { data } = await requestJson<unknown>(`/sessions/${encodeURIComponent(sessionId)}/sources`, {
    query: { cursor, limit: 20 },
  });
  const page = asRecord(data, 'SourcePage');
  return {
    items: asArray(page['items'], 'SourcePage.items').map((item, index) => parseSource(item, `SourcePage.items[${index}]`)),
    next_cursor: asNullableString(page, 'next_cursor', 'SourcePage'),
  };
}

/** 协议 A04：按课堂分页恢复全部分析历史，不能只依赖 Session.latest_run_id。 */
export async function listSessionRuns(sessionId: string, cursor?: string): Promise<RunPage> {
  const { data } = await requestJson<unknown>(`/sessions/${encodeURIComponent(sessionId)}/analysis-runs`, {
    query: { cursor, limit: 20 },
  });
  const page = asRecord(data, 'RunPage');
  return {
    items: asArray(page['items'], 'RunPage.items').map((item, index) => parseRun(item, `RunPage.items[${index}]`)),
    next_cursor: asNullableString(page, 'next_cursor', 'RunPage'),
  };
}

export async function createSource(input: CreateSourceInput): Promise<SourceRecord> {
  const { data } = await requestJson<unknown>('/sources', { method: 'POST', body: input, csrf: true });
  return parseSource(data, 'Source');
}

export async function getSource(id: string): Promise<SourceRecord> {
  const { data } = await requestJson<unknown>(`/sources/${encodeURIComponent(id)}`);
  return parseSource(data, 'Source');
}

export async function verifySource(id: string, input: VerifySourceInput): Promise<SourceRecord> {
  const { data } = await requestJson<unknown>(`/sources/${encodeURIComponent(id)}/verify`, {
    method: 'POST', body: input, csrf: true,
  });
  return parseSource(data, 'Source');
}
