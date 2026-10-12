import { describe, expect, it } from 'vitest';
import {
  actionStateFor,
  emptyActionState,
  intentSignature,
  processingMediaId,
  writeActionState,
} from './sessionActions';

const SCOPE_A = 'user:A:session-1';
const SCOPE_B = 'user:B:session-1';

describe('动作状态按认证会话隔离（评审意见 F01）', () => {
  it('会话切换后渲染的是空状态，不显示上一个账号的回执', () => {
    const a = { ...emptyActionState(SCOPE_A), note: 'A 已提交', busy: true };
    const shown = actionStateFor(a, SCOPE_B);

    expect(shown.scope).toBe(SCOPE_B);
    expect(shown.note).toBeNull();
    expect(shown.busy).toBe(false);
  });

  it('同一会话内正常保留状态', () => {
    const a = { ...emptyActionState(SCOPE_A), note: 'A 已提交' };
    expect(actionStateFor(a, SCOPE_A)).toBe(a);
  });

  it('旧会话的迟到写入被丢弃，不会污染新会话', () => {
    const b = { ...emptyActionState(SCOPE_B), note: 'B 的页面' };
    // A 的响应在切换到 B 之后才回来。
    const afterLateWrite = writeActionState(b, SCOPE_A, (base) => ({ ...base, note: 'A 迟到的结果' }));

    expect(afterLateWrite).toBe(b);
    expect(afterLateWrite.note).toBe('B 的页面');
  });

  it('同一会话的写入正常生效', () => {
    const a = emptyActionState(SCOPE_A);
    const next = writeActionState(a, SCOPE_A, (base) => ({ ...base, note: '已提交' }));
    expect(next.note).toBe('已提交');
  });
});

describe('幂等键与处理媒体（评审意见 F02/F03）', () => {
  it('同课堂+模式+媒体的签名相同，换任一项就不同', () => {
    const base = intentSignature('s1', 'full', 'm1');
    expect(intentSignature('s1', 'full', 'm1')).toBe(base);
    expect(intentSignature('s1', 'media_prepare', 'm1')).not.toBe(base);
    expect(intentSignature('s1', 'full', 'm2')).not.toBe(base);
    expect(intentSignature('s2', 'full', 'm1')).not.toBe(base);
  });

  it('处理媒体按选择意图决定：auto 优先刚上传，primary 必须用主媒体（评审意见 N06）', () => {
    const base = emptyActionState(SCOPE_A);
    expect(processingMediaId(base, 'primary')).toBe('primary');
    expect(processingMediaId({ ...base, uploadedMediaId: 'uploaded' }, 'primary')).toBe('uploaded');
    // 显式选了"使用课堂当前主媒体"：不能退回刚上传的媒体。
    expect(
      processingMediaId({ ...base, uploadedMediaId: 'uploaded', mediaChoice: { kind: 'primary' } }, 'primary'),
    ).toBe('primary');
    // 显式选了某个媒体：按它来。
    expect(
      processingMediaId({ ...base, mediaChoice: { kind: 'asset', mediaId: 'picked' } }, 'primary'),
    ).toBe('picked');
    // 显式选主媒体但当前没有主媒体：不能用别的媒体顶替。
    expect(processingMediaId({ ...base, uploadedMediaId: 'uploaded', mediaChoice: { kind: 'primary' } }, null)).toBeNull();
    expect(processingMediaId(base, null)).toBeNull();
  });
});
