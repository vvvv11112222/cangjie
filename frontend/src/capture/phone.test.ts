import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { captureHint, PhoneCapture } from './phone';
import type { PhoneEnvironment } from './phone';
import type { CapturePublisher, LiveCapture } from '../captureTypes';
import { gatherICE, outboundCodecs, resourceURL, verifyAnswer } from './whip';

const publisher: CapturePublisher = { live_capture_id: 'capture-1', session_id: 'lesson-1',
  whip_url: 'https://school.test/media/captures/1/0/whip', publish_token: 'publish-secret', control_token: 'control-secret', expires_at: '2026-10-15T12:00:00Z' };
const waiting: LiveCapture = { id: 'capture-1', session_id: 'lesson-1', state: 'waiting', connection_state: 'unknown',
  recording_state: 'waiting', audio_track: 'unknown', video_track: 'unknown', created_at: '', expires_at: '', started_at: null,
  ended_at: null, duration_ms: null, media_asset_id: null, media_prepare_run_id: null, end_reason: null, error_code: null, allowed_actions: ['read','stop'] };

class Track extends EventTarget {
  readyState = 'live'; muted = false;
  onended: (() => void) | null = null;
  onmute: (() => void) | null = null;
  stop = vi.fn(() => { this.readyState = 'ended'; });
  constructor(readonly kind: string) { super(); }
  getSettings() { return this.kind === 'video' ? { width: 640, height: 480, frameRate: 15, facingMode: 'environment' } : { sampleRate: 48000 }; }
}

class Peer extends EventTarget {
  iceGatheringState = 'complete'; connectionState = 'new';
  onconnectionstatechange: (() => void) | null = null;
  localDescription: RTCSessionDescriptionInit | null = null;
  transceivers: { sender: { track: MediaStreamTrack }; setCodecPreferences: ReturnType<typeof vi.fn>; direction: string }[] = [];
  addTrack(track: MediaStreamTrack) { this.transceivers.push({sender:{track},setCodecPreferences:vi.fn(),direction:'sendrecv'}); }
  getTransceivers() { return this.transceivers; }
  createOffer() { return Promise.resolve({type:'offer',sdp:answer}); }
  async setLocalDescription(description: RTCSessionDescriptionInit) { this.localDescription=description; }
  setRemoteDescription = vi.fn(async () => { this.connectionState='connected'; this.onconnectionstatechange?.(); });
  close = vi.fn(() => { this.connectionState='closed'; });
  getStats = vi.fn(async () => new Map([
    ['video',{type:'outbound-rtp',codecId:'vcodec'}],['vcodec',{mimeType:'video/H264'}],
    ['audio',{type:'outbound-rtp',codecId:'acodec'}],['acodec',{mimeType:'audio/opus'}],
  ]));
}

function deferred<T>() {
  let resolve!: (value:T) => void;
  const promise=new Promise<T>(r=>{resolve=r;});
  return {promise,resolve};
}
const response = (data: unknown, status=200) => new Response(JSON.stringify({data,request_id:'synthetic'}),{status,headers:{'Content-Type':'application/json'}});
const answer = 'v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=rtpmap:96 H264/90000\r\na=recvonly\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=rtpmap:111 opus/48000/2\r\na=recvonly\r\n';

function setup() {
  const video=new Track('video'); const audio=new Track('audio');
  const stream={ getTracks:()=>[video,audio], getVideoTracks:()=>[video],getAudioTracks:()=>[audio] } as unknown as MediaStream;
  const peer=new Peer();
  const stopMeter=vi.fn(); const releaseWake=vi.fn(async()=>{});
  const getMedia=vi.fn(async()=>stream);
  const fetch=vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url=String(input);
    if(url.endsWith('/exchange')) return response(publisher);
    if(init?.method==='DELETE') return new Response(null,{status:204});
    if(url.endsWith('/whip')) return new Response(answer,{status:201,headers:{Location:publisher.whip_url+'/resource-1','Content-Type':'application/sdp'}});
    if(url.endsWith('/stop')) return response({...waiting,state:'finalizing',recording_state:'stopping'},202);
    return response(waiting);
  });
  const env:PhoneEnvironment={fetch:fetch as typeof window.fetch,getMedia,makePeer:()=>peer as unknown as RTCPeerConnection,
    capabilities:kind=>({codecs:[{mimeType:kind==='video'?'video/H264':'audio/opus',clockRate:kind==='video'?90000:48000}],headerExtensions:[]}),
    meter:vi.fn(async(_stream,update)=>{update(.1);return stopMeter;}),
    wake:vi.fn(async()=>({release:releaseWake,addEventListener:vi.fn()})),origin:'https://school.test'};
  const changes=vi.fn(); const phone=new PhoneCapture('a'.repeat(64),env,changes);
  return {phone,env,fetch,getMedia,peer,video,audio,stopMeter,releaseWake,stream,changes};
}

