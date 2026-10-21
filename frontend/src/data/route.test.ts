import { describe, expect, it } from 'vitest';
import { parseSessionRoute, sessionSearchFromHash, sessionSearchHash } from './route';

const ID = 'abcdef00-0000-4000-8000-000000000001';

describe('详情路由解析（评审意见 F09/F10）', () => {
  it('普通页面返回 list', () => {
    expect(parseSessionRoute('#/sessions')).toEqual({ kind: 'list' });
    expect(parseSessionRoute('#/transcript')).toEqual({ kind: 'list' });
    expect(parseSessionRoute('')).toEqual({ kind: 'list' });
  });

  it('合法 UUID 进入详情，并把大写规范成小写', () => {
    expect(parseSessionRoute(`#/sessions/${ID}`)).toEqual({ kind: 'detail', sessionId: ID });
    expect(parseSessionRoute(`#/sessions/${ID.toUpperCase()}`)).toEqual({ kind: 'detail', sessionId: ID });
  });

  it('畸形百分号编码变成受控的 invalid，而不是抛 URIError', () => {
    const route = parseSessionRoute('#/sessions/%E0%A4%A');
    expect(route.kind).toBe('invalid');
    if (route.kind === 'invalid') {
      expect(route.message).toContain('URL 编码');
    }
  });

  it('不是 UUID 的 id 也返回 invalid', () => {
    const route = parseSessionRoute('#/sessions/not-a-uuid');
    expect(route.kind).toBe('invalid');
    if (route.kind === 'invalid') {
      expect(route.message).toContain('UUID');
    }
  });
});

describe('课堂档案搜索地址', () => {
  it('生成可分享的搜索地址并还原中文和特殊字符', () => {
    const hash = sessionSearchHash('  数据结构 & 算法  ');
    expect(hash).toBe('#/sessions?query=%E6%95%B0%E6%8D%AE%E7%BB%93%E6%9E%84+%26+%E7%AE%97%E6%B3%95');
    expect(sessionSearchFromHash(hash)).toBe('数据结构 & 算法');
  });

  it('空搜索回到课堂档案，其它页面不误读查询词', () => {
    expect(sessionSearchHash('   ')).toBe('#/sessions');
    expect(sessionSearchFromHash('#/reports?query=test')).toBe('');
    expect(sessionSearchFromHash('#/sessions')).toBe('');
  });
});
