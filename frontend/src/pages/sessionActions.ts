/**
 * 课堂详情页的动作状态（纯逻辑，方便测试）。
 *
 * 评审意见 F01：动作状态、上传文件和回执都属于"某个登录会话 + 某个课堂"。
 * 账号 A 点提交后退出、再登录账号 B 时，A 的迟到响应不能写进 B 的页面。
 * 所以状态里带一个 scope 标识（auth sessionKey + sessionId），
 * 渲染时用 actionStateFor 过滤，写回时用 writeActionState 校验归属。
 *
 * 评审意见 F03：同一次逻辑提交要复用同一个幂等键，所以状态里保存
 * intentKey（键）与 intentSignature（提交内容签名）；签名相同=重发，签名不同=新分析。
 */

import type { Run, RunMode } from '../types';

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
  /** 用户选定的处理目标媒体（当前或历史原媒体）。 */
  selectedMediaId: string | null;
  /** 一次逻辑提交持有的幂等键；为 null 表示下次提交是新分析（F03）。 */
  intentKey: string | null;
  /** 幂等键对应的提交内容签名：`sessionId:mode:mediaAssetId`。 */
  intentSignature: string | null;
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
    selectedMediaId: null,
    intentKey: null,
    intentSignature: null,
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

/** 提交内容签名：签名相同表示同一次逻辑提交，重发要复用幂等键。 */
export function intentSignature(sessionId: string, mode: RunMode, mediaAssetId: string): string {
  return `${sessionId}:${mode}:${mediaAssetId}`;
}

/** 当前要处理的媒体：优先用户选定，其次刚上传的，最后才是 Session 的主媒体。 */
export function processingMediaId(state: ActionState, sessionPrimaryMediaId: string | null): string | null {
  return state.selectedMediaId ?? state.uploadedMediaId ?? sessionPrimaryMediaId;
}
