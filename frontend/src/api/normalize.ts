/* ==================================================================
 * 响应归一化：把后端原始报文转换成页面使用的统一视图模型。
 *
 * 归一化只做「字段改名 + 状态词映射 + 缺省值兜底」，
 * 不做任何业务判定（是否扣额、是否越权等一律以后端为准）。
 * ================================================================== */

import type {
  CredentialItem,
  DailyReportModel,
  DiagnosticItem,
  InvokeResult,
  QuotaChangeItem,
  RequestItem,
  RequestStatus,
  TenantCard,
  TenantEventItem,
  TenantStatus,
  UsageSummary
} from '@/types';

type Raw = Record<string, any>;

const asRecord = (value: unknown): Raw =>
  value && typeof value === 'object' ? (value as Raw) : {};

const num = (value: unknown, fallback: number | null = null): number | null => {
  if (value === null || value === undefined || value === '') return fallback;
  const n = Number(value);
  return Number.isFinite(n) ? n : fallback;
};

const str = (value: unknown, fallback = ''): string =>
  typeof value === 'string' ? value : value === null || value === undefined ? fallback : String(value);

/* --------------------------- 状态词映射 --------------------------- */

const TENANT_STATUS: Record<string, TenantStatus> = {
  active: 'ACTIVE',
  ACTIVE: 'ACTIVE',
  enabled: 'ACTIVE',
  disabled: 'SUSPENDED',
  suspended: 'SUSPENDED',
  SUSPENDED: 'SUSPENDED',
  pending: 'PENDING',
  PENDING: 'PENDING'
};

const REQUEST_STATUS: Record<string, RequestStatus> = {
  processing: 'PROCESSING',
  PROCESSING: 'PROCESSING',
  success: 'SUCCESS',
  SUCCESS: 'SUCCESS',
  failed: 'FAILED',
  FAILED: 'FAILED',
  timeout: 'TIMEOUT',
  TIMEOUT: 'TIMEOUT',
  rejected: 'REJECTED',
  REJECTED: 'REJECTED'
};

export const tenantStatusLabel = (s: TenantStatus): string =>
  s === 'ACTIVE' ? 'ACTIVE' : s === 'SUSPENDED' ? 'SUSPENDED' : 'PENDING';

export const requestStatusLabel = (s: RequestStatus): string => s;

/* ------------------------------ 租户 ------------------------------ */

export function normalizeTenant(raw: unknown): TenantCard {
  const r = asRecord(raw);
  return {
    id: str(r.id ?? r.tenant_id),
    code: str(r.code ?? r.tenant_code ?? r.id ?? r.tenant_id),
    name: str(r.name ?? r.tenant_name ?? r.id),
    status: TENANT_STATUS[str(r.status).toLowerCase()] ?? 'PENDING',
    dailyQuota: num(r.daily_quota ?? r.dailyQuota ?? r.current_daily_quota, 0) ?? 0,
    initialDailyQuota: num(r.initial_daily_quota ?? r.initialDailyQuota),
    success: num(r.success),
    inFlight: num(r.in_flight ?? r.inFlight ?? r.processing),
    available: num(r.available ?? r.remaining),
    credentialCount: num(r.credential_count ?? r.credentialCount),
    credentialLimit: num(r.credential_limit ?? r.credentialLimit, 2) ?? 2,
    adminName: r.admin_name ? str(r.admin_name) : null
  };
}

/* ------------------------------ 用量 ------------------------------ */

export function normalizeUsage(raw: unknown): UsageSummary {
  const r = asRecord(asRecord(raw).usage ?? raw);
  const limit = num(r.quota_limit ?? r.limit, 0) ?? 0;
  const success = num(r.success ?? r.success_count, 0) ?? 0;
  const inFlight = num(r.in_flight ?? r.inflight_count ?? r.processing, 0) ?? 0;
  const available =
    num(r.available) ?? num(r.remaining) ?? Math.max(0, limit - (success + inFlight));
  return { limit, success, inFlight, available };
}

/* ------------------------------ 凭证 ------------------------------ */

export function normalizeCredential(raw: unknown, maskedSecret?: string | null): CredentialItem {
  const r = asRecord(raw);
  const status = str(r.status).toLowerCase();
  return {
    id: str(r.id),
    tenantId: str(r.tenant_id),
    name: str(r.name),
    status: status === 'disabled' || status === 'suspended' ? 'SUSPENDED' : 'ACTIVE',
    createdAt: str(r.created_at),
    disabledAt: r.disabled_at ? str(r.disabled_at) : null,
    disabledBy: r.disabled_by ? str(r.disabled_by) : null,
    disableReason: r.disable_reason ? str(r.disable_reason) : null,
    replayCount: num(r.replay_count, 0) ?? 0,
    maskedSecret: maskedSecret ?? null,
    todaySuccess: null,
    todayInFlight: null,
    todayFailed: null,
    todayTimeout: null
  };
}

/* ------------------------------ 流水 ------------------------------ */

