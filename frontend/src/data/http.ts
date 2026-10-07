/**
 * 统一 HTTP 入口：把协议的响应信封、错误信封和 CSRF 头收在一个地方。
 *
 * 为什么需要：协议规定成功是 {data,request_id}、错误是 {error:{code,message,details},request_id}
 * （见 docs/开发协议.md 第 1 节）。如果每个页面各自 fetch，就会出现"有的地方读 data、
 * 有的地方读 body"这种散乱写法，错误处理也各不相同。这里统一成：
 *   成功 → 返回 {data, requestId}；失败 → 抛 ApiError。
 *
 * 浏览器端始终使用同源 Cookie 会话，因此不开 CORS、不手工拼 Cookie；
 * 写请求额外带 X-CSRF-Token（服务端还会校验 Origin，见协议第 1.1 节）。
 */

import { ApiError } from './errors';
import type { ErrorDetails } from './errors';

/** 业务接口统一前缀，与 docs/开发协议.md 第 1 节一致。 */
export const API_PREFIX = '/api/v1';

/**
 * CSRF token 只放在内存里：它是和会话绑定的短期令牌，刷新页面时重新取一次即可。
 * 放进 localStorage 会让令牌在标签页之间串用，也会留下不必要的持久痕迹。
 */
let csrfToken: string | null = null;

export function setCsrfToken(token: string): void {
  csrfToken = token;
}

export function clearCsrfToken(): void {
  csrfToken = null;
}

export function currentCsrfToken(): string | null {
  return csrfToken;
}

export interface Envelope<T> {
  data: T;
  /** 服务端请求 ID，排查问题时把它一起报给后端。 */
  requestId: string;
}

export type QueryValue = string | number | boolean | null | undefined;

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE';
  /** 查询参数；值为 null/undefined/'' 时该参数不发送，筛选变化的清空逻辑由调用方负责。 */
  query?: Record<string, QueryValue>;
  /** JSON 请求体；不传时使用 GET 语义（POST 空请求体发送 {}）。 */
  body?: unknown;
  /** 是否需要携带 X-CSRF-Token，写请求都要打开。 */
  csrf?: boolean;
}

/** 拼出 /api/v1/... 的完整路径，跳过空查询参数。 */
export function apiPath(path: string, query?: Record<string, QueryValue>): string {
  const normalized = path.startsWith('/') ? path : `/${path}`;
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value === null || value === undefined || value === '') {
      continue;
    }
    search.append(key, String(value));
  }
  const queryString = search.toString();
  return queryString === '' ? `${API_PREFIX}${normalized}` : `${API_PREFIX}${normalized}?${queryString}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function parseJson(text: string): unknown {
  if (text.trim() === '') {
    return null;
  }
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return undefined;
  }
}

/** 把错误信封转成 ApiError；信封本身不符合协议时降级为 MALFORMED_RESPONSE。 */
function toApiError(payload: unknown, status: number): ApiError {
  const envelope = isRecord(payload) ? payload : null;
  const error = envelope !== null && isRecord(envelope['error']) ? envelope['error'] : null;
  const requestId = envelope !== null && typeof envelope['request_id'] === 'string' ? envelope['request_id'] : null;

  if (error === null) {
    return new ApiError({
      code: 'MALFORMED_RESPONSE',
      status,
      requestId,
      message: `HTTP ${status} 且响应不是协议规定的错误信封`,
    });
  }

  const code = typeof error['code'] === 'string' && error['code'] !== '' ? error['code'] : 'MALFORMED_RESPONSE';
  const message = typeof error['message'] === 'string' ? error['message'] : '';
  const details: ErrorDetails = isRecord(error['details']) ? error['details'] : {};

  return new ApiError({
    // 服务端可能新增错误码，这里保留原字符串，展示层走 errorCodeText 的兜底文案。
    code: code as ApiError['code'],
    status,
    details,
    requestId,
    message,
  });
}

/**
 * 发送一次业务请求。
 * 调用方拿到的是协议里的 data 部分；request_id 一并返回，方便页面或日志附带。
 */
export async function requestJson<T>(path: string, options: RequestOptions = {}): Promise<Envelope<T>> {
  const { method = 'GET', query, body, csrf = false } = options;
  const headers: Record<string, string> = { Accept: 'application/json' };
  const init: RequestInit = { method, headers, credentials: 'same-origin' };

  if (method !== 'GET') {
    headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body ?? {});
  }
  if (csrf) {
    if (csrfToken === null) {
      throw new ApiError({
        code: 'FORBIDDEN',
        status: 0,
        message: '缺少 CSRF 令牌，写请求未发送',
      });
    }
    headers['X-CSRF-Token'] = csrfToken;
  }

  let response: Response;
  try {
    response = await fetch(apiPath(path, query), init);
  } catch (error) {
    throw new ApiError({
      code: 'NETWORK_ERROR',
      status: 0,
      message: error instanceof Error ? error.message : '请求未到达服务端',
    });
  }

  const text = await response.text();
  const payload = parseJson(text);

  if (payload === undefined) {
    throw new ApiError({
      code: 'MALFORMED_RESPONSE',
      status: response.status,
      message: '响应不是合法的 JSON',
    });
  }

  if (!response.ok) {
    throw toApiError(payload, response.status);
  }

  // 204 与文件字节没有 JSON 包装；业务 JSON 接口成功时必须是 {data,request_id}。
  if (response.status === 204 || payload === null) {
    return { data: undefined as T, requestId: '' };
  }

  if (!isRecord(payload) || !('data' in payload)) {
    throw new ApiError({
      code: 'MALFORMED_RESPONSE',
      status: response.status,
      message: '成功响应缺少 data 字段',
    });
  }

  const requestId = typeof payload['request_id'] === 'string' ? payload['request_id'] : '';
  return { data: payload['data'] as T, requestId };
}
