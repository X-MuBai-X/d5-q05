/* ==================================================================
 * 接口端点集合
 *
 * 语义一律以 `docs/19-前后端接口契约.md` 为准；
 * 与现有 Go 实现存在字面差异的位置（路径 / 字段名 / 查询参数）通过
 * `callVariants` 的「线格式变体」兼容，两种写法都能工作。
 *
 * 变体顺序：先放当前 Go 实现的写法，后放契约文档的写法。
 * 原因见 src/api/compat.ts 顶部说明。
 * ================================================================== */

import { apiRequest, setToken } from './client';
import { callVariants, type WireVariant } from './compat';
import {
  normalizeCredential,
  normalizeDailyReport,
  normalizeDiagnostic,
  normalizeInvokeResult,
  normalizeQuotaChange,
  normalizeRequest,
  normalizeTenant,
  normalizeUsage
} from './normalize';
import type {
  CredentialItem,
  DailyReportModel,
  DiagnosticItem,
  Identity,
  InvokeResult,
  QuotaChangeItem,
  RequestItem,
  Role,
  TenantCard,
  UsageSummary
} from '@/types';

/* ---------------------------- 身份归一 ---------------------------- */

function normalizeRole(raw: unknown, subjectType: unknown): Role {
  const role = String(raw ?? '').toUpperCase();
  if (role === 'OPS_ADMIN' || role === 'OPS') return 'OPS_ADMIN';
  if (role === 'TENANT_ADMIN' || role === 'TENANT_ADMIN'.toUpperCase()) return 'TENANT_ADMIN';
  if (role === 'TENANT_APP' || role === 'CREDENTIAL') return 'TENANT_APP';
  if (String(subjectType ?? '').toLowerCase() === 'credential') return 'TENANT_APP';
  return 'TENANT_ADMIN';
}

export function normalizeIdentity(raw: unknown): Identity {
  const r = (raw ?? {}) as Record<string, any>;
  return {
    role: normalizeRole(r.role, r.subject_type),
    subjectId: String(r.subject_id ?? r.subjectId ?? ''),
    tenantId: String(r.tenant_id ?? r.tenantId ?? ''),
    displayName: String(r.display_name ?? r.username ?? r.subject_id ?? '未命名账号'),
    tenantCode: r.tenant_code ? String(r.tenant_code) : undefined,
    tenantName: r.tenant_name ? String(r.tenant_name) : undefined
  };
}

/* ------------------------------ 认证 ------------------------------ */

export interface LoginResult {
  token: string;
  identity: Identity;
}

export async function loginAdmin(username: string, password: string): Promise<LoginResult> {
  const payload = await apiRequest<any>('/api/v1/auth/login', {
    method: 'POST',
    body: { username, password },
    auth: false
  });
  const token = String(payload.token ?? '');
  setToken(token);
  return { token, identity: normalizeIdentity(payload.identity) };
}

export async function loginCredential(secret: string): Promise<LoginResult> {
  const payload = await apiRequest<any>('/api/v1/auth/login', {
    method: 'POST',
    body: { secret },
    auth: false
  });
  const token = String(payload.token ?? '');
  setToken(token);
  return { token, identity: normalizeIdentity(payload.identity) };
}

export async function whoami(): Promise<Identity> {
  const payload = await apiRequest<any>('/api/v1/auth/whoami');
  return normalizeIdentity(payload);
}

export async function logout(): Promise<void> {
  try {
    await apiRequest('/api/v1/auth/logout', { method: 'POST' });
  } finally {
    setToken(null);
  }
}

/* ------------------------------ 运营 ------------------------------ */

export async function listTenants(): Promise<TenantCard[]> {
  const payload = await apiRequest<any>('/api/v1/ops/tenants');
  const list = Array.isArray(payload?.tenants) ? payload.tenants : [];
  return list.map(normalizeTenant);
}

export interface OpenTenantInput {
  tenantId?: string;
  name: string;
  dailyQuota: number;
  reason: string;
}

