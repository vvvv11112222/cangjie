/**
 * 课堂详情的"回放"标签：播放当前主媒体的播放代理，并把片段/证据跳到原录像时间。
 *
 * 分工：本组件只做展示与跳转交互；取数由 SessionDetailPage 传进来的两个 hook 结果负责
 * （useMediaDetail 拿播放地址，useSessionTranscript 拿片段与证据），
 * 时间与目标的挑选逻辑在 data/playback.ts，可单独单元测试。
 */

import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import { MediaPlayer } from '../components/MediaPlayer';
import type { SeekRequest } from '../components/MediaPlayer';
import { IconAlert } from '../components/icons';
import {
  BadgeTag,
  Card,
  EmptyState,
  Field,
  IdChip,
  LoadControls,
  LoadStateNote,
  TechDetails,
} from '../components/ui';
import { isApiMode } from '../data/sources';
import { usePlaybackProbe } from '../data/hooks';
import type { useMediaDetail, useSessionTranscript, useSessionMedia } from '../data/hooks';
import {
  activeTargetId,
  isJumpable,
  jumpTargets,
  playbackTranscript,
} from '../data/playback';
import { evidenceKindLabel, formatDateTime, formatTimestamp, mediaKindLabel, mediaStatusBadge } from '../format';

type MediaDetailResult = ReturnType<typeof useMediaDetail>;
type TranscriptResult = ReturnType<typeof useSessionTranscript>;

