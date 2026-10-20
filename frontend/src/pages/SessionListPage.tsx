import { useEffect, useMemo, useState } from 'react';
import type { FormEvent } from 'react';
import type { ReactElement } from 'react';
import type { SessionTaskView } from '../data/viewModel';
import { useSessionTasks } from '../data/hooks';
import { formatDateTime, sessionActionLabel, sessionStatusBadge, shortId } from '../format';
import {
  BadgeTag,
  Card,
  DataSourceNote,
  DisclosureList,
  EmptyState,
  LoadControls,
  LoadStateNote,
  Pill,
  TechDetails,
} from '../components/ui';
import { IconAlert, IconPlay, IconPlus, IconRefresh, IconUpload } from '../components/icons';
import { describeError } from '../data/errors';
import { isApiMode } from '../data/sources';
import { listClassGroups, listCourses, listOfferings, offeringLabel } from '../data/academicApi';
import type { ClassGroup, Course, Offering } from '../data/academicApi';
import { createSession, uploadSessionMedia } from '../data/sessionOps';
import { UploadSourcePicker } from './UploadSourcePicker';
import { isUuid } from '../data/ids';

/**
 * 课堂档案（《系统草图.html》的 CLASSROOM ARCHIVE）。
 *
 * 结构和草图一致：页头 → 状态页签 + 搜索 + 重置 → 一张紧凑表格 → 底部记录数与来源。
 * 阶段流程（阶段名与完成阶段数）不放在这一页了：草图的列表页没有它，
 * 课堂详情页「批次与操作」和分析任务页都会显示，信息没有丢。
 *
 * 与草图仍有差异的两处，都是数据本身拿不到，不是漏做：
 *   1) 草图表格有「授课教师 / 班级」两列。Session 与 Offering 接口只返回
 *      offering_id / teacher_id / class_group_id，没有姓名；GET /users 只对
 *      系统管理员和学院教务开放，教师和督导角色读不到教师姓名。这两列等后端
 *      在 DTO 里补只读显示名，或团队同意前端拼接口后再加。
 *   2) 草图的操作列是「查看详情 / 查看报告 / 上传录像」按钮。这里保住了真实可用的
 *      跳转（查看详情）和服务端授予的操作标签，但不把没有实现的写操作做成按钮。
 */

type Filter = 'all' | 'review' | 'published' | 'processing' | 'empty';

/** 页头右上角的两个操作（对应草图的"创建课堂""上传课堂录像"）。 */
type Compose = 'create' | 'upload' | null;

const FILTERS: readonly { key: Filter; label: string }[] = [
  { key: 'all', label: '全部课堂' },
  { key: 'review', label: '待复核' },
  { key: 'published', label: '已发布' },
  { key: 'processing', label: '分析中' },
  { key: 'empty', label: '待上传' },
];

/**
 * 页签口径全部取自协议字段，不自造状态：
 * 待上传 = 还没有主媒体；分析中 = 最新批次在排队或运行；
 * 待复核 = 批次已结束但还没有当前报告；已发布 = 已经有当前报告。
 */
function bucketOf(task: SessionTaskView): Exclude<Filter, 'all'> {
  const { session, run } = task;
  if (session.primary_media_asset_id === null) {
    return 'empty';
  }
  if (run !== null && (run.status === 'queued' || run.status === 'running')) {
    return 'processing';
  }
  if (session.current_report_id !== null) {
    return 'published';
  }
  return 'review';
}

function Reference({ label, id }: { label: string; id: string | null }): ReactElement {
  return (
    <span className="reference">
      <span className="reference-label">{label}</span>
      {id === null ? <span className="muted">null</span> : <span className="mono">{shortId(id)}</span>}
    </span>
  );
}

