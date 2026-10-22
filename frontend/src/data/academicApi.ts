/**
 * 基础资料（学院/院系、班级）的读写。
 *
 * 路径见 contracts/endpoints.json，DTO 字段与 Go 的 internal/academic/service.go
 * （OrgUnit / ClassGroup）逐个对齐；前端只做解析和展示，不做权限判断——
 * 谁能改什么由服务端逐次决定，越权会返回 403/404，页面把服务端的错误原样显示。
 *
 * 两个必须记住的约束：
 *   1) 这些资源支持 GET / POST / PATCH 及受引用保护的 DELETE，
 *      页面删除对应真实后端接口（frontend/UI约定.md 第 5 节）；
 *   2) 列表接口只支持 cursor/limit，没有筛选参数，"按学院/入学年份筛选"是
 *      前端在已加载的数据上做的（和《系统草图.html》的原型行为一致）。
 */

import { ApiError } from './errors';
import {
  asEnum,
  asNullableInteger,
  asNullableString,
  asRecord,
  asString,
  objectList,
} from './guards';
import { requestJson } from './http';

/** org_units.kind 的取值，见 internal/academic/service.go 的 validateOrg。 */
export const ORG_UNIT_KINDS = ['school', 'college', 'department'] as const;
export type OrgUnitKind = (typeof ORG_UNIT_KINDS)[number];

export const ORG_UNIT_KIND_LABEL: Record<OrgUnitKind, string> = {
  school: '学校',
  college: '学院',
  department: '系 / 教研室',
};

export interface OrgUnit {
  id: string;
  parentId: string | null;
  code: string;
  name: string;
  kind: OrgUnitKind;
}

export interface ClassGroup {
  id: string;
  orgUnitId: string;
  code: string;
  name: string;
  /** 入学年份不是组织层级，只是班级的一个属性（项目文档 3.1）。 */
  enrollmentYear: number | null;
  expectedSize: number | null;
}

/** 创建与编辑用同一组字段：后端 PATCH 就是逐字段覆盖，见 PatchOrgUnit。 */
export interface OrgUnitInput {
  parentId: string | null;
  code: string;
  name: string;
  kind: OrgUnitKind;
}

export interface ClassGroupInput {
  orgUnitId: string;
  code: string;
  name: string;
  enrollmentYear: number;
  expectedSize: number | null;
}

/** 开课实例：课堂（session）必须挂在某个开课实例下，所以创建课堂要先选它。 */
export interface Offering {
  id: string;
  orgUnitId: string;
  courseId: string;
  classGroupId: string;
  code: string;
  status: string;
}

/** 课程：用来把 offering.course_id 显示成课程名。 */
export interface Course {
  id: string;
  code: string;
  name: string;
}

export function parseOffering(value: unknown, path = 'Offering'): Offering {
  const source = asRecord(value, path);
  return {
    id: asString(source, 'id', path),
    orgUnitId: asString(source, 'org_unit_id', path),
    courseId: asString(source, 'course_id', path),
    classGroupId: asString(source, 'class_group_id', path),
    code: asString(source, 'code', path),
    status: asString(source, 'status', path),
  };
}

export function parseCourse(value: unknown, path = 'Course'): Course {
  const source = asRecord(value, path);
  return {
    id: asString(source, 'id', path),
    code: asString(source, 'code', path),
    name: asString(source, 'name', path),
  };
}

function asMalformed(error: unknown): ApiError {
  return new ApiError({
    code: 'MALFORMED_RESPONSE',
    status: 200,
    message: error instanceof Error ? error.message : '响应不符合协议',
  });
}

export function parseOrgUnit(value: unknown, path = 'OrgUnit'): OrgUnit {
  const source = asRecord(value, path);
  return {
    id: asString(source, 'id', path),
    parentId: asNullableString(source, 'parent_id', path),
    code: asString(source, 'code', path),
    name: asString(source, 'name', path),
    kind: asEnum(source, 'kind', path, ORG_UNIT_KINDS),
  };
}

export function parseClassGroup(value: unknown, path = 'ClassGroup'): ClassGroup {
  const source = asRecord(value, path);
  return {
    id: asString(source, 'id', path),
    orgUnitId: asString(source, 'org_unit_id', path),
    code: asString(source, 'code', path),
    name: asString(source, 'name', path),
    enrollmentYear: asNullableInteger(source, 'enrollment_year', path),
    expectedSize: asNullableInteger(source, 'expected_size', path),
  };
}

/** 列表返回 {items,next_cursor}，与协议第 1 节的列表信封一致。 */
interface ListEnvelope<T> {
  items: T[];
  nextCursor: string | null;
}

function parseList<T>(
  value: unknown,
  path: string,
  parse: (item: unknown, itemPath: string) => T,
): ListEnvelope<T> {
  const source = asRecord(value, path);
  const items = objectList(source, 'items', path).map((item, index) =>
    parse(item, `${path}.items[${index}]`),
  );
  return {
    items,
    // 列表的 next_cursor 是"下一页的起点"，null 表示已经到最后一页。
    nextCursor: asNullableString(source, 'next_cursor', path),
  };
}

