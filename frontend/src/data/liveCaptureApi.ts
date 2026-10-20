import type { CapturePairing, CaptureViewer, CreateLiveCapture, LiveCapture } from '../captureTypes';
import { ApiError } from './errors';
import { asEnum, asNullableInteger, asNullableString, asRecord, asString, enumList } from './guards';
import { requestJson } from './http';

const STATES = ['waiting', 'recording', 'finalizing', 'ready', 'failed'] as const;
const CONNECTIONS = ['unknown', 'connected', 'disconnected'] as const;
const RECORDINGS = ['waiting', 'recording', 'stopping', 'preparing', 'complete', 'cleanup_pending', 'cleaned'] as const;
const TRACKS = ['unknown', 'present', 'absent'] as const;
const ACTIONS = ['read', 'pair', 'watch', 'stop'] as const;

function malformed(error: unknown): ApiError {
  return new ApiError({ code: 'MALFORMED_RESPONSE', status: 200, message: error instanceof Error ? error.message : '响应不符合协议' });
}

export function parseLiveCapture(value: unknown): LiveCapture {
  try {
    const item = asRecord(value, 'LiveCapture');
    return {
      id: asString(item, 'id', 'LiveCapture'), session_id: asString(item, 'session_id', 'LiveCapture'),
      state: asEnum(item, 'state', 'LiveCapture', STATES),
      connection_state: asEnum(item, 'connection_state', 'LiveCapture', CONNECTIONS),
      recording_state: asEnum(item, 'recording_state', 'LiveCapture', RECORDINGS),
      audio_track: asEnum(item, 'audio_track', 'LiveCapture', TRACKS),
      video_track: asEnum(item, 'video_track', 'LiveCapture', TRACKS),
      created_at: asString(item, 'created_at', 'LiveCapture'), expires_at: asString(item, 'expires_at', 'LiveCapture'),
      started_at: asNullableString(item, 'started_at', 'LiveCapture'), ended_at: asNullableString(item, 'ended_at', 'LiveCapture'),
      duration_ms: asNullableInteger(item, 'duration_ms', 'LiveCapture'),
      media_asset_id: asNullableString(item, 'media_asset_id', 'LiveCapture'),
      media_prepare_run_id: asNullableString(item, 'media_prepare_run_id', 'LiveCapture'),
      end_reason: asNullableString(item, 'end_reason', 'LiveCapture'), error_code: asNullableString(item, 'error_code', 'LiveCapture'),
      allowed_actions: enumList(item, 'allowed_actions', 'LiveCapture', ACTIONS),
    };
  } catch (error) { throw malformed(error); }
}

export async function createLiveCapture(sessionId: string, input: CreateLiveCapture, idempotencyKey: string): Promise<LiveCapture> {
  const { data } = await requestJson<unknown>(`/sessions/${encodeURIComponent(sessionId)}/live-captures`, {
    method: 'POST', body: input, csrf: true, idempotencyKey,
  });
  return parseLiveCapture(data);
}

export async function getActiveLiveCapture(sessionId: string): Promise<LiveCapture> {
  const { data } = await requestJson<unknown>(`/sessions/${encodeURIComponent(sessionId)}/live-captures/active`);
  return parseLiveCapture(data);
}

export async function getLiveCapture(captureId: string): Promise<LiveCapture> {
  const { data } = await requestJson<unknown>(`/live-captures/${encodeURIComponent(captureId)}`);
  return parseLiveCapture(data);
}

export async function createPairing(captureId: string): Promise<CapturePairing> {
  const { data } = await requestJson<unknown>(`/live-captures/${encodeURIComponent(captureId)}/pairings`, { method: 'POST', body: {}, csrf: true });
  try {
    const item = asRecord(data, 'CapturePairing');
    return { pairing_id: asString(item, 'pairing_id', 'CapturePairing'), phone_url: asString(item, 'phone_url', 'CapturePairing'), expires_at: asString(item, 'expires_at', 'CapturePairing') };
  } catch (error) { throw malformed(error); }
}

export async function createViewer(captureId: string): Promise<CaptureViewer> {
  const { data } = await requestJson<unknown>(`/live-captures/${encodeURIComponent(captureId)}/viewer-sessions`, { method: 'POST', body: {}, csrf: true });
  try {
    const item = asRecord(data, 'CaptureViewer');
    return { viewer_session_id: asString(item, 'viewer_session_id', 'CaptureViewer'), whep_url: asString(item, 'whep_url', 'CaptureViewer'), view_token: asString(item, 'view_token', 'CaptureViewer'), expires_at: asString(item, 'expires_at', 'CaptureViewer') };
  } catch (error) { throw malformed(error); }
}

export async function stopLiveCapture(captureId: string): Promise<LiveCapture> {
  const { data } = await requestJson<unknown>(`/live-captures/${encodeURIComponent(captureId)}/stop`, { method: 'POST', body: {}, csrf: true });
  return parseLiveCapture(data);
}
