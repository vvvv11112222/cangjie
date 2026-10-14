// @vitest-environment jsdom
import { act } from 'react';
import type { ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import sessionPageJson from '../../../contracts/examples/session-page.json';
import resultsJson from '../../../contracts/examples/results.json';
import revisionJson from '../../../contracts/examples/revision.json';
import runPartialJson from '../../../contracts/examples/run-partial.json';

/**
 * 接口模式下播放地址预检失败时，页面必须把服务端的真实错误码显示出来。
 * 浏览器自己发 Range 请求时前端看不到状态码（<video> 只会给"加载失败"），
 * 所以这条路径必须单独验证：这里让预检返回 416，断言页面显示 RANGE_NOT_SATISFIABLE 与 HTTP 416。
 */
const MEDIA_ID = '00000000-0000-4000-8000-000000000002';
const PROXY_ID = '00000000-0000-4000-8000-000000000004';
const PLAYBACK_URL = `/api/v1/media/${PROXY_ID}/content`;

const hoisted = vi.hoisted(() => ({
  getSessions: vi.fn(),
  getRun: vi.fn(),
  getSessionMedia: vi.fn(),
  getResults: vi.fn(),
  getRevision: vi.fn(),
  getMedia: vi.fn(),
}));

vi.mock('../data/sources', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../data/sources')>();
  return {
    ...actual,
    isApiMode: () => true,
    getMedia: hoisted.getMedia,
    dataSource: {
      ...actual.dataSource,
      getSessions: hoisted.getSessions,
      getRun: hoisted.getRun,
      getSessionMedia: hoisted.getSessionMedia,
      getResults: hoisted.getResults,
      getRevision: hoisted.getRevision,
      getMedia: hoisted.getMedia,
    },
  };
});

vi.mock('../auth', () => ({
  AuthProvider: ({ children }: { children: ReactNode }): ReactNode => children,
  useAuth: (): unknown => ({
    status: 'authenticated',
    me: { user_id: 'user-A' },
    error: null,
    capabilities: null,
    sessionKey: 'user:A#1',
    login: async () => false,
    logout: async () => {},
    clearError: () => {},
  }),
  sessionKeyOf: () => 'user:A#1',
}));

import { SessionDetailPage } from './SessionDetailPage';

const SESSION = sessionPageJson.items[0]!;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  hoisted.getSessions.mockReset().mockResolvedValue(sessionPageJson);
  hoisted.getRun.mockReset().mockResolvedValue(runPartialJson);
  hoisted.getResults.mockReset().mockResolvedValue(resultsJson);
  hoisted.getRevision.mockReset().mockResolvedValue(revisionJson);
  hoisted.getSessionMedia
    .mockReset()
    .mockResolvedValue({ items: [{ id: MEDIA_ID, kind: 'source', status: 'ready', allowed_actions: ['playback'] }], nextCursor: null });
  hoisted.getMedia.mockReset().mockResolvedValue({
    id: MEDIA_ID,
    session_id: SESSION.id,
    source_record_id: '00000000-0000-4000-8000-000000000006',
    parent_asset_id: null,
    kind: 'source',
    status: 'ready',
    sha256: 'a'.repeat(64),
    mime_type: 'video/mp4',
    byte_size: 1024,
    duration_ms: 30000,
    expires_at: '2027-10-02T09:00:00+08:00',
    playback_asset_id: PROXY_ID,
    playback_url: PLAYBACK_URL,
    allowed_actions: ['playback'],
  });
  // 播放地址预检：服务端返回 416 + 协议错误信封。
  vi.stubGlobal('fetch', async (url: unknown) => {
    expect(String(url)).toBe(PLAYBACK_URL);
    return {
      ok: false,
      status: 416,
      text: async () =>
        JSON.stringify({
          error: { code: 'RANGE_NOT_SATISFIABLE', message: 'invalid range', details: {} },
          request_id: 'req-416',
        }),
    } as unknown as Response;
  });
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe('接口模式的播放地址预检', () => {
  it('416 时显示 RANGE_NOT_SATISFIABLE 与 HTTP 416，并给出重新检查入口', async () => {
    await act(async () => {
      root.render(<SessionDetailPage sessionId={SESSION.id} />);
    });
    await act(async () => {
      await Promise.resolve();
    });

    const tab = [...container.querySelectorAll<HTMLButtonElement>('button.tab-item')].find((item) =>
      item.textContent?.includes('回放'),
    );
    await act(async () => {
      tab?.click();
      await Promise.resolve();
    });
    // 预检是异步的，多刷一轮微任务再断言。
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    const notice = container.querySelector('.notice-strip')?.textContent ?? '';
    expect(notice).toContain('RANGE_NOT_SATISFIABLE');
    expect(notice).toContain('416');
    expect(notice).toContain('播放区间无效');
    expect(
      [...container.querySelectorAll('button')].some((item) => item.textContent?.includes('重新检查播放地址')),
    ).toBe(true);
    // 地址不可用时不再渲染播放器，避免只给一个黑框。
    expect(container.querySelector('video')).toBeNull();
  });
});
