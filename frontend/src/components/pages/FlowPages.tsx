import React from 'react';
import { AlertTriangle, ArrowRight, Clock, FileClock, FlaskConical, GitBranch } from 'lucide-react';
import { Card } from '@/components/shell/ui';

/* ==================================================================
 * 图 04 · 网关受理流程与故障注入（流程说明页）
 *
 * 本页是**说明页**：只描述受理链路与故障注入开关的语义，
 * 不提供任何会修改后端数据的操作入口。
 * ================================================================== */

interface FlowStep {
  order: string;
  title: string;
  detail: string;
  rule: string;
  acceptance: string;
}

const GATEWAY_STEPS: FlowStep[] = [
  {
    order: '1',
    title: '客户端请求 · Bearer + req_id',
    detail: '客户端携带 Bearer 凭证与租户内全局唯一编号发起调用。',
    rule: 'R3 编号按 UTC 自然日归属',
    acceptance: 'AC-10 跨日归属'
  },
  {
    order: '2',
    title: '凭证鉴权 · 401 / 锚定租户',
    detail: '网关解析令牌确定凭证与租户归属，请求参数中的 tenant_id 一律无效。',
    rule: 'R1 凭证与租户系统级强绑定',
    acceptance: 'AC-01 / AC-11 租户数据隔离'
  },
  {
    order: '3',
    title: '幂等检查 · 200 命中 / 409',
    detail: '同编号同参数 → 命中历史结果，零上游调用、零重复计量；同编号改参数 → 409。',
    rule: 'R5 全局唯一编号与幂等',
    acceptance: 'AC-07 幂等与 409 篡改拦截'
  },
  {
    order: '4',
    title: '停用检查 · 403 租户 / 凭证',
    detail: '租户或凭证已停用 → 新调用一律 403；停用前已受理请求照常结算。',
    rule: 'R10 停用后不受理新调用',
    acceptance: 'AC-04 停用切断与在途保全'
  },
  {
    order: '5',
    title: '配额预占 · 429 / 在途占额',
    detail: '共享日额度原子预判并占用在途槽位；可用额度 ≤ 0 → 429。',
    rule: 'R3 共享日额度 / R4 在途占额',
    acceptance: 'AC-02 / AC-08 并发防超卖'
  },
  {
    order: '6',
    title: '上游调用 · 2 秒熔断阈值',
    detail: '调用上游模型；超过 2000ms 立即熔断返回 504 并释放预占。',
    rule: 'R6 2 秒超时熔断',
    acceptance: 'AC-05 失败与超时释放额度'
  },
  {
    order: '7',
    title: '终态判定 · 200 SUCCESS → 实扣 1',
    detail: '成功 → 实扣 1 次并释放在途；502 / 504 → 释放预占，扣额 = 0。',
    rule: 'R4 成功扣额、在途占额、其余不扣',
    acceptance: 'AC-01 / AC-09 日终账实一致'
  },
  {
    order: '8',
    title: '迟到响应（> 2s）· 仅归档',
    detail: '迟到包只写入诊断池，原结论保持不变，绝不补扣额度。',
    rule: 'R7 迟到结果隔离诊断',
    acceptance: 'AC-06 迟到隔离'
  }
];

const FAULT_SWITCHES = [
  {
    name: 'SUCCESS',
    desc: '上游在 2 秒内成功返回',
    effect: '200 SUCCESS · 实扣 1 次 · 释放在途',
    rule: 'R4 / R3',
    acceptance: 'AC-01 共享额度扣额'
  },
  {
    name: 'FAILED 502',
    desc: '上游明确失败',
    effect: '502 UPSTREAM_ERROR · 释放预占 · 扣额 0',
    rule: 'R4',
    acceptance: 'AC-05 失败释放额度'
  },
  {
    name: 'TIMEOUT 504',
    desc: '上游耗时超过 2 秒阈值',
    effect: '504 GATEWAY_TIMEOUT · 释放预占 · 扣额 0',
    rule: 'R6',
    acceptance: 'AC-05 超时释放额度'
  },
  {
    name: 'DELAYED_SUCCESS',
    desc: '上游在超时之后才返回成功（迟到包）',
    effect: '原结论保持 TIMEOUT · 仅归档诊断池 · 补扣 = 0',
    rule: 'R7',
    acceptance: 'AC-06 迟到隔离'
  },
  {
    name: '并发 5 连击',
    desc: '多个请求争抢最后一个额度',
    effect: '仅 1 个成功受理，其余 429 · 总扣额 = 1 · 零超卖',
    rule: 'R3 / R4',
    acceptance: 'AC-08 并发防超卖'
  }
];

