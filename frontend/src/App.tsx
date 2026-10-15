import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { useAuth } from './auth';
import { AccountBar } from './components/AccountBar';
import { NotificationBell } from './components/NotificationBell';
import {
  IconCalendar,
  IconCamera,
  IconBook,
  IconChevronDown,
  IconFolder,
  IconGrid,
  IconHistory,
  IconHome,
  IconInsights,
  IconList,
  IconReport,
  IconSettings,
  IconShield,
  IconTask,
  IconTranscript,
  IconVideo,
  IconUser,
} from './components/icons';
import type { RoleCode } from './data/authApi';
import { parseSessionRoute } from './data/route';
import { isApiMode } from './data/sources';
import { roleLabel } from './format';
import { PLANNED_PAGES, PlaceholderPage } from './pages/PlaceholderPage';
import { AccountsPage } from './pages/AccountsPage';
import { OrganizationsPage } from './pages/OrganizationsPage';
import { ReportPage } from './pages/ReportPage';
import { SessionDetailPage } from './pages/SessionDetailPage';
import { SessionListPage } from './pages/SessionListPage';
import { TranscriptPage } from './pages/TranscriptPage';
import {
  DEFAULT_ROUTE,
  isVisiblePath,
  navItemAt,
  navLabel,
  navLabelAt,
  SESSIONS_PATH,
  visibleGroups,
} from './roleNav';

/**
 * 侧栏按《系统草图.html》分成三段（管理中心 / 教学工作 / 资源与管理），
 * 并按登录角色过滤可见入口；规则集中在 src/roleNav.ts，这里只负责渲染。
 *
 * 看不到的入口不渲染，但这不是授权：真正能不能做仍由服务端逐次判断
 * （协议第 1.1 节、根目录 AGENTS.md 第 3 节）。
 * 同一套分组规则被窄屏导航的回归检查使用（src/navigation.test.tsx）。
 */

/** 导航图标按路由固定；图标只是视觉标记，点击行为仍然完全由 <a> 决定。 */
const NAV_ICONS: Record<string, ReactElement> = {
  '#/management': <IconGrid size={16} />,
  '#/organizations': <IconList size={16} />,
  '#/accounts': <IconShield size={16} />,
  '#/recordings': <IconVideo size={16} />,
  '#/dashboard': <IconInsights size={16} />,
  '#/sessions': <IconTask size={16} />,
  '#/transcript': <IconTranscript size={16} />,
  '#/academic': <IconCalendar size={16} />,
  '#/tasks': <IconHistory size={16} />,
  '#/reports': <IconReport size={16} />,
  '#/sources': <IconFolder size={16} />,
  '#/devices': <IconCamera size={16} />,
  '#/settings': <IconSettings size={16} />,
  '#/audit': <IconShield size={16} />,
};

/** 已经实现的页面：课堂档案（列表 + 详情）、转写、教学报告。 */
const IMPLEMENTED_PAGES: Record<string, () => ReactElement> = {
  [SESSIONS_PATH]: () => <SessionListPage />,
  '#/accounts': () => <AccountsPage />,
  '#/organizations': () => <OrganizationsPage />,
  '#/transcript': () => <TranscriptPage />,
  '#/reports': () => <ReportPage />,
};

/** 模板里有、前端还没实现的页面，统一渲染占位页（依赖口径见 PlaceholderPage.tsx）。 */
const PLANNED_RENDERERS = Object.fromEntries(
  Object.entries(PLANNED_PAGES).map(([path, page]): [string, () => ReactElement] => [
    path,
    () => <PlaceholderPage page={page} />,
  ]),
);

const PAGE_RENDERERS: Record<string, () => ReactElement> = {
  // 已实现的页面放在后面：同一个路径万一还留在占位清单里，以真实页面为准。
  ...PLANNED_RENDERERS,
  ...IMPLEMENTED_PAGES,
};

/**
 * 唯一的页面导航入口，宽屏在侧栏、窄屏在顶部导航条（见 styles.css 的 900px 断点）。
 * 三段分组的入口都从这里渲染，避免出现某一种布局下没有入口的情况。
 */
