/**
 * 账号与权限（《系统草图.html》的 ACCOUNTS & PERMISSIONS）。
 *
 * 结构和草图一致：页头 + 工具条（所属学院 / 角色 / 搜索）+ 账号表格 + 新增账号。
 * 只调 /api/v1/users*，权限判断交给服务端：学院教务只能管本学院的教师与督导，
 * 越权会返回 403/404，页面把服务端的错误原文显示出来。
 *
 * 与草图的两处差异都是接口缺口，不是漏做：
 *   1) 草图表格有"角色 / 功能权限"列。后端没有读取用户已有角色绑定的端点
 *      （只有 POST 与按 binding_id 的 DELETE），所以这一列现在显示不出来；
 *      补齐需要在后端加 GET /users/{id}/role-bindings。
 *   2) 草图有"删除账号"。协议里没有 DELETE /users/{id}，所以不放这个按钮。
 * 固定样例模式（未接后端）下只给一句说明，不编假账号。
 */

import { useCallback, useEffect, useMemo, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Card, EmptyState } from '../components/ui';
import { IconAlert, IconInfo, IconShield } from '../components/icons';
import { describeError } from '../data/errors';
import { isApiMode } from '../data/sources';
import { ROLE_CODES } from '../data/authApi';
import type { RoleCode } from '../data/authApi';
import { roleLabel } from '../format';
import { listOrgUnits, orgUnitName } from '../data/academicApi';
import type { OrgUnit } from '../data/academicApi';
import {
  USER_STATUSES,
  USER_STATUS_LABEL,
  createRoleBinding,
  createUser,
  listUsers,
  patchUser,
} from '../data/identityApi';
import type { ManagedUser, UserStatus } from '../data/identityApi';

const MAX_PAGES = 5;

async function loadAllUsers(): Promise<{ items: ManagedUser[]; hasMore: boolean }> {
  const items: ManagedUser[] = [];
  let cursor: string | undefined;
  for (let index = 0; index < MAX_PAGES; index += 1) {
    const page = await listUsers(cursor);
    items.push(...page.items);
    if (page.nextCursor === null) {
      return { items, hasMore: false };
    }
    cursor = page.nextCursor;
  }
  return { items, hasMore: true };
}

export function AccountsPage(): ReactElement {
  return isApiMode() ? <AccountsView /> : (
    <div className="page">
      <AccountsHead roleCount={0} />
      <p className="notice-strip">
        <IconInfo size={14} aria-hidden="true" />
        固定样例模式未接入后端：这一页读写 /api/v1/users 与 /users/{'{id}'}/role-bindings。
        设 VITE_DATA_SOURCE=api 并启动 Go 服务后可用。
      </p>
    </div>
  );
}

function AccountsHead({ roleCount }: { roleCount: number }): ReactElement {
  return (
    <header className="page-head">
      <div>
        <p className="eyebrow">ACCOUNTS &amp; PERMISSIONS</p>
        <h1>账号与权限</h1>
        <p className="muted">
          系统管理员不隶属任何学院；学院教务按所属学院管理教师与督导账号。
          能否修改由服务端逐次判断，页面只是按结果展示。
          {roleCount === 0 ? '' : `（本页可授予 ${roleCount} 种角色）`}
        </p>
      </div>
    </header>
  );
}

