import { expect, it, vi } from 'vitest';
const h = vi.hoisted(() => ({ request: vi.fn().mockResolvedValue({ data: { items: [], next_cursor: null } }) }));
vi.mock('./http', () => ({ requestJson: h.request }));
import { listSchedules, listCourses } from './m1Api';

it('sends the same week interval with the opaque schedule cursor', async () => {
  const range = { from: '2026-10-12T00:00:00+08:00', to: '2026-10-19T00:00:00+08:00' };
  await listSchedules('page:2', range);
  expect(h.request).toHaveBeenLastCalledWith('/schedules', { query: { limit: 100, cursor: 'page:2', ...range } });
});
it('passes academic continuation cursors through the API', async () => {
  await listCourses('course:101');
  expect(h.request).toHaveBeenLastCalledWith('/courses', { query: { limit: 100, cursor: 'course:101' } });
});
