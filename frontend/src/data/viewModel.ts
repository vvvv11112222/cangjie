/**
 * 把数据访问层取回的数据整理成页面视图模型，并显式计算数据之间的引用一致性。
 *
 * 样例是彼此独立的合成场景，ID 不一定互相衔接；接口数据也可能缺少某些资源
 * （例如后端尚未实现对应端点）。两种情况都必须如实显示出来，
 * 不能悄悄拼成一条看似完整的业务链路。
 *
 * 默认参数仍然是固定样例，且措辞与 M0 完全一致，保证离线渲染和既有测试不变。
 */

import type { Evidence, Report, Results, Revision, Run, Session, SessionPage, Segment } from '../types';
import type { DataSourceKind } from './sources';
import { report, results, revision, run, sessionPage } from './samples';

/**
 * 文案里指代数据来源的词。固定样例沿用 M0 的措辞（"样例"），
 * 接口模式换成"接口数据"，避免把真实数据说成样例。
 */
export interface DataSourceWording {
  /** 来源标签，用于 DataSourceNote，例如"固定样例""接口数据"。 */
  label: string;
  /** 句子中指代数据的词，例如"样例""接口数据"。 */
  noun: string;
}

export const FIXTURE_WORDING: DataSourceWording = { label: '固定样例', noun: '样例' };
export const API_WORDING: DataSourceWording = { label: '接口数据', noun: '接口数据' };

export function wordingFor(kind: DataSourceKind): DataSourceWording {
  return kind === 'api' ? API_WORDING : FIXTURE_WORDING;
}

export interface Disclosure {
  tone: 'info' | 'warn';
  text: string;
}

export interface StageSummary {
  total: number;
  succeeded: number;
  failed: number;
  skipped: number;
  pending: number;
  active: boolean;
}

export interface SessionTaskView {
  session: Session;
  run: Run | null;
  stages: StageSummary | null;
  disclosures: Disclosure[];
}

export interface TranscriptView {
  results: Results;
  revision: Revision | null;
  run: Run | null;
  segments: Segment[];
  evidence: Evidence[];
  disclosures: Disclosure[];
}

export interface ReportView {
  report: Report;
  session: Session | null;
  run: Run | null;
  evidenceById: Map<string, Evidence>;
  unresolvedEvidenceIds: string[];
  disclosures: Disclosure[];
}

/** buildSessionTasks 的输入：课堂列表 + 按批次 id 索引的批次数据。 */
export interface SessionTasksInput {
  sessionPage: SessionPage;
  /** 值为 null 表示该批次读取失败（例如接口未实现或返回错误）。 */
  runs: Map<string, Run | null>;
  kind: DataSourceKind;
  /** 已取到、但尚未成为当前发布版的报告，用来解释 current_report_id 为 null；没有就传 null。 */
  draftReport?: Report | null;
  /** 批次读取失败的原因，按批次 id 索引，用于把可读提示写进核对区。 */
  runErrors?: Map<string, string>;
}

export interface TranscriptInput {
  results: Results;
  revision: Revision | null;
  run: Run | null;
  kind: DataSourceKind;
}

export interface ReportInput {
  report: Report;
  results: Results;
  sessionPage: SessionPage;
  run: Run | null;
  /** 结果引用的转写修订；接口模式下取不到就传 null。 */
  revision: Revision | null;
  kind: DataSourceKind;
}

function summarizeStages(run: Run): StageSummary {
  const byStatus = (status: Run['jobs'][number]['status']) =>
    run.jobs.filter((job) => job.status === status).length;
  return {
    total: run.stage_plan.length,
    succeeded: byStatus('succeeded'),
    failed: byStatus('failed'),
    skipped: byStatus('skipped'),
    pending: byStatus('queued') + byStatus('running'),
    active: run.status === 'queued' || run.status === 'running',
  };
}

function indexEvidence(evidence: Evidence[]): Map<string, Evidence> {
  return new Map(evidence.map((item) => [item.id, item]));
}

function defaultSessionTasksInput(): SessionTasksInput {
  return {
    sessionPage,
    runs: new Map([[run.id, run]]),
    kind: 'fixtures',
    draftReport: report,
  };
}

