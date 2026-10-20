import { describe, expect, it } from 'vitest';
import example from '../../../contracts/examples/visual-video-result.json';
import { observationAt, observationColor, parseObservation, parseVisualSummary, verdictText } from './visualApi';
import { formatDateTime } from '../format';

describe('正式视觉结果', () => {
  it('保留未见和看不清的差别', () => {
    const row = parseObservation(example.visual.persons[0]!.observations[0]);
    expect(row.behaviors.hand_raise).toBe(true);
    expect(row.behaviors.head_down).toBeNull();
    expect(row.behaviors.leave_seat).toBe(false);
    expect(verdictText(null)).toBe('看不清'); expect(verdictText(false)).toBe('未见');
  });
  it('拒绝缺失的行为字段和无效坐标', () => {
    const original = example.visual.persons[0]!.observations[0];
    expect(() => parseObservation({ ...original, behaviors: { hand_raise: false } })).toThrow();
    expect(() => parseObservation({ ...original, bbox: [1, 2, 3] })).toThrow();
  });
  it('只显示附近实际观察，不向未采样时刻延长行为', () => {
    const row = parseObservation(example.visual.persons[0]!.observations[0]);
    expect(observationAt([row], 100, 500)).toBe(row);
    expect(observationAt([row], 700, 500)).toBeNull();
    expect(observationAt([], 0, 500)).toBeNull();
  });
  it('颜色取决于识别状态，不取决于是否选中某人', () => {
    const row = parseObservation(example.visual.persons[0]!.observations[0]);
    expect(observationColor(row)).toBe('var(--danger)');
    expect(observationColor({ ...row, behaviors: { ...row.behaviors, hand_raise: false } })).toBe('var(--muted)');
  });
  it('读取概况中的真实覆盖和数量', () => {
    const summary = parseVisualSummary(example);
    expect(summary.visual.counts).toEqual(example.visual.counts);
    expect(summary.coverage).toEqual(example.coverage);
    expect(summary.media_asset_id).toBe(example.media_asset_id);
  });
  it('兼容 Go/PostgreSQL 返回的微秒时间', () => {
    expect(formatDateTime('2026-10-19T21:54:28.804341+08:00')).toBe('2026-10-19 21:54:28 (UTC+08:00)');
  });
});
