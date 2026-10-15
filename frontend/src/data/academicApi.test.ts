import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './errors';
import { clearCsrfToken, setCsrfToken } from './http';
import {
  createClassGroup,
  createOrgUnit,
  listClassGroups,
  listOrgUnits,
  orgUnitName,
  parseClassGroup,
  parseOrgUnit,
  parsePositiveInteger,
  patchClassGroup,
  patchOrgUnit,
} from './academicApi';

/**
 * 基础资料接口的字段与 Go 的 internal/academic/service.go 对齐：
 * OrgUnit = id/parent_id/code/name/kind，ClassGroup 另外带 enrollment_year 与 expected_size。
 * 这里把请求路径、请求体字段名（snake_case）、空值和分页游标都钉住。
 */

interface Call {
  url: string;
  init: RequestInit | undefined;
}

let calls: Call[] = [];

function respond(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => (body === undefined ? '' : JSON.stringify(body)),
  } as unknown as Response;
}

/** 统一回一个成功信封；列表与单条都用同一套 {data, request_id}。 */
function envelope(data: unknown): Response {
  return respond(200, { data, request_id: 'req-1' });
}

function stub(handler: (url: string) => Response): void {
  vi.stubGlobal('fetch', async (url: unknown, init?: RequestInit) => {
    const target = String(url);
    calls.push({ url: target, init });
    return handler(target);
  });
}

const COLLEGE = {
  id: '00000000-0000-4000-8000-000000000001',
  parent_id: null,
  code: 'CS',
  name: '计算机学院',
  kind: 'college',
};

const CLASS_GROUP = {
  id: '00000000-0000-4000-8000-000000000002',
  org_unit_id: COLLEGE.id,
  code: 'SE-2301',
  name: '软件工程 2301',
  enrollment_year: 2023,
  expected_size: null,
};

