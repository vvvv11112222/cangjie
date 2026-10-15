/**
 * 课堂的写操作：创建课堂、上传课堂录像。
 *
 * 对应协议第 2 节的 POST /sessions 与 POST /sessions/{id}/media（multipart）。
 * 页面只调这里，不自己拼 fetch；能否操作仍由服务端按 allowed_actions 逐次判断，
 * 越权会返回 403/404，页面把服务端错误原文展示出来。
 *
 * 注意 media 上传是 multipart：文件名交给服务端解析，前端不发送本地路径字符串。
 */

import { ApiError } from './errors';
import { asRecord, asString } from './guards';
import { requestFormData, requestJson } from './http';

export interface CreateSessionInput {
  offeringId: string;
  title: string;
  /** 带时区的 RFC3339；页面上用 datetime-local 取值后转成 ISO。 */
  plannedStartAt: string;
  plannedEndAt: string;
  isDemo: boolean;
}

export interface SessionSummary {
  id: string;
  title: string;
  status: string;
}

export interface MediaSummary {
  id: string;
  status: string;
}

function asMalformed(error: unknown): ApiError {
  return new ApiError({
    code: 'MALFORMED_RESPONSE',
    status: 200,
    message: error instanceof Error ? error.message : '响应不符合协议',
  });
}

export async function createSession(input: CreateSessionInput): Promise<SessionSummary> {
  const { data } = await requestJson<unknown>('/sessions', {
    method: 'POST',
    body: {
      offering_id: input.offeringId,
      // 有课表时由课表带出；这一版不选排课，所以显式发 null。
      schedule_entry_id: null,
      title: input.title,
      planned_start_at: input.plannedStartAt,
      planned_end_at: input.plannedEndAt,
      is_demo: input.isDemo,
    },
    csrf: true,
  });
  try {
    const source = asRecord(data, 'Session');
    return {
      id: asString(source, 'id', 'Session'),
      title: asString(source, 'title', 'Session'),
      status: asString(source, 'status', 'Session'),
    };
  } catch (error) {
    throw asMalformed(error);
  }
}

/** 上传原媒体；新媒体的 status 由服务端置为 pending，时长要等 probe 完成后才有。 */
export async function uploadSessionMedia(
  sessionId: string,
  file: File,
  sourceRecordId: string,
): Promise<MediaSummary> {
  const form = new FormData();
  form.append('source_record_id', sourceRecordId);
  form.append('file', file);
  const { data } = await requestFormData<unknown>(
    `/sessions/${encodeURIComponent(sessionId)}/media`,
    form,
    { csrf: true },
  );
  try {
    const source = asRecord(data, 'Media');
    return {
      id: asString(source, 'id', 'Media'),
      status: asString(source, 'status', 'Media'),
    };
  } catch (error) {
    throw asMalformed(error);
  }
}
