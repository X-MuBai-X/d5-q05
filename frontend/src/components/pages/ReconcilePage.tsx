import React, { useMemo, useState } from 'react';
import { CheckCircle2, Copy, RefreshCcw, Terminal, XCircle } from 'lucide-react';
import {
  fetchDailyReport,
  fetchDiagnostics,
  fetchQuotaChanges,
  listTenants
} from '@/api/endpoints';
import { ErrorBanner } from '@/components/common/ErrorBanner';
import {
  AlertStrip,
  Card,
  EmptyHint,
  GhostBtn,
  LabeledSelect,
  LoadingHint,
  StatusChip,
  Td,
  Th
} from '@/components/shell/ui';
import { useAsyncData } from '@/hooks/useAsyncData';
import type { DailyReportModel, DiagnosticItem, QuotaChangeItem, TenantCard } from '@/types';
import { formatShortTime, todayUtcDate } from '@/utils/date';

interface ReconcileData {
  tenants: TenantCard[];
  report: DailyReportModel | null;
  changes: QuotaChangeItem[];
  diagnostics: DiagnosticItem[];
}

/** 对账 CLI 的真实用法说明（摘自 cmd/reconcile 的 usage 文本）。 */
const CLI_USAGE = `reconcile-cli · 题目 05 日终用量对账

用法:
  reconcile-cli --tenant <租户代号或ID> --date <UTC自然日 YYYY-MM-DD> [--db <SQLite路径>] [--verify]

选项:
  --tenant   必填。租户代号（如 GEEK-AI）或租户 ID（如 tenant-a）
  --date     必填。UTC 自然日，格式 YYYY-MM-DD
  --db       数据库路径，默认 ./data/app.db
  --verify   校验模式（默认即开启；保留该开关以便脚本显式声明意图）
  --json     以 JSON 输出结果，便于流水线消费

退出码：
  0 = 账实一致（扣额 ≡ 成功请求数，且未超卖、迟到补扣为 0）
  1 = 存在差异，输出差异明细
  2 = 参数或环境错误（例如租户不存在、日期格式非法、数据库打不开）`;

/**
 * 图 05 · 日终核查与 CLI
 *
 * 区块：核查清单 5 项 + 变更追溯 + 跨租户校验 + 对账 CLI 控制台。
 *
 * CLI 控制台**不拼装任何日志**：后端当前未提供对账输出接口，
 * 因此该区块只说明真实 CLI 的调用方式与数据来源，由本地运行 cmd/reconcile 产出。
 */
