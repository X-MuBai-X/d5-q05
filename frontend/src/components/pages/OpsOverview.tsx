import React, { useMemo, useState } from 'react';
import {
  Activity,
  Ban,
  Building2,
  KeyRound,
  Layers,
  Plus,
  RefreshCcw,
  Timer,
  TrendingUp
} from 'lucide-react';
import {
  adjustQuota,
  disableTenant,
  fetchDiagnostics,
  fetchQuotaChanges,
  fetchTenantRequests,
  fetchTenantUsage,
  listCredentials,
  listTenants,
  openTenant,
  setClockOffset
} from '@/api/endpoints';
import { describeError } from '@/api/client';
import { ErrorBanner, type ErrorInfo } from '@/components/common/ErrorBanner';
import {
  AlertStrip,
  Card,
  EmptyHint,
  GhostBtn,
  KpiTile,
  LabeledInput,
  LoadingHint,
  Modal,
  PrimaryBtn,
  Progress,
  StatusChip,
  Td,
  Th
} from '@/components/shell/ui';
import { useAsyncData } from '@/hooks/useAsyncData';
import type {
  CredentialItem,
  DiagnosticItem,
  QuotaChangeItem,
  RequestItem,
  TenantCard,
  UsageSummary
} from '@/types';
import { formatShortTime, formatUtcTime, todayUtcDate } from '@/utils/date';

interface TenantDetail {
  tenant: TenantCard;
  usage: UsageSummary | null;
  credentials: CredentialItem[];
}

interface OpsData {
  details: TenantDetail[];
  changes: QuotaChangeItem[];
  diagnostics: DiagnosticItem[];
  requestIndex: Map<string, string>;
}

/**
 * 图 01 · 运营总控大盘
 *
 * 区块：租户卡片（额度/成功/在途/剩余/状态/凭证 n/2）+ 调额窗口 + 停用租户
 *       + 额度变更流水表（含被拒绝的变更）+ 迟到响应诊断池。
 *
 * 说明：降额底线、停用、凭证上限等**全部由后端裁决**，
 * 前端只把后端返回的 code / message 原样展示。
 */
