import React, { useState } from 'react';
import { RefreshCcw, Search, Send, ShieldCheck } from 'lucide-react';
import { appInvoke, fetchAppRequest, fetchAppRequests } from '@/api/endpoints';
import { describeError } from '@/api/client';
import { ErrorBanner, type ErrorInfo } from '@/components/common/ErrorBanner';
import {
  Card,
  EmptyHint,
  GhostBtn,
  LabeledInput,
  LabeledSelect,
  LabeledTextarea,
  LoadingHint,
  PrimaryBtn,
  StatusChip,
  Td,
  Th
} from '@/components/shell/ui';
import { useAsyncData } from '@/hooks/useAsyncData';
import type { DiagnosticItem, InvokeResult, RequestItem } from '@/types';
import { formatUtcTime, todayUtcDate } from '@/utils/date';

const MODELS = [{ value: 'Jinghua-Text-Omni-v1', label: 'Jinghua-Text-Omni-v1（单模型非流式）' }];

/**
 * 图 03 · 应用调用台（客户应用视角）
 *
 * 区块：发起调用表单 + 响应区 + 防越权查询器 + 我的请求表。
 *
 * 前端不做任何拦截：额度不足、凭证停用、租户停用、幂等冲突等
 * 全部由后端裁决并返回错误码，本页只展示。
 */
