import { ResultContext } from '../components/ResultContext';
import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import type { Evidence } from '../types';
import { useReportData } from '../data/hooks';
import type { ReportView } from '../data/viewModel';
import {
  formatDateTime,
  formatIntervals,
  reportActionLabel,
  reportStatusBadge,
} from '../format';
import { BadgeTag, Card, DataSourceNote, DisclosureList, Field, LoadControls, LoadStateNote, Pill, TechDetails } from '../components/ui';
import { dataSource, isApiMode } from '../data/sources';
import { loadReportContext } from '../data/loaders';
import { describeError } from '../data/errors';
import { sameId } from '../data/ids';
import { ReportWorkflowPanel } from './ReportWorkflowPanel';
import { CitedEvidence, ReportAnalysis, ReportEvidence, reportLimitation } from './ReportAnalysis';

export function ReportPage({
  view: injectedView,
  embedded = false,
  onPublicationChanged,
}: {
  view?: ReportView;
  /** 嵌入课堂详情页时隐藏页头与数据来源说明，只保留正文卡片。 */
  embedded?: boolean;
  onPublicationChanged?(): void;
} = {}): ReactElement {
  const loaded = useReportData(injectedView === undefined);
  const view = injectedView ?? loaded.data.view;
  const origin = loaded.origin;
  const { report: incomingReport, session } = view;
  const [report, setReport] = useState(incomingReport);
  const [currentReportId, setCurrentReportId] = useState(session?.current_report_id ?? null);
  useEffect(() => setReport(incomingReport), [incomingReport]);
  useEffect(() => setCurrentReportId(session?.current_report_id ?? null), [session?.current_report_id]);
  const [selectedContext, setSelectedContext] = useState<{ report: typeof report; currentId: string | null; view: ReportView } | null>(null);
  const [contextError, setContextError] = useState<string | null>(null);
  const [contextRetry, setContextRetry] = useState(0);
  const usesIncomingContext = report === incomingReport && currentReportId === (session?.current_report_id ?? null);
  useEffect(() => {
    if (!isApiMode() || session === null || usesIncomingContext) return;
    let active = true;
    setSelectedContext(null); setContextError(null);
    void loadReportContext(dataSource, report, { ...session, current_report_id: currentReportId })
      .then((next) => { if (active) setSelectedContext({ report, currentId: currentReportId, view: next }); })
      .catch((error: unknown) => { if (active) setContextError(describeError(error)); });
    return () => { active = false; };
  }, [report, session, currentReportId, usesIncomingContext, contextRetry]);
  const context = !isApiMode() || usesIncomingContext ? view
    : selectedContext?.report === report && selectedContext.currentId === currentReportId ? selectedContext.view : null;
  const { run, evidenceById: contextEvidence, disclosures } = context ?? { run: null, evidenceById: new Map<string, Evidence>(), disclosures: [] };
  const evidenceById = useMemo(() => new Map([...contextEvidence].filter(([, evidence]) =>
    sameId(evidence.run_id, report.run_id) && sameId(evidence.session_id, report.session_id)
    && (report.provenance === null || sameId(evidence.media_asset_id, report.provenance.media_asset_id)))), [contextEvidence, report]);
  const published = currentReportId === report.id;
  const [selectedEvidence, setSelectedEvidence] = useState<{ report: typeof report; context: ReportView | null; evidence: Evidence } | null>(null);
  const evidencePanel = useRef<HTMLDivElement>(null);
  const evidenceTrigger = useRef<HTMLElement | null>(null);
  // A changed report/context invalidates the old selection immediately, before effects run.
  const activeEvidence = selectedEvidence?.report === report && selectedEvidence.context === context
    ? evidenceById.get(selectedEvidence.evidence.id) ?? null : null;
  function selectEvidence(evidence: Evidence): void {
    evidenceTrigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setSelectedEvidence({ report, context, evidence });
  }
  useEffect(() => { if (activeEvidence !== null) evidencePanel.current?.focus(); }, [activeEvidence, selectedEvidence]);

  // 数据没准备好（接口模式加载中/失败）时不渲染明细：避免用空报告假装"确实没有内容"。
  const ready = injectedView !== undefined || loaded.hasData;
  const head = (
    <header className="page-head">
      <div>
        <p className="eyebrow">教学观察报告</p>
        <h1>报告</h1>
        <p className="muted">
          先看课堂主线，再按维度查看课堂分析、问题和实施建议。每条依据都可核对原录像时间。
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
        {embedded ? null : head}
        {embedded ? null : notes}
      </div>
    );
  }

  return (
    <div className="page">
      {embedded ? null : head}
      {embedded ? null : notes}

      <ResultContext key={report.run_id} runId={report.run_id} mediaId={report.provenance?.media_asset_id} version={`报告第 ${report.revision} 版`}/>
      <Card title="报告状态" subtitle="AI 分析用于教学复盘；草稿经人工复核后才能发布。">
        <div className="field-grid">
          <Field label="报告版本">第 {report.revision} 版</Field>
          <Field label="状态">
            <BadgeTag badge={reportStatusBadge(report.status)} />
          </Field>
          <Field label="当前发布版">
            {published ? (
              <Pill label="是" tone="green" />
            ) : (
              <span className="muted">当前查看的不是发布版</span>
            )}
          </Field>
          <Field label="复核记录">
            {report.reviewed_by === null
              ? '尚未完成复核'
              : `已由授权复核者确认${report.reviewed_at === null ? '' : ` · ${formatDateTime(report.reviewed_at)}`}`}
          </Field>
          <Field label="可执行操作">
            {report.allowed_actions.length === 0
              ? '无（只读）'
              : report.allowed_actions.map(reportActionLabel).join('、')}
          </Field>
        </div>
        {/* 版本号与内容摘要属于并发校验用的字段，收进折叠区，不占业务视线。 */}
        <TechDetails>
          <ul className="tech-list">
            <li className="mono">report.id = {report.id}</li>
            <li className="mono">report.session_id = {report.session_id}</li>
            <li className="mono">report.run_id = {report.run_id}</li>
            <li className="mono">report.reviewed_by = {report.reviewed_by ?? 'null'}</li>
            <li className="mono">report.revision = v{report.revision}</li>
            <li className="mono">report.lock_version = v{report.lock_version}</li>
            <li className="mono">report.content_sha256 = {report.content_sha256 ?? 'null'}</li>
            <li className="mono">
              report.reviewed_content_sha256 = {report.reviewed_content_sha256 ?? 'null'}
            </li>
            <li className="mono">report.status = {report.status}</li>
            <li className="mono">
              report.allowed_actions = [{report.allowed_actions.join(', ')}]
            </li>
          </ul>
        </TechDetails>
      </Card>

      {isApiMode() && session !== null ? <ReportWorkflowPanel session={session} initial={report}
        contextReady={context !== null} onChange={setReport}
        onPublicationChanged={(id) => { setCurrentReportId(id); onPublicationChanged?.(); }} /> : null}

      {context === null ? <Card title="报告证据加载">
        <p className={contextError === null ? 'notice-strip' : 'notice-strip load-note-error'}>{contextError ?? '正在加载所选报告的批次和证据…'}</p>
        {contextError === null ? null : <button type="button" className="btn btn-secondary" onClick={() => setContextRetry((value) => value + 1)}>重新加载报告证据</button>}
      </Card> : <>

      <Card title="课堂概览" subtitle="概括本次可见的教学主线和最值得复盘的内容。">
        {report.provenance?.limitations.length ? <p className="notice-strip">分析范围说明：{report.provenance.limitations.map(reportLimitation).join('；')}</p> : null}
        <p className="summary-text">{report.summary === '' ? '（空）' : report.summary}</p>
        <div className="cite-row">
          <span className="field-label">引用证据</span>
          <CitedEvidence ids={report.summary_evidence_ids} evidenceById={evidenceById} onSelect={selectEvidence} />
        </div>
      </Card>

      <Card title="六维分析" subtitle="选择左侧维度，查看课堂分析与对应建议；重要内容直接展示。">
        <ReportAnalysis key={report.id} report={report} evidenceById={evidenceById} onSelect={selectEvidence} />
      </Card>

      {activeEvidence === null ? null : <div ref={evidencePanel} tabIndex={-1} role="region" aria-label="所选课堂依据">
        <ReportEvidence key={activeEvidence.id} evidence={activeEvidence} onClose={() => { setSelectedEvidence(null); evidenceTrigger.current?.focus(); }} />
      </div>}

      <Card title="溯源与限制" subtitle="记录模型版本、提示词摘要、输入清单摘要、覆盖和来源。">
        {report.provenance === null ? (
          <p className="empty">这份历史报告缺少完整的生成记录，无法补证，请结合原始课堂核对。</p>
        ) : (
          <>
            <div className="field-grid">
            <Field label="模型">
              {report.provenance.model.name} · {report.provenance.model.revision}
            </Field>
            <Field label="提示词版本">{report.provenance.prompt_version}</Field>
            <Field label="模型输入覆盖">
              <span className="mono">{formatIntervals(report.provenance.coverage)}</span>
            </Field>
            <Field label="来源">
              {report.provenance.source.title} · {report.provenance.source.attribution} · rights v
              {report.provenance.source.rights_version}
            </Field>
            <Field label="来源链接">
              {report.provenance.source.source_url === null ? (
                <span className="muted">未提供来源链接</span>
              ) : (
                <a href={report.provenance.source.source_url}>{report.provenance.source.source_url}</a>
              )}
            </Field>
            <Field label="溯源限制">
              {report.provenance.limitations.length === 0
                ? '无'
                : report.provenance.limitations.map(reportLimitation).join('；')}
            </Field>
            </div>
            {/* 各类 sha256 是追溯用的校验值，完整值放进折叠区，页面上只留模型与来源。 */}
            <TechDetails>
              <ul className="tech-list">
                <li className="mono">provenance.prompt_sha256 = {report.provenance.prompt_sha256}</li>
                <li className="mono">provenance.manifest_sha256 = {report.provenance.manifest_sha256}</li>
                <li className="mono">provenance.input_sha256 = {report.provenance.input_sha256}</li>
                <li className="mono">
                  provenance.transcript_revision_id = {report.provenance.transcript_revision_id ?? 'null'}
                </li>
                <li className="mono">provenance.media_asset_id = {report.provenance.media_asset_id}</li>
                <li className="mono">run.status = {run?.status ?? 'unavailable'}</li>
                <li className="mono">provenance.limitations = {JSON.stringify(report.provenance.limitations)}</li>
              </ul>
            </TechDetails>
          </>
        )}
      </Card>

      {/* 样例口径说明收进折叠区：引用不一致仍要显示，但不占业务视线（UI约定第 4 节）。 */}
      <TechDetails summary="样例数据核对">
        <DisclosureList items={disclosures} />
      </TechDetails>
      </>}
    </div>
  );
}
