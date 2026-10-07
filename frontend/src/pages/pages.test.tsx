import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { SessionListPage } from './SessionListPage';
import { TranscriptPage } from './TranscriptPage';
import { ReportPage } from './ReportPage';
import { buildReportView, buildSessionTasks, buildTranscriptView } from '../data/viewModel';
import type { ReportView, SessionTaskView } from '../data/viewModel';
import type { ReportAction, SessionAction } from '../types';

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

/**
 * M1 起页面改为经数据访问层取数。默认（VITE_DATA_SOURCE 未设）仍是固定样例，
 * 所以不传 props 时的输出必须和 M0 一致：同步有内容，不出现加载态。
 */
describe('页面默认路径（数据访问层 · 固定样例模式）', () => {
  it('课堂任务页不传 props 也能直接渲染样例', () => {
    const html = renderToStaticMarkup(<SessionListPage />);
    expect(html).toContain('合成课堂');
    expect(html).toContain('阶段完成 4/6');
    expect(html).not.toContain('正在读取数据');
  });

  it('转写页不传 props 也能直接渲染样例', () => {
    const html = renderToStaticMarkup(<TranscriptPage />);
    expect(html).toContain('这节课介绍矩阵的基本运算。');
    expect(html).not.toContain('正在读取数据');
  });

  it('报告页不传 props 也能直接渲染样例', () => {
    const html = renderToStaticMarkup(<ReportPage />);
    expect(html).toContain('synthetic-report');
    expect(html).not.toContain('正在读取数据');
  });
});

/**
 * 评审意见 P2：页面必须保留服务端已经授予的资源操作。
 * 项目文档第 2 节允许教师"修改转写、编辑报告草稿""导出当前发布版报告"，
 * 后端角色能力里却没有对应的 review/publish，所以前端不能再用账号能力去删减资源操作。
 */
describe('资源 allowed_actions 原样展示', () => {
  it('课堂：教师看到服务端授予的 edit_transcript / analyze', () => {
    const [task] = buildSessionTasks();
    const granted: SessionAction[] = ['edit_transcript', 'analyze'];
    const teacherTask: SessionTaskView = {
      ...task!,
      session: { ...task!.session, allowed_actions: granted },
    };

    const html = renderToStaticMarkup(<SessionListPage tasks={[teacherTask]} />);

    expect(html).toContain('修改转写');
    expect(html).toContain('提交分析');
  });

  it('报告：草稿的编辑与导出不被删掉', () => {
    const view = buildReportView();
    const granted: ReportAction[] = ['edit', 'export', 'publish'];
    const withGranted: ReportView = { ...view, report: { ...view.report, allowed_actions: granted } };

    const html = renderToStaticMarkup(<ReportPage view={withGranted} />);

    expect(html).toContain('编辑草稿');
    expect(html).toContain('导出');
  });
});
