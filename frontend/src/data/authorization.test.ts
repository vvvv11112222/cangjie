import { describe, expect, it } from 'vitest';
import { filterReportActions, filterRunActions, filterSessionActions } from './authorization';
import type { AccountAction } from './authApi';

const TEACHER: AccountAction[] = ['upload', 'analyze'];
const SUPERVISOR: AccountAction[] = ['review', 'publish'];

describe('账号能力过滤（前端展示约定）', () => {
  it('样例/未登录时不过滤，展示与 M0 一致', () => {
    expect(filterSessionActions(['upload', 'prepare_media', 'analyze'], null)).toEqual([
      'upload',
      'prepare_media',
      'analyze',
    ]);
  });

  it('教师能看到上传与分析，看不到复核类操作', () => {
    expect(filterSessionActions(['upload', 'prepare_media', 'analyze', 'edit_transcript', 'delete'], TEACHER)).toEqual([
      'upload',
      'prepare_media',
      'analyze',
    ]);
    expect(filterSessionActions(['upload', 'analyze'], SUPERVISOR)).toEqual([]);
  });

  it('查看类动作不受账号能力限制（可见性由资源本身决定）', () => {
    expect(filterSessionActions(['view_results', 'view_report'], SUPERVISOR)).toEqual([
      'view_results',
      'view_report',
    ]);
    expect(filterRunActions(['view_results', 'cancel'], SUPERVISOR)).toEqual(['view_results']);
  });

  it('报告的发布与撤回要求 publish，编辑要求 review', () => {
    expect(filterReportActions(['edit', 'publish', 'export'], SUPERVISOR)).toEqual(['edit', 'publish', 'export']);
    expect(filterReportActions(['edit', 'publish'], TEACHER)).toEqual([]);
  });
});