describe('phone capture lifecycle (synthetic browser APIs, not Android acceptance)',()=>{
  beforeEach(()=>vi.useFakeTimers());
  afterEach(()=>vi.useRealTimers());

  it('publishes exactly two tracks once; local preview is not server recording',async()=>{
    const s=setup();
    const first=s.phone.start(); await s.phone.start(); await first;
    expect(s.getMedia).toHaveBeenCalledTimes(1);
    expect(s.peer.transceivers).toHaveLength(2);
    expect(s.peer.transceivers.every(t=>t.direction==='sendonly')).toBe(true);
    expect(s.phone.state.sessionID).toBe('lesson-1');
    expect(s.phone.state.capture).toBeNull();
    expect(s.phone.state.phase).toBe('publishing');
    expect(s.phone.state.settings[0]?.width).toBe(640);
    expect(s.phone.state.audioActivity).toBe(true);
    const whip=s.fetch.mock.calls.find(([url])=>String(url).endsWith('/whip'));
    expect(whip?.[1]?.headers).toMatchObject({'Content-Type':'application/sdp',Authorization:'Bearer publish-secret'});
    expect(whip?.[1]?.body).toContain('H264/90000');
    expect(JSON.stringify(s.changes.mock.calls)).not.toContain('publish-secret');
    expect(JSON.stringify(s.changes.mock.calls)).not.toContain('control-secret');
    await s.phone.poll(); expect(s.phone.state.capture?.state).toBe('waiting');
    await s.phone.stop(); s.phone.dispose();
    expect(s.video.stop).toHaveBeenCalled(); expect(s.audio.stop).toHaveBeenCalled();
    expect(s.stopMeter).toHaveBeenCalled(); expect(s.releaseWake).toHaveBeenCalled();
    expect(s.fetch.mock.calls.some(([url,init])=>url===publisher.whip_url+'/resource-1'&&init?.method==='DELETE')).toBe(true);
  });

  it('does not exchange a token when microphone permission is refused; permits a local retry',async()=>{
    const s=setup(); s.getMedia.mockRejectedValue(new DOMException('Denied','NotAllowedError'));
    await s.phone.start();
    expect(s.fetch).not.toHaveBeenCalled(); expect(s.phone.state.canStart).toBe(true);
    expect(s.phone.state.error).toContain('麦克风'); s.phone.dispose();
  });

  it('rejects a video-only stream and releases its hardware without publishing',async()=>{
    const s=setup(); s.env.getMedia=async()=>({...s.stream,getAudioTracks:()=>[],getTracks:()=>[s.video]}) as unknown as MediaStream;
    await s.phone.start(); expect(s.fetch).not.toHaveBeenCalled();
    expect(s.video.stop).toHaveBeenCalled(); expect(s.phone.state.error).toContain('必须同时'); s.phone.dispose();
  });

  it('never retries an expired/consumed exchange, or an exchange whose response was lost',async()=>{
    for(const lost of [false,true]) {
      const s=setup(); s.fetch.mockImplementation(async()=>{
        if(lost) throw new TypeError('network');
        return new Response(JSON.stringify({error:{code:'UNAUTHENTICATED'}}),{status:401});
      });
      await s.phone.start(); await s.phone.start();
      expect(s.fetch).toHaveBeenCalledTimes(1); expect(s.phone.state.canStart).toBe(false);
      expect(s.phone.state.notice).toContain('回电脑'); expect(s.peer.close).toHaveBeenCalled(); s.phone.dispose();
    }
  });

  it('stops local hardware immediately even while backend stop is pending; permits retry',async()=>{
    const s=setup(); await s.phone.start();
    const pending=deferred<Response>();
    s.fetch.mockImplementation(async(input,init)=>{
      if(String(input).endsWith('/stop'))return pending.promise;
      if(init?.method==='DELETE')return new Response(null,{status:204});
      return response(waiting);
    });
    const stopping=s.phone.stop(); await s.phone.stop();
    expect(s.video.stop).toHaveBeenCalled(); expect(s.audio.stop).toHaveBeenCalled();
    expect(s.fetch.mock.calls.filter(([url])=>String(url).endsWith('/stop'))).toHaveLength(1);
    pending.resolve(new Response(JSON.stringify({error:{code:'SERVICE_UNAVAILABLE'}}),{status:503}));
    await stopping; expect(s.phone.state.stopPending).toBe(true); expect(s.phone.state.error).toContain('本地已停');
    s.fetch.mockResolvedValue(response({...waiting,state:'ready',media_asset_id:'formal-media'},200));
    await s.phone.stop(); expect(s.phone.state.capture?.media_asset_id).toBe('formal-media');
    expect(s.phone.state.canStop).toBe(false); s.phone.dispose();
  });

  it('does not start when a stopped permission request returns late',async()=>{
    const s=setup();const pending=deferred<MediaStream>();s.getMedia.mockReturnValue(pending.promise);
    const start=s.phone.start();await s.phone.stop();pending.resolve(s.stream);await start;
    expect(s.video.stop).toHaveBeenCalled();expect(s.fetch).not.toHaveBeenCalled(); s.phone.dispose();
  });

  it('handles a late exchange after stop without posting a second publisher',async()=>{
    const s=setup();const pending=deferred<Response>();
    s.fetch.mockImplementation(async input=>String(input).endsWith('/exchange')?pending.promise:response({...waiting,state:'finalizing'},202));
    const start=s.phone.start();await vi.waitFor(()=>expect(s.fetch).toHaveBeenCalled());
    await s.phone.stop();pending.resolve(response(publisher));await start;
    expect(s.fetch.mock.calls.some(([url])=>String(url).endsWith('/stop'))).toBe(true);
    expect(s.fetch.mock.calls.some(([url])=>String(url).endsWith('/whip'))).toBe(false);s.phone.dispose();
  });

  it('does not silently fall back to an incompatible codec',async()=>{
    const s=setup();s.env.capabilities=()=>({codecs:[{mimeType:'video/VP8',clockRate:90000}],headerExtensions:[]});
    await s.phone.start();expect(s.fetch).not.toHaveBeenCalled();expect(s.phone.state.error).toContain('H.264/Opus');s.phone.dispose();
  });

  it('ends once on disconnect and never automatically publishes a new connection',async()=>{
    const s=setup();await s.phone.start();s.peer.connectionState='disconnected';s.peer.onconnectionstatechange?.();
    await vi.waitFor(()=>expect(s.phone.state.phase).toBe('stopped'));
    await s.phone.start(); expect(s.getMedia).toHaveBeenCalledTimes(1);
    expect(s.phone.state.error).toContain('断开'); s.phone.dispose();
  });

  it('releases tracks on a system track revocation and hidden-page interruption',async()=>{
    for(const revoked of [true,false]) {
      const s=setup();await s.phone.start();
      if(revoked)s.audio.onended?.();else await s.phone.interrupted('页面已进入后台');
      await vi.waitFor(()=>expect(s.video.stop).toHaveBeenCalled());
      expect(s.phone.state.canStart).toBe(false);s.phone.dispose();
    }
  });

  it('follows finalizing to ready, stops polling and records the formal media ID',async()=>{
    const s=setup();await s.phone.start();
    s.fetch.mockImplementation(async(_url,init)=>init?.method==='DELETE'?new Response(null,{status:204}):response({...waiting,state:'finalizing'}));
    await s.phone.poll();expect(s.phone.state.phase).toBe('stopped');expect(s.video.stop).toHaveBeenCalled();
    s.fetch.mockResolvedValue(response({...waiting,state:'ready',media_asset_id:'formal-1'}));
    await vi.advanceTimersByTimeAsync(1500); expect(s.phone.state.capture?.media_asset_id).toBe('formal-1');
    const count=s.fetch.mock.calls.length;await vi.advanceTimersByTimeAsync(6000);expect(s.fetch).toHaveBeenCalledTimes(count);s.phone.dispose();
  });

  it('keeps polling failures visible without pretending a recording was saved',async()=>{
    const s=setup();await s.phone.start();s.fetch.mockRejectedValue(new TypeError('network'));
    await s.phone.poll();expect(s.phone.state.error).toContain('状态暂时无法确认');expect(s.phone.state.capture).toBeNull();s.phone.dispose();
  });

  it('ignores optional meter/wake failures and provides manual wake guidance',async()=>{
    const s=setup();s.env.meter=async()=>{throw Error('unsupported');};s.env.wake=async()=>{throw Error('unsupported');};
    await s.phone.start();await Promise.resolve();expect(s.phone.state.phase).toBe('publishing');
    expect(s.phone.state.wake).toContain('手工');expect(s.phone.state.meterAvailable).toBe(false);s.phone.dispose();
  });

  it('handles refresh without storing credentials or requiring a teacher password',async()=>{
    const s=setup();const phone=new PhoneCapture(null,s.env,vi.fn());await phone.start();
    expect(s.fetch).not.toHaveBeenCalled();expect(phone.state.notice).toContain('配对信息已丢失');phone.dispose();s.phone.dispose();
  });

  it('bounds an SDP exchange that never establishes the media connection',async()=>{
    const s=setup();s.peer.setRemoteDescription.mockImplementation(async()=>{});
    await s.phone.start();expect(s.phone.state.phase).toBe('connecting');
    await vi.advanceTimersByTimeAsync(15000);
    expect(s.phone.state.phase).toBe('stopped');expect(s.phone.state.error).toContain('连接超时');
    expect(s.audio.stop).toHaveBeenCalled();expect(s.phone.state.canStart).toBe(false);s.phone.dispose();
  });

  it('stops when the server detects a missing audio track during recording',async()=>{
    const s=setup();await s.phone.start();
    s.fetch.mockImplementation(async(input,init)=>init?.method==='DELETE'?new Response(null,{status:204}):
      response({...waiting,state:String(input).endsWith('/stop')?'finalizing':'recording',audio_track:'absent'}));
    await s.phone.poll();expect(s.phone.state.error).toContain('缺少音轨');
    expect(s.video.stop).toHaveBeenCalled();expect(s.phone.state.canStart).toBe(false);s.phone.dispose();
  });

  it('releases an optional meter which becomes ready after a failed start',async()=>{
    const s=setup();const meter=deferred<()=>void>();s.env.meter=()=>meter.promise;
    s.fetch.mockRejectedValue(new TypeError('network'));await s.phone.start();
    meter.resolve(s.stopMeter);await Promise.resolve();await Promise.resolve();
    expect(s.stopMeter).toHaveBeenCalled();expect(s.phone.state.meterAvailable).toBe(false);s.phone.dispose();
  });
});

