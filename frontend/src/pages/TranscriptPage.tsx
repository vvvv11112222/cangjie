import type { ReactElement } from 'react';
import type { Evidence, Segment } from '../types';
import { useTranscriptData } from '../data/hooks';
import type { TranscriptView } from '../data/viewModel';
import {
  evidenceAvailabilityBadge,
  evidenceKindLabel,
  formatDateTime,
  formatInterval,
  formatTimestamp,
  runStatusBadge,
  shortId,
  sourceTypeBadge,
  speakerLabel,
  truncateHash,
} from '../format';
import { BadgeTag, Card, DataSourceNote, DisclosureList, Field, IdChip, LoadStateNote } from '../components/ui';

/** 证据与片段的业务关联在后端建立；样例用 provenance.source_segment_no 表达。 */
function evidenceForSegment(evidence: Evidence[], segment: Segment): Evidence[] {
  return evidence.filter((item) => item.provenance['source_segment_no'] === segment.segment_no);
}

export function TranscriptPage({ view: injectedView }: { view?: TranscriptView } = {}): ReactElement {
  const loaded = useTranscriptData(injectedView === undefined);
  const view = injectedView ?? loaded.data.view;
  const origin = loaded.origin;
  const { results, revision, run, segments, evidence, disclosures } = view;

  // 数据没准备好（接口模式加载中/失败）时不渲染明细：避免把空数组当成"确实没有转写"。
  const ready = injectedView !== undefined || loaded.status === 'ready';
  const head = (
    <header className="page-head">
      <div>
        <p className="eyebrow">转写与证据</p>
        <h1>转写</h1>
        <p className="muted">
          片段起止时间为原录像起点的整数毫秒；speaker_label 为 unknown 表示未知说话人，不是空值。
        </p>
      </div>
      {ready ? <BadgeTag badge={runStatusBadge(results.run_status)} /> : null}
    </header>
  );
  const notes = (
    <>
      <DataSourceNote sources={origin.sources} label={origin.label} note={origin.note} />
      <LoadStateNote status={loaded.status} error={loaded.error} />
      {loaded.data.selection === null ? null : <p className="load-note">{loaded.data.selection}</p>}
    </>
  );

  if (!ready) {
    return (
      <div className="page">
        {head}
        {notes}
      </div>
    );
  }

  return (
    <div className="page">
      {head}
      {notes}

      <Card
        title="结果摘要"
        subtitle="空数组是否因为尚未生成，必须结合批次状态判断，不能仅凭数组为空下结论。"
      >
        <div className="field-grid">
          <Field label="run_status">
            <BadgeTag badge={runStatusBadge(results.run_status)} />
          </Field>
          <Field label="transcript_revision_id">
            {results.transcript_revision_id === null ? (
              <span className="muted">null</span>
            ) : (
              <IdChip id={results.transcript_revision_id} />
            )}
          </Field>
          <Field label="segments / evidence">
            {segments.length} 段 · {evidence.length} 条证据
          </Field>
          <Field label="frames / events">
            {results.frames.length} 帧 · {results.events.length} 事件
          </Field>
          <Field label="批次样例状态">
            {run === null ? '无对应批次样例' : `run-partial.status=${run.status}（${run.status === results.run_status ? '与结果一致' : '与结果不一致'}）`}
          </Field>
        </div>
        {results.limitations.length > 0 ? (
          <p className="callout callout-amber">结果限制：{results.limitations.join('；')}</p>
        ) : null}
      </Card>

      {revision === null ? null : (
        <Card
          title={`转写修订第 ${revision.revision_no} 版`}
          subtitle="修订保留父版本、操作者、时间与原因；旧报告继续引用旧证据。"
          actions={<BadgeTag badge={sourceTypeBadge(revision.source_type)} />}
        >
          <div className="field-grid">
            <Field label="修订 ID">
              <IdChip id={revision.id} />
            </Field>
            <Field label="父修订">
              {revision.parent_revision_id === null ? (
                <span className="muted">null</span>
              ) : (
                <IdChip id={revision.parent_revision_id} />
              )}
            </Field>
            <Field label="来源批次">
              <IdChip id={revision.source_run_id} />
            </Field>
            <Field label="媒体资产">
              <IdChip id={revision.media_asset_id} />
            </Field>
            <Field label="创建时间">{formatDateTime(revision.created_at)}</Field>
            <Field label="操作者">
              <IdChip id={revision.created_by} />
            </Field>
            <Field label="修订原因">{revision.reason}</Field>
            <Field label="转写锁版本">v{revision.transcript_lock_version}</Field>
            <Field label="内容摘要">
              <span className="mono" title={revision.content_sha256}>
                {truncateHash(revision.content_sha256)}
              </span>
            </Field>
          </div>
        </Card>
      )}

      <Card title="转写片段" subtitle={`${segments.length} 段，按 segment_no 升序`}>
        {segments.length === 0 ? (
          <p className="empty">
            segments 为空数组。当前 run_status={results.run_status}，须区分“尚未生成”与“确实无语音”。
          </p>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>#</th>
                  <th>时间</th>
                  <th>说话人</th>
                  <th>文本</th>
                  <th>关联证据</th>
                </tr>
              </thead>
              <tbody>
                {segments.map((segment) => {
                  const linked = evidenceForSegment(evidence, segment);
                  return (
                    <tr key={segment.segment_no}>
                      <td className="mono nowrap">{segment.segment_no}</td>
                      <td className="mono nowrap">
                        {formatTimestamp(segment.start_ms)}
                        <br />
                        <span className="muted">{formatTimestamp(segment.end_ms)}</span>
                      </td>
                      <td className="nowrap">{speakerLabel(segment.speaker_label)}</td>
                      <td className="text-cell">{segment.text_content}</td>
                      <td>
                        {linked.length === 0 ? (
                          <span className="muted">无</span>
                        ) : (
                          linked.map((item) => <IdChip key={item.id} id={item.id} label="证据" />)
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card title="证据" subtitle="证据必须归属同一课堂和同一批次，不能跨课堂引用。">
        {evidence.length === 0 ? (
          <p className="empty">evidence 为空数组，报告与观察将没有可引用依据。</p>
        ) : (
          <ul className="evidence-list">
            {evidence.map((item) => (
              <li key={item.id} className="evidence-item">
                <div className="evidence-head">
                  <IdChip id={item.id} />
                  <span className="pill pill-blue">{evidenceKindLabel(item.kind)}</span>
                  <BadgeTag badge={evidenceAvailabilityBadge(item.availability)} />
                  <span className="muted mono small">{formatInterval({ start_ms: item.start_ms, end_ms: item.end_ms })}</span>
                </div>
                <p className="evidence-text">{item.description}</p>
                <div className="cell-meta mono tiny">
                  <span title={item.run_id}>批次 {shortId(item.run_id)}</span>
                  <span title={item.session_id}>课堂 {shortId(item.session_id)}</span>
                  <span title={item.media_asset_id}>媒体 {shortId(item.media_asset_id)}</span>
                  {item.provenance['source_segment_no'] === undefined ? null : (
                    <span>来源片段 #{String(item.provenance['source_segment_no'])}</span>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card title="样例数据核对" subtitle="以下差异属于样例本身，接入后端后应由服务端状态替代。">
        <DisclosureList items={disclosures} />
      </Card>
    </div>
  );
}
