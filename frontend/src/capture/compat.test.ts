import { expect, it, vi } from 'vitest';
import { compatibleOffer, selectCodecs } from './whip';
import { fetchWithDeadline } from './compat';
import { liveColor, parseLiveResult } from './liveVision';

it('Safari fallback filters VP8/HEVC and unrelated RTX while retaining H264 and Opus', () => {
  const sdp = 'v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96 97 98 99 100\r\na=rtpmap:96 VP8/90000\r\na=rtpmap:97 H264/90000\r\na=rtpmap:98 rtx/90000\r\na=fmtp:98 apt=97\r\na=rtpmap:99 rtx/90000\r\na=fmtp:99 apt=96\r\na=rtpmap:100 H265/90000\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111 0\r\na=rtpmap:111 opus/48000/2\r\na=rtpmap:0 PCMU/8000\r\n';
  const output = compatibleOffer({ type: 'offer', sdp }).sdp!;
  expect(output).toContain('SAVPF 97 98'); expect(output).toContain('apt=97');
  expect(output).not.toMatch(/VP8|H265|apt=96|PCMU/);
  const peer = { getTransceivers: () => [{ sender: { track: { kind: 'video' } }, direction: '' }] } as unknown as RTCPeerConnection;
  expect(() => selectCodecs(peer, () => null)).not.toThrow();
  expect(() => compatibleOffer({ type: 'offer', sdp: sdp.replace('H264', 'VP9') })).toThrow('CODEC_UNSUPPORTED');
});

it('request cancellation works without AbortSignal.any/timeout and cleans up its timer', async () => {
  const parent = new AbortController();
  const fetcher = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
    init?.signal?.addEventListener('abort', () => reject(new DOMException('stopped', 'AbortError')));
  }));
  const request = fetchWithDeadline(fetcher, '/capture', {}, 10000, parent.signal);
  parent.abort(); await expect(request).rejects.toMatchObject({ name: 'AbortError' });
});

it('unknown behavior never becomes green and malformed/mismatched boxes are rejected', () => {
  const person = { track_id: 'L0001', bbox: [.1,.1,.2,.2] as [number,number,number,number], hand_raise: false, head_down: null };
  expect(liveColor(person)).toBe('#a3a3a3');
  expect(liveColor({ ...person, hand_raise: true })).toBe('#ef4444');
  expect(liveColor({ ...person, head_down: true })).toBe('#3b82f6');
  const frame = { sequence: 1, width: 640, height: 360, people: [person], error_code: null };
  expect(parseLiveResult(frame)).toEqual(frame);
  expect(() => parseLiveResult({ ...frame, people: [{ ...person, bbox: [0,0,2,1] }] })).toThrow();
  expect(() => parseLiveResult({ ...frame, people: [person,person] })).toThrow();
});
