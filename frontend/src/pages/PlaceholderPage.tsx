import type { ReactElement } from 'react';
import { Card, EmptyState } from '../components/ui';
import { IconInfo } from '../components/icons';

export interface PlannedPage {
  kicker: string;
  title: string;
  description: string;
  unavailableMessage: string;
  emptyMessage: string;
}

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
        {page.unavailableMessage}
      </p>

      <Card title="暂无可查看的记录">
        <EmptyState text={page.emptyMessage} />
      </Card>
    </div>
  );
}

export const PLANNED_PAGES: Record<string, PlannedPage> = {
  '#/audit': {
    kicker: 'ACTIVITY LOG',
    title: '操作记录',
    description: '查看课堂管理与报告复核的操作轨迹。',
    unavailableMessage: '操作记录查询暂未开放。',
    emptyMessage: '你仍可在具体课堂、分析任务和教学报告中查看当前处理状态。',
  },
};
