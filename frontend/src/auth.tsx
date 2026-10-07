/**
 * 前端登录状态（React 侧）：真正的会话规则在 data/sessionFlow.ts，这里只做状态保存与分发。
 *
 * 为什么单独放一层：三个页面都需要知道"当前是哪个会话、账号有哪些 allowed_actions"，
 * 如果各自去取，就会出现三次 /auth/me 请求和三种不一致的状态。这里在应用根部取一次，
 * 页面通过 useAuth() 读：sessionKey 变化（登录、退出、换账号）时页面数据自动重新加载。
 *
 * 固定样例模式（VITE_DATA_SOURCE 未设为 api）没有后端，状态固定为 offline，
 * 页面也不再请求接口，展示与 M0 一致。
 */

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactElement, ReactNode } from 'react';
import type { AccountAction, AccountMe } from './data/authApi';
import { isApiMode } from './data/sources';
import { CHECKING, OFFLINE, bootstrapSession, capabilitiesOf, sessionKeyOf, signIn, signOut } from './data/sessionFlow';
import type { AuthStatus, SessionState } from './data/sessionFlow';

export type { AuthStatus };
export { sessionKeyOf };

export interface AuthContextValue {
  status: AuthStatus;
  me: AccountMe | null;
  error: string | null;
  /** 账号级能力，用于账号区展示；资源能否操作以各资源返回的 allowed_actions 为准。 */
  capabilities: readonly AccountAction[] | null;
  /** 会话标识；登录、退出、换账号都会变化，页面据此重新取数。 */
  sessionKey: string;
  login(username: string, password: string): Promise<boolean>;
  logout(): Promise<void>;
  clearError(): void;
}

const OFFLINE_VALUE: AuthContextValue = {
  status: 'offline',
  me: null,
  error: null,
  capabilities: null,
  sessionKey: 'offline',
  async login() {
    return false;
  },
  async logout() {},
  clearError() {},
};

const AuthContext = createContext<AuthContextValue>(OFFLINE_VALUE);

export function AuthProvider({ children }: { children: ReactNode }): ReactElement {
  const api = isApiMode();
  const [session, setSession] = useState<SessionState>(() => (api ? CHECKING : OFFLINE));
  // signOut 需要在失败时原样保留当前会话，用一个 ref 读到最新的 session。
  const sessionRef = useRef(session);
  sessionRef.current = session;

  useEffect(() => {
    if (!api) {
      return;
    }
    let cancelled = false;
    void (async () => {
      const next = await bootstrapSession();
      if (!cancelled) {
        setSession(next);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [api]);

  const login = useCallback(
    async (username: string, password: string): Promise<boolean> => {
      if (!api) {
        setSession((current) => ({ ...current, error: '固定样例模式未接入后端，无法登录。' }));
        return false;
      }
      setSession(CHECKING);
      // 登录成功后 me 变化会让 sessionKeyOf 改变，页面钩子据此重新取数。
      const next = await signIn(username, password);
      setSession(next);
      return next.status === 'authenticated';
    },
    [api],
  );

  const logout = useCallback(async (): Promise<void> => {
    if (!api) {
      return;
    }
    // 服务端撤销失败时 signOut 会保留当前会话，界面据此显示"退出失败、可重试"。
    setSession(await signOut(sessionRef.current));
  }, [api]);

  const clearError = useCallback(() => setSession((current) => ({ ...current, error: null })), []);

  const value = useMemo<AuthContextValue>(
    () => ({
      status: session.status,
      me: session.me,
      error: session.error,
      capabilities: capabilitiesOf(session),
      sessionKey: sessionKeyOf(session),
      login,
      logout,
      clearError,
    }),
    [session, login, logout, clearError],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

/** 没有 Provider 时返回 offline 默认值，页面测试可以单独渲染而不必包一层。 */
export function useAuth(): AuthContextValue {
  return useContext(AuthContext);
}
