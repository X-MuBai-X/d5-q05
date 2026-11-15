import React, { useMemo, useState } from 'react';
import {
  Building2,
  Check,
  ChevronDown,
  Cpu,
  FlaskConical,
  LayoutDashboard,
  LogOut,
  ScrollText,
  ShieldCheck,
  Terminal,
  Workflow
} from 'lucide-react';
import { useSession, ROLE_LABEL, ROLE_PERMISSIONS, ROLE_SCOPE, type AccountDef } from '@/context/SessionContext';
import { ErrorBanner } from '../common/ErrorBanner';
import { GhostBtn, LabeledInput, Modal, PrimaryBtn } from './ui';

export type PageId =
  | 'OPS_OVERVIEW'
  | 'RECONCILE'
  | 'TENANT_CONSOLE'
  | 'APP_CONSOLE'
  | 'GATEWAY_FLOW'
  | 'SEQUENCE';

interface NavItem {
  id: PageId;
  label: string;
  icon: React.ComponentType<{ className?: string; strokeWidth?: number }>;
  /** 图号，对应原型蓝图 */
  blueprint: string;
}

/**
 * 角色 → 可见页面集合（契约 §9）。
 * 说明页（图 04 / 图 06）对所有角色开放，其余页面按角色区分。
 */
export const ROLE_NAV: Record<string, NavItem[]> = {
  OPS_ADMIN: [
    { id: 'OPS_OVERVIEW', label: '运营总控大盘', icon: LayoutDashboard, blueprint: '图 01' },
    { id: 'RECONCILE', label: '日终核查与对账', icon: Terminal, blueprint: '图 05' },
    { id: 'GATEWAY_FLOW', label: '受理流程说明', icon: Workflow, blueprint: '图 04' },
    { id: 'SEQUENCE', label: '时序与额度动作', icon: ScrollText, blueprint: '图 06' }
  ],
  TENANT_ADMIN: [
    { id: 'TENANT_CONSOLE', label: '租户控制台', icon: Building2, blueprint: '图 02' },
    { id: 'GATEWAY_FLOW', label: '受理流程说明', icon: Workflow, blueprint: '图 04' },
    { id: 'SEQUENCE', label: '时序与额度动作', icon: ScrollText, blueprint: '图 06' }
  ],
  TENANT_APP: [
    { id: 'APP_CONSOLE', label: '应用调用台', icon: FlaskConical, blueprint: '图 03' },
    { id: 'GATEWAY_FLOW', label: '受理流程说明', icon: Workflow, blueprint: '图 04' },
    { id: 'SEQUENCE', label: '时序与额度动作', icon: ScrollText, blueprint: '图 06' }
  ]
};

export const ROLE_HOME: Record<string, PageId> = {
  OPS_ADMIN: 'OPS_OVERVIEW',
  TENANT_ADMIN: 'TENANT_CONSOLE',
  TENANT_APP: 'APP_CONSOLE'
};

export const PAGE_TITLES: Record<PageId, string> = {
  OPS_OVERVIEW: '运营总控大盘',
  RECONCILE: '日终核查与对账',
  TENANT_CONSOLE: '租户控制台',
  APP_CONSOLE: '应用调用台',
  GATEWAY_FLOW: '网关受理流程与故障注入说明',
  SEQUENCE: '关键时序与额度动作'
};

export const allowedPagesFor = (role: string): PageId[] => ROLE_NAV[role].map(n => n.id);

export interface AppShellProps {
  activePage: PageId;
  onNavigate: (page: PageId) => void;
  children: React.ReactNode;
}