export const OpsOverview: React.FC = () => {
  const [day, setDay] = useState(todayUtcDate());
  const [actionError, setActionError] = useState<ErrorInfo | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const [quotaTarget, setQuotaTarget] = useState<TenantDetail | null>(null);
  const [newQuota, setNewQuota] = useState('');
  const [quotaReason, setQuotaReason] = useState('');
  const [busy, setBusy] = useState(false);

  const [disableTarget, setDisableTarget] = useState<TenantDetail | null>(null);
  const [disableReason, setDisableReason] = useState('');

  const [openOpen, setOpenOpen] = useState(false);
  const [openName, setOpenName] = useState('');
  const [openQuota, setOpenQuota] = useState('10');
  const [openReason, setOpenReason] = useState('');

  const [clockOffset, setClockOffsetInput] = useState('0');
  const [serverNow, setServerNow] = useState('');

  const { data, error, loading, reload } = useAsyncData<OpsData>(async () => {
    const tenants = await listTenants();

    const details = await Promise.all(
      tenants.map(async tenant => {
        const [usageRes, credRes] = await Promise.allSettled([
          fetchTenantUsage({ tenantId: tenant.id, day }),
          listCredentials({ tenantId: tenant.id })
        ]);
        return {
          tenant,
          usage: usageRes.status === 'fulfilled' ? usageRes.value : null,
          credentials: credRes.status === 'fulfilled' ? credRes.value : []
        };
      })
    );

    const [changeResults, diagResults, requestResults] = await Promise.all([
      Promise.allSettled(tenants.map(t => fetchQuotaChanges({ tenantId: t.id, day }))),
      Promise.allSettled(tenants.map(t => fetchDiagnostics({ tenantId: t.id }))),
      Promise.allSettled(tenants.map(t => fetchTenantRequests({ tenantId: t.id, day })))
    ]);

    const changes = changeResults.flatMap(r =>
      r.status === 'fulfilled' ? r.value : []
    );
    const diagnostics = diagResults.flatMap(r => (r.status === 'fulfilled' ? r.value : []));
    const requests: RequestItem[] = requestResults.flatMap(r =>
      r.status === 'fulfilled' ? r.value : []
    );
    const requestIndex = new Map(requests.map(r => [r.id, r.clientReqId]));

    return { details, changes, diagnostics, requestIndex };
  }, [day]);

  const totals = useMemo(() => {
    const list = data?.details ?? [];
    return {
      tenants: list.length,
      active: list.filter(d => d.tenant.status === 'ACTIVE').length,
      success: list.reduce((acc, d) => acc + (d.usage?.success ?? 0), 0),
      inFlight: list.reduce((acc, d) => acc + (d.usage?.inFlight ?? 0), 0),
      late: data?.diagnostics.length ?? 0
    };
  }, [data]);

  const submitQuota = async () => {
    if (!quotaTarget) return;
    setBusy(true);
    setActionError(null);
    try {
      await adjustQuota({
        tenantId: quotaTarget.tenant.id,
        dailyQuota: Number(newQuota),
        reason: quotaReason
      });
      setQuotaTarget(null);
      setNewQuota('');
      setQuotaReason('');
      setNotice('额度调整已提交（结论以额度变更流水为准）');
      reload();
    } catch (e) {
      setActionError(describeError(e));
    } finally {
      setBusy(false);
    }
  };

  const submitDisable = async () => {
    if (!disableTarget) return;
    setBusy(true);
    setActionError(null);
    try {
      await disableTenant({ tenantId: disableTarget.tenant.id, reason: disableReason });
      setDisableTarget(null);
      setDisableReason('');
      setNotice('停用操作已提交（结论以后端返回为准）');
      reload();
    } catch (e) {
      setActionError(describeError(e));
    } finally {
      setBusy(false);
    }
  };

  const submitOpen = async () => {
    setBusy(true);
    setActionError(null);
    try {
      await openTenant({
        name: openName,
        dailyQuota: Number(openQuota),
        reason: openReason
      });
      setOpenOpen(false);
      setOpenName('');
      setOpenQuota('10');
      setOpenReason('');
      setNotice('租户开通已提交');
      reload();
    } catch (e) {
      setActionError(describeError(e));
    } finally {
      setBusy(false);
    }
  };

  const submitClock = async () => {
    setBusy(true);
    setActionError(null);
    try {
      const r = await setClockOffset(Number(clockOffset));
      setServerNow(r.now);
      setNotice(
        `教学时钟已调整 ${r.offsetSeconds} 秒（配额与日期归属规则不变；时钟前移后旧会话令牌可能失效，必要时重新登录）`
      );
      reload();
    } catch (e) {
      setActionError(describeError(e));
    } finally {
      setBusy(false);
    }
  };

  const floor = quotaTarget?.usage ? quotaTarget.usage.success + quotaTarget.usage.inFlight : 0;

  return (
    <>
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div className="flex items-center gap-3 flex-wrap">
          <span className="text-[13px] text-slate-500">核算基准日（UTC）</span>
          <input
            type="date"
            value={day}
            onChange={e => setDay(e.target.value || todayUtcDate())}
            className="rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-sm text-slate-700 outline-none focus:border-[#2f6b4f]"
          />
          <span className="text-[13px] text-slate-500">教学时钟偏移（秒）</span>
          <input
            type="number"
            value={clockOffset}
            onChange={e => setClockOffsetInput(e.target.value)}
            className="w-[110px] rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-sm text-slate-700 outline-none focus:border-[#2f6b4f]"
          />
          <GhostBtn onClick={submitClock} disabled={busy}>
            调整时钟
          </GhostBtn>
          {serverNow && <span className="text-[11.5px] text-slate-400">后端当前时间 {serverNow}</span>}
        </div>
        <div className="flex items-center gap-2">
          <GhostBtn onClick={reload}>
            <span className="inline-flex items-center gap-1.5">
              <RefreshCcw className="w-3.5 h-3.5" />
              刷新
            </span>
          </GhostBtn>
          <PrimaryBtn onClick={() => setOpenOpen(true)}>
            <span className="inline-flex items-center gap-1.5">
              <Plus className="w-3.5 h-3.5" />
              开通租户
            </span>
          </PrimaryBtn>
        </div>
      </div>

      <ErrorBanner error={error} />
      <ErrorBanner error={actionError} />
      {notice && (
        <AlertStrip tone="emerald" icon={Activity}>
          {notice}
        </AlertStrip>
      )}

      <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-5 gap-4">
        <KpiTile icon={Building2} tone="green" label="租户总数" value={totals.tenants} />
        <KpiTile icon={Activity} tone="blue" label="ACTIVE 租户" value={totals.active} />
        <KpiTile icon={TrendingUp} tone="violet" label="当日总成功" value={totals.success} />
        <KpiTile icon={Layers} tone="amber" label="当日总在途" value={totals.inFlight} />
        <KpiTile icon={Timer} tone="red" label="迟到诊断条数" value={totals.late} />
      </div>

      <Card
        title="租户卡片"
        subtitle="额度 / 成功 / 在途 / 剩余 / 状态 / 凭证 n/2 —— 数值全部来自后端"
        action={
          <span className="text-[11px] text-slate-400">
            剩余可用 = Limit − (成功 + 在途)
          </span>
        }
      >
        {loading && <LoadingHint />}
        {!loading && (data?.details.length ?? 0) === 0 && (
          <EmptyHint>后端未返回任何租户</EmptyHint>
        )}
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
          {(data?.details ?? []).map(d => {
            const u = d.usage;
            const limit = u?.limit ?? d.tenant.dailyQuota;
            const success = u?.success ?? 0;
            const inFlight = u?.inFlight ?? 0;
            const available = u?.available ?? Math.max(0, limit - success - inFlight);
            const credCount = d.credentials.length;
            const credLimit = d.tenant.credentialLimit || 2;
            return (
              <div
                key={d.tenant.id}
                className="rounded-xl border border-slate-200/80 p-4 bg-slate-50/40"
              >
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <div className="text-[14px] font-semibold text-slate-900">
                      租户 {d.tenant.code}
                      {d.tenant.name ? `（${d.tenant.name}）` : ''}
                    </div>
                    <div className="text-[11px] text-slate-400 mt-0.5">
                      租户编号 {d.tenant.id}
                      {d.tenant.adminName ? ` · 管理员 ${d.tenant.adminName}` : ''}
                    </div>
                  </div>
                  <StatusChip status={d.tenant.status} dot />
                </div>

                <div className="mt-3 grid grid-cols-4 gap-2 text-center">
                  {[
                    { k: '日额度', v: limit },
                    { k: '成功', v: success },
                    { k: '在途', v: inFlight },
                    { k: '剩余', v: available }
                  ].map(x => (
                    <div key={x.k} className="rounded-lg bg-white border border-slate-200/70 py-2">
                      <div className="text-[10.5px] text-slate-400">{x.k}</div>
                      <div className="text-[16px] font-semibold text-slate-900">{x.v}</div>
                    </div>
                  ))}
                </div>

                <div className="mt-3">
                  <Progress
                    value={success + inFlight}
                    max={limit}
                    tone={available <= 0 ? 'red' : inFlight > 0 ? 'amber' : 'green'}
                  />
                </div>

                <div className="mt-3 flex items-center justify-between gap-3 flex-wrap">
                  <span className="text-[12px] text-slate-500 inline-flex items-center gap-1.5">
                    <KeyRound className="w-3.5 h-3.5" />
                    凭证 {credCount}/{credLimit}
                  </span>
                  <div className="flex items-center gap-2">
                    <GhostBtn
                      onClick={() => {
                        setQuotaTarget(d);
                        setNewQuota(String(limit));
                        setQuotaReason('');
                      }}
                    >
                      调额
                    </GhostBtn>
                    <GhostBtn
                      onClick={() => {
                        setDisableTarget(d);
                        setDisableReason('');
                      }}
                    >
                      <span className="inline-flex items-center gap-1.5">
                        <Ban className="w-3.5 h-3.5" />
                        停用租户
                      </span>
                    </GhostBtn>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      </Card>

      <Card
        title="额度变更流水（含被拒绝的变更）"
        subtitle="被拒绝的降额同样留痕（is_rejected_attempt），与生效变更同表展示"
      >
        {(data?.changes.length ?? 0) === 0 ? (
          <EmptyHint>当日没有额度变更记录</EmptyHint>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px]">
              <thead className="border-b border-slate-100">
                <tr>
                  <Th>时间</Th>
                  <Th>租户</Th>
                  <Th>变更</Th>
                  <Th>当时成功/在途</Th>
                  <Th>结论</Th>
                  <Th>操作人 / 理由</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-50">
                {(data?.changes ?? []).map(c => (
                  <tr key={c.id}>
                    <Td className="whitespace-nowrap">{formatShortTime(c.effectiveAt)}</Td>
                    <Td>{c.tenantCode || c.tenantId}</Td>
                    <Td className="font-mono">
                      {c.oldQuota} → {c.newQuota}
                    </Td>
                    <Td>
                      {c.successAtMoment} / {c.inFlightAtMoment}
                    </Td>
                    <Td>
                      <StatusChip status={c.isRejected ? '拒绝并留痕' : '生效'} dot />
                    </Td>
                    <Td className="text-xs text-slate-500">
                      <div>{c.operator || '—'}</div>
                      <div className="mt-0.5">{c.reason || '—'}</div>
                      {c.isRejected && c.rejectedReason && (
                        <div className="mt-0.5 text-rose-500">{c.rejectedReason}</div>
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card
        title="迟到响应诊断池（只归档，不改结论、不补扣）"
        subtitle="迟到结果只写诊断表：不改终态、不追加扣额、不计入成功量"
      >
        {(data?.diagnostics.length ?? 0) === 0 ? (
          <EmptyHint>当前没有迟到诊断记录</EmptyHint>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px]">
              <thead className="border-b border-slate-100">
                <tr>
                  <Th>client_req_id</Th>
                  <Th>原结论</Th>
                  <Th>迟到详情</Th>
                  <Th>归档时间</Th>
                  <Th>处理</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-50">
                {(data?.diagnostics ?? []).map(d => (
                  <tr key={d.id}>
                    <Td className="font-mono text-xs">
                      {data?.requestIndex.get(d.requestId) ?? d.requestId}
                    </Td>
                    <Td>TIMEOUT</Td>
                    <Td className="text-xs text-slate-500">{d.detail || d.kind}</Td>
                    <Td className="whitespace-nowrap">{formatUtcTime(d.recordedAt)}</Td>
                    <Td>
                      <StatusChip status="仅归档" />
                    </Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {/* 调额窗口 */}
      <Modal
        open={Boolean(quotaTarget)}
        title={`调额窗口 · ${quotaTarget?.tenant.code ?? ''}`}
        subtitle="降额底线由后端校验，前端不做额度判定"
        onClose={() => setQuotaTarget(null)}
        footer={
          <>
            <GhostBtn onClick={() => setQuotaTarget(null)}>取消</GhostBtn>
            <PrimaryBtn onClick={submitQuota} disabled={busy}>
              {busy ? '提交中…' : '提交调额'}
            </PrimaryBtn>
          </>
        }
      >
        {quotaTarget && (
          <>
            <div className="rounded-xl border border-slate-200 bg-slate-50/60 px-4 py-3 text-sm text-slate-700">
              原额度 {quotaTarget.usage?.limit ?? quotaTarget.tenant.dailyQuota} → 新额度{' '}
              <span className="font-mono font-semibold text-slate-900">{newQuota || '—'}</span>
            </div>
            <LabeledInput
              label="新额度"
              type="number"
              value={newQuota}
              onChange={setNewQuota}
              hint="留空或非整数由后端返回 INVALID_PARAMS / QUOTA_RANGE_INVALID"
            />
            <LabeledInput
              label="理由"
              value={quotaReason}
              onChange={setQuotaReason}
              placeholder="演示活动临时增额"
              hint="理由必填；后端返回 REASON_REQUIRED 表示未填写"
            />
            <AlertStrip tone="amber">
              ↓ 新额度不得低于 成功+在途 = {floor}（提交后由后端判定，违规会写入变更流水）
            </AlertStrip>
            <ErrorBanner error={actionError} />
          </>
        )}
      </Modal>

      {/* 停用租户 */}
      <Modal
        open={Boolean(disableTarget)}
        title={`停用租户 · ${disableTarget?.tenant.code ?? ''}`}
        subtitle="停用后不再受理新调用；已受理请求按原规则结算"
        onClose={() => setDisableTarget(null)}
        footer={
          <>
            <GhostBtn onClick={() => setDisableTarget(null)}>取消</GhostBtn>
            <PrimaryBtn onClick={submitDisable} disabled={busy}>
              {busy ? '提交中…' : '确认停用'}
            </PrimaryBtn>
          </>
        }
      >
        <LabeledInput
          label="停用理由"
          value={disableReason}
          onChange={setDisableReason}
          placeholder="客户违规 / 欠费 / 应急切断"
          hint="理由必填；缺失时后端返回 REASON_REQUIRED"
        />
        <ErrorBanner error={actionError} />
      </Modal>

      {/* 开通租户 */}
      <Modal
        open={openOpen}
        title="开通租户"
        subtitle="设置初始日额度并留痕（操作人与原因进入额度变更流水）"
        onClose={() => setOpenOpen(false)}
        footer={
          <>
            <GhostBtn onClick={() => setOpenOpen(false)}>取消</GhostBtn>
            <PrimaryBtn onClick={submitOpen} disabled={busy}>
              {busy ? '提交中…' : '开通'}
            </PrimaryBtn>
          </>
        }
      >
        <LabeledInput label="租户名称" value={openName} onChange={setOpenName} placeholder="企业名称" />
        <LabeledInput
          label="初始日额度"
          type="number"
          value={openQuota}
          onChange={setOpenQuota}
          hint="每个企业初始日额度 10 次"
        />
        <LabeledInput label="开通理由" value={openReason} onChange={setOpenReason} />
        <ErrorBanner error={actionError} />
      </Modal>
    </>
  );
};
