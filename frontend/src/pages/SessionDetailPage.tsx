import type { ReactElement } from 'react';
import { useEffect,useRef,useState } from 'react';
import { flushSync } from 'react-dom';
import { useAuth } from '../auth';
import { IconAlert } from '../components/icons';
import { ServiceChecklist } from '../components/ServiceChecklist';
import {
BadgeTag,
Card,
ErrorMessage,
LoadControls,
LoadStateNote,
RunErrorBox,
StageFlow,
TechDetails
} from '../components/ui';
import { cancelRun,createRun,uploadMedia } from '../data/actions';
import { ApiError,describeError } from '../data/errors';
import {
fixtureSession,
useMediaDetail,
useRunPolling,
useSessionDetail,
useSessionMedia,
useSessionReport,
useSessionTranscript,
} from '../data/hooks';
import { isUuid } from '../data/ids';
import { isRunActive } from '../data/loaders';
import { getSource } from '../data/m2Api';
import { pickPlaybackMediaId } from '../data/playback';
import { parseSessionRoute,sessionIntent } from '../data/route';
import { fixtures } from '../data/samples';
import { featureProblem,getServiceCapabilities,useServiceCapabilities } from '../data/serviceCapabilities';
import type { MediaAction } from '../data/sources';
import { isApiMode } from '../data/sources';
import { useSourceRecord } from '../data/useSourceRecord';
import {
formatDateTime,
runModeLabel,
runStatusBadge,
sessionStatusBadge,
shortId
} from '../format';
import type { CreateRun,RunMode } from '../types';
import { DeletionPanel } from './DeletionPanel';
import { LiveCapturePanel } from './LiveCapturePanel';
import { pendingIntent } from './pendingIntent';
import { PlaybackPanel } from './PlaybackPanel';
import { ReportPage } from './ReportPage';
import type { ActionState,MediaChoice } from './sessionActions';
import {
actionStateFor,
emptyActionState,
intentSignature,
processingMediaId,
writeActionState,
} from './sessionActions';
import { TranscriptPage } from './TranscriptPage';
import { UploadSourcePicker } from './UploadSourcePicker';
import { VisualPanel } from './VisualPanel';

type Tab = 'run' | 'live' | 'transcript' | 'report' | 'playback' | 'visual';

const TABS: { key: Tab; label: string }[] = [
  { key: 'run', label: '批次与操作' },
  { key: 'live', label: '实时课堂' },
  { key: 'transcript', label: '转写' },
  { key: 'report', label: '报告' },
  { key: 'playback', label: '回放' },
  { key: 'visual', label: '视觉观察' },
];

function analyzeInput(mediaAssetId: string, mode: RunMode): CreateRun {
  return {
    media_asset_id: mediaAssetId,
    mode,
    input_transcript_revision_id: null,
    config_profile: mode === 'visual_only' ? 'vision-v1' : 'p0-v1',
  };
}

/** 某个模式需要所选媒体具备的用途（与后端 mediaActions 的推导一致，评审意见 N05）。 */
function requiredMediaAction(mode: RunMode): MediaAction {
  return mode === 'media_prepare' ? 'prepare_media' : 'analyze';
}

