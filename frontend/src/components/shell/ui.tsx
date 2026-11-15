import React from 'react';
import { LucideIcon } from 'lucide-react';

/* ================================================================== */
/* 设计令牌（对齐参考模板：深绿侧栏 + 白色卡片工作台）                      */
/* ================================================================== */

export const TONE = {
  green: { icon: 'bg-emerald-50 text-emerald-600', value: 'text-slate-900' },
  blue: { icon: 'bg-blue-50 text-blue-600', value: 'text-slate-900' },
  amber: { icon: 'bg-amber-50 text-amber-600', value: 'text-slate-900' },
  red: { icon: 'bg-rose-50 text-rose-600', value: 'text-slate-900' },
  violet: { icon: 'bg-violet-50 text-violet-600', value: 'text-slate-900' }
} as const;

export type ToneName = keyof typeof TONE;

/* ------------------------------- 卡片 ------------------------------- */

export const Card: React.FC<{
  title?: string;
  subtitle?: string;
  action?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  bodyClassName?: string;
}> = ({ title, subtitle, action, children, className = '', bodyClassName = '' }) => (
  <section className={`bg-white rounded-2xl border border-slate-200/80 shadow-sm ${className}`}>
    {(title || action) && (
      <header className="flex items-start justify-between gap-4 px-6 pt-5 pb-4">
        <div>
          {title && <h3 className="text-[15px] font-semibold text-slate-900 leading-tight">{title}</h3>}
          {subtitle && <p className="text-xs text-slate-400 mt-1">{subtitle}</p>}
        </div>
        {action}
      </header>
    )}
    <div className={`px-6 pb-6 ${bodyClassName}`}>{children}</div>
  </section>
);

/* ------------------------------ KPI 磁贴 ------------------------------ */

export const KpiTile: React.FC<{
  icon: LucideIcon;
  tone: ToneName;
  label: string;
  value: React.ReactNode;
  sub?: string;
}> = ({ icon: Icon, tone, label, value, sub }) => (
  <div className="bg-white rounded-2xl border border-slate-200/80 shadow-sm px-5 py-5">
    <div className={`w-9 h-9 rounded-xl ${TONE[tone].icon} flex items-center justify-center`}>
      <Icon className="w-4.5 h-4.5" strokeWidth={1.8} />
    </div>
    <div className="text-[13px] text-slate-400 mt-4">{label}</div>
    <div className="text-[28px] font-semibold text-slate-900 leading-none mt-1.5 tracking-tight">
      {value}
    </div>
    {sub && <div className="text-xs text-slate-400 mt-2">{sub}</div>}
  </div>
);

/* ------------------------------ 状态标签 ------------------------------ */

const STATUS_STYLE: Record<string, string> = {
  SUCCESS: 'bg-emerald-50 text-emerald-700',
  PROCESSING: 'bg-amber-50 text-amber-700',
  FAILED: 'bg-orange-50 text-orange-700',
  TIMEOUT: 'bg-rose-50 text-rose-700',
  REJECTED: 'bg-pink-50 text-pink-700',
  ACTIVE: 'bg-emerald-50 text-emerald-700',
  SUSPENDED: 'bg-rose-50 text-rose-700',
  PENDING: 'bg-slate-100 text-slate-600',
  IDEMPOTENT_HIT: 'bg-violet-50 text-violet-700',
  仅归档: 'bg-slate-100 text-slate-600',
  已追溯: 'bg-sky-50 text-sky-700',
  启用: 'bg-emerald-50 text-emerald-700',
  生效: 'bg-emerald-50 text-emerald-700',
  拒绝并留痕: 'bg-pink-50 text-pink-700',
  正常: 'bg-emerald-50 text-emerald-700',
  停用: 'bg-rose-50 text-rose-700'
};

