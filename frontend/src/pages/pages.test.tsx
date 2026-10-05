import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { SessionListPage } from './SessionListPage';
import { TranscriptPage } from './TranscriptPage';
import { ReportPage } from './ReportPage';
import { buildReportView, buildSessionTasks, buildTranscriptView } from '../data/viewModel';

describe('三个页面的固定样例渲染', () => {
  it('课堂任务列表显示课堂、批次阶段和允许操作', () => {
    const html = renderToStaticMarkup(<SessionListPage tasks={buildSessionTasks()} />);
    expect(html).toContain('课堂任务');
    expect(html).toContain('合成课堂');
    expect(html).toContain('阶段完成 4/6');
    expect(html).toContain('提交分析');
    expect(html).toContain('contracts/examples/session-page.json');
  });

  it('转写页显示片段、未知说话人与修订信息', () => {
    const html = renderToStaticMarkup(<TranscriptPage view={buildTranscriptView()} />);
    expect(html).toContain('这节课介绍矩阵的基本运算。');
    expect(html).toContain('未知说话人');
    expect(html).toContain('00:01.000');
    expect(html).toContain('纠正专业术语');
    expect(html).toContain('人工修订');
  });

  it('报告页显示六维、证据不足原因与溯源', () => {
    const html = renderToStaticMarkup(<ReportPage view={buildReportView()} />);
    for (const label of ['教学内容', '教学节奏', '思维启发', '教学表达', '课堂管理', '教学技术']) {
      expect(html).toContain(label);
    }
    expect(html).toContain('证据不足');
    expect(html).toContain('synthetic-report');
    expect(html).toContain('固定样例');
  });
});
