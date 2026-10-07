import { describe, expect, it } from 'vitest';
import {
  applyIfSameSession,
  errorState,
  forSession,
  loadingState,
  readyState,
  signedOutState,
} from './loadState';

/**
 * 评审意见 P1：账号 A 的数据不能留到账号 B 的页面上，A 的迟到响应也不能覆盖 B。
 * 这些规则集中在 loadState.ts，所以可以不依赖 DOM 直接测。
 */
describe('按会话隔离的加载状态', () => {
  it('切换会话时立刻按新会话的初始状态呈现，不残留上一个账号的数据', () => {
    const stateA = readyState('user:A', { title: 'A 的课堂' });
    const shown = forSession(stateA, 'user:B', () => loadingState('user:B', { title: '' }));

    expect(shown.status).toBe('loading');
    expect(shown.data.title).toBe('');
  });

  it('同一会话内正常返回就绪状态', () => {
    const state = readyState('user:A', { title: 'A 的课堂' });
    expect(forSession(state, 'user:A', () => loadingState('user:A', { title: '' }))).toBe(state);
  });

  it('旧会话的迟到响应被丢弃，不覆盖新会话已经加载好的数据', () => {
    const stateB = readyState('user:B', { title: 'B 的课堂' });
    // A 的请求在切换之后才返回。
    const afterLateResponse = applyIfSameSession(stateB, 'user:A', readyState('user:A', { title: 'A 的课堂' }));

    expect(afterLateResponse).toBe(stateB);
    expect(afterLateResponse.data.title).toBe('B 的课堂');
  });

  it('同一会话的响应正常写入（含失败态）', () => {
    const loading = loadingState('user:B', { title: '' });
    expect(applyIfSameSession(loading, 'user:B', readyState('user:B', { title: 'B' })).status).toBe('ready');
    expect(applyIfSameSession(loading, 'user:B', errorState('user:B', '读取失败', { title: '' })).error).toBe(
      '读取失败',
    );
  });

  it('未登录时是 signed_out，不是"没有数据"', () => {
    const state = signedOutState('anonymous', { title: '' });
    expect(state.status).toBe('signed_out');
    expect(state.error).toBeNull();
  });
});
