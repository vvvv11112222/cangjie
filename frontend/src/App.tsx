import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { AccountBar } from './components/AccountBar';
import { DATA_SOURCE_KIND, DATA_SOURCE_LABEL } from './data/sources';
import { ReportPage } from './pages/ReportPage';
import { SessionDetailPage } from './pages/SessionDetailPage';
import { SessionListPage } from './pages/SessionListPage';
import { TranscriptPage } from './pages/TranscriptPage';

/**
 * M0 只有三个页面，用一个最小 hash 路由代替路由库：
 * 静态托管时不需要服务端重写，接入 Go 后可按需替换为正式路由。
 * NAV_ROUTES 同时被窄屏导航的回归检查使用（src/navigation.test.ts）。
 */
export const NAV_ROUTES = [
  { path: '#/sessions', label: '课堂任务', title: '课堂任务' },
  { path: '#/transcript', label: '转写', title: '转写' },
  { path: '#/reports', label: '报告', title: '报告' },
] as const;

const PAGES: Record<(typeof NAV_ROUTES)[number]['path'], () => ReactElement> = {
  '#/sessions': () => <SessionListPage />,
  '#/transcript': () => <TranscriptPage />,
  '#/reports': () => <ReportPage />,
};

const DEFAULT_ROUTE = NAV_ROUTES[0].path;

/** #/sessions/{id} 进入指定课堂详情；id 之外的路径仍走三个固定页面。 */
const SESSION_DETAIL = /^#\/sessions\/([^/]+)$/;

/**
 * 唯一的页面导航入口，宽屏在侧栏、窄屏在顶部导航条（见 styles.css 的 900px 断点）。
 * 三个页面的链接都从这里渲染，避免出现某一种布局下没有入口的情况。
 */
export function NavLinks({ current }: { current: string }): ReactElement {
  return (
    <nav aria-label="主导航">
      <p className="nav-label">教学分析</p>
      {NAV_ROUTES.map((item) => (
        <a
          key={item.path}
          className={`nav-item${item.path === current ? ' active' : ''}`}
          href={item.path}
          aria-current={item.path === current ? 'page' : undefined}
        >
          {item.label}
        </a>
      ))}
    </nav>
  );
}

function useHashRoute(): string {
  const [hash, setHash] = useState(() => window.location.hash || DEFAULT_ROUTE);
  useEffect(() => {
    const onChange = () => setHash(window.location.hash || DEFAULT_ROUTE);
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);
  return hash;
}

export function App(): ReactElement {
  const hash = useHashRoute();
  const detailMatch = SESSION_DETAIL.exec(hash);
  const detailId = detailMatch === null ? null : decodeURIComponent(detailMatch[1]!);
  const route = detailMatch ? NAV_ROUTES[0] : NAV_ROUTES.find((item) => item.path === hash) ?? NAV_ROUTES[0];
  const breadcrumbTitle = detailMatch ? '课堂详情' : route.title;
  const dataBadge =
    DATA_SOURCE_KIND === 'api'
      ? `${DATA_SOURCE_LABEL.api} · 走 /api/v1`
      : `${DATA_SOURCE_LABEL.fixtures} · 未接入后端`;

  return (
    <div className="layout">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">观</span>
          <div>
            <div className="brand-name">观课</div>
            <div className="brand-caption">教学质量管理系统</div>
          </div>
        </div>
        <NavLinks current={detailMatch ? NAV_ROUTES[0].path : route.path} />
        <AccountBar />
        <div className="sidebar-note">
          <p>M1 数据访问层</p>
          <p className="tiny">
            默认读取 contracts/examples 固定样例；设 VITE_DATA_SOURCE=api 时改走 /api/v1。
          </p>
        </div>
      </aside>

      <div className="workspace">
        <header className="topbar">
          <div className="breadcrumb">
            <span className="muted">教学工作空间</span>
            <span className="crumb-divider">/</span>
            <strong>{breadcrumbTitle}</strong>
          </div>
          <span className="prototype-badge">{dataBadge}</span>
        </header>
        <main id="main">
          {detailId !== null ? (
            <SessionDetailPage key={detailId} sessionId={detailId} />
          ) : (
            PAGES[route.path]()
          )}
        </main>
        <footer className="workspace-footer">
          <span>观课 · 让教学观察有据可循</span>
          <span className="muted tiny">
            数据形状以 contracts/v1.schema.json 与 docs/开发协议.md 为准
          </span>
        </footer>
      </div>
    </div>
  );
}
