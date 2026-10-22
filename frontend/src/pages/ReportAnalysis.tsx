import { useId, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import type { DimensionCode, Evidence, Report } from '../types';
import { BadgeTag, TechDetails } from '../components/ui';
import { MediaPlayer } from '../components/MediaPlayer';
import { useMediaDetail, usePlaybackProbe } from '../data/hooks';
import { sameId } from '../data/ids';
import { coverageStatusBadge, dimensionLabel, formatIntervals, formatTimestamp, observationTypeBadge, reviewStatusBadge } from '../format';

const dimensionHelp: Record<DimensionCode, string> = {
  content: '看核心问题、知识联系和例子如何组织。',
  pace: '看教师按什么思路推进，以及可验证的活动安排。',
  thinking: '看提问是否引导学生解释、比较和推理。',
  expression: '看术语、例子和衔接是否清楚易懂。',
  management: '看任务指令、讨论规则和反馈安排。',
  technology: '看工具和演示是否服务于教学目标。',
};

export function reportLimitation(value: string): string {
  if (value.startsWith('MODEL_INPUT_TRUNCATED:')) return '部分课堂文字因输入额度限制被截短，结论只针对实际送入模型的文字';
  if (value.startsWith('MODEL_INPUT_SKIPPED_COUNT:')) return `有 ${value.slice('MODEL_INPUT_SKIPPED_COUNT:'.length)} 条课堂记录未送入模型，不能据此评价整节课`;
  if (value.startsWith('LIMITATIONS_SKIPPED_COUNT:')) return `另有 ${value.slice('LIMITATIONS_SKIPPED_COUNT:'.length)} 项限制未展开，详细记录请核对本次分析输入`;
  return value;
}

export function CitedEvidence({ ids, evidenceById, onSelect }: {
  ids: string[]; evidenceById: Map<string, Evidence>; onSelect(evidence: Evidence): void;
}): ReactElement {
  if (ids.length === 0) return <span className="muted">没有可引用的课堂记录</span>;
  return <span className="report-citations">{ids.map((id, index) => {
    const evidence = evidenceById.get(id);
    if (evidence === undefined) return <span key={id} className="muted">第 {index + 1} 处依据暂未加载</span>;
    return <button type="button" key={id} className="btn btn-secondary report-citation" onClick={() => onSelect(evidence)}>
      查看依据 · {formatTimestamp(evidence.start_ms)}–{formatTimestamp(evidence.end_ms)} · {Array.from(evidence.description).slice(0, 80).join('') || '课堂记录'}{Array.from(evidence.description).length > 80 ? '…' : ''}
    </button>;
  })}</span>;
}

/** Playback is requested for the evidence's own media, never the current primary media. */
function EvidencePlayback({ evidence }: { evidence: Evidence }): ReactElement {
  const detail = useMediaDetail(evidence.media_asset_id);
  const media = detail.status === 'ready' ? detail.data.media : null;
  const sameMedia = media !== null && sameId(media.id, evidence.media_asset_id) && sameId(media.session_id, evidence.session_id);
  const url = sameMedia && media.status === 'ready' ? media.playback_url : null;
  const probe = usePlaybackProbe(url);
  return <>
    {detail.status === 'error' ? <p role="alert">{detail.error}</p> : null}
    {detail.status === 'loading' ? <p role="status">正在取得这段课堂录像…</p> : null}
    {url === null && detail.status === 'ready' ? <p className="notice-strip">这段原录像目前不可回放，可能尚未准备好、已过期或未获播放许可。课堂文字记录仍可核对。</p> : null}
    {probe.status === 'failed' ? <p role="alert">{probe.note}</p> : null}
    {probe.status === 'checking' ? <p role="status">正在检查播放许可…</p> : null}
    {url !== null && probe.status !== 'failed' && probe.status !== 'checking' ? <MediaPlayer
      key={`${evidence.id}:${url}`} src={url} durationMs={media?.duration_ms ?? null}
      seekRequest={{ at_ms: evidence.start_ms, nonce: 1 }} label="报告依据对应的原录像"
      showCaptions={false} onRetry={() => { detail.reload(); probe.recheck(); }}
    /> : null}
    {detail.status === 'error' || probe.status === 'failed' ? <button type="button" className="btn btn-secondary" onClick={() => { detail.reload(); probe.recheck(); }}>重新加载原录像</button> : null}
  </>;
}

export function ReportEvidence({ evidence, onClose }: { evidence: Evidence; onClose(): void }): ReactElement {
  return <section className="report-evidence" aria-label="课堂依据与回放">
    <header className="observation-head"><h3>课堂依据 · {formatTimestamp(evidence.start_ms)}–{formatTimestamp(evidence.end_ms)}</h3>
      <button type="button" className="btn btn-secondary" onClick={onClose}>关闭依据</button></header>
    <p className="report-prose">{evidence.description}</p>
    {evidence.availability === 'available' ? <EvidencePlayback evidence={evidence} />
      : <p className="notice-strip">原始依据{evidence.availability === 'expired' ? '已过期' : '已移除'}，保留文字供追溯，无法回放。</p>}
    <TechDetails><p>证据：{evidence.id}</p><p>媒体：{evidence.media_asset_id}</p><p>分析批次：{evidence.run_id}</p></TechDetails>
  </section>;
}

export function ReportAnalysis({ report, evidenceById, onSelect }: {
  report: Report; evidenceById: Map<string, Evidence>; onSelect(evidence: Evidence): void;
}): ReactElement {
  const [selected, setSelected] = useState<DimensionCode>('content');
  const dimension = report.dimensions.find((item) => item.dimension_code === selected) ?? report.dimensions[0];
  const panelId = useId();
  const buttons = useRef(new Map<number, HTMLButtonElement>());
  if (dimension === undefined) return <p className="empty">暂未取得六维分析。</p>;
  const observations = report.observations.filter((item) => item.dimension_code === dimension.dimension_code);
  return <div className="report-analysis">
    {report.provenance && typeof report.provenance['included_materials'] === 'object' && report.provenance['included_materials'] !== null ? <p className="notice-strip">本报告实际采用：{Object.entries(report.provenance['included_materials'] as Record<string, unknown>).filter(([, value]) => value === true).map(([key]) => ({ academic_schedule: '课程、教师、班级及课表登记', speech: '本录像的语音文字', visual: '本录像已完成的举手、低头画面观察' }[key] ?? '')).filter(Boolean).join('；')}。未提供或未纳入的材料不作推断。</p> : null}
    <nav className="report-dimension-nav" aria-label="六维分析导航">
      <div role="tablist" aria-label="选择分析维度" aria-orientation="vertical">
        {report.dimensions.map((item, index) => <button type="button" role="tab" key={item.dimension_code}
          id={`${panelId}-${item.dimension_code}`} aria-controls={panelId} aria-selected={item === dimension}
          tabIndex={item === dimension ? 0 : -1} ref={(node) => { if (node) buttons.current.set(index, node); else buttons.current.delete(index); }}
          onClick={() => setSelected(item.dimension_code)} onKeyDown={(event) => {
            const delta = event.key === 'ArrowDown' || event.key === 'ArrowRight' ? 1 : event.key === 'ArrowUp' || event.key === 'ArrowLeft' ? -1 : 0;
            if (!delta && event.key !== 'Home' && event.key !== 'End') return;
            event.preventDefault();
            const next = event.key === 'Home' ? 0 : event.key === 'End' ? report.dimensions.length - 1 : (index + delta + report.dimensions.length) % report.dimensions.length;
            const target = report.dimensions[next];
            if (target) { setSelected(target.dimension_code); buttons.current.get(next)?.focus(); }
          }}>
          <strong>{dimensionLabel(item.dimension_code)}</strong>
          <span>{dimensionHelp[item.dimension_code]}</span>
          <BadgeTag badge={coverageStatusBadge(item.coverage_status)} />
        </button>)}
      </div>
    </nav>
    <section className="report-dimension-body" role="tabpanel" id={panelId} aria-labelledby={`${panelId}-${dimension.dimension_code}`} tabIndex={0}>
      <h3>{dimensionLabel(dimension.dimension_code)}</h3>
      <p className="muted">{dimensionHelp[dimension.dimension_code]}</p>
      {dimension.coverage_status === 'observed' ? <>
        <h4>{dimension.dimension_code === 'pace' ? '课堂分析 · 教师讲解思路与推进' : '课堂分析'}</h4>
        <p className="report-prose">{dimension.summary || '本维度没有摘要，请核对下方课堂观察。'}</p>
        <CitedEvidence ids={dimension.summary_evidence_ids} evidenceById={evidenceById} onSelect={onSelect} />
      </> : <p className="notice-strip">{coverageStatusBadge(dimension.coverage_status).label}：{dimension.limitation}</p>}
      {dimension.coverage_status === 'observed' && dimension.limitation ? <p className="notice-strip">分析范围说明：{dimension.limitation}</p> : null}
      <p className="muted small">本维度依据覆盖：{formatIntervals(dimension.coverage)}</p>
      {observations.length === 0 ? <p className="muted">本维度没有另列观察或修改建议。</p> : <ul className="observation-list">
        {observations.map((observation) => <li key={observation.id} className="observation">
          <div className="observation-head"><BadgeTag badge={observationTypeBadge(observation.observation_type)} /><BadgeTag badge={reviewStatusBadge(observation.review_status)} /></div>
          <h4>{observation.observation_type === 'issue' ? '存在的问题与原因' : observation.observation_type === 'highlight' ? '值得保留的做法与原因' : '课堂观察与分析'}</h4>
          <p className="report-prose">{observation.observation_text}</p>
          {observation.suggestion ? <><h4>{observation.observation_type === 'highlight' ? '延续与应用建议' : '修改方向与实施建议'}</h4><p className="report-prose">{observation.suggestion}</p></> : null}
          <CitedEvidence ids={observation.evidence_ids} evidenceById={evidenceById} onSelect={onSelect} />
          <TechDetails><p>观察记录：{observation.id}</p></TechDetails>
        </li>)}
      </ul>}
    </section>
  </div>;
}
