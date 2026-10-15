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
  '#/dashboard': {
    kicker: 'TEACHING OVERVIEW',
    title: '教学工作台',
    description: '从课堂记录到教学改进，每一步都有证据可循。',
    dependency: 'available',
    dependencyDetail: 'GET /sessions、GET /sessions/{id}/reports',
  },
  '#/management': {
    kicker: 'SCHOOL ADMINISTRATION',
    title: '全校管理工作台',
    description: '系统管理员独立于各学院，统一管理全校组织、账号权限与课堂录像。',
    dependency: 'available',
    dependencyDetail: 'GET /org-units、GET /users、GET /sessions',
  },
  '#/recordings': {
    kicker: 'RECORDING MANAGEMENT',
    title: '课堂录像管理',
    description: '按学院、年级、班级定位录像，维护课堂信息与对应访问链接。',
    dependency: 'available',
    dependencyDetail: 'GET /sessions、GET /sessions/{id}/media',
  },
  '#/academic': {
    kicker: 'ACADEMIC AFFAIRS',
    title: '教务与课表',
    description: '管理各班课表，同一教师、班级或教室禁止时间重叠。',
    dependency: 'available',
    dependencyDetail: 'GET/POST/PATCH /schedules；/courses、/class-groups、/offerings',
  },
  '#/tasks': {
    kicker: 'ANALYSIS WORKSPACE',
    title: '分析任务',
    description: '了解分析的每一步，让异常与处理结果清晰可见。',
    dependency: 'available',
    dependencyDetail: 'GET /sessions/{id}/analysis-runs、GET /analysis-runs/{id}',
  },
  '#/sources': {
    kicker: 'MEDIA & SOURCES',
    title: '素材与来源',
    description: '为课堂素材保留来处、用途与使用限制。',
    dependency: 'available',
    dependencyDetail: 'POST /sources、GET /sessions/{id}/sources、POST /sources/{id}/verify',
  },
  '#/devices': {
    kicker: 'DEVICE CONNECTION',
    title: '设备接入',
    description: '后续接入实时课堂，沿用现有分析和报告流程。',
    dependency: 'planned',
    dependencyDetail: 'FR-12 RTSP 与录像分段',
  },
  '#/settings': {
    kicker: 'WORKSPACE SETTINGS',
    title: '运行与设置',
    description: '配置工作空间的资源预算与数据保留策略。',
    dependency: 'available',
    dependencyDetail: 'GET /operations、GET /model-calls',
  },
  '#/audit': {
    kicker: 'ACTIVITY LOG',
    title: '操作记录',
    description: '查看课堂管理与报告复核的操作轨迹。',
    dependency: 'missing',
    dependencyDetail: '协议里只有操作结果资源，没有独立的审计查询端点',
  },
};
