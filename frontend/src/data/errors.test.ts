import { describe, expect, it } from 'vitest';
import { ApiError, describeError, errorCodeText } from './errors';

describe('错误码到提示文案', () => {
  it('协议里列出的错误码都有中文提示', () => {
    for (const code of [
      'INVALID_ARGUMENT',
      'UNAUTHENTICATED',
      'FORBIDDEN',
      'NOT_FOUND',
      'REVISION_CONFLICT',
      'SERVICE_UNAVAILABLE',
    ] as const) {
      const text = errorCodeText(code);
      expect(text.length).toBeGreaterThan(0);
      expect(text).not.toContain('未收录');
    }
  });

  it('未收录的错误码给出兜底提示而不是空白', () => {
    expect(errorCodeText('SOMETHING_NEW')).toContain('SOMETHING_NEW');
  });

  it('describeError 对服务端错误同时给出提示和错误码', () => {
    const error = new ApiError({ code: 'NOT_FOUND', status: 404, message: 'not implemented' });
    const text = describeError(error);
    expect(text).toContain('不存在，或超出了当前账号的可见范围');
    expect(text).toContain('NOT_FOUND');
    expect(text).toContain('HTTP 404');
  });

  it('describeError 对前端本地错误直接使用中文说明', () => {
    const error = new ApiError({ code: 'NOT_FOUND', status: 0, message: '当前账号没有可见的分析批次。' });
    expect(describeError(error)).toBe('当前账号没有可见的分析批次。');
  });

  it('describeError 对普通异常兜底', () => {
    expect(describeError(new Error('数据不符合协议：SessionPage.items[0].id 缺少字段'))).toContain(
      '数据不符合协议',
    );
    expect(describeError('不是 Error')).toContain('未知错误');
  });
});
