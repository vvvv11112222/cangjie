// @vitest-environment jsdom
import { act } from 'react';
import type { ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * M2 回放标签的端到端检查（固定样例模式）：真的渲染课堂详情、切到「回放」标签、
 * 点一个片段跳转，确认播放器拿到了仓库内样例视频并且定位到了原录像毫秒。
 * 这里不 mock 数据源，走的就是页面默认的固定样例路径。
 */
vi.mock('../auth', () => ({
  AuthProvider: ({ children }: { children: ReactNode }): ReactNode => children,
  useAuth: (): unknown => ({
    status: 'anonymous',
    me: null,
    error: null,
    capabilities: null,
    sessionKey: 'local',
    login: async () => false,
    logout: async () => {},
    clearError: () => {},
  }),
  sessionKeyOf: () => 'local',
}));

import { SessionDetailPage } from './SessionDetailPage';

/** 固定样例里那条课堂（session-page.json）。 */
const SESSION_ID = '00000000-0000-4000-8000-000000000001';

let container: HTMLDivElement;
let root: Root;

function tabNamed(text: string): HTMLButtonElement {
  const found = [...container.querySelectorAll('button.tab-item')].find((item) =>
    item.textContent?.includes(text),
  );
  if (found === undefined) {
    throw new Error(`页面上找不到标签：${text}`);
  }
  return found as HTMLButtonElement;
}

function buttonByText(text: string): HTMLButtonElement {
  const found = [...container.querySelectorAll('button')].find((item) =>
    item.textContent?.trim().startsWith(text),
  );
  if (found === undefined) {
    throw new Error(`页面上找不到按钮：${text}`);
  }
  return found as HTMLButtonElement;
}

beforeEach(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

describe('课堂详情的回放标签', () => {
  it('播放仓库内样例视频，并按片段时间跳转与倍速播放', async () => {
    await act(async () => {
      root.render(<SessionDetailPage sessionId={SESSION_ID} />);
    });

    await act(async () => {
      tabNamed('回放').click();
    });

    const video = container.querySelector('video');
    expect(video).not.toBeNull();
    expect(video?.getAttribute('src')).toContain('samples/m2-playback-demo.mp4');

    // 固定样例里那段转写是 1000–5000ms，点击后播放器应定位到 1 秒。
    // 同一时间点还有一条转写证据，所以按文案挑出"片段 0"那一行。
    const jump = [...container.querySelectorAll<HTMLButtonElement>('.jump-item')].find((item) =>
      item.textContent?.includes('片段 0'),
    );
    expect(jump).toBeDefined();
    await act(async () => {
      jump?.click();
    });
    expect(video?.currentTime).toBe(1);
    // 点过的那一行要高亮（同一时间点还有一条证据，不能高亮错行）。
    expect(jump?.getAttribute('aria-current')).toBe('true');

    // 倍速：默认 1×，切换后立即作用到播放器。
    const speed = container.querySelector<HTMLSelectElement>('.player-speed select');
    expect(speed?.value).toBe('1');
    await act(async () => {
      if (speed !== null) {
        speed.value = '1.5';
        speed.dispatchEvent(new Event('change', { bubbles: true }));
      }
    });
    expect(video?.playbackRate).toBe(1.5);
  });

  it('固定样例模式说明播放的是仓库内合成样例视频', async () => {
    await act(async () => {
      root.render(<SessionDetailPage sessionId={SESSION_ID} />);
    });
    await act(async () => {
      tabNamed('回放').click();
    });
    expect(container.textContent).toContain('合成样例视频');
    // 协议字段仍然可查，只是下沉到折叠区（UI 约定第 4 节）。
    expect(container.textContent).toContain('media.playback_url');
  });

  it('回放提供前后 5 秒与可开关的当前片段字幕', async () => {
    await act(async () => {
      root.render(<SessionDetailPage sessionId={SESSION_ID} />);
    });
    await act(async () => {
      tabNamed('回放').click();
    });
    const video = container.querySelector('video');
    // 位置 0ms 不在片段区间内：提示"这一段没有字幕"。
    // 字幕本身走浏览器原生字幕轨（jsdom 没实现），所以这里只断言提示行和开关行为。
    expect(container.querySelector('.player-caption-note')?.textContent).toContain('当前位置没有片段字幕');

    const jump = [...container.querySelectorAll<HTMLButtonElement>('.jump-item')].find((item) =>
      item.textContent?.includes('片段 0'),
    );
    await act(async () => {
      jump?.click();
    });
    // 跳到片段上以后不再提示"没有字幕"，字幕交给原生字幕轨显示。
    expect(container.querySelector('.player-caption-note')?.textContent).not.toContain('当前位置没有片段字幕');

    // 字幕开关：关掉后不再叠加，按钮状态同步。
    await act(async () => {
      buttonByText('字幕：开').click();
    });
    expect(container.querySelector('.player-caption')).toBeNull();
    expect(buttonByText('字幕：关').getAttribute('aria-pressed')).toBe('false');

    // 前后 5 秒：从 1.0 秒前进到 6.0 秒，再退回 1.0 秒。
    await act(async () => {
      buttonByText('前进 5 秒').click();
    });
    expect(video?.currentTime).toBe(6);
    await act(async () => {
      buttonByText('后退 5 秒').click();
    });
    expect(video?.currentTime).toBe(1);
  });
});
