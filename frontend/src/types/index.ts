/* ==================================================================
 * 前端内部数据模型（normalized view model）
 *
 * 说明：后端返回的字段命名在「接口契约」与现有 Go 实现之间存在差异
 * （例如编号字段 client_req_id / request_no、租户状态 active / ACTIVE），
 * 因此所有后端响应统一在 src/api/normalize.ts 内归一化，
 * 页面组件只使用本文件里的类型，不再直接接触后端原始字段名。
 * ================================================================== */

export type Role = 'OPS_ADMIN' | 'TENANT_ADMIN' | 'TENANT_APP';

export type TenantStatus = 'ACTIVE' | 'SUSPENDED' | 'PENDING';

export type RequestStatus =
  | 'PROCESSING'
  | 'SUCCESS'
  | 'FAILED'
  | 'TIMEOUT'
  | 'REJECTED';

export interface Identity {
  role: Role;
  subjectId: string;
  tenantId: string;
  /** 展示名：运营/管理员账号名，或应用凭证名 */
  displayName: string;
  /** 平台运营为空 */
  tenantCode?: string;
  tenantName?: string;
}

export interface TenantCard {
  id: string;
  code: string;
  name: string;
  status: TenantStatus;
  dailyQuota: number;
  initialDailyQuota: number | null;
  success: number | null;
  inFlight: number | null;
  available: number | null;
  credentialCount: number | null;
  credentialLimit: number;
  adminName: string | null;
}

export interface CredentialItem {
  id: string;
  tenantId: string;
  name: string;
  status: 'ACTIVE' | 'SUSPENDED';
  createdAt: string;
  disabledAt: string | null;
  disabledBy: string | null;
  disableReason: string | null;
  replayCount: number;
  /** 仅在本地记住的密钥（后端创建时只返回一次），用于掩码展示 */
  maskedSecret: string | null;
  /** 当日计数（由后端流水归集，前端不做业务判定） */
  todaySuccess: number | null;
  todayInFlight: number | null;
  todayFailed: number | null;
  todayTimeout: number | null;
}

export interface RequestItem {
  id: string;
  clientReqId: string;
  tenantId: string;
  credentialId: string;
  credentialName: string | null;
  model: string;
  prompt: string;
  status: RequestStatus;
  errorReason: string;
  receivedAt: string;
  completedAt: string | null;
  latencyMs: number | null;
  /** 是否计入扣额：按后端终态展示（只有 SUCCESS 实扣），权威计数见 usage.success */
  deducted: boolean;
  replayCount: number;
  upstreamResult: string;
  /** 被拒绝的尝试（后端 rejected_attempts 表） */
  rejected: boolean;
}

export interface UsageSummary {
  limit: number;
  success: number;
  inFlight: number;
  available: number;
}

export interface QuotaChangeItem {
  id: string;
  tenantId: string;
  tenantCode: string;
  oldQuota: number;
  newQuota: number;
  operator: string;
  reason: string;
  effectiveAt: string;
  day: string;
  successAtMoment: number;
  inFlightAtMoment: number;
  isRejected: boolean;
  rejectedReason: string;
}

export interface TenantEventItem {
  id: string;
  tenantId: string;
  kind: string;
  actor: string;
  reason: string;
  detail: string;
  occurredAt: string;
}

export interface DiagnosticItem {
  id: string;
  requestId: string;
  tenantId: string;
  clientReqId: string | null;
  kind: string;
  detail: string;
  recordedAt: string;
}

export interface DailyReportModel {
  tenantId: string;
  day: string;
  counts: Record<RequestStatus, number>;
  usageSuccess: number;
  usageInFlight: number;
  quotaLimit: number;
  remaining: number;
  changes: QuotaChangeItem[];
  events: TenantEventItem[];
  consistent: boolean;
  mismatchNote: string;
}

export interface InvokeResult {
  clientReqId: string;
  status: RequestStatus;
  model: string;
  acceptedAt: string;
  finishedAt: string | null;
  latencyMs: number | null;
  quotaDeducted: boolean;
  errorCode: string;
  errorMessage: string;
  idempotentHit: boolean;
  usage: UsageSummary | null;
  upstreamResult: string;
}
