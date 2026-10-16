import { afterEach, describe, expect, it, vi } from 'vitest';
import { listSessionRuns, listSessionSources } from './m2Api';

const SESSION_ID = '00000000-0000-4000-8000-000000000001';
const SOURCE = {
  id: '00000000-0000-4000-8000-000000000010', session_id: SESSION_ID,
  source_type: 'self_recorded', title: '课堂录像', attribution: '教学团队', source_url: null,
  authorization_note: '已获课堂复盘授权', requested_uses: ['playback', 'analysis'],
  external_processing_requested: false, rights_status: 'verified', allowed_uses: ['playback', 'analysis'],
  external_processing_allowed: false, rights_version: 1, verified_by: null, verified_at: null,
  allowed_actions: ['upload'],
};
const RUN = {
  id: '00000000-0000-4000-8000-000000000020', session_id: SESSION_ID,
  media_asset_id: '00000000-0000-4000-8000-000000000030', mode: 'full',
  input_transcript_revision_id: null, status: 'running', jobs: [],
  stage_plan: ['probe', 'audio_analysis', 'video_analysis', 'evidence', 'report', 'validate'],
  report_id: null, error_code: null, limitations: [], allowed_actions: ['cancel'],
};

afterEach(() => vi.unstubAllGlobals());

describe('M2 来源接口', () => {
  it('按课堂和不透明游标读取分页结果', async () => {
    const fetch = vi.fn(async (_input: RequestInfo | URL) => new Response(JSON.stringify({
      data: { items: [SOURCE], next_cursor: 'opaque-next' }, request_id: 'req-1',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', fetch);

    const page = await listSessionSources(SESSION_ID, 'opaque-current');
    expect(page.items[0]?.title).toBe('课堂录像');
    expect(page.next_cursor).toBe('opaque-next');
    expect(String(fetch.mock.calls[0]?.[0])).toContain(`sessions/${SESSION_ID}/sources?cursor=opaque-current&limit=20`);
  });

  it('拒绝协议枚举之外的来源状态', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      data: { items: [{ ...SOURCE, rights_status: 'approved' }], next_cursor: null }, request_id: 'req-2',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })));

    await expect(listSessionSources(SESSION_ID)).rejects.toThrow('rights_status');
  });

  it('按课堂和不透明游标恢复分析批次历史', async () => {
    const fetch = vi.fn(async (_input: RequestInfo | URL) => new Response(JSON.stringify({
      data: { items: [RUN], next_cursor: 'older-runs' }, request_id: 'req-3',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', fetch);

    const page = await listSessionRuns(SESSION_ID, 'current-runs');
    expect(page.items[0]?.id).toBe(RUN.id);
    expect(page.next_cursor).toBe('older-runs');
    expect(String(fetch.mock.calls[0]?.[0])).toContain(`sessions/${SESSION_ID}/analysis-runs?cursor=current-runs&limit=20`);
  });
});
