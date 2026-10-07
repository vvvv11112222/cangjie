import type { ReactElement } from 'react';
import type { Evidence } from '../types';
import { useReportData } from '../data/hooks';
import type { ReportView } from '../data/viewModel';
import {
  coverageStatusBadge,
  dimensionLabel,
  formatDateTime,
  formatIntervals,
  observationTypeBadge,
  reportActionLabel,
  reportStatusBadge,
  reviewStatusBadge,
  shortId,
  truncateHash,
} from '../format';
import { BadgeTag, Card, DataSourceNote, DisclosureList, Field, IdChip, LoadControls, LoadStateNote, Pill } from '../components/ui';

function CitedEvidence({
  ids,
  evidenceById,
}: {
  ids: string[];
  evidenceById: Map<string, Evidence>;
}): ReactElement {
  if (ids.length === 0) {
    return <span className="muted">空摘要引用（无证据支撑）</span>;
  }
  return (
    <span className="pill-row">
      {ids.map((id) => {
        const evidence = evidenceById.get(id) ?? null;
        return (
          <span
            key={id}
            className={`cite ${evidence === null ? 'cite-missing' : ''}`}
            title={evidence === null ? '样例中找不到该证据' : evidence.description}
          >
            {evidence === null ? `缺证据 ${shortId(id)}` : `${shortId(id)} · ${evidence.description}`}
          </span>
        );
      })}
    </span>
  );
}

