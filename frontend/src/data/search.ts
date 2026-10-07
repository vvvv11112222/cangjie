import type { Session } from '../types';
import { ApiError } from './errors';
import type { DataSource } from './sources';

export const MAX_SESSION_PAGES = 10;
export const MAX_DRAFT_RUN_LOOKUPS = 20;

/** 每个页面、每个认证会话持有独立检查点；失败时仍指向未完成的记录。 */
export interface SearchCheckpoint {
  items: Session[];
  index: number;
  cursor: string | undefined;
  seen: string[];
  exhausted: boolean;
}

export function newSearch(): SearchCheckpoint {
  return { items: [], index: 0, cursor: undefined, seen: [], exhausted: false };
}

/** 前端本轮暂停，不是服务端失败或最终 NOT_FOUND。 */
export class SearchPaused extends Error {
  constructor() {
    super('本轮尚未找到结果，点击“继续查找”查看后续课堂。');
  }
}

export function checkNextCursor(next: string | null, seen: readonly string[]): void {
  if (next !== null && seen.includes(next)) {
    throw new ApiError({ code: 'MALFORMED_RESPONSE', status: 200, message: '服务端返回了重复的分页游标，请重新加载或联系管理员。' });
  }
}

/** 只在成功检查记录后推进位置；用户续查不会重扫前缀，失败重试不跳过记录。 */
export async function searchSessions<T>(
  source: DataSource,
  checkpoint: SearchCheckpoint,
  inspect: (session: Session) => Promise<T | null>,
  preferPublished = false,
): Promise<T | null> {
  let pages = 0;
  while (true) {
    while (checkpoint.index < checkpoint.items.length) {
      const session = checkpoint.items[checkpoint.index]!;
      const result = await inspect(session);
      if (result !== null) return result;
      checkpoint.index += 1;
    }
    if (checkpoint.exhausted) return null;
    if (pages >= MAX_SESSION_PAGES) throw new SearchPaused();
    const page = await source.getSessions({ cursor: checkpoint.cursor });
    const seen = [...checkpoint.seen, checkpoint.cursor ?? ''];
    checkNextCursor(page.next_cursor, seen);
    checkpoint.items = preferPublished
      ? [...page.items].sort((a, b) => Number(b.current_report_id !== null) - Number(a.current_report_id !== null))
      : page.items;
    checkpoint.index = 0;
    checkpoint.seen = seen;
    checkpoint.cursor = page.next_cursor ?? undefined;
    checkpoint.exhausted = page.next_cursor === null;
    pages += 1;
  }
}
