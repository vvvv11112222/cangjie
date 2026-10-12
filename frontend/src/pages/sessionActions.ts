/**
 * 课堂详情页的动作状态（纯逻辑，方便测试）。
 *
 * 评审意见 F01：动作状态、上传文件和回执都属于"某个登录会话 + 某个课堂"。
 * 账号 A 点提交后退出、再登录账号 B 时，A 的迟到响应不能写进 B 的页面。
 * 所以状态里带一个 scope 标识（auth sessionKey + sessionId），
 * 渲染时用 actionStateFor 过滤，写回时用 writeActionState 校验归属。
 *
 * 评审意见 F03/N02：同一次逻辑提交要复用同一个幂等键。键由 pages/pendingIntent.ts
 * 按"认证会话 + 课堂"保存（跨组件卸载仍有效），本模块只负责"选哪个媒体、按什么规则算"。
 */

import type { CreateRun, Run } from '../types';

/**
 * 处理目标的选择意图（评审意见 N06）：
 *   auto    —— 未显式选择：优先刚上传的媒体，其次课堂主媒体；
 *   primary —— 明确选择"使用课堂当前主媒体"；
 *   asset   —— 明确选择某个媒体。
 * 把 primary 与"没有选择"分开编码，否则选主媒体后仍会去分析刚上传的录像。
 */
export type MediaChoice =
  | { kind: 'auto' }
  | { kind: 'primary' }
  | { kind: 'asset'; mediaId: string };

export interface ActionState {
  /** 归属标识：`${auth sessionKey}:${sessionId}`。 */
  scope: string;
  busy: boolean;
  error: string | null;
  note: string | null;
  /** 动作或轮询得到、要优先展示的批次。 */
  run: Run | null;
  /** 上传成功后拿到的媒体 id：首次处理用它，不再死等主媒体（F02）。 */
  uploadedMediaId: string | null;
  /** 处理目标的选择意图（见 MediaChoice）。 */
  mediaChoice: MediaChoice;
  uploadOpen: boolean;
  sourceRecordId: string;
  file: File | null;
}

export function emptyActionState(scope: string): ActionState {
  return {
    scope,
    busy: false,
    error: null,
    note: null,
    run: null,
    uploadedMediaId: null,
    mediaChoice: { kind: 'auto' },
    uploadOpen: false,
    sourceRecordId: '',
    file: null,
  };
}

/** 渲染用：状态不属于当前会话时立即按空状态呈现。 */
export function actionStateFor(state: ActionState, scope: string): ActionState {
  return state.scope === scope ? state : emptyActionState(scope);
}

/**
 * 写回：只有"写入方所属会话"仍是当前状态时才允许更新。
 * 会话已经切换时原样返回，等于丢弃上一个账号的迟到结果。
 */
export function writeActionState(
  state: ActionState,
  fromScope: string,
  update: (base: ActionState) => ActionState,
): ActionState {
  return state.scope === fromScope ? update(state) : state;
}

/** 固定字段顺序及 UUID 大小写，按完整 CreateRun 区分重发与不同请求。 */
export function intentSignature(sessionId: string, input: CreateRun): string {
  const normalized: CreateRun = {
    media_asset_id: input.media_asset_id.toLowerCase(),
    mode: input.mode,
    input_transcript_revision_id: input.input_transcript_revision_id?.toLowerCase() ?? null,
    config_profile: input.config_profile,
  };
  return JSON.stringify([sessionId.toLowerCase(), normalized]);
}

/** 当前要处理的媒体：按选择意图决定（N06）。 */
export function processingMediaId(state: ActionState, sessionPrimaryMediaId: string | null): string | null {
  switch (state.mediaChoice.kind) {
    case 'asset':
      return state.mediaChoice.mediaId;
    case 'primary':
      // 明确选了主媒体：没有主媒体就不能退回刚上传的媒体。
      return sessionPrimaryMediaId;
    default:
      return state.uploadedMediaId ?? sessionPrimaryMediaId;
  }
}
