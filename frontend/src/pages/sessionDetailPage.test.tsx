// @vitest-environment jsdom
import { act } from 'react';
import type { ReactElement, ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import fixturesSessionPage from '../../../contracts/examples/session-page.json';
import fixturesRun from '../../../contracts/examples/run-partial.json';
import capabilitiesReady from '../../../contracts/examples/service-capabilities.json';
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
  capabilities: null as import('../data/serviceCapabilities').ServiceCapabilities | null,
  getCapabilities: vi.fn(),
}));

vi.mock('../data/serviceCapabilities', async original => {
  const actual=await original<typeof import('../data/serviceCapabilities')>();
  const data=(await import('../../../contracts/examples/service-capabilities.json')).default;
  return {...actual,getServiceCapabilities:hoisted.getCapabilities,useServiceCapabilities:()=>({data:hoisted.capabilities ?? data,error:null,reload:vi.fn()})};
});
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

vi.mock('../data/m2Api', async original => ({...await original<typeof import('../data/m2Api')>(),getSource:async(id:string)=>({id,session_id:SESSION.id,title:'合成测试来源',rights_status:'verified',requested_uses:['playback','analysis'],allowed_uses:['playback','analysis'],external_processing_allowed:true,allowed_actions:[]})}));
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
import { pendingIntent } from './pendingIntent';

const SESSION = fixturesSessionPage.items[0]!;
/** 故意与课堂主媒体（…0002）不同：用来确认"重试"用的是批次自己的媒体。 */
const RUN_MEDIA_ID = '00000000-0000-4000-8000-000000000009';

let container: HTMLDivElement;
let root: Root;
let submitted: { body: string; key: string }[];

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
  hoisted.capabilities=capabilitiesReady;
  hoisted.getCapabilities.mockReset().mockResolvedValue(capabilitiesReady);
  hoisted.getSessions.mockReset().mockResolvedValue(fixturesSessionPage);
  hoisted.getRun.mockReset().mockResolvedValue(fixturesRun);
  hoisted.getSessionMedia.mockReset().mockResolvedValue({ items: [], nextCursor: null });
  setCsrfToken('test-csrf');
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
  submitted = [];
});

afterEach(async () => {
  await act(async () => {
    root.unmount();
  });
  container.remove();
  for (const request of submitted) {
    pendingIntent.resolve(hoisted.sessionKey, SESSION.id, request.key);
  }
  vi.unstubAllGlobals();
});

async function reenterDetail(): Promise<void> {
  await act(async () => root.unmount());
  root = createRoot(container);
  await renderAndSettle();
}

function mediaOptions(): void {
  hoisted.getSessionMedia.mockResolvedValue({
    items: [SESSION.primary_media_asset_id!, RUN_MEDIA_ID].map((id) => ({
      id, kind: 'source', source_record_id: '00000000-0000-4000-8000-000000000006', status: 'ready', allowed_actions: ['analyze', 'prepare_media'],
    })),
    nextCursor: null,
  });
}

function capture(init?: RequestInit): void {
  submitted.push({ body: String(init?.body), key: (init?.headers as Record<string, string>)['Idempotency-Key']! });
}

