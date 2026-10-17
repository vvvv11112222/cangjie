import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import revisionJson from '../../../contracts/examples/revision.json';
import reportJson from '../../../contracts/examples/report-draft.json';
import { clearCsrfToken, setCsrfToken } from './http';
import { createExport, createRevision, publishReport, reviewReport } from './m3Api';

interface Call { url: string; init?: RequestInit }
let calls: Call[] = [];
const response = (data: unknown, status = 200) => new Response(JSON.stringify({ data, request_id: 'req-m3' }), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
  calls = [];
  clearCsrfToken(); setCsrfToken('csrf-m3');
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init });
    if (String(input).endsWith('/exports')) return response({ id: '00000000-0000-4000-8000-000000000099', report_id: reportJson.id, status: 'queued', error_code: null, download_url: null, expires_at: '2026-10-17T00:00:00Z' }, 202);
    if (String(input).includes('transcript-revisions')) return response(revisionJson, 201);
    return response(reportJson);
  }));
});
afterEach(() => vi.unstubAllGlobals());

describe('M3 写接口', () => {
  it('创建转写修订保留基础版本和锁版本', async () => {
    await createRevision(revisionJson.session_id, {
      media_asset_id: revisionJson.media_asset_id,
      base_revision_id: revisionJson.id,
      transcript_lock_version: revisionJson.transcript_lock_version,
      reason: '纠正术语', segments: revisionJson.segments,
    });
    expect(calls[0]?.url).toContain(`/sessions/${revisionJson.session_id}/transcript-revisions`);
    expect(JSON.parse(String(calls[0]?.init?.body))).toMatchObject({ base_revision_id: revisionJson.id, reason: '纠正术语' });
  });

  it('整篇确认必须携带内容摘要，发布携带预期当前版本', async () => {
    await reviewReport(reportJson.id, { lock_version: reportJson.lock_version, action: 'confirm_report', observation_id: null, content_sha256: reportJson.content_sha256, reason: '核验证据' });
    await publishReport(reportJson.id, { lock_version: reportJson.lock_version, expected_current_report_id: null });
    expect(JSON.parse(String(calls[0]?.init?.body)).content_sha256).toBe(reportJson.content_sha256);
    expect(JSON.parse(String(calls[1]?.init?.body)).expected_current_report_id).toBeNull();
  });

  it('创建导出携带格式与幂等键', async () => {
    const result = await createExport(reportJson.id, 'pdf', 'export-intent');
    expect(result.status).toBe('queued');
    expect((calls[0]?.init?.headers as Record<string, string>)['Idempotency-Key']).toBe('export-intent');
    expect(calls[0]?.init?.body).toBe(JSON.stringify({ format: 'pdf' }));
  });
});
