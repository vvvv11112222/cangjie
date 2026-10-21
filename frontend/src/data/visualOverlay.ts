/** Load immutable observations through the existing Go pages, once per open run. */
import { getVisualObservations, getVisualPeople } from './visualApi';
import type { VisualObservation, VisualPerson } from './visualApi';

export interface PersonBox { track_id: string; observation: VisualObservation }
export interface VisualOverlayData {
  people: VisualPerson[];
  byTrack: Map<string, VisualObservation[]>;
  frames: Map<number, PersonBox[]>;
}
interface LoadOptions {
  cancelled: () => boolean;
  progress: (completed: number, total: number) => void;
}

export async function loadVisualOverlay(run: string, options: LoadOptions): Promise<VisualOverlayData> {
  function check(): void { if (options.cancelled()) throw new Error('画框读取已停止。'); }
  const people: VisualPerson[] = [];
  const ids = new Set<string>();
  const personCursors = new Set<string>();
  let cursor: string | undefined;
  do {
    check();
    const page = await getVisualPeople(run, cursor);
    check();
    if (!page.items.length && page.next_cursor) throw new Error('人物分页返回空页，请重试。');
    for (const person of page.items) {
      if (ids.has(person.track_id)) throw new Error('人物分页出现重复编号，请重试。');
      ids.add(person.track_id); people.push(person);
    }
    if (people.length > 10000) throw new Error('人物数量超过本次结果的读取上限。');
    cursor = page.next_cursor ?? undefined;
    if (cursor && personCursors.has(cursor)) throw new Error('人物分页没有前进，请重试。');
    if (cursor) personCursors.add(cursor);
  } while (cursor);
  options.progress(0, people.length);
  const byTrack = new Map<string, VisualObservation[]>();
  let next = 0; let completed = 0; let count = 0;
  let failed = false; let failure: unknown;
  async function reader(): Promise<void> {
    while (next < people.length && !failed) {
      check();
      const person = people[next++]!;
      const rows: VisualObservation[] = [];
      const cursors = new Set<string>();
      const times = new Set<number>();
      let after: string | undefined;
      do {
        check(); if (failed) return;
        const page = await getVisualObservations(run, person.track_id, after);
        check(); if (failed) return;
        if (!page.items.length && page.next_cursor) throw new Error('人物记录分页返回空页，请重试。');
        count += page.items.length;
        if (count > 100000) throw new Error('人物记录超过本次结果的读取上限。');
        for (const row of page.items) {
          if (times.has(row.timestamp_ms)) throw new Error('人物记录时间重复，请重试。');
          times.add(row.timestamp_ms); rows.push(row);
        }
        after = page.next_cursor ?? undefined;
        if (after && cursors.has(after)) throw new Error('人物记录分页没有前进，请重试。');
        if (after) cursors.add(after);
      } while (after);
      if (rows.length !== person.sample_count) throw new Error('人物记录尚未读全，请重试。');
      byTrack.set(person.track_id, rows.sort((a, b) => a.timestamp_ms - b.timestamp_ms));
      options.progress(++completed, people.length);
    }
  }
  // Keep classroom reads bounded rather than sending one request per person at once.
  await Promise.all(Array.from({ length: Math.min(3, people.length) }, async () => {
    try { await reader(); } catch (error) { if (!failed) { failed = true; failure = error; } }
  }));
  if (failed) throw failure;
  check();
  const frames = new Map<number, PersonBox[]>();
  for (const person of people) for (const observation of byTrack.get(person.track_id)!) {
    const boxes = frames.get(observation.timestamp_ms) ?? [];
    boxes.push({ track_id: person.track_id, observation });
    frames.set(observation.timestamp_ms, boxes);
  }
  return { people, byTrack, frames };
}

/** Match one common sampled frame. Never mix people from adjacent sample times. */
export function sampledTimeAt(times: readonly number[], at: number, interval: number): number | null {
  let low = 0; let high = times.length;
  while (low < high) {
    const mid = (low + high) >>> 1;
    if (times[mid]! < at) low = mid + 1; else high = mid;
  }
  const before = times[low - 1]; const after = times[low];
  const nearest = before === undefined ? after : after === undefined || at - before <= after - at ? before : after;
  return nearest !== undefined && Math.abs(nearest - at) <= interval / 2 ? nearest : null;
}
