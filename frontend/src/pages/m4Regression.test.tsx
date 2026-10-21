// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Deletion, ModelCall, Operations, Session } from '../types';

const state = vi.hoisted(() => ({
  account: 'm4-account',
  getOperations: vi.fn(),
  listModelCalls: vi.fn(),
  reconcileModelCall: vi.fn(),
  createDeletion: vi.fn(),
  getDeletion: vi.fn(),
}));
vi.mock('../auth', () => ({ useAuth: () => ({ me: { user_id: state.account }, sessionKey: `${state.account}#1` }) }));

vi.mock('../data/sources', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../data/sources')>();
  return { ...actual, isApiMode: () => true };
});

vi.mock('../data/m4Api', () => ({
  getOperations: state.getOperations,
  listModelCalls: state.listModelCalls,
  reconcileModelCall: state.reconcileModelCall,
  createDeletion: state.createDeletion,
  getDeletion: state.getDeletion,
}));

import { OperationsPage } from './OperationsPage';
import { DeletionPanel } from './DeletionPanel';

const id = '00000000-0000-4000-8000-000000000071';
const operations: Operations = {
  workers: [{ worker_id: 'worker-01', last_seen_at: '2026-10-18T10:00:00+08:00', capabilities: ['probe', 'audio_analysis'] }],
  queued_jobs: 3,
  storage_free_bytes: 1024 * 1024 * 8,
  budget: { currency: 'CNY', billing_period: '2026-10-01', limit: '10.000000', charged: '2.000000', reserved: '1.000000', unknown: '0.500000' },
};
const modelCall: ModelCall = {
  id, status: 'unknown', reserved_cost: '1.000000', actual_cost: null, currency: 'CNY',
  billing_period: '2026-10-01', price_version: 'price-v1', lock_version: 2,
  dispatch_started_at: '2026-10-18T10:00:00+08:00', settled_at: null,
};
const session: Session = {
  id, offering_id: '00000000-0000-4000-8000-000000000072', schedule_entry_id: null,
  title: '待清理课堂', planned_start_at: '2026-10-18T09:00:00+08:00', planned_end_at: '2026-10-18T10:00:00+08:00',
  is_demo: false, status: 'ready', transcript_lock_version: 0, primary_media_asset_id: null,
  latest_run_id: null, current_report_id: null, allowed_actions: ['delete'],
};
const deletion: Deletion = { id: '00000000-0000-4000-8000-000000000073', session_id: id, status: 'requested', error_code: null, completed_at: null };

let container: HTMLDivElement;
let root: Root;

function button(text: string): HTMLButtonElement {
  const result = [...container.querySelectorAll('button')].find((item) => item.textContent?.trim() === text);
  if (result === undefined) throw new Error(`找不到按钮：${text}`);
  return result as HTMLButtonElement;
}

