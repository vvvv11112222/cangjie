/**
 * 账号入口：顶栏右上角的头像按钮 + 下拉面板。
 *
 * 面板按《系统草图.html》的"切换演示角色"弹窗做成两步：
 *   1) pick     —— 列出可选账号（圆形首字 + 「角色 · 姓名」+「学院 · 账号」的卡片）；
 *   2) password —— 选中账号后只填密码。
 * 已登录时点"切换账号"回到第一步，**不先退出**：取消就等于什么都没发生，
 * 只有真正登录成功才会替换会话（协议第 1.1 节：登录成功轮换会话与 CSRF 令牌）。
 *
 * "切换账号"就用 /auth/login，后端没有也不需要额外的切换接口。
 * 固定样例模式（未接入后端）下只说明当前模式，不给点了必然失败的登录按钮。
 * 文件名保留 AccountBar 是为了不动仓库里的其他引用；组件本身已经是一个菜单。
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { useAuth } from '../auth';
import { isApiMode } from '../data/sources';
import { accountActionLabel, roleLabel } from '../format';
import { IconChevronDown, IconUser } from './icons';
import { PasswordForm } from './ManagementActions';

/**
 * 开发模式下的演示账号，取自 README 的 seed-dev（密码来自本机环境变量，仓库里没有）。
 * 只在开发构建里显示：正式构建的产物里没有这一段，避免把演示身份当成真实登录。
 */
const DEMO_ACCOUNTS = [
  { username: 'admin', role: '系统管理员', person: '管理员', scope: '全校' },
  { username: 'academic_demo', role: '学院教务', person: '林老师', scope: '计算机学院' },
  { username: 'supervisor_demo', role: '教学督导', person: '陈知远', scope: '计算机学院' },
  { username: 'teacher_demo', role: '任课教师', person: '周明', scope: '计算机学院' },
] as const;

