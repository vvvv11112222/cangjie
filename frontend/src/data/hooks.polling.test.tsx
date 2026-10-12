// @vitest-environment jsdom
import { act } from 'react';
import type { ReactElement } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './errors';
import type { Run, RunStatus } from '../types';
import runPartialJson from '../../../contracts/examples/run-partial.json';

/**
 * 评审意见 F05/F07：轮询必须串行、失败必须可见。
 * 这两个问题都发生在"组件里的异步状态写回"，所以用 jsdom 渲染真实的 hook 来验证。
 */
const hoisted = vi.hoisted(() => ({ getRun: vi.fn() }));

vi.mock('./sources', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./sources')>();
  return { ...actual, dataSource: { ...actual.dataSource, getRun: hoisted.getRun } };
});

import { useRunPolling } from './hooks';

const RUN_ID = runPartialJson.id;

function runWith(status: RunStatus): Run {
  return { ...runPartialJson, status } as unknown as Run;
}

function Harness({ status }: { status: RunStatus }): ReactElement {
  const poll = useRunPolling(RUN_ID, status, true);
  return (
    <div
      data-testid="out"
      data-status={poll.run?.status ?? ''}
      data-error={poll.error ?? ''}
      data-polling={poll.polling ? 'yes' : 'no'}
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
  vi.useFakeTimers();
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
  hoisted.getRun.mockReset();
});

afterEach(async () => {
  await act(async () => {
    root.unmount();
  });
  container.remove();
  vi.useRealTimers();
});

describe('轮询串行与错误暴露（评审意见 F05/F07）', () => {
  it('上一次请求没返回前不会发起下一次，终态响应不会被旧响应覆盖', async () => {
    const resolvers: ((run: Run) => void)[] = [];
    hoisted.getRun.mockImplementation(
      () => new Promise<Run>((resolve) => {
        resolvers.push(resolve);
      }),
    );

    await act(async () => {
      root.render(<Harness status="running" />);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(hoisted.getRun).toHaveBeenCalledTimes(1);

    // 再等三个轮询周期：因为是串行的，不应该出现第二个在途请求。
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(hoisted.getRun).toHaveBeenCalledTimes(1);

    // 第一个请求返回终态：页面显示终态，并停止轮询。
    await act(async () => {
      resolvers[0]?.(runWith('succeeded'));
      await Promise.resolve();
    });
    expect(out().getAttribute('data-status')).toBe('succeeded');
    expect(out().getAttribute('data-polling')).toBe('no');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(hoisted.getRun).toHaveBeenCalledTimes(1);
  });

  it('不可恢复错误（401/403/410）停止轮询并显示原因', async () => {
    hoisted.getRun.mockRejectedValue(new ApiError({ code: 'FORBIDDEN', status: 403, message: 'forbidden' }));

    await act(async () => {
      root.render(<Harness status="running" />);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(out().getAttribute('data-polling')).toBe('no');
    expect(out().getAttribute('data-error')).toContain('权限');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(hoisted.getRun).toHaveBeenCalledTimes(1);
  });

  it('临时故障保留上次结果、提示未更新，并继续重试', async () => {
    hoisted.getRun
      .mockRejectedValueOnce(new ApiError({ code: 'SERVICE_UNAVAILABLE', status: 503, message: 'busy' }))
      .mockResolvedValue(runWith('succeeded'));

    await act(async () => {
      root.render(<Harness status="running" />);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(out().getAttribute('data-polling')).toBe('yes');
    expect(out().getAttribute('data-error')).toContain('服务暂时不可用');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(hoisted.getRun).toHaveBeenCalledTimes(2);
    expect(out().getAttribute('data-status')).toBe('succeeded');
    expect(out().getAttribute('data-error')).toBe('');
    expect(out().getAttribute('data-polling')).toBe('no');
  });
});
