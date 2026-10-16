import { describe, expect, it } from 'vitest';
import { verifyWHEPAnswer } from './whep';

const answer = (direction: string) => `v=0\r\n` +
  `m=video 9 UDP/TLS/RTP/SAVPF 96\r\na=${direction}\r\na=rtpmap:96 H264/90000\r\n` +
  `m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=${direction}\r\na=rtpmap:111 opus/48000/2\r\n`;

describe('WHEP answer', () => {
  it('接受媒体服务向浏览器发送的 sendonly answer', () => expect(() => verifyWHEPAnswer(answer('sendonly'))).not.toThrow());
  it('拒绝只接收而不发送轨道的 answer', () => expect(() => verifyWHEPAnswer(answer('recvonly'))).toThrow('TRACKS_MISSING'));
  it('拒绝缺少正式编解码器的 answer', () => expect(() => verifyWHEPAnswer(answer('sendonly').replace('H264', 'VP8'))).toThrow('CODEC_UNSUPPORTED'));
});