export function SessionListPage({ tasks: injectedTasks }: { tasks?: SessionTaskView[] } = {}): ReactElement {
  const loaded = useSessionTasks(injectedTasks === undefined);
  const tasks = injectedTasks ?? loaded.data.tasks;
  const origin = loaded.origin;
  const [filter, setFilter] = useState<Filter>('all');
  const [query, setQuery] = useState('');
  const [compose, setCompose] = useState<Compose>(null);
  const [uploadNotice, setUploadNotice] = useState<string | null>(null);
  const api = isApiMode();

  // 操作展示以各资源返回的 allowed_actions 为准（协议第 1.1 节：前端按 allowed_actions 展示，
  // 后端逐次授权）。账号级能力只在账号菜单展示，不用来删减服务端已授予的操作。
  const truncated = injectedTasks === undefined && loaded.data.nextCursor !== null;
  // 数据没准备好（接口模式加载中/失败）时不渲染明细：避免用空数组假装"没有课堂"。
  const ready = injectedTasks !== undefined || loaded.hasData;

  // 筛选与搜索都在已加载的这一页上做：列表接口只有 cursor/limit，没有筛选参数。
  const shown = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return tasks.filter(
      (task) =>
        (filter === 'all' || bucketOf(task) === filter) &&
        (keyword === '' || task.session.title.toLowerCase().includes(keyword)),
    );
  }, [tasks, filter, query]);

  const head = (
    <header className="page-head">
      <div>
        <p className="eyebrow">CLASSROOM ARCHIVE</p>
        <h1>课堂档案</h1>
        <p className="muted">
          关联课程与录像，保留每一次教学观察的来处。
        </p>
      </div>
      {/*
        草图的页头操作：学院教务只有"创建课堂"，其它角色还能"上传课堂录像"。
        固定样例模式没有后端，这两个写操作按钮不渲染（避免点了必然失败）。
      */}
      <div className="heading-actions">
          <button type="button" className="btn btn-secondary" disabled={!api} title={!api ? '接口模式下可创建课堂' : undefined} onClick={() => setCompose('create')}>
            <IconPlus size={15} />
            创建课堂
          </button>
          <button type="button" className="btn btn-primary" disabled={!api} title={!api ? '接口模式下可上传课堂录像' : undefined} onClick={() => { setUploadNotice(null); setCompose('upload'); }}>
            <IconUpload size={15} />
            上传课堂录像
          </button>
      </div>
    </header>
  );

  const notes = (
    <>
      <DataSourceNote sources={origin.sources} label={origin.label} note={origin.note} />
      <LoadStateNote status={loaded.status} error={loaded.error} />
      <LoadControls actionLabel={loaded.actionLabel} loadMore={loaded.loadMore} />
    </>
  );

  if (!ready) {
    return (
      <div className="page">
        {notes}
        {head}
      </div>
    );
  }

  return (
    <div className="page">
      {notes}
      {head}

      {uploadNotice && <p className="notice-strip" role="status">{uploadNotice}</p>}
      {compose === 'create' ? (
        <CreateSessionForm
          onCancel={() => setCompose(null)}
          onCreated={() => {
            setCompose(null);
            loaded.reload();
          }}
        />
      ) : null}
      {compose === 'upload' ? (
        <UploadMediaForm
          tasks={tasks}
          onCancel={() => setCompose(null)}
          onUploaded={() => {
            setCompose(null);
            setUploadNotice('录像已上传，可进入对应课堂继续进行举手与低头分析。');
            loaded.reload();
          }}
        />
      ) : null}

      <div className="toolbar">
          <div className="tab-bar" role="tablist" aria-label="课堂状态筛选">
            {FILTERS.map((item) => (
              <button
                key={item.key}
                type="button"
                role="tab"
                aria-selected={filter === item.key}
                className={`tab-item${filter === item.key ? ' active' : ''}`}
                onClick={() => setFilter(item.key)}
              >
                {item.label}
              </button>
            ))}
          </div>
          <div className="toolbar-actions">
            <input
              className="input filter-search"
              type="search"
              aria-label="筛选课堂"
              placeholder="搜索课程、教师、班级"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={() => {
                setFilter('all');
                setQuery('');
              }}
            >
              <IconRefresh size={14} />
              重置
            </button>
          </div>
      </div>

      <section className="card session-archive-card">
        {shown.length === 0 ? (
          <p className="empty">
            {truncated
              ? '本页没有符合条件的课堂，可继续加载后续页面。'
              : origin.kind === 'api'
                ? '当前账号没有符合条件的课堂。'
                : '固定样例中没有符合条件的课堂。'}
          </p>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>课堂 / 课程</th>
                  <th>授课教师</th>
                  <th>班级</th>
                  <th>上课时间</th>
                  <th>课堂状态</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {shown.map(({ session }) => (
                  <tr key={session.id}>
                    <td>
                      <div className="cell-title">
                        <span className="cell-mark" aria-hidden="true">
                          <IconPlay size={14} />
                        </span>
                        <strong>{session.title}</strong>
                        {session.is_demo ? <Pill label="示范数据" tone="blue" /> : null}
                      </div>
                      <div className="cell-meta mono">
                        <span title={`session.id = ${session.id}`}>课堂编号 {shortId(session.id)}</span>
                        <span title={`session.offering_id = ${session.offering_id}`}>
                          开课 {shortId(session.offering_id)}
                        </span>
                      </div>
                    </td>
                    <td className="muted">—</td>
                    <td className="muted">—</td>
                    <td className="mono nowrap">
                      <div>{formatDateTime(session.planned_start_at)}</div>
                      <div className="muted">{formatDateTime(session.planned_end_at)}</div>
                    </td>
                    <td>
                      <BadgeTag badge={sessionStatusBadge(session.status)} />
                      <div className="muted tiny" title="session.transcript_lock_version">
                        转写版本 v{session.transcript_lock_version}
                      </div>
                    </td>
                    <td>
                      <a className="cell-link" href={`#/sessions/${session.id}`}>
                        {session.primary_media_asset_id === null
                          ? '上传录像'
                          : session.current_report_id === null
                            ? '查看详情'
                            : '查看报告'} →
                      </a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <div className="table-footer">
          <span>
            共 {shown.length} 条记录
            {shown.length === tasks.length ? '' : `（已筛选，本页共 ${tasks.length} 条）`}，已按当前角色过滤
          </span>
          <span className="badge badge-gray">{origin.kind === 'api' ? '接口课堂档案' : '固定样例档案'}</span>
        </div>
      </section>

      {/* 样例数据核对是给开发看的口径说明，收进折叠区，不占业务视线（UI约定第 4 节）。 */}
      {tasks.map(({ session, disclosures }) =>
        disclosures.length === 0 ? null : (
          <TechDetails key={`notes-${session.id}`} summary={`样例数据核对：${session.title}`}>
            <DisclosureList items={disclosures} />
          </TechDetails>
        ),
      )}
      {tasks.map(({ session }) => (
        <TechDetails key={`refs-${session.id}`} summary={`技术引用：${session.title}`}>
          <div className="reference-list">
            <Reference label="主媒体" id={session.primary_media_asset_id} />
            <Reference label="最新批次" id={session.latest_run_id} />
            <Reference label="当前报告" id={session.current_report_id} />
          </div>
          <div className="pill-row">
            {session.allowed_actions.length === 0
              ? <span className="muted tiny">服务端未授予操作</span>
              : session.allowed_actions.map((action) => (
                  <Pill key={action} label={sessionActionLabel(action)} />
                ))}
          </div>
          <p className="muted tiny">
            授课教师和班级展示名不在 Session DTO 中；页面不根据编号猜测名称。
          </p>
        </TechDetails>
      ))}
    </div>
  );
}

/** datetime-local 的值转成协议要求的带时区 RFC3339；非法输入返回 null。 */
function toRfc3339(local: string): string | null {
  if (local.trim() === '') {
    return null;
  }
  const date = new Date(local);
  return Number.isNaN(date.getTime()) ? null : date.toISOString();
}

/**
 * 创建课堂：POST /sessions。开课实例必须先选（课堂挂在开课实例下），
 * 计划开始/结束要与课表一致由服务端判断，这里只做必填与时间顺序检查。
 */
function CreateSessionForm({
  onCancel,
  onCreated,
}: {
  onCancel(): void;
  onCreated(): void;
}): ReactElement {
  const [offerings, setOfferings] = useState<Offering[] | null>(null);
  const [courses, setCourses] = useState<Course[]>([]);
  const [classes, setClasses] = useState<ClassGroup[]>([]);
  const [offeringId, setOfferingId] = useState('');
  const [title, setTitle] = useState('');
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');
  const [isDemo, setIsDemo] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [offeringPage, coursePage, classPage] = await Promise.all([
          listOfferings(),
          listCourses(),
          listClassGroups(),
        ]);
        if (cancelled) {
          return;
        }
        setOfferings(offeringPage.items);
        setCourses(coursePage.items);
        setClasses(classPage.items);
        setOfferingId(offeringPage.items[0]?.id ?? '');
      } catch (caught) {
        if (!cancelled) {
          setOfferings([]);
          setError(describeError(caught));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  async function submit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    const plannedStartAt = toRfc3339(start);
    const plannedEndAt = toRfc3339(end);
    if (plannedStartAt === null || plannedEndAt === null) {
      setError('计划开始与结束时间都要填。');
      return;
    }
    if (new Date(plannedEndAt).getTime() <= new Date(plannedStartAt).getTime()) {
      setError('计划结束时间必须晚于开始时间。');
      return;
    }
    if (offeringId === '') {
      setError('请先选择一个开课实例。');
      return;
    }
    setBusy(true);
    try {
      await createSession({ offeringId, title: title.trim(), plannedStartAt, plannedEndAt, isDemo });
      onCreated();
    } catch (caught) {
      setError(describeError(caught));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="创建课堂" subtitle="课堂必须挂在开课实例下；计划时间与课表冲突时由服务端拒绝。">
      {offerings === null ? (
        <p className="load-note">正在读取开课实例……</p>
      ) : offerings.length === 0 ? (
        <EmptyState text="当前范围内没有开课实例，先由学院教务或系统管理员在「教务与课表」里建好开课。" />
      ) : (
        <form className="directory-form" onSubmit={submit}>
          <label className="account-field">
            <span>开课实例</span>
            <select value={offeringId} onChange={(event) => setOfferingId(event.target.value)} required>
              {offerings.map((item) => (
                <option key={item.id} value={item.id}>
                  {offeringLabel(item, courses, classes)}
                </option>
              ))}
            </select>
          </label>
          <label className="account-field">
            <span>课堂标题</span>
            <input value={title} onChange={(event) => setTitle(event.target.value)} required />
          </label>
          <label className="account-field">
            <span>计划开始</span>
            <input type="datetime-local" value={start} onChange={(event) => setStart(event.target.value)} required />
          </label>
          <label className="account-field">
            <span>计划结束</span>
            <input type="datetime-local" value={end} onChange={(event) => setEnd(event.target.value)} required />
          </label>
          <label className="check-row">
            <input type="checkbox" checked={isDemo} onChange={(event) => setIsDemo(event.target.checked)} />
            标记为示范数据（is_demo，检索时可以区分）
          </label>
          <div className="directory-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>
              {busy ? '创建中…' : '创建课堂'}
            </button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={onCancel}>
              取消
            </button>
          </div>
          {error === null ? null : (
            <p className="notice-strip">
              <IconAlert size={14} aria-hidden="true" />
              创建失败：{error}
            </p>
          )}
        </form>
      )}
    </Card>
  );
}

/**
 * 上传课堂录像：POST /sessions/{id}/media（multipart）。
 * 只能传到自己有 upload 权限的课堂上——用服务端给的 allowed_actions 判断，
 * 前端只是提前提示，真正能不能传还是服务端说了算。
 */
function UploadMediaForm({
  tasks,
  onCancel,
  onUploaded,
}: {
  tasks: readonly SessionTaskView[];
  onCancel(): void;
  onUploaded(): void;
}): ReactElement {
  const uploadable = tasks.filter((task) => task.session.allowed_actions.includes('upload'));
  const [sessionId, setSessionId] = useState(uploadable[0]?.session.id ?? '');
  const [sourceRecordId, setSourceRecordId] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (file === null) {
      setError('请选择要上传的录像文件。');
      return;
    }
    if (!isUuid(sourceRecordId.trim())) {
      setError('请选择这节课堂已登记的录像来源；来源由系统生成，不能填写“1.2”等自编编号。');
      return;
    }
    setBusy(true); setError(null);
    try {
      await uploadSessionMedia(sessionId, file, sourceRecordId.trim());
      onUploaded();
    } catch (caught) {
      setError(describeError(caught));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="上传课堂录像" subtitle="上传后媒体状态为 pending，时长与关键帧要等媒体检查完成。">
      {uploadable.length === 0 ? (
        <EmptyState text="当前没有可上传的课堂：要么还没有课堂，要么服务端没有给你这些课堂的 upload 权限。" />
      ) : (
        <form className="directory-form" onSubmit={submit}>
          <label className="account-field">
            <span>课堂</span>
            <select value={sessionId} disabled={busy} onChange={(event) => { setSessionId(event.target.value); setSourceRecordId(''); setError(null); }} required>
              {uploadable.map((task) => (
                <option key={task.session.id} value={task.session.id}>
                  {task.session.title}（{shortId(task.session.id)}）
                </option>
              ))}
            </select>
          </label>
          <UploadSourcePicker key={sessionId} sessionId={sessionId} value={sourceRecordId} onChange={setSourceRecordId} manualFallback={false} disabled={busy} />
          <label className="account-field">
            <span>录像文件</span>
            <input
              type="file"
              accept="video/*"
              onChange={(event) => setFile(event.target.files?.[0] ?? null)}
              required
            />
          </label>
          <div className="directory-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>
              {busy ? '上传中…' : '开始上传'}
            </button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={onCancel}>
              取消
            </button>
          </div>
          {error === null ? null : (
            <p className="notice-strip">
              <IconAlert size={14} aria-hidden="true" />
              上传失败：{error}
            </p>
          )}
        </form>
      )}
    </Card>
  );
}
