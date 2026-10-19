import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { clearCsrfToken, setCsrfToken } from './http';
import {
  createDeletion,
  getOperations,
  listModelCalls,
  parseOperations,
  reconcileModelCall,
} from './m4Api';
import { ApiError } from './errors';

interface Call { url: string; init?: RequestInit }
let calls: Call[] = [];
const id = '00000000-0000-4000-8000-000000000041';

const operations = {
  workers: [{ worker_id: 'worker-01', last_seen_at: '2026-10-18T10:00:00+08:00', capabilities: ['probe'] }],
  queued_jobs: 2,
  storage_free_bytes: 1024,
  budget: {
    currency: 'CNY', billing_period: '2026-10-01', limit: '10.000000',
    charged: '2.000000', reserved: '1.000000', unknown: '0.500000',
  },
};

const modelCall = {
  id,
  status: 'unknown',
  reserved_cost: '1.000000',
  actual_cost: null,
  currency: 'CNY',
  billing_period: '2026-10-01',
  price_version: 'price-v1',
  lock_version: 2,
  dispatch_started_at: '2026-10-18T10:00:00+08:00',
  settled_at: null,
};

function response(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ data, request_id: 'req-m4' }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  calls = [];
  clearCsrfToken(); setCsrfToken('csrf-m4');
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, init });
    if (url.endsWith('/operations')) return response(operations);
    if (url.includes('/model-calls/') && init?.method === 'POST') return response({ ...modelCall, status: 'failed', actual_cost: '0.250000', lock_version: 3, settled_at: '2026-10-18T11:00:00+08:00' });
    if (url.includes('/model-calls')) return response({ items: [modelCall], next_cursor: 'next-call' });
    if (url.includes('/deletion-requests')) return response({ id, session_id: id, status: 'requested', error_code: null, completed_at: null }, 202);
    throw new Error(`unexpected ${url}`);
  }));
});

afterEach(() => vi.unstubAllGlobals());

describe('M4 治理接口', () => {
  it('严格读取 Operations，包括独立的 unknown 预算', async () => {
    await expect(getOperations()).resolves.toMatchObject({ queued_jobs: 2, budget: { unknown: '0.500000' } });
    expect(calls[0]?.url).toBe('/api/v1/operations');
  });

  it('后端缺少 Schema 必填的 budget.unknown 时明确报契约错误', () => {
    const withoutUnknown: Record<string, unknown> = { ...operations.budget };
    delete withoutUnknown['unknown'];
    try {
      parseOperations({ ...operations, budget: withoutUnknown });
      throw new Error('expected parseOperations to fail');
    } catch (error) {
      expect(error).toBeInstanceOf(ApiError);
      expect((error as ApiError).code).toBe('MALFORMED_RESPONSE');
      expect((error as ApiError).message).toContain('unknown');
    }
  });

  it('模型账目保留游标，核对请求携带锁版本与 CSRF', async () => {
    const page = await listModelCalls(undefined, 20);
    expect(page.next_cursor).toBe('next-call');
    expect(calls[0]?.url).toBe('/api/v1/model-calls?limit=20');

    const reconciled = await reconcileModelCall(id, {
      lock_version: 2,
      status: 'failed',
      actual_cost: '0.250000',
      provider_request_id: null,
      reason: '供应商账单核对',
    });
    expect(reconciled.status).toBe('failed');
    expect(JSON.parse(String(calls[1]?.init?.body))).toMatchObject({ lock_version: 2, actual_cost: '0.250000' });
    expect((calls[1]?.init?.headers as Record<string, string>)['X-CSRF-Token']).toBe('csrf-m4');
  });

  it('创建清理请求携带原因、CSRF 与幂等键', async () => {
    const deletion = await createDeletion(id, '超过保留期限', 'delete-intent-1');
    expect(deletion.status).toBe('requested');
    expect(calls[0]?.init?.body).toBe(JSON.stringify({ reason: '超过保留期限' }));
    const headers = calls[0]?.init?.headers as Record<string, string>;
    expect(headers['X-CSRF-Token']).toBe('csrf-m4');
    expect(headers['Idempotency-Key']).toBe('delete-intent-1');
  });
});