export function buildSessionTasks(input: SessionTasksInput = defaultSessionTasksInput()): SessionTaskView[] {
  const { kind } = input;
  const wording = wordingFor(kind);
  return input.sessionPage.items.map((session) => {
    const disclosures: Disclosure[] = [];
    let activeRun: Run | null = null;

    const latestRunId = session.latest_run_id;
    if (latestRunId !== null && input.runs.has(latestRunId)) {
      const linkedRun = input.runs.get(latestRunId) ?? null;
      if (linkedRun === null) {
        const reason = input.runErrors?.get(latestRunId) ?? null;
        disclosures.push({
          tone: 'warn',
          text:
            reason === null
              ? `课堂的最新批次 ${latestRunId} 读取失败，暂时显示不出批次详情。`
              : `课堂的最新批次 ${latestRunId} 读取失败：${reason}`,
        });
      } else {
        activeRun = linkedRun;
      }
      if (linkedRun !== null && linkedRun.session_id !== session.id) {
        disclosures.push({
          tone: 'warn',
          text: `批次 ${linkedRun.id} 的 session_id 与课堂不一致，${wording.noun}不可当作同一课堂结果。`,
        });
      }
      if (
        linkedRun !== null &&
        session.primary_media_asset_id !== null &&
        linkedRun.media_asset_id !== session.primary_media_asset_id
      ) {
        disclosures.push({
          tone: 'warn',
          text: '批次使用的媒体与课堂当前主媒体不是同一资产，接入后端后须逐次复核。',
        });
      }
    } else if (session.latest_run_id !== null) {
      disclosures.push({
        tone: 'warn',
        text: `课堂的最新批次 ${session.latest_run_id} 没有对应的${wording.label}。`,
      });
    }

    if (session.current_report_id === null) {
      // 样例模式下能拿固定样例里的草稿解释 current_report_id；接口模式下只有拿到过报告才知道，
      // 所以接口模式传 draftReport = null，只如实说明"没有当前发布版"。
      const draft = input.draftReport ?? null;
      disclosures.push({
        tone: 'info',
        text: draft !== null && draft.session_id === session.id
          ? `报告 ${draft.id} 为 ${draft.status}，未发布，因此课堂 current_report_id 为 null。`
          : '课堂没有当前发布版报告，current_report_id 为 null。',
      });
    }

    return { session, run: activeRun, stages: activeRun ? summarizeStages(activeRun) : null, disclosures };
  });
}

function defaultTranscriptInput(): TranscriptInput {
  return { results, revision, run, kind: 'fixtures' };
}

export function buildTranscriptView(input: TranscriptInput = defaultTranscriptInput()): TranscriptView {
  const { results: currentResults, revision: currentRevision, run: currentRun, kind } = input;
  const wording = wordingFor(kind);
  const disclosures: Disclosure[] = [];

  if (currentRun !== null && currentResults.run_status !== currentRun.status) {
    disclosures.push({
      tone: 'warn',
      text:
        kind === 'fixtures'
          ? `状态不一致：results.run_status=${currentResults.run_status}，run-partial.status=${currentRun.status}；两份样例是独立合成场景，不能当作同一次运行。`
          : `状态不一致：results.run_status=${currentResults.run_status}，批次状态=${currentRun.status}；两个响应须由后端确认后修复。`,
    });
  }

  const revisionId = currentResults.transcript_revision_id;
  if (currentRevision === null) {
    disclosures.push({
      tone: revisionId === null ? 'info' : 'warn',
      text:
        revisionId === null
          ? '本次结果没有引用转写修订。'
          : `结果引用的转写修订 ${revisionId} 读取失败，页面无法显示修订内容。`,
    });
  } else if (revisionId === currentRevision.id) {
    disclosures.push({ tone: 'info', text: `结果为修订 ${currentRevision.id}（第 ${currentRevision.revision_no} 版）的内容。` });
  } else if (revisionId !== null && currentRevision.parent_revision_id === revisionId) {
    disclosures.push({
      tone: 'info',
      text: `结果引用第 ${revisionId} 版；下面的第 ${currentRevision.revision_no} 版由它派生，例句文本尚未变化。`,
    });
  } else {
    disclosures.push({
      tone: 'warn',
      text: `结果引用的转写修订在${wording.noun}中没有对应记录。`,
    });
  }

  if (currentResults.evidence.length === 0) {
    disclosures.push({
      tone: 'info',
      text: `证据为空数组；须结合 run_status=${currentResults.run_status} 与批次状态区分“等待生成”和“确实无结果”。`,
    });
  }
  if (currentResults.events.length === 0) {
    disclosures.push({ tone: 'info', text: 'P0 行为事件恒为空数组，行为识别属于 P1。' });
  }
  if (currentResults.limitations.length > 0) {
    disclosures.push({ tone: 'warn', text: `本次结果自述限制：${currentResults.limitations.join('；')}` });
  }

  return {
    results: currentResults,
    // 结果与修订的引用关系在 disclosures 中显式说明，不在这里做合并推断。
    revision: currentRevision,
    run: currentRun,
    segments: currentResults.segments,
    evidence: currentResults.evidence,
    disclosures,
  };
}

