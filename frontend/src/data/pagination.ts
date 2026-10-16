import { ApiError } from './errors';

type Page<T> = { items: T[] } & ({ nextCursor: string | null } | { next_cursor: string | null });

/** 管理表及其导出需要完整列表；不把第一页冒充全部数据。 */
export async function collectPages<T>(load: (cursor?: string) => Promise<Page<T>>): Promise<T[]> {
  const items: T[] = [];
  const seen = new Set<string>();
  let cursor: string | undefined;
  while (true) {
    const page = await load(cursor);
    items.push(...page.items);
    const next = 'nextCursor' in page ? page.nextCursor : page.next_cursor;
    if (next === null) return items;
    if (seen.has(next)) throw new ApiError({ code: 'MALFORMED_RESPONSE', status: 0, message: '分页游标重复，无法确认列表完整。' });
    seen.add(next);
    cursor = next;
  }
}

/** 按浏览器本地日历计算周一至下周一，发送有时区的 ISO 时间。 */
export function calendarWeekRange(now = new Date()): { from: string; to: string } {
  const from = new Date(now);
  from.setHours(0, 0, 0, 0);
  from.setDate(from.getDate() - (from.getDay() + 6) % 7);
  const to = new Date(from);
  to.setDate(to.getDate() + 7);
  return { from: from.toISOString(), to: to.toISOString() };
}
