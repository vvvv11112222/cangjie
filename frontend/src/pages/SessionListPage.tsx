import type { FormEvent,ReactElement } from 'react';
import { useEffect,useMemo,useState } from 'react';
import { IconAlert,IconInfo,IconPlay,IconPlus,IconRefresh,IconUpload } from '../components/icons';
import {
BadgeTag,
Card,
EmptyState,
LoadControls,
LoadStateNote,
Pill
} from '../components/ui';
import type { ClassGroup,Course,Offering } from '../data/academicApi';
import { listClassGroups,listCourses,listOfferings,offeringLabel } from '../data/academicApi';
import { describeError } from '../data/errors';
import { useSessionTasks } from '../data/hooks';
import { isUuid } from '../data/ids';
import { collectPages } from '../data/pagination';
import { routeQuery as readRouteQuery,sessionDetailHash,sessionSearchFromHash } from '../data/route';
import { createSession,uploadSessionMedia } from '../data/sessionOps';
import { isApiMode } from '../data/sources';
import type { SessionTaskView } from '../data/viewModel';
import { formatDateTime,sessionStatusBadge } from '../format';
import { UploadSourcePicker } from './UploadSourcePicker';

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
 * 没有主媒体不能推断没有上传。分析中看真实批次；待复核须已有报告草稿。
 * 无当前报告的视觉分析保留在全部课堂，不冒充待复核教学报告。
 */
export function bucketOf(task: SessionTaskView): Exclude<Filter, 'all'> | 'other' {
  const { session, run } = task;
  if (run !== null && (run.status === 'queued' || run.status === 'running')) {
    return 'processing';
  }
  if (session.current_report_id !== null) {
    return 'published';
  }
  if (run !== null && run.report_id !== null && run.mode !== 'visual_only' && session.current_report_id !== run.report_id) return 'review';
  return session.primary_media_asset_id === null && run === null ? 'empty' : 'other';
}

