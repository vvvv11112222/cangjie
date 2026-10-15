/**
 * 侧栏导航：把《系统草图.html》里的"角色 × 页面"搬到 React。
 *
 * 为什么单独一个文件：这是唯一一处"菜单可见性"的规则。散在 App.tsx 和各个页面里
 * 很快就会对不上，评审再拿模板比对时又得重新找一遍。
 *
 * 这里只决定"谁能看到哪个入口"，不做授权判断：按协议第 1.1 节和根目录 AGENTS.md
 * 第 3 节，前端只负责展示，能不能真正操作由服务端按资源逐次检查。
 *
 * 来源：
 *   - 每个角色的页面清单：仓库根目录《系统草图.html》第 107～110 行 roles[].pages；
 *   - 三段分组：同一文件第 363 行（管理中心 / 教学工作 / 资源与管理）；
 *   - 角色码：协议 contracts/v1.schema.json 里 RoleBinding.role_code 的取值，
 *     不是草图里的简写（草图写 admin，协议写 sys_admin）。
 */

import type { RoleCode } from './data/authApi';

/** 协议角色码与草图简写的对应关系，写成常量避免以后来回猜。 */
export const SKETCH_ROLE_ALIAS: Record<RoleCode, string> = {
  sys_admin: 'admin',
  academic_admin: 'academic',
  supervisor: 'supervisor',
  teacher: 'teacher',
};

/** 四种固定角色（项目文档第 2 节）。未登录时用它代表"显示全部入口"。 */
export const ALL_ROLES: readonly RoleCode[] = [
  'sys_admin',
  'academic_admin',
  'supervisor',
  'teacher',
];

const ADMIN_ONLY: readonly RoleCode[] = ['sys_admin'];
const ADMIN_AND_ACADEMIC: readonly RoleCode[] = ['sys_admin', 'academic_admin'];
const ADMIN_AND_SUPERVISOR: readonly RoleCode[] = ['sys_admin', 'supervisor'];
/** 三个能看课堂内容的角色；学院教务按项目文档第 2 节没有读取录像/转写/报告的权限。 */
const CONTENT_ROLES: readonly RoleCode[] = ['sys_admin', 'supervisor', 'teacher'];

/** 课堂档案的路径常量：课堂详情 #/sessions/{id} 也算这个入口下的页面。 */
export const SESSIONS_PATH = '#/sessions';

export interface NavItem {
  /** hash 路由；课堂详情仍是 #/sessions/{id}，由 data/route.ts 解析。 */
  path: string;
  label: string;
  /** 能看到这个入口的角色。 */
  roles: readonly RoleCode[];
  /**
   * 草图为不同角色用了不同菜单名，例如学院教务看到的是"学院人员与权限"。
   * 只写与默认 label 不同的部分。
   */
  labelByRole?: Partial<Record<RoleCode, string>>;
}

export interface NavGroup {
  name: string;
  items: readonly NavItem[];
}

/** 三段式侧栏，顺序与草图第 363 行一致。 */
export const NAV_GROUPS: readonly NavGroup[] = [
  {
    name: '管理中心',
    items: [
      { path: '#/management', label: '全校管理工作台', roles: ADMIN_ONLY },
      { path: '#/organizations', label: '学院、年级与班级', roles: ADMIN_ONLY },
      {
        path: '#/accounts',
        label: '账号与权限',
        roles: ADMIN_AND_ACADEMIC,
        labelByRole: { academic_admin: '学院人员与权限' },
      },
      { path: '#/recordings', label: '课堂录像管理', roles: ADMIN_ONLY },
    ],
  },
  {
    name: '教学工作',
    items: [
      { path: '#/dashboard', label: '教学工作台', roles: ALL_ROLES },
      // 课堂档案 = 现有的课堂列表与详情页，只是按模板改了名字。
      { path: '#/sessions', label: '课堂档案', roles: ALL_ROLES },
      {
        path: '#/academic',
        label: '教务与课表',
        roles: ADMIN_AND_ACADEMIC,
      },
      { path: '#/tasks', label: '分析任务', roles: CONTENT_ROLES },
      { path: '#/reports', label: '教学报告', roles: CONTENT_ROLES },
    ],
  },
  {
    name: '资源与管理',
    items: [
      { path: '#/sources', label: '素材与来源', roles: ALL_ROLES },
      { path: '#/devices', label: '设备接入', roles: ADMIN_AND_SUPERVISOR },
      { path: '#/settings', label: '运行与设置', roles: ADMIN_ONLY },
      { path: '#/audit', label: '操作记录', roles: ['sys_admin', 'academic_admin', 'supervisor'] },
    ],
  },
];

/** 所有导航项的扁平化结果，路由表按它生成。 */
export const NAV_ITEMS: readonly NavItem[] = NAV_GROUPS.flatMap((group) => group.items);

/**
 * 转写仍保留旧地址，供课堂详情和既有链接进入；系统草图没有独立菜单入口，
 * 因此不放进 NAV_GROUPS。它的可见范围与课堂内容一致。
 */
const AUXILIARY_ITEMS: readonly NavItem[] = [
  { path: '#/transcript', label: '转写', roles: CONTENT_ROLES },
];

const ROUTE_ITEMS: readonly NavItem[] = [...NAV_ITEMS, ...AUXILIARY_ITEMS];

/** 默认落地页：课堂档案是当前唯一有真实内容的页面，登录前后都不会落到空页上。 */
export const DEFAULT_ROUTE = SESSIONS_PATH;

/** 按路径取导航项；路径不存在时返回 null，由调用方决定回落到哪一页。 */
export function navItemAt(path: string): NavItem | null {
  return ROUTE_ITEMS.find((item) => item.path === path) ?? null;
}

/** 面包屑用：按路径取当前角色看到的菜单名；路径不存在时返回空串。 */
export function navLabelAt(path: string, roles: readonly RoleCode[]): string {
  const item = navItemAt(path);
  return item === null ? '' : navLabel(item, roles);
}

function rolesToUse(roles: readonly RoleCode[]): readonly RoleCode[] {
  // 未登录、固定样例模式都拿不到角色：这时显示全部入口，不假装用户有某个角色。
  return roles.length === 0 ? ALL_ROLES : roles;
}

export function isVisible(item: NavItem, roles: readonly RoleCode[]): boolean {
  const effective = rolesToUse(roles);
  return item.roles.some((role) => effective.includes(role));
}

/** 该角色看到的菜单名；没有按角色定制时用默认名。 */
export function navLabel(item: NavItem, roles: readonly RoleCode[]): string {
  for (const role of rolesToUse(roles)) {
    const override = item.labelByRole?.[role];
    if (override !== undefined) {
      return override;
    }
  }
  return item.label;
}

/** 按角色过滤后的分组；空分组不渲染，避免出现一个只有标题的栏目。 */
export function visibleGroups(roles: readonly RoleCode[]): NavGroup[] {
  return NAV_GROUPS.map((group) => ({
    name: group.name,
    items: group.items.filter((item) => isVisible(item, roles)),
  })).filter((group) => group.items.length > 0);
}

/** 某个 hash 是否属于当前角色可见的入口；不可见时 App 不会渲染它。 */
export function isVisiblePath(path: string, roles: readonly RoleCode[]): boolean {
  const item = ROUTE_ITEMS.find((candidate) => candidate.path === path);
  return item !== undefined && isVisible(item, roles);
}
