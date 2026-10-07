/**
 * 会话流程：启动时恢复登录状态、登录、退出。
 *
 * 把这些规则从 React 组件里拿出来，一是因为"什么时候该重新取 CSRF""什么算退出成功"
 * 都是业务规则，二是这样才能在 node 环境下直接测（不需要 DOM）。
 *
 * 两条容易踩的规则（评审意见 P2）：
 *   1. 每次登录前都重新取一次 CSRF：临时 Cookie 可能已过期，或另一个标签页让 Cookie 轮换过，
 *      内存里的旧令牌会让后端直接回 403；失败时也要清掉旧令牌，否则重试永远失败。
 *   2. 退出登录时，只有服务端明确撤销成功（或明确回应"会话已失效"）才算退出完成；
 *      网络错误、500、CSRF 校验失败都可能意味着 HttpOnly 会话 Cookie 仍然有效，
 *      此时保留原状态并允许重试，不能显示成"已退出"。
 */

import { ApiError, describeError } from './errors';
import { clearCsrfToken, currentCsrfToken } from './http';
import { fetchCsrfToken, fetchMe, login as requestLogin, logout as requestLogout } from './authApi';
import type { AccountAction, AccountMe } from './authApi';

export type AuthStatus = 'offline' | 'checking' | 'anonymous' | 'authenticated';

export interface SessionState {
  status: AuthStatus;
  me: AccountMe | null;
  error: string | null;
}

export const CHECKING: SessionState = { status: 'checking', me: null, error: null };
export const SIGNED_OUT: SessionState = { status: 'anonymous', me: null, error: null };
export const OFFLINE: SessionState = { status: 'offline', me: null, error: null };

// Cookie 和内存令牌属于同一个浏览器会话；完整认证流程串行执行，
// 即使 Provider 卸载后重新挂载，也不能让旧请求与新登录交错写入令牌。
let operationTail: Promise<unknown> = Promise.resolve();
export function serializeSessionOperation<T>(operation: () => Promise<T>): Promise<T> {
  const next = operationTail.then(operation);
  operationTail = next.catch(() => undefined);
  return next;
}

/**
 * 页面取数用的会话标识：登录后按用户区分，未登录/检查中按状态区分。
 * 页面钩子用它做依赖，登录、退出、换账号都会触发重新取数。
 */
export function sessionKeyOf(state: SessionState): string {
  return state.status === 'authenticated' && state.me !== null
    ? `user:${state.me.user_id}`
    : state.status;
}

/** 启动时恢复登录状态：先取令牌，再问 /auth/me。未登录是正常状态，不报错。 */
export async function bootstrapSession(): Promise<SessionState> {
  let error: string | null = null;
  try {
    await fetchCsrfToken();
  } catch (csrfError) {
    // 取不到 CSRF 时还不能登录，但页面其余部分照常渲染；登录动作会再报一次。
    error = describeError(csrfError);
  }
  try {
    const me = await fetchMe();
    return { status: 'authenticated', me, error };
  } catch (meError) {
    if (meError instanceof ApiError && meError.code === 'UNAUTHENTICATED') {
      return { status: 'anonymous', me: null, error };
    }
    return { status: 'anonymous', me: null, error: describeError(meError) };
  }
}

/** 登录：重新取令牌 → 提交账号密码 → 取账号信息与 allowed_actions。 */
export async function signIn(username: string, password: string): Promise<SessionState> {
  try {
    // 每次都重取，保证令牌与浏览器当前的 Cookie 匹配（见文件头第 1 条）。
    await fetchCsrfToken();
    await requestLogin(username, password);
    const me = await fetchMe();
    return { status: 'authenticated', me, error: null };
  } catch (error) {
    // 清掉可能已经失效的令牌，下一次点击登录会重新获取，不会卡在同一个 403 上。
    clearCsrfToken();
    return { status: 'anonymous', me: null, error: describeError(error) };
  }
}

/**
 * 退出登录。current 传入当前会话，便于在撤销失败时原样保留状态（含已加载的账号信息）。
 */
export async function signOut(current: SessionState): Promise<SessionState> {
  // 退出也是写请求，需要 X-CSRF-Token。正常情况下启动时已经取过；
  // 万一内存里没有（例如启动取令牌失败），这里补取一次，避免用户"退不出去还看不到原因"。
  if (currentCsrfToken() === null) {
    try {
      await fetchCsrfToken();
    } catch {
      // 取不到就交给下面的 logout 报错，错误会如实显示在账号区。
    }
  }
  try {
    await requestLogout();
  } catch (error) {
    const sessionGone = error instanceof ApiError && error.code === 'UNAUTHENTICATED';
    if (!sessionGone) {
      clearCsrfToken();
      // 撤销没有真正完成：保留已登录状态，界面显示失败原因，用户可以再点一次退出。
      return { ...current, error: describeError(error) };
    }
  }

  clearCsrfToken();
  // 撤销确认后立即退出；下一次登录本来就会获取新的 CSRF。
  return SIGNED_OUT;
}

export function capabilitiesOf(state: SessionState): readonly AccountAction[] | null {
  return state.me?.allowed_actions ?? null;
}
