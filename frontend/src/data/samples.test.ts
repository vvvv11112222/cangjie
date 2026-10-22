import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  fixtureMediaFor,
  parseMedia,
  parseReport,
  parseRun,
  parseSessionPage,
  playbackMedia,
  report,
  results,
  revision,
  run,
  sessionPage,
} from './samples';
import { buildReportView, buildSessionTasks, buildTranscriptView } from './viewModel';
import reportDraft from '../../../contracts/examples/report-draft.json';

describe('固定样例解析', () => {
  it('reads optional comprehensive material flags while retaining older report compatibility', () => {
    const source = { ...reportDraft, provenance: { ...reportDraft.provenance, included_materials: { academic_schedule: true, speech: true, visual: false } } };
    expect(parseReport(source).provenance?.included_materials?.speech).toBe(true);
    expect(parseReport(reportDraft).provenance?.included_materials).toBeUndefined();
    expect(() => parseReport({ ...source, provenance: { ...source.provenance, included_materials: { ...source.provenance.included_materials, visual: 'true' } } })).toThrow();
  });
  it('支持独立视觉任务而不要求教学报告', () => {
    const visual = parseRun({ ...run, mode: 'visual_only', stage_plan: ['probe', 'video_analysis'],
      jobs: run.jobs.filter(job => ['probe', 'video_analysis'].includes(job.stage)), report_id: null });
    expect(visual.mode).toBe('visual_only');
    expect(visual.report_id).toBeNull();
  });
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

  it('媒体样例不符合协议时报错可定位', () => {
    expect(() => parseMedia({ ...playbackMedia, kind: 'video' }, 'Media')).toThrowError(/Media\.kind/);
    // Schema 规定播放地址只能是 /api/v1/media/...：本地离线地址不能伪装成协议里的 playback_url。
    expect(() => parseMedia({ ...playbackMedia, playback_url: '/samples/x.mp4' }, 'Media')).toThrowError(
      /Media\.playback_url/,
    );
    expect(() => parseMedia({ ...playbackMedia, duration_ms: null }, 'Media')).not.toThrow();
  });

  it('回放样例的 sha256 与字节数必须和仓库内样例视频一致', () => {
    const bytes = readFileSync(
      fileURLToPath(new URL('../../public/samples/m2-playback-demo.mp4', import.meta.url)),
    );
    expect(bytes.byteLength).toBe(playbackMedia.byte_size);
    expect(createHash('sha256').update(bytes).digest('hex')).toBe(playbackMedia.sha256);
  });

  it('固定样例模式把播放地址改写到仓库内样例视频，其它 id 不编造媒体', () => {
    const media = fixtureMediaFor(playbackMedia.id);
    expect(media?.playback_url).toBe(`${import.meta.env.BASE_URL}samples/m2-playback-demo.mp4`);
    expect(media?.id).toBe(playbackMedia.id);
    // 用播放资产 id 也能取到同一条媒体（协议里播放地址指向播放资产）。
    expect(fixtureMediaFor(playbackMedia.playback_asset_id ?? '')?.id).toBe(playbackMedia.id);
    expect(fixtureMediaFor('00000000-0000-4000-8000-0000000000ff')).toBeNull();
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
