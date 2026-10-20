import { afterEach, describe, expect, it, vi } from 'vitest';
import { connectWHEP, verifyWHEPAnswer } from './whep';

const answer = (direction: string) => `v=0\r\n` +
  `m=video 9 UDP/TLS/RTP/SAVPF 96\r\na=${direction}\r\na=rtpmap:96 H264/90000\r\n` +
  `m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=${direction}\r\na=rtpmap:111 opus/48000/2\r\n`;

describe('WHEP answer', () => {
  it('接受媒体服务向浏览器发送的 sendonly answer', () => expect(() => verifyWHEPAnswer(answer('sendonly'))).not.toThrow());
  it('拒绝只接收而不发送轨道的 answer', () => expect(() => verifyWHEPAnswer(answer('recvonly'))).toThrow('TRACKS_MISSING'));
  it('拒绝缺少正式编解码器的 answer', () => expect(() => verifyWHEPAnswer(answer('sendonly').replace('H264', 'VP8'))).toThrow('CODEC_UNSUPPORTED'));
});

afterEach(() => vi.unstubAllGlobals());

function peer() {
  const pc = { addTransceiver: vi.fn(), createOffer: async () => ({ sdp: 'offer' }),
    setLocalDescription: vi.fn(async () => {}), localDescription: { sdp: 'offer' }, iceGatheringState: 'complete',
    setRemoteDescription: vi.fn(async () => {}), close: vi.fn(), ontrack: null };
  vi.stubGlobal('RTCPeerConnection', vi.fn(function () { return pc; }));
  vi.stubGlobal('MediaStream', class { getTracks() { return []; } });
  vi.stubGlobal('window', { location: { origin: 'https://example.test' } });
  return pc;
}

it('does not allocate a peer for an already aborted watch', async () => {
  const pc = peer(); const controller = new AbortController(); controller.abort();
  await expect(connectWHEP('/whep', 'synthetic', controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
  expect(pc.addTransceiver).not.toHaveBeenCalled();
});

it('deletes a server resource that arrives after cancellation', async () => {
  const pc = peer(); const controller = new AbortController();
  let respond!: (response: Response) => void;
  const pending = new Promise<Response>(resolve => { respond = resolve; });
  const fetch = vi.fn().mockReturnValueOnce(pending).mockResolvedValue(new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetch);
  const connecting = connectWHEP('/whep', 'synthetic', controller.signal);
  const rejected = expect(connecting).rejects.toMatchObject({ name: 'AbortError' });
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
  controller.abort(); respond(new Response(answer('sendonly'), { status: 201, headers: { Location: '/whep/resource' } }));
  await rejected;
  expect(pc.close).toHaveBeenCalledTimes(1); expect(pc.setRemoteDescription).not.toHaveBeenCalled();
  expect(fetch.mock.calls[1]![0]).toBe('https://example.test/whep/resource');
  expect(fetch.mock.calls[1]![1].method).toBe('DELETE');
});

it('closes the peer and deletes WHEP only once when cleanup and abort both run', async () => {
  const pc = peer(); const controller = new AbortController();
  const fetch = vi.fn().mockResolvedValueOnce(new Response(answer('sendonly'), { status: 201, headers: { Location: '/whep/resource' } })).mockResolvedValue(new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetch);
  const session = await connectWHEP('/whep', 'synthetic', controller.signal);
  controller.abort(); await session.close(); await session.close();
  expect(pc.close).toHaveBeenCalledTimes(1); expect(fetch).toHaveBeenCalledTimes(2);
});