async function change(input: HTMLInputElement | HTMLTextAreaElement, value: string): Promise<void> {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value')?.set;
    setter?.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

beforeEach(() => {
  sessionStorage.clear(); state.account = 'm4-account';
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  state.getOperations.mockReset().mockResolvedValue(operations);
  state.listModelCalls.mockReset().mockResolvedValue({ items: [modelCall], next_cursor: null });
  state.reconcileModelCall.mockReset().mockResolvedValue({ ...modelCall, status: 'failed', actual_cost: '0.250000', lock_version: 3 });
  state.createDeletion.mockReset().mockResolvedValue(deletion);
  state.getDeletion.mockReset().mockResolvedValue({ ...deletion, status: 'succeeded', completed_at: '2026-10-18T11:00:00+08:00' });
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.useRealTimers();
});

describe('M4 页面回归', () => {
  it('运行页展示真实预算分栏、Worker 和 unknown 账目入口', async () => {
    await act(async () => {
      root.render(<OperationsPage />);
      await Promise.resolve(); await Promise.resolve();
    });
    expect(container.textContent).toContain('运行与设置');
    expect(container.textContent).toContain('计费未知');
    expect(container.textContent).toContain('¥ 0.500000');
    expect(container.textContent).toContain('worker-01');
    expect(container.textContent).toContain('媒体检查、音频转写');
    expect(button('核对账目')).toBeTruthy();
  });

  it('切换核对调用后清空上一条调用的费用和供应商信息', async () => {
    const second = { ...modelCall, id: 'call-b', lock_version: 7 };
    state.listModelCalls.mockResolvedValue({ items: [modelCall, second], next_cursor: null });
    await act(async () => root.render(<OperationsPage />));
    await act(async () => container.querySelectorAll<HTMLButtonElement>('tbody button')[0]!.click());
    const inputs = container.querySelectorAll<HTMLInputElement>('form input');
    await change(inputs[0]!, '1.250000'); await change(inputs[1]!, 'provider-a'); await change(inputs[2]!, '调用 A 的原因');
    await act(async () => container.querySelectorAll<HTMLButtonElement>('tbody button')[1]!.click());
    const nextInputs = container.querySelectorAll<HTMLInputElement>('form input');
    expect([...nextInputs].map(input => input.value)).toEqual(['0.000000', '', '']);
    await change(nextInputs[2]!, '调用 B 的原因');
    await act(async () => container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
    expect(state.reconcileModelCall).toHaveBeenCalledWith(second.id, { lock_version: 7, status: 'failed', actual_cost: '0.000000', provider_request_id: null, reason: '调用 B 的原因' });
  });

  it.each(['operations', 'calls'])('另一项请求未结束时仍显示已经返回的 %s', async (kind) => {
    if (kind === 'operations') state.listModelCalls.mockReturnValue(new Promise(() => {}));
    else state.getOperations.mockReturnValue(new Promise(() => {}));
    await act(async () => root.render(<OperationsPage />));
    if (kind === 'operations') expect(container.textContent).toContain('worker-01');
    else expect(button('核对账目')).toBeTruthy();
  });

  it('刷新失败时保留已经展示的运行状态和账目，并提供重试', async () => {
    await act(async () => root.render(<OperationsPage />));
    state.getOperations.mockRejectedValueOnce(new Error('operations unavailable'));
    state.listModelCalls.mockRejectedValueOnce(new Error('calls unavailable'));
    await act(async () => button('刷新状态').click());
    expect(container.textContent).toContain('worker-01');
    expect(container.textContent).toContain('¥ 0.500000');
    expect(button('重新读取')).toBeTruthy();
    expect(button('重新读取账目')).toBeTruthy();
  });

  it('运行状态重试等待账目分页结束，保留连续记录和下一页游标', async () => {
    const second = { ...modelCall, id: 'call-second' };
    const third = { ...modelCall, id: 'call-third' };
    let finishThird!: (value: { items: ModelCall[]; next_cursor: string }) => void;
    state.getOperations.mockRejectedValueOnce(new Error('operations unavailable'));
    state.listModelCalls
      .mockResolvedValue({ items: [modelCall], next_cursor: 'second' })
      .mockResolvedValueOnce({ items: [modelCall], next_cursor: 'second' })
      .mockResolvedValueOnce({ items: [second], next_cursor: 'third' })
      .mockReturnValueOnce(new Promise(resolve => { finishThird = resolve; }));
    await act(async () => root.render(<OperationsPage />));
    await act(async () => button('加载更多账目').click());
    await act(async () => button('加载更多账目').click());
    expect(button('重新读取').disabled).toBe(true);
    await act(async () => button('重新读取').click());
    expect(state.getOperations).toHaveBeenCalledTimes(1);
    expect(state.listModelCalls).toHaveBeenCalledTimes(3);
    await act(async () => finishThird({ items: [third], next_cursor: 'fourth' }));
    expect(container.querySelectorAll('tbody tr')).toHaveLength(3);
    expect(button('重新读取').disabled).toBe(false);
    await act(async () => button('加载更多账目').click());
    expect(state.listModelCalls).toHaveBeenLastCalledWith('fourth', 20);
    await act(async () => button('重新读取').click());
    expect(state.getOperations).toHaveBeenCalledTimes(2);
    expect(container.textContent).toContain('worker-01');
  });

  it('核对账目提交期间禁止运行状态重试关闭表单', async () => {
    let finish!: (value: ModelCall) => void;
    state.getOperations.mockRejectedValueOnce(new Error('operations unavailable'));
    state.reconcileModelCall.mockReturnValue(new Promise(resolve => { finish = resolve; }));
    await act(async () => root.render(<OperationsPage />));
    await act(async () => button('核对账目').click());
    await change(container.querySelectorAll<HTMLInputElement>('form input')[2]!, '核对原因');
    await act(async () => button('提交核对').click());
    expect(button('重新读取').disabled).toBe(true);
    await act(async () => button('重新读取').click());
    expect(state.getOperations).toHaveBeenCalledTimes(1);
    expect(container.querySelector('form')).not.toBeNull();
    await act(async () => finish({ ...modelCall, status: 'failed', actual_cost: '0.000000' }));
    expect(container.querySelector('form')).toBeNull();
    expect(container.textContent).toContain('模型调用账目已核对');
  });

  it('彻底清理要求原因和确认文字，并提交真实课堂 id', async () => {
    const changed = vi.fn();
    await act(async () => root.render(<DeletionPanel session={session} api onSessionChanged={changed} />));
    await act(async () => button('发起彻底清理').click());

    const reason = container.querySelector('textarea')!;
    const confirmation = container.querySelector('input')!;
    expect(button('确认彻底清理').disabled).toBe(true);
    await change(reason, '超过获准保留期限');
    await change(confirmation, '彻底清理');
    expect(button('确认彻底清理').disabled).toBe(false);

    await act(async () => {
      button('确认彻底清理').click();
      await Promise.resolve(); await Promise.resolve();
    });
    expect(state.createDeletion).toHaveBeenCalledWith(id, '超过获准保留期限', expect.any(String));
    expect(container.textContent).toContain('当前状态：已请求');
    expect(changed).toHaveBeenCalled();
  });

  async function submitDeletion(): Promise<void> {
    await act(async () => button('发起彻底清理').click());
    await change(container.querySelector('textarea')!, '原始清理原因');
    await change(container.querySelector('input')!, '彻底清理');
    await act(async () => button('确认彻底清理').click());
  }

  it('切换标签和整页重建后恢复请求并继续轮询，账号之间隔离', async () => {
    vi.useFakeTimers();
    state.getDeletion.mockResolvedValue({ ...deletion, status: 'blocked' });
    const changed = vi.fn();
    await act(async () => root.render(<DeletionPanel session={session} api onSessionChanged={changed} />));
    await submitDeletion();
    await act(async () => root.unmount()); root = createRoot(container);
    await act(async () => root.render(<DeletionPanel session={{ ...session, status: 'deleting' }} api onSessionChanged={changed} />));
    expect(state.getDeletion).toHaveBeenCalledWith(deletion.id);
    expect(container.textContent).toContain('清理受阻');
    state.getDeletion.mockResolvedValue({ ...deletion, status: 'succeeded' });
    await act(async () => { await vi.advanceTimersByTimeAsync(8000); });
    expect(state.getDeletion).toHaveBeenCalledTimes(2);
    expect(container.textContent).toContain('清理完成');
    await act(async () => root.unmount()); root = createRoot(container); state.account = 'other-admin';
    await act(async () => root.render(<DeletionPanel session={{ ...session, status: 'deleting' }} api onSessionChanged={changed} />));
    expect(container.textContent).not.toContain(deletion.id);
    expect(state.getDeletion).toHaveBeenCalledTimes(2);
  });

  it('未确认的清理请求禁止更改原因，重进页面重试仍发送原参数与原键', async () => {
    state.createDeletion.mockRejectedValue(new TypeError('response lost'));
    await act(async () => root.render(<DeletionPanel session={session} api onSessionChanged={() => {}} />));
    await submitDeletion();
    const first = state.createDeletion.mock.calls[0]!;
    expect(container.querySelector('textarea')!.disabled).toBe(true);
    await act(async () => root.unmount()); root = createRoot(container);
    await act(async () => root.render(<DeletionPanel session={{ ...session, status: 'deleting' }} api onSessionChanged={() => {}} />));
    expect(container.querySelector('textarea')!.value).toBe('原始清理原因');
    await change(container.querySelector('input')!, '彻底清理');
    state.createDeletion.mockResolvedValue(deletion);
    await act(async () => button('确认彻底清理').click());
    expect(state.createDeletion.mock.calls[1]).toEqual(first);
  });

  it('迟到的创建响应保留编号，但不调用已卸载页面的刷新回调', async () => {
    let accept!: (value: Deletion) => void;
    state.createDeletion.mockReturnValue(new Promise(resolve => { accept = resolve; }));
    const changed = vi.fn();
    await act(async () => root.render(<DeletionPanel session={session} api onSessionChanged={changed} />));
    await submitDeletion();
    await act(async () => root.render(<div />));
    await act(async () => accept(deletion)); expect(changed).not.toHaveBeenCalled();
    await act(async () => root.render(<DeletionPanel session={{ ...session, status: 'deleting' }} api onSessionChanged={changed} />));
    expect(state.getDeletion).toHaveBeenCalledWith(deletion.id);
  });
});
