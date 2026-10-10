import { useState } from 'react';
import type { ReactElement } from 'react';
import { createRun, cancelRun, uploadMedia } from '../data/actions';
import { describeError } from '../data/errors';
import {
  fixtureSession,
  useRunPolling,
  useSessionDetail,
  useSessionReport,
  useSessionTranscript,
} from '../data/hooks';
import { isRunActive } from '../data/loaders';
import { fixtures } from '../data/samples';
import { isApiMode } from '../data/sources';
import type { CreateRun, Run, RunMode } from '../types';
import {
  formatDateTime,
  jobStageLabel,
  jobStatusBadge,
  runModeLabel,
  runStatusBadge,
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
} from '../components/ui';
import { ReportPage } from './ReportPage';
import { TranscriptPage } from './TranscriptPage';

type Tab = 'run' | 'transcript' | 'report';

const TABS: { key: Tab; label: string }[] = [
  { key: 'run', label: '批次与操作' },
  { key: 'transcript', label: '转写' },
  { key: 'report', label: '报告' },
];

/** 提交分析 / 媒体准备都要主媒体；M1 骨架按协议只发 full 或 media_prepare。 */
function analyzeInput(sessionPrimaryMedia: string | null, mode: RunMode): CreateRun | null {
  if (sessionPrimaryMedia === null) {
    return null;
  }
  return {
    media_asset_id: sessionPrimaryMedia,
    mode,
    input_transcript_revision_id: null,
    config_profile: 'p0-v1',
  };
}

