import { useEffect, useRef, useState } from 'react';
import type { ReactElement, RefObject } from 'react';
import { detectLiveFrame, liveColor } from '../capture/liveVision';
import { ApiError } from '../data/errors';

export function LiveVisionPreview({ captureId, video, enabled }: {
  captureId: string; video: RefObject<HTMLVideoElement | null>; enabled: boolean;
}): ReactElement {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [ready, setReady] = useState(false);
  const [status, setStatus] = useState('等待检测画面');
  useEffect(() => {
    setReady(false);
    if (!enabled) return;
    let current = true, sequence = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let stale: ReturnType<typeof setTimeout> | undefined;
    let request: AbortController | undefined;
    const tick = async () => {
      const source = video.current;
      if (!current || !source) return;
      if (source.readyState < 2 || !source.videoWidth || source.paused || document.hidden) {
        setReady(false); setStatus('等待实时画面');
        timer = setTimeout(() => { void tick(); }, 500); return;
      }
      const frame = document.createElement('canvas');
      const scale = Math.min(1, 1280 / source.videoWidth, 1080 / source.videoHeight);
      frame.width = Math.round(source.videoWidth * scale); frame.height = Math.round(source.videoHeight * scale);
      frame.getContext('2d')?.drawImage(source, 0, 0, frame.width, frame.height);
      const sampled = performance.now(); request = new AbortController();
      const timeout = setTimeout(() => request?.abort(), 42000);
      let retry = true;
      try {
        const result = await detectLiveFrame(captureId, ++sequence, frame, request.signal);
        if (!current || !canvas.current) return;
        const target = canvas.current; target.width = frame.width; target.height = frame.height;
        const context = target.getContext('2d');
        if (!context) return;
        context.drawImage(frame, 0, 0);
        context.lineWidth = Math.max(2, frame.width / 550); context.font = `${Math.max(13, frame.width / 75)}px sans-serif`;
        for (const person of result.people) {
          const [x1, y1, x2, y2] = person.bbox;
          context.strokeStyle = context.fillStyle = liveColor(person);
          context.strokeRect(x1 * frame.width, y1 * frame.height, (x2 - x1) * frame.width, (y2 - y1) * frame.height);
          const label = person.track_id + (person.hand_raise ? ' 举手' : '') + (person.head_down ? ' 低头' : '');
          context.fillText(label, x1 * frame.width, Math.max(15, y1 * frame.height - 4));
        }
        setReady(true);
        setStatus(`检测画面延迟 ${((performance.now() - sampled) / 1000).toFixed(1)} 秒 · 可见 ${result.people.length} 人 · 举手 ${result.people.filter(p => p.hand_raise === true).length} · 低头 ${result.people.filter(p => p.head_down === true).length}`);
        clearTimeout(stale);
        stale = setTimeout(() => { if (current) { setReady(false); setStatus('检测暂未更新，显示实时原画'); } }, 5000);
      } catch (error) {
        if (!current) return;
        setReady(false);
        if (error instanceof ApiError && [401, 403, 404, 410].includes(error.status)) {
          retry = false; setStatus(error.status === 403 ? '当前无权检测；请检查账号权限及来源是否已核验并允许分析' : '登录或采集状态已变化，请重新连接观看');
        } else setStatus(error instanceof ApiError && error.status === 503 ? '检测 Worker 忙碌或尚未就绪；录像继续保存' : '本次画面未完成检测，正在重试');
      } finally {
        clearTimeout(timeout);
        if (current && retry) timer = setTimeout(() => { void tick(); }, Math.max(100, 500 - (performance.now() - sampled)));
      }
    };
    setStatus('正在加载直播检测'); void tick();
    return () => { current = false; request?.abort(); clearTimeout(timer); clearTimeout(stale); };
  }, [captureId, enabled, video]);
  return <>
    <canvas ref={canvas} className="live-detection-canvas" hidden={!enabled || !ready} aria-label="举手与低头直播检测画面" />
    {enabled && <div className="live-detection-status" role="status">{status}</div>}
  </>;
}
