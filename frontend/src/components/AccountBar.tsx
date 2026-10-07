/**
 * 账号区：放在侧栏里，所以宽屏在左边、窄屏（≤900px）随侧栏一起变成顶部栏。
 *
 * 未登录时只显示一个「登录」按钮，点开才展开输入框 —— 登录是低频操作，
 * 常驻两行输入框会一直挤占空间；窄屏顶栏只有一行高，更放不下。
 * 固定样例模式没有后端，这里只说明当前模式，不给一个点了必然失败的按钮。
 */

import { useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { useAuth } from '../auth';
import { isApiMode } from '../data/sources';
import { accountActionLabel, roleLabel } from '../format';

export function AccountBar(): ReactElement {
  const { status, me, error, busy, login, logout, clearError } = useAuth();
  const [formOpen, setFormOpen] = useState(false);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');

  if (!isApiMode()) {
    return (
      <div className="account-bar">
        <p className="account-title">样例模式</p>
        <p className="account-sub">未接入后端；设 VITE_DATA_SOURCE=api 后可登录</p>
      </div>
    );
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    const ok = await login(username, password);
    if (ok) {
      setPassword('');
      setFormOpen(false);
    }
  }

  if (status === 'authenticated' && me !== null) {
    return (
      <div className="account-bar">
        <p className="account-title" title={me.user_id}>
          {me.display_name}
        </p>
        <p className="account-sub">
          {me.roles.length === 0 ? '无角色' : me.roles.map((role) => roleLabel(role.role_code)).join('、')}
        </p>
        <p className="account-sub">
          可用操作：
          {me.allowed_actions.length === 0
            ? '无'
            : me.allowed_actions.map(accountActionLabel).join('、')}
        </p>
        <button
          type="button"
          className="account-button"
          disabled={busy}
          onClick={() => {
            clearError();
            void logout();
          }}
        >
          {busy ? '正在退出…' : '退出'}
        </button>
        {error === null ? null : <p className="account-error">{error}</p>}
      </div>
    );
  }


  return (
    <div className="account-bar">
      {formOpen ? (
        <form className="account-form" onSubmit={handleSubmit}>
          <label className="account-field">
            <span>用户名</span>
            <input
              name="username"
              value={username}
              autoComplete="username"
              onChange={(event) => setUsername(event.target.value)}
              required
            />
          </label>
          <label className="account-field">
            <span>密码</span>
            <input
              name="password"
              type="password"
              value={password}
              autoComplete="current-password"
              onChange={(event) => setPassword(event.target.value)}
              required
            />
          </label>
          <div className="account-actions">
            <button type="submit" className="account-button" disabled={busy}>
              {busy ? '登录中…' : '登录'}
            </button>
            <button
              type="button"
              className="account-button account-button-plain"
              onClick={() => {
                setFormOpen(false);
                clearError();
              }}
            >
              取消
            </button>
          </div>
        </form>
      ) : (
        <button
          type="button"
          className="account-button"
          onClick={() => {
            clearError();
            setFormOpen(true);
          }}
        >
          {busy ? '检查登录状态…' : '登录'}
        </button>
      )}
      {error === null ? null : <p className="account-error">{error}</p>}
    </div>
  );
}
