import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { ReportPage } from './pages/ReportPage';
import { SessionListPage } from './pages/SessionListPage';
import { TranscriptPage } from './pages/TranscriptPage';

/**
 * M0 只有三个页面，用一个最小 hash 路由代替路由库：
 * 静态托管时不需要服务端重写，接入 Go 后可按需替换为正式路由。
 */
const ROUTES = [
  { path: '#/sessions', label: '课堂任务', title: '课堂任务', render: () => <SessionListPage /> },
  { path: '#/transcript', label: '转写', title: '转写', render: () => <TranscriptPage /> },
  { path: '#/reports', label: '报告', title: '报告', render: () => <ReportPage /> },
] as const;

const DEFAULT_ROUTE = ROUTES[0].path;

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
  const route = ROUTES.find((item) => item.path === hash) ?? ROUTES[0];

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
        <nav aria-label="主导航">
          <p className="nav-label">教学分析</p>
          {ROUTES.map((item) => (
            <a
              key={item.path}
              className={`nav-item${item.path === route.path ? ' active' : ''}`}
              href={item.path}
              aria-current={item.path === route.path ? 'page' : undefined}
            >
              {item.label}
            </a>
          ))}
        </nav>
        <div className="sidebar-note">
          <p>M0 前端骨架</p>
          <p className="tiny">
            仅读取 contracts/examples 固定样例；登录、权限与真实数据在 M1 接入 Go。
          </p>
        </div>
      </aside>

      <div className="workspace">
        <header className="topbar">
          <div className="breadcrumb">
            <span className="muted">教学工作空间</span>
            <span className="crumb-divider">/</span>
            <strong>{route.title}</strong>
          </div>
          <span className="prototype-badge">固定样例 · 未接入后端</span>
        </header>
        <main id="main">{route.render()}</main>
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
