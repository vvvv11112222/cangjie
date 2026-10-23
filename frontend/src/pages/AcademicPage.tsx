import type { FormEvent,ReactElement } from 'react';
import { useEffect,useMemo,useState } from 'react';
import { useAuth } from '../auth';
import { DeleteUnusedButton } from '../components/ManagementActions';
import { IconPlus,IconUpload } from '../components/icons';
import { Card,EmptyState,LoadStateNote } from '../components/ui';
import type { ClassGroup,OrgUnit } from '../data/academicApi';
import { listClassGroups,listOrgUnits } from '../data/academicApi';
import { csvTextCell } from '../data/csv';
import { ApiError,describeError } from '../data/errors';
import type { ManagedUser } from '../data/identityApi';
import { listUsers } from '../data/identityApi';
import type { Classroom,Course,Offering,Schedule,Term } from '../data/m1Api';
import {
createClassroom,
createCourse,
createOffering,
createSchedule,
createTerm,
importSchedules,
listClassrooms,
listCourses,
listOfferings,
listSchedules,
listTerms,
patchClassroom,
patchCourse,
patchOffering,
patchSchedule,
patchTerm,
} from '../data/m1Api';
import { calendarWeekRange,collectPages } from '../data/pagination';
import { routeQuery } from '../data/route';
import { isApiMode } from '../data/sources';
import { formatDateTime } from '../format';

type Tab = 'schedule' | 'offerings' | 'courses' | 'terms' | 'classrooms';
type Editor =
  | { kind: 'schedule'; item: Schedule | null }
  | { kind: 'offering'; item: Offering | null }
  | { kind: 'course'; item: Course | null }
  | { kind: 'term'; item: Term | null }
  | { kind: 'classroom'; item: Classroom | null };

interface AcademicData {
  terms: Term[];
  courses: Course[];
  classrooms: Classroom[];
  offerings: Offering[];
  schedules: Schedule[];
  classGroups: ClassGroup[];
  orgUnits: OrgUnit[];
  users: ManagedUser[];
}

const EMPTY: AcademicData = { terms: [], courses: [], classrooms: [], offerings: [], schedules: [], classGroups: [], orgUnits: [], users: [] };
const TAB_LABELS: Record<Tab, string> = { schedule: '本周课表', offerings: '开课实例', courses: '课程', terms: '学期', classrooms: '教室' };

function requestedTab(): Tab {
  const value = routeQuery().get('tab');
  return value !== null && Object.hasOwn(TAB_LABELS, value) ? value as Tab : 'schedule';
}

function academicError(cause: unknown): string {
  const text = describeError(cause);
  if (!(cause instanceof ApiError) || cause.code !== 'SCHEDULE_CONFLICT') return text;
  const details = cause.details;
  const resource = ({ teacher: '教师', class_group: '班级', classroom: '教室' } as Record<string, string>)[String(details.resource_type)] ?? '资源';
  const rows = Array.isArray(details.row_numbers) ? details.row_numbers.filter(value => Number.isInteger(value)).join('、') : '';
  return `${text} 冲突资源：${resource}${rows ? `；导入行：${rows}` : ''}。`;
}

function downloadScheduleCsv(rows: Schedule[], data: AcademicData): void {
  const lines = [['课程', '开课编号', '班级', '教师', '教室', '开始时间', '结束时间', '状态'], ...rows.map(row => {
    const offering = data.offerings.find(item => item.id === row.offering_id);
    return [data.courses.find(item => item.id === offering?.course_id)?.name ?? '', offering?.code ?? '', data.classGroups.find(item => item.id === row.class_group_id)?.name ?? '', data.users.find(item => item.id === row.teacher_id)?.displayName ?? row.teacher_id, data.classrooms.find(item => item.id === row.classroom_id)?.name ?? '', row.starts_at, row.ends_at, row.status];
  })];
  const blob = new Blob([`\uFEFF${lines.map(line => line.map(csvTextCell).join(',')).join('\r\n')}`], { type: 'text/csv;charset=utf-8' });
  const href = URL.createObjectURL(blob); const anchor = document.createElement('a'); anchor.href = href; anchor.download = 'schedule.csv'; anchor.click(); URL.revokeObjectURL(href);
}

function localDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

function toIso(value: FormDataEntryValue | null): string {
  const date = new Date(String(value ?? ''));
  if (Number.isNaN(date.getTime())) throw new Error('开始和结束时间必须填写完整。');
  return date.toISOString();
}

function Header({ actions }: { actions: ReactElement }): ReactElement {
  return <header className="page-head"><div><h1>教务与课表</h1></div><div className="page-actions">{actions}</div></header>;
}

export function AcademicPage(): ReactElement {
  const { status, me } = useAuth();
  const api = isApiMode();
  const isAdmin = me?.roles.some(role => role.role_code === 'sys_admin') ?? false;
  const canManage = api && status === 'authenticated' && (isAdmin || (me?.roles.some(role => role.role_code === 'academic_admin') ?? false));
  const [data, setData] = useState<AcademicData>(EMPTY);
  const [tab, setTab] = useState<Tab>(requestedTab);
  const [classId, setClassId] = useState('');
  const [editor, setEditor] = useState<Editor | null>(null);
  const [showImport, setShowImport] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState<Schedule | null>(null);
  const canEditTab = canManage && (isAdmin || (tab !== 'terms' && tab !== 'classrooms'));

  useEffect(() => {
    const followLink = () => { setTab(requestedTab()); setEditor(null); setShowImport(false); setFormError(null); };
    window.addEventListener('hashchange', followLink);
    return () => window.removeEventListener('hashchange', followLink);
  }, []);

  async function reload(): Promise<void> {
    if (!api || status !== 'authenticated') return;
    setLoading(true); setError(null);
    try {
      const week = calendarWeekRange();
      const [terms, courses, classrooms, offerings, schedules, classGroups, orgUnits, users] = await Promise.all([
        collectPages(listTerms), collectPages(listCourses), collectPages(listClassrooms), collectPages(listOfferings),
        collectPages(cursor => listSchedules(cursor, week)), collectPages(listClassGroups), collectPages(listOrgUnits), collectPages(listUsers),
      ]);
      setData({ terms, courses, classrooms, offerings, schedules, classGroups, orgUnits, users });
    } catch (cause) {
      setError(describeError(cause));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { void reload(); }, [api, status]);

  const visibleSchedules = useMemo(() => data.schedules.filter(item => classId === '' || item.class_group_id === classId), [data.schedules, classId]);
  const visibleOfferings = useMemo(() => data.offerings.filter(item => classId === '' || item.class_group_id === classId), [data.offerings, classId]);
  const courseName = (id: string) => data.courses.find(item => item.id === id)?.name ?? `课程 ${id.slice(0, 8)}`;
  const teacherName = (id: string) => data.users.find(item => item.id === id)?.displayName ?? id;

  function openNew(): void {
    setFormError(null); setNotice(null); setShowImport(false);
    if (tab === 'schedule') setEditor({ kind: 'schedule', item: null });
    if (tab === 'offerings') setEditor({ kind: 'offering', item: null });
    if (tab === 'courses') setEditor({ kind: 'course', item: null });
    if (tab === 'terms') setEditor({ kind: 'term', item: null });
    if (tab === 'classrooms') setEditor({ kind: 'classroom', item: null });
  }

  async function submitEditor(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (editor === null || !canEditTab || saving) return;
    const form = new FormData(event.currentTarget);
    setSaving(true); setFormError(null); setNotice(null);
    try {
      if (editor.kind === 'schedule') {
        const input = { offering_id: String(form.get('offering_id')), classroom_id: String(form.get('classroom_id')), starts_at: toIso(form.get('starts_at')), ends_at: toIso(form.get('ends_at')), status: String(form.get('status') ?? 'active') };
        if (new Date(input.starts_at) >= new Date(input.ends_at)) throw new Error('结束时间必须晚于开始时间。');
        await (editor.item === null ? createSchedule(input) : patchSchedule(editor.item.id, input));
      } else if (editor.kind === 'offering') {
        const orgUnitId = String(form.get('org_unit_id'));
        let courseId = String(form.get('course_id') ?? '');
        let newCourseSaved = false;
        if (form.get('course_mode') === 'new') {
          const name = String(form.get('new_course_name') ?? '').trim();
          const code = String(form.get('new_course_code') ?? '').trim();
          if (!name || !code) throw new Error('请填写新课程名称和课程编码。');
          // Re-read before creating: a previous save may have created the course
          // before the offering failed, including an uncertain network response.
          const courses = await collectPages(listCourses);
          let course = courses.find(item => item.org_unit_id === orgUnitId && item.code === code);
          if (course && course.name !== name) throw new Error('该课程编码已用于其他课程名称，请选择已有课程或更换编码。');
          if (!course) course = await createCourse({ org_unit_id: orgUnitId, code, name, description: '' });
          courseId = course.id; newCourseSaved = true;
          setData(previous => ({ ...previous, courses: [...previous.courses.filter(item => item.id !== course!.id), course!] }));
        }
        const input = { org_unit_id: orgUnitId, term_id: String(form.get('term_id')), course_id: courseId, teacher_id: String(form.get('teacher_id')), class_group_id: String(form.get('class_group_id')), code: String(form.get('code')).trim(), status: String(form.get('status')) };
        try { await (editor.item === null ? createOffering(input) : patchOffering(editor.item.id, input)); }
        catch (cause) { if (newCourseSaved) throw new Error(`课程已保存，开课尚未保存：${academicError(cause)}。修正后再保存会复用该课程，不会重复创建。`); throw cause; }
      } else if (editor.kind === 'course') {
        const input = { org_unit_id: String(form.get('org_unit_id')), code: String(form.get('code')).trim(), name: String(form.get('name')).trim(), description: String(form.get('description')).trim() };
        await (editor.item === null ? createCourse(input) : patchCourse(editor.item.id, input));
      } else if (editor.kind === 'term') {
        const input = { code: String(form.get('code')).trim(), name: String(form.get('name')).trim(), start_date: String(form.get('start_date')), end_date: String(form.get('end_date')) };
        if (input.start_date >= input.end_date) throw new Error('学期结束日期必须晚于开始日期。');
        await (editor.item === null ? createTerm(input) : patchTerm(editor.item.id, input));
      } else {
        const capacityText = String(form.get('capacity')).trim();
        const capacity = capacityText === '' ? null : Number(capacityText);
        if (capacity !== null && (!Number.isInteger(capacity) || capacity < 0)) throw new Error('教室容量必须是非负整数。');
        const input = { code: String(form.get('code')).trim(), name: String(form.get('name')).trim(), capacity };
        await (editor.item === null ? createClassroom(input) : patchClassroom(editor.item.id, input));
      }
      setEditor(null); setNotice('资料已保存。'); await reload();
    } catch (cause) {
      setFormError(academicError(cause));
    } finally {
      setSaving(false);
    }
  }

  async function cancelSchedule(item: Schedule): Promise<void> {
    if (!canManage || saving) return;
    setSaving(true); setError(null); setNotice(null);
    try {
      await patchSchedule(item.id, { offering_id: item.offering_id, classroom_id: item.classroom_id, starts_at: item.starts_at, ends_at: item.ends_at, status: 'cancelled' });
      setCancelling(null); setNotice('排课已取消，历史记录仍然保留。'); await reload();
    } catch (cause) {
      setError(describeError(cause));
    } finally {
      setSaving(false);
    }
  }

  async function submitImport(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (!canManage || saving) return;
    const form = new FormData(event.currentTarget);
    setSaving(true); setFormError(null); setNotice(null);
    try {
      const lines = String(form.get('rows')).split(/\r?\n/).map(line => line.trim()).filter(Boolean);
      const rows = lines.map((line, index) => {
        const cells = line.split(',').map(cell => cell.trim());
        if (cells.length !== 4 || cells.some(cell => cell === '')) throw new Error(`第 ${index + 1} 行必须包含 4 列。`);
        const starts_at = toIso(cells[2]!); const ends_at = toIso(cells[3]!);
        if (new Date(starts_at) >= new Date(ends_at)) throw new Error(`第 ${index + 1} 行结束时间必须晚于开始时间。`);
        const offering_id = data.offerings.find(item => item.code === cells[0])?.id ?? cells[0]!;
        const classroom_id = data.classrooms.find(item => item.code === cells[1])?.id ?? cells[1]!;
        return { offering_id, classroom_id, starts_at, ends_at };
      });
      if (rows.length === 0) throw new Error('请至少填写一条排课。');
      if (rows.length > 1000) throw new Error('一次最多导入 1000 条排课。');
      const imported = await importSchedules(rows);
      setShowImport(false); setNotice(`已整批导入 ${imported.length} 条排课。`); await reload();
    } catch (cause) {
      setFormError(academicError(cause));
    } finally {
      setSaving(false);
    }
  }

  const primaryLabel = tab === 'schedule' ? '新增排课' : `新增${TAB_LABELS[tab]}`;
  const headerActions = <>{tab === 'schedule' ? <button className="btn btn-secondary" type="button" disabled={saving} onClick={() => { setShowImport(true); setEditor(null); setFormError(null); }}><IconUpload size={16}/>批量导入</button> : null}<button className="btn btn-primary" type="button" disabled={saving} onClick={openNew}><IconPlus size={16}/>{primaryLabel}</button></>;

  return <div className="page">
    <Header actions={<>{tab === 'schedule' ? <button className="btn btn-secondary" type="button" disabled={visibleSchedules.length === 0} onClick={() => downloadScheduleCsv(visibleSchedules, data)}>导出课表 CSV</button> : null}{canEditTab ? headerActions : null}</>}/>
    {!api ? <p className="notice-strip">固定样例模式不编造教务资料；切换到 API 模式并登录后读取真实数据。</p> : null}
        <div className="toolbar academic-toolbar"><div data-guide="academic-tabs" className="tabs">{(Object.keys(TAB_LABELS) as Tab[]).map(value => <button className={`tab ${tab === value ? 'active' : ''}`} type="button" key={value} disabled={saving} onClick={() => { setTab(value); setEditor(null); setShowImport(false); setFormError(null); }}>{TAB_LABELS[value]}</button>)}</div>{tab === 'schedule' || tab === 'offerings' ? <select className="m1-filter-control" aria-label="课表班级" value={classId} onChange={event => setClassId(event.target.value)}><option value="">全部班级</option>{data.classGroups.map(item => <option key={item.id} value={item.id}>{item.enrollmentYear ?? '未设置年份'} · {item.name}</option>)}</select> : null}</div>
        {notice ? <p className="notice-strip">{notice}</p> : null}
    {showImport ? <Card title="批量导入课表" ><form className="m1-form" onSubmit={event => void submitImport(event)}><label className="account-field"><span>排课数据</span><textarea name="rows" rows={7} placeholder="COURSE-2026,A101,2026-10-20T08:00:00+08:00,2026-10-20T09:40:00+08:00" required/></label><p className="muted">可从开课实例、教室标签页查看编号；不带时区的时间按当前电脑所在时区解释。</p>{formError ? <p className="load-note load-note-error">{formError}</p> : null}<div className="form-actions"><button className="btn btn-secondary" type="button" disabled={saving} onClick={() => setShowImport(false)}>取消</button><button className="btn btn-primary" disabled={saving}>{saving ? '导入中…' : '整批导入'}</button></div></form></Card> : null}
    {cancelling ? <Card title="确认取消排课" ><p>{formatDateTime(cancelling.starts_at)} — {formatDateTime(cancelling.ends_at)}</p><div className="form-actions"><button type="button" className="btn btn-secondary" disabled={saving} onClick={() => setCancelling(null)}>返回</button><button type="button" className="btn btn-danger" disabled={saving} onClick={() => void cancelSchedule(cancelling)}>确认取消排课</button></div></Card> : null}
    {editor ? <AcademicEditor key={`${editor.kind}:${editor.item?.id ?? 'new'}`} editor={editor} data={data} saving={saving} error={formError} onCancel={() => setEditor(null)} onSubmit={submitEditor}/> : null}
    <section className="card m1-table-card">
      <div className="m1-section-head"><div><h2>{TAB_LABELS[tab]}</h2><p className="muted small">{tab === 'schedule' ? '显示本周排课；取消后仍保留记录。' : '维护教学基础资料，已关联课堂的历史记录保留。'}</p></div></div>
      <LoadStateNote status={loading ? 'loading' : error ? 'error' : 'ready'} error={error}/>
      {error ? <button type="button" className="btn btn-secondary btn-sm" disabled={loading} onClick={() => void reload()}>{loading ? '重新读取中…' : '重新读取教务资料'}</button> : null}
      <div><AcademicTable onDeleted={reload} canEdit={canEditTab} tab={tab} data={data} schedules={visibleSchedules} offerings={visibleOfferings} courseName={courseName} teacherName={teacherName} saving={saving} onEdit={value => { if (canEditTab && !saving) { setFormError(null); setShowImport(false); setEditor(value); } }} onCancelSchedule={async item => { if (canManage && !saving) setCancelling(item); }}/></div>
    </section>
  </div>;
}

function AcademicEditor({ editor, data, saving, error, onCancel, onSubmit }: { editor: Editor; data: AcademicData; saving: boolean; error: string | null; onCancel(): void; onSubmit(event: FormEvent<HTMLFormElement>): Promise<void> }): ReactElement {
  const title = `${editor.item === null ? '新增' : '编辑'}${({ schedule: '排课', offering: '开课实例', course: '课程', term: '学期', classroom: '教室' })[editor.kind]}`;
  const [newCourse, setNewCourse] = useState(false);
  return <Card title={title}><form className="m1-form" onSubmit={event => void onSubmit(event)}>
    {editor.kind === 'schedule' ? <><label className="account-field"><span>开课实例</span><select name="offering_id" defaultValue={editor.item?.offering_id} required>{data.offerings.map(item => <option key={item.id} value={item.id}>{data.courses.find(course => course.id === item.course_id)?.name ?? item.course_id} · {item.code}</option>)}</select></label><label className="account-field"><span>教室</span><select name="classroom_id" defaultValue={editor.item?.classroom_id} required>{data.classrooms.map(item => <option key={item.id} value={item.id}>{item.name}（{item.code}）</option>)}</select></label><label className="account-field"><span>开始时间</span><input name="starts_at" type="datetime-local" defaultValue={editor.item ? localDateTime(editor.item.starts_at) : ''} required/></label><label className="account-field"><span>结束时间</span><input name="ends_at" type="datetime-local" defaultValue={editor.item ? localDateTime(editor.item.ends_at) : ''} required/></label><input type="hidden" name="status" value={editor.item?.status ?? 'active'}/></> : null}
    {editor.kind === 'offering' ? <><label className="account-field"><span>所属学院 / 院系</span><select name="org_unit_id" defaultValue={editor.item?.org_unit_id} required>{data.orgUnits.filter(item => item.kind !== 'school').map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label className="account-field"><span>学期</span><select name="term_id" defaultValue={editor.item?.term_id} required>{data.terms.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><div className="account-field"><input type="hidden" name="course_mode" value={newCourse ? 'new' : 'existing'}/>{newCourse ? <><label className="account-field"><span>新课程名称</span><input name="new_course_name" placeholder="例如：操作系统" required disabled={saving}/></label><label className="account-field"><span>新课程编码</span><input name="new_course_code" placeholder="例如：OS-001（在所属学院 / 院系内唯一）" required disabled={saving}/></label><p className="field-hint">保存时新增课程并关联这次开课。不会修改原有课程名称；若只需改名，请在“课程”标签编辑。</p></> : <label className="account-field"><span>课程</span><select name="course_id" defaultValue={editor.item?.course_id} required disabled={saving}>{data.courses.map(item => <option key={item.id} value={item.id}>{item.name}（{item.code}）</option>)}</select></label>}<button type="button" className="btn btn-secondary btn-sm" disabled={saving} onClick={() => setNewCourse(value => !value)}>{newCourse ? '改为选择已有课程' : '输入新课程名称'}</button></div><label className="account-field"><span>任课教师</span><select name="teacher_id" defaultValue={editor.item?.teacher_id} required>{data.users.filter(item => item.status === 'active').map(item => <option key={item.id} value={item.id}>{item.displayName}（{item.username}）</option>)}</select></label><label className="account-field"><span>班级</span><select name="class_group_id" defaultValue={editor.item?.class_group_id} required>{data.classGroups.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label className="account-field"><span>开课编号</span><input name="code" defaultValue={editor.item?.code} required/></label><label className="account-field"><span>状态</span><select name="status" defaultValue={editor.item?.status ?? 'active'}><option value="active">进行中</option><option value="archived">已归档</option></select></label></> : null}
    {editor.kind === 'course' ? <><label className="account-field"><span>所属学院 / 院系</span><select name="org_unit_id" defaultValue={editor.item?.org_unit_id} required>{data.orgUnits.filter(item => item.kind !== 'school').map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label className="account-field"><span>课程编码</span><input name="code" defaultValue={editor.item?.code} required/></label><label className="account-field"><span>课程名称</span><input name="name" defaultValue={editor.item?.name} required/></label><label className="account-field"><span>课程说明</span><textarea name="description" defaultValue={editor.item?.description}/></label></> : null}
    {editor.kind === 'term' ? <><label className="account-field"><span>学期编码</span><input name="code" defaultValue={editor.item?.code} required/></label><label className="account-field"><span>学期名称</span><input name="name" defaultValue={editor.item?.name} required/></label><label className="account-field"><span>开始日期</span><input name="start_date" type="date" defaultValue={editor.item?.start_date} required/></label><label className="account-field"><span>结束日期</span><input name="end_date" type="date" defaultValue={editor.item?.end_date} required/></label></> : null}
    {editor.kind === 'classroom' ? <><label className="account-field"><span>教室编码</span><input name="code" defaultValue={editor.item?.code} required/></label><label className="account-field"><span>教室名称</span><input name="name" defaultValue={editor.item?.name} required/></label><label className="account-field"><span>容量</span><input name="capacity" type="number" min="0" step="1" defaultValue={editor.item?.capacity ?? ''}/></label></> : null}
    {error ? <p className="load-note load-note-error">{error}</p> : null}
    
    <div className="form-actions"><button className="btn btn-secondary" type="button" disabled={saving} onClick={onCancel}>取消</button><button className="btn btn-primary" disabled={saving}>{saving ? '保存中…' : '保存'}</button></div>
  </form></Card>;
}

function AcademicTable({ onDeleted, canEdit, tab, data, schedules, offerings, courseName, teacherName, saving, onEdit, onCancelSchedule }: { onDeleted(): void | Promise<void>; canEdit: boolean; tab: Tab; data: AcademicData; schedules: Schedule[]; offerings: Offering[]; courseName(id: string): string; teacherName(id: string): string; saving: boolean; onEdit(editor: Editor): void; onCancelSchedule(item: Schedule): Promise<void> }): ReactElement {
  if (tab === 'schedule') return <><div className="table-wrap"><table className="table"><thead><tr><th>课程 / 开课实例</th><th>班级 / 教师</th><th>教室</th><th>上课时间</th><th>状态</th><th>操作</th></tr></thead><tbody>{schedules.map(item => { const current = data.offerings.find(value => value.id === item.offering_id); return <tr key={item.id}><td><strong>{courseName(current?.course_id ?? '')}</strong><br/><span className="muted small">{current?.code ?? item.offering_id}</span></td><td>{data.classGroups.find(group => group.id === item.class_group_id)?.name ?? item.class_group_id}<br/><span className="muted small">{teacherName(item.teacher_id)}</span></td><td>{data.classrooms.find(room => room.id === item.classroom_id)?.name ?? item.classroom_id}</td><td>{formatDateTime(item.starts_at)}<br/>{formatDateTime(item.ends_at)}</td><td>{item.status === 'cancelled' ? '已取消' : '有效'}</td><td>{canEdit ? <div className="row-actions"><button className="btn btn-secondary btn-sm" type="button" disabled={saving || item.status === 'cancelled'} onClick={() => onEdit({ kind: 'schedule', item })}>编辑</button><button className="btn btn-secondary btn-sm" type="button" disabled={saving || item.status === 'cancelled'} onClick={() => void onCancelSchedule(item)}>取消排课</button><DeleteUnusedButton resource="schedules" id={item.id} name={`排课 ${formatDateTime(item.starts_at)}`} disabled={saving} onDeleted={onDeleted}/></div> : '—'}</td></tr>; })}</tbody></table></div>{schedules.length === 0 ? <div className="academic-empty-state"><EmptyState text="暂无可见课表。"/></div> : null}</>;
  if (tab === 'offerings') return <><div className="table-wrap"><table className="table"><thead><tr><th>课程 / 编号</th><th>任课教师</th><th>班级</th><th>学期</th><th>状态</th><th>操作</th></tr></thead><tbody>{offerings.map(item => <tr key={item.id}><td><strong>{courseName(item.course_id)}</strong><br/><span className="muted small">{item.code}</span></td><td>{teacherName(item.teacher_id)}</td><td>{data.classGroups.find(group => group.id === item.class_group_id)?.name ?? item.class_group_id}</td><td>{data.terms.find(term => term.id === item.term_id)?.name ?? item.term_id}</td><td>{item.status === 'archived' ? '已归档' : '进行中'}</td><td>{canEdit ? <div className="row-actions"><button className="btn btn-secondary btn-sm" type="button" disabled={saving} onClick={() => onEdit({ kind: 'offering', item })}>编辑</button><DeleteUnusedButton resource="offerings" id={item.id} name={item.code} disabled={saving} onDeleted={onDeleted}/></div> : '—'}</td></tr>)}</tbody></table></div>{offerings.length === 0 ? <div className="academic-empty-state"><EmptyState text="暂无开课实例。"/></div> : null}</>;
  if (tab === 'courses') return <><div className="table-wrap"><table className="table"><thead><tr><th>课程名称</th><th>编码</th><th>所属组织</th><th>说明</th><th>操作</th></tr></thead><tbody>{data.courses.map(item => <tr key={item.id}><td><strong>{item.name}</strong></td><td>{item.code}</td><td>{data.orgUnits.find(unit => unit.id === item.org_unit_id)?.name ?? item.org_unit_id}</td><td>{item.description || '—'}</td><td>{canEdit ? <div className="row-actions"><button className="btn btn-secondary btn-sm" type="button" disabled={saving} onClick={() => onEdit({ kind: 'course', item })}>编辑</button><DeleteUnusedButton resource="courses" id={item.id} name={item.name} disabled={saving} onDeleted={onDeleted}/></div> : '—'}</td></tr>)}</tbody></table></div>{data.courses.length === 0 ? <div className="academic-empty-state"><EmptyState text="暂无课程。"/></div> : null}</>;
  if (tab === 'terms') return <><div className="table-wrap"><table className="table"><thead><tr><th>学期名称</th><th>编码</th><th>开始日期</th><th>结束日期</th><th>操作</th></tr></thead><tbody>{data.terms.map(item => <tr key={item.id}><td><strong>{item.name}</strong></td><td>{item.code}</td><td>{item.start_date}</td><td>{item.end_date}</td><td>{canEdit ? <div className="row-actions"><button className="btn btn-secondary btn-sm" type="button" disabled={saving} onClick={() => onEdit({ kind: 'term', item })}>编辑</button><DeleteUnusedButton resource="terms" id={item.id} name={item.name} disabled={saving} onDeleted={onDeleted}/></div> : '—'}</td></tr>)}</tbody></table></div>{data.terms.length === 0 ? <div className="academic-empty-state"><EmptyState text="暂无学期。"/></div> : null}</>;
  return <><div className="table-wrap"><table className="table"><thead><tr><th>教室名称</th><th>编码</th><th>容量</th><th>操作</th></tr></thead><tbody>{data.classrooms.map(item => <tr key={item.id}><td><strong>{item.name}</strong></td><td>{item.code}</td><td>{item.capacity ?? '未设置'}</td><td>{canEdit ? <div className="row-actions"><button className="btn btn-secondary btn-sm" type="button" disabled={saving} onClick={() => onEdit({ kind: 'classroom', item })}>编辑</button><DeleteUnusedButton resource="classrooms" id={item.id} name={item.name} disabled={saving} onDeleted={onDeleted}/></div> : '—'}</td></tr>)}</tbody></table></div>{data.classrooms.length === 0 ? <div className="academic-empty-state"><EmptyState text="暂无教室。"/></div> : null}</>;
}
