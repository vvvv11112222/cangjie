import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { AccountBar } from './components/AccountBar';
import { IconHome, IconReport, IconTask, IconTranscript } from './components/icons';
import { parseSessionRoute } from './data/route';
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

/** 导航图标按路由固定；图标只是视觉标记，点击行为仍然完全由上面的 <a> 决定。 */
const NAV_ICONS: Record<(typeof NAV_ROUTES)[number]['path'], ReactElement> = {
  '#/sessions': <IconTask size={16} />,
  '#/transcript': <IconTranscript size={16} />,
  '#/reports': <IconReport size={16} />,
};

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
          <span className="nav-icon" aria-hidden="true">{NAV_ICONS[item.path]}</span>
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
  // 解析在纯函数里完成：畸形编码或非法 UUID 变成受控提示，不会抛到 React 根节点（F09/F10）。
  const sessionRoute = parseSessionRoute(hash);
  const detailId = sessionRoute.kind === 'detail' ? sessionRoute.sessionId : null;
  const route = detailId !== null ? NAV_ROUTES[0] : NAV_ROUTES.find((item) => item.path === hash) ?? NAV_ROUTES[0];
  const breadcrumbTitle =
    sessionRoute.kind === 'invalid' ? '课堂地址无效' : detailId !== null ? '课堂详情' : route.title;
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
        <NavLinks current={detailId !== null ? NAV_ROUTES[0].path : route.path} />
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
            <IconHome size={15} className="crumb-icon" />
            <span className="muted">教学工作空间</span>
            <span className="crumb-divider">/</span>
            <strong>{breadcrumbTitle}</strong>
          </div>
          <span className="prototype-badge">{dataBadge}</span>
        </header>
        <main id="main">
          {sessionRoute.kind === 'invalid' ? (
            <div className="page">
              <header className="page-head">
                <div>
                  <p className="eyebrow">
                    <a href="#/sessions">← 返回课堂列表</a>
                  </p>
                  <h1>课堂地址无效</h1>
                  <p className="muted">{sessionRoute.message}</p>
                </div>
              </header>
            </div>
          ) : detailId !== null ? (
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
