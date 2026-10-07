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

import type { Report, Results, Revision, Run, SessionPage, SessionStatus } from '../types';
import { ApiError } from './errors';
import { parseReport, parseResults, parseRevision, parseRun, parseSessionPage, fixtures } from './samples';
import { requestJson } from './http';

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

/**
 * 样例实现：只认得 contracts/examples 里那几份固定样例，按 id 匹配。
 * 它存在的意义是让页面在无后端时也走完全相同的调用路径。
 */
export const fixtureSource: DataSource = {
  kind: 'fixtures',
  async getSessions(): Promise<SessionPage> {
    return fixtures.sessionPage;
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

/** 当前生效的实现，由开关决定；下面五个函数是数据访问层对外的入口。 */
const selected: DataSource = DATA_SOURCE_KIND === 'api' ? apiSource : fixtureSource;

export const dataLabel: string = DATA_SOURCE_LABEL[DATA_SOURCE_KIND];

export function isApiMode(): boolean {
  return DATA_SOURCE_KIND === 'api';
}

export const getSessions = (query?: SessionQuery): Promise<SessionPage> => selected.getSessions(query);
export const getRun = (id: string): Promise<Run> => selected.getRun(id);
export const getResults = (runId: string): Promise<Results> => selected.getResults(runId);
export const getRevision = (id: string): Promise<Revision> => selected.getRevision(id);
export const getReport = (id: string): Promise<Report> => selected.getReport(id);

/**
 * 把五个入口打包成可注入的数据源，供 loaders 组合调用；测试可以换成指定实现。
 * 它和上面的函数是同一份逻辑，不额外分支。
 */
export const dataSource: DataSource = {
  kind: DATA_SOURCE_KIND,
  getSessions,
  getRun,
  getResults,
  getRevision,
  getReport,
};