export function ReportPage({ view: injectedView }: { view?: ReportView } = {}): ReactElement {
  const loaded = useReportData(injectedView === undefined);
  const view = injectedView ?? loaded.data.view;
  const origin = loaded.origin;
  const { report, session, run, evidenceById, disclosures } = view;
  const published = session !== null && session.current_report_id === report.id;

  // 数据没准备好（接口模式加载中/失败）时不渲染明细：避免用空报告假装"确实没有内容"。
  const ready = injectedView !== undefined || loaded.hasData;
  const head = (
    <header className="page-head">
      <div>
        <p className="eyebrow">教学观察报告</p>
        <h1>报告</h1>
        <p className="muted">
          六维固定齐全，事实性摘要必须引用同批次证据；证据不足的维度保留原因，不生成结论。
        </p>
      </div>
      {ready ? <BadgeTag badge={reportStatusBadge(report.status)} /> : null}
    </header>
  );
  const notes = (
    <>
      <DataSourceNote sources={origin.sources} label={origin.label} note={origin.note} />
      <LoadStateNote status={loaded.status} error={loaded.error} />
      <LoadControls actionLabel={loaded.actionLabel} loadMore={loaded.loadMore} />
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

      <Card title="报告状态" subtitle="版本与内容摘要用于并发检查和复核追溯。">
        <div className="field-grid">
          <Field label="报告 ID">
            <IdChip id={report.id} />
          </Field>
          <Field label="状态">
            <BadgeTag badge={reportStatusBadge(report.status)} />
          </Field>
          <Field label="报告版本 / 锁定版本">
            v{report.revision} · lock v{report.lock_version}
          </Field>
          <Field label="当前发布版">
            {published ? (
              <Pill label="是" tone="green" />
            ) : (
              <span className="muted">否（课堂 current_report_id 为 null）</span>
            )}
          </Field>
          <Field label="内容摘要 content_sha256">
            <span className="mono" title={report.content_sha256 ?? 'null'}>
              {truncateHash(report.content_sha256)}
            </span>
          </Field>
          <Field label="整篇确认摘要 reviewed_content_sha256">
            <span className="mono" title={report.reviewed_content_sha256 ?? 'null'}>
              {truncateHash(report.reviewed_content_sha256)}
            </span>
          </Field>
          <Field label="复核人 / 时间">
            {report.reviewed_by === null
              ? 'null'
              : `${shortId(report.reviewed_by)} · ${report.reviewed_at === null ? 'null' : formatDateTime(report.reviewed_at)}`}
          </Field>
          <Field label="关联课堂 / 批次">
            <IdChip id={report.session_id} label="课堂" /> <IdChip id={report.run_id} label="批次" />
          </Field>
          <Field label="allowed_actions">
            {report.allowed_actions.length === 0
              ? '无（只读）'
              : report.allowed_actions.map(reportActionLabel).join('、')}
          </Field>
        </div>
      </Card>

      <Card title="总摘要" subtitle="总摘要中的事实也须引用证据。">
        <p className="summary-text">{report.summary === '' ? '（空）' : report.summary}</p>
        <div className="cite-row">
          <span className="field-label">引用证据</span>
          <CitedEvidence ids={report.summary_evidence_ids} evidenceById={evidenceById} />
        </div>
      </Card>

      <Card title="六维观察" subtitle={`固定 6 个维度，当前 ${report.dimensions.length} 个`}>
        <div className="dimension-grid">
          {report.dimensions.map((dimension) => (
            <article key={dimension.dimension_code} className="dimension">
              <header className="dimension-head">
                <h3>{dimensionLabel(dimension.dimension_code)}</h3>
                <BadgeTag badge={coverageStatusBadge(dimension.coverage_status)} />
              </header>
              {dimension.coverage_status === 'observed' ? (
                <p className="dimension-summary">{dimension.summary === '' ? '（空摘要）' : dimension.summary}</p>
              ) : (
                <p className="dimension-limit">限制：{dimension.limitation}</p>
              )}
              <div className="dimension-meta">
                <span className="field-label">覆盖区间</span>
                <span className="mono small">{formatIntervals(dimension.coverage)}</span>
              </div>
              <div className="dimension-meta">
                <span className="field-label">摘要证据</span>
                <CitedEvidence ids={dimension.summary_evidence_ids} evidenceById={evidenceById} />
              </div>
            </article>
          ))}
        </div>
      </Card>

      <Card title="观察条目" subtitle="复核状态由后端维护，前端不写入 review_status 等只读字段。">
        {report.observations.length === 0 ? (
          <p className="empty">observations 为空数组。</p>
        ) : (
          <ul className="observation-list">
            {report.observations.map((observation) => (
              <li key={observation.id} className="observation">
                <div className="observation-head">
                  <BadgeTag badge={observationTypeBadge(observation.observation_type)} />
                  <Pill label={dimensionLabel(observation.dimension_code)} tone="blue" />
                  <BadgeTag badge={reviewStatusBadge(observation.review_status)} />
                  <IdChip id={observation.id} />
                </div>
                <p className="observation-text">{observation.observation_text}</p>
                {observation.suggestion === '' ? null : (
                  <p className="observation-suggestion">建议：{observation.suggestion}</p>
                )}
                <div className="cite-row">
                  <span className="field-label">证据</span>
                  <CitedEvidence ids={observation.evidence_ids} evidenceById={evidenceById} />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card title="溯源与限制" subtitle="记录模型版本、提示词摘要、输入清单摘要、覆盖和来源。">
        {report.provenance === null ? (
          <p className="empty">provenance 为 null；旧版无法补证的字段应保持 null 并标记为历史记录。</p>
        ) : (
          <div className="field-grid">
            <Field label="模型">
              {report.provenance.model.name} · {report.provenance.model.revision}
            </Field>
            <Field label="提示词版本">
              {report.provenance.prompt_version}（{truncateHash(report.provenance.prompt_sha256)}）
            </Field>
            <Field label="输入清单摘要">
              <span className="mono">{truncateHash(report.provenance.manifest_sha256)}</span>
            </Field>
            <Field label="输入内容摘要">
              <span className="mono">{truncateHash(report.provenance.input_sha256)}</span>
            </Field>
            <Field label="输入转写修订">
              {report.provenance.transcript_revision_id === null ? (
                <span className="muted">null</span>
              ) : (
                <IdChip id={report.provenance.transcript_revision_id} />
              )}
            </Field>
            <Field label="媒体资产">
              <IdChip id={report.provenance.media_asset_id} />
            </Field>
            <Field label="模型输入覆盖">
              <span className="mono">{formatIntervals(report.provenance.coverage)}</span>
            </Field>
            <Field label="来源">
              {report.provenance.source.title} · {report.provenance.source.attribution} · rights v
              {report.provenance.source.rights_version}
            </Field>
            <Field label="来源链接">
              {report.provenance.source.source_url === null ? (
                <span className="muted">null</span>
              ) : (
                <a href={report.provenance.source.source_url}>{report.provenance.source.source_url}</a>
              )}
            </Field>
            <Field label="溯源限制">
              {report.provenance.limitations.length === 0
                ? '无'
                : report.provenance.limitations.join('；')}
            </Field>
            <Field label="批次样例">
              {run === null ? '报告 run_id 没有对应批次数据' : `run.status=${run.status}`}
            </Field>
          </div>
        )}
      </Card>

      <Card title="样例数据核对" subtitle="引用不一致必须显示，不能拼成一条看似完整但实际不存在的链路。">
        <DisclosureList items={disclosures} />
      </Card>
    </div>
  );
}