export const GatewayFlowPage: React.FC = () => (
  <>
    <div className="rounded-2xl border border-amber-200 bg-amber-50/60 px-5 py-4 text-[12.5px] leading-relaxed text-amber-900">
      <div className="flex items-start gap-2.5">
        <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
        <div>
          本页为**流程说明页**，不是故障注入控制台：开关只做说明性展示，不向后端写入任何数据。
          真实调用请在「应用调用台」（图 03）发起，真实异常结果以后端返回的终态与错误码为准。
        </div>
      </div>
    </div>

    <Card title="受理链路" subtitle="客户端请求 → 鉴权 → 幂等 → 停用 → 预占 → 上游 → 终态 → 迟到">
      <ol className="space-y-3">
        {GATEWAY_STEPS.map(step => (
          <li key={step.order} className="flex gap-3">
            <div className="shrink-0 w-7 h-7 rounded-lg bg-[#1f3a30] text-white grid place-items-center text-[12px] font-semibold">
              {step.order}
            </div>
            <div className="min-w-0">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-[14px] font-semibold text-slate-900">{step.title}</span>
                <span className="rounded-full bg-slate-100 px-2 py-0.5 text-[10.5px] text-slate-500">
                  {step.rule}
                </span>
                <span className="rounded-full bg-emerald-50 px-2 py-0.5 text-[10.5px] text-emerald-600">
                  {step.acceptance}
                </span>
              </div>
              <div className="text-[12.5px] text-slate-500 mt-1 leading-relaxed">{step.detail}</div>
            </div>
          </li>
        ))}
      </ol>
    </Card>

    <Card
      title="故障注入开关（说明性展示）"
      subtitle="每个开关都对应一条业务规则与一条验收标准"
    >
      <div className="overflow-x-auto">
        <table className="w-full min-w-[720px]">
          <thead className="border-b border-slate-100">
            <tr>
              <th className="text-left text-xs font-medium text-slate-400 py-3">开关</th>
              <th className="text-left text-xs font-medium text-slate-400 py-3">触发场景</th>
              <th className="text-left text-xs font-medium text-slate-400 py-3">预期结果</th>
              <th className="text-left text-xs font-medium text-slate-400 py-3">规则</th>
              <th className="text-left text-xs font-medium text-slate-400 py-3">验收</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-50">
            {FAULT_SWITCHES.map(s => (
              <tr key={s.name}>
                <td className="py-3.5 text-sm text-slate-700 font-mono">{s.name}</td>
                <td className="py-3.5 text-sm text-slate-700">{s.desc}</td>
                <td className="py-3.5 text-sm text-slate-700">{s.effect}</td>
                <td className="py-3.5 text-xs text-slate-500">{s.rule}</td>
                <td className="py-3.5 text-xs text-slate-500">{s.acceptance}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  </>
);

/* ==================================================================
 * 图 06 · 关键时序与额度动作（时序说明页）
 * ================================================================== */

const SEQUENCE_ROWS = [
  {
    actor: '应用方',
    action: '调用（预占 1 槽位）',
    gateway: '鉴权 → 幂等 → 停用检查 → 额度预占',
    upstream: '—'
  },
  {
    actor: '网关',
    action: '转发上游',
    gateway: '已占用 1 个在途槽位',
    upstream: '上游推理中'
  },
  {
    actor: '上游模型',
    action: '200 SUCCESS',
    gateway: '实扣 1，释放在途',
    upstream: '200 SUCCESS（实扣 1，释放在途）'
  },
  {
    actor: '上游模型',
    action: '上游超时',
    gateway: '释放预占，零扣额',
    upstream: '504（释放预占，零扣额）'
  },
  {
    actor: '上游模型',
    action: '迟到成功包',
    gateway: '仅归档诊断池，结论不变',
    upstream: '迟到成功包 → 仅归档诊断池，结论不变'
  },
  {
    actor: '应用方',
    action: '停用后新调用',
    gateway: '403（零扣额）',
    upstream: '—'
  }
];

export const SequencePage: React.FC = () => (
  <>
    <Card
      title="关键时序"
      subtitle="应用方 → 网关 → 上游模型：预占 → 实扣 / 释放；迟到仅归档；停用后 403"
    >
      <div className="overflow-x-auto">
        <table className="w-full min-w-[720px]">
          <thead className="border-b border-slate-100">
            <tr>
              <th className="text-left text-xs font-medium text-slate-400 py-3">参与方</th>
              <th className="text-left text-xs font-medium text-slate-400 py-3">动作</th>
              <th className="text-left text-xs font-medium text-slate-400 py-3">网关</th>
              <th className="text-left text-xs font-medium text-slate-400 py-3">上游模型</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-50">
            {SEQUENCE_ROWS.map((r, i) => (
              <tr key={i}>
                <td className="py-3.5 text-sm text-slate-700 whitespace-nowrap">{r.actor}</td>
                <td className="py-3.5 text-sm text-slate-700">{r.action}</td>
                <td className="py-3.5 text-sm text-slate-500">{r.gateway}</td>
                <td className="py-3.5 text-sm text-slate-500">{r.upstream}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>

    <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
      <Card title="额度动作 · 预占" subtitle="受理即占用 1 个在途槽位">
        <div className="flex items-center gap-2 text-[12.5px] text-slate-600">
          <FileClock className="w-4 h-4 text-amber-600" />
          <span>PROCESSING 计入占用，防止并发击穿额度（R3 / R4）</span>
        </div>
      </Card>
      <Card title="额度动作 · 实扣 / 释放" subtitle="只有成功才扣额">
        <div className="flex items-center gap-2 text-[12.5px] text-slate-600">
          <ArrowRight className="w-4 h-4 text-emerald-600" />
          <span>SUCCESS 实扣 1；FAILED / TIMEOUT / REJECTED 释放预占、扣额 0（R4）</span>
        </div>
      </Card>
      <Card title="额度动作 · 迟到与停用" subtitle="结论不可改写">
        <div className="flex items-center gap-2 text-[12.5px] text-slate-600">
          <Clock className="w-4 h-4 text-rose-600" />
          <span>迟到包仅归档、补扣 = 0；停用后新调用 403（R7 / R10）</span>
        </div>
      </Card>
    </div>

    <Card title="架构与规则对照" subtitle="每条时序都对应需求文档的规则编号">
      <div className="flex flex-wrap gap-2">
        {[
          { code: 'R1', name: '凭证与租户强绑定' },
          { code: 'R2', name: '每租户凭证上限 2' },
          { code: 'R3', name: '共享日额度 · UTC 自然日' },
          { code: 'R4', name: '成功扣额 · 在途占额' },
          { code: 'R5', name: '唯一编号与幂等' },
          { code: 'R6', name: '2 秒超时熔断' },
          { code: 'R7', name: '迟到结果隔离诊断' },
          { code: 'R8', name: '额度调整安全底线' },
          { code: 'R9', name: '变更留痕且不回溯' },
          { code: 'R10', name: '停用后不受理新调用' },
          { code: 'R11', name: '细粒度可见性' },
          { code: 'R12', name: '日终对账' }
        ].map(r => (
          <span
            key={r.code}
            className="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50 px-2.5 py-1.5 text-[12px] text-slate-600"
          >
            <GitBranch className="w-3.5 h-3.5 text-emerald-600" />
            <span className="font-mono font-semibold text-slate-800">{r.code}</span>
            <span>{r.name}</span>
          </span>
        ))}
      </div>
      <div className="mt-4 flex items-center gap-2 text-[12px] text-slate-400">
        <FlaskConical className="w-4 h-4" />
        <span>
          说明页内容来自需求文档与原型蓝图，不含后端数据；真实链路结果请在「模型调用台」与「运营总控大盘」查看。
        </span>
      </div>
    </Card>
  </>
);