beforeEach(() => {
  calls = [];
  clearCsrfToken();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('基础资料 DTO 解析', () => {
  it('OrgUnit 的 kind 只接受 school / college / department', () => {
    expect(parseOrgUnit(COLLEGE).name).toBe('计算机学院');
    // 解析层直接抛普通 Error（guardFail 的原文），包成 ApiError 是读写函数的职责。
    expect(() => parseOrgUnit({ ...COLLEGE, kind: 'faculty' })).toThrow(/kind/);
    expect(() => parseOrgUnit({ ...COLLEGE, code: 1 })).toThrow(/code/);
  });

  it('ClassGroup 允许 enrollment_year / expected_size 为 null，但不允许缺字段', () => {
    expect(parseClassGroup(CLASS_GROUP).enrollmentYear).toBe(2023);
    expect(parseClassGroup({ ...CLASS_GROUP, expected_size: 42 }).expectedSize).toBe(42);
    const { expected_size: _drop, ...missing } = CLASS_GROUP;
    expect(() => parseClassGroup(missing)).toThrow(/expected_size/);
  });
});

describe('基础资料读写', () => {
  it('列表读取带上游标，返回 items 与 next_cursor', async () => {
    stub((url) => {
      expect(url).toContain('cursor=abc');
      expect(url).toContain('limit=100');
      return envelope({ items: [COLLEGE], next_cursor: null });
    });

    const page = await listOrgUnits('abc');
    expect(page.items).toHaveLength(1);
    expect(page.nextCursor).toBeNull();
    expect(calls[0]?.url).toContain('/api/v1/org-units');
  });

  it('班级列表按同一套信封解析', async () => {
    stub(() => envelope({ items: [CLASS_GROUP], next_cursor: 'next-1' }));
    const page = await listClassGroups();
    expect(page.nextCursor).toBe('next-1');
    expect(page.items[0]?.code).toBe('SE-2301');
  });

  it('新增学院用 POST，字段名是 snake_case，并且带 CSRF 令牌', async () => {
    setCsrfToken('csrf-1');
    stub(() => respond(201, { data: COLLEGE, request_id: 'req-2' }));

    await createOrgUnit({ parentId: null, code: 'CS', name: '计算机学院', kind: 'college' });

    const call = calls[0];
    expect(call?.init?.method).toBe('POST');
    expect(call?.init?.body).toBe(
      JSON.stringify({ parent_id: null, code: 'CS', name: '计算机学院', kind: 'college' }),
    );
    expect((call?.init?.headers as Record<string, string>)['X-CSRF-Token']).toBe('csrf-1');
  });

  it('编辑学院用 PATCH，路径带 URL 编码后的 id', async () => {
    setCsrfToken('csrf-1');
    stub(() => envelope({ ...COLLEGE, name: '计算机与人工智能学院' }));

    const updated = await patchOrgUnit(
      '00000000-0000-4000-8000-000000000001',
      { parentId: null, code: 'CS', name: '计算机与人工智能学院', kind: 'college' },
    );

    expect(calls[0]?.init?.method).toBe('PATCH');
    expect(calls[0]?.url).toContain('/api/v1/org-units/00000000-0000-4000-8000-000000000001');
    expect(updated.name).toBe('计算机与人工智能学院');
  });

  it('新增班级要求入学年份，预计人数可空', async () => {
    setCsrfToken('csrf-1');
    stub(() => respond(201, { data: CLASS_GROUP, request_id: 'req-3' }));

    await createClassGroup({
      orgUnitId: COLLEGE.id,
      code: 'SE-2301',
      name: '软件工程 2301',
      enrollmentYear: 2023,
      expectedSize: null,
    });

    expect(calls[0]?.init?.body).toBe(
      JSON.stringify({
        org_unit_id: COLLEGE.id,
        code: 'SE-2301',
        name: '软件工程 2301',
        enrollment_year: 2023,
        expected_size: null,
      }),
    );
  });

  it('编辑班级同样是 PATCH', async () => {
    setCsrfToken('csrf-1');
    stub(() => envelope(CLASS_GROUP));
    await patchClassGroup(CLASS_GROUP.id, {
      orgUnitId: COLLEGE.id,
      code: 'SE-2301',
      name: '软件工程 2301',
      enrollmentYear: 2023,
      expectedSize: 40,
    });
    expect(calls[0]?.init?.method).toBe('PATCH');
    expect(calls[0]?.url).toContain(`/api/v1/class-groups/${CLASS_GROUP.id}`);
  });

  it('写请求没有 CSRF 令牌时直接报错，不发请求', async () => {
    stub(() => envelope(COLLEGE));
    await expect(
      createOrgUnit({ parentId: null, code: 'CS', name: '计算机学院', kind: 'college' }),
    ).rejects.toThrow(ApiError);
    expect(calls).toHaveLength(0);
  });

  it('服务端错误按统一信封转成 ApiError，页面才能显示真实错误码', async () => {
    stub(() =>
      respond(403, {
        error: { code: 'FORBIDDEN', message: 'system administrator permission is required', details: {} },
        request_id: 'req-4',
      }),
    );
    await expect(listOrgUnits()).rejects.toThrow(/system administrator permission is required/);
  });
});

describe('展示与本地校验的小工具', () => {
  it('用 id 找名字，找不到时说明不可见', () => {
    const units = [parseOrgUnit(COLLEGE)];
    expect(orgUnitName(units, COLLEGE.id)).toContain('计算机学院');
    expect(orgUnitName(units, null)).toBe('—');
    expect(orgUnitName(units, '00000000-0000-4000-8000-0000000000ff')).toBe('不可见');
  });

  it('入学年份与人数只接受正整数', () => {
    expect(parsePositiveInteger('2026')).toBe(2026);
    expect(parsePositiveInteger(' 30 ')).toBe(30);
    expect(parsePositiveInteger('')).toBeNull();
    expect(parsePositiveInteger('0')).toBeNull();
    expect(parsePositiveInteger('-1')).toBeNull();
    expect(parsePositiveInteger('20 26')).toBeNull();
    expect(parsePositiveInteger('2026.5')).toBeNull();
  });
});
