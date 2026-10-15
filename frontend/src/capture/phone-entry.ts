import '../styles.css';
import { PhoneCapture } from './phone';
import type { PhoneEnvironment, PhoneState } from './phone';

declare global { interface Window { __capturePairing?: string | null } }

const root = document.getElementById('phone-root');
if (!root) throw new Error('Missing phone capture root');
root.innerHTML = `
  <header class="page-head"><div class="eyebrow">观课 · 手机采集</div><h1>记录这堂课</h1>
    <p>横放手机，对准讲台。请保持前台、供电和屏幕开启。</p></header>
  <section class="card" aria-labelledby="capture-heading">
    <h2 id="capture-heading">摄像头与麦克风</h2>
    <p>课堂标识：<span id="session-id" class="phone-id">配对后显示</span></p>
    <video id="preview" autoplay playsinline muted aria-label="手机本地静音预览"></video>
    <p id="local-state" role="status">本地尚未采集</p>
    <label for="mic-level">麦克风输入活动</label>
    <meter id="mic-level" min="0" max="1" value="0"></meter>
    <p id="mic-status">尚未获得音轨</p>
    <p class="phone-caption">电平只表示输入活动，不能证明收音清晰，也不是语音识别。请由电脑开启声音试听。</p>
    <div class="phone-actions"><button id="start" class="btn btn-primary" type="button">开始采集</button>
      <button id="stop" class="btn btn-danger" type="button" disabled>停止采集</button></div>
    <p id="notice" class="notice-strip" role="status"></p>
    <p id="error" class="notice-strip" role="alert" hidden></p>
    <p id="wake-status" class="phone-caption"></p>
  </section>
  <section class="card" aria-labelledby="server-heading"><h2 id="server-heading">服务器录像</h2>
    <p id="server-state" role="status">尚未确认。打开本地预览不代表服务器正在录像。</p>
    <p id="server-tracks"></p><p id="result" class="phone-id"></p>
    <p class="phone-caption">停止后等待服务器收尾，再回电脑回放。断流结束本次采集，页面不会自动重连。</p>
  </section>
  <details class="card phone-details"><summary>采集参数与联调记录</summary>
    <pre id="diagnostics">尚无实际参数</pre>
    <p>开头、结尾、收音和可读文字仍须检查正式录像。刷新会丢失控制信息，请回电脑恢复或停止原采集，再重新创建。</p>
  </details>`;

function element<T extends HTMLElement>(id: string): T { return document.getElementById(id) as T; }
const preview = element<HTMLVideoElement>('preview');
let previousStream: MediaStream | null = null;
const phaseNames = { idle: '本地尚未采集', opening: '正在请求设备权限', connecting: '本地预览已打开，媒体正在连接', publishing: '音视频连接已建立', stopped: '本地硬件已释放' };
const serverNames = { waiting: '等待发布或录像确认', recording: '服务器正在录像', finalizing: '服务器正在收尾，尚未确认保存', ready: '正式录像已登记，请回电脑验证回放', failed: '服务器采集失败' };
const trackNames = { present: '已检测到', absent: '缺失', unknown: '尚未确认' };