function AccountsView(): ReactElement {
  const [users, setUsers] = useState<ManagedUser[] | null>(null);
  const [units, setUnits] = useState<OrgUnit[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [editing, setEditing] = useState<{ id: string | null } | null>(null);
  /** 授予成功后记下返回值：没有读取绑定的接口，只能显示刚授出去的那一条。 */
  const [granted, setGranted] = useState<{ user: string; role: RoleCode } | null>(null);

  const reload = useCallback(async () => {
    setError(null);
    try {
      const [userPage, unitPage] = await Promise.all([loadAllUsers(), listOrgUnits()]);
      setUsers(userPage.items);
      setUnits(unitPage.items);
      setHasMore(userPage.hasMore);
    } catch (caught) {
      setError(describeError(caught));
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const shown = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return (users ?? []).filter(
      (user) =>
        (statusFilter === '' || user.status === statusFilter) &&
        (keyword === '' ||
          user.displayName.toLowerCase().includes(keyword) ||
          user.username.toLowerCase().includes(keyword)),
    );
  }, [users, query, statusFilter]);

  async function saveUser(
    id: string | null,
    input: { displayName: string; status: UserStatus; username: string; orgUnitId: string; password: string },
  ): Promise<string | null> {
    try {
      if (id === null) {
        await createUser({
          orgUnitId: input.orgUnitId,
          username: input.username,
          displayName: input.displayName,
          status: input.status,
          initialPassword: input.password,
        });
      } else {
        await patchUser(id, { displayName: input.displayName, status: input.status });
      }
      setEditing(null);
      await reload();
      return null;
    } catch (caught) {
      return describeError(caught);
    }
  }

  async function grantRole(userId: string, role: RoleCode, scopeOrgId: string | null): Promise<string | null> {
    try {
      const binding = await createRoleBinding(userId, role, scopeOrgId);
      setGranted({ user: userId, role: binding.roleCode });
      return null;
    } catch (caught) {
      return describeError(caught);
    }
  }

  const ready = users !== null;

  return (
    <div className="page">
      <AccountsHead roleCount={ROLE_CODES.length} />

      {error === null ? null : (
        <p className="notice-strip">
          <IconAlert size={14} aria-hidden="true" />
          读取账号失败：{error}
        </p>
      )}
      {hasMore ? (
        <p className="notice-strip">
          <IconInfo size={14} aria-hidden="true" />
          账号较多，这里只加载了前 {MAX_PAGES} 页（每页最多 100 条）。
        </p>
      ) : null}
      {ready ? null : <p className="load-note">正在读取账号……</p>}

      {ready ? (
        <>
          {editing === null ? (
            <button type="button" className="btn btn-primary btn-sm" onClick={() => setEditing({ id: null })}>
              新增账号
            </button>
          ) : (
            <UserForm
              units={units}
              user={editing.id === null ? null : (users ?? []).find((item) => item.id === editing.id) ?? null}
              onCancel={() => setEditing(null)}
              onSave={(input) => saveUser(editing.id, input)}
            />
          )}

          <Card
            title="账号"
            icon={<IconShield size={16} />}
            subtitle={`共 ${shown.length} / ${users?.length ?? 0} 个账号；角色绑定只能在授予时看到结果`}
          >
            <div className="toolbar">
              <div className="toolbar-actions">
                <label className="account-field">
                  <span>状态</span>
                  <select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
                    <option value="">全部状态</option>
                    {USER_STATUSES.map((value) => (
                      <option key={value} value={value}>
                        {USER_STATUS_LABEL[value]}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="account-field">
                  <span>搜索账号</span>
                  <input
                    type="search"
                    placeholder="搜索姓名、账号"
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                  />
                </label>
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() => {
                    setQuery('');
                    setStatusFilter('');
                  }}
                >
                  重置
                </button>
              </div>
            </div>

            {shown.length === 0 ? (
              <EmptyState text="没有符合条件的账号。可以清空搜索或状态筛选。" />
            ) : (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>姓名 / 账号</th>
                      <th>所属学院 / 院系</th>
                      <th>状态</th>
                      <th>授予角色</th>
                      <th>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {shown.map((user) => (
                      <tr key={user.id}>
                        <td>
                          <strong>{user.displayName}</strong>
                          <div className="cell-meta mono">{user.username}</div>
                        </td>
                        <td>{orgUnitName(units, user.orgUnitId)}</td>
                        <td>{USER_STATUS_LABEL[user.status]}</td>
                        <td>
                          <select
                            aria-label={`给 ${user.displayName} 授予角色`}
                            defaultValue=""
                            onChange={(event) => {
                              const role = event.target.value as RoleCode | '';
                              if (role === '') {
                                return;
                              }
                              void grantRole(user.id, role, user.orgUnitId);
                              setError(null);
                              event.target.value = '';
                            }}
                          >
                            <option value="">选择角色…</option>
                            {ROLE_CODES.map((role) => (
                              <option key={role} value={role}>
                                {roleLabel(role)}
                              </option>
                            ))}
                          </select>
                          {granted !== null && granted.user === user.id ? (
                            <div className="cell-meta">最近授予：{roleLabel(granted.role)}</div>
                          ) : null}
                        </td>
                        <td>
                          <button
                            type="button"
                            className="btn btn-secondary btn-sm"
                            onClick={() => setEditing({ id: user.id })}
                          >
                            编辑
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            <div className="table-footer">
              <span>
                共 {shown.length} 条记录，已按当前角色过滤
              </span>
              <span className="muted tiny">
                后端暂无"读取用户角色绑定"的接口，所以这里不显示已有角色；补齐后才能像草图那样列出角色与权限
              </span>
            </div>
          </Card>
        </>
      ) : null}
    </div>
  );
}

function UserForm({
  units,
  user,
  onCancel,
  onSave,
}: {
  units: readonly OrgUnit[];
  user: ManagedUser | null;
  onCancel(): void;
  onSave(input: {
    displayName: string;
    status: UserStatus;
    username: string;
    orgUnitId: string;
    password: string;
  }): Promise<string | null>;
}): ReactElement {
  const [displayName, setDisplayName] = useState(user?.displayName ?? '');
  const [username, setUsername] = useState(user?.username ?? '');
  const [orgUnitId, setOrgUnitId] = useState(user?.orgUnitId ?? units[0]?.id ?? '');
  const [status, setStatus] = useState<UserStatus>(user?.status ?? 'active');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (user === null && password.length < 12) {
      setError('初始密码至少 12 位（服务端同样要求）。');
      return;
    }
    setBusy(true);
    const message = await onSave({ displayName: displayName.trim(), status, username, orgUnitId, password });
    setBusy(false);
    setError(message);
  }

  return (
    <Card title={user === null ? '新增账号' : `编辑：${user.displayName}`} subtitle="保存后会重新读取账号列表。">
      <form className="directory-form" onSubmit={submit}>
        <label className="account-field">
          <span>姓名</span>
          <input value={displayName} onChange={(event) => setDisplayName(event.target.value)} required />
        </label>
        <label className="account-field">
          <span>登录账号</span>
          <input
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            disabled={user !== null}
            required
          />
        </label>
        <label className="account-field">
          <span>所属学院 / 院系</span>
          <select
            value={orgUnitId}
            disabled={user !== null}
            onChange={(event) => setOrgUnitId(event.target.value)}
            required
          >
            {units.map((unit) => (
              <option key={unit.id} value={unit.id}>
                {unit.name}
              </option>
            ))}
          </select>
        </label>
        <label className="account-field">
          <span>状态</span>
          <select value={status} onChange={(event) => setStatus(event.target.value as UserStatus)}>
            {USER_STATUSES.map((value) => (
              <option key={value} value={value}>
                {USER_STATUS_LABEL[value]}
              </option>
            ))}
          </select>
        </label>
        {user === null ? (
          <label className="account-field">
            <span>初始密码（≥12 位）</span>
            <input
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              required
            />
          </label>
        ) : null}
        <div className="directory-actions">
          <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>
            {busy ? '保存中…' : '保存'}
          </button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={onCancel}>
            取消
          </button>
        </div>
        {error === null ? null : (
          <p className="notice-strip">
            <IconAlert size={14} aria-hidden="true" />
            保存失败：{error}
          </p>
        )}
        {user === null ? null : (
          <p className="muted tiny">登录账号与所属学院不支持在这里修改（PATCH 不传该字段即保持不变）。</p>
        )}
      </form>
    </Card>
  );
}
