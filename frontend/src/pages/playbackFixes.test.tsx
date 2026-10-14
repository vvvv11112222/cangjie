// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { beforeEach, afterEach, it, expect, vi } from 'vitest';
import sessionPage from '../../../contracts/examples/session-page.json';
import baseRun from '../../../contracts/examples/run-partial.json';
import baseResults from '../../../contracts/examples/results.json';
import baseRevision from '../../../contracts/examples/revision.json';

const h = vi.hoisted(() => ({
  getSessions: vi.fn(), getRun: vi.fn(), getResults: vi.fn(), getRevision: vi.fn(),
  getSessionMedia: vi.fn(), getMedia: vi.fn(), sessionKey: 'A#1', userId: 'A',
}));
vi.mock('../data/sources', async (original) => {
  const actual = await original<typeof import('../data/sources')>();
  return { ...actual, isApiMode: () => true, getMedia: h.getMedia, getSessionMedia: h.getSessionMedia,
    dataSource: { ...actual.dataSource, getSessions: h.getSessions, getRun: h.getRun,
      getResults: h.getResults, getRevision: h.getRevision, getMedia: h.getMedia, getSessionMedia: h.getSessionMedia } };
});
vi.mock('../auth', () => ({ useAuth: () => ({ status: 'authenticated', sessionKey: h.sessionKey,
  me: { user_id: h.userId }, capabilities: null, error: null }) }));
import { SessionDetailPage } from './SessionDetailPage';
import { MediaPlayer } from '../components/MediaPlayer';
import { usePlaybackProbe } from '../data/hooks';

const M1 = sessionPage.items[0]!.primary_media_asset_id!;
const M2 = '00000000-0000-4000-8000-000000000202';
const P2 = '00000000-0000-4000-8000-000000000204';
const media = (id = M1) => ({ id, session_id: sessionPage.items[0]!.id,
  source_record_id: '00000000-0000-4000-8000-000000000006', parent_asset_id: null,
  kind: 'source', status: 'ready', sha256: 'a'.repeat(64), mime_type: 'video/mp4', byte_size: 572814,
  duration_ms: 30000, expires_at: '2027-10-14T09:00:00+08:00', playback_asset_id: P2,
  playback_url: `/api/v1/media/${P2}/content`, allowed_actions: ['playback'] });
