import type { ReactElement } from 'react';
import { useEffect,useMemo,useRef,useState } from 'react';
import { ResultContext } from '../components/ResultContext';
import { BadgeTag,Card,LoadControls,LoadStateNote,TechDetails } from '../components/ui';
import { describeError } from '../data/errors';
import { useReportData } from '../data/hooks';
import { sameId } from '../data/ids';
import { loadReportContext } from '../data/loaders';
import { dataSource,isApiMode } from '../data/sources';
import type { ReportView } from '../data/viewModel';
import {
reportStatusBadge
} from '../format';
import type { Evidence } from '../types';
import { CitedEvidence,ReportAnalysis,ReportEvidence,reportLimitation } from './ReportAnalysis';
import { ReportWorkflowPanel } from './ReportWorkflowPanel';

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
  const { evidenceById: contextEvidence } = context ?? { run: null, evidenceById: new Map<string, Evidence>() };
  const evidenceById = useMemo(() => new Map([...contextEvidence].filter(([, evidence]) =>
    sameId(evidence.run_id, report.run_id) && sameId(evidence.session_id, report.session_id)
    && (report.provenance === null || sameId(evidence.media_asset_id, report.provenance.media_asset_id)))), [contextEvidence, report]);

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

        <h1>报告</h1>

      </div>
      {ready ? <BadgeTag badge={reportStatusBadge(report.status)} /> : null}
    </header>
  );
  const notes = (
    <>

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
            {isApiMode() && session !== null ? <ReportWorkflowPanel session={session} initial={report}
        evidenceById={evidenceById} onSelectEvidence={selectEvidence}
        contextReady={context !== null} onChange={setReport}
        onPublicationChanged={(id) => { setCurrentReportId(id); onPublicationChanged?.(); }} /> : null}

      {context === null ? <Card title="报告证据加载">
        <p className={contextError === null ? 'notice-strip' : 'notice-strip load-note-error'}>{contextError ?? '正在加载所选报告的批次和证据…'}</p>
        {contextError === null ? null : <button type="button" className="btn btn-secondary" onClick={() => setContextRetry((value) => value + 1)}>重新加载报告证据</button>}
      </Card> : <>

      <Card title="课堂概览" >

        {report.provenance?.limitations.length ? <TechDetails summary="查看分析范围与限制"><p>{report.provenance.limitations.map(reportLimitation).join('；')}</p></TechDetails> : null}
        <p className="summary-text">{report.summary === '' ? '（空）' : report.summary}</p>
        <div className="cite-row">
          <span className="field-label">引用证据</span>
          <CitedEvidence ids={report.summary_evidence_ids} evidenceById={evidenceById} onSelect={selectEvidence} />
        </div>
      </Card>

      <Card title="六维分析" >
        <ReportAnalysis key={report.id} report={report} evidenceById={evidenceById} onSelect={selectEvidence} />
      </Card>

      {activeEvidence === null ? null : <div ref={evidencePanel} tabIndex={-1} role="region" aria-label="所选课堂依据">
        <ReportEvidence key={activeEvidence.id} evidence={activeEvidence} onClose={() => { setSelectedEvidence(null); evidenceTrigger.current?.focus(); }} />
      </div>}


      </>}
    </div>
  );
}
