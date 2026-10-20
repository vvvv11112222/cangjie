import { expect, it, vi } from 'vitest';
const h = vi.hoisted(() => ({ request: vi.fn().mockResolvedValue({ data: { items: [], next_cursor: null } }) }));
vi.mock('./http', () => ({ requestJson: h.request }));
import { listSchedules, listCourses, createTerm, patchTerm, createCourse, patchCourse, createClassroom, patchClassroom, createOffering, patchOffering, patchSchedule, importSchedules } from './m1Api';

it('sends the same week interval with the opaque schedule cursor', async () => {
  const range = { from: '2026-10-12T00:00:00+08:00', to: '2026-10-19T00:00:00+08:00' };
  await listSchedules('page:2', range);
  expect(h.request).toHaveBeenLastCalledWith('/schedules', { query: { limit: 100, cursor: 'page:2', ...range } });
});
it('passes academic continuation cursors through the API', async () => {
  await listCourses('course:101');
  expect(h.request).toHaveBeenLastCalledWith('/courses', { query: { limit: 100, cursor: 'course:101' } });
});

it('sends typed academic writes with CSRF and no response-only id', async () => {
  const cases = [
    { path: 'terms', input: { code: 'T1', name: '学期', start_date: '2026-09-01', end_date: '2027-01-01' }, create: createTerm, patch: patchTerm },
  ];
  for (const item of cases) {
    h.request.mockResolvedValue({ data: { id: 'id', ...item.input } });
    await item.create(item.input); expect(h.request).toHaveBeenLastCalledWith(`/${item.path}`, { method: 'POST', body: item.input, csrf: true });
    await item.patch('id', item.input); expect(h.request).toHaveBeenLastCalledWith(`/${item.path}/id`, { method: 'PATCH', body: item.input, csrf: true });
  }
  const course = { org_unit_id: 'college', code: 'C1', name: '课程', description: '' };
  h.request.mockResolvedValue({ data: { id: 'id', ...course } });
  await createCourse(course); await patchCourse('id', course);
  expect(h.request).toHaveBeenLastCalledWith('/courses/id', { method: 'PATCH', body: course, csrf: true });
  const room = { code: 'R1', name: '教室', capacity: null };
  h.request.mockResolvedValue({ data: { id: 'id', ...room } });
  await createClassroom(room); await patchClassroom('id', room);
  expect(h.request).toHaveBeenLastCalledWith('/classrooms/id', { method: 'PATCH', body: room, csrf: true });
  const offering = { org_unit_id: 'college', term_id: 'term', course_id: 'course', teacher_id: 'teacher', class_group_id: 'group', code: 'O1', status: 'active' };
  h.request.mockResolvedValue({ data: { id: 'id', ...offering } });
  await createOffering(offering); await patchOffering('id', offering);
  expect(h.request).toHaveBeenLastCalledWith('/offerings/id', { method: 'PATCH', body: offering, csrf: true });
});

it('imports rows without a status field and cancels through PATCH', async () => {
  const row = { offering_id: 'offering', classroom_id: 'room', starts_at: '2026-10-20T00:00:00Z', ends_at: '2026-10-20T01:00:00Z' };
  const result = { id: 'schedule', ...row, teacher_id: 'teacher', class_group_id: 'group', status: 'active' };
  h.request.mockResolvedValueOnce({ data: { items: [result] } });
  expect(await importSchedules([row])).toEqual([result]);
  expect(h.request).toHaveBeenLastCalledWith('/schedules/import', { method: 'POST', body: { rows: [row] }, csrf: true });
  h.request.mockResolvedValueOnce({ data: { ...result, status: 'cancelled' } });
  await patchSchedule('schedule', { ...row, status: 'cancelled' });
  expect(h.request).toHaveBeenLastCalledWith('/schedules/schedule', { method: 'PATCH', body: { ...row, status: 'cancelled' }, csrf: true });
});
