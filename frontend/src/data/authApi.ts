/**
 * 登录与鉴权骨架（协议第 1.1、2 节）。
 *
 * 调用顺序固定为三步，不能颠倒：
 *   1. GET  /api/v1/auth/csrf  → 拿到和当前（或登录前的临时）会话绑定的令牌；
 *   2. POST /api/v1/auth/login → 带 X-CSRF-Token 提交账号密码，服务端轮换会话与令牌；
 *   3. GET  /api/v1/auth/me    → 取 display_name、roles 和 allowed_actions，
 *      页面据此决定展示哪些操作。前端只是展示，真正的授权由服务端逐次检查。
 */

import { requestJson, setCsrfToken } from './http';
import { ApiError } from './errors';
import {
  asArray,
  asEnum,
  asNullableString,
  asRecord,
  asString,
  enumList,
} from './guards';

export const ROLE_CODES = ['sys_admin', 'academic_admin', 'supervisor', 'teacher'] as const;
export type RoleCode = (typeof ROLE_CODES)[number];

export const ACCOUNT_ACTIONS = [
  'manage_academic',
  'manage_users',
  'upload',
  'analyze',
  'review',
  'publish',
  'operate',
  'delete',
] as const;
export type AccountAction = (typeof ACCOUNT_ACTIONS)[number];

export interface RoleBinding {
  id: string;
  role_code: RoleCode;
  scope_org_id: string | null;
}

/** GET /auth/me 的 data，见 contracts/v1.schema.json 的 Me。 */
export interface AccountMe {
  user_id: string;
  display_name: string;
  roles: RoleBinding[];
  allowed_actions: AccountAction[];
}

export function parseRoleBinding(value: unknown, path = 'RoleBinding'): RoleBinding {
  const source = asRecord(value, path);
  return {
    id: asString(source, 'id', path),
    role_code: asEnum(source, 'role_code', path, ROLE_CODES),
    scope_org_id: asNullableString(source, 'scope_org_id', path),
  };
}

function parseRoleList(source: Record<string, unknown>, path: string): RoleBinding[] {
  return asArray(source['roles'], `${path}.roles`).map((item, index) =>
    parseRoleBinding(item, `${path}.roles[${index}]`),
  );
}

export function parseMe(value: unknown, path = 'Me'): AccountMe {
  const source = asRecord(value, path);
  return {
    user_id: asString(source, 'user_id', path),
    display_name: asString(source, 'display_name', path),
    roles: parseRoleList(source, path),
    allowed_actions: enumList(source, 'allowed_actions', path, ACCOUNT_ACTIONS),
  };
}

function parseCsrf(value: unknown, path = 'CSRF'): string {
  return asString(asRecord(value, path), 'csrf_token', path);
}

interface LoginResult {
  user_id: string;
  roles: RoleBinding[];
  csrf_token: string;
}

function parseLoginResult(value: unknown, path = 'LoginResult'): LoginResult {
  const source = asRecord(value, path);
  return {
    user_id: asString(source, 'user_id', path),
    roles: parseRoleList(source, path),
    csrf_token: asString(source, 'csrf_token', path),
  };
}

/** 把解析失败（guardFail 抛出的普通 Error）统一收敛成可展示的 ApiError。 */
function asMalformed(error: unknown, status: number): ApiError {
  return new ApiError({
    code: 'MALFORMED_RESPONSE',
    status,
    message: error instanceof Error ? error.message : '响应不符合协议',
  });
}

/**
 * 取 CSRF 令牌，并存进 http.ts 的内存变量，后续写请求自动带上。
 * 页面加载时先调它，登录按钮才可用。
 */
export async function fetchCsrfToken(): Promise<string> {
  const { data } = await requestJson<unknown>('/auth/csrf');
  let token: string;
  try {
    token = parseCsrf(data);
  } catch (error) {
    throw asMalformed(error, 200);
  }
  setCsrfToken(token);
  return token;
}

export interface LoginOutcome {
  userId: string;
  roles: RoleBinding[];
}

/**
 * 登录。令牌由 http.ts 保存在内存里，成功后会替换成服务端轮换后的新令牌。
 * 失败时抛出 ApiError：FORBIDDEN 表示 CSRF/Origin 校验没过，UNAUTHENTICATED 表示账号密码不对。
 */
export async function login(username: string, password: string): Promise<LoginOutcome> {
  const { data } = await requestJson<unknown>('/auth/login', {
    method: 'POST',
    body: { username, password },
    csrf: true,
  });
  let result: LoginResult;
  try {
    result = parseLoginResult(data);
  } catch (error) {
    throw asMalformed(error, 200);
  }
  // 登录成功会轮换会话，令牌随之变化：协议第 1.1 节要求登录后使用新的 X-CSRF-Token。
  setCsrfToken(result.csrf_token);
  return { userId: result.user_id, roles: result.roles };
}

/** GET /auth/me；UNAUTHENTICATED 表示当前没有有效会话。 */
export async function fetchMe(): Promise<AccountMe> {
  const { data } = await requestJson<unknown>('/auth/me');
  try {
    return parseMe(data);
  } catch (error) {
    throw asMalformed(error, 200);
  }
}

/** 退出登录：服务端撤销会话，响应 204 没有 JSON 包装。 */
export async function logout(): Promise<void> {
  await requestJson<unknown>('/auth/logout', { method: 'POST', body: {}, csrf: true });
}