function defaultReportInput(): ReportInput {
  return { report, results, sessionPage, run, revision, kind: 'fixtures' };
}

export function buildReportView(input: ReportInput = defaultReportInput()): ReportView {
  const {
    report: currentReport,
    results: currentResults,
    sessionPage: currentSessionPage,
    run: currentRun,
    revision: currentRevision,
    kind,
  } = input;
  const wording = wordingFor(kind);
  const session = currentSessionPage.items.find((item) => item.id === currentReport.session_id) ?? null;
  const evidenceById = indexEvidence(currentResults.evidence);
  const cited = new Set<string>([
    ...currentReport.summary_evidence_ids,
    ...currentReport.dimensions.flatMap((dimension) => dimension.summary_evidence_ids),
    ...currentReport.observations.flatMap((observation) => observation.evidence_ids),
  ]);
  const unresolvedEvidenceIds = [...cited].filter((id) => !evidenceById.has(id));
  const disclosures: Disclosure[] = [];

  if (session === null) {
    disclosures.push({ tone: 'warn', text: `报告引用的课堂在课堂列表${wording.noun}中不存在。` });
  } else if (session.current_report_id === null) {
    disclosures.push({
      tone: 'info',
      text: `报告状态为 ${currentReport.status}，课堂 current_report_id 仍为 null；只有发布后的当前版才会写入该字段。`,
    });
  }

  if (currentRun === null) {
    disclosures.push({
      tone: 'info',
      text: `没有取到报告 ${currentReport.run_id} 对应的批次数据，无法核对批次状态。`,
    });
  } else if (currentReport.run_id !== currentRun.id) {
    disclosures.push({
      tone: 'warn',
      text: `报告的 run_id=${currentReport.run_id} 与分析批次${wording.noun} ${currentRun.id} 不同。`,
    });
  }

  const knownRevisions = new Set(
    [
      currentResults.transcript_revision_id,
      currentRevision?.id ?? null,
      currentRevision?.parent_revision_id ?? null,
    ].filter((value): value is string => value !== null),
  );
  const provenanceRevision = currentReport.provenance?.transcript_revision_id ?? null;
  if (provenanceRevision !== null && !knownRevisions.has(provenanceRevision)) {
    disclosures.push({
      tone: 'warn',
      text: `报告 provenance 引用的转写修订 ${provenanceRevision} 没有对应${wording.noun}，证据链需在后端联调时核对。`,
    });
  }

  if (unresolvedEvidenceIds.length > 0) {
    disclosures.push({
      tone: 'warn',
      text: `有 ${unresolvedEvidenceIds.length} 个引用证据在${wording.noun}中找不到：${unresolvedEvidenceIds.join('、')}。`,
    });
  }

  if (currentReport.dimensions.length !== 6) {
    disclosures.push({
      tone: 'warn',
      text: `六维报告应固定 6 个维度，当前为 ${currentReport.dimensions.length} 个。`,
    });
  }

  const notObserved = currentReport.dimensions.filter((item) => item.coverage_status !== 'observed');
  if (notObserved.length > 0) {
    disclosures.push({
      tone: 'info',
      text: `${notObserved.length} 个维度标记为证据不足或不适用，界面保留原因，不生成结论。`,
    });
  }

  return {
    report: currentReport,
    session,
    run: currentRun !== null && currentReport.run_id === currentRun.id ? currentRun : null,
    evidenceById,
    unresolvedEvidenceIds,
    disclosures,
  };
}
