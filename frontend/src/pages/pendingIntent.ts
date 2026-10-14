/**
 * 尚未确定的"分析意图"（评审意见 N02）。
 *
 * 为什么需要：幂等键必须跟着"一次逻辑提交"走，而不是跟着组件实例走。
 * 如果把它放在页面 state 里，用户提交后响应丢失、返回列表再回到同一课堂时，
 * 键就没了，重发会被服务端当成新的分析（可能重复调用模型/计费）。
 * 所以这里按"账号 + 课堂 + 完整请求签名"保存各份未确定意图，重进详情或刷新仍复用原键；
 * 换账号不会串（键含 user_id）；服务端已受理（202）后清除，下次提交才算新分析。
 *
 * 存储位置：sessionStorage（只在本标签页、关掉标签即清），不可用时退化为内存 Map。
 * 只存不透明的业务 ID（幂等键、提交签名），不含凭据。
 */

import { newIdempotencyKey } from '../data/actions';
import { intentSignature } from './sessionActions';

const STORAGE_KEY = 'cangjie.pending-analysis-intents';

interface PendingIntent {
  key: string;
  signature: string;
}

type PendingMap = Record<string, PendingIntent[]>;

function isPendingIntent(value: unknown): value is PendingIntent {
  if (typeof value !== 'object' || value === null) {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  return typeof candidate['key'] === 'string' && typeof candidate['signature'] === 'string';
}

/** 旧版 full/media_prepare 的请求固定为 null 修订、p0-v1，可安全补齐签名。 */
function restoreIntent(intent: PendingIntent): PendingIntent {
  const legacy = /^([^:]+):(full|media_prepare):([^:]+)$/.exec(intent.signature);
  if (legacy === null) {
    // 旧 report_only 签名没有修订信息，保留记录但不能猜测并匹配新请求。
    return intent;
  }
  return {
    key: intent.key,
    signature: intentSignature(legacy[1]!, {
      media_asset_id: legacy[3]!,
      mode: legacy[2] === 'full' ? 'full' : 'media_prepare',
      input_transcript_revision_id: null,
      config_profile: 'p0-v1',
    }),
  };
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
      // 兼容旧版每个账号/课堂仅保存一条记录的存储格式。
      const entries: unknown[] = Array.isArray(value) ? value : [value];
      const restored = entries.filter(isPendingIntent).map(restoreIntent);
      if (restored.length > 0) {
        result[id] = restored;
      }
    }
    return result;
  } catch {
    return {};
  }
}

function persist(pending: Map<string, PendingIntent[]>): void {
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
  /** 只清除本次已受理的键；迟到确认不能删除其他未确定意图。 */
  resolve(scope: string, sessionId: string, acceptedKey: string): void;
  /** 当前保留的未确定意图数量（测试与诊断用）。 */
  size(): number;
}

export function createIntentStore(generateKey: () => string = newIdempotencyKey): IntentStore {
  // 构造时从 sessionStorage 恢复：整页刷新后仍能复用尚未确定的意图。
  const pending = new Map<string, PendingIntent[]>(Object.entries(loadPersisted()));
  const idOf = (scope: string, sessionId: string): string => `${scope}:${sessionId}`;
  return {
    keyFor(scope, sessionId, signature) {
      const id = idOf(scope, sessionId);
      const entries = pending.get(id) ?? [];
      const entry = entries.find((item) => item.signature === signature);
      if (entry !== undefined) {
        return entry.key;
      }
      const key = generateKey();
      pending.set(id, [...entries, { key, signature }]);
      persist(pending);
      return key;
    },
    resolve(scope, sessionId, acceptedKey) {
      const id = idOf(scope, sessionId);
      const entries = pending.get(id);
      if (entries === undefined || !entries.some((item) => item.key === acceptedKey)) {
        return;
      }
      const remaining = entries.filter((item) => item.key !== acceptedKey);
      if (remaining.length === 0) {
        pending.delete(id);
      } else {
        pending.set(id, remaining);
      }
      persist(pending);
    },
    size() {
      return [...pending.values()].reduce((count, entries) => count + entries.length, 0);
    },
  };
}

/** 页面共用的单例。 */
export const pendingIntent = createIntentStore();