export function SessionDetailPage({ sessionId }: { sessionId: string }): ReactElement {
  const api = isApiMode();
  const { sessionKey, me } = useAuth();
  /**
   * 动作状态用 sessionKey（含登录代次）做归属；幂等意图用稳定的账号标识，
   * 这样整页刷新后（sessionKey 的代次会重置）仍能找回未确定的意图（N02）。
   */
  const accountScope = me?.user_id ?? sessionKey;
  /**
   * 动作状态归属：auth sessionKey + 课堂 id（评审意见 F01）。
   * 退出、换账号、换课堂都会改变 scope，渲染时立即按空状态呈现。
   */
  const scope = `${sessionKey}:${sessionId}`;
  const activeScope=useRef(scope);activeScope.current=scope;
  useEffect(()=>()=>{activeScope.current="unmounted";},[]);

  const [tab, setTab] = useState<Tab>(()=>sessionIntent().tab);
  const [replayChoice, setReplayChoice] = useState<{ scope: string; mediaId: string } | null>(null);
  const [actions, setActions] = useState<ActionState>(() => emptyActionState(scope));
  const uploadFile = useRef<HTMLInputElement>(null);
  const uploadForm = useRef<HTMLDivElement>(null);
  /**
   * 评审意见 N01：scope 变化时必须**真正重建可写状态**。
   * 只把 actionStateFor 当渲染兜底的话，state 里仍是旧 scope，
   * 之后所有写入都会被 writeActionState 当成"旧会话的迟到结果"丢掉
   * （表现为按钮能点但表单打不开、错误不显示）。
   */
  if (actions.scope !== scope) {
    setActions(emptyActionState(scope));
  }
  const action = actionStateFor(actions, scope);
  const write = (fromScope: string, update: (base: ActionState) => ActionState): void => {
    setActions((current) => writeActionState(current, fromScope, update));
  };

  const detail = useSessionDetail(sessionId, api);
  const fixtureSessionData = api ? null : fixtureSession(sessionId);
  const session = api ? detail.data.session : fixtureSessionData;
  const services = useServiceCapabilities(api && session !== null && tab === 'run');
  useEffect(()=>{
    const applyIntent=()=>{
      const route=parseSessionRoute(window.location.hash);
      if(route.kind!=='detail' || route.sessionId!==sessionId) return;
      const intent=sessionIntent();
      setTab(intent.tab);
      setActions(current=>writeActionState(current,scope,base=>({...base,uploadOpen:intent.upload,mediaChoice:intent.mediaId ? {kind:'asset',mediaId:intent.mediaId} : base.mediaChoice})));
    };
    applyIntent();window.addEventListener('hashchange',applyIntent);
    return ()=>window.removeEventListener('hashchange',applyIntent);
  },[scope,sessionId]);
  const baseRun = api
    ? detail.run
    : fixtureSessionData !== null && fixtureSessionData.latest_run_id === fixtures.run.id
      ? fixtures.run
      : null;

  // 回放标签要拿片段与证据做跳转点，所以和转写标签共用同一份取数。
  const transcript = useSessionTranscript(session, tab === 'transcript' || tab === 'playback');
  const report = useSessionReport(session, baseRun, tab === 'report');
  // 回放要用媒体列表挑出可播放的目标（没有主媒体时退回到列表里的原媒体），因此也在这里加载。
  const media = useSessionMedia(session, api && (tab === 'run' || tab === 'live' || tab === 'playback'), 'source');
  const recentMediaId = baseRun !== null && media.data.items.some(item => item.id === baseRun.media_asset_id && item.kind === 'source') ? baseRun.media_asset_id : null;
  const playbackMediaId = replayChoice?.scope === scope ? replayChoice.mediaId : session === null ? null : pickPlaybackMediaId(session, media.data.items);
  const playback = useMediaDetail(playbackMediaId, tab === 'playback');

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
    const mediaAssetId = processingMediaId(action, session?.primary_media_asset_id ?? null, recentMediaId);
    if (session === null || mediaAssetId === null) {
      write(fromScope, (base) => ({ ...base, error: '该课堂还没有可处理的媒体，请先上传录像。' }));
      return;
    }
    // 按实际发送的完整请求保存意图，离开详情后仍能重发同一份提交。
    const input = analyzeInput(mediaAssetId, mode);
    const signature = intentSignature(session.id, input);
    const key = pendingIntent.keyFor(accountScope, session.id, signature);

    write(fromScope, (base) => ({
      ...base,
      busy: true,
      error: null,
      note: null,
    }));
    try {
      const latest = await getServiceCapabilities();
      if(activeScope.current!==fromScope)return;
      const feature = mode === 'full' ? latest.audio_report : mode === 'visual_only' ? latest.visual_analysis : latest.media_prepare;
      if (!feature.available) { write(fromScope,base=>({...base,busy:false,error:featureProblem(feature)}));services.reload();return; }
      if (mode === 'full') {
        const selected = media.data.items.find(item=>item.id===mediaAssetId) ?? selectedDetail.data.media;
        const source = selected?.source_record_id ? await getSource(selected.source_record_id) : null;
        if (!source || source.session_id !== session.id || source.rights_status !== 'verified' || !source.allowed_uses.includes('analysis') || !source.external_processing_allowed) { write(fromScope,base=>({...base,busy:false,error:'该录像尚未获准云端音频与文本处理。请联系学院教务或系统管理员核验，完成后重新检查来源。'}));return; }
      }
      if(activeScope.current!==fromScope)return;
      const run = await createRun(session.id, input, key);
      if (mode === 'visual_only') setTab('visual');
      // 服务端已受理：清除未确定意图，下一次提交才算新的分析。
      pendingIntent.resolve(accountScope, session.id, key);
      write(fromScope, (base) => ({
        ...base,
        busy: false,
        run,
        note: `已提交，批次 ${shortId(run.id)}，当前状态 ${run.status}。`,
      }));
      if (api) {
        detail.reload();
      }
    } catch (error) {
      // 失败时保留 pendingIntent：重试（含重进详情）会复用同一个键，避免重复分析。
      write(fromScope, (base) => ({ ...base, busy: false, error: error instanceof ApiError && error.code === 'SERVICE_UNAVAILABLE' && typeof error.details.reason === 'string' ? error.details.reason : describeError(error) }));
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

  /**
   * 重试重放"这个批次自己"的提交：媒体、模式、修订都取批次字段，
   * 而不是当前选中的处理目标——否则上传新录像后点重试会去分析错的录像。
   */
  async function submitRetry(): Promise<void> {
    const fromScope = scope;
    if (session === null || shownRun === null) {
      return;
    }
    const input: CreateRun = {
      media_asset_id: shownRun.media_asset_id,
      mode: shownRun.mode,
      input_transcript_revision_id: shownRun.input_transcript_revision_id,
      config_profile: shownRun.mode === 'visual_only' ? 'vision-v1' : 'p0-v1',
    };
    const signature = intentSignature(session.id, input);
    const key = pendingIntent.keyFor(accountScope, session.id, signature);

    write(fromScope, (base) => ({ ...base, busy: true, error: null, note: null }));
    try {
      const latest=await getServiceCapabilities();
      if(activeScope.current!==fromScope)return;
      const feature=shownRun.mode==='report_only' ? latest.report_generation : shownRun.mode==='full' ? latest.audio_report : shownRun.mode==='visual_only' ? latest.visual_analysis : latest.media_prepare;
      if(!feature.available){write(fromScope,base=>({...base,busy:false,error:featureProblem(feature)}));services.reload();return;}
      const run = await createRun(session.id, input, key);
      pendingIntent.resolve(accountScope, session.id, key);
      write(fromScope, (base) => ({
        ...base,
        busy: false,
        run,
        note: `已重试，批次 ${shortId(run.id)}，当前状态 ${run.status}。`,
      }));
      if (api) {
        detail.reload();
      }
    } catch (error) {
      write(fromScope, (base) => ({ ...base, busy: false, error: describeError(error) }));
    }
  }

  function openUpload(): void {
    // Mount the input within this click, so browsers keep the file chooser's user gesture.
    flushSync(() => write(scope, (base) => ({ ...base, uploadOpen: true })));
    uploadForm.current?.scrollIntoView?.({ block: 'center' });
    uploadFile.current?.click();
  }

  async function submitUpload(): Promise<void> {
    const fromScope = scope;
    if (session === null || action.file === null || action.sourceRecordId.trim() === '') {
      write(fromScope, (base) => ({
        ...base,
        error: '请选择录像文件和这节课堂已登记的录像来源。可以在上传区域直接登记来源。',
      }));
      return;
    }
    if (!isUuid(action.sourceRecordId.trim())) {
      write(fromScope, base => ({ ...base, error: '来源编号格式不正确。请选择已登记的录像来源，不能填写“1.2”等自编编号。' }));
      return;
    }
    write(fromScope, (base) => ({ ...base, busy: true, error: null, note: null }));
    try {
      const result = await uploadMedia(session.id, action.file, action.sourceRecordId.trim());
      // 评审意见 F02：上传返回的 pending 媒体要成为接下来的处理目标（N06 的 auto 规则会优先它）。
      write(fromScope, (base) => ({
        ...base,
        busy: false,
        note: '录像已上传，尚未开始分析。请选择下方的分析方式；来源待核验时先联系学院教务或系统管理员处理。',
        uploadedMediaId: result.mediaId,
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

  const mediaTarget = processingMediaId(action, session?.primary_media_asset_id ?? null, recentMediaId);
  const canUpload = session !== null && session.allowed_actions.includes('upload');
  /**
   * 评审意见 N05：Session 的授权不能覆盖所选媒体自己的用途。
   * 目标媒体必须先在已加载列表里拿到 allowed_actions 才允许提交；
   * 拿不到授权信息时按"不放行"处理（fail-closed），并提示可加载更多媒体页。
   */
  const selectedDetail = useMediaDetail(mediaTarget, api && tab === 'run' && mediaTarget !== null && !media.data.items.some(item=>item.id===mediaTarget));
  const targetMedia = mediaTarget === null ? null : media.data.items.find((item) => item.id === mediaTarget) ?? (selectedDetail.status === 'ready' && selectedDetail.data.media?.id === mediaTarget && selectedDetail.data.media.session_id === session?.id ? selectedDetail.data.media : null);
  const targetSource = useSourceRecord(targetMedia?.source_record_id ?? null, api && tab === 'run');
  const targetMediaKnown = targetMedia !== null;
  const mediaAllows = (needed: MediaAction): boolean =>
    targetMedia !== null && targetMedia.allowed_actions.includes(needed);
  // 按钮是否显示看 Session 的 allowed_actions；是否可点还要看所选媒体自己的用途（N05）。
  // 两者分开，避免"因为媒体授权未知"就把按钮整个藏掉，那样用户看不出这里本该有什么操作。
  const showPrepare = session !== null && session.allowed_actions.includes('prepare_media');
  const showAnalyze = session !== null && session.allowed_actions.includes('analyze');
  const mediaReadyForActions = api && !action.busy && mediaTarget !== null;
  const prepareDisabled = !mediaReadyForActions || !mediaAllows('prepare_media') || !services.data?.media_prepare.available;
  const analyzeDisabled = !mediaReadyForActions || !mediaAllows('analyze');
  const fullDisabled = (shownRun !== null && isRunActive(shownRun)) || analyzeDisabled || !services.data?.audio_report.available || !targetSource.data?.external_processing_allowed;
  const visualDisabled = analyzeDisabled || !services.data?.visual_analysis.available || (shownRun !== null && isRunActive(shownRun));
  const canCancel = shownRun !== null && shownRun.allowed_actions.includes('cancel') && isRunActive(shownRun);
  const showRetry = shownRun !== null && shownRun.allowed_actions.includes('retry') && !isRunActive(shownRun);
  // 重试针对批次自己的媒体，授权也按那个媒体判断（不是当前选中的目标）。
  const retryMedia =
    shownRun === null ? null : media.data.items.find((item) => item.id === shownRun.media_asset_id) ?? null;
  const retryDisabled =
    !api || action.busy || retryMedia === null || shownRun === null
      ? true
      : !retryMedia.allowed_actions.includes(requiredMediaAction(shownRun.mode));
  const sourceMedia = session === null ? [] : [...media.data.items.filter((item) => item.kind === 'source'), ...(targetMedia?.kind==='source' && !media.data.items.some(item=>item.id===targetMedia.id) ? [targetMedia] : [])];
  const mediaChoiceValue =
    action.mediaChoice.kind === 'asset'
      ? action.mediaChoice.mediaId
      : action.mediaChoice.kind === 'primary'
        ? '__primary__'
        : '__auto__';

  return (
    <div className="page">
      <header className="page-head">
        <div>

          <a className="text-button" href="#/sessions">← 返回课堂列表</a><h1>{session?.title ?? '课堂详情'}</h1>{session&&<span className="muted small">{formatDateTime(session.planned_start_at)} – {formatDateTime(session.planned_end_at)}</span>}

        </div>
        {session === null ? null : <BadgeTag badge={sessionStatusBadge(session.status)} />}
      </header>


      <LoadStateNote status={detail.status} error={detail.error} />
      <LoadControls actionLabel={detail.actionLabel} loadMore={detail.loadMore} />

      {session === null ? (
        detail.status === 'ready' ? <p className="empty">找不到该课堂，或不在当前账号可见范围。</p> : null
      ) : (
        <>
          <div data-guide="classroom-tabs" className="tab-bar" role="tablist" aria-label="课堂详情">
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
              <Card title="当前课堂与录像" >
                <p className="result-context">处理目标：{targetMedia?.original_filename || (targetMedia ? `历史录像（${formatDateTime(targetMedia.created_at ?? '')}）` : '尚未选择录像')}。{shownRun && <>最近一次分析：{runModeLabel(shownRun.mode)}，{shownRun.created_at ? formatDateTime(shownRun.created_at) : '旧记录未提供提交时间'}。{mediaTarget !== shownRun.media_asset_id && '所选处理目标与这次分析的录像不同，请核对后再提交。'}</>}</p>
                                {/* 协议字段原样保留，只是从业务视线里下沉到折叠区。 */}
                              </Card>

              {detail.runError === null ? null : (
                <Card title="批次读取失败" >
                  <div className="notice-strip">
                    <IconAlert size={14} aria-hidden="true" />
                    <ErrorMessage text={detail.runError}/>
                  </div>
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
                    ? // 评审意见 N08：只有"确实没有批次引用"才是空态；读取失败不能断言不存在。
                      detail.runError === null
                      ? session.latest_run_id === null
                        ? '该课堂还没有分析批次。'
                        : '批次引用存在，但暂时读不到内容，请重试读取。'
                      : '批次读取失败，暂时无法显示。'
                    : `${runModeLabel(shownRun.mode)} · ${runStatusBadge(shownRun.status).label}${
                        isRunActive(shownRun)
                          ? poll.error === null
                            ? ' · 进度自动更新'
                            : ' · 更新暂时中断，显示上次进度'
                          : ''
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
                  detail.runError === null && session.latest_run_id === null ? (
                    <p className="empty">暂无批次。</p>
                  ) : null
                ) : (
                  <>
                    {/* 与课堂任务页共用同一套阶段流程和错误区，避免同一批次两种样子。 */}
                    <StageFlow run={shownRun} />
                    {shownRun.error_code === null ? null : (
                      <RunErrorBox run={shownRun} />
                    )}
                    {shownRun.limitations.length > 0 ? (
                      <TechDetails summary="查看本次分析范围与限制"><p>{shownRun.limitations.join('；')}</p></TechDetails>
                    ) : null}
                  </>
                )}
              </Card>

              <Card title="录像分析" >
                                {api && <ServiceChecklist data={services.data} error={services.error} onReload={()=>{services.reload();targetSource.reload();selectedDetail.reload();media.reload();}}/>}
                {mediaTarget === null ? <p className="onboarding-next">还没有选择录像。请先点击“上传录像”，在同一区域登记或选择来源。</p> : targetSource.data && !mediaAllows('analyze') ? <p className="onboarding-next">{targetSource.data.rights_status !== 'verified' ? '录像来源尚未通过核验。' : !targetSource.data.allowed_uses.includes('analysis') ? '该来源尚未获准分析用途。未申请过该用途时，需要重新登记来源。' : '当前录像状态或当前账号授权暂不允许分析。'}请联系学院教务或系统管理员处理。<a href={`#/sources?session=${session.id}`}>前往这节课堂的来源登记与核验</a></p> : null}
                {targetMedia && !targetSource.data && <div className="field-hint">正在确认录像来源与云端处理许可。{targetSource.error && <>读取失败，请重新检查。<TechDetails><p>{targetSource.error}</p></TechDetails></>}</div>}
                {targetSource.data && !targetSource.data.external_processing_allowed && <p className="field-hint">语音与报告需要额外的云端音频、文本处理许可；本地举手与低头识别可按已获准的分析用途执行。<a href={`#/sources?session=${session.id}`}>查看或核验来源</a></p>}
                {/* 主次分明：提交分析是主操作，上传/媒体准备/重试是次操作，取消批次是危险操作。 */}
                <div className="action-row">
                  {canUpload ? (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={!api || action.busy}
                      aria-expanded={action.uploadOpen}
                      aria-controls="classroom-upload-form" data-guide="upload-recording"
                      onClick={openUpload}
                    >
                      上传录像
                    </button>
                  ) : null}
                  {showPrepare ? (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={prepareDisabled}
                      onClick={() => void submitAnalyze('media_prepare')}
                    >
                      媒体准备
                    </button>
                  ) : null}
                  {showAnalyze ? (
                    <button
                      type="button"
                      className="btn btn-primary"
                      disabled={fullDisabled}
                      data-guide="speech-analyze" onClick={() => void submitAnalyze('full')}
                    >
                      语音转写与教学报告
                    </button>
                  ) : null}
                  {showAnalyze ? <button type="button" className="btn btn-secondary" disabled={visualDisabled} data-guide="visual-analyze" onClick={() => void submitAnalyze('visual_only')}>举手与低头识别</button> : null}
                  {canCancel ? (
                    <button type="button" className="btn btn-danger" disabled={!api || action.busy} onClick={() => void submitCancel()}>
                      取消批次
                    </button>
                  ) : null}
                  {showRetry ? (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={retryDisabled}
                      onClick={() => void submitRetry()}
                    >
                      重试
                    </button>
                  ) : null}
                  {session.allowed_actions.length === 0 ? <span className="muted">该课堂没有可执行的操作。</span> : null}
                </div>

                {api ? (
                  <>
                    {/* 评审意见 N07：媒体列表的失败要显示并可重试，和"正常空列表"分开。 */}
                    <LoadStateNote status={media.status} error={media.error} />
                    <LoadControls actionLabel={media.actionLabel} loadMore={media.loadMore} />
                    {sourceMedia.length > 0 || action.uploadedMediaId !== null || session.primary_media_asset_id !== null ? (
                      <label className="account-field">
                        <span>处理目标媒体</span>
                        <select
                          value={mediaChoiceValue}
                          onChange={(event) => {
                            const value = event.target.value;
                            // 评审意见 N06：显式选主媒体要记成 primary，不能和"未选择"都写成 null。
                            const choice: MediaChoice =
                              value === '__auto__'
                                ? { kind: 'auto' }
                                : value === '__primary__'
                                  ? { kind: 'primary' }
                                  : { kind: 'asset', mediaId: value };
                            write(scope, (base) => ({ ...base, mediaChoice: choice }));
                          }}
                        >
                          <option value="__auto__">自动（刚上传录像 → 主录像 → 最近分析录像）</option>
                          <option value="__primary__">
                            使用课堂当前主媒体{session.primary_media_asset_id === null ? '（当前没有）' : ''}
                          </option>
                          {sourceMedia.map((item) => (
                            <option key={item.id} value={item.id}>
                              {item.original_filename || `历史录像 ${sourceMedia.indexOf(item)+1}`} · {item.created_at ? formatDateTime(item.created_at) : '上传时间未记录'} · {item.status === 'ready' ? '录像就绪' : item.status === 'pending' ? '等待媒体检查' : '暂不可用'}
                            </option>
                          ))}
                        </select>
                      </label>
                    ) : media.status === 'ready' ? (
                      <p className="load-note">{media.data.nextCursor !== null ? '已加载页面尚未找到原录像，请点击“加载更多”继续查找。' : '该课堂还没有可选的原录像，先上传录像。'}</p>
                    ) : null}
                    {mediaTarget === null ? (
                      <p className="load-note">{sourceMedia.length > 0 ? '请在“处理目标媒体”中选择已上传的录像，或上传新的录像。' : '请先上传录像。'}</p>
                    ) : null}
                    {mediaTarget !== null && !targetMediaKnown ? (
                      <p className="load-note">
                        还没拿到该媒体的授权信息（它不在已加载的媒体页里），为避免用课堂授权覆盖媒体用途，
                        暂不能提交；可以先「加载更多」媒体，或改选已列出的媒体。
                      </p>
                    ) : null}
                    {targetMedia !== null && targetMedia.status !== 'ready' ? (
                      <p className="load-note">
                        {targetMedia.status === 'pending' ? '录像等待媒体检查。选择可用分析方式后系统会先准备录像；只需回放时可点击“媒体准备”。' : '录像暂不可处理，请检查有效期或联系管理员。'}
                      </p>
                    ) : null}
                    {showRetry && retryDisabled && retryMedia === null ? (
                      <p className="load-note">
                        重试需要失败批次自己那个媒体的授权信息；可以在媒体列表里继续加载后再重试。
                      </p>
                    ) : null}
                  </>
                ) : null}

                {!api ? <p className="load-note">固定样例模式未接入后端，操作按钮不可用。</p> : null}
                {action.uploadOpen ? (
                  <div className="upload-form" id="classroom-upload-form" ref={uploadForm}>
                                        <label className="account-field">
                      <span>录像文件（必填）</span>
                      <input
                        ref={uploadFile}
                        type="file"
                        onChange={(event) =>
                          write(scope, (base) => ({ ...base, file: event.target.files?.[0] ?? null }))
                        }
                      />
                    </label>
                    <UploadSourcePicker key={scope} sessionId={session.id} value={action.sourceRecordId} onChange={value => write(scope, base => ({ ...base, sourceRecordId: value }))} />
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
                      {action.busy ? '正在上传，请稍候…' : '提交上传'}
                    </button>
                    <button type="button" className="btn btn-secondary" disabled={action.busy} onClick={() => write(scope, base => ({ ...base, uploadOpen: false, file: null }))}>取消上传</button>
                  </div>
                ) : null}
                {action.error === null ? null : <div className="load-note load-note-error"><ErrorMessage text={action.error}/></div>}
                {action.note === null ? null : <p className="load-note">{action.note}</p>}
              </Card>
              {session.allowed_actions.includes('delete') || session.status === 'deleting' || session.status === 'deleted' ? (
                <DeletionPanel session={session} api={api} onSessionChanged={detail.reload} />
              ) : null}
            </>
          ) : null}

          {tab === 'transcript' ? (
            transcript.status === 'ready' ? (
              <TranscriptPage
                embedded
                view={transcript.data}
                session={session}
                onRunCreated={(run) => {
                  write(scope, (base) => ({ ...base, run, note: `报告重跑 ${shortId(run.id)} 已创建。` }));
                  setTab('run');
                }}
              />
            ) : (
              <>
                <LoadStateNote status={transcript.status} error={transcript.error} />
                <LoadControls actionLabel={transcript.actionLabel} loadMore={transcript.loadMore} />
              </>
            )
          ) : null}

          {tab === 'live' ? (
            api ? (
              <LiveCapturePanel
                session={session}
                enabled
                onUseMedia={(mediaId) => {
                  write(scope, (base) => ({ ...base, mediaChoice: { kind: 'asset', mediaId } }));
                  media.reload();
                  setTab('run');
                }}
              />
            ) : (
              <Card title="实时课堂" >
                <p className="empty">固定样例模式不伪造实时画面。切换到接口模式后可创建手机采集。</p>
              </Card>
            )
          ) : null}

          {tab === 'report' ? (
            report.status === 'ready' ? (
              <ReportPage embedded view={report.data} onPublicationChanged={detail.reload} />
            ) : (
              <>
                <LoadStateNote status={report.status} error={report.error} />
                <LoadControls actionLabel={report.actionLabel} loadMore={report.loadMore} />
              </>
            )
          ) : null}

          {tab === 'visual' ? api ? <VisualPanel key={scope} sessionId={session.id} currentRun={shownRun} onReplayMedia={mediaId => { setReplayChoice({ scope, mediaId }); setTab('playback'); }} /> : <Card title="视觉观察"><p className="empty">当前是固定样例模式，请连接团队后端后上传录像进行视觉分析。</p></Card> : null}

          {tab === 'playback' ? (
                <PlaybackPanel key={`${scope}:${playbackMediaId}:${session.latest_run_id}`} mediaId={playbackMediaId} detail={playback} transcript={transcript} mediaList={media} needsMediaList onSelectMedia={mediaId => setReplayChoice({ scope, mediaId })} />
          ) : null}
        </>
      )}
    </div>
  );
}
