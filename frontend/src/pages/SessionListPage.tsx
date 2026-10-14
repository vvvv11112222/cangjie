import type { ReactElement } from 'react';
import type { SessionTaskView } from '../data/viewModel';
import { useSessionTasks } from '../data/hooks';
import {
  formatDateTime,
  runModeLabel,
  runStatusBadge,
  sessionActionLabel,
  sessionStatusBadge,
  shortId,
} from '../format';
import { BadgeTag, Card, DataSourceNote, DisclosureList, IdChip, LoadControls, LoadStateNote, Pill, RunErrorBox, RunFields, StageFlow } from '../components/ui';
import { IconAlert, IconHistory, IconList, IconPlay, IconShield } from '../components/icons';

function Reference({ label, id }: { label: string; id: string | null }): ReactElement {
  return (
    <span className="reference">
      <span className="reference-label">{label}</span>
      {id === null ? <span className="muted">null</span> : <IdChip id={id} />}
    </span>
  );
}

export function SessionListPage({ tasks: injectedTasks }: { tasks?: SessionTaskView[] } = {}): ReactElement {
  const loaded = useSessionTasks(injectedTasks === undefined);
  const tasks = injectedTasks ?? loaded.data.tasks;
  const origin = loaded.origin;
  // 操作展示以各资源返回的 allowed_actions 为准（协议第 1.1 节：前端按 allowed_actions 展示，
  // 后端逐次授权）。账号级能力只在账号区展示，不用来删减服务端已授予的操作。
  const truncated = injectedTasks === undefined && loaded.data.nextCursor !== null;
  // 数据没准备好（接口模式加载中/失败）时不渲染明细：避免用空数组假装"没有课堂"。
  const ready = injectedTasks !== undefined || loaded.hasData;
  const head = (
    <header className="page-head">
      <div>
        <p className="eyebrow">我的课堂</p>
        <h1>课堂任务</h1>
        <p className="muted">
          一行对应一个课堂，显示当前主媒体、最新批次和当前报告引用。
          按钮按 allowed_actions 展示，前端展示不代替后端逐次授权。
        </p>
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
        {head}
        {notes}
      </div>
    );
  }

  return (
    <div className="page">
      {head}
      {notes}

      <Card
        title="课堂列表"
        icon={<IconList size={16} />}
        subtitle={
          truncated
            ? `已加载 ${tasks.length} 个课堂，还有更多课堂可加载`
            : `共 ${tasks.length} 个课堂，next_cursor 为 null，已到最后一页`
        }
      >
        {tasks.length === 0 ? (
          <p className="empty">
            {truncated ? '本页没有课堂，可继续加载后续页面。' : origin.kind === 'api' ? '当前账号没有可见的课堂。' : '固定样例中没有课堂。'}
          </p>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>课堂</th>
                  <th>计划时间</th>
                  <th>状态</th>
                  <th>当前引用</th>
                  <th>允许操作</th>
                </tr>
              </thead>
              <tbody>
                {tasks.map(({ session }) => (
                  <tr key={session.id}>
                    <td>
                      <div className="cell-title">
                        <span className="cell-mark" aria-hidden="true">
                          <IconPlay size={14} />
                        </span>
                        <strong>{session.title}</strong>
                        {session.is_demo ? <Pill label="示范数据" tone="blue" /> : null}
                        <a className="cell-link" href={`#/sessions/${session.id}`}>
                          详情
                        </a>
                      </div>
                      <div className="cell-meta mono">
                        <span title={`session.id = ${session.id}`}>课堂编号 {shortId(session.id)}</span>
                        <span title={`session.offering_id = ${session.offering_id}`}>开课 {shortId(session.offering_id)}</span>
                        <span title="session.schedule_entry_id">
                          {session.schedule_entry_id === null
                            ? '未关联排课'
                            : `排课 ${shortId(session.schedule_entry_id)}`}
                        </span>
                      </div>
                    </td>
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
                      <div className="reference-list">
                        <Reference label="主媒体" id={session.primary_media_asset_id} />
                        <Reference label="最新批次" id={session.latest_run_id} />
                        <Reference label="当前报告" id={session.current_report_id} />
                      </div>
                    </td>
                    <td>
                      {session.allowed_actions.length === 0 ? (
                        <span className="muted">无</span>
                      ) : (
                        <div className="pill-row">
                          {session.allowed_actions.map((action) => (
                            <Pill key={action} label={sessionActionLabel(action)} />
                          ))}
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {tasks.map(({ session, run, stages }) =>
        run === null || stages === null ? null : (
          <Card
            key={run.id}
            title={`最近批次 ${shortId(run.id)}`}
            icon={<IconHistory size={16} />}
            subtitle={`${session.title} · ${runModeLabel(run.mode)} · 状态 ${run.status}`}
            actions={<BadgeTag badge={runStatusBadge(run.status)} />}
          >
            <div className="stage-summary">
              <div className="stage-summary-head">
                <strong>
                  阶段完成 {stages.succeeded}/{stages.total}
                </strong>
                <span className="muted small">
                  失败 {stages.failed} · 跳过 {stages.skipped} · 待处理 {stages.pending}
                </span>
              </div>
              {/* 进度只按真实阶段状态算，跳过不计入完成，也不当作"未开始"。 */}
              <div
                className="progress"
                role="img"
                aria-label={`阶段完成 ${stages.succeeded}/${stages.total}`}
              >
                <span
                  className="progress-fill"
                  style={{ width: `${stages.total === 0 ? 0 : Math.round((stages.succeeded / stages.total) * 100)}%` }}
                />
              </div>
              <span className="muted tiny">
                {stages.active
                  ? '活动批次：接入后端后每 2 秒轮询，进入终态即停止。'
                  : '批次已到终态，停止轮询。'}
              </span>
            </div>

            <StageFlow run={run} />

            {/* 没出错时直接显示三项协议字段；出错时收进错误区的技术详情。 */}
            {run.error_code === null ? <RunFields run={run} /> : <RunErrorBox run={run} />}

            {run.limitations.length > 0 ? (
              <p className="notice-strip">
                <IconAlert size={14} aria-hidden="true" />
                批次限制：{run.limitations.join('；')}
              </p>
            ) : null}
          </Card>
        ),
      )}

      {tasks.map(({ session, disclosures }) =>
        disclosures.length === 0 ? null : (
          <Card
            key={`notes-${session.id}`}
            title="样例数据核对"
            icon={<IconShield size={16} />}
            subtitle={`${session.title}：这些提示来自数据本身，接入后端后应由服务端状态替代。`}
          >
            <DisclosureList items={disclosures} />
          </Card>
        ),
      )}
    </div>
  );
}