export async function listOrgUnits(cursor?: string): Promise<ListEnvelope<OrgUnit>> {
  // limit 上界是 100（协议第 1 节），显式带上可以减少翻页次数；cursor 原样回传，
  // 它是服务端编码的不透明游标，前端不解码。
  const { data } = await requestJson<unknown>('/org-units', { query: { cursor, limit: 100 } });
  try {
    return parseList(data, 'OrgUnitPage', parseOrgUnit);
  } catch (error) {
    throw asMalformed(error);
  }
}

export async function createOrgUnit(input: OrgUnitInput): Promise<OrgUnit> {
  const { data } = await requestJson<unknown>('/org-units', {
    method: 'POST',
    body: toOrgUnitBody(input),
    csrf: true,
  });
  try {
    return parseOrgUnit(data);
  } catch (error) {
    throw asMalformed(error);
  }
}

export async function patchOrgUnit(id: string, input: OrgUnitInput): Promise<OrgUnit> {
  const { data } = await requestJson<unknown>(`/org-units/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: toOrgUnitBody(input),
    csrf: true,
  });
  try {
    return parseOrgUnit(data);
  } catch (error) {
    throw asMalformed(error);
  }
}

export async function listClassGroups(cursor?: string): Promise<ListEnvelope<ClassGroup>> {
  const { data } = await requestJson<unknown>('/class-groups', { query: { cursor, limit: 100 } });
  try {
    return parseList(data, 'ClassGroupPage', parseClassGroup);
  } catch (error) {
    throw asMalformed(error);
  }
}

export async function createClassGroup(input: ClassGroupInput): Promise<ClassGroup> {
  const { data } = await requestJson<unknown>('/class-groups', {
    method: 'POST',
    body: toClassGroupBody(input),
    csrf: true,
  });
  try {
    return parseClassGroup(data);
  } catch (error) {
    throw asMalformed(error);
  }
}

export async function patchClassGroup(id: string, input: ClassGroupInput): Promise<ClassGroup> {
  const { data } = await requestJson<unknown>(`/class-groups/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: toClassGroupBody(input),
    csrf: true,
  });
  try {
    return parseClassGroup(data);
  } catch (error) {
    throw asMalformed(error);
  }
}

export async function listOfferings(cursor?: string): Promise<ListEnvelope<Offering>> {
  const { data } = await requestJson<unknown>('/offerings', { query: { cursor, limit: 100 } });
  try {
    return parseList(data, 'OfferingPage', parseOffering);
  } catch (error) {
    throw asMalformed(error);
  }
}

export async function listCourses(cursor?: string): Promise<ListEnvelope<Course>> {
  const { data } = await requestJson<unknown>('/courses', { query: { cursor, limit: 100 } });
  try {
    return parseList(data, 'CoursePage', parseCourse);
  } catch (error) {
    throw asMalformed(error);
  }
}

/** 开课实例的展示名：课程名（找不到就退回课程编号）+ 开课编号。 */
export function offeringLabel(
  offering: Offering,
  courses: readonly Course[],
  classGroups: readonly ClassGroup[],
): string {
  const course = courses.find((item) => item.id === offering.courseId);
  const group = classGroups.find((item) => item.id === offering.classGroupId);
  const head = course === undefined ? `课程 ${offering.courseId.slice(0, 4)}` : course.name;
  return `${head} · ${group?.name ?? '未知班级'}（${offering.code}）`;
}

/** 请求体字段名用 snake_case，空值写 null，不省略字段（协议第 1 节）。 */
function toOrgUnitBody(input: OrgUnitInput): Record<string, unknown> {
  return {
    parent_id: input.parentId,
    code: input.code,
    name: input.name,
    kind: input.kind,
  };
}

function toClassGroupBody(input: ClassGroupInput): Record<string, unknown> {
  return {
    org_unit_id: input.orgUnitId,
    code: input.code,
    name: input.name,
    enrollment_year: input.enrollmentYear,
    expected_size: input.expectedSize,
  };
}

/** 页面展示用：把 parent_id / org_unit_id 换成名字，找不到时说明"不在可见范围内"。 */
export function orgUnitName(units: readonly OrgUnit[], id: string | null): string {
  if (id === null) {
    return '—';
  }
  const unit = units.find((item) => item.id === id);
  return unit === undefined ? '不可见' : `${unit.name}（${ORG_UNIT_KIND_LABEL[unit.kind]}）`;
}

/** 列表里的整数校验复用 guards，供页面表单做一次本地检查，避免白跑一趟服务端。 */
export function parsePositiveInteger(raw: string): number | null {
  if (!/^\d+$/.test(raw.trim())) {
    return null;
  }
  const value = Number(raw.trim());
  return Number.isInteger(value) && value > 0 ? value : null;
}
