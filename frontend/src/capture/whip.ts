/** Full-ICE WHIP; no trickle PATCH or automatic reconnection. */
export async function gatherICE(pc: RTCPeerConnection, signal: AbortSignal, timeoutMs = 10000): Promise<void> {
  if (signal.aborted) throw new DOMException('Stopped', 'AbortError');
  if (pc.iceGatheringState === 'complete') return;
  await new Promise<void>((resolve, reject) => {
    const finish = (error?: Error) => {
      clearTimeout(timer);
      pc.removeEventListener('icegatheringstatechange', check);
      signal.removeEventListener('abort', abort);
      if (error) reject(error); else resolve();
    };
    const check = () => { if (pc.iceGatheringState === 'complete') finish(); };
    const abort = () => finish(new DOMException('Stopped', 'AbortError'));
    const timer = setTimeout(() => finish(new Error('ICE_TIMEOUT')), timeoutMs);
    pc.addEventListener('icegatheringstatechange', check);
    signal.addEventListener('abort', abort, { once: true });
    check();
  });
}

export function mediaURL(value: string, base: string): URL {
  const url = new URL(value, base);
  if (url.username || url.password || url.hash || url.search ||
    (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)))) {
    throw new Error('MEDIA_URL_INVALID');
  }
  return url;
}

export function resourceURL(location: string | null, endpoint: URL): string {
  if (!location) throw new Error('WHIP_LOCATION_MISSING');
  const url = mediaURL(location, endpoint.href);
  if (url.origin !== endpoint.origin || !url.pathname.startsWith(endpoint.pathname.replace(/\/$/, '') + '/')) {
    throw new Error('WHIP_LOCATION_INVALID');
  }
  return url.href;
}

export function selectCodecs(pc: RTCPeerConnection, capabilities: (kind: 'audio' | 'video') => RTCRtpCapabilities | null): void {
  for (const transceiver of pc.getTransceivers()) {
    const kind = transceiver.sender.track?.kind;
    if (kind !== 'video' && kind !== 'audio') continue;
    const mime = kind === 'video' ? 'video/h264' : 'audio/opus';
    const codecs = capabilities(kind)?.codecs.filter(codec => codec.mimeType.toLowerCase() === mime);
    if (!codecs?.length || !transceiver.setCodecPreferences) throw new Error('CODEC_UNSUPPORTED');
    transceiver.direction = 'sendonly';
    transceiver.setCodecPreferences(codecs);
  }
}

export function verifyAnswer(sdp: string): void {
  if (!sdp.startsWith('v=0')) throw new Error('WHIP_RESPONSE_INVALID');
  const sections = sdp.split(/\r?\nm=/).slice(1);
  for (const [kind, codec] of [['video', 'H264'], ['audio', 'opus']]) {
    const section = sections.find(value => value.startsWith(kind + ' '));
    const media = section?.split(/\r?\n/)[0]?.split(' ') ?? [];
    if (!section || media[1] === '0' || /(?:^|\n)a=(inactive|sendonly)(?:\r?\n|$)/.test(section)) throw new Error('TRACKS_MISSING');
    const payloads = media.slice(3);
    const mappings = [...section.matchAll(/a=rtpmap:(\d+) ([^/\s]+)\//g)];
    if (!mappings.some(mapping => payloads.includes(mapping[1] ?? '') && mapping[2]?.toLowerCase() === codec?.toLowerCase())) throw new Error('CODEC_UNSUPPORTED');
  }
}

/** Verify selected outbound RTP codecs, rather than labelling an SDP offer as negotiated. */
export async function outboundCodecs(pc: RTCPeerConnection): Promise<string[]> {
  const stats = await pc.getStats();
  const codecs = new Set<string>();
  stats.forEach(stat => {
    if (stat.type === 'outbound-rtp' && !stat.isRemote && stat.codecId) {
      const codec = stats.get(stat.codecId) as { mimeType?: string } | undefined;
      if (codec?.mimeType) codecs.add(codec.mimeType.toLowerCase());
    }
  });
  if ([...codecs].some(mime => !['video/h264', 'audio/opus', 'video/rtx'].includes(mime))) {
    throw new Error('CODEC_UNSUPPORTED');
  }
  return [...codecs].filter(mime => mime !== 'video/rtx');
}