export function normalizeRequest(raw: unknown, opts: { rejected?: boolean } = {}): RequestItem {
  const r = asRecord(raw);
  const statusKey = str(r.status).toLowerCase();
  const status = REQUEST_STATUS[statusKey] ?? 'REJECTED';
  const received = str(r.received_at ?? r.accepted_at ?? r.occurred_at);
  const completed = r.completed_at ?? r.finished_at;
  let latency = num(r.latency_ms);
  if (latency === null && received && completed) {
    const diff = new Date(str(completed)).getTime() - new Date(received).getTime();
    latency = Number.isFinite(diff) ? diff : null;
  }
  return {
    id: str(r.id ?? r.request_no ?? r.client_req_id),
    clientReqId: str(r.request_no ?? r.client_req_id),
    tenantId: str(r.tenant_id),
    credentialId: str(r.credential_id),
    credentialName: r.credential_name ? str(r.credential_name) : null,
    model: str(r.model),
    prompt: str(r.prompt),
    status,
    errorReason: str(r.error_reason ?? r.error_code ?? r.reason),
    receivedAt: received,
    completedAt: completed ? str(completed) : null,
    latencyMs: latency,
    deducted: status === 'SUCCESS',
    replayCount: num(r.replay_count, 0) ?? 0,
    upstreamResult: str(r.upstream_result),
    rejected: opts.rejected ?? false
  };
}

/* --------------------------- 额度变更 --------------------------- */

export function normalizeQuotaChange(raw: unknown, tenantCode = ''): QuotaChangeItem {
  const r = asRecord(raw);
  return {
    id: str(r.id),
    tenantId: str(r.tenant_id),
    tenantCode: str(r.tenant_code ?? tenantCode ?? r.tenant_id),
    oldQuota: num(r.old_quota ?? r.previous_quota, 0) ?? 0,
    newQuota: num(r.new_quota, 0) ?? 0,
    operator: str(r.operator),
    reason: str(r.reason),
    effectiveAt: str(r.effective_at),
    day: str(r.day),
    successAtMoment: num(r.success_count_at_moment, 0) ?? 0,
    inFlightAtMoment: num(r.in_flight_count_at_moment, 0) ?? 0,
    isRejected: Boolean(r.is_rejected_attempt),
    rejectedReason: str(r.rejected_reason)
  };
}

/* --------------------------- 停用事件 --------------------------- */

export function normalizeTenantEvent(raw: unknown): TenantEventItem {
  const r = asRecord(raw);
  return {
    id: str(r.id),
    tenantId: str(r.tenant_id),
    kind: str(r.kind),
    actor: str(r.actor),
    reason: str(r.reason),
    detail: str(r.detail),
    occurredAt: str(r.occurred_at)
  };
}

/* ---------------------------- 诊断记录 ---------------------------- */

export function normalizeDiagnostic(raw: unknown, clientReqId?: string | null): DiagnosticItem {
  const r = asRecord(raw);
  return {
    id: str(r.id),
    requestId: str(r.request_id),
    tenantId: str(r.tenant_id),
    clientReqId: clientReqId ?? (r.client_req_id ? str(r.client_req_id) : null),
    kind: str(r.kind),
    detail: str(r.detail),
    recordedAt: str(r.recorded_at)
  };
}

/* ---------------------------- 日终报表 ---------------------------- */

export function normalizeDailyReport(raw: unknown): DailyReportModel {
  const r = asRecord(asRecord(raw).report ?? raw);
  const countsRaw = asRecord(r.counts);
  const countOf = (key: string): number =>
    num(countsRaw[key] ?? r[key] ?? r[`${key}_count`], 0) ?? 0;
  return {
    tenantId: str(r.tenant_id),
    day: str(r.day),
    counts: {
      SUCCESS: countOf('success'),
      FAILED: countOf('failed'),
      TIMEOUT: countOf('timeout'),
      PROCESSING: countOf('processing'),
      REJECTED: countOf('rejected')
    },
    usageSuccess: num(r.usage_success, 0) ?? 0,
    usageInFlight: num(r.usage_in_flight, 0) ?? 0,
    quotaLimit: num(r.quota_limit, 0) ?? 0,
    remaining: num(r.remaining, 0) ?? 0,
    changes: Array.isArray(r.quota_changes)
      ? r.quota_changes.map((c: unknown) => normalizeQuotaChange(c))
      : [],
    events: Array.isArray(r.events) ? r.events.map((e: unknown) => normalizeTenantEvent(e)) : [],
    consistent: Boolean(r.consistent ?? r.is_consistent),
    mismatchNote: str(r.mismatch_note)
  };
}

/* ---------------------------- 调用结果 ---------------------------- */

export function normalizeInvokeResult(raw: unknown): InvokeResult {
  const r = asRecord(raw);
  const request = asRecord(r.request);
  const status = REQUEST_STATUS[str(request.status).toLowerCase()] ?? 'REJECTED';
  const acceptedAt = str(request.received_at ?? request.accepted_at);
  const finishedAt = request.completed_at ?? request.finished_at;
  let latencyMs = num(request.latency_ms);
  if (latencyMs === null && acceptedAt && finishedAt) {
    const diff = new Date(str(finishedAt)).getTime() - new Date(acceptedAt).getTime();
    latencyMs = Number.isFinite(diff) ? diff : null;
  }
  const usageRaw = r.usage;
  return {
    clientReqId: str(request.request_no ?? request.client_req_id),
    status,
    model: str(request.model),
    acceptedAt,
    finishedAt: finishedAt ? str(finishedAt) : null,
    latencyMs,
    quotaDeducted: status === 'SUCCESS',
    errorCode: str(request.error_reason ?? request.error_code),
    errorMessage: str(request.error_message ?? request.error_reason),
    idempotentHit: Boolean(r.idempotent_hit ?? r.replayed),
    usage: usageRaw ? normalizeUsage(usageRaw) : null,
    upstreamResult: str(request.upstream_result)
  };
}
