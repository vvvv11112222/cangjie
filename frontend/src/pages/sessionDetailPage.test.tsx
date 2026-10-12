// @vitest-environment jsdom
import { act } from 'react';
import type { ReactElement, ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import fixturesSessionPage from '../../../contracts/examples/session-page.json';
import fixturesRun from '../../../contracts/examples/run-partial.json';
import { ApiError } from '../data/errors';
import { setCsrfToken } from '../data/http';

/**
 * 评审意见 N01（P1）：会话变化后必须真的重建可写状态。
 * 旧实现只在渲染时用 actionStateFor 兜底，state 里仍是旧 scope，
 * 于是"详情页等到认证恢复后点上传 / A→B 切换后点上传"都会点不动（表单打不开）。
 * 这里用真实页面 + jsdom 把这条路径跑一遍。
 */
const hoisted = vi.hoisted(() => ({
  sessionKey: 'user:A#1',
  getSessions: vi.fn(),
  getRun: vi.fn(),
  getSessionMedia: vi.fn(),
}));

vi.mock('../data/sources', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../data/sources')>();
  return {
    ...actual,
    isApiMode: () => true,
    getSessionMedia: hoisted.getSessionMedia,
    dataSource: {
      ...actual.dataSource,
      getSessions: hoisted.getSessions,
      getRun: hoisted.getRun,
      getSessionMedia: hoisted.getSessionMedia,
    },
  };
});

vi.mock('../auth', () => ({
  AuthProvider: ({ children }: { children: ReactNode }): ReactNode => children,
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
  sessionKeyOf: () => hoisted.sessionKey,
}));

import { SessionDetailPage } from './SessionDetailPage';

const SESSION = fixturesSessionPage.items[0]!;
/** 故意与课堂主媒体（…0002）不同：用来确认"重试"用的是批次自己的媒体。 */
const RUN_MEDIA_ID = '00000000-0000-4000-8000-000000000009';

let container: HTMLDivElement;
let root: Root;

function page(): ReactElement {
  return <SessionDetailPage sessionId={SESSION.id} />;
}

function buttonByText(text: string): HTMLButtonElement {
  const found = [...container.querySelectorAll('button')].find((item) => item.textContent?.includes(text));
  if (found === undefined) {
    throw new Error(`页面上找不到按钮：${text}`);
  }
  return found as HTMLButtonElement;
}

/** 精确匹配按钮文字，避免"重试"命中"重试读取"。 */
function buttonWithExactText(text: string): HTMLButtonElement | null {
  const found = [...container.querySelectorAll('button')].find((item) => item.textContent?.trim() === text);
  return found === undefined ? null : (found as HTMLButtonElement);
}

async function renderAndSettle(): Promise<void> {
  await act(async () => {
    root.render(page());
  });
  await act(async () => {
    await Promise.resolve();
  });
}

beforeEach(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  hoisted.sessionKey = 'user:A#1';
  hoisted.getSessions.mockReset().mockResolvedValue(fixturesSessionPage);
  hoisted.getRun.mockReset().mockResolvedValue(fixturesRun);
  hoisted.getSessionMedia.mockReset().mockResolvedValue({ items: [], nextCursor: null });
  setCsrfToken('test-csrf');
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

describe('详情页动作状态与会话绑定（评审意见 N01）', () => {
  it('账号切换后仍然能打开上传表单（新 scope 可写）', async () => {
    await renderAndSettle();

    await act(async () => {
      buttonByText('上传录像').click();
    });
    expect(container.querySelector('input[type="file"]')).not.toBeNull();

    // 关闭表单、切到账号 B，再点一次：状态必须属于新会话并且可写。
    await act(async () => {
      buttonByText('上传录像').click();
    });
    expect(container.querySelector('input[type="file"]')).toBeNull();

    hoisted.sessionKey = 'user:B#2';
    await renderAndSettle();

    await act(async () => {
      buttonByText('上传录像').click();
    });
    expect(container.querySelector('input[type="file"]')).not.toBeNull();
  });

  it('媒体列表读取失败时显示错误与重试，并且不放行分析（评审意见 N05/N07）', async () => {
    hoisted.getSessionMedia.mockRejectedValue(
      new ApiError({ code: 'SERVICE_UNAVAILABLE', status: 503, message: 'busy' }),
    );

    await renderAndSettle();

    expect(container.textContent).toContain('服务暂时不可用');
    expect(buttonWithExactText('重试')).not.toBeNull();
    // 拿不到目标媒体的授权信息时按 fail-closed 处理，不能提交。
    expect(buttonByText('提交分析').disabled).toBe(true);
    expect(buttonByText('媒体准备').disabled).toBe(true);
  });

  it('批次读取失败时不再同时声称“没有批次”（评审意见 N08）', async () => {
    hoisted.getRun.mockRejectedValue(
      new ApiError({ code: 'SERVICE_UNAVAILABLE', status: 503, message: 'busy' }),
    );

    await renderAndSettle();

    expect(container.textContent).toContain('批次读取失败');
    expect(buttonWithExactText('重试读取')).not.toBeNull();
    expect(container.textContent).not.toContain('还没有分析批次');
    expect(container.textContent).not.toContain('暂无批次');
  });

  it('“重试”重放的是失败批次自己的媒体，而不是当前选中的目标', async () => {
    const failedRun = {
      ...fixturesRun,
      status: 'failed',
      media_asset_id: RUN_MEDIA_ID,
      allowed_actions: ['retry'],
    };
    hoisted.getRun.mockResolvedValue(failedRun);
    hoisted.getSessionMedia.mockResolvedValue({
      items: [
        { id: RUN_MEDIA_ID, kind: 'source', status: 'ready', allowed_actions: ['analyze'] },
        {
          id: SESSION.primary_media_asset_id!,
          kind: 'source',
          status: 'ready',
          allowed_actions: ['analyze'],
        },
      ],
      nextCursor: null,
    });

    const bodies: string[] = [];
    vi.stubGlobal('fetch', async (_url: unknown, init?: RequestInit) => {
      bodies.push(String(init?.body ?? ''));
      return {
        ok: true,
        status: 202,
        text: async () => JSON.stringify({ data: failedRun, request_id: 'r' }),
      } as unknown as Response;
    });

    await renderAndSettle();
    await act(async () => {
      buttonByText('重试').click();
    });
    await act(async () => {
      await Promise.resolve();
    });

    expect(bodies).toHaveLength(1);
    expect(JSON.parse(bodies[0]!).media_asset_id).toBe(RUN_MEDIA_ID);
    vi.unstubAllGlobals();
  });
});