export const StatusChip: React.FC<{ status: string; dot?: boolean }> = ({ status, dot }) => (
  <span
    className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-[11px] font-medium ${
      STATUS_STYLE[status] || 'bg-slate-100 text-slate-600'
    }`}
  >
    {dot && <span className="w-1.5 h-1.5 rounded-full bg-current" />}
    {status}
  </span>
);

/* ------------------------------ 进度条 ------------------------------ */

export const Progress: React.FC<{
  value: number;
  max: number;
  tone?: 'green' | 'amber' | 'red';
  height?: string;
}> = ({ value, max, tone = 'green', height = 'h-1.5' }) => {
  const pct = max > 0 ? Math.min(100, Math.round((value / max) * 100)) : 0;
  const bg = tone === 'red' ? 'bg-rose-500' : tone === 'amber' ? 'bg-amber-500' : 'bg-emerald-600';
  return (
    <div className={`w-full ${height} rounded-full bg-slate-100 overflow-hidden`}>
      <div className={`h-full ${bg} rounded-full`} style={{ width: `${pct}%` }} />
    </div>
  );
};

/* ------------------------------- 表格 ------------------------------- */

export const Th: React.FC<{ children: React.ReactNode; className?: string }> = ({
  children,
  className = ''
}) => <th className={`text-left text-xs font-medium text-slate-400 py-3 ${className}`}>{children}</th>;

export const Td: React.FC<{ children: React.ReactNode; className?: string }> = ({
  children,
  className = ''
}) => <td className={`py-3.5 text-sm text-slate-700 align-middle ${className}`}>{children}</td>;

/* ------------------------------- 按钮 ------------------------------- */

export const PrimaryBtn: React.FC<{
  children: React.ReactNode;
  onClick?: () => void;
  className?: string;
  type?: 'button' | 'submit';
  disabled?: boolean;
}> = ({ children, onClick, className = '', type = 'button', disabled }) => (
  <button
    type={type}
    onClick={onClick}
    disabled={disabled}
    className={`px-4 py-2 rounded-lg bg-[#1f3a30] hover:bg-[#2b4d3f] text-white text-[13px] font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${className}`}
  >
    {children}
  </button>
);

export const GhostBtn: React.FC<{
  children: React.ReactNode;
  onClick?: () => void;
  className?: string;
  disabled?: boolean;
  type?: 'button' | 'submit';
}> = ({ children, onClick, className = '', disabled, type = 'button' }) => (
  <button
    type={type}
    onClick={onClick}
    disabled={disabled}
    className={`px-4 py-2 rounded-lg border border-slate-200 bg-white hover:bg-slate-50 text-slate-700 text-[13px] font-medium transition-colors disabled:opacity-40 ${className}`}
  >
    {children}
  </button>
);

export const LinkBtn: React.FC<{
  children: React.ReactNode;
  onClick?: () => void;
  className?: string;
}> = ({ children, onClick, className = '' }) => (
  <button
    type="button"
    onClick={onClick}
    className={`text-[13px] font-medium text-[#2f6b4f] hover:text-[#1f3a30] transition-colors ${className}`}
  >
    {children}
  </button>
);

/* ------------------------------- 提示条 ------------------------------- */

export const AlertStrip: React.FC<{
  tone?: 'amber' | 'rose' | 'emerald' | 'slate';
  children: React.ReactNode;
  icon?: LucideIcon;
}> = ({ tone = 'amber', children, icon: Icon }) => {
  const map = {
    amber: 'bg-amber-50/70 border-amber-100 text-amber-800',
    rose: 'bg-rose-50/70 border-rose-100 text-rose-800',
    emerald: 'bg-emerald-50/70 border-emerald-100 text-emerald-800',
    slate: 'bg-slate-50 border-slate-200 text-slate-600'
  };
  return (
    <div
      className={`flex items-start gap-2.5 rounded-xl border px-4 py-3 text-xs leading-relaxed ${map[tone]}`}
    >
      {Icon && <Icon className="w-4 h-4 shrink-0 mt-px" />}
      <div>{children}</div>
    </div>
  );
};

/* ------------------------------- 弹层 ------------------------------- */

export const Modal: React.FC<{
  open: boolean;
  title: string;
  subtitle?: string;
  onClose: () => void;
  children: React.ReactNode;
  footer?: React.ReactNode;
}> = ({ open, title, subtitle, onClose, children, footer }) => {
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-slate-900/40 backdrop-blur-[2px]" onClick={onClose} />
      <div className="relative w-full max-w-lg bg-white rounded-2xl shadow-xl overflow-hidden">
        <header className="px-6 pt-5 pb-4 border-b border-slate-100">
          <h3 className="text-[15px] font-semibold text-slate-900">{title}</h3>
          {subtitle && <p className="text-xs text-slate-400 mt-1">{subtitle}</p>}
        </header>
        <div className="px-6 py-5 space-y-4 max-h-[60vh] overflow-y-auto">{children}</div>
        {footer && (
          <footer className="px-6 py-4 border-t border-slate-100 bg-slate-50/60 flex justify-end gap-2">
            {footer}
          </footer>
        )}
      </div>
    </div>
  );
};

