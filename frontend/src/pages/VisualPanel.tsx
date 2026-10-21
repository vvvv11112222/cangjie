import { useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import { Card, StageFlow, TechDetails } from '../components/ui';
import { useRunPolling } from '../data/hooks';
import { listSessionRuns } from '../data/m2Api';
import { getMedia, isApiMode } from '../data/sources';
import { describeError } from '../data/errors';
import { getVisualSummary, getVisualPeople, getVisualObservations, observationAt, observationColor, verdictText } from '../data/visualApi';
import type { Behavior, VisualObservation, VisualPerson, VisualSummary } from '../data/visualApi';
import type { Media, Run } from '../types';
import { formatTimestamp, runStatusBadge } from '../format';

const LABELS: Record<Behavior, string> = { hand_raise: '举手', head_down: '低头', leave_seat: '离座', possible_phone: '疑似持手机' };

/** The existing classroom page gains one tab; navigation and existing panels stay intact. */
export function VisualPanel({ sessionId, currentRun }: { sessionId: string; currentRun: Run | null }): ReactElement {
  const [history, setHistory] = useState<Run[]>([]);
  const [historyCursor, setHistoryCursor] = useState<string | null>(null);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [chosen, setChosen] = useState<string | null>(null);
  const [historyBusy, setHistoryBusy] = useState(false);
  useEffect(() => {
    let live = true;
    void listSessionRuns(sessionId).then(page => { if (live) { setHistory(page.items); setHistoryCursor(page.next_cursor); } }).catch(error => { if (live) setHistoryError(describeError(error)); });
    return () => { live = false; };
  }, [sessionId]);
  useEffect(() => { if (currentRun?.mode === 'visual_only') setChosen(currentRun.id); }, [currentRun?.id]);
  const all = [...(currentRun?.mode === 'visual_only' ? [currentRun] : []), ...history.filter(r => r.mode === 'visual_only' && r.id !== currentRun?.id)];
  const selected = all.find(r => r.id === chosen) ?? all[0] ?? null;
  const polling = useRunPolling(selected?.id ?? null, selected?.status ?? null, isApiMode() && selected?.id !== currentRun?.id);
  const run = polling.run?.id === selected?.id ? polling.run : selected;
  async function moreHistory(): Promise<void> {
    if (!historyCursor || historyBusy) return;
    setHistoryBusy(true); setHistoryError(null);
    try { const page = await listSessionRuns(sessionId, historyCursor); if (page.next_cursor === historyCursor) throw new Error('记录分页没有前进，请重新打开页面。'); setHistory(rows => [...rows, ...page.items.filter(item => !rows.some(r => r.id === item.id))]); setHistoryCursor(page.next_cursor); }
    catch (error) { setHistoryError(describeError(error)); } finally { setHistoryBusy(false); }
  }
  return <>
    <Card title="视觉观察" subtitle="检查录像中的可见人数与动作。看不清的部分保留为“看不清”，不计为没有动作。">
      {all.length > 0 && <label className="account-field"><span>选择视觉分析记录</span><select value={selected?.id ?? ''} onChange={event => setChosen(event.target.value)}>{all.map(r => <option key={r.id} value={r.id}>{r.id.slice(0, 8)} · {runStatusBadge(r.status).label}</option>)}</select></label>}
      {historyError && <p role="alert" className="load-note load-note-error">{historyError}</p>}
      {historyCursor && <button className="btn btn-secondary" disabled={historyBusy} onClick={() => void moreHistory()}>查找更早的分析</button>}
      {!run && <p className="empty">还没有视觉分析记录。在“批次与操作”中上传录像后，点击“举手与低头分析”。</p>}
      {run && run.status !== 'succeeded' && <><StageFlow run={run} /><p className="load-note">{run.status === 'queued' || run.status === 'running' ? '正在分析。完成后会自动显示结果；可以先切换标签查看其他内容。' : '本次分析未完成，没有可展示的完整结果。请回到“批次与操作”查看原因或重试。'}</p></>}
      {polling.error && <p role="alert">{polling.error}</p>}
    </Card>
    {run?.status === 'succeeded' && <VisualResults key={run.id} run={run} />}
  </>;
}

function VisualResults({ run }: { run: Run }): ReactElement {
  const [summary, setSummary] = useState<VisualSummary | null>(null);
  const [people, setPeople] = useState<VisualPerson[]>([]);
  const [peopleCursor, setPeopleCursor] = useState<string | null>(null);
  const [track, setTrack] = useState<string | null>(null);
  const [observations, setObservations] = useState<{ track: string; rows: VisualObservation[]; cursor: string | null } | null>(null);
  const [media, setMedia] = useState<Media | null>(null);
  const [mediaError, setMediaError] = useState<string | null>(null);
  const [mediaReload, setMediaReload] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [personError, setPersonError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);
  const [at, setAt] = useState(0);
  const [filter, setFilter] = useState('all');
  const [playerFailed, setPlayerFailed] = useState(false);
  const [imageFailed, setImageFailed] = useState(false);
  const video = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    let live = true;
    setError(null); setSummary(null); setPeople([]); setTrack(null); setObservations(null);
    void Promise.all([getVisualSummary(run.id), getVisualPeople(run.id)]).then(([s, p]) => {
      if (!live) return;
      if (s.run_id !== run.id || s.media_asset_id !== run.media_asset_id) throw new Error('结果与当前录像不一致，请重新读取。');
      setSummary(s); setPeople(p.items); setPeopleCursor(p.next_cursor); setTrack(p.items[0]?.track_id ?? null);
    }).catch(e => { if (live) setError(describeError(e)); });
    return () => { live = false; };
  }, [run.id, reload]);
  useEffect(() => {
    let live = true;
    setMedia(null); setMediaError(null); setPlayerFailed(false);
    void getMedia(run.media_asset_id).then(m => {
      if (!live) return;
      if (m.id !== run.media_asset_id) throw new Error('播放信息与本次分析的录像不一致。');
      setMedia(m);
    }).catch(e => { if (live) setMediaError(describeError(e)); });
    return () => { live = false; };
  }, [run.id, reload, mediaReload]);
  useEffect(() => {
    let live = true; setPersonError(null); setFilter('all');
    if (track) void getVisualObservations(run.id, track).then(page => { if (live) { setObservations({ track, rows: page.items, cursor: page.next_cursor }); } }).catch(e => { if (live) setPersonError(describeError(e)); });
    return () => { live = false; };
  }, [run.id, track, reload]);
  const rows = observations?.track === track ? observations.rows : [];
  const current = summary ? observationAt(rows, at, summary.sampling.interval_ms) : null;
  useEffect(() => { setImageFailed(false); }, [current?.frame_asset_id]);
  useEffect(() => () => { const node = video.current; if (node) { node.pause(); node.removeAttribute('src'); node.load(); } }, []);
  async function morePeople(): Promise<void> {
    if (!peopleCursor || busy) return; setBusy(true);
    try { const page = await getVisualPeople(run.id, peopleCursor); if (page.next_cursor === peopleCursor) throw new Error('人物分页没有前进，请重新读取。'); setPeople(old => [...old, ...page.items.filter(p => !old.some(row => row.track_id === p.track_id))]); setPeopleCursor(page.next_cursor); }
    catch (e) { setError(describeError(e)); } finally { setBusy(false); }
  }
  async function moreObservations(): Promise<void> {
    if (!track || observations?.track !== track || !observations.cursor || busy) return;
    const fromTrack = track; setBusy(true); setPersonError(null);
    try { const page = await getVisualObservations(run.id, fromTrack, observations.cursor); if (page.next_cursor === observations.cursor) throw new Error('人物记录分页没有前进，请重新读取。'); setObservations(old => old?.track === fromTrack && old.cursor === observations.cursor ? { track: fromTrack, rows: [...old.rows, ...page.items.filter(p => !old.rows.some(row => row.timestamp_ms === p.timestamp_ms))], cursor: page.next_cursor } : old); }
    catch (e) { setPersonError(describeError(e)); } finally { setBusy(false); }
  }
  function jump(row: VisualObservation): void {
    setAt(row.timestamp_ms);
    if (video.current) {
      video.current.currentTime = row.timestamp_ms / 1000;
      video.current.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }
  }
  if (!summary) return <Card title="读取视觉结果"><p role={error ? 'alert' : undefined}>{error ?? '正在读取分析结果…'}</p>{error && <button className="btn btn-secondary" onClick={() => setReload(v => v + 1)}>重新读取</button>}</Card>;
  const known = summary.visual.counts.flatMap(p => p.visible_count === null ? [] : [p.visible_count]);
  const max = (key: 'hand_raise_count' | 'head_down_count'): string => { const n = summary.visual.counts.flatMap(p => p[key] === null ? [] : [p[key]!]); return n.length ? String(Math.max(...n)) : '看不清'; };
  const shown = rows.filter(row => filter === 'all' || (filter === 'unknown' ? row.behaviors.hand_raise === null || row.behaviors.head_down === null : row.behaviors[filter as Behavior] === true));
  const box = current?.bbox;
  const hasPlayer = Boolean(media?.playback_url) && !playerFailed;
  const records = track && <><h3>{track} 的画面记录</h3><label className="account-field"><span>筛选动作</span><select value={filter} onChange={e => setFilter(e.target.value)}><option value="all">全部已读取画面</option><option value="hand_raise">发现举手</option><option value="head_down">发现低头</option><option value="unknown">有动作看不清</option></select></label><p className="load-note">已读取 {rows.length} 次观察。每行对应一次实际检查，不代表动作持续了这么多次。{observations?.track === track && observations.cursor ? '还有记录未读取，可继续加载；筛选只作用于已读取部分。' : ''}</p><div className="table-wrap"><table className="table"><thead><tr><th>录像时间</th>{Object.values(LABELS).map(label => <th key={label}>{label}</th>)}<th>核对</th></tr></thead><tbody>{shown.map(row => <tr key={row.timestamp_ms}><td>{formatTimestamp(row.timestamp_ms)}</td>{(Object.keys(LABELS) as Behavior[]).map(kind => <td key={kind}>{verdictText(row.behaviors[kind])}</td>)}<td><button className="btn btn-secondary" onClick={() => jump(row)}>{hasPlayer ? '定位录像' : '查看记录'}</button></td></tr>)}</tbody></table></div>{!shown.length && <p className="empty">已读取的画面中没有符合筛选条件的记录。</p>}{observations?.track === track && observations.cursor && <button className="btn btn-secondary" disabled={busy} onClick={() => void moreObservations()}>继续读取这个人的记录</button>}</>;
  return <>
    <Card title="分析概况" subtitle={`每隔 ${summary.sampling.interval_ms / 1000} 秒检查一次；共检查 ${summary.visual.counts.length} 张画面。可见人数包含画面中的人员，不等于学生出勤人数。`}>
      <div className="visual-metrics"><div><strong>{known.length ? (known.reduce((a, b) => a + b, 0) / known.length).toFixed(1) : '看不清'}</strong><span>平均可见人数</span></div><div><strong>{max('hand_raise_count')}</strong><span>同一画面举手最多人数</span></div><div><strong>{max('head_down_count')}</strong><span>同一画面低头最多人数</span></div></div>
      <p className="load-note">低头可能是在阅读或书写；这些观察需要结合教学内容核对，不直接评价学生是否认真。</p>
    </Card>
    <Card title="按人物查看与回放" subtitle="编号只属于本次录像。选中人物不会改变行为颜色：红色举手，蓝色低头，绿色未见这两种动作，灰色看不清。">
      {error && <p role="alert" className="load-note load-note-error">{error}</p>}
      {!hasPlayer && <p className="load-note" role="status">分析结果已保存。右侧选择人物，下方表格查看举手、低头的判断；录像不能播放不代表分析结果丢失。</p>}
      <div className="visual-workspace">
        <div>
          {media?.playback_url && !playerFailed ? <div className="visual-video" style={{ aspectRatio: `${summary.visual.width}/${summary.visual.height}` }}>
            <video ref={video} controls preload="metadata" playsInline src={media.playback_url} aria-label="视觉分析录像" onLoadedMetadata={e => { e.currentTarget.currentTime = at / 1000; }} onTimeUpdate={e => setAt(Math.round(e.currentTarget.currentTime * 1000))} onError={() => setPlayerFailed(true)} />
            {box && <svg className="visual-overlay" viewBox={`0 0 ${summary.visual.width} ${summary.visual.height}`} aria-label={`当前人物 ${track}`}><rect x={box[0]} y={box[1]} width={box[2] - box[0]} height={box[3] - box[1]} fill="none" stroke={observationColor(current!)} strokeWidth="2" /><text x={box[0]} y={Math.max(12, box[1] - 3)} fill={observationColor(current!)} fontSize="13">{track}</text></svg>}
          </div> : <><p className="load-note">{playerFailed ? '录像播放失败，可以重新检查播放许可。' : '当前录像未开放回放，或播放信息暂不可用。请在“素材与来源”核对是否申请并允许“回放”；人物分析记录仍可查看。'}</p><a className="btn btn-secondary" href="#/sources">核对录像来源与用途</a><button className="btn btn-secondary" onClick={() => setMediaReload(v => v + 1)}>重新检查回放</button></>}
          {mediaError && <p role="alert" className="load-note load-note-error">播放信息读取失败：{mediaError}；已保存的分析结果继续显示。</p>}
          <p className="load-note">{formatTimestamp(at)} · {track ? `当前选中 ${track}` : '未选择人物'} · {current ? `举手：${verdictText(current.behaviors.hand_raise)}；低头：${verdictText(current.behaviors.head_down)}` : '这个时刻没有已加载的观察，不补画框或推测行为。'}</p>
          {media?.allowed_actions.includes('playback') && current?.frame_asset_id && !imageFailed && <details><summary>查看这个时刻的证据图片</summary><div className="visual-evidence"><img loading="lazy" alt={`${track} 在 ${formatTimestamp(current.timestamp_ms)} 的原画面`} src={`/api/v1/media/${encodeURIComponent(current.frame_asset_id)}/content`} onError={() => setImageFailed(true)} />{box && <svg className="visual-overlay" viewBox={`0 0 ${summary.visual.width} ${summary.visual.height}`}><rect x={box[0]} y={box[1]} width={box[2] - box[0]} height={box[3] - box[1]} fill="none" stroke={observationColor(current)} strokeWidth="2" /></svg>}</div></details>}
          {imageFailed && <p role="alert">图片暂不可读取，可以定位原录像核对。</p>}
          {!hasPlayer && records}
        </div>
        <aside className="visual-people" aria-label="人物列表"><h3>人物编号</h3><p className="muted">已读取 {people.length} 个编号；点击查看该人的记录。</p>{people.map(person => <button key={person.track_id} type="button" className={`visual-person${track === person.track_id ? ' selected' : ''}`} aria-pressed={track === person.track_id} onClick={() => setTrack(person.track_id)}><strong>{person.track_id}</strong><span>检查了 {person.sample_count} 次</span>{person.track_id === track && current && <small>举手{verdictText(current.behaviors.hand_raise)} · 低头{verdictText(current.behaviors.head_down)}</small>}</button>)}{peopleCursor && <button className="btn btn-secondary" disabled={busy} onClick={() => void morePeople()}>加载更多人物</button>}{!people.length && <p>这段录像没有检出可记录的人物。</p>}</aside>
      </div>
      {personError && <p role="alert">{personError}</p>}
      {hasPlayer && records}
    </Card>
    <Card title="观察范围与限制"><ul>{summary.limitations.map((text, index) => <li key={index}>{text}</li>)}</ul><TechDetails><p>批次：{run.id}；模型：{summary.model.name}；版本：{summary.model.revision}</p></TechDetails></Card>
  </>;
}
