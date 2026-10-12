// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest';
import { createIntentStore } from './pendingIntent';
import { intentSignature } from './sessionActions';

/** 评审意见 N02：未确定的分析意图要跨组件卸载（甚至整页刷新）保留，并按账号隔离。 */

let counter = 0;
const generateKey = (): string => `key-${++counter}`;

beforeEach(() => {
  counter = 0;
  globalThis.sessionStorage.clear();
});

describe('未确定分析意图的幂等键', () => {
  it('同一账号、同一课堂、同一签名复用同一个键（离开详情再回来也一样）', () => {
    const intents = createIntentStore(generateKey);
    const first = intents.keyFor('user-A', 's1', 's1:full:m1');
    const again = intents.keyFor('user-A', 's1', 's1:full:m1');
    expect(again).toBe(first);
  });

  it('换模式或换媒体（签名变化）时换新键', () => {
    const intents = createIntentStore(generateKey);
    const full = intents.keyFor('user-A', 's1', 's1:full:m1');
    const prepare = intents.keyFor('user-A', 's1', 's1:media_prepare:m1');
    const otherMedia = intents.keyFor('user-A', 's1', 's1:full:m2');
    expect(new Set([full, prepare, otherMedia]).size).toBe(3);
  });

  it('服务端已受理后清除意图，下次提交是新分析', () => {
    const intents = createIntentStore(generateKey);
    const first = intents.keyFor('user-A', 's1', 's1:full:m1');
    intents.resolve('user-A', 's1', first);
    const second = intents.keyFor('user-A', 's1', 's1:full:m1');
    expect(second).not.toBe(first);
    expect(intents.size()).toBe(1);
  });

  it('不同账号或不同课堂互不影响', () => {
    const intents = createIntentStore(generateKey);
    const a = intents.keyFor('user-A', 's1', 's1:full:m1');
    const b = intents.keyFor('user-B', 's1', 's1:full:m1');
    const otherSession = intents.keyFor('user-A', 's2', 's2:full:m1');
    expect(new Set([a, b, otherSession]).size).toBe(3);
    expect(intents.size()).toBe(3);
  });

  it('整页刷新后仍能找回未确定的意图（sessionStorage 持久化）', () => {
    const before = createIntentStore(generateKey);
    const signature = intentSignature('s1', {
      media_asset_id: 'm1', mode: 'full', input_transcript_revision_id: null, config_profile: 'p0-v1',
    });
    const key = before.keyFor('user-A', 's1', signature);

    // 模拟整页刷新：新实例从 sessionStorage 恢复。
    const afterReload = createIntentStore(generateKey);

    expect(afterReload.keyFor('user-A', 's1', signature)).toBe(key);
  });

  it('已受理的意图不会在刷新后被复用', () => {
    const before = createIntentStore(generateKey);
    const key = before.keyFor('user-A', 's1', 's1:full:m1');
    before.resolve('user-A', 's1', key);

    const afterReload = createIntentStore(generateKey);

    expect(afterReload.keyFor('user-A', 's1', 's1:full:m1')).not.toBe(key);
  });

  it('旧媒体的迟到确认只清除自己的记录，新媒体重发仍保留原键（R17-01）', () => {
    const before = createIntentStore(generateKey);
    const oldKey = before.keyFor('user-A', 's1', 'request-M1');
    const newKey = before.keyFor('user-A', 's1', 'request-M2');
    expect(before.size()).toBe(2);
    before.resolve('user-A', 's1', oldKey);
    expect(before.size()).toBe(1);
    const afterReload = createIntentStore(generateKey);
    expect(afterReload.keyFor('user-A', 's1', 'request-M2')).toBe(newKey);
  });

  it('切换模式或修订不会丢掉旧的未确定提交，返回原请求仍复用键', () => {
    const intents = createIntentStore(generateKey);
    const full = intents.keyFor('user-A', 's1', 'full-M1');
    const prepare = intents.keyFor('user-A', 's1', 'prepare-M1');
    const reportR1 = intents.keyFor('user-A', 's1', 'report-M1-R1');
    const reportR2 = intents.keyFor('user-A', 's1', 'report-M1-R2');
    const afterReload = createIntentStore(generateKey);
    expect(afterReload.keyFor('user-A', 's1', 'full-M1')).toBe(full);
    expect(afterReload.keyFor('user-A', 's1', 'prepare-M1')).toBe(prepare);
    expect(afterReload.keyFor('user-A', 's1', 'report-M1-R1')).toBe(reportR1);
    expect(afterReload.keyFor('user-A', 's1', 'report-M1-R2')).toBe(reportR2);
    expect(afterReload.size()).toBe(4);
  });

  it('重复旧确认和其他账号/课堂的确认不能清除新意图', () => {
    const intents = createIntentStore(generateKey);
    const oldKey = intents.keyFor('user-A', 's1', 'request');
    intents.resolve('user-A', 's1', oldKey);
    const newKey = intents.keyFor('user-A', 's1', 'request');
    intents.resolve('user-A', 's1', oldKey);
    intents.resolve('user-B', 's1', newKey);
    intents.resolve('user-A', 's2', newKey);
    expect(intents.keyFor('user-A', 's1', 'request')).toBe(newKey);
    expect(intents.size()).toBe(1);
    intents.resolve('user-A', 's1', newKey);
    expect(createIntentStore(generateKey).size()).toBe(0);
  });

  it.each(['full', 'media_prepare'] as const)('升级后保留旧版 %s 未确定请求的原键', (mode) => {
    globalThis.sessionStorage.setItem('cangjie.pending-analysis-intents', JSON.stringify({
      'user-A:s1': { key: 'old-key', signature: `s1:${mode}:m1` },
    }));
    const intents = createIntentStore(generateKey);
    const signature = intentSignature('s1', {
      media_asset_id: 'm1', mode, input_transcript_revision_id: null, config_profile: 'p0-v1',
    });
    expect(intents.keyFor('user-A', 's1', signature)).toBe('old-key');
    intents.resolve('user-A', 's1', 'old-key');
    expect(createIntentStore(generateKey).size()).toBe(0);
  });

  it('旧 report_only 缺少修订信息时保留记录但不猜测匹配', () => {
    globalThis.sessionStorage.setItem('cangjie.pending-analysis-intents', JSON.stringify({
      'user-A:s1': { key: 'old-report-key', signature: 's1:report_only:m1' },
    }));
    const intents = createIntentStore(generateKey);
    const key = intents.keyFor('user-A', 's1', intentSignature('s1', {
      media_asset_id: 'm1', mode: 'report_only', input_transcript_revision_id: 'r2', config_profile: 'p0-v1',
    }));
    expect(key).not.toBe('old-report-key');
    expect(intents.size()).toBe(2);
  });
});