/* ------------------------------- 表单 ------------------------------- */

const fieldClass =
  'w-full rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-800 outline-none transition-colors focus:border-[#2f6b4f] focus:ring-2 focus:ring-[#2f6b4f]/10';

export const LabeledInput: React.FC<{
  label: string;
  hint?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
  readOnly?: boolean;
  type?: string;
}> = ({ label, hint, value, onChange, placeholder, mono, readOnly, type = 'text' }) => (
  <label className="block">
    <span className="block text-xs font-medium text-slate-500 mb-1.5">{label}</span>
    <input
      type={type}
      value={value}
      readOnly={readOnly}
      placeholder={placeholder}
      onChange={e => onChange(e.target.value)}
      className={`${fieldClass} ${mono ? 'font-mono' : ''} ${
        readOnly ? 'bg-slate-50 text-slate-500' : 'bg-white'
      }`}
    />
    {hint && <span className="block text-[11px] text-slate-400 mt-1.5">{hint}</span>}
  </label>
);

export const LabeledTextarea: React.FC<{
  label: string;
  hint?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  rows?: number;
}> = ({ label, hint, value, onChange, placeholder, rows = 4 }) => (
  <label className="block">
    <span className="block text-xs font-medium text-slate-500 mb-1.5">{label}</span>
    <textarea
      rows={rows}
      value={value}
      placeholder={placeholder}
      onChange={e => onChange(e.target.value)}
      className={`${fieldClass} bg-white resize-none`}
    />
    {hint && <span className="block text-[11px] text-slate-400 mt-1.5">{hint}</span>}
  </label>
);

export const LabeledSelect: React.FC<{
  label: string;
  hint?: string;
  value: string;
  onChange: (v: string) => void;
  options: Array<{ value: string; label: string }>;
}> = ({ label, hint, value, onChange, options }) => (
  <label className="block">
    <span className="block text-xs font-medium text-slate-500 mb-1.5">{label}</span>
    <select
      value={value}
      onChange={e => onChange(e.target.value)}
      className={`${fieldClass} bg-white`}
    >
      {options.map(o => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
    {hint && <span className="block text-[11px] text-slate-400 mt-1.5">{hint}</span>}
  </label>
);

/* ------------------------------ 空状态 ------------------------------ */

export const EmptyHint: React.FC<{ children: React.ReactNode }> = ({ children }) => (
  <div className="text-center text-sm text-slate-400 py-12">{children}</div>
);

/* ------------------------------ 加载态 ------------------------------ */

export const LoadingHint: React.FC<{ text?: string }> = ({ text = '加载中…' }) => (
  <div className="text-center text-sm text-slate-400 py-10">{text}</div>
);
