import { useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import { useAuth } from '../auth';
import { cancelRun, createRun, newIdempotencyKey, uploadMedia } from '../data/actions';
import { describeError } from '../data/errors';
import {
  fixtureSession,
  useRunPolling,
  useSessionDetail,
  useSessionMedia,
  useSessionReport,
  useSessionTranscript,
} from '../data/hooks';
import { isRunActive } from '../data/loaders';
import { fixtures } from '../data/samples';
import { isApiMode } from '../data/sources';
import type { CreateRun, RunMode } from '../types';
import {
  formatDateTime,
  runModeLabel,
  runStatusBadge,
  sessionActionLabel,
  sessionStatusBadge,
  shortId,
} from '../format';
import {
  BadgeTag,
  Card,
  DataSourceNote,
  Field,
  IdChip,
  LoadControls,
  LoadStateNote,
  RunErrorBox,
  RunFields,
  StageFlow,
  TechDetails,
} from '../components/ui';
import { IconAlert } from '../components/icons';
import {
  actionStateFor,
  emptyActionState,
  intentSignature,
  processingMediaId,
  writeActionState,
} from './sessionActions';
import type { ActionState } from './sessionActions';
import { ReportPage } from './ReportPage';
import { TranscriptPage } from './TranscriptPage';

type Tab = 'run' | 'transcript' | 'report';

const TABS: { key: Tab; label: string }[] = [
  { key: 'run', label: '批次与操作' },
  { key: 'transcript', label: '转写' },
  { key: 'report', label: '报告' },
];

function analyzeInput(mediaAssetId: string, mode: RunMode): CreateRun {
  return {
    media_asset_id: mediaAssetId,
    mode,
    input_transcript_revision_id: null,
    config_profile: 'p0-v1',
  };
}

export function SessionDetailPage({ sessionId }: { sessionId: string }): ReactElement {
  const api = isApiMode();
  const { sessionKey } = useAuth();
  /**
   * 动作状态归属：auth sessionKey + 课堂 id（评审意见 F01）。
   * 退出、换账号、换课堂都会改变 scope，渲染时立即按空状态呈现。
   */
  const scope = `${sessionKey}:${sessionId}`;

  const [tab, setTab] = useState<Tab>('run');
  const [actions, setActions] = useState<ActionState>(() => emptyActionState(scope));
  const action = actionStateFor(actions, scope);
  const write = (fromScope: string, update: (base: ActionState) => ActionState): void => {
    setActions((current) => writeActionState(current, fromScope, update));
  };

  const detail = useSessionDetail(sessionId, api);
  const fixtureSessionData = api ? null : fixtureSession(sessionId);
  const session = api ? detail.data.session : fixtureSessionData;
  const baseRun = api
    ? detail.run
    : fixtureSessionData !== null && fixtureSessionData.latest_run_id === fixtures.run.id
      ? fixtures.run
      : null;

  const transcript = useSessionTranscript(session, tab === 'transcript');
  const report = useSessionReport(session, baseRun, tab === 'report');
  const media = useSessionMedia(session, api && tab === 'run');

  const effectiveRun = action.run ?? baseRun;
  const poll = useRunPolling(effectiveRun?.id ?? null, effectiveRun?.status ?? null, api && session !== null);
  const shownRun = poll.run ?? effectiveRun;

  // 评审意见 F04：批次进入终态后刷新 Session，否则转写/报告还绑在旧的 latest_run_id 上。
  const activeRuns = useRef<Set<string>>(new Set());
  const refreshedRuns = useRef<Set<string>>(new Set());
  // 会话切换（退出、换账号）后这些"已刷新过的批次"记录也失效，避免影响新会话（F01）。
  useEffect(() => {
    activeRuns.current = new Set();
    refreshedRuns.current = new Set();
  }, [scope]);
  useEffect(() => {
    if (shownRun === null) {
      return;
    }
    if (isRunActive(shownRun)) {
      activeRuns.current.add(shownRun.id);
      return;
    }
    if (!activeRuns.current.has(shownRun.id) || refreshedRuns.current.has(shownRun.id)) {
      return;
    }
    refreshedRuns.current.add(shownRun.id);
    detail.reload();
  }, [api, shownRun]);

  async function submitAnalyze(mode: RunMode): Promise<void> {
    const fromScope = scope;
    const mediaAssetId = processingMediaId(action, session?.primary_media_asset_id ?? null);
    if (session === null || mediaAssetId === null) {
      write(fromScope, (base) => ({ ...base, error: '该课堂还没有可处理的媒体，请先上传录像。' }));
      return;
    }
    // 评审意见 F03：同一次逻辑提交（同课堂+模式+媒体）复用同一个幂等键；
    // 只有换模式/换媒体才算新的分析，重新生成键。
    const signature = intentSignature(session.id, mode, mediaAssetId);
    const key =
      action.intentSignature === signature && action.intentKey !== null ? action.intentKey : newIdempotencyKey();

    write(fromScope, (base) => ({
      ...base,
      busy: true,
      error: null,
      note: null,
      intentKey: key,
      intentSignature: signature,
    }));
    try {
      const run = await createRun(session.id, analyzeInput(mediaAssetId, mode), key);
      write(fromScope, (base) => ({
        ...base,
        busy: false,
        run,
        note: `已提交，批次 ${shortId(run.id)}，当前状态 ${run.status}。`,
        intentKey: null,
        intentSignature: null,
      }));
      if (api) {
        detail.reload();
      }
    } catch (error) {
      // 失败时保留 intentKey：用户重试会复用同一个键，避免重复分析（F03）。
      write(fromScope, (base) => ({ ...base, busy: false, error: describeError(error) }));
    }
  }

  async function submitCancel(): Promise<void> {
    const fromScope = scope;
    if (shownRun === null) {
      return;
    }
    write(fromScope, (base) => ({ ...base, busy: true, error: null, note: null }));
    try {
      const run = await cancelRun(shownRun.id);
      write(fromScope, (base) => ({
        ...base,
        busy: false,
        run,
        note: `已请求取消，批次 ${shortId(run.id)} 状态 ${run.status}。`,
      }));
    } catch (error) {
      write(fromScope, (base) => ({ ...base, busy: false, error: describeError(error) }));
    }
  }

  async function submitUpload(): Promise<void> {
    const fromScope = scope;
    if (session === null || action.file === null || action.sourceRecordId.trim() === '') {
      write(fromScope, (base) => ({
        ...base,
        error: '请选择录像文件，并填写已登记来源的 source_record_id。',
      }));
      return;
    }
    write(fromScope, (base) => ({ ...base, busy: true, error: null, note: null }));
    try {
      const result = await uploadMedia(session.id, action.file, action.sourceRecordId.trim());
      // 评审意见 F02：上传返回的 pending 媒体要成为接下来的处理目标，不能只显示一下 id。
      write(fromScope, (base) => ({
        ...base,
        busy: false,
        note: `已上传，媒体 ${shortId(result.mediaId)} 已设为处理目标（状态 pending，可直接媒体准备）。`,
        uploadedMediaId: result.mediaId,
        selectedMediaId: result.mediaId,
        uploadOpen: false,
        file: null,
      }));
      if (api) {
        detail.reload();
        media.reload();
      }
    } catch (error) {
      write(fromScope, (base) => ({ ...base, busy: false, error: describeError(error) }));
    }
  }

  const mediaTarget = processingMediaId(action, session?.primary_media_asset_id ?? null);
  const canUpload = session !== null && session.allowed_actions.includes('upload');
  const canPrepare = session !== null && session.allowed_actions.includes('prepare_media');
  const canAnalyze = session !== null && session.allowed_actions.includes('analyze');
  const canCancel = shownRun !== null && shownRun.allowed_actions.includes('cancel') && isRunActive(shownRun);
  const canRetry = shownRun !== null && shownRun.allowed_actions.includes('retry') && !isRunActive(shownRun);
  const sourceMedia = session === null ? [] : media.data.filter((item) => item.kind === 'source');

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <p className="eyebrow">
            <a href="#/sessions">← 返回课堂列表</a>
          </p>
          <h1>{session?.title ?? '课堂详情'}</h1>
          <p className="muted">
            {session === null
              ? '从课堂列表进入后显示该课堂的批次、转写和报告。'
              : `${formatDateTime(session.planned_start_at)} – ${formatDateTime(session.planned_end_at)}`}
          </p>
        </div>
        {session === null ? null : <BadgeTag badge={sessionStatusBadge(session.status)} />}
      </header>

      <DataSourceNote sources={detail.origin.sources} label={detail.origin.label} note={detail.origin.note} />
      <LoadStateNote status={detail.status} error={detail.error} />
      <LoadControls actionLabel={detail.actionLabel} loadMore={detail.loadMore} />

      {session === null ? (
        <p className="empty">找不到该课堂，或不在当前账号可见范围。</p>
      ) : (
        <>
          <div className="tab-bar" role="tablist" aria-label="课堂详情">
            {TABS.map((item) => (
              <button
                key={item.key}
                type="button"
                role="tab"
                aria-selected={tab === item.key}
                className={`tab-item${tab === item.key ? ' active' : ''}`}
                onClick={() => setTab(item.key)}
              >
                {item.label}
              </button>
            ))}
          </div>

          {tab === 'run' ? (
            <>
              <Card title="课堂信息" subtitle="跳转目标仍逐次鉴权；前端只展示，能否操作由服务端决定。">
                <div className="field-grid">
                  <Field label="课堂 ID">
                    <IdChip id={session.id} />
                  </Field>
                  <Field label="主媒体">
                    {session.primary_media_asset_id === null ? (
                      <span className="muted">null</span>
                    ) : (
                      <IdChip id={session.primary_media_asset_id} />
                    )}
                  </Field>
                  <Field label="最新批次">
                    {session.latest_run_id === null ? <span className="muted">null</span> : <IdChip id={session.latest_run_id} />}
                  </Field>
                  <Field label="当前报告">
                    {session.current_report_id === null ? <span className="muted">null</span> : <IdChip id={session.current_report_id} />}
                  </Field>
                  <Field label="允许操作">
                    {session.allowed_actions.length === 0
                      ? '无'
                      : session.allowed_actions.map(sessionActionLabel).join('、')}
                  </Field>
                  <Field label="处理目标媒体">
                    {mediaTarget === null ? <span className="muted">尚无（先上传录像）</span> : <IdChip id={mediaTarget} />}
                  </Field>
                </div>
                {/* 协议字段原样保留，只是从业务视线里下沉到折叠区。 */}
                <TechDetails>
                  <ul className="tech-list">
                    <li className="mono">session.id = {session.id}</li>
                    <li className="mono">session.offering_id = {session.offering_id}</li>
                    <li className="mono">session.schedule_entry_id = {session.schedule_entry_id ?? 'null'}</li>
                    <li className="mono">session.status = {session.status}</li>
                    <li className="mono">session.is_demo = {String(session.is_demo)}</li>
                    <li className="mono">session.transcript_lock_version = {session.transcript_lock_version}</li>
                    <li className="mono">session.primary_media_asset_id = {session.primary_media_asset_id ?? 'null'}</li>
                    <li className="mono">session.latest_run_id = {session.latest_run_id ?? 'null'}</li>
                    <li className="mono">session.current_report_id = {session.current_report_id ?? 'null'}</li>
                    <li className="mono">
                      session.allowed_actions = [{session.allowed_actions.join(', ')}]
                    </li>
                  </ul>
                </TechDetails>
              </Card>

              {detail.runError === null ? null : (
                <Card title="批次读取失败" subtitle="这是读取错误，不等于该课堂没有批次。">
                  <p className="notice-strip">
                    <IconAlert size={14} aria-hidden="true" />
                    {detail.runError}
                  </p>
                  <div className="action-row">
                    <button type="button" className="btn btn-secondary" onClick={() => detail.reload()}>
                      重试读取
                    </button>
                  </div>
                </Card>
              )}

              <Card
                title={shownRun === null ? '最近批次' : `最近批次 ${shortId(shownRun.id)}`}
                subtitle={
                  shownRun === null
                    ? '该课堂还没有分析批次。'
                    : `${runModeLabel(shownRun.mode)} · 状态 ${shownRun.status}${
                        isRunActive(shownRun)
                          ? poll.error === null
                            ? ' · 每 2 秒刷新'
                            : ' · 轮询中断，显示的是上次结果'
                          : ' · 已进入终态，停止刷新'
                      }`
                }
                actions={shownRun === null ? undefined : <BadgeTag badge={runStatusBadge(shownRun.status)} />}
              >
                {poll.error === null ? null : (
                  <p className="notice-strip">
                    <IconAlert size={14} aria-hidden="true" />
                    轮询失败：{poll.error}
                    {poll.polling ? '（会自动重试）' : '（已停止轮询，请重新登录或刷新页面）'}
                  </p>
                )}
                {shownRun === null ? (
                  <p className="empty">暂无批次。</p>
                ) : (
                  <>
                    {/* 与课堂任务页共用同一套阶段流程和错误区，避免同一批次两种样子。 */}
                    <StageFlow run={shownRun} />
                    {shownRun.error_code === null ? (
                      <RunFields run={shownRun} />
                    ) : (
                      <RunErrorBox run={shownRun} />
                    )}
                    {shownRun.limitations.length > 0 ? (
                      <p className="notice-strip">
                        <IconAlert size={14} aria-hidden="true" />
                        批次限制：{shownRun.limitations.join('；')}
                      </p>
                    ) : null}
                  </>
                )}
              </Card>

              <Card title="操作" subtitle="按钮按资源 allowed_actions 显示；样例模式下不可提交。">
                {/* 主次分明：提交分析是主操作，上传/媒体准备/重试是次操作，取消批次是危险操作。 */}
                <div className="action-row">
                  {canUpload ? (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={!api || action.busy}
                      onClick={() => write(scope, (base) => ({ ...base, uploadOpen: !base.uploadOpen }))}
                    >
                      上传录像
                    </button>
                  ) : null}
                  {canPrepare ? (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={!api || action.busy || mediaTarget === null}
                      onClick={() => void submitAnalyze('media_prepare')}
                    >
                      媒体准备
                    </button>
                  ) : null}
                  {canAnalyze ? (
                    <button
                      type="button"
                      className="btn btn-primary"
                      disabled={!api || action.busy || mediaTarget === null}
                      onClick={() => void submitAnalyze('full')}
                    >
                      提交分析
                    </button>
                  ) : null}
                  {canCancel ? (
                    <button type="button" className="btn btn-danger" disabled={!api || action.busy} onClick={() => void submitCancel()}>
                      取消批次
                    </button>
                  ) : null}
                  {canRetry ? (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={!api || action.busy || mediaTarget === null}
                      onClick={() => void submitAnalyze('full')}
                    >
                      重试
                    </button>
                  ) : null}
                  {session.allowed_actions.length === 0 ? <span className="muted">该课堂没有可执行的操作。</span> : null}
                </div>

                {api && sourceMedia.length > 0 ? (
                  <label className="account-field">
                    <span>处理目标媒体</span>
                    <select
                      value={action.selectedMediaId ?? ''}
                      onChange={(event) =>
                        write(scope, (base) => ({ ...base, selectedMediaId: event.target.value || null }))
                      }
                    >
                      <option value="">（使用课堂当前主媒体）</option>
                      {sourceMedia.map((item) => (
                        <option key={item.id} value={item.id}>
                          {shortId(item.id)} · 原媒体 · {item.status}
                        </option>
                      ))}
                    </select>
                  </label>
                ) : null}

                {!api ? <p className="load-note">固定样例模式未接入后端，操作按钮不可用。</p> : null}
                {action.uploadOpen ? (
                  <div className="upload-form">
                    <label className="account-field">
                      <span>录像文件（必填）</span>
                      <input
                        type="file"
                        onChange={(event) =>
                          write(scope, (base) => ({ ...base, file: event.target.files?.[0] ?? null }))
                        }
                      />
                    </label>
                    <label className="account-field">
                      <span>来源编号（必填）</span>
                      <input
                        value={action.sourceRecordId}
                        onChange={(event) =>
                          write(scope, (base) => ({ ...base, sourceRecordId: event.target.value }))
                        }
                        placeholder="已登记来源的 UUID"
                        aria-describedby="upload-source-hint"
                      />
                      <span className="field-hint" id="upload-source-hint">
                        对应 source_record_id：录像必须先登记来源，上传时才有关联对象。
                      </span>
                    </label>
                    <button
                      type="button"
                      className="btn btn-primary"
                      disabled={!api || action.busy || action.file === null || action.sourceRecordId.trim() === ''}
                      onClick={() => void submitUpload()}
                    >
                      提交上传
                    </button>
                  </div>
                ) : null}
                {action.error === null ? null : <p className="load-note load-note-error">{action.error}</p>}
                {action.note === null ? null : <p className="load-note">{action.note}</p>}
              </Card>
            </>
          ) : null}

          {tab === 'transcript' ? (
            transcript.status === 'ready' ? (
              <TranscriptPage embedded view={transcript.data} />
            ) : (
              <>
                <LoadStateNote status={transcript.status} error={transcript.error} />
                <LoadControls actionLabel={transcript.actionLabel} loadMore={transcript.loadMore} />
              </>
            )
          ) : null}

          {tab === 'report' ? (
            report.status === 'ready' ? (
              <ReportPage embedded view={report.data} />
            ) : (
              <>
                <LoadStateNote status={report.status} error={report.error} />
                <LoadControls actionLabel={report.actionLabel} loadMore={report.loadMore} />
              </>
            )
          ) : null}
        </>
      )}
    </div>
  );
}
