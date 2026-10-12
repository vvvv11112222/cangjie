/**
 * 详情路由解析。
 *
 * 评审意见 F09/F10：地址栏是用户可编辑的输入，`decodeURIComponent` 遇到畸形百分号编码会抛
 * URIError——如果直接在 render 里解码，整个 React 根节点会卸载，连导航都没了。
 * 所以这里把"解码 + UUID 校验 + 规范化"收成一个纯函数，失败时返回受控的 invalid 结果。
 */

import { isUuid, normalizeId } from './ids';

const SESSION_DETAIL = /^#\/sessions\/([^/]+)$/;

export type SessionRoute =
  | { kind: 'list' }
  | { kind: 'detail'; sessionId: string }
  | { kind: 'invalid'; message: string };

export function parseSessionRoute(hash: string): SessionRoute {
  const match = SESSION_DETAIL.exec(hash);
  if (match === null) {
    return { kind: 'list' };
  }
  let decoded: string;
  try {
    decoded = decodeURIComponent(match[1] ?? '');
  } catch {
    return { kind: 'invalid', message: '课堂地址的 URL 编码不完整，请从课堂列表重新进入。' };
  }
  if (!isUuid(decoded)) {
    return { kind: 'invalid', message: '课堂地址里的 ID 不是合法的 UUID，请从课堂列表重新进入。' };
  }
  // UUID 按值比较：地址里写成大写也要能命中同一个课堂（F10）。
  return { kind: 'detail', sessionId: normalizeId(decoded) };
}
