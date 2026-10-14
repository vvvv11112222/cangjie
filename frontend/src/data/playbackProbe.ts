/**
 * 播放地址预检。
 *
 * 为什么需要单独探测：`<video src=...>` 里的 Range 请求是浏览器自己发的，
 * 前端拿不到 HTTP 状态码，只能看到"加载失败"这种笼统结果。
 * 协议里 /media/{id}/content 的失败是有明确错误码的（403 无播放许可、410 已过期、
 * 416 RANGE_NOT_SATISFIABLE……，见 docs/开发协议.md 第 1.2 节），
 * 所以这里主动发一次 1 字节的 Range 请求，把服务端的真实错误码取回来给页面显示。
 *
 * 只做一次头部检查，不读整段媒体字节；200/206 都算可播放（不支持 Range 的静态服务会返回 200）。
 */

import { ApiError } from './errors';
import { responseError } from './http';

export type PlaybackProbeResult =
  /** 地址可用：206 说明服务端支持 Range，200 说明可播放但不能拖动。 */
  | { kind: 'ok'; status: number }
  | { kind: 'cancelled' }
  /** 服务端明确拒绝（含错误码）。 */
  | { kind: 'failed'; error: ApiError };

export const PLAYBACK_PROBE_TIMEOUT_MS = (() => {
  const value = Number(import.meta.env.VITE_PLAYBACK_PROBE_TIMEOUT_MS ?? 10000);
  return Number.isSafeInteger(value) && value > 0 ? value : 10000;
})();

export async function probePlaybackAddress(
  url: string,
  options: { signal?: AbortSignal; timeoutMs?: number } = {},
): Promise<PlaybackProbeResult> {
  if (options.signal?.aborted) return { kind: 'cancelled' };
  const controller = new AbortController();
  const cancelled = new Error('预检已取消');
  let abortListener = () => {};
  let timer: ReturnType<typeof setTimeout> | undefined;
  const stop = new Promise<never>((_, reject) => {
    abortListener = () => { controller.abort(); reject(cancelled); };
    options.signal?.addEventListener('abort', abortListener, { once: true });
    timer = setTimeout(() => {
      controller.abort();
      reject(new Error('播放地址检查超时，请重新检查。'));
    }, options.timeoutMs ?? PLAYBACK_PROBE_TIMEOUT_MS);
  });
  try {
    return await Promise.race([stop, (async (): Promise<PlaybackProbeResult> => {
      const response = await fetch(url, {
        headers: { Range: 'bytes=0-0' },
        credentials: 'same-origin',
        signal: controller.signal,
      });
      if (response.status === 200 || response.status === 206) {
        // 不等待 cancel 的完成；服务器忽略 Range 时也不能继续下载整份录像。
        try { void response.body?.cancel().catch(() => undefined); } catch { /* abort 同样会释放连接 */ }
        return { kind: 'ok', status: response.status };
      }
      const text = await response.text();
      let payload: unknown;
      try { payload = text.trim() === '' ? null : JSON.parse(text); } catch { payload = undefined; }
      return { kind: 'failed', error: responseError(payload, response.status) };
    })()]);
  } catch (error) {
    if (options.signal?.aborted || error === cancelled) return { kind: 'cancelled' };
    return {
      kind: 'failed',
      error: new ApiError({
        code: 'NETWORK_ERROR',
        status: 0,
        message: error instanceof Error ? error.message : '播放地址请求未到达服务端',
      }),
    };
  } finally {
    clearTimeout(timer);
    options.signal?.removeEventListener('abort', abortListener);
    controller.abort();
  }
}
