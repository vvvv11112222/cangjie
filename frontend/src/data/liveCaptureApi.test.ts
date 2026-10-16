import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { clearCsrfToken, setCsrfToken } from './http';
import { createLiveCapture, getActiveLiveCapture, parseLiveCapture } from './liveCaptureApi';

const CAPTURE = {
  id: '00000000-0000-4000-8000-000000000061', session_id: '00000000-0000-4000-8000-000000000001',
  state: 'recording', connection_state: 'connected', recording_state: 'recording', audio_track: 'present', video_track: 'present',
  created_at: '2026-10-16T01:00:00Z', expires_at: '2026-10-16T03:00:00Z', started_at: '2026-10-16T01:00:02Z', ended_at: null,
  duration_ms: null, media_asset_id: null, media_prepare_run_id: null, end_reason: null, error_code: null,
  allowed_actions: ['read', 'watch', 'stop'],
};
let calls: Array<{ url: string; init?: RequestInit }> = [];

beforeEach(() => {
  calls = []; clearCsrfToken(); setCsrfToken('capture-csrf');
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init });
    return new Response(JSON.stringify({ data: CAPTURE, request_id: 'capture-request' }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  }));
});
afterEach(() => vi.unstubAllGlobals());

describe('实时采集接口', () => {
  it('恢复课堂当前活动采集', async () => {
    await expect(getActiveLiveCapture(CAPTURE.session_id)).resolves.toMatchObject({ state: 'recording' });
    expect(calls[0]?.url).toContain(`/sessions/${CAPTURE.session_id}/live-captures/active`);
  });

  it('创建采集携带固定配置、CSRF 和幂等键', async () => {
    await createLiveCapture(CAPTURE.session_id, { source_record_id: '00000000-0000-4000-8000-000000000010', config_profile: 'phone-demo-v1' }, 'capture-intent');
    const headers = calls[0]?.init?.headers as Record<string, string>;
    expect(headers['X-CSRF-Token']).toBe('capture-csrf');
    expect(headers['Idempotency-Key']).toBe('capture-intent');
    expect(calls[0]?.init?.body).toContain('phone-demo-v1');
  });

  it('拒绝协议外状态，避免页面伪造未知含义', () => {
    expect(() => parseLiveCapture({ ...CAPTURE, state: 'streaming' })).toThrow('LiveCapture.state');
  });
});
