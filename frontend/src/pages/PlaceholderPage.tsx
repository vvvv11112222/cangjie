/**
 * 模板里已经有、前端还没实现的页面。
 *
 * 为什么不直接照草图把界面画出来：草图是 HTML 原型，数据全在浏览器里手写。
 * 本仓库的约定是"按钮必须对应真实功能"（frontend/UI约定.md 第 5 节），
 * 所以这里只保留页头（kicker、标题、说明）和一句依赖说明，
 * 不放任何点了没有接口支撑的按钮——那种按钮比没有更糟，会让人以为功能已经通了。
 *
 * 每页写清楚卡在哪一层，评审据此判断该等谁：
 *   available：接口已经在 main 上，只差页面开发；
 *   missing  ：协议 contracts/endpoints.json 里还没有对应端点；
 *   planned  ：属于 P1 规划（项目文档第 3 节），首期不做。
 */

import type { ReactElement } from 'react';
import { Card, EmptyState } from '../components/ui';
import { IconInfo } from '../components/icons';

export type DependencyKind = 'available' | 'missing' | 'planned';

export interface PlannedPage {
  /** 与模板 pageHeading 的第一个参数一致，作为页头小字。 */
  kicker: string;
  title: string;
  /** 一句话说明这个页面是做什么的，取自《系统草图.html》对应页面的描述。 */
  description: string;
  dependency: DependencyKind;
  /** 依赖的接口或资料，用于告诉接手的成员"卡在哪"。 */
  dependencyDetail: string;
}

const DEPENDENCY_NOTE: Record<DependencyKind, string> = {
  available: '这个页面依赖的接口已经在本仓库 main 上实现，页面本身还没开发。',
  missing: '这个页面在模板里出现，但协议 contracts/endpoints.json 还没有对应的端点。',
  planned: '这一块属于 P1 规划（项目文档第 3 节），首期以离线录像为入口，暂不开发。',
};

export function PlaceholderPage({ page }: { page: PlannedPage }): ReactElement {
  return (
    <div className="page">
      <header className="page-head">
        <div>
          <p className="eyebrow">{page.kicker}</p>
          <h1>{page.title}</h1>
          <p className="muted">{page.description}</p>
        </div>
      </header>

      <p className="notice-strip">
        <IconInfo size={14} aria-hidden="true" />
        {DEPENDENCY_NOTE[page.dependency]}依赖：{page.dependencyDetail}
      </p>

      <Card title="页面尚未实现" subtitle="保留页头与依赖说明，避免出现点了没有反应的假功能。">
        <EmptyState
          text={`${page.title}的界面还没有开发。接口就绪后，这一页按《系统草图.html》的布局补齐：统计卡片、筛选条、列表与详情。`}
        />
      </Card>
    </div>
  );
}

/**
 * 占位页清单，键是 hash 路由。文案与《系统草图.html》里各页的
 * pageHeading / 描述保持一致，免得评审再拿两套说法比对。
 * 已经实现的页面不在这里（例如 #/organizations 已经由 OrganizationsPage 实现）。
 */
export const PLANNED_PAGES: Record<string, PlannedPage> = {
  '#/audit': {
    kicker: 'ACTIVITY LOG',
    title: '操作记录',
    description: '查看课堂管理与报告复核的操作轨迹。',
    dependency: 'missing',
    dependencyDetail: '协议里只有操作结果资源，没有独立的审计查询端点',
  },
};
