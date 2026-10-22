import { ApiError } from './errors';
import { asEnum, asNullableString, asRecord, asString, objectList } from './guards';
import { requestJson } from './http';

export interface AuditRecord {
  id: string; actor_user_id: string | null; actor_name: string | null;
  action: string; resource_type: string; resource_id: string | null;
  result: 'success' | 'denied' | 'failure'; created_at: string;
}
export interface AuditPageData { items: AuditRecord[]; next_cursor: string | null }
export async function listAudit(query: { cursor?: string; action?: string; result?: string }): Promise<AuditPageData> {
  const { data } = await requestJson<unknown>('/audit-logs', { query: { ...query, limit: 20 } });
  try {
    const page = asRecord(data, 'AuditPage');
    return {
      items: objectList(page, 'items', 'AuditPage').map(row => {
        const id = asString(row, 'id', 'AuditRecord');
        if (!/^[1-9][0-9]*$/.test(id)) throw new Error('操作记录编号格式不正确');
        return { id, actor_user_id: asNullableString(row, 'actor_user_id', 'AuditRecord'), actor_name: asNullableString(row, 'actor_name', 'AuditRecord'),
          action: asString(row, 'action', 'AuditRecord'), resource_type: asString(row, 'resource_type', 'AuditRecord'), resource_id: asNullableString(row, 'resource_id', 'AuditRecord'),
          result: asEnum(row, 'result', 'AuditRecord', ['success', 'denied', 'failure'] as const), created_at: asString(row, 'created_at', 'AuditRecord') };
      }),
      next_cursor: asNullableString(page, 'next_cursor', 'AuditPage'),
    };
  } catch (reason) { throw new ApiError({ code: 'MALFORMED_RESPONSE', status: 200, message: reason instanceof Error ? reason.message : '操作记录格式不正确' }); }
}
