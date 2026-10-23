// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, expect, it, vi } from 'vitest';
const h = vi.hoisted(() => ({ detect: vi.fn() }));
vi.mock('../capture/liveVision', async original => ({
  ...await original<typeof import('../capture/liveVision')>(), detectLiveFrame: (...args: unknown[]) => h.detect(...args),
}));
import { LiveVisionPreview } from './LiveVisionPreview';
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); h.detect.mockReset(); });
it('draws boxes on the submitted snapshot and removes stale frames; aborts on leaving', async () => {
  vi.useFakeTimers();
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
  const context = { drawImage: vi.fn(), strokeRect: vi.fn(), fillText: vi.fn(), lineWidth: 1, font: '', strokeStyle: '', fillStyle: '' };
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as unknown as CanvasRenderingContext2D);
  const video = document.createElement('video');
  Object.defineProperties(video, { readyState: { value: 2 }, paused: { value: false }, videoWidth: { value: 640 }, videoHeight: { value: 360 } });
  const reference = { current: video };
  const responses: ((v: unknown) => void)[] = [];
  h.detect.mockImplementation(() => new Promise(resolve => responses.push(resolve)));
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container);
  try {
    await act(async () => root.render(<LiveVisionPreview captureId="capture" video={reference} enabled />));
    const snapshot = h.detect.mock.calls[0]![2] as HTMLCanvasElement;
    expect(snapshot).not.toBe(container.querySelector('canvas'));
    await act(async () => responses[0]!({ sequence: 1, width: 640, height: 360, people: [{ track_id: 'L1', bbox: [.1,.2,.3,.4], hand_raise: true, head_down: null }], error_code: null }));
    expect(context.drawImage.mock.calls.at(-1)?.[0]).toBe(snapshot);
    expect(context.strokeRect).toHaveBeenCalledWith(64, 72, expect.any(Number), 72);
    expect(container.querySelector('canvas')?.hidden).toBe(false);
    await act(async () => { await vi.advanceTimersByTimeAsync(5001); });
    expect(container.querySelector('canvas')?.hidden).toBe(true);
    expect(container.textContent).toContain('显示实时原画');
    const signal = h.detect.mock.calls.at(-1)![3] as AbortSignal;
    act(() => root.unmount()); expect(signal.aborted).toBe(true);
  } finally { container.remove(); }
});
