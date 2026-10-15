/**
 * 账号与角色绑定（协议第 2 节的 /users*）。
 *
 * DTO 与 Go 的 internal/identity/service.go 对齐：
 *   User = id / org_unit_id(可空) / username / display_name / status
 *   RoleBinding = id / role_code / scope_org_id(可空)
 *
 * 接口缺口（写在这里免得下次又当成漏做）：**没有读取用户已有角色绑定的端点**。
 * 只有 POST /users/{id}/role-bindings 和 DELETE .../{binding_id}，
 * 而 binding_id 除了创建时的返回值之外没有地方能读到。所以页面上不显示"该用户已有的角色"，
 * 也不能撤销历史绑定；需要这一列时，得后端补一个 GET /users/{id}/role-bindings。
 */

import { ApiError } from './errors';
import { asEnum, asNullableString, asRecord, asString, objectList } from './guards';
import { requestJson } from './http';
import { ROLE_CODES } from './authApi';
import type { RoleCode } from './authApi';

export const USER_STATUSES = ['active', 'disabled'] as const;
export type UserStatus = (typeof USER_STATUSES)[number];

export const USER_STATUS_LABEL: Record<UserStatus, string> = {
  active: '已启用',
  disabled: '已停用',
};

export interface ManagedUser {
  id: string;
  orgUnitId: string | null;
  username: string;
  displayName: string;
  status: UserStatus;
}

export interface RoleBinding {
  id: string;
  roleCode: RoleCode;
  scopeOrgId: string | null;
}

export interface CreateUserInput {
  orgUnitId: string;
  username: string;
  displayName: string;
  status: UserStatus;
  /** 后端要求 12—72 位，页面上也要先校验一次。 */
  initialPassword: string;
}

export interface PatchUserInput {
  displayName: string;
  status: UserStatus;
}

function asMalformed(error: unknown): ApiError {
  return new ApiError({
    code: 'MALFORMED_RESPONSE',
    status: 200,
    message: error instanceof Error ? error.message : '响应不符合协议',
  });
}

export function parseManagedUser(value: unknown, path = 'User'): ManagedUser {
  const source = asRecord(value, path);
  return {
    id: asString(source, 'id', path),
    orgUnitId: asNullableString(source, 'org_unit_id', path),
    username: asString(source, 'username', path),
    displayName: asString(source, 'display_name', path),
    status: asEnum(source, 'status', path, USER_STATUSES),
  };
}

export function parseRoleBinding(value: unknown, path = 'RoleBinding'): RoleBinding {
  const source = asRecord(value, path);
  return {
    id: asString(source, 'id', path),
    roleCode: asEnum(source, 'role_code', path, ROLE_CODES),
    scopeOrgId: asNullableString(source, 'scope_org_id', path),
  };
}

export interface UserListEnvelope {
  items: ManagedUser[];
  nextCursor: string | null;
}

export async function listUsers(cursor?: string): Promise<UserListEnvelope> {
  const { data } = await requestJson<unknown>('/users', { query: { cursor, limit: 100 } });
  try {
    const source = asRecord(data, 'UserPage');
    return {
      items: objectList(source, 'items', 'UserPage').map((item, index) =>
        parseManagedUser(item, `UserPage.items[${index}]`),
      ),
      nextCursor: asNullableString(source, 'next_cursor', 'UserPage'),
    };
  } catch (error) {
    throw asMalformed(error);
  }
}

export async function createUser(input: CreateUserInput): Promise<ManagedUser> {
  const { data } = await requestJson<unknown>('/users', {
    method: 'POST',
    body: {
      org_unit_id: input.orgUnitId,
      username: input.username,
      display_name: input.displayName,
      status: input.status,
      initial_password: input.initialPassword,
    },
    csrf: true,
  });
  try {
    return parseManagedUser(data);
  } catch (error) {
    throw asMalformed(error);
  }
}

/** 只改显示名与状态；org_unit_id 涉及范围调整，这一版不做（PATCH 不传即不变）。 */
export async function patchUser(id: string, input: PatchUserInput): Promise<ManagedUser> {
  const { data } = await requestJson<unknown>(`/users/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: { display_name: input.displayName, status: input.status },
    csrf: true,
  });
  try {
    return parseManagedUser(data);
  } catch (error) {
    throw asMalformed(error);
  }
}

/** 授予角色绑定；scope_org_id 为 null 表示不限定学院（只有系统管理员能这么授）。 */
export async function createRoleBinding(
  userId: string,
  roleCode: RoleCode,
  scopeOrgId: string | null,
): Promise<RoleBinding> {
  const { data } = await requestJson<unknown>(`/users/${encodeURIComponent(userId)}/role-bindings`, {
    method: 'POST',
    body: { role_code: roleCode, scope_org_id: scopeOrgId },
    csrf: true,
  });
  try {
    return parseRoleBinding(data);
  } catch (error) {
    throw asMalformed(error);
  }
}
