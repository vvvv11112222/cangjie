// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Deletion, ModelCall, Operations, Session } from '../types';

const state = vi.hoisted(() => ({
  getOperations: vi.fn(),
  listModelCalls: vi.fn(),
  reconcileModelCall: vi.fn(),
  createDeletion: vi.fn(),
  getDeletion: vi.fn(),
}));

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
});