describe('WHIP protocol and bounded waits',()=>{
  beforeEach(()=>vi.useFakeTimers()); afterEach(()=>vi.useRealTimers());
  it('accepts same-channel relative Location and refuses credential exfiltration',()=>{
    const endpoint=new URL(publisher.whip_url);
    expect(resourceURL('/media/captures/1/0/whip/session',endpoint)).toBe(endpoint.href+'/session');
    for(const invalid of [null,'https://other.test/whip/session','/api/v1/users','http://school.test/media/captures/1/0/whip/x']) {
      expect(()=>resourceURL(invalid,endpoint)).toThrow();
    }
  });
  it('completes ICE, removes listeners, and aborts or times out without hanging',async()=>{
    const peer=new Peer();peer.iceGatheringState='gathering';const rtc=peer as unknown as RTCPeerConnection;
    const remove=vi.spyOn(peer,'removeEventListener');const abort=new AbortController();
    const complete=gatherICE(rtc,abort.signal);peer.iceGatheringState='complete';peer.dispatchEvent(new Event('icegatheringstatechange'));await complete;
    expect(remove).toHaveBeenCalled();peer.iceGatheringState='gathering';
    const timeout=expect(gatherICE(rtc,abort.signal,100)).rejects.toThrow('ICE_TIMEOUT');await vi.advanceTimersByTimeAsync(100);await timeout;
    const cancelled=expect(gatherICE(rtc,abort.signal)).rejects.toThrow('Stopped');abort.abort();await cancelled;
  });
  it('reports actual outbound codecs and rejects an incompatible encoder',async()=>{
    const peer=new Peer();expect(await outboundCodecs(peer as unknown as RTCPeerConnection)).toEqual(['video/h264','audio/opus']);
    peer.getStats.mockResolvedValue(new Map([['video',{type:'outbound-rtp',codecId:'vcodec'}],['vcodec',{mimeType:'video/VP8'}]]));
    await expect(outboundCodecs(peer as unknown as RTCPeerConnection)).rejects.toThrow('CODEC_UNSUPPORTED');
  });
  it('does not disclose raw error messages containing a credential',()=>{
    expect(captureHint(new Error('token=private'))).not.toContain('private');
  });
  it('rejects rejected audio, inactive video or an answer without the supported pair',()=>{
    expect(()=>verifyAnswer(answer)).not.toThrow();
    for(const sdp of [answer.replace('m=audio 9','m=audio 0'), answer.replace('a=recvonly','a=inactive'),answer.replace('H264/90000','VP8/90000'), 'v=0\r\n']) {
      expect(()=>verifyAnswer(sdp)).toThrow();
    }
  });
});
