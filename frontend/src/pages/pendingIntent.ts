/**
 * 尚未确定的"分析意图"（评审意见 N02）。
 *
 * 为什么需要：幂等键必须跟着"一次逻辑提交"走，而不是跟着组件实例走。
 * 如果把它放在页面 state 里，用户提交后响应丢失、返回列表再回到同一课堂时，
 * 键就没了，重发会被服务端当成新的分析（可能重复调用模型/计费）。
 * 所以这里按"账号 + 课堂"保存未确定的意图：同一账号重进详情、甚至整页刷新后仍复用原键；
 * 换账号不会串（键含 user_id）；服务端已受理（202）后清除，下次提交才算新分析。
 *
 * 存储位置：sessionStorage（只在本标签页、关掉标签即清），不可用时退化为内存 Map。
 * 只存不透明的业务 ID（幂等键、提交签名），不含凭据。
 */

import { newIdempotencyKey } from '../data/actions';

const STORAGE_KEY = 'cangjie.pending-analysis-intents';

interface PendingIntent {
  key: string;
  signature: string;
}

type PendingMap = Record<string, PendingIntent>;

function isPendingIntent(value: unknown): value is PendingIntent {
  if (typeof value !== 'object' || value === null) {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  return typeof candidate['key'] === 'string' && typeof candidate['signature'] === 'string';
}

/** 读取持久化的意图；解析失败、隐私模式禁用存储等一律退化为空。 */
function loadPersisted(): PendingMap {
  try {
    const raw = globalThis.sessionStorage?.getItem(STORAGE_KEY);
    if (typeof raw !== 'string' || raw === '') {
      return {};
    }
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      return {};
    }
    const result: PendingMap = {};
    for (const [id, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (isPendingIntent(value)) {
        result[id] = value;
      }
    }
    return result;
  } catch {
    return {};
  }
}

function persist(pending: Map<string, PendingIntent>): void {
  try {
    const plain: PendingMap = {};
    for (const [id, value] of pending) {
      plain[id] = value;
    }
    globalThis.sessionStorage?.setItem(STORAGE_KEY, JSON.stringify(plain));
  } catch {
    // 存储不可用（隐私模式、配额等）：本次运行退化为内存，不影响幂等键复用。
  }
}

export interface IntentStore {
  /** 取这次逻辑提交的幂等键：签名相同视为重发，复用原键；否则新建。 */
  keyFor(scope: string, sessionId: string, signature: string): string;
  /** 服务端已受理后清除；下次提交是新分析，使用新键。 */
  resolve(scope: string, sessionId: string): void;
  /** 当前保留的未确定意图数量（测试与诊断用）。 */
  size(): number;
}

export function createIntentStore(generateKey: () => string = newIdempotencyKey): IntentStore {
  // 构造时从 sessionStorage 恢复：整页刷新后仍能复用尚未确定的意图。
  const pending = new Map<string, PendingIntent>(Object.entries(loadPersisted()));
  const idOf = (scope: string, sessionId: string): string => `${scope}:${sessionId}`;
  return {
    keyFor(scope, sessionId, signature) {
      const entry = pending.get(idOf(scope, sessionId));
      if (entry !== undefined && entry.signature === signature) {
        return entry.key;
      }
      const key = generateKey();
      pending.set(idOf(scope, sessionId), { key, signature });
      persist(pending);
      return key;
    },
    resolve(scope, sessionId) {
      pending.delete(idOf(scope, sessionId));
      persist(pending);
    },
    size() {
      return pending.size;
    },
  };
}

/** 页面共用的单例。 */
export const pendingIntent = createIntentStore();
