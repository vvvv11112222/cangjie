import { expect, it, vi } from 'vitest';
import { calendarWeekRange, collectPages } from './pagination';

it('collects all snake_case pages using the opaque cursor', async () => {
  const load = vi.fn().mockResolvedValueOnce({ items: Array.from({ length: 100 }, (_, id) => id), next_cursor: 'opaque:2' }).mockResolvedValueOnce({ items: [100], next_cursor: null });
  expect(await collectPages(load)).toHaveLength(101);
  expect(load.mock.calls).toEqual([[undefined], ['opaque:2']]);
});
it('collects camelCase API pages and rejects a repeated cursor', async () => {
  const load = vi.fn().mockResolvedValue({ items: [], nextCursor: 'repeat' });
  await expect(collectPages(load)).rejects.toMatchObject({ code: 'MALFORMED_RESPONSE' });
  expect(load).toHaveBeenCalledTimes(2);
});
it.each([new Date(2026, 9, 12, 9), new Date(2026, 9, 18, 23, 59)])('uses Monday inclusive and next Monday exclusive for %s', now => {
  const range = calendarWeekRange(now);
  expect(new Date(range.from)).toEqual(new Date(2026, 9, 12));
  expect(new Date(range.to)).toEqual(new Date(2026, 9, 19));
});
