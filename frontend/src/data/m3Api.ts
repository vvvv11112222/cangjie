import type {
  CreateRevision,
  ExportFormat,
  PatchReport,
  PublishReport,
  Report,
  ReportExport,
  ReportPage,
  Revision,
  RevisionPage,
  ReviewReport,
} from '../types';
import { ApiError } from './errors';
import { asEnum, asNullableString, asRecord, asString, objectList } from './guards';
import { requestJson } from './http';
import { parseReport, parseRevision } from './samples';

function malformed(error: unknown): ApiError {
  return new ApiError({
    code: 'MALFORMED_RESPONSE',
    status: 200,
    message: error instanceof Error ? error.message : '响应不符合协议',
  });
}

function report(value: unknown): Report {
  try { return parseReport(value); } catch (error) { throw malformed(error); }
}

function revision(value: unknown): Revision {
  try { return parseRevision(value); } catch (error) { throw malformed(error); }
}

export async function listRevisions(sessionId: string, mediaAssetId: string, cursor?: string): Promise<RevisionPage> {
  const { data } = await requestJson<unknown>(`/sessions/${encodeURIComponent(sessionId)}/transcript-revisions`, {
    query: { media_asset_id: mediaAssetId, cursor },
  });
  try {
    const page = asRecord(data, 'RevisionPage');
    return {
      items: objectList(page, 'items', 'RevisionPage').map((item) => revision(item)),
      next_cursor: asNullableString(page, 'next_cursor', 'RevisionPage'),
    };
  } catch (error) { throw malformed(error); }
}

export async function createRevision(sessionId: string, input: CreateRevision): Promise<Revision> {
  const { data } = await requestJson<unknown>(`/sessions/${encodeURIComponent(sessionId)}/transcript-revisions`, {
    method: 'POST', body: input, csrf: true,
  });
  return revision(data);
}

export async function listReports(sessionId: string, cursor?: string): Promise<ReportPage> {
  const { data } = await requestJson<unknown>(`/sessions/${encodeURIComponent(sessionId)}/reports`, { query: { cursor } });
  try {
    const page = asRecord(data, 'ReportPage');
    return {
      items: objectList(page, 'items', 'ReportPage').map((item) => report(item)),
      next_cursor: asNullableString(page, 'next_cursor', 'ReportPage'),
    };
  } catch (error) { throw malformed(error); }
}

export async function copyReport(reportId: string, lockVersion: number, reason: string): Promise<Report> {
  const { data } = await requestJson<unknown>(`/reports/${encodeURIComponent(reportId)}/revisions`, {
    method: 'POST', body: { lock_version: lockVersion, reason }, csrf: true,
  });
  return report(data);
}

export async function patchReport(reportId: string, input: PatchReport): Promise<Report> {
  const { data } = await requestJson<unknown>(`/reports/${encodeURIComponent(reportId)}`, {
    method: 'PATCH', body: input, csrf: true,
  });
  return report(data);
}

export async function reviewReport(reportId: string, input: ReviewReport): Promise<Report> {
  const { data } = await requestJson<unknown>(`/reports/${encodeURIComponent(reportId)}/review`, {
    method: 'POST', body: input, csrf: true,
  });
  return report(data);
}

export async function publishReport(reportId: string, input: PublishReport): Promise<Report> {
  const { data } = await requestJson<unknown>(`/reports/${encodeURIComponent(reportId)}/publish`, {
    method: 'POST', body: input, csrf: true,
  });
  return report(data);
}

export async function withdrawReport(reportId: string, lockVersion: number, reason: string): Promise<Report> {
  const { data } = await requestJson<unknown>(`/reports/${encodeURIComponent(reportId)}/withdraw`, {
    method: 'POST', body: { lock_version: lockVersion, reason }, csrf: true,
  });
  return report(data);
}

const EXPORT_STATUSES = ['queued', 'running', 'succeeded', 'failed', 'expired'] as const;
function parseExport(value: unknown): ReportExport {
  try {
    const item = asRecord(value, 'Export');
    return {
      id: asString(item, 'id', 'Export'), report_id: asString(item, 'report_id', 'Export'),
      status: asEnum(item, 'status', 'Export', EXPORT_STATUSES),
      error_code: asNullableString(item, 'error_code', 'Export'),
      download_url: asNullableString(item, 'download_url', 'Export'),
      expires_at: asString(item, 'expires_at', 'Export'),
    };
  } catch (error) { throw malformed(error); }
}

export async function createExport(reportId: string, format: ExportFormat, idempotencyKey: string): Promise<ReportExport> {
  const { data } = await requestJson<unknown>(`/reports/${encodeURIComponent(reportId)}/exports`, {
    method: 'POST', body: { format }, csrf: true, idempotencyKey,
  });
  return parseExport(data);
}

export async function getExport(exportId: string): Promise<ReportExport> {
  const { data } = await requestJson<unknown>(`/exports/${encodeURIComponent(exportId)}`);
  return parseExport(data);
}