function render(state: PhoneState): void {
  element('session-id').textContent = state.sessionID || '配对后显示';
  element('local-state').textContent = phaseNames[state.phase];
  element<HTMLButtonElement>('start').disabled = !state.canStart;
  element<HTMLButtonElement>('stop').disabled = !state.canStop;
  element('stop').textContent = state.stopPending ? '重试停止' : '停止采集';
  element('notice').textContent = state.notice;
  element('error').textContent = state.error;
  element('error').hidden = !state.error;
  element('wake-status').textContent = state.wake;
  if (previousStream !== state.stream) {
    previousStream = state.stream;
    preview.muted = true;
    preview.srcObject = state.stream;
    if (state.stream) void preview.play().catch(() => {
      element('notice').textContent = '本地预览未自动播放，请点击预览画面；服务器状态请看下方。';
    });
  }
  element<HTMLMeterElement>('mic-level').value = state.level;
  element('mic-status').textContent = !state.stream ? '没有活动音轨' : state.audioMuted ? '音轨被系统暂停' :
    !state.meterAvailable ? '已获得音轨，电平暂不可用；请在电脑试听' :
      state.audioActivity ? `已检测到输入活动，当前电平 ${Math.round(state.level * 100)}%（清晰度待试听）` : '已获得音轨，尚未检测到明显输入；请讲话测试';
  const capture = state.capture;
  if (capture) {
    element('server-state').textContent = '上次服务端确认：' + serverNames[capture.state];
    element('server-tracks').textContent = `画面：${trackNames[capture.video_track]}；音轨：${trackNames[capture.audio_track]}`;
    element('result').textContent = capture.media_asset_id ? '正式媒体 ID：' + capture.media_asset_id : '正式媒体 ID：尚未登记';
  }
  const video = state.settings.find(settings => settings.width !== undefined);
  const downgrade = video && (video.width !== 1280 || video.height !== 720 || Math.abs((video.frameRate ?? 0) - 15) > 1 || video.facingMode !== 'environment');
  // Explicit whitelist: no DTO containing publisher/control credentials is rendered.
  element('diagnostics').textContent = JSON.stringify({
    capture_id: capture?.id ?? null, session_id: state.sessionID || null,
    target: '后置摄像头，1280×720，15fps，横屏',
    settings: state.settings.map(({ width, height, frameRate, facingMode, sampleRate, channelCount, echoCancellation, noiseSuppression }) =>
      ({ width, height, frameRate, facingMode, sampleRate, channelCount, echoCancellation, noiseSuppression })),
    adjustment: downgrade ? '设备实际参数与目标不同，请按以上实际值验收；若未使用后置摄像头，请调整手机或浏览器设置。' : '以实际参数为准',
    outbound_codecs: state.codecs.length ? state.codecs : '等待实际发送统计，尚未确认',
    server_state: capture?.state ?? '未确认', recording_state: capture?.recording_state ?? '未确认',
    error_code: capture?.error_code ?? null, end_reason: capture?.end_reason ?? null,
    media_asset_id: capture?.media_asset_id ?? null, media_prepare_run_id: capture?.media_prepare_run_id ?? null,
    browser: navigator.userAgent,
  }, null, 2);
}

const environment: PhoneEnvironment = {
  fetch: window.fetch.bind(window), origin: location.origin,
  getMedia: constraints => {
    if (!window.isSecureContext || !navigator.mediaDevices?.getUserMedia) {
      return Promise.reject(new DOMException('Trusted HTTPS is required', 'SecurityError'));
    }
    return navigator.mediaDevices.getUserMedia(constraints);
  },
  makePeer: () => new RTCPeerConnection(),
  capabilities: kind => RTCRtpReceiver.getCapabilities(kind),
  meter: async (stream, update) => {
    const context = new AudioContext();
    let source: MediaStreamAudioSourceNode | undefined;
    let analyser: AnalyserNode | undefined;
    let frame = 0;
    let lastUpdate = 0;
    const stop = () => { cancelAnimationFrame(frame); source?.disconnect(); analyser?.disconnect(); void context.close().catch(() => {}); };
    try {
      source = context.createMediaStreamSource(stream);
      analyser = context.createAnalyser(); analyser.fftSize = 1024;
      source.connect(analyser); // Deliberately never connected to speakers.
      await context.resume();
      if (context.state !== 'running') throw new Error('METER_UNAVAILABLE');
      const samples = new Float32Array(analyser.fftSize);
      const tick = (time = 0) => {
        if (time - lastUpdate >= 100) {
          analyser?.getFloatTimeDomainData(samples);
          const rms = Math.sqrt(samples.reduce((sum, value) => sum + value * value, 0) / samples.length);
          update(Math.min(1, rms)); lastUpdate = time;
        }
        frame = requestAnimationFrame(tick);
      };
      tick(); return stop;
    } catch (error) { stop(); throw error; }
  },
  wake: async () => navigator.wakeLock.request('screen'),
};
const pairing = window.__capturePairing ?? null;
delete window.__capturePairing;
const capture = new PhoneCapture(pairing, environment, render);
render(capture.state);
element('start').onclick = () => { void capture.start(); };
element('stop').onclick = () => { void capture.stop(); };
preview.onclick = () => { if (preview.srcObject) void preview.play().catch(() => {}); };
document.addEventListener('visibilitychange', () => {
  if (document.hidden) void capture.interrupted('页面已进入后台，本次采集结束。回到前台后请回电脑确认收尾，再新建采集。');
});
window.addEventListener('offline', () => { void capture.interrupted('Wi-Fi 或网络已断开，本地采集已停止；服务器状态待确认，请回电脑检查收尾。'); });
window.addEventListener('pagehide', () => capture.dispose());
window.addEventListener('pageshow', event => {
  if (event.persisted) {
    element('notice').textContent = '页面已从后台缓存恢复，采集不会重连。请回电脑确认旧采集已停止，再获取新链接。';
    element<HTMLButtonElement>('start').disabled = true;
    element<HTMLButtonElement>('stop').disabled = true;
  }
});
