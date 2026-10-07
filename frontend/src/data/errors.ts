/**
 * 统一错误模型。
 *
 * 为什么需要：Go 返回的错误是 {error:{code,message,details}}，其中 code 是机器可读的
 * 判定依据（见 docs/开发协议.md 第 1.2 节），message 只是给人看的英文调试信息。
 * 页面不能把英文 message 直接摊给用户，也不能靠 message 文本猜错误类型，
 * 所以这里把 code 映射成中文提示，页面只跟 ApiError 打交道。
 */

export type ApiErrorCode =
  // 协议第 1.2 节列出的错误码
  | 'INVALID_ARGUMENT'
  | 'UNAUTHENTICATED'
  | 'FORBIDDEN'
  | 'SOURCE_NOT_VERIFIED'
  | 'EXTERNAL_PROCESSING_FORBIDDEN'
  | 'NOT_FOUND'
  | 'SCHEDULE_CONFLICT'
  | 'IDEMPOTENCY_CONFLICT'
  | 'REVISION_CONFLICT'
  | 'LEASE_LOST'
  | 'COMPLETION_CONFLICT'
  | 'INVALID_STATE'
  | 'RESOURCE_EXPIRED'
  | 'UPLOAD_TOO_LARGE'
  | 'UNSUPPORTED_MEDIA'
  | 'RANGE_NOT_SATISFIABLE'
  | 'TIMELINE_INVALID'
  | 'INVALID_RESULT'
  | 'EVIDENCE_UNAVAILABLE'
  | 'TOO_MANY_REQUESTS'
  | 'SERVICE_UNAVAILABLE'
  // 前端自己产生的两类：请求根本没到服务端，或响应不符合协议
  | 'NETWORK_ERROR'
  | 'MALFORMED_RESPONSE';

/** 服务端 details 是开放的 map，不参与页面判断，只在排查时展示。 */
export type ErrorDetails = Record<string, unknown>;

export class ApiError extends Error {
  readonly code: ApiErrorCode;
  readonly status: number;
  readonly details: ErrorDetails;
  readonly requestId: string | null;

  constructor(options: {
    code: ApiErrorCode;
    status: number;
    details?: ErrorDetails;
    requestId?: string | null;
    message?: string;
  }) {
    super(options.message ?? options.code);
    this.name = 'ApiError';
    this.code = options.code;
    this.status = options.status;
    this.details = options.details ?? {};
    this.requestId = options.requestId ?? null;
  }
}

/**
 * 错误码 → 页面提示。文案只解释"发生了什么、我该做什么"，不复述英文原文。
 * 未收录的码统一走 errorCodeText 的兜底分支，不会让页面显示空白。
 */
const CODE_TEXT: Partial<Record<ApiErrorCode, string>> = {
  INVALID_ARGUMENT: '提交的内容不符合要求，请检查筛选条件或输入后再试。',
  UNAUTHENTICATED: '登录状态已失效，请重新登录。',
  FORBIDDEN: '当前账号没有执行该操作的权限。',
  SOURCE_NOT_VERIFIED: '该录像来源尚未核验，暂时不能提交分析。',
  EXTERNAL_PROCESSING_FORBIDDEN: '该来源未获准用于外部模型处理。',
  NOT_FOUND: '请求的数据不存在，或超出了当前账号的可见范围。',
  SCHEDULE_CONFLICT: '该时间段已有排课，请更换教室或时间。',
  IDEMPOTENCY_CONFLICT: '同一次提交的重复请求带了不同内容，服务端拒绝了本次请求。',
  REVISION_CONFLICT: '内容已被他人修改，请刷新后基于最新版本重新提交。',
  LEASE_LOST: '任务租约已失效，处理已被服务端中止。',
  COMPLETION_CONFLICT: '同一任务提交了不同的完成内容，服务端拒绝了本次提交。',
  INVALID_STATE: '当前状态不允许该操作，请刷新后重试。',
  RESOURCE_EXPIRED: '资源已过期，无法继续读取或导出。',
  UPLOAD_TOO_LARGE: '上传文件超过大小限制。',
  UNSUPPORTED_MEDIA: '不支持的媒体格式。',
  RANGE_NOT_SATISFIABLE: '请求的播放区间无效。',
  TIMELINE_INVALID: '时间轴无法映射到原录像，处理被拒绝。',
  INVALID_RESULT: '结果与任务或来源关联不一致，服务端拒绝了本次提交。',
  EVIDENCE_UNAVAILABLE: '引用的证据已不可用。',
  TOO_MANY_REQUESTS: '请求过于频繁，请稍后再试。',
  SERVICE_UNAVAILABLE: '服务暂时不可用，请稍后再试。',
  NETWORK_ERROR: '连接不上服务端，请确认接口服务已启动、地址正确后重试。',
  MALFORMED_RESPONSE: '返回的数据不符合协议，无法解析。',
};

export function errorCodeText(code: string): string {
  const known = CODE_TEXT[code as ApiErrorCode];
  return known ?? `服务端返回了未收录的错误码 ${code}。`;
}

/**
 * 把任意异常整理成一行可以直接显示的中文提示。
 * 附带错误码和 HTTP 状态，方便截图反馈给后端，但不暴露服务端英文原文作为唯一信息。
 */
export function describeError(error: unknown): string {
  if (error instanceof ApiError) {
    // status 0 表示错误由前端本地产生（例如"当前账号没有可见批次"），
    // message 已经是写给用户看的中文说明，直接使用。
    const localText =
      error.status === 0 && error.code !== 'NETWORK_ERROR' && error.code !== 'MALFORMED_RESPONSE';
    if (localText && error.message !== '') {
      return error.message;
    }
    const suffix = error.status > 0 ? `，HTTP ${error.status}` : '';
    // 协议不一致时把守卫给出的字段位置带上，方便直接定位是哪个 DTO 漂移。
    const detail =
      error.code === 'MALFORMED_RESPONSE' && error.message !== '' ? `：${error.message}` : '';
    return `${errorCodeText(error.code)}（错误码 ${error.code}${suffix}）${detail}`;
  }
  if (error instanceof Error) {
    return `前端读取数据时出错：${error.message}`;
  }
  return '读取数据时发生未知错误。';
}
