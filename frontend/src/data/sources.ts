/**
 * 数据访问层：页面只认这里的五个 getXxx，不直接 import 样例文件，也不自己拼 fetch。
 *
 * 为什么需要开关：M0 骨架默认读取 contracts/examples 的固定样例，保证离线可跑；
 * M1 联调时把构建变量 VITE_DATA_SOURCE 设为 api，同样五个函数改成调用 /api/v1。
 * 两种实现对页面是同一个接口，所以页面代码不再关心数据从哪来。
 *
 * 需要注意：/sessions、/analysis-runs、/results、/transcript-revisions、/reports
 * 这些接口后端尚未实现，api 模式下会返回 NOT_FOUND，页面会显示可读提示而不是假数据。
 */

import type {
  Media,
  MediaAction,
  MediaKind,
  MediaStatus,
  Report,
  Results,
  Revision,
  Run,
  SessionPage,
  SessionStatus,
} from '../types';
import { ApiError } from './errors';
import { asArray, asEnum, asNullableString, asRecord, asString, enumList } from './guards';
import {
  MEDIA_ACTIONS,
  MEDIA_KINDS,
  MEDIA_STATUSES,
  fixtureMediaFor,
  fixtures,
  parseMedia,
  parseReport,
  parseResults,
  parseRevision,
  parseRun,
  parseSessionPage,
} from './samples';
import { requestJson } from './http';

/** Media 的枚举常量与类型；取值见 contracts/v1.schema.json 的 Media。 */
export { MEDIA_ACTIONS, MEDIA_KINDS, MEDIA_STATUSES };
export type { MediaAction, MediaKind, MediaStatus } from '../types';

/**
 * 媒体列表里挑原媒体需要的字段。
 * allowed_actions 必须保留：评审意见 N05 指出，不能用 Session 的授权覆盖所选媒体自己的用途
 * （Session 允许 analyze、但该媒体只允许 playback 时，不能启用"提交分析"）。
 * 完整的 Media DTO 校验等接入媒体详情页时再补，这里不做多余字段的假设。
 */
export interface MediaLite {
  source_record_id?: string | null;
  original_filename?: string | null;
  created_at?: string;
  id: string;
  kind: MediaKind;
  status: MediaStatus;
  allowed_actions: MediaAction[];
}

/** MediaPage 的一页；游标要保留并继续消费（评审意见 N04）。 */
export interface MediaPageLite {
  items: MediaLite[];
  nextCursor: string | null;
}

export type DataSourceKind = 'fixtures' | 'api';

/** 展示用的来源标签：样例模式说"固定样例"，接口模式说"接口数据"。 */
export const DATA_SOURCE_LABEL: Record<DataSourceKind, string> = {
  fixtures: '固定样例',
  api: '接口数据',
};

/** GET /sessions 的可选筛选，字段与 contracts/endpoints.json 的 SessionQuery 一致。 */
export interface SessionQuery {
  college_id?: string;
  enrollment_year?: number;
  class_group_id?: string;
  teacher_id?: string;
  from?: string;
  to?: string;
  status?: SessionStatus;
  cursor?: string;
  limit?: number;
}

export interface DataSource {
  readonly kind: DataSourceKind;
  getSessions(query?: SessionQuery): Promise<SessionPage>;
  /** GET /sessions/{id}/media；cursor 为空表示第一页（协议第 1 节：游标不透明）。 */
  getSessionMedia(sessionId: string, cursor?: string): Promise<MediaPageLite>;
  /** GET /media/{id}；播放地址、有效期与 allowed_actions 都以这条响应为准。 */
  getMedia(id: string): Promise<Media>;
  getRun(id: string): Promise<Run>;
  /** 结果挂在批次下：GET /analysis-runs/{id}/results。 */
  getResults(runId: string): Promise<Results>;
  getRevision(id: string): Promise<Revision>;
  getReport(id: string): Promise<Report>;
}

function notFound(path: string, id: string): ApiError {
  return new ApiError({
    code: 'NOT_FOUND',
    status: 0,
    message: `${path} 中没有 id=${id} 的数据`,
  });
}

function malformed(error: unknown, status = 200): ApiError {
  return new ApiError({
    code: 'MALFORMED_RESPONSE',
    status,
    message: error instanceof Error ? error.message : '响应不符合协议',
  });
}

/** 解析 MediaPage；保留 next_cursor 供继续读取（评审意见 N04）。 */
function parseMediaPage(value: unknown): MediaPageLite {
  const page = asRecord(value, 'MediaPage');
  const nextCursor = asNullableString(page, 'next_cursor', 'MediaPage');
  const items = asArray(page['items'], 'MediaPage.items').map((item, index) => {
    const path = `MediaPage.items[${index}]`;
    const media = asRecord(item, path);
    return {
      source_record_id: media['source_record_id'] === undefined ? undefined : asNullableString(media, 'source_record_id', path),
      original_filename: media['original_filename'] === undefined ? undefined : asNullableString(media, 'original_filename', path),
      created_at: media['created_at'] === undefined ? undefined : asString(media, 'created_at', path),
      id: asString(media, 'id', path),
      kind: asEnum(media, 'kind', path, MEDIA_KINDS),
      status: asEnum(media, 'status', path, MEDIA_STATUSES),
      allowed_actions: enumList(media, 'allowed_actions', path, MEDIA_ACTIONS),
    };
  });
  return { items, nextCursor };
}

/**
 * 样例实现：只认得 contracts/examples 里那几份固定样例，按 id 匹配。
 * 它存在的意义是让页面在无后端时也走完全相同的调用路径。
 */
