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
    if (codecs && !codecs.length) throw new Error('CODEC_UNSUPPORTED');
    transceiver.direction = 'sendonly';
    if (codecs?.length && transceiver.setCodecPreferences) transceiver.setCodecPreferences(codecs);
  }
}

/** Older Safari lacks setCodecPreferences. Restrict the actual offer as well;
 * never silently publish VP8/HEVC when the recording contract requires H264. */
export function compatibleOffer(offer: RTCSessionDescriptionInit): RTCSessionDescriptionInit {
  const parts = (offer.sdp ?? '').split(/\r?\nm=/);
  if (parts.length < 3) throw new Error('CODEC_UNSUPPORTED');
  for (let i = 1; i < parts.length; i++) {
    const lines = parts[i]!.split(/\r?\n/), media = lines[0]!.split(' ');
    const kind = media[0];
    if (kind !== 'video' && kind !== 'audio') continue;
    const codec = kind === 'video' ? 'h264' : 'opus';
    const allowed = new Set(lines.filter(line => /^a=rtpmap:/.test(line) && line.split(' ')[1]?.split('/')[0]?.toLowerCase() === codec)
      .map(line => line.split(':')[1]!.split(' ')[0]!));
    if (!allowed.size) throw new Error('CODEC_UNSUPPORTED');
    // Keep only retransmission payloads associated with an allowed video codec.
    if (kind === 'video') for (const line of lines) {
      const match = /^a=fmtp:(\d+) .*\bapt=(\d+)(?:;|$)/.exec(line);
      if (match && allowed.has(match[2]!)) allowed.add(match[1]!);
    }
    const payloads = media.slice(3).filter(payload => allowed.has(payload));
    lines[0] = [...media.slice(0, 3), ...payloads].join(' ');
    parts[i] = lines.filter(line => {
      const payload = /^a=(?:rtpmap|fmtp|rtcp-fb):(\d+)\b/.exec(line)?.[1];
      return !payload || allowed.has(payload);
    }).join('\r\n');
  }
  return { type: offer.type, sdp: parts.join('\r\nm=') };
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
