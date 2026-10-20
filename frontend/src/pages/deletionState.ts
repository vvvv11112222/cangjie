/** 同一浏览器标签内按账号和课堂保留清理进度；不保存登录凭据。 */
const STORAGE_KEY = 'cangjie.deletion-state';
export interface DeletionState {
  requestId: string | null;
  intent: { key: string; reason: string } | null;
}
let fallback: Record<string, DeletionState> = {};

function entries(): Record<string, DeletionState> {
  try {
    const raw: unknown = JSON.parse(sessionStorage.getItem(STORAGE_KEY) ?? '{}');
    return typeof raw === 'object' && raw !== null && !Array.isArray(raw)
      ? raw as Record<string, DeletionState> : {};
  } catch { return fallback; }
}

const scopeKey = (account: string, sessionId: string): string => JSON.stringify([account, sessionId]);
export function readDeletionState(account: string, sessionId: string): DeletionState {
  const value = entries()[scopeKey(account, sessionId)];
  return {
    requestId: typeof value?.requestId === 'string' && value.requestId !== '' ? value.requestId : null,
    intent: typeof value?.intent?.key === 'string' && value.intent.key !== '' && typeof value.intent.reason === 'string'
      && value.intent.reason.trim() !== '' ? { ...value.intent } : null,
  };
}

export function saveDeletionState(account: string, sessionId: string, value: DeletionState): void {
  fallback = { ...entries(), [scopeKey(account, sessionId)]: value };
  try { sessionStorage.setItem(STORAGE_KEY, JSON.stringify(fallback)); } catch { /* 禁用存储时保留内存恢复能力。 */ }
}