let container: HTMLDivElement;
let root: Root;
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => resolve = r); return {promise, resolve}; }
async function settle() { await act(async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); }); }
async function mountDetail() {
  await act(async () => root.render(<SessionDetailPage sessionId={sessionPage.items[0]!.id}/>));
  await settle();
  await act(async () => (Array.from(container.querySelectorAll<HTMLButtonElement>('.tab-item')).find(b => b.textContent === '回放')!).click());
  await settle();
}
beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {});
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  h.sessionKey = 'A#1'; h.userId = 'A';
  h.getSessions.mockReset().mockResolvedValue(sessionPage);
  h.getRun.mockReset().mockResolvedValue(baseRun);
  h.getResults.mockReset().mockResolvedValue(baseResults);
  h.getRevision.mockReset().mockResolvedValue(baseRevision);
  h.getSessionMedia.mockReset().mockResolvedValue({items: [], nextCursor: null});
  h.getMedia.mockReset().mockResolvedValue(media());
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status: 206 }));
  container = document.createElement('div'); document.body.append(container); root = createRoot(container);
});
afterEach(() => { act(() => root.unmount()); container.remove(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });


it('F01: mismatched primary keeps video but excludes foreign captions and jumps', async () => {
  h.getSessions.mockResolvedValue({ ...sessionPage, items: [{...sessionPage.items[0], primary_media_asset_id: M2}] });
  h.getMedia.mockResolvedValue(media(M2));
  await mountDetail();
  expect(container.querySelector('video')?.getAttribute('src')).toContain(P2);
  expect(container.querySelector('.jump-item')).toBeNull();
  expect(container.textContent).not.toContain(baseResults.segments[0]!.text_content);
  expect(container.textContent).toContain('当前录像与所选分析结果不同');
});
it('F01: revision for another media cannot supply captions or jumps', async () => {
  h.getRevision.mockResolvedValue({...baseRevision,media_asset_id:M2});
  await mountDetail();
  expect(container.querySelector('.jump-item')).toBeNull();
  expect(container.textContent).toContain('转写修订与当前录像');
});
it('F05: failed precheck disables jumps; recheck restores real seeking', async () => {
  const fetcher=vi.fn().mockResolvedValueOnce({status:416,text:async()=>JSON.stringify({error:{code:'RANGE_NOT_SATISFIABLE',message:'synthetic',details:{}}})})
    .mockResolvedValue({status:206});
  vi.stubGlobal('fetch',fetcher);
  await mountDetail();
  expect(container.querySelector('video')).toBeNull();
  expect(container.querySelector<HTMLButtonElement>('.jump-item')?.disabled).toBe(true);
  await act(async()=> Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b=>b.textContent==='重新检查播放地址')!.click());
  await settle();
  const jump=container.querySelector<HTMLButtonElement>('.jump-item')!;
  expect(jump.disabled).toBe(false);
  await act(async()=>jump.click());
  expect(container.querySelector('video')?.currentTime).toBe(1);
});
it('F02: player failure disables jumps; retry revalidates detail then reloads the video', async () => {
  await mountDetail();
  const oldVideo=container.querySelector('video')!;
  const readCount=h.getMedia.mock.calls.length;
  await act(async()=>oldVideo.dispatchEvent(new Event('error')));
  expect(container.querySelector<HTMLButtonElement>('.jump-item')?.disabled).toBe(true);
  const retry=Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b=>b.textContent==='重试播放')!;
  await act(async()=>retry.click()); await settle();
  expect(h.getMedia.mock.calls.length).toBeGreaterThan(readCount);
  expect(container.querySelector('video')).toBe(oldVideo);
  expect(HTMLMediaElement.prototype.load).toHaveBeenCalled();
  expect(oldVideo.getAttribute('src')).toContain('#playback_retry=');
  expect(oldVideo.getAttribute('src')?.split('#')[0]).toBe(media().playback_url);
  expect(container.textContent).not.toContain('播放器无法加载');
  expect(container.querySelector<HTMLButtonElement>('.jump-item')?.disabled).toBe(false);
});
it('F02: retry honors newly revoked playback address', async () => {
  await mountDetail();
  await act(async()=>container.querySelector('video')!.dispatchEvent(new Event('error')));
  h.getMedia.mockResolvedValue({...media(),playback_url:null,allowed_actions:[]});
  await act(async()=>Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b=>b.textContent==='重试播放')!.click()); await settle();
  expect(container.querySelector('video')).toBeNull();
  expect(container.querySelector<HTMLButtonElement>('.jump-item')?.disabled).toBe(true);
});
it('F02: replacing src discards old failure state and playback position', async () => {
  const props={durationMs:30000,seekRequest:null,label:'synthetic'};
  await act(async()=>root.render(<MediaPlayer {...props} src='/m1.mp4'/>));
  const oldVideo=container.querySelector('video')!;
  await act(async()=>oldVideo.dispatchEvent(new Event('error')));
  await act(async()=>root.render(<MediaPlayer {...props} src='/m2.mp4'/>));
  expect(container.querySelector('video')).not.toBe(oldVideo);
  expect(container.textContent).not.toContain('播放器无法加载');
});
it('F03: hook unmount aborts the hanging request', async () => {
  let signal:AbortSignal | null | undefined;
  vi.stubGlobal('fetch',(_url:unknown,init:RequestInit)=>{signal=init.signal;return new Promise(()=>{});});
  function Probe(){ usePlaybackProbe('/api/v1/media/x/content');return null; }
  await act(async()=>root.render(<Probe/>));
  expect(signal?.aborted).toBe(false);
  await act(async()=>root.render(null));
  expect(signal?.aborted).toBe(true);
});
it('F03: late failed response cannot overwrite a new session and old request aborts', async () => {
  const old=deferred<Response>();let count=0;const signals:AbortSignal[]=[];
  vi.stubGlobal('fetch',(_url:unknown,init:RequestInit)=>{signals.push(init.signal as AbortSignal);return ++count===1?old.promise:Promise.resolve({status:206});});
  function Probe(){const state=usePlaybackProbe('/api/v1/media/x/content');return <span>{state.status}:{state.note}</span>;}
  await act(async()=>root.render(<Probe/>));
  h.sessionKey='B#2';h.userId='B';
  await act(async()=>root.render(<Probe/>));await settle();
  expect(signals[0]?.aborted).toBe(true);
  await act(async()=>old.resolve({status:403,text:async()=>JSON.stringify({error:{code:'FORBIDDEN',message:'A only',details:{}}})} as Response));
  expect(container.textContent).toBe('ok:');
});
it('F04: media list error remains visible and retry can recover', async () => {
  h.getSessions.mockResolvedValue({...sessionPage,items:[{...sessionPage.items[0],primary_media_asset_id:null}]});
  h.getSessionMedia.mockRejectedValue(new Error('MEDIA-LIST-503'));
  await mountDetail();
  expect(container.textContent).toContain('MEDIA-LIST-503');
  expect(container.textContent).not.toContain('暂无可回放媒体');
  expect(container.textContent).not.toContain('这一批次的片段与证据为空');
  h.getSessionMedia.mockResolvedValue({items:[{id:M1,kind:'source',status:'ready',allowed_actions:['playback']}],nextCursor:null});
  await act(async()=>Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b=>b.textContent==='重试')!.click());await settle();
  expect(container.querySelector('video')).not.toBeNull();
});
it('F04: empty page with cursor offers load-more and reaches playable second page', async () => {
  h.getSessions.mockResolvedValue({...sessionPage,items:[{...sessionPage.items[0],primary_media_asset_id:null}]});
  h.getSessionMedia.mockResolvedValueOnce({items:[],nextCursor:'page2'}).mockResolvedValue({items:[{id:M1,kind:'source',status:'ready',allowed_actions:['playback']}],nextCursor:null});
  await mountDetail();
  expect(container.textContent).not.toContain('暂无可回放媒体');
  const more=Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b=>b.textContent==='加载更多')!;
  expect(more).toBeDefined();
  await act(async()=>more.click());await settle();
  expect(h.getSessionMedia).toHaveBeenLastCalledWith(sessionPage.items[0]!.id,'page2');
  expect(container.querySelector('video')).not.toBeNull();
});
it('F04: unrelated list failure does not block known primary', async () => {
  h.getSessionMedia.mockRejectedValue(new Error('UNRELATED-LIST-ERROR'));
  await mountDetail();
  expect(container.querySelector('video')).not.toBeNull();
  expect(container.textContent).not.toContain('UNRELATED-LIST-ERROR');
});
