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
import type { useMediaDetail, useSessionTranscript } from '../data/hooks';
import {
  activeTargetId,
  isJumpable,
  jumpTargets,
} from '../data/playback';
import { evidenceKindLabel, formatDateTime, formatTimestamp, mediaKindLabel, mediaStatusBadge } from '../format';

type MediaDetailResult = ReturnType<typeof useMediaDetail>;
type TranscriptResult = ReturnType<typeof useSessionTranscript>;

export function PlaybackPanel({
  mediaId,
  detail,
  transcript,
}: {
  /** 当前回放目标媒体；为 null 表示该课堂还没有可回放的媒体。 */
  mediaId: string | null;
  detail: MediaDetailResult;
  transcript: TranscriptResult;
}): ReactElement {
  const api = isApiMode();
  const media = detail.data.media;
  const targets = useMemo(
    () => jumpTargets(transcript.data.segments, transcript.data.evidence),
    [transcript.data.segments, transcript.data.evidence],
  );
  const [seekRequest, setSeekRequest] = useState<SeekRequest | null>(null);
  const [positionMs, setPositionMs] = useState(0);
  const [clickedId, setClickedId] = useState<string | null>(null);
  const [showCaption, setShowCaption] = useState(true);
  const nonce = useRef(0);
  /** 跳转按钮节点，用于让高亮行跟随播放位置滚进可视区域。 */
  const jumpNodes = useRef(new Map<string, HTMLElement>());

  const playbackUrl = media?.playback_url ?? null;
  const timeActive = activeTargetId(targets, positionMs);
  // 同一时间点可能既有片段又有证据（样例里就是），按时间算会高亮先排序的那一条；
  // 刚点过的目标在播放位置仍落在它区间内时优先高亮，点了哪行就亮哪行。
  const clickedStillActive =
    clickedId !== null &&
    targets.some(
      (target) => target.id === clickedId && positionMs >= target.at_ms && positionMs < target.end_ms,
    );
  const active = clickedStillActive ? clickedId : timeActive;
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
    nonce.current += 1;
    setClickedId(id);
    setSeekRequest({ at_ms, nonce: nonce.current });
  }

  return (
    <>
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

        {mediaId === null ? (
          <EmptyState text="该课堂还没有主媒体。上传录像并通过媒体准备后，这里才能回放。" />
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
            ) : (
              <>
                <MediaPlayer
                  src={playbackUrl}
                  durationMs={media.duration_ms}
                  seekRequest={seekRequest}
                  onTimeMs={setPositionMs}
                  label={`课堂主媒体 ${media.id} 的播放代理`}
                  captions={transcript.data.segments}
                  showCaptions={showCaption}
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

        {transcript.status !== 'ready' ? null : targets.length === 0 ? (
          <EmptyState text="这一批次的片段与证据为空。结果为空时要结合批次状态判断是未生成还是确实没有，不能直接当成“没有讲话”。" />
        ) : (
          <ul className="jump-list">
            {targets.map((target) => {
              const jumpable = isJumpable(target, media?.duration_ms ?? null);
              const label =
                target.kind === 'segment'
                  ? `片段 ${target.segment_no}`
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
                    disabled={playbackUrl === null || !jumpable}
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

        {playbackUrl === null ? (
          <p className="load-note">没有可用的播放地址，跳转按钮不可用。</p>
        ) : (
          <p className="load-note">当前位置 {formatTimestamp(positionMs)}，命中的目标会高亮。</p>
        )}
      </Card>
    </>
  );
}
