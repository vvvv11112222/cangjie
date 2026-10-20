/** Formal vision-v1 reads use the existing same-origin, authenticated Go client. */
import { requestJson } from './http';
import { asArray, asInteger, asNullableInteger, asNullableString, asRecord, asString, guardFail } from './guards';

export const BEHAVIORS = ['hand_raise', 'head_down', 'leave_seat', 'possible_phone'] as const;
export type Behavior = typeof BEHAVIORS[number];
export type Verdict = boolean | null;
export interface VisualObservation {
  timestamp_ms: number;
  bbox: [number, number, number, number];
  behaviors: Record<Behavior, Verdict>;
  frame_asset_id: string | null;
}
export interface VisualPerson { track_id: string; sample_count: number }
export interface VisualPage<T> { items: T[]; next_cursor: string | null }
export interface VisualCount { timestamp_ms: number; visible_count: number | null; hand_raise_count: number | null; head_down_count: number | null }
export interface VisualSummary {
  run_id: string; media_asset_id: string; model: { name: string; revision: string };
  sampling: { interval_ms: number; max_frames: number };
  coverage: { start_ms: number; end_ms: number }[];
  limitations: string[];
  visual: { width: number; height: number; counts: VisualCount[] };
}

export function parseObservation(value: unknown): VisualObservation {
  const row = asRecord(value, 'VisualObservation');
  const flags = asRecord(row.behaviors, 'VisualObservation.behaviors');
  const behaviors = {} as Record<Behavior, Verdict>;
  for (const kind of BEHAVIORS) {
    const flag = flags[kind];
    if (flag !== null && typeof flag !== 'boolean') guardFail(kind, '应为 true、false 或 null');
    behaviors[kind] = flag as Verdict;
  }
  const bbox = asArray(row.bbox, 'bbox');
  if (bbox.length !== 4 || bbox.some(n => typeof n !== 'number' || !Number.isFinite(n) || n < 0)) guardFail('bbox', '应为四个非负坐标');
  return { timestamp_ms: asInteger(row, 'timestamp_ms', 'VisualObservation'), bbox: bbox as VisualObservation['bbox'],
    behaviors, frame_asset_id: asNullableString(row, 'frame_asset_id', 'VisualObservation') };
}

export function parseVisualSummary(value: unknown): VisualSummary {
  const row = asRecord(value, 'VisualSummary');
  const visual = asRecord(row.visual, 'visual');
  const model = asRecord(row.model, 'model');
  const sampling = asRecord(row.sampling, 'sampling');
  const counts = asArray(visual.counts, 'counts').map(value => {
    const c = asRecord(value, 'count');
    return { timestamp_ms: asInteger(c, 'timestamp_ms', 'count'), visible_count: asNullableInteger(c, 'visible_count', 'count'),
      hand_raise_count: asNullableInteger(c, 'hand_raise_count', 'count'), head_down_count: asNullableInteger(c, 'head_down_count', 'count') };
  });
  return { run_id: asString(row, 'run_id', 'summary'), media_asset_id: asString(row, 'media_asset_id', 'summary'),
    model: { name: asString(model, 'name', 'model'), revision: asString(model, 'revision', 'model') },
    sampling: { interval_ms: asInteger(sampling, 'interval_ms', 'sampling'), max_frames: asInteger(sampling, 'max_frames', 'sampling') },
    coverage: asArray(row.coverage, 'coverage').map(value => { const c = asRecord(value, 'coverage'); return { start_ms: asInteger(c, 'start_ms', 'coverage'), end_ms: asInteger(c, 'end_ms', 'coverage') }; }),
    limitations: asArray(row.limitations, 'limitations').map(value => { if (typeof value !== 'string') guardFail('limitation', '应为文字'); return value as string; }),
    visual: { width: asInteger(visual, 'width', 'visual'), height: asInteger(visual, 'height', 'visual'), counts } };
}
function path(run: string): string { return `/analysis-runs/${encodeURIComponent(run)}/visual`; }
export async function getVisualSummary(run: string): Promise<VisualSummary> {
  return parseVisualSummary((await requestJson<unknown>(path(run))).data);
}
export async function getVisualPeople(run: string, cursor?: string): Promise<VisualPage<VisualPerson>> {
  const { data } = await requestJson<unknown>(`${path(run)}/persons`, { query: { cursor, limit: 100 } });
  const page = asRecord(data, 'VisualPersonsPage');
  return { items: asArray(page.items, 'items').map(value => { const p = asRecord(value, 'person'); return { track_id: asString(p, 'track_id', 'person'), sample_count: asInteger(p, 'sample_count', 'person') }; }),
    next_cursor: asNullableString(page, 'next_cursor', 'page') };
}
export async function getVisualObservations(run: string, track: string, cursor?: string): Promise<VisualPage<VisualObservation>> {
  const { data } = await requestJson<unknown>(`${path(run)}/persons/${encodeURIComponent(track)}`, { query: { cursor, limit: 100 } });
  const page = asRecord(data, 'VisualObservationsPage');
  return { items: asArray(page.items, 'items').map(parseObservation), next_cursor: asNullableString(page, 'next_cursor', 'page') };
}
export function observationAt(rows: VisualObservation[], at: number, interval: number): VisualObservation | null {
  let best: VisualObservation | null = null;
  for (const row of rows) if (Math.abs(row.timestamp_ms - at) <= interval / 2 && (best === null || Math.abs(row.timestamp_ms - at) < Math.abs(best.timestamp_ms - at))) best = row;
  return best;
}
export const verdictText = (value: Verdict): string => value === true ? '发现' : value === false ? '未见' : '看不清';
export function observationColor(row: VisualObservation): string {
  return row.behaviors.hand_raise === true ? 'var(--danger)' : row.behaviors.head_down === true ? 'var(--info)' :
    row.behaviors.hand_raise === false && row.behaviors.head_down === false ? 'var(--ok)' : 'var(--muted)';
}
