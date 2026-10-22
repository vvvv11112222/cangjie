// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import sessionPage from '../../../contracts/examples/session-page.json';
import { ApiError } from '../data/errors';
import { parseSessionPage } from '../data/samples';
const h = vi.hoisted(() => ({ sources: vi.fn(), active: vi.fn() }));
vi.mock('../data/m2Api', () => ({ listSessionSources: (...args: unknown[]) => h.sources(...args) }));
vi.mock('../data/liveCaptureApi', () => ({ getActiveLiveCapture: (...args: unknown[]) => h.active(...args), createLiveCapture: vi.fn(), createPairing: vi.fn(), createViewer: vi.fn(), getLiveCapture: vi.fn(), stopLiveCapture: vi.fn() }));
import { LiveCapturePanel } from './LiveCapturePanel';
const lesson = { ...parseSessionPage(sessionPage).items[0]!, allowed_actions: ['upload' as const] };
const source = { id: 'source-id', title: '合法课堂录像', rights_status: 'verified', allowed_uses: ['playback', 'analysis'] };
let root: Root; let container: HTMLDivElement;
async function render() { await act(async () => root.render(<LiveCapturePanel session={lesson} enabled onUseMedia={() => {}}/>)); }
beforeEach(() => { Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); h.sources.mockReset().mockResolvedValue({ items: [source], next_cursor: null }); h.active.mockReset().mockResolvedValue(null); container = document.createElement('div'); document.body.append(container); root = createRoot(container); });
afterEach(() => { act(() => root.unmount()); container.remove(); });
it('keeps verified sources visible when capture service is disabled', async () => {
  h.active.mockRejectedValue(new ApiError({ status: 503, code: 'SERVICE_UNAVAILABLE', message: 'disabled' })); await render();
  expect(container.querySelector('select')?.textContent).toContain('合法课堂录像');
  expect(container.textContent).toContain('实时采集服务尚未启用');
  expect(container.textContent).not.toContain('没有已核验');
  expect([...container.querySelectorAll('button')].find(b => b.textContent === '创建实时采集')?.disabled).toBe(true);
});
it('404 means no active capture; existing source can create a new one', async () => {
  h.active.mockRejectedValue(new ApiError({ status: 404, code: 'NOT_FOUND', message: 'none' })); await render();
  expect([...container.querySelectorAll('button')].find(b => b.textContent === '创建实时采集')?.disabled).toBe(false);
  expect(container.textContent).not.toContain('无法读取采集状态');
});
it('source error is not mislabeled as no sources and retry reloads both', async () => {
  h.sources.mockRejectedValueOnce(new Error('SOURCE-503')); await render();
  expect(container.querySelector('select')?.textContent).toBe('来源读取失败');
  expect(container.textContent).toContain('SOURCE-503');
  await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === '重新检查来源与采集服务')!.click());
  expect(container.querySelector('select')?.textContent).toContain('合法课堂录像'); expect(h.active).toHaveBeenCalledTimes(2);
});
