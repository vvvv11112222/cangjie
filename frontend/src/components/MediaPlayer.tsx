/**
 * 回放播放器：一个 <video> + 倍速选择 + 当前播放位置。
 *
 * 为什么自己做而不是引组件库：UI 约定第 7 节要求不新增依赖，项目里也没有 UI 库；
 * 播放本身交给浏览器原生控件（它自带进度条、音量、全屏），我们只补两件业务上需要的事——
 * 倍速，以及"跳到某个原录像时间点"。
 *
 * 时间口径：src 是服务端给的播放代理，协议规定代理不裁剪、不变速（origin_offset_ms=0），
 * 所以 <video> 的秒数 × 1000 就是原录像毫秒，跳转时不做偏移换算。
 */

import { useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import { PLAYBACK_SPEEDS, formatSpeed, msToSeconds, secondsToMs } from '../data/playback';
import type { PlaybackSpeed } from '../data/playback';
import { formatTimestamp } from '../format';
import { IconAlert } from './icons';

/** 快退/快进一次的步长（毫秒）。 */
const STEP_MS = 5000;

/** 外部请求的跳转：nonce 让"再点同一个时间点"也能重新触发。 */
export interface SeekRequest {
  at_ms: number;
  nonce: number;
}

function MediaPlayerInstance({
  src,
  durationMs,
  seekRequest,
  onTimeMs,
  label,
  captions = [],
  showCaptions = true,
  onRetry,
  onFailed,
  reloadToken = 0,
  suspended = false,
}: {
  src: string;
  /** 服务端给的时长；为 null 时进度条由浏览器按实际文件决定。 */
  durationMs: number | null;
  seekRequest: SeekRequest | null;
  onTimeMs?(ms: number): void;
  label: string;
  /**
   * 字幕片段（原录像毫秒）。用浏览器原生字幕轨渲染，而不是 DOM 覆盖层：
   * 原生字幕由浏览器排在播放控件上方，不会挡住进度条和播放键，画中画字幕取决于浏览器支持。
   */
  captions?: readonly { start_ms: number; end_ms: number; text_content: string }[];
  /** 字幕开关。关掉时同时隐藏原生字幕轨和"没有片段字幕"的提示行。 */
  showCaptions?: boolean;
  onRetry?(): void;
  onFailed?(failed: boolean): void;
  /** 重试取得当前授权后显式启动新的媒体读取。 */
  reloadToken?: number;
  suspended?: boolean;
}): ReactElement {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const trackRef = useRef<TextTrack | null>(null);
  const [speed, setSpeed] = useState<PlaybackSpeed>(1);
  const [positionMs, setPositionMs] = useState(0);
  const [failed, setFailed] = useState(false);
  const [localReloadToken, setLocalReloadToken] = useState(0);
  // fragment 只改变浏览器媒体资源身份，不发送到 Go；路径、查询和授权地址保持原样。
  const attempt = reloadToken + localReloadToken;
  const playbackSrc = attempt === 0 ? src : `${src}${src.includes('#') ? '&' : '#'}playback_retry=${attempt}`;
  const [loadedDurationMs, setLoadedDurationMs] = useState<number | null>(durationMs);
  const [pipAvailable] = useState(
    () => typeof document !== 'undefined' && document.pictureInPictureEnabled === true,
  );
  useEffect(() => {
    const video = videoRef.current;
    if ((reloadToken > 0 || localReloadToken > 0) && video !== null) {
      setFailed(false);
      onFailed?.(false);
      setPositionMs(0);
      setLoadedDurationMs(durationMs);
      video.load();
    }
  }, [reloadToken, localReloadToken, src]);
  useEffect(() => {
    if (suspended && videoRef.current !== null && !videoRef.current.paused) videoRef.current.pause();
  }, [suspended]);
  useEffect(() => {
    const video = videoRef.current;
    return () => {
      if (video !== null && !video.paused) video.pause();
      // 释放旧媒体，避免离开页面后继续读取或保留旧播放资源。
      if (video !== null && video.currentSrc !== '') {
        video.removeAttribute('src');
        video.load();
      }
      if (video !== null && document.pictureInPictureElement === video) {
        void document.exitPictureInPicture?.().catch(() => undefined);
      }
    };
  }, []);

  /**
   * 把片段写进原生字幕轨。
   * 两个浏览器约束要照顾：cue 必须落在媒体时长内（超出会抛 IndexSizeError），
   * 所以按已知时长裁剪并丢弃完全落在时长之外的片段；jsdom 没有字幕轨实现，直接跳过。
   */
  useEffect(() => {
    const video = videoRef.current;
    if (video === null || typeof video.addTextTrack !== 'function' || typeof VTTCue !== 'function') {
      return;
    }
    let track = trackRef.current;
    if (track === null) {
      track = video.addTextTrack('subtitles', '转写', 'zh');
      trackRef.current = track;
    }
    const cues = track.cues;
    if (cues !== null) {
      for (let index = cues.length - 1; index >= 0; index -= 1) {
        const cue = cues[index];
        if (cue !== undefined) {
          track.removeCue(cue);
        }
      }
    }
    const limitMs = loadedDurationMs ?? durationMs;
    for (const segment of captions) {
      const endMs = limitMs === null ? segment.end_ms : Math.min(segment.end_ms, limitMs);
      if (limitMs !== null && segment.start_ms >= limitMs) {
        continue;
      }
      try {
        track.addCue(new VTTCue(msToSeconds(segment.start_ms), msToSeconds(endMs), segment.text_content));
      } catch {
        // 个别浏览器对越界 cue 更严格：跳过这一条，不影响播放与其它字幕。
      }
    }
    track.mode = showCaptions ? 'showing' : 'hidden';
  }, [captions, showCaptions, loadedDurationMs, durationMs]);

  // 倍速在切换时立即生效，不用重新开始播放。
  useEffect(() => {
    const video = videoRef.current;
    if (video !== null) {
      video.playbackRate = speed;
    }
  }, [speed]);

  useEffect(() => {
    const video = videoRef.current;
    if (video === null || seekRequest === null) {
      return;
    }
    video.currentTime = msToSeconds(seekRequest.at_ms);
    setPositionMs(seekRequest.at_ms);
    onTimeMs?.(seekRequest.at_ms);
    // 只有拿到元数据（readyState≥1）才尝试从新位置继续播放：这时浏览器已经知道时长，
    // 跳转本身就是用户手势，允许播放。元数据没到就只定位，不在这里猜能不能播
    // （jsdom 也没有实现 play()，这条判断顺便让单元测试不产生噪音）。
    if (video.readyState >= 1) {
      try {
        void video.play()?.catch(() => undefined);
      } catch {
        // 浏览器拒绝自动播放时忽略：位置已经设置好了。
      }
    }
  }, [seekRequest]);

  /** 相对当前位置前后跳一步，并夹在 [0, 时长] 内；时长未知时只保证不为负。 */
  function jumpBy(stepMs: number): void {
    const video = videoRef.current;
    if (video === null) {
      return;
    }
    const limitMs =
      durationMs ?? (Number.isFinite(video.duration) ? secondsToMs(video.duration) : null);
    const raw = secondsToMs(video.currentTime) + stepMs;
    const target = Math.max(0, limitMs === null ? raw : Math.min(raw, limitMs));
    video.currentTime = msToSeconds(target);
    setPositionMs(target);
    onTimeMs?.(target);
  }

  function togglePictureInPicture(): void {
    const video = videoRef.current;
    if (video === null || typeof video.requestPictureInPicture !== 'function') {
      return;
    }
    try {
      if (document.pictureInPictureElement === video) {
        void document.exitPictureInPicture?.();
      } else {
        void video.requestPictureInPicture()?.catch(() => undefined);
      }
    } catch {
      // 浏览器拒绝（例如用户未交互）时忽略：播放位置不受影响。
    }
  }

  /**
   * 当前位置没有命中任何片段时的提示。
   * 它只作为视频下面的一行说明，不再压在画面上：覆盖层会挡住原生进度条与播放键，
   * 这也是"当前片段没有字幕挡住了进度条和暂停开关"的直接修复。
   */
  const hasSegmentHere = captions.some(
    (segment) => positionMs >= segment.start_ms && positionMs < segment.end_ms,
  );
  const captionNote =
    showCaptions && captions.length > 0 && !hasSegmentHere ? '当前位置没有片段字幕' : null;

  return (
    <div className="player">
      <video
        ref={videoRef}
        className="player-video"
        src={playbackSrc}
        controls
        preload="metadata"
        playsInline
        aria-label={label}
        onLoadedMetadata={(event) => {
          setFailed(false);
          onFailed?.(false);
          const seconds = event.currentTarget.duration;
          setLoadedDurationMs(Number.isFinite(seconds) ? secondsToMs(seconds) : null);
          if (seekRequest !== null) event.currentTarget.currentTime = msToSeconds(seekRequest.at_ms);
        }}
        onTimeUpdate={(event) => {
          const ms = secondsToMs(event.currentTarget.currentTime);
          setPositionMs(ms);
          onTimeMs?.(ms);
        }}
        onKeyDown={(event) => {
          // 只在播放器自己聚焦时接管左右键；其它控件（下拉框等）的方向键不受影响。
          if (event.key === 'ArrowLeft') {
            event.preventDefault();
            jumpBy(-STEP_MS);
          } else if (event.key === 'ArrowRight') {
            event.preventDefault();
            jumpBy(STEP_MS);
          }
        }}
        onCanPlay={() => { setFailed(false); onFailed?.(false); }}
        onError={() => { setFailed(true); onFailed?.(true); }}
      />
      <div className="player-bar">
        <button type="button" className="btn btn-secondary" disabled={failed} onClick={() => jumpBy(-STEP_MS)}>
          后退 5 秒
        </button>
        <button type="button" className="btn btn-secondary" disabled={failed} onClick={() => jumpBy(STEP_MS)}>
          前进 5 秒
        </button>
        <label className="account-field player-speed">
          <span>倍速</span>
          <select
            value={String(speed)}
            onChange={(event) => setSpeed(Number(event.target.value) as PlaybackSpeed)}
          >
            {PLAYBACK_SPEEDS.map((item) => (
              <option key={item} value={String(item)}>
                {formatSpeed(item)}
              </option>
            ))}
          </select>
        </label>
        {pipAvailable ? (
          <button type="button" className="btn btn-secondary" onClick={togglePictureInPicture}>
            画中画
          </button>
        ) : null}
        <p className="player-position mono">
          当前 {formatTimestamp(positionMs)}
          {durationMs === null ? '' : ` / ${formatTimestamp(durationMs)}`}
        </p>
      </div>
      {/*
        字幕状态单独一行，永远占位（避免出现/消失时控件跳动）：
        命中片段时说明字幕位置，没命中时说清楚"这一段没有字幕"。
      */}
      <p className="load-note player-caption-note">
        {captionNote ?? '字幕由浏览器原生字幕轨显示在播放控件上方（画中画字幕取决于浏览器支持）。'}
      </p>
      <p className="load-note">
        播放器聚焦时按 ← / → 可前后 5 秒；倍速只影响播放速度，不改变时间轴与原录像时间的对应关系。
      </p>
      {failed ? (
        <div>
        <p className="notice-strip">
          <IconAlert size={14} aria-hidden="true" />
          播放器无法加载这段媒体字节。接口模式下这通常表示地址已过期或没有内容读取权限；
          请重试播放，并核对媒体有效期与用途。
        </p>
        <button type="button" className="btn btn-secondary" disabled={suspended} onClick={() => {
          if (onRetry) onRetry();
          else setLocalReloadToken(current => current + 1);
        }}>重试播放</button>
        </div>
      ) : null}
    </div>
  );
}

/** src 切换重建播放器，避免旧错误、时间与字幕轨延续到新媒体。 */
export function MediaPlayer(props: Parameters<typeof MediaPlayerInstance>[0]): ReactElement {
  return <MediaPlayerInstance key={props.src} {...props} />;
}
