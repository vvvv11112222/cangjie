/**
 * 账号能力（/auth/me 的 allowed_actions）与资源动作（各 DTO 的 allowed_actions）的对应关系。
 *
 * ⚠ 这张表是【前端展示约定】，协议里没有定义两套动作如何对应：
 *   - 账号级能力只有 8 个：manage_academic / manage_users / upload / analyze /
 *     review / publish / operate / delete（见 contracts/v1.schema.json 的 Me）；
 *   - 资源级动作是 Session / Run / Report 各自的 allowed_actions。
 * 后端角色与能力的现有对应（internal/identity/service.go 的 AllowedActions）是：
 *   教师 = upload + analyze；督导 = review + publish；学院教务 = manage_academic + manage_users；
 *   系统管理员 = 全部。表的取值按这个对应关系推断。
 *
 * 因此：这是需要团队确认的假设。改动只发生在本文件一处，确认后按结论替换即可。
 * 只为"隐藏前端展示"服务，不代替任何服务端授权（协议第 1.1 节：后端逐次授权）。
 */

import type { AccountAction } from './authApi';
import type { ReportAction, RunAction, SessionAction } from '../types';

/** null 表示"查看类动作"，只要能看到该资源就允许展示。 */
type Capability = AccountAction | null;

export const SESSION_ACTION_CAPABILITY: Record<SessionAction, Capability> = {
  upload: 'upload',
  prepare_media: 'analyze',
  analyze: 'analyze',
  edit_transcript: 'review',
  view_results: null,
  view_report: null,
  archive: 'delete',
  delete: 'delete',
};

export const RUN_ACTION_CAPABILITY: Record<RunAction, Capability> = {
  view_results: null,
  cancel: 'analyze',
  retry: 'analyze',
};

export const REPORT_ACTION_CAPABILITY: Record<ReportAction, Capability> = {
  edit: 'review',
  submit: 'review',
  review: 'review',
  confirm_report: 'review',
  publish: 'publish',
  withdraw: 'publish',
  revise: 'review',
  export: 'publish',
};

/**
 * capabilities 为 null 表示"拿不到账号能力"（固定样例模式，或还没登录）：
 * 此时不做过滤，页面展示与 M0 完全一致，避免离线时把内容显示成空的。
 */
export function canShowAction(capability: Capability, capabilities: readonly AccountAction[] | null): boolean {
  if (capabilities === null) {
    return true;
  }
  if (capability === null) {
    return true;
  }
  return capabilities.includes(capability);
}

export function filterSessionActions(
  actions: readonly SessionAction[],
  capabilities: readonly AccountAction[] | null,
): SessionAction[] {
  return actions.filter((action) => canShowAction(SESSION_ACTION_CAPABILITY[action], capabilities));
}

export function filterRunActions(
  actions: readonly RunAction[],
  capabilities: readonly AccountAction[] | null,
): RunAction[] {
  return actions.filter((action) => canShowAction(RUN_ACTION_CAPABILITY[action], capabilities));
}

export function filterReportActions(
  actions: readonly ReportAction[],
  capabilities: readonly AccountAction[] | null,
): ReportAction[] {
  return actions.filter((action) => canShowAction(REPORT_ACTION_CAPABILITY[action], capabilities));
}
