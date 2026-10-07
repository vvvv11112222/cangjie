/**
 * 前端登录状态。
 *
 * 为什么单独放一层：三个页面都需要知道"当前账号有哪些 allowed_actions"，
 * 如果各自去取，就会出现三次 /auth/me 请求和三种不一致的状态。
 * 这里在应用根部取一次，页面通过 useAuth() 读。
 *
 * 固定样例模式（VITE_DATA_SOURCE 未设为 api）没有后端，状态固定为 offline，
 * capabilities 为 null —— 页面据此不做过滤，保持 M0 的展示。
 */

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactElement, ReactNode } from 'react';
import type { AccountAction, AccountMe } from './data/authApi';
import { fetchCsrfToken, fetchMe, login as requestLogin, logout as requestLogout } from './data/authApi';
import { ApiError, describeError } from './data/errors';
import { clearCsrfToken, currentCsrfToken } from './data/http';
import { isApiMode } from './data/sources';

export type AuthStatus = 'offline' | 'checking' | 'anonymous' | 'authenticated';

export interface AuthContextValue {
  status: AuthStatus;
  me: AccountMe | null;
  error: string | null;
  /** 账号级能力；null 表示"未知/不适用"，页面此时不做任何前端过滤。 */
  capabilities: readonly AccountAction[] | null;
  login(username: string, password: string): Promise<boolean>;
  logout(): Promise<void>;
  clearError(): void;
}

const OFFLINE_VALUE: AuthContextValue = {
  status: 'offline',
  me: null,
  error: null,
  capabilities: null,
  async login() {
    return false;
  },
  async logout() {},
  clearError() {},
};

const AuthContext = createContext<AuthContextValue>(OFFLINE_VALUE);

export function AuthProvider({ children }: { children: ReactNode }): ReactElement {
  const api = isApiMode();
  const [status, setStatus] = useState<AuthStatus>(api ? 'checking' : 'offline');
  const [me, setMe] = useState<AccountMe | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!api) {
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        await fetchCsrfToken();
      } catch (csrfError) {
        // 取不到 CSRF 时还不能登录，但页面其余部分照常渲染，错误留给登录动作再报一次。
        if (!cancelled) {
          setError(describeError(csrfError));
        }
      }
      try {
        const current = await fetchMe();
        if (!cancelled) {
          setMe(current);
          setStatus('authenticated');
        }
      } catch (meError) {
        if (!cancelled) {
          setMe(null);
          setStatus('anonymous');
          // 未登录是正常状态，不是错误提示；其它失败（网络、协议）才提示。
          setError(meError instanceof ApiError && meError.code === 'UNAUTHENTICATED' ? null : describeError(meError));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [api]);

  const login = useCallback(
    async (username: string, password: string): Promise<boolean> => {
      if (!api) {
        setError('固定样例模式未接入后端，无法登录。');
        return false;
      }
      setStatus('checking');
      setError(null);
      try {
        if (currentCsrfToken() === null) {
          await fetchCsrfToken();
        }
        await requestLogin(username, password);
        const current = await fetchMe();
        setMe(current);
        setStatus('authenticated');
        return true;
      } catch (loginError) {
        setMe(null);
        setStatus('anonymous');
        setError(describeError(loginError));
        return false;
      }
    },
    [api],
  );

  const logout = useCallback(async (): Promise<void> => {
    if (!api) {
      return;
    }
    try {
      await requestLogout();
    } catch (logoutError) {
      setError(describeError(logoutError));
    }
    clearCsrfToken();
    setMe(null);
    setStatus('anonymous');
    // 退出后重新取一次令牌，方便直接换账号登录。
    try {
      await fetchCsrfToken();
    } catch {
      // 忽略：下次点登录时会重新取。
    }
  }, [api]);

  const clearError = useCallback(() => setError(null), []);

  const value = useMemo<AuthContextValue>(
    () => ({ status, me, error, capabilities: me?.allowed_actions ?? null, login, logout, clearError }),
    [status, me, error, login, logout, clearError],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

/** 没有 Provider 时返回 offline 默认值，页面测试可以单独渲染而不必包一层。 */
export function useAuth(): AuthContextValue {
  return useContext(AuthContext);
}
