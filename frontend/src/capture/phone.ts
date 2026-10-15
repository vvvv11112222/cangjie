import type { CapturePublisher, LiveCapture } from '../captureTypes';
import { gatherICE, mediaURL, outboundCodecs, resourceURL, selectCodecs, verifyAnswer } from './whip';

export interface PhoneState {
  phase: 'idle' | 'opening' | 'connecting' | 'publishing' | 'stopped';
  sessionID: string;
  capture: LiveCapture | null;
  stream: MediaStream | null;
  settings: MediaTrackSettings[];
  codecs: string[];
  notice: string;
  error: string;
  stopPending: boolean;
  canStart: boolean;
  canStop: boolean;
  level: number;
  audioActivity: boolean;
  audioMuted: boolean;
  meterAvailable: boolean;
  wake: string;
}

export interface PhoneEnvironment {
  fetch: typeof fetch;
  getMedia: (constraints: MediaStreamConstraints) => Promise<MediaStream>;
  makePeer: () => RTCPeerConnection;
  capabilities: (kind: 'audio' | 'video') => RTCRtpCapabilities | null;
  meter: (stream: MediaStream, update: (level: number) => void) => Promise<() => void>;
  wake: () => Promise<{ release: () => Promise<void>; addEventListener: (type: string, cb: () => void) => void }>;
  origin: string;
}

class CaptureError extends Error {
  constructor(readonly code: string) { super(code); }
}

export function captureHint(error: unknown): string {
  const code = error instanceof CaptureError ? error.code : error instanceof Error ? error.name : '';
  const message = error instanceof Error ? error.message : '';
  const hints: Record<string, string> = {
    NotAllowedError: '请在浏览器站点设置中允许摄像头和麦克风；若被系统限制，请到安卓权限设置中开启后重试。',
    NotFoundError: '未找到摄像头或麦克风，请检查设备是否具备两种输入。',
    NotReadableError: '设备可能被其他应用占用，请关闭相机、通话等应用后重试。',
    OverconstrainedError: '设备无法满足采集参数，请换用支持摄像头与麦克风的浏览器。',
    SecurityError: '请使用后端提供的可信 HTTPS 地址，并在安卓安装匹配的 CA 公共证书。',
    UNAUTHENTICATED: '配对已过期、已消费或凭据失效。请回电脑停止当前采集，再新建采集和配对链接。',
    FORBIDDEN: '配对或采集权限不可用。请回电脑检查课堂和来源授权，停止后重新创建。',
    RESOURCE_EXPIRED: '配对或采集已过期，请回电脑重新创建。',
    CAPTURE_ACTIVE: '当前课堂已有活动采集，请先在电脑停止它，再创建新的采集。',
    RATE_LIMITED: '配对请求过于频繁，请等待一分钟并回电脑获取新链接。',
    SERVICE_UNAVAILABLE: '采集服务尚未就绪，请联系后端检查媒体服务和配置。',
    ICE_TIMEOUT: '网络候选收集超时。请检查同一 Wi-Fi、VPN 与媒体端口；本次不自动重连。',
    CONNECTION_TIMEOUT: '媒体连接超时，请检查 Wi-Fi 和 UDP 8189；回电脑停止后重新建采集。',
    CODEC_UNSUPPORTED: '浏览器未能提供兼容的 H.264/Opus，请更新安卓 Chrome 或与后端确认兼容方案。',
    TRACKS_MISSING: '必须同时获得一个摄像头和一个麦克风，当前不能完成音视频采集。',
    WHIP_LOCATION_MISSING: '媒体代理未返回资源地址，请后端检查 Location 响应头。',
    WHIP_LOCATION_INVALID: '媒体资源地址与发布通道不匹配，请后端检查代理的 Location 重写。',
    MEDIA_URL_INVALID: '发布地址不是可信 HTTPS 通道，请后端检查媒体地址配置。',
    WHIP_RESPONSE_INVALID: '媒体服务未返回有效的 SDP，请后端检查 WHIP 代理。',
  };
  return hints[code] ?? hints[message] ?? '网络请求未完成。请检查 Wi-Fi 和证书是否可信；不要反复使用已交换的配对链接，必要时回电脑停止并重建采集。';
}

