import { describe, expect, it } from 'vitest';
import { csvTextCell } from './csv';

describe('spreadsheet CSV text', () => {
  it.each(['=1+1', '+SUM(A1)', '-1+1', '@SUM(A1)', '  =1+1', '\t=1+1', '\rtext', '\ntext'])('keeps formula-like input as text: %j', value => {
    expect(csvTextCell(value)).toBe(`"'${value}"`);
  });
  it('preserves Chinese, quoted text, commas, ordinary spaces and dates', () => {
    expect(csvTextCell('操作系统, "第一讲"')).toBe('"操作系统, ""第一讲"""');
    expect(csvTextCell('2026-10-22T08:00:00Z')).toBe('"2026-10-22T08:00:00Z"');
    expect(csvTextCell(' 普通课程')).toBe('" 普通课程"');
  });
});
