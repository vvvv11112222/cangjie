import { useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import type { CapturePairing, LiveCapture } from '../captureTypes';
import type { Session } from '../types';
import type { SourceRecord } from '../data/m2Api';
import { listSessionSources } from '../data/m2Api';
import {
  createLiveCapture,
  createPairing,
  createViewer,
  getActiveLiveCapture,
  getLiveCapture,
  stopLiveCapture,
} from '../data/liveCaptureApi';
import { newIdempotencyKey } from '../data/actions';
import { ApiError, describeError, isTerminalReadError } from '../data/errors';
import { collectPages } from '../data/pagination';
import { connectWHEP } from '../capture/whep';
import type { WHEPSession } from '../capture/whep';
import { BadgeTag, Card, Field, TechDetails } from '../components/ui';
import { formatDateTime, formatTimestamp } from '../format';

const terminal = (capture: LiveCapture): boolean => capture.state === 'ready' || capture.state === 'failed';
async function verifiedSources(sessionId: string): Promise<SourceRecord[]> {
  const items = await collectPages((cursor) => listSessionSources(sessionId, cursor));
  return [...new Map(items.filter((item) => item.rights_status === 'verified' && item.allowed_uses.includes('playback'))
    .map((item) => [item.id, item])).values()];
}
const stateBadge = (state: LiveCapture['state']) => ({
  label: { waiting: '等待手机', recording: '正在录像', finalizing: '正在收尾', ready: '录像就绪', failed: '采集失败' }[state],
  tone: ({ waiting: 'gray', recording: 'teal', finalizing: 'amber', ready: 'green', failed: 'red' } as const)[state],
  icon: state === 'failed' ? 'alert' as const : state === 'recording' || state === 'finalizing' ? 'clock' as const : state === 'ready' ? 'check' as const : 'dot' as const,
});

export function LiveCapturePanel({ session, enabled, onUseMedia }: {
  session: Session;
  enabled: boolean;
  onUseMedia(mediaId: string): void;
}): ReactElement {
  const [capture, setCapture] = useState<LiveCapture | null>(null);
  const [sources, setSources] = useState<SourceRecord[]>([]);
  const [sourceId, setSourceId] = useState('');
  const [pairing, setPairing] = useState<CapturePairing | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [watching, setWatching] = useState(false);
  const [muted, setMuted] = useState(true);
  const video = useRef<HTMLVideoElement>(null);
  const whep = useRef<WHEPSession | null>(null);
  const watchAbort = useRef<AbortController | null>(null);
  const watchExpiry = useRef<number | null>(null);
  const createKey = useRef(newIdempotencyKey());
  const watchGeneration = useRef(0);
  const [pollStopped, setPollStopped] = useState(false);

  function stopWatching(): void {
    watchGeneration.current += 1;
    if (watchExpiry.current !== null) window.clearTimeout(watchExpiry.current);
    watchExpiry.current = null;
    if (whep.current !== null) whep.current.connection.onconnectionstatechange = null;
    watchAbort.current?.abort(); watchAbort.current = null;
    void whep.current?.close(); whep.current = null;
    if (video.current !== null) video.current.srcObject = null;
  }

  useEffect(() => {
    if (!enabled) return;
    let current = true;
    setError(null);
    void Promise.all([
      verifiedSources(session.id),
      getActiveLiveCapture(session.id).catch((reason: unknown) => {
        if (reason instanceof ApiError && reason.status === 404) return null;
        throw reason;
      }),
    ]).then(([verified, active]) => {
      if (!current) return;
      setSources(verified);
      setSourceId((value) => value || verified[0]?.id || '');
      setCapture(active);
    }).catch((reason: unknown) => { if (current) setError(describeError(reason)); });
    return () => { current = false; };
  }, [enabled, session.id]);

  useEffect(() => {
    if (!enabled || pollStopped || capture === null || terminal(capture)) return;
    let current = true;
    let pending = false;
    const timer = window.setInterval(() => {
      if (pending) return;
      pending = true;
      void getLiveCapture(capture.id).then((next) => {
        if (!current) return;
        setCapture(next);
        setError((previous) => previous?.startsWith('状态刷新失败：') ? null : previous);
        if (terminal(next)) { current = false; window.clearInterval(timer); stopWatching(); setWatching(false); }
      }).catch((reason: unknown) => {
        if (!current) return;
        const terminalError = isTerminalReadError(reason);
        if (terminalError) {
          current = false; window.clearInterval(timer); setPollStopped(true);
          stopWatching(); setWatching(false); setCapture(null); setPairing(null); setSources([]); setSourceId('');
        }
        setError(`状态刷新失败：${describeError(reason)}${terminalError ? ' 已停止自动刷新，请重新进入页面。' : ''}`);
      }).finally(() => { pending = false; });
    }, 1500);
    return () => { current = false; window.clearInterval(timer); };
  }, [capture?.id, capture?.state, enabled, pollStopped]);

  useEffect(() => () => stopWatching(), [session.id]);

  async function act(operation: () => Promise<void>): Promise<void> {
    setBusy(true); setError(null); setNotice(null);
    try { await operation(); } catch (reason) { setError(describeError(reason)); } finally { setBusy(false); }
  }

  const start = () => act(async () => {
    const next = await createLiveCapture(session.id, { source_record_id: sourceId, config_profile: 'phone-demo-v1' }, createKey.current);
    setCapture(next); setPairing(null); createKey.current = newIdempotencyKey();
    setNotice('采集已创建。下一步生成手机配对链接。');
  });
  const pair = () => act(async () => {
    if (capture === null) return;
    const next = await createPairing(capture.id); setPairing(next);
    setNotice('配对链接只给本次手机使用，过期后请重新生成。');
  });
  const stop = () => act(async () => {
    if (capture === null) return;
    stopWatching(); setWatching(false);
    setCapture(await stopLiveCapture(capture.id));
    setNotice('已请求停止，服务端正在封口和准备录像。');
  });
  const watch = () => act(async () => {
    if (capture === null || pollStopped) return;
    stopWatching(); setWatching(false);
    const generation = watchGeneration.current;
    // 在任何 await 之前登记取消句柄，离开页面后迟到的授权响应不能启动连接。
    const controller = new AbortController(); watchAbort.current = controller;
    const current = () => generation === watchGeneration.current && !controller.signal.aborted;
    const viewer = await createViewer(capture.id);
    if (!current()) return;
    const connected = await connectWHEP(viewer.whep_url, viewer.view_token, controller.signal);
    if (!current()) { await connected.close(); return; }
    whep.current = connected;
    connected.connection.onconnectionstatechange = () => {
      if (whep.current !== connected) return;
      if (['failed', 'disconnected', 'closed'].includes(connected.connection.connectionState)) {
        setWatching(false);
        setNotice('实时观看已断开，但服务端可能仍在录像；可点击“重新连接观看”重新鉴权。');
      }
    };
    if (video.current !== null) {
      video.current.srcObject = connected.stream; video.current.muted = true;
      await video.current.play().catch(() => undefined);
    }
    if (!current()) return;
    const renewAfter = Date.parse(viewer.expires_at) - Date.now() - 5000;
    if (renewAfter > 0) {
      watchExpiry.current = window.setTimeout(() => {
        if (!current() || whep.current !== connected) return;
        connected.connection.onconnectionstatechange = null;
        controller.abort(); whep.current = null; setWatching(false);
        setNotice('观看凭据即将到期，正在按当前权限重新连接。');
        void watch();
      }, renewAfter);
    }
    setMuted(true); setWatching(true); setNotice('实时观看已连接；录像状态仍以服务端采集状态为准。');
  });

  return (
    <div className="live-capture-layout">
      <Card title="实时课堂" subtitle="手机发布音视频，电脑实时观看；停止后录像进入现有回放与分析链路。" actions={capture === null ? undefined : <BadgeTag badge={stateBadge(capture.state)} />}>
        {capture === null ? (
          <div className="capture-create">
            <label className="account-field">
              <span>已核验来源</span>
              <select value={sourceId} onChange={(event) => setSourceId(event.target.value)}>
                {sources.length === 0 ? <option value="">没有可用来源</option> : sources.map((source) => <option key={source.id} value={source.id}>{source.title}</option>)}
              </select>
            </label>
            <button type="button" className="btn btn-primary" disabled={busy || pollStopped || sourceId === '' || !session.allowed_actions.includes('upload')} onClick={() => void start()}>
              创建实时采集
            </button>
            <p className="field-hint">创建权限由服务端再次校验；页面不会把普通观看权限当作采集权限。</p>
          </div>
        ) : (
          <>
            <div className="field-grid">
              <Field label="连接状态">{capture.connection_state}</Field>
              <Field label="录像状态">{capture.recording_state}</Field>
              <Field label="音轨 / 视频轨">{capture.audio_track} / {capture.video_track}</Field>
              <Field label="服务端时长">{capture.duration_ms === null ? '尚未提供' : formatTimestamp(capture.duration_ms)}</Field>
              <Field label="开始时间">{capture.started_at === null ? '尚未开始' : formatDateTime(capture.started_at)}</Field>
              <Field label="结束时间">{capture.ended_at === null ? '尚未结束' : formatDateTime(capture.ended_at)}</Field>
            </div>
            <div className="action-row">
              {capture.allowed_actions.includes('pair') ? <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void pair()}>生成手机链接</button> : null}
              {capture.allowed_actions.includes('watch') ? <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void watch()}>{watching ? '重新连接观看' : '开始实时观看'}</button> : null}
              {capture.allowed_actions.includes('stop') ? <button type="button" className="btn btn-danger" disabled={busy} onClick={() => void stop()}>停止录像</button> : null}
              {capture.state === 'ready' && capture.media_asset_id !== null ? <button type="button" className="btn btn-secondary" onClick={() => onUseMedia(capture.media_asset_id as string)}>用此录像继续处理</button> : null}
            </div>
          </>
        )}
        {pairing === null ? null : <div className="capture-pairing"><strong>手机配对链接</strong><a href={pairing.phone_url} target="_blank" rel="noreferrer">{pairing.phone_url}</a><span>有效至 {formatDateTime(pairing.expires_at)}</span><button type="button" className="btn btn-secondary" onClick={() => void navigator.clipboard.writeText(pairing.phone_url).then(() => setNotice('配对链接已复制。')).catch(() => setError('浏览器拒绝复制，请手动选择链接。'))}>复制链接</button></div>}
        {error === null ? null : <p className="notice-strip load-note-error">{error}</p>}
        {notice === null ? null : <p className="notice-strip">{notice}</p>}
        {capture?.error_code == null ? null : <p className="notice-strip load-note-error">采集失败：{capture.error_code}{capture.end_reason === null ? '' : ` · ${capture.end_reason}`}</p>}
        {capture === null ? null : <TechDetails><ul className="tech-list"><li className="mono">live_capture.id = {capture.id}</li><li className="mono">media_asset_id = {capture.media_asset_id ?? 'null'}</li><li className="mono">media_prepare_run_id = {capture.media_prepare_run_id ?? 'null'}</li><li className="mono">allowed_actions = [{capture.allowed_actions.join(', ')}]</li></ul></TechDetails>}
      </Card>
      <Card title="实时画面" subtitle="观看断开不代表服务端录像已经停止。">
        <video ref={video} className="live-video" autoPlay playsInline muted={muted} aria-label="课堂实时画面" />
        <div className="action-row">
          <button type="button" className="btn btn-secondary" disabled={!watching} onClick={() => { if (video.current !== null) { video.current.muted = !muted; setMuted(!muted); } }}>{muted ? '开启声音' : '静音'}</button>
          <button type="button" className="btn btn-secondary" disabled={!watching} onClick={() => void video.current?.requestFullscreen()}>全屏</button>
        </div>
        {!watching ? <p className="empty">尚未建立观看连接。手机可以继续录像，观看状态与录像状态相互独立。</p> : null}
      </Card>
      <Card title="课后分析" subtitle="实时采集只负责录像；转写、关键帧和报告仍走课后分析。">
        <p className="muted">录像 ready 后选择“用此录像继续处理”，再到“批次与操作”提交完整分析。当前正式契约没有 OCR 与行为字段，不展示模拟算法结果。</p>
      </Card>
    </div>
  );
}