export class PhoneCapture {
  readonly state: PhoneState;
  private pairing: string | null;
  private publisher: CapturePublisher | null = null;
  private peer: RTCPeerConnection | null = null;
  private resource: string | null = null;
  private operation = new AbortController();
  private generation = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private pollRunning = false;
  private connectionTimer: ReturnType<typeof setTimeout> | undefined;
  private stopBusy = false;
  private finished = false;
  private disposed = false;
  private meterStop: (() => void) | undefined;
  private wakeLock: Awaited<ReturnType<PhoneEnvironment['wake']>> | undefined;

  constructor(pairing: string | null, private env: PhoneEnvironment, private change: (state: PhoneState) => void) {
    this.pairing = pairing;
    this.state = {
      phase: 'idle', sessionID: '', capture: null, stream: null, settings: [], codecs: [],
      notice: pairing ? '点击开始，同时允许摄像头和麦克风。请横放手机。' : '配对信息已丢失或链接不完整。请回电脑停止原采集，再创建新的采集和配对链接；不要刷新后重复交换旧链接。',
      error: '', stopPending: false, canStart: !!pairing, canStop: false,
      level: 0, audioActivity: false, audioMuted: false, meterAvailable: false, wake: '请保持前台、供电和屏幕开启。',
    };
  }

  private emit(): void { if (!this.disposed) this.change({ ...this.state }); }

