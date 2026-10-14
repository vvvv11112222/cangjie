// @vitest-environment jsdom
import { act } from 'react';
import type { ReactElement } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { MediaPageLite } from './sources';
import type { Session } from '../types';

/**
 * 评审意见 N03：刷新（reload）也是副作用，必须按会话隔离。
 * 旧会话的迟到动作调用 reload 时，不能把新会话正在进行的加载关掉，
 * 否则新账号的页面会一直停在"正在读取数据"。
 */
const hoisted = vi.hoisted(() => ({ sessionKey: 'user:A#1', getSessionMedia: vi.fn() }));

vi.mock('./sources', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./sources')>();
  return {
    ...actual,
    isApiMode: () => true,
    getSessionMedia: hoisted.getSessionMedia,
    dataSource: { ...actual.dataSource, getSessionMedia: hoisted.getSessionMedia },
  };
});

vi.mock('../auth', () => ({
  useAuth: (): unknown => ({
    status: 'authenticated',
    me: null,
    error: null,
    capabilities: null,
    sessionKey: hoisted.sessionKey,
    login: async () => false,
    logout: async () => {},
    clearError: () => {},
  }),
  AuthProvider: ({ children }: { children: unknown }): unknown => children,
  sessionKeyOf: () => hoisted.sessionKey,
}));

import { useSessionMedia } from './hooks';

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

const page = (items: MediaPageLite['items']): MediaPageLite => ({ items, nextCursor: null });

let capturedReload: (() => void) | null = null;

function Harness({ sessionId }: { sessionId: string }): ReactElement {
  const media = useSessionMedia({ id: sessionId } as Session, true);
  capturedReload = media.reload;
  return (
    <div
      data-testid="out"
      data-status={media.status}
      data-count={String(media.data.items.length)}
    />
  );
}

let container: HTMLDivElement;
let root: Root;

function out(): HTMLElement {
  return container.querySelector('[data-testid="out"]') as HTMLElement;
}

beforeEach(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  hoisted.sessionKey = 'user:A#1';
  hoisted.getSessionMedia.mockReset();
  capturedReload = null;
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => {
    root.unmount();
  });
  container.remove();
});

describe('刷新副作用按会话隔离（评审意见 N03）', () => {
  it('旧会话的迟到 reload 不会打断新会话的加载', async () => {
    const first = deferred<MediaPageLite>();
    const second = deferred<MediaPageLite>();
    hoisted.getSessionMedia.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);

    // 账号 A 的课堂：请求还在飞。
    await act(async () => {
      root.render(<Harness sessionId="session-a" />);
    });
    const staleReload = capturedReload;
    expect(staleReload).not.toBeNull();

    // 切到账号 B 的课堂：新的加载开始。
    hoisted.sessionKey = 'user:B#2';
    await act(async () => {
      root.render(<Harness sessionId="session-b" />);
    });

    // A 的动作此刻才返回并调用它自己的 reload。
    await act(async () => {
      staleReload?.();
    });

    // B 的响应正常落地：如果旧 reload 关掉了 B 的上下文，这里会一直停在 loading。
    await act(async () => {
      second.resolve(page([]));
      await Promise.resolve();
    });
    expect(hoisted.getSessionMedia).toHaveBeenCalledTimes(2);
    expect(out().getAttribute('data-status')).toBe('ready');
    expect(hoisted.getSessionMedia).toHaveBeenLastCalledWith('session-b', undefined);
  });
});
