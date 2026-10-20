import { gatherICE, mediaURL, resourceURL } from './whip';

export interface WHEPSession {
  stream: MediaStream;
  connection: RTCPeerConnection;
  close(): Promise<void>;
}

export function verifyWHEPAnswer(sdp: string): void {
  if (!sdp.startsWith('v=0')) throw new Error('WHEP_RESPONSE_INVALID');
  const sections = sdp.split(/\r?\nm=/).slice(1);
  for (const [kind, codec] of [['video', 'H264'], ['audio', 'opus']] as const) {
    const section = sections.find((value) => value.startsWith(`${kind} `));
    const media = section?.split(/\r?\n/)[0]?.split(' ') ?? [];
    // WHEP 远端必须向浏览器发送，因此 answer 应为 sendonly 或 sendrecv，不能是 recvonly。
    if (!section || media[1] === '0' || /(?:^|\n)a=(inactive|recvonly)(?:\r?\n|$)/.test(section)) throw new Error('TRACKS_MISSING');
    const payloads = media.slice(3);
    const mappings = [...section.matchAll(/a=rtpmap:(\d+) ([^/\s]+)\//g)];
    if (!mappings.some((mapping) => payloads.includes(mapping[1] ?? '') && mapping[2]?.toLowerCase() === codec.toLowerCase())) throw new Error('CODEC_UNSUPPORTED');
  }
}

/**
 * WHEP 是 WebRTC 的接收端信令。whep_url 不是媒体文件地址；这里先交换 SDP，
 * 再把收到的远端轨道交给页面的 video.srcObject。
 */
export async function connectWHEP(
  whepUrl: string,
  viewToken: string,
  signal: AbortSignal,
): Promise<WHEPSession> {
  signal.throwIfAborted();
  const endpoint = mediaURL(whepUrl, window.location.origin);
  const connection = new RTCPeerConnection();
  const stream = new MediaStream();
  let sessionResource: string | null = null;
  let closed = false;
  let deletedResource: string | null = null;

  connection.addTransceiver('video', { direction: 'recvonly' });
  connection.addTransceiver('audio', { direction: 'recvonly' });
  connection.ontrack = (event) => {
    const incoming = event.streams[0];
    if (incoming !== undefined) {
      for (const track of incoming.getTracks()) {
        if (!stream.getTracks().some((candidate) => candidate.id === track.id)) stream.addTrack(track);
      }
    } else if (!stream.getTracks().some((candidate) => candidate.id === event.track.id)) {
      stream.addTrack(event.track);
    }
  };

  const close = async (): Promise<void> => {
    signal.removeEventListener('abort', abort);
    if (!closed) {
      closed = true;
      connection.ontrack = null;
      for (const track of stream.getTracks()) track.stop();
      connection.close();
    }
    if (sessionResource !== null && deletedResource !== sessionResource) {
      deletedResource = sessionResource;
      await fetch(sessionResource, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${viewToken}` },
        signal: AbortSignal.timeout(5000),
      }).catch(() => undefined);
    }
  };

  const abort = () => { void close(); };
  signal.addEventListener('abort', abort, { once: true });
  try {
    const offer = await connection.createOffer();
    await connection.setLocalDescription(offer);
    await gatherICE(connection, signal);
    const response = await fetch(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp', Authorization: `Bearer ${viewToken}` },
      body: connection.localDescription?.sdp ?? offer.sdp,
      signal,
    });
    if (!response.ok) throw new Error(`WHEP 信令失败（HTTP ${response.status}）`);
    sessionResource = resourceURL(response.headers.get('Location'), endpoint);
    const answer = await response.text();
    signal.throwIfAborted();
    verifyWHEPAnswer(answer);
    await connection.setRemoteDescription({ type: 'answer', sdp: answer });
    signal.throwIfAborted();
    return { stream, connection, close };
  } catch (error) {
    await close();
    throw error;
  }
}