export function PlaybackPanel({
  mediaId,
  detail,
  transcript,
  mediaList,
  needsMediaList,
}: {
  /** 当前回放目标媒体；为 null 表示该课堂还没有可回放的媒体。 */
  mediaId: string | null;
  detail: MediaDetailResult;
  transcript: TranscriptResult;
  mediaList: ReturnType<typeof useSessionMedia>;
  needsMediaList: boolean;
}): ReactElement {
  const api = isApiMode();
  // 重取许可时保留已失败的节点但暂停操作；许可成功后对同一节点执行 load，清除失败资源。
  const media = detail.status === 'ready' || detail.status === 'loading' ? detail.data.media : null;
  const matched = useMemo(() => playbackTranscript(media, transcript.data), [media, transcript.data]);
  const targets = useMemo(
    () => transcript.status === 'ready' ? jumpTargets(matched.segments, matched.evidence, matched.frames) : [],
    [matched, transcript.status],
  );
  const [seekRequest, setSeekRequest] = useState<SeekRequest | null>(null);
  const [positionMs, setPositionMs] = useState(0);
  const [clickedId, setClickedId] = useState<string | null>(null);
  const [showCaption, setShowCaption] = useState(true);
  const [playerFailed, setPlayerFailed] = useState(false);
  const [retryNonce, setRetryNonce] = useState(0);
  const retryPending = useRef(false);
  const nonce = useRef(0);
  /** 跳转按钮节点，用于让高亮行跟随播放位置滚进可视区域。 */
  const jumpNodes = useRef(new Map<string, HTMLElement>());

  const playbackUrl = media?.status === 'ready' ? media.playback_url : null;
  // 浏览器自己发 Range 请求，前端看不到 416/403 这类状态码，所以先预检一次播放地址。
  const probe = usePlaybackProbe(playbackUrl);
  const playerAvailable = detail.status === 'ready' && playbackUrl !== null && probe.status !== 'failed' && !playerFailed;
  useEffect(() => {
    if (!retryPending.current || detail.status === 'loading') return;
    // 许可与新预检都确认后再 load，避免视频读取与 1 字节探测互相抢占同一资源。
    if (detail.status === 'ready' && probe.status === 'checking') return;
    retryPending.current = false;
    if (detail.status === 'ready' && probe.status === 'ok') {
      setPlayerFailed(false);
      setRetryNonce(current => current + 1);
    }
  }, [detail.status, detail.data, probe.status]);
  useEffect(() => {
    setPlayerFailed(false);
    setSeekRequest(null);
    setClickedId(null);
    setPositionMs(0);
  }, [playbackUrl]);
  const timeActive = activeTargetId(targets, positionMs);
  // 同一时间点可能既有片段又有证据（样例里就是），按时间算会高亮先排序的那一条；
  // 刚点过的目标在播放位置仍落在它区间内时优先高亮，点了哪行就亮哪行。
  const clickedStillActive =
    clickedId !== null &&
    targets.some(
      (target) => target.id === clickedId && positionMs >= target.at_ms && positionMs < target.end_ms,
    );
  const active = playerAvailable ? (clickedStillActive ? clickedId : timeActive) : null;
  // 高亮行跟随播放进度滚动（block:'nearest' 只在必要时滚动，不会整页跳动）。
  useEffect(() => {
    if (active === null) {
      return;
    }
    const node = jumpNodes.current.get(active);
    if (node === undefined || typeof node.scrollIntoView !== 'function') {
      return;
    }
    try {
      node.scrollIntoView({ block: 'nearest' });
    } catch {
      // jsdom 与老浏览器忽略即可：这只影响体验，不影响跳转本身。
    }
  }, [active]);

  function jumpTo(id: string, at_ms: number): void {
    if (!playerAvailable) return;
    nonce.current += 1;
    setClickedId(id);
    setSeekRequest({ at_ms, nonce: nonce.current });
  }

  function retryPlayback(): void {
    if (detail.status !== 'ready' || retryPending.current) return;
    setSeekRequest(null);
    setClickedId(null);
    setPositionMs(0);
    // 重新核对服务端当前许可，不仅重放旧地址。
    if (api) {
      retryPending.current = true;
      detail.reload();
    } else {
      setPlayerFailed(false);
      setRetryNonce(current => current + 1);
    }
    probe.recheck();
  }

  return (
    <>
      <p className="result-context">当前回放：{media?.original_filename || "历史录像（文件名未记录）"}。片段与证据仅匹配当前回放录像。</p>
      <Card
        title="回放"
        subtitle="播放的是课堂当前主媒体的播放代理；时间口径为原录像起点的整数毫秒，代理不裁剪、不变速，所以跳转时间与原录像一致。"
        actions={
          playbackUrl === null ? undefined : (
            <button
              type="button"
              className="btn btn-secondary"
              aria-pressed={showCaption}
              onClick={() => setShowCaption((current) => !current)}
            >
              字幕：{showCaption ? '开' : '关'}
            </button>
          )
        }
      >
        <LoadStateNote status={detail.status} error={detail.error} />
        <LoadControls actionLabel={detail.actionLabel} loadMore={detail.loadMore} />
        {needsMediaList ? (
          <div>
            <LoadStateNote status={mediaList.status} error={mediaList.error} />
            <LoadControls actionLabel={mediaList.actionLabel} loadMore={mediaList.loadMore} />
            {mediaId === null && mediaList.data.nextCursor !== null && mediaList.status === 'ready' ? (
              <p className="load-note">已读取页面尚无可回放媒体，请继续加载后续页面。</p>
            ) : null}
          </div>
        ) : null}

        {mediaId === null ? (
          mediaList.status === 'ready' && mediaList.data.nextCursor === null ? (
            <EmptyState text="暂无可回放媒体：尚未上传、尚未就绪或未获准播放。可在「批次与操作」查看媒体状态。" />
          ) : null
        ) : media === null ? null : (
          <>
            <div className="field-grid">
              <Field label="媒体">
                <IdChip id={media.id} />
              </Field>
              <Field label="类型">{mediaKindLabel(media.kind)}</Field>
              <Field label="状态">
                <BadgeTag badge={mediaStatusBadge(media.status)} />
              </Field>
              <Field label="时长">
                {media.duration_ms === null ? <span className="muted">null</span> : formatTimestamp(media.duration_ms)}
              </Field>
              <Field label="播放资产">
                {media.playback_asset_id === null ? <span className="muted">null</span> : <IdChip id={media.playback_asset_id} />}
              </Field>
              <Field label="有效期">
                {media.expires_at === null ? <span className="muted">null</span> : formatDateTime(media.expires_at)}
              </Field>
            </div>

            {media.status === 'ready' ? null : (
              <p className="notice-strip">
                <IconAlert size={14} aria-hidden="true" />
                媒体状态为 {media.status}
                {media.status === 'pending'
                  ? '，可先在「批次与操作」里做媒体准备（probe 允许对 pending 原媒体执行）。'
                  : '，未就绪前不能播放。'}
              </p>
            )}

            {playbackUrl === null ? (
              media.status === 'ready' ? (
                <p className="notice-strip">
                  <IconAlert size={14} aria-hidden="true" />
                  服务端没有给出 playback_url：来源未核验、缺少 playback 用途、媒体已过期，
                  或课堂内容读取权限已到期。页面不拿别的地址顶替，请按上述原因核对。
                </p>
              ) : null
            ) : probe.status === 'failed' ? (
              <>
                <p className="notice-strip">
                  <IconAlert size={14} aria-hidden="true" />
                  {probe.note}
                  {probe.note?.includes('RANGE_NOT_SATISFIABLE')
                    ? '（416：这段媒体的时长/字节范围与播放地址不匹配，常见于播放代理被重新生成或媒体还在准备中。）'
                    : ''}
                </p>
                <div className="action-row">
                  <button type="button" className="btn btn-secondary" onClick={() => probe.recheck()}>
                    重新检查播放地址
                  </button>
                </div>
              </>
            ) : (
              <>
                {/* 预检只是附加信息，不阻挡播放器出现：检查期间照常渲染播放器。 */}
                {probe.status === 'checking' ? (
                  <p className="load-note">正在检查播放地址（发送一次 1 字节的 Range 请求）……</p>
                ) : null}
                <MediaPlayer
                  key={playbackUrl}
                  src={playbackUrl}
                  durationMs={media.duration_ms}
                  seekRequest={seekRequest}
                  onTimeMs={setPositionMs}
                  label={`课堂主媒体 ${media.id} 的播放代理`}
                  captions={transcript.status === 'ready' ? matched.segments : []}
                  showCaptions={showCaption}
                  onRetry={retryPlayback}
                  onFailed={setPlayerFailed}
                  reloadToken={retryNonce}
                  suspended={detail.status !== 'ready'}
                />
                {api ? null : (
                  <p className="load-note">
                    固定样例模式播放的是仓库内自带的合成样例视频
                    （frontend/public/samples/m2-playback-demo.mp4，无真实课堂内容）。
                    拖动进度条与时间跳转都要静态服务支持 Range：`npm run dev` 和
                    `npx vite preview` 支持；`python -m http.server` 不支持，只能从头连续播放。
                    接口模式下播放地址来自服务端，由 Go 按协议返回 200/206/416。
                  </p>
                )}
              </>
            )}

            {/* 协议字段原样保留，只是从业务视线里下沉到折叠区。 */}
            <TechDetails>
              <ul className="tech-list">
                <li className="mono">media.id = {media.id}</li>
                <li className="mono">media.session_id = {media.session_id}</li>
                <li className="mono">media.source_record_id = {media.source_record_id ?? 'null'}</li>
                <li className="mono">media.parent_asset_id = {media.parent_asset_id ?? 'null'}</li>
                <li className="mono">media.kind = {media.kind}</li>
                <li className="mono">media.status = {media.status}</li>
                <li className="mono">media.sha256 = {media.sha256}</li>
                <li className="mono">media.mime_type = {media.mime_type}</li>
                <li className="mono">media.byte_size = {media.byte_size}</li>
                <li className="mono">media.duration_ms = {media.duration_ms ?? 'null'}</li>
                <li className="mono">media.expires_at = {media.expires_at ?? 'null'}</li>
                <li className="mono">media.playback_asset_id = {media.playback_asset_id ?? 'null'}</li>
                <li className="mono">media.playback_url = {media.playback_url ?? 'null'}</li>
                <li className="mono">media.allowed_actions = [{media.allowed_actions.join(', ')}]</li>
              </ul>
            </TechDetails>
          </>
        )}
      </Card>

      <Card
        title="跳到证据与片段"
        subtitle="片段与证据的时间都是原录像起点毫秒；点击后播放器定位到该时间点。过期或已移除的证据不能定位，与协议 EVIDENCE_UNAVAILABLE 一致。"
      >
        <LoadStateNote status={transcript.status} error={transcript.error} />
        <LoadControls actionLabel={transcript.actionLabel} loadMore={transcript.loadMore} />
        {transcript.status === 'ready' && matched.note !== null ? <p className="notice-strip">{matched.note}</p> : null}

        {media === null || transcript.status !== 'ready' || (matched.note !== null && targets.length === 0) ? null : targets.length === 0 ? (
          <EmptyState text="这一批次的片段与证据为空。结果为空时要结合批次状态判断是未生成还是确实没有，不能直接当成“没有讲话”。" />
        ) : (
          <ul className="jump-list">
            {targets.map((target) => {
              const jumpable = isJumpable(target, media?.duration_ms ?? null);
              const label =
                target.kind === 'segment'
                  ? `片段 ${target.segment_no}`
                  : target.kind === 'frame'
                    ? '关键帧'
                    : `证据 · ${target.evidence_kind === null ? '未知类型' : evidenceKindLabel(target.evidence_kind)}`;
              return (
                <li
                  key={target.id}
                  ref={(node) => {
                    if (node === null) {
                      jumpNodes.current.delete(target.id);
                    } else {
                      jumpNodes.current.set(target.id, node);
                    }
                  }}
                >
                  <button
                    type="button"
                    className={`jump-item${active === target.id ? ' active' : ''}`}
                    aria-current={active === target.id ? 'true' : undefined}
                    disabled={!playerAvailable || !jumpable}
                    onClick={() => jumpTo(target.id, target.at_ms)}
                  >
                    <span className="jump-time mono">
                      {formatTimestamp(target.at_ms)} – {formatTimestamp(target.end_ms)}
                    </span>
                    <span className="jump-label">{label}</span>
                    <span className="jump-text">{target.text}</span>
                    {jumpable ? null : <span className="jump-warn">超出媒体时长，无法跳转</span>}
                  </button>
                </li>
              );
            })}
          </ul>
        )}

        {!playerAvailable ? (
          <p className="load-note">播放器当前不可用，跳转按钮不可用；请先恢复播放。</p>
        ) : (
          <p className="load-note">当前位置 {formatTimestamp(positionMs)}，命中的目标会高亮。</p>
        )}
      </Card>
    </>
  );
}