export function NavLinks({ current, roles }: { current: string; roles: readonly RoleCode[] }): ReactElement {
  return (
    <nav aria-label="主导航">
      {visibleGroups(roles).map((group) => (
        <div className="nav-group" key={group.name}>
          <p className="nav-label">{group.name}</p>
          {group.items.map((item) => (
            <a
              key={item.path}
              className={`nav-item${item.path === current ? ' active' : ''}`}
              href={item.path}
              aria-current={item.path === current ? 'page' : undefined}
            >
              <span className="nav-icon" aria-hidden="true">{NAV_ICONS[item.path]}</span>
              {navLabel(item, roles)}
            </a>
          ))}
        </div>
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

/** 一行说明加一个返回入口，用于地址无效和"当前角色看不到这个入口"两种情况。 */
function PageNotice({ eyebrow, title, message }: {
  eyebrow: string;
  title: string;
  message: string;
}): ReactElement {
  return (
    <div className="page">
      <header className="page-head">
        <div>
          <p className="eyebrow">
            <a href={DEFAULT_ROUTE}>← 返回课堂档案</a>
          </p>
          <h1>{title}</h1>
          <p className="muted">{eyebrow}</p>
          <p className="muted">{message}</p>
        </div>
      </header>
    </div>
  );
}

export function App(): ReactElement {
  const hash = useHashRoute();
  const { me } = useAuth();
  const apiMode = isApiMode();
  const [previewRole, setPreviewRole] = useState<RoleCode | null>(null);
  // 账号面板由 App 掌管开合：顶栏头像和侧栏底部的"当前身份"都能打开它。
  const [accountOpen, setAccountOpen] = useState(false);
  // 未登录（含固定样例模式）时 roles 为空，roleNav 会显示全部入口，不假装用户有某个角色。
  const roles: readonly RoleCode[] =
    me !== null ? me.roles.map((role) => role.role_code) : previewRole === null ? [] : [previewRole];
  // 解析在纯函数里完成：畸形编码或非法 UUID 变成受控提示，不会抛到 React 根节点（F09/F10）。
  const sessionRoute = parseSessionRoute(hash);
  const detailId = sessionRoute.kind === 'detail' ? sessionRoute.sessionId : null;
  // 课堂详情属于课堂档案；未知地址回落到默认页，不留空白。
  const currentItem = navItemAt(detailId !== null ? SESSIONS_PATH : hash) ?? navItemAt(DEFAULT_ROUTE);
  const currentPath = currentItem?.path ?? DEFAULT_ROUTE;
  const render = PAGE_RENDERERS[currentPath];

  const breadcrumbTitle =
    sessionRoute.kind === 'invalid'
      ? '课堂地址无效'
      : detailId !== null
        ? '课堂详情'
        : navLabelAt(currentPath, roles);

  return (
    <div className="layout">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">
            <IconBook size={24} />
          </span>
          <div>
            <div className="brand-name">观课</div>
            <div className="brand-caption">教学质量管理系统</div>
          </div>
        </div>
        {/*
          未登录时侧栏显示全部入口，别让人误以为"所有角色看到的都一样"：
          这里写清楚现在的口径，登录后菜单会按角色收窄。
        */}
        {roles.length === 0 ? (
          <p className="nav-hint">未登录：显示全部功能入口；登录后按你的角色显示。</p>
        ) : null}
        <NavLinks current={currentPath} roles={roles} />
        {/*
          草图侧栏底部是「本地演示工作空间 + 当前身份 + 切换角色视角」。
          这里的按钮只负责打开顶栏那个账号面板（登录入口按之前的要求留在右上角）。
        */}
        <div className="sidebar-bottom">
          {!apiMode ? (
            <label className="role-preview-field">
              <span>演示角色视角</span>
              <select
                aria-label="演示角色视角"
                value={previewRole ?? ''}
                onChange={(event) => setPreviewRole((event.target.value || null) as RoleCode | null)}
              >
                <option value="">全部功能</option>
                <option value="sys_admin">系统管理员</option>
                <option value="academic_admin">学院教务</option>
                <option value="supervisor">教学督导</option>
                <option value="teacher">任课教师</option>
              </select>
            </label>
          ) : null}
          <p className="local-workspace">
            <span className="status-dot" aria-hidden="true" />
            本地工作空间
          </p>
          <button
            type="button"
            className="profile-button"
            aria-expanded={accountOpen}
            onClick={() => setAccountOpen((value) => !value)}
          >
            <span className="account-avatar" aria-hidden="true">
              {me === null ? <IconUser size={17} /> : me.display_name.slice(0, 1)}
            </span>
            <span>
              <span className="profile-name">
                {me === null
                  ? previewRole === null ? '未登录' : `${roleLabel(previewRole)} · 演示视角`
                  : `${me.display_name} · ${me.roles.map((role) => roleLabel(role.role_code)).join('、')}`}
              </span>
              <span className="profile-caption">切换账号 / 退出登录</span>
            </span>
            <IconChevronDown size={14} aria-hidden="true" />
          </button>
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
          <div className="topbar-actions">
            {/* 待办提醒（草图的 notification-button）。 */}
            <NotificationBell />
            {/* 账号入口：登录、退出、切换账号都在顶栏右上角。 */}
            <AccountBar open={accountOpen} onOpenChange={setAccountOpen} />
          </div>
        </header>
        <main id="main">
          {sessionRoute.kind === 'invalid' ? (
            <PageNotice
              eyebrow="课堂地址无法解析"
              title="课堂地址无效"
              message={sessionRoute.message}
            />
          ) : !isVisiblePath(currentPath, roles) ? (
            <PageNotice
              eyebrow="当前账号的角色看不到这个入口"
              title="没有这个入口"
              message="侧栏只显示当前角色可用的功能。需要这一页时，请让管理员调整你的角色或范围。"
            />
          ) : detailId !== null ? (
            <SessionDetailPage key={detailId} sessionId={detailId} />
          ) : render === undefined ? (
            <PageNotice
              eyebrow="页面还没有实现"
              title="页面尚未实现"
              message="这个地址属于模板里的页面，但前端还没开发。"
            />
          ) : (
            render()
          )}
        </main>
        <footer className="workspace-footer">
          <span>观课 · 让教学观察有据可循</span>
        </footer>
      </div>
    </div>
  );
}
