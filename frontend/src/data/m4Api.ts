import type {
  Deletion,
  ModelCall,
  ModelCallPage,
  ModelCallStatus,
  Operations,
  ReconcileCall,
  WorkerCapability,
} from '../types';
import { ApiError } from './errors';
import {
  asEnum,
  asInteger,
  asNullableString,
  asRecord,
  asString,
  objectList,
  stringList,
} from './guards';
import { requestJson } from './http';

const WORKER_CAPABILITIES = ['probe', 'audio_analysis', 'video_analysis'] as const;
const MODEL_CALL_STATUSES = ['reserved', 'unknown', 'succeeded', 'failed'] as const;
const DELETION_STATUSES = ['requested', 'processing', 'blocked', 'succeeded', 'failed'] as const;
const MONEY = /^[0-9]+\.[0-9]{6}$/;

function malformed(error: unknown): ApiError {
  return new ApiError({
    code: 'MALFORMED_RESPONSE',
    status: 200,
    message: error instanceof Error ? error.message : '响应不符合协议',
  });
}

function money(source: Record<string, unknown>, key: string, path: string): string {
  const value = asString(source, key, path);
  if (!MONEY.test(value)) {
    throw new Error(`数据不符合协议：${path}.${key} 应为 6 位小数金额字符串`);
  }
  return value;
}

function nonNegativeInteger(source: Record<string, unknown>, key: string, path: string): number {
  const value = asInteger(source, key, path);
  if (value < 0) {
    throw new Error(`数据不符合协议：${path}.${key} 应为非负整数`);
  }
  return value;
}

export function parseOperations(value: unknown): Operations {
  try {
    const item = asRecord(value, 'Operations');
    const budget = asRecord(item['budget'], 'Operations.budget');
    const currency = asString(budget, 'currency', 'Operations.budget');
    if (currency !== 'CNY') {
      throw new Error('数据不符合协议：Operations.budget.currency 应为 CNY');
    }
    return {
      workers: objectList(item, 'workers', 'Operations').map((worker, index) => {
        const path = `Operations.workers[${index}]`;
        const capabilities = stringList(worker, 'capabilities', path);
        if (
          capabilities.length === 0 ||
          new Set(capabilities).size !== capabilities.length ||
          capabilities.some((capability) => !WORKER_CAPABILITIES.includes(capability as WorkerCapability))
        ) {
          throw new Error(`${path}.capabilities 不符合协议`);
        }
        return {
          worker_id: asString(worker, 'worker_id', path),
          last_seen_at: asString(worker, 'last_seen_at', path),
          capabilities: capabilities as WorkerCapability[],
        };
      }),
      queued_jobs: nonNegativeInteger(item, 'queued_jobs', 'Operations'),
      storage_free_bytes: nonNegativeInteger(item, 'storage_free_bytes', 'Operations'),
      budget: {
        currency,
        billing_period: asString(budget, 'billing_period', 'Operations.budget'),
        limit: money(budget, 'limit', 'Operations.budget'),
        charged: money(budget, 'charged', 'Operations.budget'),
        reserved: money(budget, 'reserved', 'Operations.budget'),
        // unknown 独立占用预算；响应缺失时不能在前端补成 0。
        unknown: money(budget, 'unknown', 'Operations.budget'),
      },
    };
  } catch (error) {
    throw malformed(error);
  }
}

export function parseModelCall(value: unknown): ModelCall {
  try {
    const item = asRecord(value, 'ModelCall');
    const currency = asString(item, 'currency', 'ModelCall');
    if (currency !== 'CNY') {
      throw new Error('数据不符合协议：ModelCall.currency 应为 CNY');
    }
    const actualCost = asNullableString(item, 'actual_cost', 'ModelCall');
    if (actualCost !== null && !MONEY.test(actualCost)) {
      throw new Error('数据不符合协议：ModelCall.actual_cost 应为 6 位小数金额字符串或 null');
    }
    const lockVersion = asInteger(item, 'lock_version', 'ModelCall');
    if (lockVersion < 1) {
      throw new Error('数据不符合协议：ModelCall.lock_version 应不小于 1');
    }
    return {
      id: asString(item, 'id', 'ModelCall'),
      status: asEnum(item, 'status', 'ModelCall', MODEL_CALL_STATUSES) as ModelCallStatus,
      reserved_cost: money(item, 'reserved_cost', 'ModelCall'),
      actual_cost: actualCost,
      currency,
      billing_period: asString(item, 'billing_period', 'ModelCall'),
      price_version: asNullableString(item, 'price_version', 'ModelCall'),
      lock_version: lockVersion,
      dispatch_started_at: asNullableString(item, 'dispatch_started_at', 'ModelCall'),
      settled_at: asNullableString(item, 'settled_at', 'ModelCall'),
    };
  } catch (error) {
    throw malformed(error);
  }
}

export function parseDeletion(value: unknown): Deletion {
  try {
    const item = asRecord(value, 'Deletion');
    return {
      id: asString(item, 'id', 'Deletion'),
      session_id: asString(item, 'session_id', 'Deletion'),
      status: asEnum(item, 'status', 'Deletion', DELETION_STATUSES),
      error_code: asNullableString(item, 'error_code', 'Deletion'),
      completed_at: asNullableString(item, 'completed_at', 'Deletion'),
    };
  } catch (error) {
    throw malformed(error);
  }
}

export async function getOperations(): Promise<Operations> {
  const { data } = await requestJson<unknown>('/operations');
  return parseOperations(data);
}

export async function listModelCalls(cursor?: string, limit = 20): Promise<ModelCallPage> {
  const { data } = await requestJson<unknown>('/model-calls', { query: { cursor, limit } });
  try {
    const page = asRecord(data, 'ModelCallPage');
    return {
      items: objectList(page, 'items', 'ModelCallPage').map((item) => parseModelCall(item)),
      next_cursor: asNullableString(page, 'next_cursor', 'ModelCallPage'),
    };
  } catch (error) {
    if (error instanceof ApiError) throw error;
    throw malformed(error);
  }
}

export async function reconcileModelCall(id: string, input: ReconcileCall): Promise<ModelCall> {
  const { data } = await requestJson<unknown>(`/model-calls/${encodeURIComponent(id)}/reconcile`, {
    method: 'POST',
    body: input,
    csrf: true,
  });
  return parseModelCall(data);
}

export async function createDeletion(
  sessionId: string,
  reason: string,
  idempotencyKey: string,
): Promise<Deletion> {
  const { data } = await requestJson<unknown>(
    `/sessions/${encodeURIComponent(sessionId)}/deletion-requests`,
    { method: 'POST', body: { reason }, csrf: true, idempotencyKey },
  );
  return parseDeletion(data);
}

export async function getDeletion(id: string): Promise<Deletion> {
  const { data } = await requestJson<unknown>(`/deletion-requests/${encodeURIComponent(id)}`);
  return parseDeletion(data);
}