export async function openTenant(input: OpenTenantInput): Promise<TenantCard> {
  const contractBody: Record<string, unknown> = {
    name: input.name,
    daily_quota: input.dailyQuota,
    reason: input.reason
  };
  if (input.tenantId) contractBody.tenant_id = input.tenantId;

  const implBody: Record<string, unknown> = {
    name: input.name,
    daily_quota: input.dailyQuota,
    reason: input.reason
  };
  if (input.tenantId) implBody.tenant_id = input.tenantId;

  const variants: WireVariant[] = [
    {
      label: '实现：POST /api/v1/ops/tenants/open',
      path: '/api/v1/ops/tenants/open',
      method: 'POST',
      body: implBody,
      accept: p => Boolean((p as any)?.tenant)
    },
    {
      label: '契约：POST /api/v1/ops/tenants',
      path: '/api/v1/ops/tenants',
      method: 'POST',
      body: contractBody,
      accept: p => Boolean((p as any)?.tenant)
    }
  ];
  const payload = await callVariants<any>(variants);
  return normalizeTenant(payload.tenant);
}

export async function adjustQuota(input: {
  tenantId: string;
  dailyQuota: number;
  reason: string;
}): Promise<QuotaChangeItem> {
  const payload = await callVariants<any>([
    {
      label: '实现/契约一致：POST /api/v1/ops/tenants/quota',
      path: '/api/v1/ops/tenants/quota',
      method: 'POST',
      body: {
        tenant_id: input.tenantId,
        daily_quota: input.dailyQuota,
        reason: input.reason
      },
      accept: p => Boolean((p as any)?.change)
    }
  ]);
  return normalizeQuotaChange(payload.change);
}

export async function disableTenant(input: { tenantId: string; reason: string }): Promise<void> {
  await callVariants<any>([
    {
      label: '实现/契约一致：POST /api/v1/ops/tenants/disable',
      path: '/api/v1/ops/tenants/disable',
      method: 'POST',
      body: { tenant_id: input.tenantId, reason: input.reason },
      accept: p => Boolean((p as any)?.status)
    }
  ]);
}

export async function fetchDailyReport(input: {
  tenantId: string;
  day: string;
}): Promise<DailyReportModel> {
  const payload = await callVariants<any>([
    {
      label: '实现：GET /api/v1/ops/report/daily?day=',
      path: '/api/v1/ops/report/daily',
      method: 'GET',
      query: { tenant_id: input.tenantId, day: input.day, date: input.day },
      accept: p => Boolean((p as any)?.report)
    }
  ]);
  return normalizeDailyReport(payload);
}

export async function fetchDiagnostics(input: {
  tenantId: string;
}): Promise<DiagnosticItem[]> {
  const payload = await callVariants<any>([
    {
      label: '实现/契约一致：GET /api/v1/ops/diagnostics',
      path: '/api/v1/ops/diagnostics',
      method: 'GET',
      query: { tenant_id: input.tenantId },
      accept: p => Array.isArray((p as any)?.diagnostics)
    }
  ]);
  return (payload.diagnostics as unknown[]).map(d => normalizeDiagnostic(d));
}

export async function fetchQuotaChanges(input: {
  tenantId: string;
  day?: string;
}): Promise<QuotaChangeItem[]> {
  const payload = await callVariants<any>([
    {
      label: '实现/契约一致：GET /api/v1/ops/quota-changes',
      path: '/api/v1/ops/quota-changes',
      method: 'GET',
      query: { tenant_id: input.tenantId, day: input.day, date: input.day },
      accept: p => Array.isArray((p as any)?.changes)
    }
  ]);
  return (payload.changes as unknown[]).map(c => normalizeQuotaChange(c));
}

export async function setClockOffset(offsetSeconds: number): Promise<{ offsetSeconds: number; now: string }> {
  const payload = await callVariants<any>([
    {
      label: '实现/契约一致：POST /api/v1/ops/clock',
      path: '/api/v1/ops/clock',
      method: 'POST',
      body: { offset_seconds: offsetSeconds },
      accept: p => p !== null && p !== undefined
    }
  ]);
  return {
    offsetSeconds: Number(payload?.offset_seconds ?? offsetSeconds),
    now: String(payload?.now ?? '')
  };
}

/* --------------------------- 客户管理员 --------------------------- */

export async function listCredentials(input: { tenantId?: string } = {}): Promise<CredentialItem[]> {
  const payload = await callVariants<any>([
    {
      label: '实现/契约一致：GET /api/v1/admin/credentials',
      path: '/api/v1/admin/credentials',
      method: 'GET',
      query: { tenant_id: input.tenantId },
      accept: p => Array.isArray((p as any)?.credentials)
    }
  ]);
  return (payload.credentials as unknown[]).map(c => normalizeCredential(c));
}

