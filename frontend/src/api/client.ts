/* ==================================================================
 * 统一 API 客户端
 *
 * 约定（见 docs/19-前后端接口契约.md）：
 *   - 所有请求自动携带 `Authorization: Bearer <token>`，令牌存 localStorage；
 *   - 非 2xx 统一抛出 ApiError，携带 `code`（程序判断依据）与 `message`（面向人）；
 *   - 前端**不做任何业务判定**：额度是否够、能否越权、能否创建第 3 个凭证
 *     一律由后端裁决，前端只负责把后端返回的 code / message 展示出来。
 * ================================================================== */

export const TOKEN_STORAGE_KEY = 'mq.token';

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly raw: unknown;

  constructor(code: string, message: string, status: number, raw?: unknown) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
    this.raw = raw;
  }
}

export function getToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_STORAGE_KEY);
  } catch {
    return null;
  }
}

export function setToken(token: string | null): void {
  try {
    if (token) localStorage.setItem(TOKEN_STORAGE_KEY, token);
    else localStorage.removeItem(TOKEN_STORAGE_KEY);
  } catch {
    /* 隐私模式下 localStorage 不可用时忽略：请求将不带令牌，由后端返回 401 */
  }
}

export type HttpMethod = 'GET' | 'POST';

export interface RequestOptions {
  method?: HttpMethod;
  body?: unknown;
  query?: Record<string, string | number | undefined | null>;
  /** 是否附带 Bearer 令牌，默认 true */
  auth?: boolean;
  signal?: AbortSignal;
}

/* --------------------------- 错误码归一 --------------------------- */

/**
 * 错误码 → 中文说明。
 *
 * 契约规定 `code` 是程序判断依据；但现有 Go 实现在部分分支只返回
 * `{"error": "中文说明"}`（无 code 字段），因此客户端在缺失 code 时
 * 按「状态码 + 文案特征」回退推导，最终仍然以契约里的 code 形态暴露给页面。
 */
const CODE_HINTS: Array<{ code: string; needles: string[] }> = [
  { code: 'CREDENTIAL_LIMIT_REACHED', needles: ['凭证数量已达上限', '凭证已达上限'] },
  { code: 'TENANT_NAME_REQUIRED', needles: ['租户名称不能为空', '租户名为空'] },
  { code: 'QUOTA_FLOOR_VIOLATION', needles: ['不得低于当日已成功', '降低额度'] },
  { code: 'REASON_REQUIRED', needles: ['原因不能为空'] },
  { code: 'QUOTA_RANGE_INVALID', needles: ['额度取值超出允许范围'] },
  { code: 'TENANT_SCOPE_REQUIRED', needles: ['必须指定 tenant_id', '运营跨租户核查'] },
  { code: 'IDEMPOTENCY_CONFLICT', needles: ['请求编号已被占用', 'IDEMPOTENCY_CONFLICT'] },
  { code: 'KEY_SUSPENDED', needles: ['凭证已停用', 'KEY_SUSPENDED'] },
  { code: 'TENANT_SUSPENDED', needles: ['租户已停用', 'TENANT_SUSPENDED'] },
  { code: 'SCOPE_DENIED', needles: ['无权访问', 'SCOPE_DENIED'] },
  { code: 'QUOTA_EXCEEDED', needles: ['当日额度已用尽', 'QUOTA_EXCEEDED'] },
  { code: 'UPSTREAM_ERROR', needles: ['上游明确失败', 'UPSTREAM_ERROR'] },
  { code: 'GATEWAY_TIMEOUT', needles: ['上游超时', 'GATEWAY_TIMEOUT'] },
  { code: 'INVALID_PARAMS', needles: ['请求编号不能为空', '请求编号格式非法', 'INVALID_PARAMS'] },
  { code: 'KEY_NOT_FOUND', needles: ['身份认证失败', 'KEY_NOT_FOUND'] },
  { code: 'NOT_FOUND', needles: ['对象不存在', 'NOT_FOUND'] }
];

const STATUS_FALLBACK: Record<number, string> = {
  400: 'INVALID_PARAMS',
  401: 'KEY_NOT_FOUND',
  403: 'SCOPE_DENIED',
  404: 'NOT_FOUND',
  409: 'IDEMPOTENCY_CONFLICT',
  429: 'QUOTA_EXCEEDED',
  502: 'UPSTREAM_ERROR',
  504: 'GATEWAY_TIMEOUT',
  500: 'INTERNAL_ERROR'
};

export function deriveCode(status: number, body: unknown): string {
  const record = (body ?? {}) as Record<string, unknown>;
  const explicit = typeof record.code === 'string' ? record.code : '';
  if (explicit) return explicit;

  const text = [record.error, record.message, record.detail]
    .filter(v => typeof v === 'string')
    .join(' | ');

  if (text) {
    for (const hint of CODE_HINTS) {
      if (hint.needles.some(n => text.includes(n))) return hint.code;
    }
  }
  return STATUS_FALLBACK[status] ?? `HTTP_${status}`;
}

export function describeError(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  if (err instanceof Error) return { code: 'CLIENT_ERROR', message: err.message };
  return { code: 'CLIENT_ERROR', message: '未知错误' };
}

/* ------------------------------ 请求 ------------------------------ */

function buildUrl(path: string, query?: RequestOptions['query']): string {
  const url = new URL(path, window.location.origin);
  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value === undefined || value === null || value === '') continue;
      url.searchParams.set(key, String(value));
    }
  }
  return url.pathname + url.search;
}

export async function apiRequest<T = unknown>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, query, auth = true, signal } = options;
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (auth) {
    const token = getToken();
    if (token) headers.Authorization = `Bearer ${token}`;
  }

  let response: Response;
  try {
    response = await fetch(buildUrl(path, query), {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'same-origin',
      signal
    });
  } catch (e) {
    throw new ApiError('NETWORK_ERROR', '无法连接后端服务，请确认后端已启动', 0, e);
  }

  const rawText = await response.text();
  let payload: unknown = undefined;
  if (rawText) {
    try {
      payload = JSON.parse(rawText);
    } catch {
      payload = { error: rawText };
    }
  }

  if (!response.ok) {
    const code = deriveCode(response.status, payload);
    const record = (payload ?? {}) as Record<string, unknown>;
    const message =
      (typeof record.message === 'string' && record.message) ||
      (typeof record.error === 'string' && record.error) ||
      `请求失败（HTTP ${response.status}）`;
    throw new ApiError(code, message, response.status, payload);
  }

  return (payload ?? {}) as T;
}