export function SessionListPage({ tasks: injectedTasks }: { tasks?: SessionTaskView[] } = {}): ReactElement {
  const loaded = useSessionTasks(injectedTasks === undefined);
  const tasks = injectedTasks ?? loaded.data.tasks;
  const origin = loaded.origin;
  const [filter, setFilter] = useState<Filter>('all');
  const routeQuery = injectedTasks === undefined && typeof window !== 'undefined'
    ? sessionSearchFromHash(window.location.hash)
    : '';
  const [query, setQuery] = useState(routeQuery);
  const [dataScope, setDataScope] = useState<'all' | 'real' | 'demo'>('all');
  const [compose, setCompose] = useState<Compose>(() => readRouteQuery().get('start') === 'upload' ? 'upload' : null);
  const [uploadNotice, setUploadNotice] = useState<string | null>(null);
  const api = isApiMode();

  useEffect(() => setQuery(routeQuery), [routeQuery]);

  // 操作展示以各资源返回的 allowed_actions 为准（协议第 1.1 节：前端按 allowed_actions 展示，
  // 后端逐次授权）。账号级能力只在账号菜单展示，不用来删减服务端已授予的操作。
  const truncated = injectedTasks === undefined && loaded.data.nextCursor !== null;
  // 数据没准备好（接口模式加载中/失败）时不渲染明细：避免用空数组假装"没有课堂"。
  const ready = injectedTasks !== undefined || loaded.hasData;
  const showSearchScope = ready && (query.trim() !== '' || filter !== 'all' || dataScope !== 'all') && truncated;

  // 筛选与搜索都在已加载的这一页上做：列表接口只有 cursor/limit，没有筛选参数。
  const shown = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return tasks.filter(
      (task) =>
        (filter === 'all' || bucketOf(task) === filter) &&
        (dataScope === 'all' || task.session.is_demo === (dataScope === 'demo')) &&
        (keyword === '' || task.session.title.toLowerCase().includes(keyword)),
    );
  }, [tasks, filter, query, dataScope]);

  const head = (
    <header className="page-head">
      <div>
        
        <h1>课堂档案</h1>
        
      </div>
      {/*
        草图的页头操作：学院教务只有"创建课堂"，其它角色还能"上传课堂录像"。
        固定样例模式没有后端，这两个写操作按钮不渲染（避免点了必然失败）。
      */}
      <div className="heading-actions">
          <button type="button" className="btn btn-secondary" disabled={!api} title={!api ? '接口模式下可创建课堂' : undefined} data-guide="create-classroom" onClick={() => setCompose('create')}>
            <IconPlus size={15} />
            创建课堂
          </button>
          <button type="button" className="btn btn-primary" disabled={!api} title={!api ? '接口模式下可上传课堂录像' : undefined} data-guide="upload-recording" onClick={() => { setUploadNotice(null); setCompose('upload'); }}>
            <IconUpload size={15} />
            上传课堂录像
          </button>
      </div>
    </header>
  );

  const notes = (
    <>
      
      <LoadStateNote status={loaded.status} error={loaded.error} />
      {loaded.status!=='ready'&&<LoadControls actionLabel={loaded.actionLabel} loadMore={loaded.loadMore}/>}
      
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
          onCreated={(created) => {
            setCompose(null);
            loaded.reload();
            window.location.hash = sessionDetailHash(created.id, { upload: true });
          }}
        />
      ) : null}
      {compose === 'upload' ? (
        <UploadMediaForm
          tasks={tasks}
          onCancel={() => setCompose(null)}
          onUploaded={(sessionId, mediaId) => {
            setCompose(null);
            setUploadNotice('录像已上传，可进入对应课堂继续进行举手与低头识别。');
            loaded.reload();
            window.location.hash = sessionDetailHash(sessionId, { mediaId });
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
            <label className="account-field"><span className="sr-only">课堂类型</span><select aria-label="课堂类型" value={dataScope} onChange={event=>setDataScope(event.target.value as typeof dataScope)}><option value="all">全部记录</option><option value="real">真实课堂</option><option value="demo">仅演示课堂</option></select></label>
            <input
              className="input filter-search"
              type="search"
              aria-label="筛选课堂"
              placeholder="搜索课堂标题"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={() => {
                setFilter('all');
                setDataScope('all');
                setQuery('');
                if (injectedTasks === undefined && routeQuery !== '') {
                  window.location.hash = '#/sessions';
                }
              }}
            >
              <IconRefresh size={14} />
              重置
            </button>
          </div>
      </div>

      {showSearchScope ? (
        <div className="notice-strip search-scope-note">
          <IconInfo size={14} />
          <span>{query.trim() ? '当前结果只搜索已加载的课堂，后续页面可能还有匹配项。' : '当前仅筛选已加载的课堂，后续页面可能还有匹配项。'}</span>
          <button type="button" className="btn btn-secondary btn-sm" disabled={loaded.actionLabel === null} onClick={loaded.loadMore}>
            {loaded.actionLabel ?? '正在读取…'}
          </button>
        </div>
      ) : null}

      <section className="card session-archive-card" data-guide="classroom-list">
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
                {shown.map(({ session, run }) => (
                  <tr key={session.id}>
                    <td>
                      <div className="cell-title">
                        <span className="cell-mark" aria-hidden="true">
                          <IconPlay size={14} />
                        </span>
                        <strong>{session.title}</strong>
                        {session.is_demo ? <Pill label="示范数据" tone="blue" /> : null}
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
                    </td>
                    <td>
                      <a className="cell-link" href={`#/sessions/${session.id}`}>
                        {session.primary_media_asset_id === null && run === null
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

        <div className="table-footer">{!showSearchScope ? <LoadControls actionLabel={loaded.actionLabel} loadMore={loaded.loadMore} /> : null}
          <span>
            共 {shown.length} 条记录
            {shown.length === tasks.length ? '' : `（已筛选，本页共 ${tasks.length} 条）`}
          </span>
          {origin.kind !== 'api' && <span className="badge badge-gray">演示数据</span>}
        </div>
      </section>

      {/* 样例数据核对是给开发看的口径说明，收进折叠区，不占业务视线（UI约定第 4 节）。 */}
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
  onCreated(session: { id: string }): void;
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
  const [loadRevision, setLoadRevision] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setOfferings(null); setError(null);
    void (async () => {
      try {
        const [allOfferings, allCourses, allClasses] = await Promise.all([
          collectPages(listOfferings),
          collectPages(listCourses),
          collectPages(listClassGroups),
        ]);
        if (cancelled) {
          return;
        }
        const activeOfferings = allOfferings.filter(item => item.status === 'active');
        setOfferings(activeOfferings);
        setCourses(allCourses);
        setClasses(allClasses);
        setOfferingId(activeOfferings[0]?.id ?? '');
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
  }, [loadRevision]);

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
      const created = await createSession({ offeringId, title: title.trim(), plannedStartAt, plannedEndAt, isDemo });
      onCreated(created);
    } catch (caught) {
      setError(describeError(caught));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="创建课堂" >
            {offerings === null ? (
        <p className="load-note">正在读取开课实例……</p>
      ) : offerings.length === 0 ? (
        error ? <div role="alert"><p>开课资料读取失败：{error}</p><button type="button" className="btn btn-secondary" onClick={() => setLoadRevision(value => value + 1)}>重新读取开课资料</button></div>
          : <><EmptyState text="当前范围内没有可创建课堂的有效开课记录。请由学院教务或系统管理员先关联课程、学期、任课教师和班级。" /><a href="#/academic?tab=offerings">前往教务与课表准备开课资料</a><p className="field-hint">没有教务入口时，请联系学院教务或系统管理员处理。</p></>
      ) : (
        <form className="directory-form" onSubmit={submit}>
          <label className="account-field">
            <span>选择课程与班级（开课记录）</span>
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
            标记为演示课堂
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
  onUploaded(sessionId: string, mediaId: string): void;
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
      const uploaded = await uploadSessionMedia(sessionId, file, sourceRecordId.trim());
      onUploaded(sessionId, uploaded.id);
    } catch (caught) {
      setError(describeError(caught));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="上传课堂录像" >
      {uploadable.length === 0 ? (
        <EmptyState text="当前没有可上传的课堂。先点击“创建课堂”；已有课堂仍未显示时，请联系学院教务或系统管理员检查任课关联与授权范围。" />
      ) : (
        <form className="directory-form" onSubmit={submit}>
          <label className="account-field">
            <span>课堂</span>
            <select value={sessionId} disabled={busy} onChange={(event) => { setSessionId(event.target.value); setSourceRecordId(''); setError(null); }} required>
              {uploadable.map((task) => (
                <option key={task.session.id} value={task.session.id}>
                  {task.session.title}
                </option>
              ))}
            </select>
          </label>
          <label className="account-field">
            <span>录像文件（MP4 / MOV / MKV）</span>
            <input
              type="file"
              accept="video/mp4,video/quicktime,video/x-matroska,.mp4,.mov,.mkv"
              onChange={(event) => setFile(event.target.files?.[0] ?? null)}
              required
            />
          </label>
          {file && <p className="field-hint">{file.name}</p>}
          <UploadSourcePicker key={sessionId} sessionId={sessionId} value={sourceRecordId} onChange={setSourceRecordId} manualFallback={false} disabled={busy} />
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