export async function createCredential(input: {
  name: string;
}): Promise<{ credential: CredentialItem; secret: string }> {
  const payload = await callVariants<any>([
    {
      label: '实现/契约一致：POST /api/v1/admin/credentials/create',
      path: '/api/v1/admin/credentials/create',
      method: 'POST',
      body: { name: input.name },
      accept: p => Boolean((p as any)?.credential)
    }
  ]);
  return {
    credential: normalizeCredential(payload.credential),
    secret: String(payload.secret ?? '')
  };
}

export async function disableCredential(input: {
  credentialId: string;
  reason: string;
}): Promise<void> {
  await callVariants<any>([
    {
      label: '实现/契约一致：POST /api/v1/admin/credentials/disable',
      path: '/api/v1/admin/credentials/disable',
      method: 'POST',
      body: { credential_id: input.credentialId, reason: input.reason },
      accept: p => Boolean((p as any)?.status)
    }
  ]);
}

export async function fetchTenantUsage(input: {
  tenantId?: string;
  day?: string;
}): Promise<UsageSummary> {
  const payload = await callVariants<any>([
    {
      label: '实现/契约一致：GET /api/v1/admin/usage',
      path: '/api/v1/admin/usage',
      method: 'GET',
      query: { tenant_id: input.tenantId, day: input.day, date: input.day },
      accept: p => Boolean((p as any)?.usage)
    }
  ]);
  return normalizeUsage(payload);
}

export async function fetchTenantRequests(input: {
  tenantId?: string;
  day?: string;
}): Promise<RequestItem[]> {
  const payload = await callVariants<any>([
    {
      label: '实现/契约一致：GET /api/v1/admin/requests',
      path: '/api/v1/admin/requests',
      method: 'GET',
      query: { tenant_id: input.tenantId, day: input.day, date: input.day },
      accept: p => Array.isArray((p as any)?.requests)
    }
  ]);
  const accepted = (payload.requests as unknown[]).map(r => normalizeRequest(r));
  const rejected = Array.isArray(payload.rejected)
    ? (payload.rejected as unknown[]).map(r => normalizeRequest(r, { rejected: true }))
    : [];
  return [...accepted, ...rejected];
}

/* ----------------------------- 客户应用 ----------------------------- */

export async function appInvoke(input: {
  clientReqId: string;
  model: string;
  prompt: string;
}): Promise<InvokeResult> {
  const variants: WireVariant[] = [
    {
      label: '实现：POST /api/v1/app/invoke（字段 request_no）',
      path: '/api/v1/app/invoke',
      method: 'POST',
      body: { request_no: input.clientReqId, model: input.model, prompt: input.prompt },
      accept: p => Boolean((p as any)?.request)
    },
    {
      label: '契约：POST /api/v1/app/invoke（字段 client_req_id）',
      path: '/api/v1/app/invoke',
      method: 'POST',
      body: { client_req_id: input.clientReqId, model: input.model, prompt: input.prompt },
      accept: p => Boolean((p as any)?.request)
    }
  ];
  const payload = await callVariants<any>(variants);
  return normalizeInvokeResult(payload);
}

export async function fetchAppRequests(input: { day?: string } = {}): Promise<RequestItem[]> {
  const payload = await callVariants<any>([
    {
      label: '实现/契约一致：GET /api/v1/app/requests',
      path: '/api/v1/app/requests',
      method: 'GET',
      query: { day: input.day, date: input.day },
      accept: p => Array.isArray((p as any)?.requests)
    }
  ]);
  return (payload.requests as unknown[]).map(r => normalizeRequest(r));
}

export async function fetchAppRequest(input: {
  clientReqId: string;
}): Promise<{ request: RequestItem; diagnostics: DiagnosticItem[] }> {
  const variants: WireVariant[] = [
    {
      label: '实现：GET /api/v1/app/request?request_no=',
      path: '/api/v1/app/request',
      method: 'GET',
      query: { request_no: input.clientReqId },
      accept: p => Boolean((p as any)?.request)
    },
    {
      label: '契约：GET /api/v1/app/request?client_req_id=',
      path: '/api/v1/app/request',
      method: 'GET',
      query: { client_req_id: input.clientReqId },
      accept: p => Boolean((p as any)?.request)
    }
  ];
  const payload = await callVariants<any>(variants);
  const diagnostics = Array.isArray(payload.diagnostics)
    ? (payload.diagnostics as unknown[]).map(d => normalizeDiagnostic(d))
    : [];
  return { request: normalizeRequest(payload.request), diagnostics };
}