/** 账号切换器：登录其他账号 → 角色与数据范围随之变化。 */
const AccountSwitcher: React.FC<{ onPicked: (account: AccountDef) => void }> = ({ onPicked }) => {
  const { accounts, identity, activeAccountId } = useSession();
  const [open, setOpen] = useState(false);

  const active = accounts.find(a => a.id === activeAccountId);
  const label = active?.displayName ?? identity?.displayName ?? '未登录';

  return (
    <div className="flex-none relative">
      <button
        onClick={() => setOpen(v => !v)}
        className="flex items-center gap-2.5 bg-slate-100 rounded-xl p-1 pl-2.5 pr-2 hover:bg-slate-200/70 transition-colors"
      >
        <span className="w-7 h-7 rounded-lg bg-white text-[12px] font-semibold text-[#1f3a30] grid place-items-center shadow-sm shrink-0">
          {label.slice(0, 1)}
        </span>
        <span className="text-left leading-tight whitespace-nowrap">
          <span className="block text-[13px] font-medium text-slate-900">{label}</span>
          <span className="block text-[11px] text-slate-500">
            {identity ? ROLE_LABEL[identity.role] : '未登录'}
          </span>
        </span>
        <ChevronDown className="w-3.5 h-3.5 text-slate-400 shrink-0" />
      </button>

      {open && (
        <div className="absolute left-1/2 -translate-x-1/2 mt-2 w-[320px] bg-white rounded-xl border border-slate-200 shadow-lg overflow-hidden z-40">
          <div className="px-4 py-2.5 text-[11px] text-slate-400 border-b border-slate-100">
            账号切换（账号决定角色，角色决定可见页面与数据范围）
          </div>
          <div className="max-h-[60vh] overflow-y-auto">
            {accounts.map(p => (
              <button
                key={p.id}
                onClick={() => {
                  setOpen(false);
                  onPicked(p);
                }}
                className={`w-full text-left px-4 py-2.5 hover:bg-slate-50 transition-colors ${
                  p.id === activeAccountId ? 'bg-emerald-50/60' : ''
                }`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="text-[13px] font-medium text-slate-800">{p.displayName}</span>
                  <span className="text-[10.5px] text-slate-400 shrink-0">{ROLE_LABEL[p.role]}</span>
                </div>
                <div className="text-[11px] text-slate-400 mt-0.5 leading-relaxed">{p.description}</div>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
};

export const AppShell: React.FC<AppShellProps> = ({ activePage, onNavigate, children }) => {
  const { identity, role, switchAccount, signOut, error, switching } = useSession();
  const [pendingSecret, setPendingSecret] = useState<AccountDef | null>(null);
  const [secretInput, setSecretInput] = useState('');
  const [localError, setLocalError] = useState<string | null>(null);

  const nav = useMemo(() => (role ? ROLE_NAV[role] : []), [role]);

  const onPicked = async (account: AccountDef) => {
    setLocalError(null);
    try {
      if (account.loginKind === 'secret') {
        setPendingSecret(account);
        setSecretInput('');
        return;
      }
      await switchAccount(account.id);
      onNavigate(ROLE_HOME[account.role]);
    } catch {
      /* 错误由 session.error 展示 */
    }
  };

  const submitSecret = async () => {
    if (!pendingSecret) return;
    setLocalError(null);
    try {
      await switchAccount(pendingSecret.id, secretInput);
      onNavigate(ROLE_HOME[pendingSecret.role]);
      setPendingSecret(null);
    } catch (e) {
      setLocalError(e instanceof Error ? e.message : '登录失败');
    }
  };

  const navButton = (item: NavItem) => {
    const active = activePage === item.id;
    const Icon = item.icon;
    return (
      <button
        key={item.id}
        onClick={() => onNavigate(item.id)}
        className={`relative w-full flex items-center gap-3 h-10 px-3 rounded-lg text-[13px] transition-colors ${
          active ? 'bg-white/10 text-white font-medium' : 'text-white/65 hover:text-white hover:bg-white/5'
        }`}
      >
        {active && <span className="absolute left-0 top-1/2 -translate-y-1/2 w-[3px] h-4 rounded-r bg-[#d9f26b]" />}
        <Icon className="w-4 h-4" strokeWidth={1.8} />
        <span>{item.label}</span>
        <span className="ml-auto text-[10px] text-white/35">{item.blueprint}</span>
      </button>
    );
  };

  const currentNav = nav.find(n => n.id === activePage);

  return (
    <div className="h-screen overflow-hidden bg-[#f4f6f5] flex flex-col">
      <header className="h-[60px] bg-white border-b border-slate-200/80 flex items-center gap-3 px-5 shrink-0 relative z-30">
        <div className="flex items-center gap-3 min-w-0 flex-1">
          <div className="w-9 h-9 rounded-xl bg-[#1f3a30] flex items-center justify-center shrink-0">
            <Cpu className="w-4.5 h-4.5 text-white" strokeWidth={1.8} />
          </div>
          <div className="leading-tight min-w-0 flex-1">
            <div className="text-[15px] font-semibold text-slate-900 truncate whitespace-nowrap">
              配额治理后台
            </div>
            <div className="text-[10px] tracking-[0.16em] text-slate-400 truncate hidden lg:block">
              JINGHUA MODEL GATEWAY
            </div>
          </div>
        </div>

        {/* 顶栏中间：账号切换器 */}
        <AccountSwitcher onPicked={onPicked} />

        <div className="flex-1 flex items-center justify-end gap-3 text-xs text-slate-500 whitespace-nowrap">
          {identity && (
            <button
              onClick={() => signOut()}
              className="flex items-center gap-1.5 text-slate-500 hover:text-slate-800 transition-colors"
            >
              <LogOut className="w-3.5 h-3.5" />
              登出
            </button>
          )}
          <span className="w-2 h-2 rounded-full bg-emerald-500 shrink-0" />
          <span className="hidden xl:inline">数据来自后端 modelquota</span>
        </div>
      </header>

      <div className="flex-1 flex min-h-0">
        <aside className="w-[248px] shrink-0 bg-[#1f3a30] flex flex-col h-full overflow-hidden">
          <div className="px-5 pt-6 pb-5 shrink-0">
            <div className="text-[15px] font-semibold text-white">模型配额工作台</div>
            <div className="text-[10px] tracking-[0.16em] text-white/35 mt-1">
              QUOTA GOVERNANCE CONSOLE
            </div>
          </div>

          <div className="flex-1 min-h-0 overflow-y-auto pb-2">
            <nav className="px-3 space-y-1">{nav.map(navButton)}</nav>
          </div>

          {role && (
            <div className="shrink-0">
              <div className="mx-3 mb-3 rounded-xl bg-white/[0.06] p-3.5">
                <div className="flex items-center gap-2 text-[13px] text-white">
                  <ShieldCheck className="w-4 h-4 text-[#d9f26b]" strokeWidth={1.8} />
                  当前角色权限
                </div>
                <div className="mt-1.5 text-[11.5px] text-[#d9f26b]/85">
                  {ROLE_LABEL[role]} · {identity?.displayName ?? ''}
                </div>
                <div className="mt-0.5 text-[11px] text-white/45">{ROLE_SCOPE[role]}</div>
                <ul className="mt-2.5 space-y-1.5">
                  {ROLE_PERMISSIONS[role].map(p => (
                    <li
                      key={p}
                      className="flex items-start gap-2 text-[11.5px] text-white/60 leading-relaxed"
                    >
                      <Check className="w-3 h-3 mt-0.5 text-[#d9f26b] shrink-0" strokeWidth={2.5} />
                      <span>{p}</span>
                    </li>
                  ))}
                </ul>
              </div>
              <div className="px-5 pb-4 text-[10.5px] text-white/30 leading-relaxed">
                越权与额度判定全部由后端裁决，前端只展示后端返回的数据与错误码。
              </div>
            </div>
          )}
        </aside>

        <main className="flex-1 min-w-0 overflow-y-auto overflow-x-hidden h-full">
          <div className="px-8 pt-7 pb-5 flex items-start justify-between gap-6">
            <div>
              <div className="text-[11px] tracking-[0.2em] text-slate-400 font-medium whitespace-nowrap">
                {currentNav?.blueprint} · {role ? ROLE_LABEL[role] : ''} WORKSPACE
              </div>
              <h1 className="text-[26px] font-semibold text-slate-900 mt-1.5 tracking-tight">
                {PAGE_TITLES[activePage]}
              </h1>
            </div>
            <div className="flex items-center gap-2.5 rounded-xl border border-slate-200 bg-white pl-2 pr-3 py-1.5 whitespace-nowrap shrink-0">
              <span className="w-7 h-7 rounded-lg bg-emerald-50 text-emerald-600 grid place-items-center text-[13px] font-medium shrink-0">
                {(identity?.displayName ?? '未').slice(0, 1)}
              </span>
              <span className="text-left leading-tight">
                <span className="block text-[13px] font-medium text-slate-800">
                  {identity?.displayName ?? '未登录'}
                </span>
                <span className="block text-[11px] text-slate-400">
                  {identity ? ROLE_LABEL[identity.role] : ''}
                </span>
              </span>
            </div>
          </div>

          <div className="px-8 pb-12 space-y-5">
            <ErrorBanner
              error={error ? { code: 'ACCOUNT_SWITCH', message: error } : null}
            />
            {children}
          </div>
        </main>
      </div>

      {/* 应用账号需要 sk- 密钥才能登录 */}
      <Modal
        open={Boolean(pendingSecret)}
        title={`登录 ${pendingSecret?.displayName ?? ''}`}
        subtitle="客户应用以凭证密钥登录（密钥仅在管理员创建凭证时返回一次）"
        onClose={() => setPendingSecret(null)}
        footer={
          <>
            <GhostBtn onClick={() => setPendingSecret(null)}>取消</GhostBtn>
            <PrimaryBtn onClick={submitSecret} disabled={switching}>
              {switching ? '登录中…' : '登录'}
            </PrimaryBtn>
          </>
        }
      >
        <LabeledInput
          label="访问密钥（sk-…）"
          value={secretInput}
          onChange={setSecretInput}
          placeholder="sk-xxxxxxxx-xxxx"
          mono
          hint="该密钥保存在浏览器本地，用于切换到该应用账号"
        />
        <ErrorBanner error={localError ? { code: 'LOGIN_FAILED', message: localError } : null} />
      </Modal>
    </div>
  );
};
