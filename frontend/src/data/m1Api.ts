/** M1 管理页面的数据访问：教务、来源均只经 Go HTTP API 读取和写入。 */
import { requestJson } from './http';
import { asArray, asNullableInteger, asNullableString, asRecord, asString } from './guards';

export interface Page<T> { items: T[]; next_cursor: string | null }
export interface Term { id: string; code: string; name: string; start_date: string; end_date: string }
export interface Course { id: string; org_unit_id: string; code: string; name: string; description: string }
export interface Classroom { id: string; code: string; name: string; capacity: number | null }
export interface Offering { id: string; org_unit_id: string; term_id: string; course_id: string; teacher_id: string; class_group_id: string; code: string; status: string }
export interface Schedule { id: string; offering_id: string; classroom_id: string; starts_at: string; ends_at: string; status: string; teacher_id: string; class_group_id: string }
export interface SourceRecord { id: string; session_id: string | null; source_type: string; title: string; attribution: string; source_url: string | null; authorization_note: string; requested_uses: string[]; external_processing_requested: boolean; rights_status: string; allowed_uses: string[]; external_processing_allowed: boolean; rights_version: number; verified_by: string | null; verified_at: string | null; allowed_actions: string[] }

function page<T>(value: unknown, path: string, parse: (value: unknown, path: string) => T): Page<T> {
  const record = asRecord(value, path);
  return { items: asArray(record.items, `${path}.items`).map((item, index) => parse(item, `${path}.items[${index}]`)), next_cursor: asNullableString(record, 'next_cursor', path) };
}
function stringList(value: unknown, path: string): string[] { return asArray(value, path).map((item, index) => asString({ value: item }, 'value', `${path}[${index}]`)); }
function term(value: unknown, path: string): Term { const x = asRecord(value, path); return { id: asString(x,'id',path), code: asString(x,'code',path), name: asString(x,'name',path), start_date: asString(x,'start_date',path), end_date: asString(x,'end_date',path) }; }
function course(value: unknown, path: string): Course { const x = asRecord(value,path); return { id: asString(x,'id',path), org_unit_id: asString(x,'org_unit_id',path), code: asString(x,'code',path), name: asString(x,'name',path), description: asString(x,'description',path) }; }
function classroom(value: unknown, path: string): Classroom { const x = asRecord(value,path); return { id: asString(x,'id',path), code: asString(x,'code',path), name: asString(x,'name',path), capacity: asNullableInteger(x,'capacity',path) }; }
function offering(value: unknown, path: string): Offering { const x = asRecord(value,path); return { id: asString(x,'id',path), org_unit_id: asString(x,'org_unit_id',path), term_id: asString(x,'term_id',path), course_id: asString(x,'course_id',path), teacher_id: asString(x,'teacher_id',path), class_group_id: asString(x,'class_group_id',path), code: asString(x,'code',path), status: asString(x,'status',path) }; }
function schedule(value: unknown, path: string): Schedule { const x = asRecord(value,path); return { id: asString(x,'id',path), offering_id: asString(x,'offering_id',path), classroom_id: asString(x,'classroom_id',path), starts_at: asString(x,'starts_at',path), ends_at: asString(x,'ends_at',path), status: asString(x,'status',path), teacher_id: asString(x,'teacher_id',path), class_group_id: asString(x,'class_group_id',path) }; }
function source(value: unknown, path: string): SourceRecord { const x = asRecord(value,path); return { id: asString(x,'id',path), session_id: asNullableString(x,'session_id',path), source_type: asString(x,'source_type',path), title: asString(x,'title',path), attribution: asString(x,'attribution',path), source_url: asNullableString(x,'source_url',path), authorization_note: asString(x,'authorization_note',path), requested_uses: stringList(x.requested_uses,`${path}.requested_uses`), external_processing_requested: x.external_processing_requested === true, rights_status: asString(x,'rights_status',path), allowed_uses: stringList(x.allowed_uses,`${path}.allowed_uses`), external_processing_allowed: x.external_processing_allowed === true, rights_version: Number(x.rights_version), verified_by: asNullableString(x,'verified_by',path), verified_at: asNullableString(x,'verified_at',path), allowed_actions: stringList(x.allowed_actions,`${path}.allowed_actions`) }; }

async function list<T>(path: string, parser: (value: unknown, path: string) => T): Promise<Page<T>> { const { data } = await requestJson<unknown>(path, { query: { limit: 100 } }); return page(data, `${path}Page`, parser); }
export const listTerms = () => list('/terms', term);
export const listCourses = () => list('/courses', course);
export const listClassrooms = () => list('/classrooms', classroom);
export const listOfferings = () => list('/offerings', offering);
export const listSchedules = () => list('/schedules', schedule);
export const listSources = (sessionId: string) => list(`/sessions/${encodeURIComponent(sessionId)}/sources`, source);
export async function createSchedule(input: Pick<Schedule, 'offering_id' | 'classroom_id' | 'starts_at' | 'ends_at' | 'status'>): Promise<Schedule> { const { data } = await requestJson<unknown>('/schedules', { method: 'POST', body: input, csrf: true }); return schedule(data, 'Schedule'); }
export async function createSource(input: Record<string, unknown>): Promise<SourceRecord> { const { data } = await requestJson<unknown>('/sources', { method: 'POST', body: input, csrf: true }); return source(data, 'Source'); }
export async function verifySource(id: string, input: Record<string, unknown>): Promise<SourceRecord> { const { data } = await requestJson<unknown>(`/sources/${encodeURIComponent(id)}/verify`, { method: 'POST', body: input, csrf: true }); return source(data, 'Source'); }
