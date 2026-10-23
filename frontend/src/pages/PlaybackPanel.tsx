/**
 * 课堂详情的"回放"标签：播放当前主媒体的播放代理，并把片段/证据跳到原录像时间。
 *
 * 分工：本组件只做展示与跳转交互；取数由 SessionDetailPage 传进来的两个 hook 结果负责
 * （useMediaDetail 拿播放地址，useSessionTranscript 拿片段与证据），
 * 时间与目标的挑选逻辑在 data/playback.ts，可单独单元测试。
 */

import type { ReactElement } from 'react';
import { useEffect,useMemo,useRef,useState } from 'react';
import type { SeekRequest } from '../components/MediaPlayer';
import { MediaPlayer } from '../components/MediaPlayer';
import { IconAlert } from '../components/icons';
import {
BadgeTag,
Card,
EmptyState,
Field,
IdChip,
LoadControls,
LoadStateNote
} from '../components/ui';
import type { useMediaDetail,useSessionMedia,useSessionTranscript } from '../data/hooks';
import { usePlaybackProbe } from '../data/hooks';
import {
activeTargetId,
isJumpable,
jumpTargets,
playbackTranscript,
} from '../data/playback';
import { isApiMode } from '../data/sources';
import { evidenceKindLabel,formatDateTime,formatTimestamp,mediaKindLabel,mediaStatusBadge } from '../format';

type MediaDetailResult = ReturnType<typeof useMediaDetail>;
type TranscriptResult = ReturnType<typeof useSessionTranscript>;

export function PlaybackPanel({
  mediaId,
  detail,
  transcript,
  mediaList,
  needsMediaList,
  onSelectMedia,
}: {
  /** 当前回放目标媒体；为 null 表示该课堂还没有可回放的媒体。 */
  mediaId: string | null;
  detail: MediaDetailResult;
  transcript: TranscriptResult;
  mediaList: ReturnType<typeof useSessionMedia>;
  needsMediaList: boolean;
  onSelectMedia?(mediaId: string): void;
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
      <p className="result-context">当前回放：{media?.original_filename || "历史录像（文件名未记录）"}</p>
      <Card
        title="回放"
        
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
        {onSelectMedia && <label className="account-field"><span>选择回放录像</span><select value={mediaId ?? ''} onChange={event => onSelectMedia(event.target.value)}>
          {mediaId !== null && !mediaList.data.items.some(item => item.id === mediaId && item.kind === 'source' && item.status === 'ready' && item.allowed_actions.includes('playback')) && <option value={mediaId}>当前录像 · {mediaId.slice(0, 8)}</option>}
          {mediaId === null && <option value="">请选择已就绪且允许回放的录像</option>}
          {mediaList.data.items.filter(item => item.kind === 'source' && item.status === 'ready' && item.allowed_actions.includes('playback')).map(item => <option key={item.id} value={item.id}>{item.original_filename || `历史录像 ${item.id.slice(0, 8)}`} · {item.created_at ? formatDateTime(item.created_at) : "上传时间未记录"}</option>)}
        </select><small className="muted">录像编号与“批次与操作”的媒体编号一致。也可以在“视觉观察”点击“在回放中打开此录像”。</small></label>}
        <LoadStateNote status={detail.status} error={detail.error} />
        <LoadControls actionLabel={detail.actionLabel} loadMore={detail.loadMore} />
        {needsMediaList ? (
          <div>
            {mediaId !== null && mediaList.error !== null ? <p className="load-note">其他录像列表暂时无法读取，当前录像仍可回放；可重试读取列表。</p> : <LoadStateNote status={mediaList.status} error={mediaList.error} />}
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
                {media.duration_ms === null ? <span className="muted">时长未读取</span> : formatTimestamp(media.duration_ms)}
              </Field>
              <Field label="播放资产">
                {media.playback_asset_id === null ? <span className="muted">未单独记录</span> : <IdChip id={media.playback_asset_id} />}
              </Field>
              <Field label="有效期">
                {media.expires_at === null ? <span className="muted">未设置到期时间</span> : formatDateTime(media.expires_at)}
              </Field>
            </div>

            {media.status === 'ready' ? null : (
              <p className="notice-strip">
                <IconAlert size={14} aria-hidden="true" />
                媒体状态为 {mediaStatusBadge(media.status).label}
                {media.status === 'pending'
                  ? '，可先在“批次与操作”里点击“媒体准备”。'
                  : '，未就绪前不能播放。'}
              </p>
            )}

            {playbackUrl === null ? (
              media.status === 'ready' ? (
                <p className="notice-strip">
                  <IconAlert size={14} aria-hidden="true" />
                  当前录像暂不能播放：来源可能未核验、未获准回放、录像已过期，
                  或课堂读取权限已到期。请在“素材与来源”核对。
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
                  <p className="load-note">正在检查录像能否播放……</p>
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
                
              </>
            )}

            {/* 协议字段原样保留，只是从业务视线里下沉到折叠区。 */}
                      </>
        )}
      </Card>

      <Card
        title="跳到证据与片段"
        
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
