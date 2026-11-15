import React, { useMemo, useState } from 'react';
import { KeyRound, Plus, RefreshCcw, ShieldAlert, Wallet } from 'lucide-react';
import {
  createCredential,
  disableCredential,
  fetchTenantRequests,
  fetchTenantUsage,
  listCredentials
} from '@/api/endpoints';
import { describeError } from '@/api/client';
import { ErrorBanner, type ErrorInfo } from '@/components/common/ErrorBanner';
import {
  AlertStrip,
  Card,
  EmptyHint,
  GhostBtn,
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
import type { CredentialItem, RequestItem, UsageSummary } from '@/types';
import { formatUtcTime, todayUtcDate } from '@/utils/date';

interface TenantData {
  usage: UsageSummary | null;
  credentials: CredentialItem[];
  requests: RequestItem[];
}

const CREDENTIAL_LIMIT = 2;

/** 凭证密钥仅在创建时返回一次，这里把密钥记在本地以便后续掩码展示。 */
function rememberSecret(credentialId: string, secret: string): string {
  const masked = secret.length > 8 ? `${secret.slice(0, 6)}***${secret.slice(-4)}` : secret;
  try {
    localStorage.setItem(`mq.secret.${credentialId}`, secret);
  } catch {
    /* 忽略存储失败 */
  }
  return masked;
}

function readMaskedSecret(credentialId: string): string | null {
  try {
    const secret = localStorage.getItem(`mq.secret.${credentialId}`);
    if (!secret) return null;
    return secret.length > 8 ? `${secret.slice(0, 6)}***${secret.slice(-4)}` : secret;
  } catch {
    return null;
  }
}

/**
 * 图 02 · 租户控制台（客户管理员视角）
 *
 * 区块：共享日额度条（Limit − 成功 − 在途 = 可用）+ 2 张凭证卡 + 停用按钮
 *       + 本租户全量调用流水表。
 */
export const TenantConsole: React.FC = () => {
  const [day, setDay] = useState(todayUtcDate());
  const [actionError, setActionError] = useState<ErrorInfo | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [createOpen, setCreateOpen] = useState(false);
  const [credName, setCredName] = useState('');
  const [createdSecret, setCreatedSecret] = useState<{ name: string; secret: string } | null>(null);

  const [disableTarget, setDisableTarget] = useState<CredentialItem | null>(null);
  const [disableReason, setDisableReason] = useState('');

  const { data, error, loading, reload } = useAsyncData<TenantData>(async () => {
    const [usageRes, credRes, reqRes] = await Promise.allSettled([
      fetchTenantUsage({ day }),
      listCredentials(),
      fetchTenantRequests({ day })
    ]);
    return {
      usage: usageRes.status === 'fulfilled' ? usageRes.value : null,
      credentials:
        credRes.status === 'fulfilled'
          ? credRes.value.map(c => ({
              ...c,
              maskedSecret: readMaskedSecret(c.id)
            }))
          : [],
      requests: reqRes.status === 'fulfilled' ? reqRes.value : []
    };
  }, [day]);

  const counters = useMemo(() => {
    const map = new Map<string, { success: number; inFlight: number; failed: number; timeout: number }>();
    for (const r of data?.requests ?? []) {
      const c = map.get(r.credentialId) ?? { success: 0, inFlight: 0, failed: 0, timeout: 0 };
      if (r.status === 'SUCCESS') c.success += 1;
      else if (r.status === 'PROCESSING') c.inFlight += 1;
      else if (r.status === 'FAILED') c.failed += 1;
      else if (r.status === 'TIMEOUT') c.timeout += 1;
      map.set(r.credentialId, c);
    }
    return map;
  }, [data]);

  const usage = data?.usage ?? null;
  const credentials = data?.credentials ?? [];
  const credCount = credentials.length;

  const submitCreate = async () => {
    setBusy(true);
    setActionError(null);
    try {
      const result = await createCredential({ name: credName });
      const masked = rememberSecret(result.credential.id, result.secret);
      setCreatedSecret({ name: result.credential.name, secret: result.secret });
      setCredName('');
      setCreateOpen(false);
      setNotice(`凭证 ${result.credential.name} 已创建（掩码 ${masked}）`);
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
      await disableCredential({ credentialId: disableTarget.id, reason: disableReason });
      setDisableTarget(null);
      setDisableReason('');
      setNotice('凭证停用已提交（后续新调用将被后端拒绝为 KEY_SUSPENDED）');
      reload();
    } catch (e) {
      setActionError(describeError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div className="flex items-center gap-3">
          <span className="text-[13px] text-slate-500">核算基准日（UTC）</span>
          <input
            type="date"
            value={day}
            onChange={e => setDay(e.target.value || todayUtcDate())}
            className="rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-sm text-slate-700 outline-none focus:border-[#2f6b4f]"
          />
        </div>
        <GhostBtn onClick={reload}>
          <span className="inline-flex items-center gap-1.5">
            <RefreshCcw className="w-3.5 h-3.5" />
            刷新
          </span>
        </GhostBtn>
      </div>

      <ErrorBanner error={error} />
      <ErrorBanner error={actionError} />
      {notice && (
        <AlertStrip tone="emerald" icon={Wallet}>
          {notice}
        </AlertStrip>
      )}

      {/* 共享日额度条 */}
      <Card
        title="共享日额度（租户维度，两个应用共用）"
        subtitle="口径：剩余可用 = Limit − (成功 + 在途)，在途已预占槽位"
        action={
          <span className="text-[11px] text-slate-400">数据来源：GET /api/v1/admin/usage</span>
        }
      >
        {loading && !usage && <LoadingHint />}
        {usage && (
          <>
            <div className="flex flex-wrap items-center gap-3 text-[15px] text-slate-800">
              <span className="font-mono">Limit {usage.limit}</span>
              <span className="text-slate-300">−</span>
              <span className="font-mono">成功 {usage.success}</span>
              <span className="text-slate-300">−</span>
              <span className="font-mono">在途 {usage.inFlight}</span>
              <span className="text-slate-300">＝</span>
              <span className="font-mono font-semibold text-[#1f3a30]">可用 {usage.available}</span>
            </div>
            <div className="mt-4">
              <Progress
                value={usage.success + usage.inFlight}
                max={usage.limit}
                tone={usage.available <= 0 ? 'red' : usage.inFlight > 0 ? 'amber' : 'green'}
                height="h-2.5"
              />
            </div>
            <div className="mt-2 text-[11px] text-slate-400">
              额度按租户共享而非按凭证计算：客户无法通过更换凭证绕过租户总额度（R3 / R4）。
            </div>
          </>
        )}
      </Card>

      {/* 凭证卡 */}
      <Card
        title="访问凭证"
        subtitle="凭证归属租户由系统锚定，客户端不可指定（伪造 tenant_id 会被丢弃）"
        action={
          <PrimaryBtn onClick={() => setCreateOpen(true)}>
            <span className="inline-flex items-center gap-1.5">
              <Plus className="w-3.5 h-3.5" />
              创建凭证
            </span>
          </PrimaryBtn>
        }
      >
        {credentials.length === 0 && !loading ? (
          <EmptyHint>本租户还没有凭证</EmptyHint>
        ) : (
          <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
            {credentials.map((c, idx) => {
              const counter = counters.get(c.id);
              return (
                <div key={c.id} className="rounded-xl border border-slate-200/80 p-4">
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <div className="text-[14px] font-semibold text-slate-900">
                        凭证 {idx + 1} ｜ {c.name}
                      </div>
                      <div className="mt-1 font-mono text-[12px] text-slate-500">
                        {c.maskedSecret ?? c.id}
                        {c.maskedSecret ? '' : ' ·（密钥仅创建时返回一次，本机未记录）'}
                      </div>
                    </div>
                    <StatusChip
                      status={c.status === 'ACTIVE' ? '启用' : '停用'}
                      dot
                    />
                  </div>

                  <div className="mt-3 grid grid-cols-4 gap-2 text-center">
                    {[
                      { k: '今日成功', v: counter?.success ?? 0 },
                      { k: '在途', v: counter?.inFlight ?? 0 },
                      { k: '失败', v: counter?.failed ?? 0 },
                      { k: '超时', v: counter?.timeout ?? 0 }
                    ].map(x => (
                      <div key={x.k} className="rounded-lg bg-slate-50 border border-slate-200/70 py-2">
                        <div className="text-[10.5px] text-slate-400">{x.k}</div>
                        <div className="text-[16px] font-semibold text-slate-900">{x.v}</div>
                      </div>
                    ))}
                  </div>

                  <div className="mt-3 flex items-center justify-between gap-3">
                    <span className="text-[11px] text-slate-400">
                      创建 {c.createdAt ? formatUtcTime(c.createdAt) : '—'}
                      {c.disableReason ? ` · 停用原因：${c.disableReason}` : ''}
                    </span>
                    <GhostBtn
                      onClick={() => {
                        setDisableTarget(c);
                        setDisableReason('');
                      }}
                    >
                      <span className="inline-flex items-center gap-1.5">
                        <ShieldAlert className="w-3.5 h-3.5" />
                        停用（泄漏处置）
                      </span>
                    </GhostBtn>
                  </div>
                </div>
              );
            })}
          </div>
        )}

        <div className="mt-4">
          <AlertStrip tone={credCount >= CREDENTIAL_LIMIT ? 'amber' : 'slate'} icon={KeyRound}>
            凭证上限 {credCount}/{CREDENTIAL_LIMIT}
            {credCount >= CREDENTIAL_LIMIT
              ? '，第三把创建将被拒绝（后端返回 CREDENTIAL_LIMIT_REACHED）'
              : '，创建第三把时后端会拒绝并提示上限'}
          </AlertStrip>
        </div>
      </Card>

      {/* 全量调用流水 */}
      <Card
        title="本租户全量调用流水（管理员可穿透两个应用）"
        subtitle="列：client_req_id ｜ 凭证 ｜ 受理时间(UTC) ｜ 状态 ｜ 扣额 ｜ 错误码"
      >
        {(data?.requests.length ?? 0) === 0 ? (
          <EmptyHint>当日没有调用流水</EmptyHint>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[760px]">
              <thead className="border-b border-slate-100">
                <tr>
                  <Th>client_req_id</Th>
                  <Th>凭证</Th>
                  <Th>受理时间(UTC)</Th>
                  <Th>状态</Th>
                  <Th>扣额</Th>
                  <Th>错误码</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-50">
                {(data?.requests ?? []).map(r => (
                  <tr key={`${r.id}-${r.receivedAt}`}>
                    <Td className="font-mono text-xs">{r.clientReqId}</Td>
                    <Td className="text-xs">
                      {r.credentialName ??
                        credentials.find(c => c.id === r.credentialId)?.name ??
                        r.credentialId}
                    </Td>
                    <Td className="whitespace-nowrap text-xs">{formatUtcTime(r.receivedAt)}</Td>
                    <Td>
                      <StatusChip status={r.status} dot />
                    </Td>
                    <Td>
                      {r.status === 'SUCCESS' ? (
                        <span className="text-emerald-600 font-medium">1</span>
                      ) : (
                        <span className="text-slate-400">0</span>
                      )}
                    </Td>
                    <Td className="text-xs font-mono text-slate-500">{r.errorReason || '—'}</Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="mt-3 text-[11px] text-slate-400">
          扣额列按后端终态展示（仅 SUCCESS 实扣），权威计数以「共享日额度」中的后端口径为准；
          跨租户请求在数据层完全不可见。
        </div>
      </Card>

      {/* 创建凭证 */}
      <Modal
        open={createOpen}
        title="创建访问凭证"
        subtitle="凭证所属租户由服务端从登录身份推导，请求参数无法指定"
        onClose={() => setCreateOpen(false)}
        footer={
          <>
            <GhostBtn onClick={() => setCreateOpen(false)}>取消</GhostBtn>
            <PrimaryBtn onClick={submitCreate} disabled={busy}>
              {busy ? '提交中…' : '创建'}
            </PrimaryBtn>
          </>
        }
      >
        <LabeledInput
          label="凭证名称"
          value={credName}
          onChange={setCredName}
          placeholder="如：智联客服机器人 App-A1"
          hint="名称用于在流水与凭证卡中标识该应用"
        />
        <AlertStrip tone="slate">
          达到上限（第 3 把）时后端会返回 CREDENTIAL_LIMIT_REACHED，前端不会提前拦截。
        </AlertStrip>
        <ErrorBanner error={actionError} />
      </Modal>

      {/* 停用凭证 */}
      <Modal
        open={Boolean(disableTarget)}
        title={`停用凭证 · ${disableTarget?.name ?? ''}`}
        subtitle="停用后新调用立即被拒（403 KEY_SUSPENDED），已受理在途请求照常结算"
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
          label="停用原因（泄漏处置）"
          value={disableReason}
          onChange={setDisableReason}
          placeholder="疑似泄漏，紧急切断"
        />
        <ErrorBanner error={actionError} />
      </Modal>

      {/* 密钥仅返回一次 */}
      <Modal
        open={Boolean(createdSecret)}
        title="凭证已创建"
        subtitle="密钥只在创建时返回这一次，请立即保存（之后无法再次查看）"
        onClose={() => setCreatedSecret(null)}
        footer={
          <>
            <GhostBtn onClick={() => setCreatedSecret(null)}>我已保存</GhostBtn>
            <PrimaryBtn
              onClick={() => {
                if (createdSecret) navigator.clipboard?.writeText(createdSecret.secret);
              }}
            >
              复制密钥
            </PrimaryBtn>
          </>
        }
      >
        {createdSecret && (
          <>
            <div className="rounded-xl border border-slate-200 bg-slate-50 px-4 py-3">
              <div className="text-xs text-slate-400">{createdSecret.name}</div>
              <div className="mt-1 font-mono text-[13px] text-slate-900 break-all">
                {createdSecret.secret}
              </div>
            </div>
            <AlertStrip tone="amber">
              该密钥可用于「模型调用台」的账号切换（App-A1 / App-A2 / App-B1）。
            </AlertStrip>
          </>
        )}
      </Modal>
    </>
  );
};