describe('详情页动作状态与会话绑定（评审意见 N01）', () => {
  it('课堂详情同时保留实时课堂与视觉观察，切换后仍绑定当前课堂', async () => {
    const reads: string[] = [];
    vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
      const url = String(input);
      reads.push(url);
      if (url.includes('/sources')) {
        return new Response(JSON.stringify({ data: { items: [], next_cursor: null }, request_id: 'sources' }), { status: 200 });
      }
      if (url.includes('/analysis-runs')) {
        return new Response(JSON.stringify({ data: { items: [], next_cursor: null }, request_id: 'visual-history' }), { status: 200 });
      }
      return new Response(JSON.stringify({ error: { code: 'NOT_FOUND', message: '没有活动采集', details: {} }, request_id: 'active' }), { status: 404 });
    });
    await renderAndSettle();
    await act(async () => {
      buttonWithExactText('实时课堂')?.click();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(container.textContent).toContain('手机发布音视频，电脑实时观看');
    expect(buttonWithExactText('创建实时采集')).not.toBeNull();
    expect(container.textContent).toContain('没有可用来源');
    await act(async () => buttonWithExactText('视觉观察')!.click());
    expect(container.textContent).toContain('还没有视觉分析记录');
    expect(reads.some(url => url.includes(`/sessions/${SESSION.id}/analysis-runs`))).toBe(true);
    await act(async () => buttonWithExactText('实时课堂')!.click());
    expect(buttonWithExactText('创建实时采集')).not.toBeNull();
  });

  it('账号切换后仍然能打开上传表单（新 scope 可写）', async () => {
    await renderAndSettle();

    await act(async () => {
      buttonByText('上传录像').click();
    });
    expect(container.querySelector('input[type="file"]')).not.toBeNull();

    // 关闭表单、切到账号 B，再点一次：状态必须属于新会话并且可写。
    await act(async () => {
      buttonByText('取消上传').click();
    });
    expect(container.querySelector('input[type="file"]')).toBeNull();

    hoisted.sessionKey = 'user:B#2';
    await renderAndSettle();

    await act(async () => {
      buttonByText('上传录像').click();
    });
    expect(container.querySelector('input[type="file"]')).not.toBeNull();
  });

  it('点击上传立即调用已挂载的文件选择器，取消选择后仍可重新选择或关闭表单', async () => {
    const choose = vi.spyOn(HTMLInputElement.prototype, 'click').mockImplementation(function (this: HTMLInputElement) {
      expect(this.type).toBe('file');
      expect(this.isConnected).toBe(true);
    });
    try {
      await renderAndSettle();
      await act(async () => buttonByText('上传录像').click());
      expect(choose).toHaveBeenCalledTimes(1);
      expect(buttonByText('上传录像').getAttribute('aria-expanded')).toBe('true');
      expect(buttonWithExactText('提交上传')?.disabled).toBe(true);
      await act(async () => buttonByText('上传录像').click());
      expect(choose).toHaveBeenCalledTimes(2);
      expect(container.querySelectorAll('input[type="file"]')).toHaveLength(1);
      await act(async () => buttonByText('取消上传').click());
      expect(container.querySelector('input[type="file"]')).toBeNull();
      expect(buttonByText('上传录像').getAttribute('aria-expanded')).toBe('false');
    } finally { choose.mockRestore(); }
  });

  it('媒体列表读取失败时显示错误与重试，并且不放行分析（评审意见 N05/N07）', async () => {
    hoisted.getSessionMedia.mockRejectedValue(
      new ApiError({ code: 'SERVICE_UNAVAILABLE', status: 503, message: 'busy' }),
    );

    await renderAndSettle();

    expect(container.textContent).toContain('服务暂时不可用');
    expect(buttonWithExactText('重试')).not.toBeNull();
    // 拿不到目标媒体的授权信息时按 fail-closed 处理，不能提交。
    expect(buttonByText('语音转写与教学报告').disabled).toBe(true);
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
        { id: RUN_MEDIA_ID, kind: 'source', source_record_id: '00000000-0000-4000-8000-000000000006', status: 'ready', allowed_actions: ['analyze'] },
        {
          id: SESSION.primary_media_asset_id!,
          kind: 'source', source_record_id: '00000000-0000-4000-8000-000000000006',
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

describe('不同未确定请求的交错与重发（R17-01/R17-02）', () => {
  it('离开详情后旧成功响应不清除新媒体请求，新媒体重发复用原键', async () => {
    hoisted.sessionKey = 'review-late-response';
    mediaOptions();
    let acceptOld!: (response: Response) => void;
    const oldResponse = new Promise<Response>((resolve) => { acceptOld = resolve; });
    vi.stubGlobal('fetch', async (_url: unknown, init?: RequestInit) => {
      capture(init);
      if (submitted.length === 1) return oldResponse;
      throw new TypeError('accepted but response lost');
    });
    await renderAndSettle();
    await act(async () => buttonByText('语音转写与教学报告').click());
    await reenterDetail();
    await act(async () => {
      const select = container.querySelector('select')!;
      select.value = RUN_MEDIA_ID;
      select.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await act(async () => buttonByText('语音转写与教学报告').click());
    expect(submitted).toHaveLength(2);
    await act(async () => acceptOld({
      ok: true, status: 202,
      text: async () => JSON.stringify({ data: fixturesRun, request_id: 'old-accepted' }),
    } as Response));
    await act(async () => buttonByText('语音转写与教学报告').click());
    expect(submitted).toHaveLength(3);
    expect(submitted[2]!.body).toBe(submitted[1]!.body);
    expect(submitted[2]!.key).toBe(submitted[1]!.key);
    expect(submitted[2]!.key).not.toBe(submitted[0]!.key);
  });

  it('report_only 换修订后用新键，返回原修订仍复用原键', async () => {
    hoisted.sessionKey = 'review-revision-retry';
    mediaOptions();
    const r1 = '00000000-0000-4000-8000-000000000091';
    const r2 = '00000000-0000-4000-8000-000000000092';
    const failed = {
      ...fixturesRun, status: 'failed', mode: 'report_only',
      input_transcript_revision_id: r1, allowed_actions: ['retry'],
    };
    hoisted.getRun.mockResolvedValue(failed);
    vi.stubGlobal('fetch', async (_url: unknown, init?: RequestInit) => {
      capture(init);
      throw new TypeError('accepted but response lost');
    });
    await renderAndSettle();
    await act(async () => buttonWithExactText('重试')!.click());
    hoisted.getRun.mockResolvedValue({ ...failed, input_transcript_revision_id: r2 });
    await reenterDetail();
    await act(async () => buttonWithExactText('重试')!.click());
    expect(submitted[0]!.body).not.toBe(submitted[1]!.body);
    expect(submitted[0]!.key).not.toBe(submitted[1]!.key);
    expect(JSON.parse(submitted[0]!.body).input_transcript_revision_id).toBe(r1);
    expect(JSON.parse(submitted[1]!.body).input_transcript_revision_id).toBe(r2);
    hoisted.getRun.mockResolvedValue(failed);
    await reenterDetail();
    await act(async () => buttonWithExactText('重试')!.click());
    expect(submitted[2]!.body).toBe(submitted[0]!.body);
    expect(submitted[2]!.key).toBe(submitted[0]!.key);
  });
});


it('unconfigured audio disables only its own analysis choice',async()=>{
 mediaOptions();hoisted.capabilities={...capabilitiesReady,audio_report:{available:false,reasons:['ASR_NOT_CONFIGURED']}};
 await renderAndSettle();
 expect(buttonByText('语音转写与教学报告').disabled).toBe(true);
 expect(buttonByText('举手与低头识别').disabled).toBe(false);
 expect(container.textContent).toContain('尚未配置语音识别');
});
it('rechecks service availability before creating a paid analysis',async()=>{
 mediaOptions();hoisted.getCapabilities.mockResolvedValue({...capabilitiesReady,audio_report:{available:false,reasons:['BUDGET_UNAVAILABLE']}});
 const fetcher=vi.fn();vi.stubGlobal('fetch',fetcher);
 await renderAndSettle();await act(async()=>buttonByText('语音转写与教学报告').click());
 expect(fetcher).not.toHaveBeenCalled();
 expect(container.textContent).toContain('模型可用预算不足');
});
