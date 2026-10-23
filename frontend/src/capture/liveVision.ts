import { currentCsrfToken, responseError } from '../data/http';

export interface LivePerson { track_id: string; bbox: [number, number, number, number]; hand_raise: boolean | null; head_down: boolean | null }
export interface LiveResult { sequence: number; width: number; height: number; people: LivePerson[]; error_code: string | null }
export function liveColor(person: LivePerson): string {
  return person.hand_raise === true ? '#ef4444' : person.head_down === true ? '#3b82f6' :
    person.hand_raise === false && person.head_down === false ? '#22c55e' : '#a3a3a3';
}
export function parseLiveResult(value: unknown): LiveResult {
  const v = value as LiveResult;
  if (!v || !Number.isSafeInteger(v.sequence) || !Number.isInteger(v.width) || !Number.isInteger(v.height) ||
    v.width < 16 || v.width > 1920 || v.height < 16 || v.height > 1080 || !Array.isArray(v.people) || v.people.length > 80 ||
    !(v.error_code === null || v.error_code === 'PROCESSING_FAILED' || v.error_code === 'PROCESSING_TIMEOUT')) throw new Error('直播检测响应无效');
  const ids = new Set<string>();
  for (const person of v.people) {
    if (!person || typeof person.track_id !== 'string' || !person.track_id || ids.has(person.track_id) ||
      !Array.isArray(person.bbox) || person.bbox.length !== 4 || person.bbox.some(n => !Number.isFinite(n) || n < 0 || n > 1) ||
      person.bbox[0] >= person.bbox[2] || person.bbox[1] >= person.bbox[3] ||
      ![true, false, null].includes(person.hand_raise) || ![true, false, null].includes(person.head_down)) throw new Error('直播人物数据无效');
    ids.add(person.track_id);
  }
  return v;
}
export async function detectLiveFrame(captureId: string, sequence: number, canvas: HTMLCanvasElement, signal: AbortSignal): Promise<LiveResult> {
  const csrf = currentCsrfToken();
  if (!csrf) throw new Error('登录状态已变化，请重新登录');
  const response = await fetch(`/api/v1/live-captures/${encodeURIComponent(captureId)}/vision-frames`, {
    method: 'POST', credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal,
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    body: JSON.stringify({ sequence, image_base64: canvas.toDataURL('image/jpeg', .86).split(',')[1] }),
  });
  const payload = await response.json() as { data?: unknown };
  if (!response.ok) throw responseError(payload, response.status);
  const result = parseLiveResult(payload.data);
  if (result.sequence !== sequence || result.width !== canvas.width || result.height !== canvas.height) throw new Error('检测结果与画面不匹配，已丢弃');
  if (result.error_code !== null) throw new Error('本次直播检测失败，正在等待下一张画面');
  return result;
}
