import { ErrorMessage } from '../components/ui';
/** 账号与权限：布局对齐《系统草图.html》，读写只经过 Go HTTP API。 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useAuth } from '../auth';
import { PasswordForm } from '../components/ManagementActions';
import type { FormEvent, ReactElement } from 'react';
import { EmptyState } from '../components/ui';
import { IconAlert, IconInfo, IconPlus } from '../components/icons';
import { describeError } from '../data/errors';
import { isApiMode } from '../data/sources';
import { ROLE_CODES } from '../data/authApi';
import type { RoleCode } from '../data/authApi';
import { roleLabel } from '../format';
import { listOrgUnits, orgUnitName } from '../data/academicApi';
import type { OrgUnit } from '../data/academicApi';
import { USER_STATUSES, USER_STATUS_LABEL, createRoleBinding, createUser, listUsers, patchUser, listRoleBindings, deleteRoleBinding } from '../data/identityApi';
import type { ManagedUser, UserStatus, RoleBinding } from '../data/identityApi';
import { collectPages } from '../data/pagination';

const MAX_PAGES = 5;
const ROLE_PERMISSIONS: Record<RoleCode, string> = {
  sys_admin: '全校最高权限',
  academic_admin: '本学院课表、督导与教师权限管理',
  supervisor: '查看录像与报告、编辑草稿、复核教学观察、发布教学报告',
  teacher: '查看录像与报告、上传课堂录像、发起分析任务、编辑草稿',
};

async function loadAllUsers(): Promise<{ items: ManagedUser[]; hasMore: boolean }> {
  const items: ManagedUser[] = []; let cursor: string | undefined;
  for (let index = 0; index < MAX_PAGES; index += 1) {
    const page = await listUsers(cursor); items.push(...page.items);
    if (page.nextCursor === null) return { items, hasMore: false };
    cursor = page.nextCursor;
  }
  return { items, hasMore: true };
}

function AccountsHead({ count, onCreate }: { count: number | null; onCreate?: () => void }): ReactElement {
  return <header className="page-head accounts-page-head"><div><p className="eyebrow">ACCOUNTS &amp; PERMISSIONS</p><h1>全校账号与权限</h1><p className="muted">系统管理员不隶属任何学院；其他角色按所属学院管理。</p></div>{onCreate ? <div className="page-actions"><button type="button" className="btn btn-primary" onClick={onCreate}><IconPlus size={16}/>新增账号</button></div> : null}{count === null ? null : <span className="sr-only">共 {count} 个账号</span>}</header>;
}

export function AccountsPage(): ReactElement {
  return <AccountsView offline={!isApiMode()}/>;
}

function parentCollege(units: readonly OrgUnit[], id: string | null): string | null {
  let current = id === null ? undefined : units.find(unit => unit.id === id); const seen = new Set<string>();
  while (current !== undefined && !seen.has(current.id)) {
    if (current.kind === 'college') return current.id;
    seen.add(current.id); current = current.parentId === null ? undefined : units.find(unit => unit.id === current?.parentId);
  }
  return null;
}

function AccountsView({ offline }: { offline: boolean }): ReactElement {
  const { me } = useAuth();
  const [users, setUsers] = useState<ManagedUser[] | null>(offline ? [] : null); const [units, setUnits] = useState<OrgUnit[]>([]); const [hasMore, setHasMore] = useState(false); const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState(''); const [collegeFilter, setCollegeFilter] = useState(''); const [editing, setEditing] = useState<{ id: string | null } | null>(null); const [busyId, setBusyId] = useState<string | null>(null);
  const [reloading, setReloading] = useState(false); const [message, setMessage] = useState<string | null>(null);
  const restoreFocus = useRef<HTMLElement | null>(null);
  const reload = useCallback(async () => { if (offline) return; setReloading(true); setError(null); try { const [userPage, orgUnits] = await Promise.all([loadAllUsers(), collectPages(listOrgUnits)]); setUsers(userPage.items); setUnits(orgUnits); setHasMore(userPage.hasMore); } catch (cause) { setError(describeError(cause)); } finally { setReloading(false); } }, [offline]);
  useEffect(() => { void reload(); }, [reload]);
  const openEditor = useCallback((id: string | null) => { restoreFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; setMessage(null); setEditing({ id }); }, []);
  const closeEditor = useCallback(() => { setEditing(null); setTimeout(() => restoreFocus.current?.focus(), 0); }, []);
  const colleges = units.filter(unit => unit.kind === 'college');
  const shown = useMemo(() => { const keyword = query.trim().toLowerCase(); return (users ?? []).filter(user => (collegeFilter === '' || parentCollege(units, user.orgUnitId) === collegeFilter) && (keyword === '' || user.displayName.toLowerCase().includes(keyword) || user.username.toLowerCase().includes(keyword))); }, [users, units, collegeFilter, query]);
  const save = async (id: string | null, input: UserInput, role: RoleCode | ''): Promise<string | null> => {
    let createdAccount = false;
    try {
      const scoped = role === 'academic_admin' || role === 'supervisor';
      const scope = scoped ? parentCollege(units, input.orgUnitId) : null;
      if (scoped && scope === null) return '授予该角色前，请选择所属学院或该学院下的院系。';
      let userId = id;
      if (id === null) {
        const created = await createUser({ orgUnitId: input.orgUnitId, username: input.username, displayName: input.displayName, status: input.status, initialPassword: input.password });
        userId = created.id;
        createdAccount = true;
        // 授权失败后保留已创建的账号身份，重试只补授权，不再次创建账号。
        setUsers(previous => [...(previous ?? []), created]);
        setEditing({ id: created.id });
      } else {
        await patchUser(id, { displayName: input.displayName, status: input.status });
      }
      if (role !== '' && userId !== null) {
        await createRoleBinding(userId, role, scope);
      }
      closeEditor();
      await reload();
      setMessage(id === null ? '账号已创建。' : role === '' ? '账号资料已保存。' : '账号资料已保存，并已提交新角色授权。');
      return null;
    } catch (cause) {
      return `${createdAccount ? '账号已创建，角色尚未授予，可重试保存。' : ''}${describeError(cause)}`;
    }
  };
  const toggle = async (user: ManagedUser) => { setBusyId(user.id); setError(null); try { await patchUser(user.id, { displayName: user.displayName, status: user.status === 'active' ? 'disabled' : 'active' }); await reload(); } catch (cause) { setError(describeError(cause)); } finally { setBusyId(null); } };
  return <div className="page"><AccountsHead count={users?.length ?? 0} onCreate={offline || users === null ? undefined : () => openEditor(null)}/>{offline ? <p className="notice-strip"><IconInfo size={14} aria-hidden="true"/>固定样例模式不编造账号；切换到 API 模式并登录后读取真实数据。</p> : null}{error ? <div className="notice-strip"><IconAlert size={14} aria-hidden="true"/><div>读取或保存账号失败：<ErrorMessage text={error}/></div><button type="button" className="btn btn-secondary btn-sm" disabled={reloading} onClick={() => void reload()}>{reloading ? '重新读取中…' : '重新读取'}</button></div> : null}{message ? <p className="notice-strip" role="status">{message}</p> : null}{hasMore ? <p className="notice-strip"><IconInfo size={14} aria-hidden="true"/>账号较多，目前只加载前 {MAX_PAGES} 页。</p> : null}{users === null ? <p className="load-note">正在读取账号……</p> : <><div className="accounts-toolbar"><div className="filters management-filters"><select className="m1-filter-control" aria-label="账号所属学院" value={collegeFilter} onChange={event => setCollegeFilter(event.target.value)}><option value="">全部学院</option>{colleges.map(unit => <option key={unit.id} value={unit.id}>{unit.name}</option>)}</select><input className="m1-filter-control accounts-search" type="search" placeholder="搜索姓名、账号" aria-label="搜索账号" value={query} onChange={event => setQuery(event.target.value)}/></div><span className="muted">{shown.length} 个账号</span></div><section className="card accounts-table-card"><div className="table-wrap"><table className="table accounts-table"><thead><tr><th>姓名 / 账号</th><th>角色</th><th>所属学院</th><th>功能权限</th><th>状态</th><th>操作</th></tr></thead><tbody>{shown.map(user => { return <tr key={user.id}><td><strong>{user.displayName}</strong><div className="cell-meta mono">{user.username}</div></td><td><span className="muted">在账号详情中查看</span></td><td>{orgUnitName(units, user.orgUnitId)}</td><td className="permission-cell"><span className="muted">按角色及授权范围执行</span></td><td><span className={`badge ${user.status === 'active' ? 'badge-green' : 'badge-gray'}`}>{USER_STATUS_LABEL[user.status]}</span></td><td><div className="accounts-row-actions"><button type="button" className="btn btn-secondary btn-sm" onClick={() => openEditor(user.id)}>账号资料 / 授予新角色</button><button type="button" className="btn btn-secondary btn-sm" disabled={busyId === user.id} onClick={() => void toggle(user)}>{busyId === user.id ? '保存中…' : user.status === 'active' ? '停用' : '启用'}</button></div></td></tr>; })}</tbody></table></div>{shown.length === 0 ? <div className="accounts-empty-state"><EmptyState text="没有符合条件的账号。"/></div> : null}<div className="accounts-table-footer"><span>共 {shown.length} 条记录，已按当前学院范围过滤</span><span>账号详情支持读取与撤销已有角色</span></div></section></>}{editing ? <div className="accounts-editor-backdrop" role="presentation"><div className="accounts-editor" role="dialog" aria-modal="true" aria-labelledby="account-editor-title"><UserForm units={units} user={editing.id === null ? null : users?.find(user => user.id === editing.id) ?? null} onCancel={closeEditor} onSave={(input, role) => save(editing.id, input, role)}/>{editing.id && <RoleBindings key={editing.id} userId={editing.id} units={units}/>}{editing.id && editing.id !== me?.user_id && <PasswordForm key={`password:${editing.id}`} userId={editing.id} onCancel={closeEditor} onDone={() => { closeEditor(); setMessage('密码已重置，该账号所有设备须使用新密码登录。'); }}/>}</div></div> : null}</div>;
}

interface UserInput { displayName: string; status: UserStatus; username: string; orgUnitId: string; password: string }
function UserForm({ units, user, onCancel, onSave }: { units: readonly OrgUnit[]; user: ManagedUser | null; onCancel(): void; onSave(input: UserInput, role: RoleCode | ''): Promise<string | null> }): ReactElement {
  const [displayName, setDisplayName] = useState(user?.displayName ?? ''); const [username, setUsername] = useState(user?.username ?? ''); const [orgUnitId, setOrgUnitId] = useState(user?.orgUnitId ?? units[0]?.id ?? ''); const [status, setStatus] = useState<UserStatus>(user?.status ?? 'active'); const [password, setPassword] = useState(''); const [role, setRole] = useState<RoleCode | ''>(''); const [error, setError] = useState<string | null>(null); const [busy, setBusy] = useState(false);
  const formRef = useRef<HTMLFormElement>(null);
  const busyRef = useRef(busy);
  busyRef.current = busy;
  useEffect(() => {
    const dialog = formRef.current?.closest<HTMLElement>('[role="dialog"]');
    const initial = formRef.current?.querySelector<HTMLElement>('input:not([disabled]), select:not([disabled]), textarea:not([disabled]), button:not([disabled])');
    initial?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busyRef.current) { event.preventDefault(); onCancel(); return; }
      if (event.key !== 'Tab') return;
      const currentDialog = event.currentTarget as HTMLElement;
      const focusable = [...currentDialog.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])')].filter(element => !element.hasAttribute('hidden'));
      const first = focusable[0]; const last = focusable.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    };
    dialog?.addEventListener('keydown', onKeyDown);
    return () => dialog?.removeEventListener('keydown', onKeyDown);
  }, [onCancel]);
  const submit = async (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); if (busy) return; if (user === null && password.length < 12) { setError('初始密码至少 12 位。'); return; } setBusy(true); setError(await onSave({ displayName: displayName.trim(), status, username, orgUnitId, password }, role)); setBusy(false); };
  return <><header className="accounts-editor-head"><div><p className="eyebrow">ACCOUNT &amp; PERMISSIONS</p><h2 id="account-editor-title">{user === null ? '新增账号' : `账号资料：${user.displayName}`}</h2><p className="muted small">{user === null ? '账号资料与角色绑定分别由服务端校验。' : '可修改账号资料并授予新角色；已有角色可在下方查询和撤销。'}</p></div><button type="button" className="icon-button" aria-label="关闭" disabled={busy} onClick={onCancel}>×</button></header><form ref={formRef} className="accounts-editor-form" onSubmit={submit}><label className="account-field"><span>姓名</span><input value={displayName} onChange={event => setDisplayName(event.target.value)} required/></label><label className="account-field"><span>登录账号</span><input value={username} onChange={event => setUsername(event.target.value)} disabled={user !== null} required/></label><label className="account-field"><span>所属学院 / 院系</span><select value={orgUnitId} disabled={user !== null} onChange={event => setOrgUnitId(event.target.value)} required>{units.map(unit => <option key={unit.id} value={unit.id}>{unit.name}</option>)}</select></label><label className="account-field"><span>状态</span><select value={status} onChange={event => setStatus(event.target.value as UserStatus)}>{USER_STATUSES.map(value => <option key={value} value={value}>{USER_STATUS_LABEL[value]}</option>)}</select></label><label className="account-field"><span>授予新角色（可选）</span><select value={role} onChange={event => setRole(event.target.value as RoleCode | '')}><option value="">不新增角色</option>{ROLE_CODES.map(value => <option key={value} value={value}>{roleLabel(value)}</option>)}</select></label>{user === null ? <label className="account-field"><span>初始密码（至少 12 位）</span><input type="password" autoComplete="new-password" value={password} onChange={event => setPassword(event.target.value)} required/></label> : null}<div className="accounts-permission-preview"><strong>新角色权限</strong><p>{role === '' ? '本次不新增角色绑定。' : ROLE_PERMISSIONS[role]}</p></div>{error ? <p className="notice-strip"><IconAlert size={14} aria-hidden="true"/>保存失败：{error}</p> : null}<div className="form-actions"><button type="button" className="btn btn-secondary" disabled={busy} onClick={onCancel}>取消</button><button type="submit" className="btn btn-primary" disabled={busy}>{busy ? '保存中…' : '保存'}</button></div></form></>;
}

function RoleBindings({userId, units}: {userId: string; units: readonly OrgUnit[]}): ReactElement {
  const [roles, setRoles] = useState<RoleBinding[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    let live = true; setRoles(null); setError(null);
    void collectPages(cursor => listRoleBindings(userId, cursor)).then(items => { if (live) setRoles(items); }).catch(cause => { if (live) setError(describeError(cause)); });
    return () => { live = false; };
  }, [userId, reload]);
  async function revoke(role: RoleBinding): Promise<void> {
    if (busy || !window.confirm(`撤销“${roleLabel(role.roleCode)}”角色？该账号将失去此角色授予的权限。`)) return;
    setBusy(true); setError(null);
    try { await deleteRoleBinding(userId, role.id); setReload(n => n + 1); }
    catch (cause) { setError(describeError(cause)); }
    finally { setBusy(false); }
  }
  return <section className="accounts-editor-form account-role-bindings" aria-label="已有角色"><h3>已有角色与授权范围</h3>{error && <p role="alert">{error}<button type="button" disabled={busy} onClick={() => setReload(n => n + 1)}>重新读取角色</button></p>}{roles === null ? !error && <p>正在读取角色…</p> : roles.length === 0 ? <p>该账号没有角色绑定。</p> : <ul>{roles.map(role => <li key={role.id}><span>{roleLabel(role.roleCode)} · {role.scopeOrgId ? orgUnitName(units, role.scopeOrgId) : role.roleCode === 'sys_admin' ? '全校' : '所授课程'}</span><button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void revoke(role)}>撤销角色</button></li>)}</ul>}</section>;
}