  private async request<T>(path: string, body?: object, token?: string): Promise<T> {
    const response = await this.env.fetch('/api/v1/' + path, {
      method: body === undefined ? 'GET' : 'POST', credentials: 'omit', cache: 'no-store', redirect: 'error',
      headers: { ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...(token ? { Authorization: 'Bearer ' + token } : {}) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(12000),
    });
    const envelope = await response.json() as { data: T; error?: { code: string } };
    if (!response.ok) throw new CaptureError(envelope.error?.code ?? 'REQUEST_FAILED');
    return envelope.data;
  }

  async start(): Promise<void> {
    if (!this.state.canStart || this.disposed) return;
    this.state.canStart = false;
    this.state.canStop = true;
    this.state.phase = 'opening';
    this.state.error = '';
    this.state.notice = '正在申请摄像头和麦克风；此时服务器尚未开始录像。';
    const generation = ++this.generation;
    this.emit();
    try {
      const stream = await this.env.getMedia({
        video: { facingMode: { ideal: 'environment' }, width: { ideal: 1280 }, height: { ideal: 720 }, frameRate: { ideal: 15 } },
        audio: { echoCancellation: true, noiseSuppression: true },
      });
      if (generation !== this.generation) { stream.getTracks().forEach(track => track.stop()); return; }
      this.state.stream = stream;
      if (stream.getVideoTracks().length !== 1 || stream.getAudioTracks().length !== 1 || stream.getTracks().some(t => t.readyState !== 'live')) throw new Error('TRACKS_MISSING');
      this.state.settings = stream.getTracks().map(track => track.getSettings());
      for (const track of stream.getTracks()) {
        track.onended = () => { void this.interrupted('摄像头或麦克风权限被收回。本次采集结束，请回电脑重新创建。'); };
        track.onmute = () => {
          this.state.audioMuted = stream.getAudioTracks().some(t => t.muted);
          this.state.notice = '输入轨道被系统暂停，正在结束本次采集。'; this.emit();
          void this.interrupted('摄像头或麦克风输入被系统暂停。本次不自动恢复，请保持前台并重新创建采集。');
        };
      }
      this.emit();
      // Meter and wake lock are optional. Neither can block capture startup.
      void this.env.meter(stream, level => {
        if (generation !== this.generation) return;
        this.state.level = level;
        this.state.audioActivity ||= level > 0.02;
        this.emit();
      }).then(stop => {
        if (generation !== this.generation) stop(); else { this.meterStop = stop; this.state.meterAvailable = true; this.emit(); }
      }).catch(() => { this.state.meterAvailable = false; this.emit(); });
      void this.env.wake().then(lock => {
        if (generation !== this.generation) { void lock.release().catch(() => {}); return; }
        this.wakeLock = lock;
        this.state.wake = '已申请屏幕常亮，仍须保持前台与供电。';
        lock.addEventListener('release', () => { this.state.wake = '屏幕常亮已释放，请手工保持亮屏与前台。'; this.emit(); });
        this.emit();
      }).catch(() => { this.state.wake = '无法自动保持亮屏，请手工延长熄屏时间并保持前台。'; this.emit(); });
      const peer = this.env.makePeer();
      this.peer = peer;
      stream.getTracks().forEach(track => peer.addTrack(track, stream));
      selectCodecs(peer, this.env.capabilities);
      // Once an exchange was sent its outcome can be unknown; never reuse it.
      const pairing = this.pairing;
      this.pairing = null;
      this.publisher = await this.request<CapturePublisher>('capture-pairings/exchange', { pairing_token: pairing });
      this.state.sessionID = this.publisher.session_id;
      if (generation !== this.generation) { await this.stop(); return; }
      this.state.phase = 'connecting';
      this.state.notice = '本地预览已打开，正在连接媒体服务；尚未确认服务器录像。';
      this.emit();
      this.schedulePoll();
      const endpoint = mediaURL(this.publisher.whip_url, this.env.origin);
      peer.onconnectionstatechange = () => {
        if (generation !== this.generation) return;
        if (peer.connectionState === 'connected') {
          this.state.phase = 'publishing'; this.emit();
          void this.readCodecs(peer);
        } else if (['failed', 'disconnected', 'closed'].includes(peer.connectionState)) {
          void this.interrupted('媒体连接已断开，本次采集结束。请回电脑确认收尾后创建新的采集。');
        }
      };
      await peer.setLocalDescription(await peer.createOffer());
      await gatherICE(peer, this.operation.signal);
      if (generation !== this.generation) return;
      const response = await this.env.fetch(endpoint.href, {
        method: 'POST', credentials: 'omit', redirect: 'error', cache: 'no-store',
        headers: { 'Content-Type': 'application/sdp', Authorization: 'Bearer ' + this.publisher.publish_token },
        body: peer.localDescription?.sdp, signal: AbortSignal.any([this.operation.signal, AbortSignal.timeout(15000)]),
      });
      if (response.status !== 201) throw new CaptureError('WHIP_HTTP_' + response.status);
      this.resource = resourceURL(response.headers.get('Location'), endpoint);
      if (!response.headers.get('Content-Type')?.toLowerCase().includes('application/sdp')) throw new Error('WHIP_RESPONSE_INVALID');
      const sdp = await response.text();
      verifyAnswer(sdp);
      if (generation !== this.generation) { await this.deleteResource(); return; }
      await peer.setRemoteDescription({ type: 'answer', sdp });
      // A separate deadline handles an SDP exchange that never establishes ICE.
      this.connectionTimer = setTimeout(() => {
        if (generation === this.generation && peer.connectionState !== 'connected') {
          void this.interrupted(captureHint(new Error('CONNECTION_TIMEOUT')));
        }
      }, 15000);
    } catch (error) {
      if (generation !== this.generation) return;
      this.state.error = captureHint(error);
      ++this.generation;
      this.releaseLocal();
      this.state.phase = 'stopped';
      this.state.canStart = !!this.pairing;
      if (this.publisher) await this.stop();
      else {
        this.state.canStop = false;
        this.state.notice = this.pairing ? '未发布音视频，可调整权限后重试。' : '配对交换失败或结果不明。请回电脑停止原采集，再创建新的采集和配对链接。';
      }
      this.emit();
    }
  }

  private async readCodecs(peer: RTCPeerConnection): Promise<void> {
    try {
      const codecs = await outboundCodecs(peer);
      if (this.peer !== peer) return;
      this.state.codecs = codecs; this.emit();
    } catch { if (this.peer === peer) await this.interrupted(captureHint(new Error('CODEC_UNSUPPORTED'))); }
  }

  private schedulePoll(): void {
    if (this.disposed || this.finished || !this.publisher) return;
    clearTimeout(this.timer);
    if (this.pollRunning) return;
    this.timer = setTimeout(() => { void this.poll(); }, 1500);
  }

  async poll(): Promise<void> {
    if (!this.publisher || this.pollRunning || this.finished || this.disposed) return;
    this.pollRunning = true;
    try {
      const capture = await this.request<LiveCapture>('live-captures/' + this.publisher.live_capture_id, undefined, this.publisher.control_token);
      if (this.disposed) return;
      this.state.capture = capture;
      if (capture.state === 'recording' && (capture.audio_track === 'absent' || capture.video_track === 'absent')) {
        await this.interrupted('服务器检测到缺少音轨或画面，本次不算完整采集，正在停止。');
      }
      if (['finalizing', 'ready', 'failed'].includes(capture.state)) {
        ++this.generation; this.releaseLocal();
        void this.deleteResource();
        this.state.phase = 'stopped';
        this.state.canStart = false;
      }
      if (capture.state === 'ready' || capture.state === 'failed') {
        this.finished = true;
        this.state.stopPending = false;
        this.state.canStop = false;
        this.state.notice = capture.state === 'ready' ? '服务器已完成录像登记，请回电脑验证回放中的画面与声音。' : '服务器采集失败，请回电脑查看错误并重新创建采集。';
      }
      if (this.peer?.connectionState === 'connected') await this.readCodecs(this.peer);
    } catch (error) {
      this.state.error = '状态暂时无法确认：' + captureHint(error);
    } finally {
      this.pollRunning = false;
      this.emit();
      this.schedulePoll();
    }
  }

  async stop(): Promise<void> {
    if (this.stopBusy || this.finished) return;
    this.stopBusy = true;
    ++this.generation;
    this.releaseLocal();
    this.state.phase = 'stopped';
    this.state.canStart = !!this.pairing;
    this.state.canStop = !!this.publisher;
    if (this.state.stopPending) this.state.error = '';
    this.state.notice = '本地采集已停止，正在请求服务器收尾；录像尚未确认保存。';
    this.emit();
    // Backend intent and WHIP cleanup proceed independently; either may be unreachable.
    const cleanup = this.deleteResource();
    try {
      if (this.publisher) {
        this.state.stopPending = true;
        const capture = await this.request<LiveCapture>('live-captures/' + this.publisher.live_capture_id + '/stop', {}, this.publisher.control_token);
        this.state.capture = capture;
        this.state.stopPending = false;
        this.state.notice = '服务器已接受停止，请等待收尾完成；只有 ready 表示正式媒体已登记。';
        if (capture.state === 'ready' || capture.state === 'failed') {
          this.finished = true; this.state.canStop = false;
          this.state.notice = capture.state === 'ready' ? '录像已登记，请回电脑验证回放。' : '采集失败，请回电脑查看错误。';
        }
      } else this.state.notice = this.pairing ? '本地已停止，尚未交换配对，可再次开始。' : '本地已停止；若配对请求在途中，请回电脑确认原采集状态。';
    } catch (error) {
      this.state.stopPending = true;
      this.state.error = '本地已停，服务器停止确认待完成。可点击“重试停止”，或回电脑停止。' + captureHint(error);
    } finally {
      await cleanup;
      this.stopBusy = false;
      this.emit();
      this.schedulePoll();
    }
  }

  async interrupted(reason: string): Promise<void> {
    if (!['opening', 'connecting', 'publishing'].includes(this.state.phase)) return;
    this.state.error = reason;
    await this.stop();
  }

  private releaseLocal(): void {
    clearTimeout(this.connectionTimer);
    this.operation.abort();
    this.operation = new AbortController();
    const peer = this.peer; this.peer = null;
    if (peer) { peer.onconnectionstatechange = null; peer.close(); }
    this.state.stream?.getTracks().forEach(track => { track.onended = null; track.onmute = null; track.stop(); });
    this.state.stream = null;
    this.meterStop?.(); this.meterStop = undefined;
    this.state.level = 0; this.state.meterAvailable = false;
    void this.wakeLock?.release().catch(() => {}); this.wakeLock = undefined;
  }

  private async deleteResource(): Promise<void> {
    const resource = this.resource;
    if (!resource || !this.publisher) return;
    this.resource = null;
    try {
      const response = await this.env.fetch(resource, {
        method: 'DELETE', credentials: 'omit', redirect: 'error',
        headers: { Authorization: 'Bearer ' + this.publisher.publish_token }, signal: AbortSignal.timeout(5000),
      });
      if (!response.ok && response.status !== 404 && response.status !== 410) throw new Error('DELETE_FAILED');
    } catch {
      this.resource = resource;
      this.state.error = '本地已停，媒体连接清理待确认；后台会按断流对账，请回电脑检查收尾状态。';
    }
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    clearTimeout(this.timer);
    ++this.generation;
    this.releaseLocal();
    // Best effort only. Page departure never means recording has been saved.
    void this.deleteResource();
    if (this.publisher && !this.finished) void this.request('live-captures/' + this.publisher.live_capture_id + '/stop', {}, this.publisher.control_token).catch(() => {});
  }
}
