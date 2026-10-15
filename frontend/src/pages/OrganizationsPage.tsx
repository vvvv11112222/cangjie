/**
 * 学院、年级与班级（《系统草图.html》的 ORGANIZATION 页）。
 *
 * 这一页直接读写 /api/v1/org-units 与 /api/v1/class-groups，没有固定样例版本：
 * 固定样例模式（VITE_DATA_SOURCE 未设为 api）下只给一句说明，不编假数据，
 * 免得把"样例"当成真实基础资料。
 *
 * 与草图的差异（都是因为没有对应接口，不是漏做）：
 *   - 没有"删除"按钮：协议里这两个资源只有 GET/POST/PATCH；
 *   - "年级"不是独立资源：入学年份是班级的属性，只在筛选与列表里体现；
 *   - 筛选在前端已加载的数据上做，因为列表接口只有 cursor/limit。
 */

import { useCallback, useEffect, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Card, EmptyState } from '../components/ui';
import { IconAlert, IconInfo, IconList } from '../components/icons';
import { isApiMode } from '../data/sources';
import { describeError } from '../data/errors';
import {
  ORG_UNIT_KINDS,
  ORG_UNIT_KIND_LABEL,
  createClassGroup,
  createOrgUnit,
  listClassGroups,
  listOrgUnits,
  orgUnitName,
  parsePositiveInteger,
  patchClassGroup,
  patchOrgUnit,
} from '../data/academicApi';
import type { ClassGroup, ClassGroupInput, OrgUnit, OrgUnitInput, OrgUnitKind } from '../data/academicApi';

type Tab = 'colleges' | 'classes';
/** 每页最多 100 条（协议第 1 节的 limit 上界），最多翻 5 页，避免一次拉太多。 */
const MAX_PAGES = 5;

const TAB_LABEL: Record<Tab, string> = { colleges: '学院 / 院系', classes: '班级' };

/** 顺着 next_cursor 把列表取完；超过 MAX_PAGES 时返回 hasMore，由页面如实说明。 */
async function loadAll<T>(
  page: (cursor?: string) => Promise<{ items: T[]; nextCursor: string | null }>,
): Promise<{ items: T[]; hasMore: boolean }> {
  const items: T[] = [];
  let cursor: string | undefined;
  for (let index = 0; index < MAX_PAGES; index += 1) {
    const result = await page(cursor);
    items.push(...result.items);
    if (result.nextCursor === null) {
      return { items, hasMore: false };
    }
    cursor = result.nextCursor;
  }
  return { items, hasMore: true };
}

function OrganizationsHead(): ReactElement {
  return (
    <header className="page-head">
      <div>
        <p className="eyebrow">ORGANIZATION</p>
        <h1>学院、年级与班级</h1>
        <p className="muted">
          班级按入学年份筛选；入学年份不是组织层级。有引用的资料保留历史，不提供删除。
        </p>
      </div>
    </header>
  );
}

export function OrganizationsPage(): ReactElement {
  // 固定样例模式没有后端，这一页不提供样例数据：宁可说明清楚，也不显示假的基础资料。
  if (!isApiMode()) {
    return (
      <div className="page">
        <OrganizationsHead />
        <p className="notice-strip">
          <IconInfo size={14} aria-hidden="true" />
          固定样例模式未接入后端：这一页直接读写 /api/v1/org-units 与 /api/v1/class-groups。
          设 VITE_DATA_SOURCE=api 并启动 Go 服务后可用。
        </p>
      </div>
    );
  }
  return <DirectoryView />;
}

