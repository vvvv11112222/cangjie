/** P1 live-capture DTOs; business transport retains the existing {data, request_id} envelope. */
export interface LiveCapture {
  id: string;
  session_id: string;
  state: 'waiting' | 'recording' | 'finalizing' | 'ready' | 'failed';
  connection_state: 'unknown' | 'connected' | 'disconnected';
  recording_state: 'waiting' | 'recording' | 'stopping' | 'preparing' | 'complete' | 'cleanup_pending' | 'cleaned';
  audio_track: 'unknown' | 'present' | 'absent';
  video_track: 'unknown' | 'present' | 'absent';
  created_at: string;
  expires_at: string;
  started_at: string | null;
  ended_at: string | null;
  duration_ms: number | null;
  media_asset_id: string | null;
  media_prepare_run_id: string | null;
  end_reason: string | null;
  error_code: string | null;
  allowed_actions: Array<'read' | 'pair' | 'watch' | 'stop'>;
}
export interface CreateLiveCapture { source_record_id: string; config_profile: 'phone-demo-v1' }
export interface CapturePairing { pairing_id: string; phone_url: string; expires_at: string }
export interface CapturePublisher { live_capture_id: string; session_id: string; whip_url: string; publish_token: string; control_token: string; expires_at: string }
export interface CaptureViewer { viewer_session_id: string; whep_url: string; view_token: string; expires_at: string }