export const AppConsole: React.FC = () => {
  const [day, setDay] = useState(todayUtcDate());
  const [model, setModel] = useState(MODELS[0].value);
  const [clientReqId, setClientReqId] = useState('req-app-a1-20250519-007');
  const [prompt, setPrompt] = useState('请生成 3 条工单回复模板');

  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<ErrorInfo | null>(null);
  const [result, setResult] = useState<InvokeResult | null>(null);

  const [queryId, setQueryId] = useState('');
  const [queryBusy, setQueryBusy] = useState(false);
  const [queryError, setQueryError] = useState<ErrorInfo | null>(null);
  const [queryResult, setQueryResult] = useState<{ request: RequestItem; diagnostics: DiagnosticItem[] } | null>(
    null
  );

  const { data, error, loading, reload } = useAsyncData<RequestItem[]>(
    () => fetchAppRequests({ day }),
    [day]
  );

  const send = async () => {
    setBusy(true);
    setActionError(null);
    setResult(null);
    try {
      const r = await appInvoke({ clientReqId: clientReqId.trim(), model, prompt });
      setResult(r);
      reload();
    } catch (e) {
      setActionError(describeError(e));
    } finally {
      setBusy(false);
    }
  };

  const query = async () => {
    setQueryBusy(true);
    setQueryError(null);
    setQueryResult(null);
    try {
      const r = await fetchAppRequest({ clientReqId: queryId.trim() });
      setQueryResult(r);
    } catch (e) {
      setQueryError(describeError(e));
    } finally {
      setQueryBusy(false);
    }
  };

  return (
    <>
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div className="flex items-center gap-3">
          <span className="text-[13px] text-slate-500">受理日期（UTC）</span>
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

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-5">
        {/* 发起调用 */}
        <Card title="发起调用" subtitle="请求必须携带本租户全局唯一编号 client_req_id">
          <div className="space-y-4">
            <LabeledSelect label="model" value={model} onChange={setModel} options={MODELS} />
            <LabeledInput
              label="client_req_id"
              value={clientReqId}
              onChange={setClientReqId}
              mono
              placeholder="req-app-a1-20250519-007"
              hint="租户内全局唯一且跨日不复用：同号同参 → 幂等命中；同号改参 → 409"
            />
            <LabeledTextarea label="prompt" value={prompt} onChange={setPrompt} />
            <PrimaryBtn onClick={send} disabled={busy}>
              <span className="inline-flex items-center gap-1.5">
                <Send className="w-3.5 h-3.5" />
                {busy ? '发送中…' : '发送请求'}
              </span>
            </PrimaryBtn>
            <ErrorBanner error={actionError} />
          </div>
        </Card>

        {/* 响应区 */}
        <Card
          title="响应区"
          subtitle="响应内容与用量口径全部来自后端"
          action={
            result?.idempotentHit ? <StatusChip status="IDEMPOTENT_HIT" /> : undefined
          }
        >
          {!result && !actionError && (
            <EmptyHint>尚未发起调用 —— 发送一次请求查看后端返回的终态与扣额口径</EmptyHint>
          )}
          {result && (
            <div className="space-y-3">
              <div className="rounded-xl border border-slate-200 bg-slate-50/70 px-4 py-3">
                <div className="flex items-center gap-2 flex-wrap text-[14px]">
                  <span className="font-mono font-semibold text-slate-900">200</span>
                  <StatusChip status={result.status} dot />
                  {result.quotaDeducted ? (
                    <span className="text-slate-600">· 实扣 1 次</span>
                  ) : (
                    <span className="text-slate-600">· 扣额 0 次</span>
                  )}
                  {result.usage && (
                    <span className="text-slate-600">· 剩余可用 {result.usage.available}</span>
                  )}
                </div>
                {result.idempotentHit && (
                  <div className="mt-2 text-[11.5px] text-violet-600">
                    同编号同参数 → 幂等命中：零上游调用、零重复计量
                  </div>
                )}
              </div>

              <dl className="grid grid-cols-2 gap-y-2 text-[12.5px]">
                <dt className="text-slate-400">client_req_id</dt>
                <dd className="font-mono text-slate-800">{result.clientReqId || '—'}</dd>
                <dt className="text-slate-400">model</dt>
                <dd className="text-slate-800">{result.model || '—'}</dd>
                <dt className="text-slate-400">受理时间(UTC)</dt>
                <dd className="text-slate-800">{formatUtcTime(result.acceptedAt)}</dd>
                <dt className="text-slate-400">终结时间(UTC)</dt>
                <dd className="text-slate-800">{formatUtcTime(result.finishedAt)}</dd>
                <dt className="text-slate-400">时延</dt>
                <dd className="text-slate-800">
                  {result.latencyMs === null ? '—' : `${result.latencyMs} ms`}
                </dd>
                <dt className="text-slate-400">用量口径</dt>
                <dd className="text-slate-800">
                  {result.usage
                    ? `Limit ${result.usage.limit} · 成功 ${result.usage.success} · 在途 ${result.usage.inFlight} · 可用 ${result.usage.available}`
                    : '—'}
                </dd>
                {result.errorCode && (
                  <>
                    <dt className="text-slate-400">错误码</dt>
                    <dd className="font-mono text-rose-600">{result.errorCode}</dd>
                  </>
                )}
              </dl>

              {result.upstreamResult && (
                <div className="rounded-xl border border-slate-200 px-4 py-3 text-[12px] leading-relaxed text-slate-600">
                  上游返回：{result.upstreamResult}
                </div>
              )}
            </div>
          )}
        </Card>
      </div>

      {/* 防越权查询器 */}
      <Card
        title="防越权查询器"
        subtitle="输入 client_req_id 查询单条请求：越权访问由后端返回 403，前端不做拦截"
        action={
          <div className="flex items-center gap-2">
            <input
              value={queryId}
              onChange={e => setQueryId(e.target.value)}
              placeholder="req-app-a1-20250519-007"
              className="w-[280px] rounded-lg border border-slate-200 px-3 py-1.5 text-sm font-mono outline-none focus:border-[#2f6b4f]"
            />
            <PrimaryBtn onClick={query} disabled={queryBusy}>
              <span className="inline-flex items-center gap-1.5">
                <Search className="w-3.5 h-3.5" />
                {queryBusy ? '查询中…' : '查询'}
              </span>
            </PrimaryBtn>
          </div>
        }
      >
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <div className="space-y-2">
            <div className="text-[12px] font-medium text-slate-500">查询结果</div>
            {queryError && <ErrorBanner error={queryError} />}
            {queryResult && (
              <div className="rounded-xl border border-slate-200 px-4 py-3 space-y-1.5">
                <div className="flex items-center gap-2">
                  <span className="font-mono font-semibold text-slate-900">200</span>
                  <StatusChip status={queryResult.request.status} dot />
                </div>
                <div className="text-[12px] text-slate-500 font-mono">
                  {queryResult.request.clientReqId}
                </div>
                <div className="text-[12px] text-slate-600">
                  受理 {formatUtcTime(queryResult.request.receivedAt)} · 模型{' '}
                  {queryResult.request.model || '—'}
                </div>
                {queryResult.diagnostics.length > 0 && (
                  <div className="mt-2 rounded-lg bg-amber-50/70 border border-amber-100 px-3 py-2 text-[11.5px] text-amber-800">
                    诊断记录 {queryResult.diagnostics.length} 条（迟到结果只归档，不改终态、不补扣）
                  </div>
                )}
              </div>
            )}
            {!queryError && !queryResult && (
              <EmptyHint>尚未查询</EmptyHint>
            )}
          </div>

          <div className="space-y-2">
            <div className="text-[12px] font-medium text-slate-500">可见性口径</div>
            <ul className="space-y-1.5 text-[12px] text-slate-600">
              {[
                '本凭证请求 → 200，返回明细',
                '兄弟应用请求 → 403 SCOPE_DENIED',
                '跨租户请求 → 403，明细 0 行',
                '伪造 tenant_id → 被丢弃，按凭证归属',
                '同号同参 → 幂等命中，零重复计量'
              ].map(t => (
                <li key={t} className="flex items-start gap-2">
                  <ShieldCheck className="w-3.5 h-3.5 mt-0.5 text-emerald-600 shrink-0" />
                  <span>{t}</span>
                </li>
              ))}
            </ul>
          </div>
        </div>
      </Card>

      {/* 我的请求 */}
      <Card
        title="我的请求（数据范围：仅本凭证创建的流水）"
        subtitle="列：client_req_id ｜ 受理时间 ｜ 状态 ｜ 扣额"
      >
        {loading && <LoadingHint />}
        {!loading && (data?.length ?? 0) === 0 && <EmptyHint>当日没有请求流水</EmptyHint>}
        {(data?.length ?? 0) > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[620px]">
              <thead className="border-b border-slate-100">
                <tr>
                  <Th>client_req_id</Th>
                  <Th>受理时间(UTC)</Th>
                  <Th>状态</Th>
                  <Th>扣额</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-50">
                {(data ?? []).map(r => (
                  <tr key={`${r.id}-${r.receivedAt}`}>
                    <Td className="font-mono text-xs">{r.clientReqId}</Td>
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
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  );
};
