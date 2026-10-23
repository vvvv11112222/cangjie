import type { ReactElement } from 'react';
import { IconInfo } from '../components/icons';
import { Card,EmptyState } from '../components/ui';

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
          
          <h1>{page.title}</h1>
          
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

export const PLANNED_PAGES: Record<string, PlannedPage> = {};