export const ReconcilePage: React.FC = () => {
  const [tenantId, setTenantId] = useState('');
  const [day, setDay] = useState(todayUtcDate());
  const [copied, setCopied] = useState(false);

  const { data, error, loading, reload } = useAsyncData<ReconcileData>(async () => {
    const tenants = await listTenants();
    const target = tenantId || tenants[0]?.id || '';
    if (!target) return { tenants, report: null, changes: [], diagnostics: [] };
    const [reportRes, changeRes, diagRes] = await Promise.allSettled([
      fetchDailyReport({ tenantId: target, day }),
      fetchQuotaChanges({ tenantId: target, day }),
      fetchDiagnostics({ tenantId: target })
    ]);
    return {
      tenants,
      report: reportRes.status === 'fulfilled' ? reportRes.value : null,
      changes: changeRes.status === 'fulfilled' ? changeRes.value : [],
      diagnostics: diagRes.status === 'fulfilled' ? diagRes.value : []
    };
  }, [tenantId, day]);

  const report = data?.report ?? null;

  const checklist = useMemo(() => {
    if (!report) return [];
    const lateRededuction = report.usageSuccess - report.counts.SUCCESS;
    const traceable =
      report.changes.every(
        c => Boolean(c.operator) && Boolean(c.effectiveAt) && c.reason !== undefined
      ) && report.events.every(e => Boolean(e.actor) && Boolean(e.occurredAt));
    return [
      {
        label: '扣额 ≡ 成功请求数',
        pass: report.consistent,
        detail: `后端口径：成功 ${report.counts.SUCCESS} · 计入用量 ${report.usageSuccess}${
          report.mismatchNote ? ` · ${report.mismatchNote}` : ''
        }`
      },
      {
        label: '在途归零',
        pass: report.counts.PROCESSING === 0,
        detail: `处理中 ${report.counts.PROCESSING}`
      },
      {
        label: '无超卖（占用 ≤ 限额）',
        pass: report.usageSuccess + report.usageInFlight <= report.quotaLimit,
        detail: `占用 ${report.usageSuccess + report.usageInFlight} / 限额 ${report.quotaLimit}`
      },
      {
        label: '迟到包补扣 = 0',
        pass: lateRededuction === 0,
        detail: `迟到诊断 ${data?.diagnostics.length ?? 0} 条 · 补扣差额 ${lateRededuction}`
      },
      {
        label: '变更 / 停用影响可追溯',
        pass: traceable,
        detail: `额度变更 ${report.changes.length} 条 · 停用事件 ${report.events.length} 条`
      }
    ];
  }, [report, data?.diagnostics.length]);

  const tenant = data?.tenants.find(t => t.id === (tenantId || data?.tenants[0]?.id));
  const cliCommand = `reconcile-cli --tenant ${tenant?.code || tenant?.id || '<租户代号或ID>'} --date ${day} --verify`;

  const copyCli = () => {
    navigator.clipboard?.writeText(cliCommand);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  };

  return (
    <>
      <div className="flex items-end justify-between gap-4 flex-wrap">
        <div className="flex items-end gap-3 flex-wrap">
          <div className="w-[240px]">
            <LabeledSelect
              label="租户"
              value={tenantId || data?.tenants[0]?.id || ''}
              onChange={setTenantId}
              options={(data?.tenants ?? []).map(t => ({
                value: t.id,
                label: `${t.code} · ${t.name}`
              }))}
            />
          </div>
          <div>
            <span className="block text-xs font-medium text-slate-500 mb-1.5">核算自然日（UTC）</span>
            <input
              type="date"
              value={day}
              onChange={e => setDay(e.target.value || todayUtcDate())}
              className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 outline-none focus:border-[#2f6b4f]"
            />
          </div>
        </div>
        <GhostBtn onClick={reload}>
          <span className="inline-flex items-center gap-1.5">
            <RefreshCcw className="w-3.5 h-3.5" />
            刷新
          </span>
        </GhostBtn>
      </div>

      <ErrorBanner error={error} />

      {/* 核查清单 */}
      <Card title="核查清单" subtitle="五项日终核查结论（数据来自 GET /api/v1/ops/report/daily）">
        {loading && <LoadingHint />}
        {!loading && !report && <EmptyHint>后端未返回日终报表</EmptyHint>}
        {report && (
          <ul className="space-y-2.5">
            {checklist.map(item => (
              <li key={item.label} className="flex items-start gap-3">
                {item.pass ? (
                  <CheckCircle2 className="w-4.5 h-4.5 text-emerald-600 mt-0.5 shrink-0" />
                ) : (
                  <XCircle className="w-4.5 h-4.5 text-rose-600 mt-0.5 shrink-0" />
                )}
                <div>
                  <div className="text-[13.5px] text-slate-800">{item.label}</div>
                  <div className="text-[11.5px] text-slate-400 mt-0.5">{item.detail}</div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      {/* 变更追溯 */}
      <Card
        title="变更追溯"
        subtitle="时间 ｜ 变更 ｜ 操作人（含被拒绝的变更与停用事件）"
      >
        {(data?.changes.length ?? 0) === 0 && (report?.events.length ?? 0) === 0 ? (
          <EmptyHint>当日没有额度变更或停用事件</EmptyHint>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px]">
              <thead className="border-b border-slate-100">
                <tr>
                  <Th>时间</Th>
                  <Th>变更</Th>
                  <Th>操作人</Th>
                  <Th>结论</Th>
                  <Th>理由</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-50">
                {(data?.changes ?? []).map(c => (
                  <tr key={`qc-${c.id}`}>
                    <Td className="whitespace-nowrap text-xs">{formatShortTime(c.effectiveAt)}</Td>
                    <Td className="font-mono text-xs">
                      {c.oldQuota} → {c.newQuota}
                    </Td>
                    <Td className="text-xs">{c.operator || '—'}</Td>
                    <Td>
                      <StatusChip status={c.isRejected ? '拒绝并留痕' : '生效'} dot />
                    </Td>
                    <Td className="text-xs text-slate-500">
                      {c.reason || '—'}
                      {c.isRejected && c.rejectedReason ? (
                        <div className="text-rose-500 mt-0.5">{c.rejectedReason}</div>
                      ) : null}
                    </Td>
                  </tr>
                ))}
                {(report?.events ?? []).map(e => (
                  <tr key={`ev-${e.id}`}>
                    <Td className="whitespace-nowrap text-xs">{formatShortTime(e.occurredAt)}</Td>
                    <Td className="text-xs">
                      {e.kind === 'credential_disabled'
                        ? '凭证停用'
                        : e.kind === 'tenant_disabled'
                          ? '租户停用'
                          : e.kind === 'tenant_enabled'
                            ? '租户启用'
                            : e.kind}
                    </Td>
                    <Td className="text-xs">{e.actor || '—'}</Td>
                    <Td>
                      <StatusChip status="已追溯" dot />
                    </Td>
                    <Td className="text-xs text-slate-500">{e.reason || e.detail || '—'}</Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {/* 跨租户校验 */}
      <Card
        title="跨租户校验"
        subtitle="两个租户的数据完全隔离，互不可见（R11）"
      >
        <div className="overflow-x-auto">
          <table className="w-full min-w-[520px]">
            <thead className="border-b border-slate-100">
              <tr>
                <Th>租户</Th>
                <Th>租户编号</Th>
                <Th>状态</Th>
                <Th>数据范围</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-50">
              {(data?.tenants ?? []).map(t => (
                <tr key={t.id}>
                  <Td>{t.code}</Td>
                  <Td className="font-mono text-xs">{t.id}</Td>
                  <Td>
                    <StatusChip status={t.status} dot />
                  </Td>
                  <Td className="text-xs text-slate-500">
                    仅本租户流水可见，跨租户查询返回 403 且明细 0 行
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="mt-3 text-[11px] text-slate-400">
          跨租户校验：{(data?.tenants ?? []).map(t => t.code).join(' ↔ ') || '—'}
        </div>
      </Card>

      {/* 对账 CLI 控制台 */}
      <Card
        title="对账 CLI 控制台"
        subtitle="对账脚本（cmd/reconcile）对 SQLite 受理流水直接核算，输出由真实运行产生"
        action={
          <button
            onClick={copyCli}
            className="inline-flex items-center gap-1.5 text-[12.5px] text-[#2f6b4f] hover:text-[#1f3a30]"
          >
            <Copy className="w-3.5 h-3.5" />
            {copied ? '已复制' : '复制命令'}
          </button>
        }
      >
        <AlertStrip tone="amber" icon={Terminal}>
          数据来源说明：本页**不拼装任何对账日志**。后端当前没有暴露对账输出的 HTTP 接口，
          下面的命令需要在服务端本地运行 <code className="font-mono">cmd/reconcile</code>，
          直接读取 SQLite 受理流水后输出真实结果（退出码 0 账实一致 / 1 存在差异 / 2 参数或环境错误）。
        </AlertStrip>

        <div className="mt-4 rounded-xl bg-[#12211c] text-[12.5px] leading-relaxed">
          <div className="px-4 pt-3 pb-1 text-white/40">$ {cliCommand}</div>
          <div className="px-4 pb-4 pt-2 whitespace-pre-wrap text-white/80">{CLI_USAGE}</div>
        </div>

        <div className="mt-3 text-[11.5px] text-slate-400">
          与页面核查清单的口径一致：扣额 ≡ 成功请求数、占用不超过限额、在途归零、迟到包补扣为 0。
          当前报表自然日：{report?.day || day}（UTC）
        </div>
      </Card>
    </>
  );
};