export function SessionDetailPage({ sessionId }: { sessionId: string }): ReactElement {
  const api = isApiMode();
  const [tab, setTab] = useState<Tab>('run');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionNote, setActionNote] = useState<string | null>(null);
  const [actionRun, setActionRun] = useState<Run | null>(null);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [sourceId, setSourceId] = useState('');
  const [file, setFile] = useState<File | null>(null);

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

  // 动作返回的批次优先展示；轮询跟着"动作后最新批次"走。
  const effectiveRun = actionRun ?? baseRun;
  const polledRun = useRunPolling(effectiveRun?.id ?? null, effectiveRun?.status ?? null, api && session !== null);
  const shownRun = polledRun ?? effectiveRun;

  async function submitAnalyze(mode: RunMode): Promise<void> {
    if (session === null) {
      return;
    }
    const input = analyzeInput(session.primary_media_asset_id, mode);
    if (input === null) {
      setActionError('该课堂还没有主媒体，无法提交分析。');
      return;
    }
    setBusy(true);
    setActionError(null);
    setActionNote(null);
    try {
      const run = await createRun(session.id, input);
      setActionRun(run);
      setActionNote(`已提交，批次 ${shortId(run.id)}，当前状态 ${run.status}。`);
    } catch (error) {
      setActionError(describeError(error));
    } finally {
      setBusy(false);
    }
  }

  async function submitCancel(): Promise<void> {
    if (shownRun === null) {
      return;
    }
    setBusy(true);
    setActionError(null);
    setActionNote(null);
    try {
      const run = await cancelRun(shownRun.id);
      setActionRun(run);
      setActionNote(`已请求取消，批次 ${shortId(run.id)} 状态 ${run.status}。`);
    } catch (error) {
      setActionError(describeError(error));
    } finally {
      setBusy(false);
    }
  }

  async function submitUpload(): Promise<void> {
    if (session === null || file === null || sourceId.trim() === '') {
      setActionError('请选择录像文件，并填写已登记来源的 source_record_id。');
      return;
    }
    setBusy(true);
    setActionError(null);
    setActionNote(null);
    try {
      const result = await uploadMedia(session.id, file, sourceId.trim());
      setActionNote(`已上传，媒体 id ${result.mediaId}。`);
      setUploadOpen(false);
      setFile(null);
    } catch (error) {
      setActionError(describeError(error));
    } finally {
      setBusy(false);
    }
  }

  const canUpload = session !== null && session.allowed_actions.includes('upload');
  const canPrepare = session !== null && session.allowed_actions.includes('prepare_media');
  const canAnalyze = session !== null && session.allowed_actions.includes('analyze');
  const canCancel = shownRun !== null && shownRun.allowed_actions.includes('cancel') && isRunActive(shownRun);
  const canRetry = shownRun !== null && shownRun.allowed_actions.includes('retry') && !isRunActive(shownRun);

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
                    {session.allowed_actions.length === 0 ? '无' : session.allowed_actions.join('、')}
                  </Field>
                </div>
              </Card>

              <Card
                title={shownRun === null ? '最近批次' : `最近批次 ${shortId(shownRun.id)}`}
                subtitle={
                  shownRun === null
                    ? '该课堂还没有分析批次。'
                    : `${runModeLabel(shownRun.mode)} · 状态 ${shownRun.status}${isRunActive(shownRun) ? ' · 每 2 秒刷新' : ' · 已进入终态，停止刷新'}`
                }
                actions={shownRun === null ? undefined : <BadgeTag badge={runStatusBadge(shownRun.status)} />}
              >
                {shownRun === null ? (
                  <p className="empty">暂无批次。</p>
                ) : (
                  <>
                    <ol className="stage-list">
                      {shownRun.stage_plan.map((stage) => {
                        const job = shownRun.jobs.find((item) => item.stage === stage) ?? null;
                        return (
                          <li key={stage} className="stage">
                            <span className="stage-name">{jobStageLabel(stage)}</span>
                            {job === null ? (
                              <span className="muted small">阶段计划中未返回任务</span>
                            ) : (
                              <>
                                <BadgeTag badge={jobStatusBadge(job.status)} />
                                <span className="muted tiny">
                                  尝试 {job.attempts}/{job.max_attempts}
                                  {job.status === 'running' ? ` · 本阶段 ${job.progress}%` : ''}
                                  {job.error_code === null ? '' : ` · ${job.error_code}`}
                                </span>
                              </>
                            )}
                          </li>
                        );
                      })}
                    </ol>
                    {shownRun.error_code === null ? null : (
                      <p className="callout callout-amber">批次错误：{shownRun.error_code}</p>
                    )}
                    {shownRun.limitations.length > 0 ? (
                      <p className="callout callout-amber">批次限制：{shownRun.limitations.join('；')}</p>
                    ) : null}
                  </>
                )}
              </Card>

              <Card title="操作" subtitle="按钮按资源 allowed_actions 显示；样例模式下不可提交。">
                <div className="pill-row">
                  {canUpload ? (
                    <button type="button" className="account-button" disabled={!api || busy} onClick={() => setUploadOpen((v) => !v)}>
                      上传录像
                    </button>
                  ) : null}
                  {canPrepare ? (
                    <button
                      type="button"
                      className="account-button"
                      disabled={!api || busy || session.primary_media_asset_id === null}
                      onClick={() => void submitAnalyze('media_prepare')}
                    >
                      媒体准备
                    </button>
                  ) : null}
                  {canAnalyze ? (
                    <button
                      type="button"
                      className="account-button"
                      disabled={!api || busy || session.primary_media_asset_id === null}
                      onClick={() => void submitAnalyze('full')}
                    >
                      提交分析
                    </button>
                  ) : null}
                  {canCancel ? (
                    <button type="button" className="account-button" disabled={!api || busy} onClick={() => void submitCancel()}>
                      取消批次
                    </button>
                  ) : null}
                  {canRetry ? (
                    <button
                      type="button"
                      className="account-button"
                      disabled={!api || busy || session.primary_media_asset_id === null}
                      onClick={() => void submitAnalyze('full')}
                    >
                      重试
                    </button>
                  ) : null}
                  {session.allowed_actions.length === 0 ? <span className="muted">该课堂没有可执行的操作。</span> : null}
                </div>

                {!api ? <p className="load-note">固定样例模式未接入后端，操作按钮不可用。</p> : null}
                {uploadOpen ? (
                  <div className="upload-form">
                    <label className="account-field">
                      <span>录像文件</span>
                      <input
                        type="file"
                        onChange={(event) => setFile(event.target.files?.[0] ?? null)}
                      />
                    </label>
                    <label className="account-field">
                      <span>来源 source_record_id</span>
                      <input value={sourceId} onChange={(event) => setSourceId(event.target.value)} placeholder="已登记来源的 UUID" />
                    </label>
                    <button type="button" className="account-button" disabled={!api || busy || file === null} onClick={() => void submitUpload()}>
                      提交上传
                    </button>
                  </div>
                ) : null}
                {actionError === null ? null : <p className="load-note load-note-error">{actionError}</p>}
                {actionNote === null ? null : <p className="load-note">{actionNote}</p>}
              </Card>
            </>
          ) : null}

          {tab === 'transcript' ? (
            transcript.status === 'ready' ? (
              <TranscriptPage embedded view={transcript.data} />
            ) : (
              <LoadStateNote status={transcript.status} error={transcript.error} />
            )
          ) : null}

          {tab === 'report' ? (
            report.status === 'ready' ? (
              <ReportPage embedded view={report.data} />
            ) : (
              <LoadStateNote status={report.status} error={report.error} />
            )
          ) : null}

        </>
      )}
    </div>
  );
}
