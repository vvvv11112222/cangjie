/**
 * 把固定样例整理成页面视图模型，并显式计算样例之间的引用一致性。
 * 样例是彼此独立的合成场景，ID 不一定互相衔接；不一致必须显示出来，
 * 不能悄悄拼成一条看似完整的业务链路。
 */

import type { Evidence, Report, Results, Revision, Run, Session, Segment } from '../types';
import { FIXTURES, report, results, revision, run, sessionPage } from './samples';

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

export function buildSessionTasks(): SessionTaskView[] {
  return sessionPage.items.map((session) => {
    const disclosures: Disclosure[] = [];
    let activeRun: Run | null = null;

    if (session.latest_run_id !== null && session.latest_run_id === run.id) {
      activeRun = run;
      if (run.session_id !== session.id) {
        disclosures.push({
          tone: 'warn',
          text: `批次 ${run.id} 的 session_id 与课堂不一致，样例不可当作同一课堂结果。`,
        });
      }
      if (
        session.primary_media_asset_id !== null &&
        run.media_asset_id !== session.primary_media_asset_id
      ) {
        disclosures.push({
          tone: 'warn',
          text: '批次使用的媒体与课堂当前主媒体不是同一资产，接入后端后须逐次复核。',
        });
      }
    } else if (session.latest_run_id !== null) {
      disclosures.push({
        tone: 'warn',
        text: `课堂的最新批次 ${session.latest_run_id} 没有对应的固定样例。`,
      });
    }

    if (session.current_report_id === null) {
      const linked = report.session_id === session.id;
      disclosures.push({
        tone: 'info',
        text: linked
          ? `报告 ${report.id} 为 ${report.status}，未发布，因此课堂 current_report_id 为 null。`
          : '课堂没有当前发布版报告，current_report_id 为 null。',
      });
    }

    return { session, run: activeRun, stages: activeRun ? summarizeStages(activeRun) : null, disclosures };
  });
}

export function buildTranscriptView(): TranscriptView {
  const disclosures: Disclosure[] = [];

  if (results.run_status !== run.status) {
    disclosures.push({
      tone: 'warn',
      text: `状态不一致：results.run_status=${results.run_status}，run-partial.status=${run.status}；两份样例是独立合成场景，不能当作同一次运行。`,
    });
  }

  const revisionAttached = results.transcript_revision_id === revision.id;
  if (revisionAttached) {
    disclosures.push({ tone: 'info', text: `结果为修订 ${revision.id}（第 ${revision.revision_no} 版）的内容。` });
  } else if (
    results.transcript_revision_id !== null &&
    revision.parent_revision_id === results.transcript_revision_id
  ) {
    disclosures.push({
      tone: 'info',
      text: `结果引用第 ${results.transcript_revision_id} 版；下面的第 ${revision.revision_no} 版由它派生，例句文本尚未变化。`,
    });
  } else {
    disclosures.push({
      tone: 'warn',
      text: '结果引用的转写修订在样例中没有对应记录。',
    });
  }

  if (results.evidence.length === 0) {
    disclosures.push({
      tone: 'info',
      text: `证据为空数组；须结合 run_status=${results.run_status} 与批次状态区分“等待生成”和“确实无结果”。`,
    });
  }
  if (results.events.length === 0) {
    disclosures.push({ tone: 'info', text: 'P0 行为事件恒为空数组，行为识别属于 P1。' });
  }
  if (results.limitations.length > 0) {
    disclosures.push({ tone: 'warn', text: `本次结果自述限制：${results.limitations.join('；')}` });
  }

  return {
    results,
    // 修订样例与结果样例的引用关系在 disclosures 中显式说明，不在这里做合并推断。
    revision,
    run,
    segments: results.segments,
    evidence: results.evidence,
    disclosures,
  };
}

export function buildReportView(): ReportView {
  const session = sessionPage.items.find((item) => item.id === report.session_id) ?? null;
  const evidenceById = indexEvidence(results.evidence);
  const cited = new Set<string>([
    ...report.summary_evidence_ids,
    ...report.dimensions.flatMap((dimension) => dimension.summary_evidence_ids),
    ...report.observations.flatMap((observation) => observation.evidence_ids),
  ]);
  const unresolvedEvidenceIds = [...cited].filter((id) => !evidenceById.has(id));
  const disclosures: Disclosure[] = [];

  if (session === null) {
    disclosures.push({ tone: 'warn', text: '报告引用的课堂在课堂列表样例中不存在。' });
  } else if (session.current_report_id === null) {
    disclosures.push({
      tone: 'info',
      text: `报告状态为 ${report.status}，课堂 current_report_id 仍为 null；只有发布后的当前版才会写入该字段。`,
    });
  }

  if (report.run_id !== run.id) {
    disclosures.push({
      tone: 'warn',
      text: `报告的 run_id=${report.run_id} 与分析批次样例 ${run.id} 不同。`,
    });
  }

  const knownRevisions = new Set(
    [results.transcript_revision_id, revision.id, revision.parent_revision_id].filter(
      (value): value is string => value !== null,
    ),
  );
  const provenanceRevision = report.provenance?.transcript_revision_id ?? null;
  if (provenanceRevision !== null && !knownRevisions.has(provenanceRevision)) {
    disclosures.push({
      tone: 'warn',
      text: `报告 provenance 引用的转写修订 ${provenanceRevision} 没有对应样例，证据链需在后端联调时核对。`,
    });
  }

  if (unresolvedEvidenceIds.length > 0) {
    disclosures.push({
      tone: 'warn',
      text: `有 ${unresolvedEvidenceIds.length} 个引用证据在样例中找不到：${unresolvedEvidenceIds.join('、')}。`,
    });
  }

  if (report.dimensions.length !== 6) {
    disclosures.push({
      tone: 'warn',
      text: `六维报告应固定 6 个维度，当前为 ${report.dimensions.length} 个。`,
    });
  }

  const notObserved = report.dimensions.filter((item) => item.coverage_status !== 'observed');
  if (notObserved.length > 0) {
    disclosures.push({
      tone: 'info',
      text: `${notObserved.length} 个维度标记为证据不足或不适用，界面保留原因，不生成结论。`,
    });
  }

  return { report, session, run: report.run_id === run.id ? run : null, evidenceById, unresolvedEvidenceIds, disclosures };
}

export const fixturePaths = FIXTURES;
