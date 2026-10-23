import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { AcademicPage, DashboardPage, M1RoleContext, ManagementPage, RecordingsPage, SourcesPage, TasksPage } from './M1Pages';

describe('M1 页面固定样例骨架', () => {
  it('保留系统草图的页面标题和真实数据边界说明', () => {
    const html = [AcademicPage, DashboardPage, ManagementPage, RecordingsPage, SourcesPage, TasksPage]
      .map(Page => renderToStaticMarkup(<Page />)).join('\n');
    for (const label of ['教务与课表', '教学工作台', '全校管理工作台', '课堂录像管理', '素材与来源', '分析任务']) expect(html).toContain(label);
    expect(html).toContain('固定样例模式没有教务与来源样例');
    expect(html).not.toContain('当前协议没有跨课堂批次列表');
  });

  it('按草图保留 M1 页面关键区块和按钮位置', () => {
    const academic = renderToStaticMarkup(<AcademicPage />);
    expect(academic).toContain('导出课表 CSV');
    expect(academic).toContain('本周课表');
    expect(academic).toContain('开课实例');
    expect(academic).toContain('全部班级');

    const management = renderToStaticMarkup(<ManagementPage />);
    expect(management.match(/management-entry/g)).toHaveLength(3);
    expect(management).not.toContain('最高权限 · 全校范围');

    const recordings = renderToStaticMarkup(<RecordingsPage />);
    for (const label of ['全部学院', '全部年级', '全部班级', '重置筛选', '上传录像并分析']) expect(recordings).toContain(label);

    const tasks = renderToStaticMarkup(<TasksPage />);
    expect(tasks).toContain('上传并分析');
    expect(tasks).toContain('progress-fill');

    const sources = renderToStaticMarkup(<SourcesPage />);
    expect(sources).toContain('课堂素材登记');
    expect(sources).toContain('来源核验');
  });

  it('学院教务预览在工作台显示新增排课入口', () => {
    const html = renderToStaticMarkup(<M1RoleContext.Provider value={['academic_admin']}><DashboardPage /></M1RoleContext.Provider>);
    expect(html).toContain('新增排课');
    expect(html).not.toContain('上传课堂录像');
  });
});
