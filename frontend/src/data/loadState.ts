/**
 * 页面加载状态（纯逻辑，不依赖 React，便于按场景测试）。
 *
 * 为什么需要它：页面的数据属于"某个登录会话"。如果只写一个 boolean 的 loading，
 * 会出现两类问题——账号 A 退出后换账号 B，页面仍显示 A 的课堂/转写/报告；
 * 账号 A 的请求迟到返回时，又会覆盖 B 已经加载好的数据。
 * 所以状态里带上会话标识（key），只有 key 匹配的结果才允许写入。
 */

export type LoadStatus =
  /** 正在请求数据。 */
  | 'loading'
  /** 数据已就绪。 */
  | 'ready'
  /** 请求失败，页面显示可读提示。 */
  | 'error'
  /** 接口模式下尚未登录：不发请求，提示先登录。 */
  | 'signed_out';

export interface LoadState<T> {
  /** 这份状态属于哪个会话；登录、退出、换账号都会让它变化。 */
  key: string;
  status: LoadStatus;
  error: string | null;
  data: T;
}

export function loadingState<T>(key: string, data: T): LoadState<T> {
  return { key, status: 'loading', error: null, data };
}

export function readyState<T>(key: string, data: T): LoadState<T> {
  return { key, status: 'ready', error: null, data };
}

export function errorState<T>(key: string, error: string, data: T): LoadState<T> {
  return { key, status: 'error', error, data };
}

export function signedOutState<T>(key: string, data: T): LoadState<T> {
  return { key, status: 'signed_out', error: null, data };
}

/**
 * 迟到的响应只允许写回它自己那次会话的状态。
 * 换成账号 B 之后，A 的响应到达时 key 已经不同，直接丢弃，避免把 A 的数据画到页面上。
 */
export function applyIfSameSession<T>(state: LoadState<T>, key: string, next: LoadState<T>): LoadState<T> {
  return state.key === key ? next : state;
}

/**
 * 渲染时用的状态：如果当前 state 还停留在旧会话，立刻按新会话的初始状态呈现，
 * 这样切换账号的那一帧不会再把上一个账号的数据显示出来。
 */
export function forSession<T>(state: LoadState<T>, key: string, fallback: () => LoadState<T>): LoadState<T> {
  return state.key === key ? state : fallback();
}
