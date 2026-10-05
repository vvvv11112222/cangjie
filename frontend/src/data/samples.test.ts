import { describe, expect, it } from 'vitest';
import { parseReport, parseSessionPage, report, results, revision, run, sessionPage } from './samples';
import { buildReportView, buildSessionTasks, buildTranscriptView } from './viewModel';
import reportDraft from '../../../contracts/examples/report-draft.json';

describe('固定样例解析', () => {
  it('读取到三页需要的样例', () => {
    expect(sessionPage.items).toHaveLength(1);
    expect(run.stage_plan).toHaveLength(6);
    expect(results.segments).toHaveLength(1);
    expect(revision.revision_no).toBe(2);
    expect(report.dimensions).toHaveLength(6);
  });

  it('样例漂移时给出可定位的错误', () => {
    const drifted = { ...reportDraft, status: 'done' };
    expect(() => parseReport(drifted, 'Report')).toThrowError(/Report\.status/);
    expect(() => parseSessionPage({ items: [{}], next_cursor: null }, 'SessionPage')).toThrowError(
      /SessionPage\.items\[0\]\.id/,
    );
  });
});

describe('视图模型与引用核对', () => {
  it('课堂任务挂上最新批次并统计阶段', () => {
    const [task] = buildSessionTasks();
    expect(task?.run?.id).toBe(sessionPage.items[0]?.latest_run_id);
    expect(task?.stages).toMatchObject({ total: 6, succeeded: 4, failed: 1, skipped: 1, pending: 0 });
  });

  it('转写页显式说明结果与批次样例状态不一致', () => {
    const view = buildTranscriptView();
    expect(view.segments).toHaveLength(1);
    expect(view.disclosures.some((item) => item.tone === 'warn' && item.text.includes('状态不一致'))).toBe(
      true,
    );
  });

  it('报告页能解析摘要与观察引用的证据，并暴露未知修订', () => {
    const view = buildReportView();
    expect(view.unresolvedEvidenceIds).toEqual([]);
    expect(view.evidenceById.get(results.evidence[0]!.id)?.description).toBe(
      results.evidence[0]!.description,
    );
    expect(
      view.disclosures.some((item) => item.tone === 'warn' && item.text.includes('没有对应样例')),
    ).toBe(true);
  });
});
