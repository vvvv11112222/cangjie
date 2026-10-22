/**
 * 详情路由解析。
 *
 * 评审意见 F09/F10：地址栏是用户可编辑的输入，`decodeURIComponent` 遇到畸形百分号编码会抛
 * URIError——如果直接在 render 里解码，整个 React 根节点会卸载，连导航都没了。
 * 所以这里把"解码 + UUID 校验 + 规范化"收成一个纯函数，失败时返回受控的 invalid 结果。
 */

import { isUuid, normalizeId } from './ids';

const SESSION_DETAIL = /^#\/sessions\/([^/]+)$/;
const SESSION_LIST = '#/sessions';

export type SessionRoute =
  | { kind: 'list' }
  | { kind: 'detail'; sessionId: string }
  | { kind: 'invalid'; message: string };

export function parseSessionRoute(hash: string): SessionRoute {
  const match = SESSION_DETAIL.exec(hash.split('?')[0] ?? '');
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

export type SessionTab = 'run' | 'live' | 'transcript' | 'report' | 'playback' | 'visual';
export function routeQuery(hash = typeof window === 'undefined' ? '' : window.location.hash): URLSearchParams {
  return new URLSearchParams(hash.includes('?') ? hash.slice(hash.indexOf('?') + 1) : '');
}
export function sessionDetailHash(id: string, options: { tab?: SessionTab; mediaId?: string; upload?: boolean } = {}): string {
  const params = new URLSearchParams();
  if (options.tab) params.set('tab', options.tab);
  if (options.mediaId) params.set('media', options.mediaId);
  if (options.upload) params.set('start', 'upload');
  return `#/sessions/${encodeURIComponent(id)}${params.size ? `?${params}` : ''}`;
}
export function sessionIntent(hash = typeof window === 'undefined' ? '' : window.location.hash): { tab: SessionTab; mediaId: string | null; upload: boolean } {
  const query = routeQuery(hash);
  const raw = query.get('media');
  const tab = query.get('tab') as SessionTab | null;
  return { tab: tab !== null && ['run','live','transcript','report','playback','visual'].includes(tab) ? tab : 'run', mediaId: raw !== null && isUuid(raw) ? normalizeId(raw) : null, upload: query.get('start') === 'upload' };
}

/**
 * 顶栏搜索只把查询词带到课堂档案，不把它当成服务端查询参数。
 * 列表接口当前没有搜索字段，因此 SessionListPage 仍只筛选已经读取的课堂。
 */
export function sessionSearchFromHash(hash: string): string {
  if (hash !== SESSION_LIST && !hash.startsWith(`${SESSION_LIST}?`)) return '';
  const query = hash.slice(SESSION_LIST.length + 1);
  return new URLSearchParams(query).get('query')?.trim() ?? '';
}

export function sessionSearchHash(query: string): string {
  const normalized = query.trim();
  if (normalized === '') return SESSION_LIST;
  const params = new URLSearchParams({ query: normalized });
  return `${SESSION_LIST}?${params.toString()}`;
}
