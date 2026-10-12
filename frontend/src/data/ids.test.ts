import { describe, expect, it } from 'vitest';
import { isUuid, normalizeId, sameId } from './ids';

const LOWER = 'abcdef00-0000-4000-8000-000000000001';
const UPPER = 'ABCDEF00-0000-4000-8000-000000000001';

describe('业务 ID 校验与规范化', () => {
  it('识别合法 UUID（大小写都算）', () => {
    expect(isUuid(LOWER)).toBe(true);
    expect(isUuid(UPPER)).toBe(true);
    expect(isUuid('not-a-uuid')).toBe(false);
    expect(isUuid('')).toBe(false);
  });

  it('UUID 规范化为小写，非 UUID 原样返回', () => {
    expect(normalizeId(UPPER)).toBe(LOWER);
    expect(normalizeId('非 UUID 的文本')).toBe('非 UUID 的文本');
  });

  it('大小写不同的同一 UUID 视为同一个资源（评审意见 F10）', () => {
    expect(sameId(UPPER, LOWER)).toBe(true);
    expect(sameId(LOWER, LOWER)).toBe(true);
    expect(sameId(LOWER, 'abcdef00-0000-4000-8000-000000000002')).toBe(false);
  });
});