export const fixtureSource: DataSource = {
  kind: 'fixtures',
  async getSessions(): Promise<SessionPage> {
    return fixtures.sessionPage;
  },
  async getSessionMedia(): Promise<MediaPageLite> {
    // 固定样例里没有媒体列表；样例模式不使用媒体选择器。
    return { items: [], nextCursor: null };
  },
  async getMedia(id: string): Promise<Media> {
    // 固定样例模式改为播放仓库内的合成样例视频（见 samples.ts 的 FIXTURE_PLAYBACK_SRC）；
    // 除这条合成媒体外的 id 都超出样例范围，按 NOT_FOUND 处理，不编造媒体。
    const media = fixtureMediaFor(id);
    if (media === null) {
      throw notFound('playbackMedia', id);
    }
    return media;
  },
  async getRun(id: string): Promise<Run> {
    if (id === fixtures.run.id) {
      return fixtures.run;
    }
    throw notFound('run-partial.json', id);
  },
  async getResults(runId: string): Promise<Results> {
    if (runId === fixtures.run.id) {
      return fixtures.results;
    }
    throw notFound('results.json', runId);
  },
  async getRevision(id: string): Promise<Revision> {
    // results.json 引用的修订与 revision.json 的 id 不一致（样例是彼此独立的合成场景），
    // 两个 id 都返回同一份修订样例，差别由页面的“样例数据核对”如实列出，不在这里掩盖。
    if (id === fixtures.revision.id || id === fixtures.results.transcript_revision_id) {
      return fixtures.revision;
    }
    throw notFound('revision.json', id);
  },
  async getReport(id: string): Promise<Report> {
    if (id === fixtures.report.id) {
      return fixtures.report;
    }
    throw notFound('report-draft.json', id);
  },
};

/** 接口实现：路径与响应 DTO 以 contracts/endpoints.json + contracts/v1.schema.json 为准。 */
export const apiSource: DataSource = {
  kind: 'api',
  async getSessions(query: SessionQuery = {}): Promise<SessionPage> {
    const { data } = await requestJson<unknown>('/sessions', { query: { ...query } });
    try {
      return parseSessionPage(data);
    } catch (error) {
      throw malformed(error);
    }
  },
  async getSessionMedia(sessionId: string, cursor?: string): Promise<MediaPageLite> {
    const { data } = await requestJson<unknown>(`/sessions/${encodeURIComponent(sessionId)}/media`, {
      query: { cursor },
    });
    try {
      return parseMediaPage(data);
    } catch (error) {
      throw malformed(error);
    }
  },
  async getMedia(id: string): Promise<Media> {
    const { data } = await requestJson<unknown>(`/media/${encodeURIComponent(id)}`);
    try {
      return parseMedia(data);
    } catch (error) {
      throw malformed(error);
    }
  },
  async getRun(id: string): Promise<Run> {
    const { data } = await requestJson<unknown>(`/analysis-runs/${encodeURIComponent(id)}`);
    try {
      return parseRun(data);
    } catch (error) {
      throw malformed(error);
    }
  },
  async getResults(runId: string): Promise<Results> {
    const { data } = await requestJson<unknown>(`/analysis-runs/${encodeURIComponent(runId)}/results`);
    try {
      return parseResults(data);
    } catch (error) {
      throw malformed(error);
    }
  },
  async getRevision(id: string): Promise<Revision> {
    const { data } = await requestJson<unknown>(`/transcript-revisions/${encodeURIComponent(id)}`);
    try {
      return parseRevision(data);
    } catch (error) {
      throw malformed(error);
    }
  },
  async getReport(id: string): Promise<Report> {
    const { data } = await requestJson<unknown>(`/reports/${encodeURIComponent(id)}`);
    try {
      return parseReport(data);
    } catch (error) {
      throw malformed(error);
    }
  },
};

/**
 * 开关：VITE_DATA_SOURCE=api 时走接口，其它情况（未设置、写错、空值）一律回落到固定样例，
 * 保证克隆仓库、没起后端时页面仍能打开。
 */
export function resolveDataSourceKind(raw: unknown): DataSourceKind {
  return raw === 'api' ? 'api' : 'fixtures';
}

export const DATA_SOURCE_KIND: DataSourceKind = resolveDataSourceKind(import.meta.env.VITE_DATA_SOURCE);

/** 当前生效的实现，由开关决定；下面的函数是数据访问层对外的入口。 */
const selected: DataSource = DATA_SOURCE_KIND === 'api' ? apiSource : fixtureSource;

export const dataLabel: string = DATA_SOURCE_LABEL[DATA_SOURCE_KIND];

export function isApiMode(): boolean {
  return DATA_SOURCE_KIND === 'api';
}

export const getSessions = (query?: SessionQuery): Promise<SessionPage> => selected.getSessions(query);
export const getSessionMedia = (sessionId: string, cursor?: string): Promise<MediaPageLite> =>
  selected.getSessionMedia(sessionId, cursor);
export const getMedia = (id: string): Promise<Media> => selected.getMedia(id);
export const getRun = (id: string): Promise<Run> => selected.getRun(id);
export const getResults = (runId: string): Promise<Results> => selected.getResults(runId);
export const getRevision = (id: string): Promise<Revision> => selected.getRevision(id);
export const getReport = (id: string): Promise<Report> => selected.getReport(id);

/**
 * 把入口打包成可注入的数据源，供 loaders 组合调用；测试可以换成指定实现。
 * 它和上面的函数是同一份逻辑，不额外分支。
 */
export const dataSource: DataSource = {
  kind: DATA_SOURCE_KIND,
  getSessions,
  getSessionMedia,
  getMedia,
  getRun,
  getResults,
  getRevision,
  getReport,
};