function DirectoryView(): ReactElement {
  const [tab, setTab] = useState<Tab>('colleges');
  const [units, setUnits] = useState<OrgUnit[] | null>(null);
  const [groups, setGroups] = useState<ClassGroup[] | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [yearFilter, setYearFilter] = useState('');
  const [editing, setEditing] = useState<
    { kind: 'unit'; id: string | null } | { kind: 'group'; id: string | null } | null
  >(null);

  const reload = useCallback(async () => {
    setError(null);
    try {
      const [unitPage, groupPage] = await Promise.all([loadAll(listOrgUnits), loadAll(listClassGroups)]);
      setUnits(unitPage.items);
      setGroups(groupPage.items);
      setHasMore(unitPage.hasMore || groupPage.hasMore);
    } catch (caught) {
      setError(describeError(caught));
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const years = [...new Set((groups ?? []).map((group) => group.enrollmentYear).filter((year): year is number => year !== null))].sort((a, b) => b - a);
  const shownGroups = (groups ?? []).filter(
    (group) => yearFilter === '' || String(group.enrollmentYear) === yearFilter,
  );

  async function saveUnit(input: OrgUnitInput, id: string | null): Promise<string | null> {
    try {
      if (id === null) {
        await createOrgUnit(input);
      } else {
        await patchOrgUnit(id, input);
      }
      setEditing(null);
      await reload();
      return null;
    } catch (caught) {
      return describeError(caught);
    }
  }

  async function saveGroup(input: ClassGroupInput, id: string | null): Promise<string | null> {
    try {
      if (id === null) {
        await createClassGroup(input);
      } else {
        await patchClassGroup(id, input);
      }
      setEditing(null);
      await reload();
      return null;
    } catch (caught) {
      return describeError(caught);
    }
  }

  const ready = units !== null && groups !== null;

  return (
    <div className="page">
      <OrganizationsHead />

      {error === null ? null : (
        <p className="notice-strip">
          <IconAlert size={14} aria-hidden="true" />
          读取基础资料失败：{error}
        </p>
      )}
      {hasMore ? (
        <p className="notice-strip">
          <IconInfo size={14} aria-hidden="true" />
          记录较多，这里只加载了前 {MAX_PAGES} 页（每页最多 100 条）。
        </p>
      ) : null}
      {ready ? null : <p className="load-note">正在读取基础资料……</p>}

      {ready ? (
        <>
          <div className="tab-bar" role="tablist" aria-label="基础资料分类">
            {(Object.keys(TAB_LABEL) as Tab[]).map((key) => (
              <button
                key={key}
                type="button"
                role="tab"
                aria-selected={tab === key}
                className={`tab-item${tab === key ? ' active' : ''}`}
                onClick={() => {
                  setTab(key);
                  setEditing(null);
                }}
              >
                {TAB_LABEL[key]}管理
              </button>
            ))}
          </div>

          {editing === null ? (
            <button
              type="button"
              className="btn btn-primary btn-sm"
              onClick={() => setEditing(tab === 'colleges' ? { kind: 'unit', id: null } : { kind: 'group', id: null })}
            >
              新增{TAB_LABEL[tab]}
            </button>
          ) : editing.kind === 'unit' ? (
            <UnitForm
              units={units ?? []}
              unit={editing.id === null ? null : (units ?? []).find((item) => item.id === editing.id) ?? null}
              onCancel={() => setEditing(null)}
              onSave={(input) => saveUnit(input, editing.id)}
            />
          ) : (
            <GroupForm
              units={units ?? []}
              group={
                editing.id === null ? null : (groups ?? []).find((item) => item.id === editing.id) ?? null
              }
              onCancel={() => setEditing(null)}
              onSave={(input) => saveGroup(input, editing.id)}
            />
          )}

          {tab === 'colleges' ? (
            <Card
              title="学院 / 院系"
              icon={<IconList size={16} />}
              subtitle={`共 ${units?.length ?? 0} 条；层级由 kind 与上级决定，没有删除接口`}
            >
              {(units ?? []).length === 0 ? (
                <EmptyState text="当前账号没有可见的组织。先由系统管理员初始化学校与学院。" />
              ) : (
                <div className="table-wrap">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>名称</th>
                        <th>层级</th>
                        <th>编码</th>
                        <th>上级</th>
                        <th>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(units ?? []).map((unit) => (
                        <tr key={unit.id}>
                          <td><strong>{unit.name}</strong></td>
                          <td>{ORG_UNIT_KIND_LABEL[unit.kind]}</td>
                          <td className="mono">{unit.code}</td>
                          <td>{orgUnitName(units ?? [], unit.parentId)}</td>
                          <td>
                            <button
                              type="button"
                              className="btn btn-secondary btn-sm"
                              onClick={() => setEditing({ kind: 'unit', id: unit.id })}
                            >
                              编辑
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Card>
          ) : (
            <Card
              title="班级"
              icon={<IconList size={16} />}
              subtitle={`共 ${shownGroups.length} / ${groups?.length ?? 0} 个班级；筛选在已加载的数据上做`}
              actions={
                <label className="account-field">
                  <span>入学年份</span>
                  <select value={yearFilter} onChange={(event) => setYearFilter(event.target.value)}>
                    <option value="">全部年份</option>
                    {years.map((year) => (
                      <option key={year} value={String(year)}>
                        {year} 级
                      </option>
                    ))}
                  </select>
                </label>
              }
            >
              {shownGroups.length === 0 ? (
                <EmptyState text="没有符合条件的班级。可以清空年份筛选，或新增一个班级。" />
              ) : (
                <div className="table-wrap">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>名称</th>
                        <th>编码</th>
                        <th>所属学院 / 院系</th>
                        <th>入学年份</th>
                        <th>预计人数</th>
                        <th>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {shownGroups.map((group) => (
                        <tr key={group.id}>
                          <td><strong>{group.name}</strong></td>
                          <td className="mono">{group.code}</td>
                          <td>{orgUnitName(units ?? [], group.orgUnitId)}</td>
                          <td>{group.enrollmentYear === null ? '—' : `${group.enrollmentYear} 级`}</td>
                          <td>{group.expectedSize === null ? '未填' : group.expectedSize}</td>
                          <td>
                            <button
                              type="button"
                              className="btn btn-secondary btn-sm"
                              onClick={() => setEditing({ kind: 'group', id: group.id })}
                            >
                              编辑
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Card>
          )}
        </>
      ) : null}
    </div>
  );
}

/** 学院 / 院系的表单：层级决定能不能选上级，学校必须是顶层。 */
function UnitForm({
  units,
  unit,
  onCancel,
  onSave,
}: {
  units: readonly OrgUnit[];
  unit: OrgUnit | null;
  onCancel(): void;
  onSave(input: OrgUnitInput): Promise<string | null>;
}): ReactElement {
  const [name, setName] = useState(unit?.name ?? '');
  const [code, setCode] = useState(unit?.code ?? '');
  const [kind, setKind] = useState<OrgUnitKind>(unit?.kind ?? 'college');
  const [parentId, setParentId] = useState(unit?.parentId ?? '');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // 只有学院和系需要上级；默认选第一个学校，省掉一次服务端报错。
  const parentCandidates = units.filter((item) => item.id !== unit?.id && item.kind !== 'department');

  async function submit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    setBusy(true);
    const message = await onSave({
      parentId: kind === 'school' || parentId === '' ? null : parentId,
      code: code.trim(),
      name: name.trim(),
      kind,
    });
    setBusy(false);
    setError(message);
  }

  return (
    <Card title={unit === null ? '新增学院 / 院系' : `编辑：${unit.name}`} subtitle="保存后会重新读取列表。">
      <form className="directory-form" onSubmit={submit}>
        <label className="account-field">
          <span>名称</span>
          <input value={name} onChange={(event) => setName(event.target.value)} required />
        </label>
        <label className="account-field">
          <span>编码</span>
          <input value={code} onChange={(event) => setCode(event.target.value)} required />
        </label>
        <label className="account-field">
          <span>层级</span>
          <select value={kind} onChange={(event) => setKind(event.target.value as OrgUnitKind)}>
            {ORG_UNIT_KINDS.map((value) => (
              <option key={value} value={value}>
                {ORG_UNIT_KIND_LABEL[value]}
              </option>
            ))}
          </select>
        </label>
        <label className="account-field">
          <span>上级</span>
          <select
            value={parentId}
            disabled={kind === 'school'}
            onChange={(event) => setParentId(event.target.value)}
          >
            <option value="">{kind === 'school' ? '学校没有上级' : '未指定'}</option>
            {parentCandidates.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}（{ORG_UNIT_KIND_LABEL[item.kind]}）
              </option>
            ))}
          </select>
        </label>
        <div className="directory-actions">
          <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>
            {busy ? '保存中…' : '保存'}
          </button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={onCancel}>
            取消
          </button>
        </div>
        {error === null ? null : (
          <p className="notice-strip">
            <IconAlert size={14} aria-hidden="true" />
            保存失败：{error}
          </p>
        )}
      </form>
    </Card>
  );
}

/** 班级的表单：入学年份必填（后端 CreateClassGroup 要求），预计人数可空。 */
function GroupForm({
  units,
  group,
  onCancel,
  onSave,
}: {
  units: readonly OrgUnit[];
  group: ClassGroup | null;
  onCancel(): void;
  onSave(input: ClassGroupInput): Promise<string | null>;
}): ReactElement {
  const [name, setName] = useState(group?.name ?? '');
  const [code, setCode] = useState(group?.code ?? '');
  const [orgUnitId, setOrgUnitId] = useState(group?.orgUnitId ?? units[0]?.id ?? '');
  const [year, setYear] = useState(group?.enrollmentYear === null || group === null ? '' : String(group.enrollmentYear));
  const [size, setSize] = useState(group?.expectedSize === null || group === null ? '' : String(group.expectedSize));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    const enrollmentYear = parsePositiveInteger(year);
    if (enrollmentYear === null) {
      setError('入学年份要填四位数字的整数年份。');
      return;
    }
    const expectedSize = size.trim() === '' ? null : parsePositiveInteger(size);
    if (expectedSize === null && size.trim() !== '') {
      setError('预计人数要填正整数，留空表示未填。');
      return;
    }
    setBusy(true);
    const message = await onSave({
      orgUnitId,
      code: code.trim(),
      name: name.trim(),
      enrollmentYear,
      expectedSize,
    });
    setBusy(false);
    setError(message);
  }

  return (
    <Card title={group === null ? '新增班级' : `编辑：${group.name}`} subtitle="保存后会重新读取列表。">
      <form className="directory-form" onSubmit={submit}>
        <label className="account-field">
          <span>名称</span>
          <input value={name} onChange={(event) => setName(event.target.value)} required />
        </label>
        <label className="account-field">
          <span>编码</span>
          <input value={code} onChange={(event) => setCode(event.target.value)} required />
        </label>
        <label className="account-field">
          <span>所属学院 / 院系</span>
          <select value={orgUnitId} onChange={(event) => setOrgUnitId(event.target.value)} required>
            {units.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}（{ORG_UNIT_KIND_LABEL[item.kind]}）
              </option>
            ))}
          </select>
        </label>
        <label className="account-field">
          <span>入学年份</span>
          <input
            inputMode="numeric"
            placeholder="2026"
            value={year}
            onChange={(event) => setYear(event.target.value)}
            required
          />
        </label>
        <label className="account-field">
          <span>预计人数（可空）</span>
          <input inputMode="numeric" value={size} onChange={(event) => setSize(event.target.value)} />
        </label>
        <div className="directory-actions">
          <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>
            {busy ? '保存中…' : '保存'}
          </button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={onCancel}>
            取消
          </button>
        </div>
        {error === null ? null : (
          <p className="notice-strip">
            <IconAlert size={14} aria-hidden="true" />
            保存失败：{error}
          </p>
        )}
      </form>
    </Card>
  );
}
