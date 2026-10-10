/**
 * 前端动作入口：提交分析、取消批次、上传媒体。
 *
 * 为什么单独放一个文件：读操作走 DataSource 的 getXxx，但写操作（create/cancel/upload）
 * 不读样例、也不在离线模式下可用，它们只对真实 Go 服务有意义。把它们和"取数"分开，
 * 页面就能在样例模式下明确禁用这些按钮，而不是发一个必然失败的请求。
 *
 * 协议依据：docs/开发协议.md 第 2 节。
 *   - POST /sessions/{id}/analysis-runs，请求体 CreateRun，要 Idempotency-Key；
 *   - POST /analysis-runs/{id}/cancel，请求体 {}；
 *   - POST /sessions/{id}/media，multipart（file + source_record_id）。
 * 前端只发送请求并展示结果，能否执行由服务端逐次授权。
 */

import { ApiError } from './errors';
import { requestFormData, requestJson } from './http';
import { parseRun } from './samples';
import type { CreateRun, Run } from '../types';
import { asRecord, asString } from './guards';

/** 每次"提交分析/重试"都用新键：同键重放会命中幂等，重复点击才是真正的新请求。 */
export function newIdempotencyKey(): string {
  return crypto.randomUUID();
}

function asRun(data: unknown, status: number): Run {
  try {
    return parseRun(data);
  } catch (error) {
    throw new ApiError({
      code: 'MALFORMED_RESPONSE',
      status,
      message: error instanceof Error ? error.message : '响应不符合协议',
    });
  }
}

/** 提交一次分析（full / media_prepare / report_only）。 */
export async function createRun(sessionId: string, input: CreateRun): Promise<Run> {
  const { data } = await requestJson<unknown>(`/sessions/${encodeURIComponent(sessionId)}/analysis-runs`, {
    method: 'POST',
    body: input,
    csrf: true,
    idempotencyKey: newIdempotencyKey(),
  });
  return asRun(data, 202);
}

/** 取消一个尚未进入终态的批次。终态时服务端会返回 INVALID_STATE。 */
export async function cancelRun(runId: string): Promise<Run> {
  const { data } = await requestJson<unknown>(`/analysis-runs/${encodeURIComponent(runId)}/cancel`, {
    method: 'POST',
    body: {},
    csrf: true,
  });
  return asRun(data, 200);
}

/**
 * 上传录像。source_record_id 是已登记来源的 id；M1 前端还没有来源登记界面，
 * 这个值由使用者在弹窗里填写（协议要求，前端不自己编造来源）。
 * 响应只取 Media.id 做回执，完整 Media DTO 的校验留到接入媒体详情页时补。
 */
export async function uploadMedia(
  sessionId: string,
  file: File,
  sourceRecordId: string,
): Promise<{ mediaId: string }> {
  const form = new FormData();
  form.append('file', file);
  form.append('source_record_id', sourceRecordId);
  const { data } = await requestFormData<unknown>(`/sessions/${encodeURIComponent(sessionId)}/media`, form, {
    csrf: true,
  });
  try {
    return { mediaId: asString(asRecord(data, 'Media'), 'id', 'Media') };
  } catch (error) {
    throw new ApiError({
      code: 'MALFORMED_RESPONSE',
      status: 201,
      message: error instanceof Error ? error.message : '响应不符合协议',
    });
  }
}