export function AccountBar({
  open: controlledOpen,
  onOpenChange,
}: {
  /** 传了就由外部控制开合（侧栏底部的"当前身份"按钮也要能打开这个面板）。 */
  open?: boolean;
  onOpenChange?(open: boolean): void;
} = {}): ReactElement {
  const { status, me, error, busy, login, logout, clearError } = useAuth();
  const api = isApiMode();
  const [innerOpen, setInnerOpen] = useState(false);
  const open = controlledOpen ?? innerOpen;
  const setOpen = useCallback(
    (next: boolean | ((value: boolean) => boolean)) => {
      const value = typeof next === 'function' ? next(open) : next;
      if (onOpenChange === undefined) {
        setInnerOpen(value);
      } else {
        onOpenChange(value);
      }
    },
    [open, onOpenChange],
  );
  const [step, setStep] = useState<'pick' | 'password'>('pick');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [changingPassword, setChangingPassword] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const authenticated = status === 'authenticated' && me !== null;
  // 演示账号列表只在开发构建里出现（正式产物不含这段代码）。
  const showDemoAccounts = api && import.meta.env.DEV;

  // 浮层不关掉会一直挡着页面内容：点面板外面或按 Esc 都关闭。
  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const onPointerDown = (event: MouseEvent): void => {
      if (wrapRef.current !== null && !wrapRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open, setOpen]);

  // 关闭时把面板复位：下次打开是干净的"选择账号"，不留上一个人的账号名。
  useEffect(() => {
    if (!open) {
      setStep('pick');
      setPassword('');
    }
  }, [open]);

  // 进入输密码这一步时把焦点放进密码框，键盘操作不用再 Tab 两次。
  useEffect(() => {
    if (open && step === 'password') {
      passwordRef.current?.focus();
    }
  }, [open, step]);

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    const ok = await login(username, password);
    if (ok) {
      setPassword('');
      setStep('pick');
      setOpen(false);
    }
  }

  const triggerName = authenticated
    ? me.display_name
    : api
      ? busy
        ? '检查登录状态…'
        : '登录'
      : '样例模式';
  const picked = DEMO_ACCOUNTS.find((item) => item.username === username) ?? null;

  return (
    <div className="account-menu-wrap" ref={wrapRef}>
      <button
        type="button"
        className="account-trigger"
        aria-expanded={open}
        aria-haspopup="true"
        aria-controls="account-menu"
        onClick={() => {
          clearError();
          setOpen((value) => !value);
        }}
      >
        <span className="account-avatar" aria-hidden="true">
          {authenticated ? me.display_name.slice(0, 1) : <IconUser size={17} />}
        </span>
        <span className="account-trigger-name">{triggerName}</span>
        <IconChevronDown size={14} className={open ? 'is-open' : undefined} />
      </button>

      {open ? (
        <div className="account-menu" id="account-menu" role="dialog" aria-label="账号">
          {!api ? (
            <p className="account-meta">
              固定样例模式：未接入后端，页面读取 contracts/examples 的示例数据。
              设 VITE_DATA_SOURCE=api 后可登录。
            </p>
          ) : step === 'pick' ? (
            <>
              <p className="account-menu-title">{authenticated ? '切换账号' : '登录'}</p>
              <p className="account-menu-sub">
                选择账号后输入密码即可登录；真实身份认证由服务端负责。
              </p>
              {showDemoAccounts ? (
                <div className="account-options">
                  {DEMO_ACCOUNTS.map((item) => (
                    <button
                      key={item.username}
                      type="button"
                      className={`account-option${username === item.username ? ' active' : ''}`}
                      onClick={() => {
                        clearError();
                        setUsername(item.username);
                        setPassword('');
                        setStep('password');
                      }}
                    >
                      <span className="account-avatar" aria-hidden="true">
                        {item.person.slice(0, 1)}
                      </span>
                      <span>
                        <strong>{item.role} · {item.person}</strong>
                        <p>{item.scope} · {item.username}</p>
                      </span>
                    </button>
                  ))}
                </div>
              ) : null}
              {authenticated ? (
                <>
                  <p className="account-line">
                    当前：{me.display_name} ·{' '}
                    {me.roles.length === 0 ? '无角色' : me.roles.map((role) => roleLabel(role.role_code)).join('、')}
                  </p>
                  <p className="account-line">
                    账号可用操作：
                    {me.allowed_actions.length === 0
                      ? '无'
                      : me.allowed_actions.map(accountActionLabel).join('、')}
                  </p>
                </>
              ) : null}
              <div className="account-actions">
                {authenticated && <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => setChangingPassword(true)}>修改密码</button>}
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() => {
                    setUsername('');
                    setPassword('');
                    clearError();
                    setStep('password');
                  }}
                >
                  用其他账号登录
                </button>
                {authenticated ? (
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    disabled={busy}
                    onClick={() => {
                      clearError();
                      void logout();
                    }}
                  >
                    {busy ? '正在退出…' : '退出登录'}
                  </button>
                ) : null}
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() => setOpen(false)}
                >
                  取消
                </button>
              </div>
              {authenticated && changingPassword && <PasswordForm onCancel={() => setChangingPassword(false)} onDone={async () => { setChangingPassword(false); await logout(); }}/> }
            </>
          ) : (
            <>
              <p className="account-menu-title">{authenticated ? '切换账号' : '登录'}</p>
              <div className="account-options">
                <span className="account-option static">
                  <span className="account-avatar" aria-hidden="true">
                    {picked === null ? <IconUser size={17} /> : picked.person.slice(0, 1)}
                  </span>
                  <span>
                    <strong>
                      {picked === null ? username.trim() || '手动输入账号' : `${picked.role} · ${picked.person}`}
                    </strong>
                    <p>{picked === null ? '手动输入账号登录' : `${picked.scope} · ${picked.username}`}</p>
                  </span>
                </span>
              </div>
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
                    ref={passwordRef}
                    name="password"
                    type="password"
                    value={password}
                    autoComplete="current-password"
                    onChange={(event) => setPassword(event.target.value)}
                    required
                  />
                </label>
                <div className="account-actions">
                  <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>
                    {busy ? '登录中…' : '登录'}
                  </button>
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() => {
                      setStep('pick');
                      clearError();
                    }}
                  >
                    上一步
                  </button>
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() => {
                      setOpen(false);
                      clearError();
                    }}
                  >
                    取消
                  </button>
                </div>
              </form>
            </>
          )}
          {error === null ? null : <p className="account-error">{error}</p>}
          {api ? <p className="account-meta">数据来源：接口数据 · 走 /api/v1</p> : null}
        </div>
      ) : null}
    </div>
  );
}
