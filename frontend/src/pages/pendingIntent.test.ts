// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest';
import { createIntentStore } from './pendingIntent';

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
    intents.resolve('user-A', 's1');
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
    const key = before.keyFor('user-A', 's1', 's1:full:m1');

    // 模拟整页刷新：新实例从 sessionStorage 恢复。
    const afterReload = createIntentStore(generateKey);

    expect(afterReload.keyFor('user-A', 's1', 's1:full:m1')).toBe(key);
  });

  it('已受理的意图不会在刷新后被复用', () => {
    const before = createIntentStore(generateKey);
    const key = before.keyFor('user-A', 's1', 's1:full:m1');
    before.resolve('user-A', 's1');

    const afterReload = createIntentStore(generateKey);

    expect(afterReload.keyFor('user-A', 's1', 's1:full:m1')).not.toBe(key);
  });
});
