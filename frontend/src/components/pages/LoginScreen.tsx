import React, { useState } from 'react';
import { Cpu, KeyRound, Lock, LogIn, User } from 'lucide-react';
import { useSession } from '@/context/SessionContext';
import { ErrorBanner } from '@/components/common/ErrorBanner';
import { LabeledInput, PrimaryBtn } from '@/components/shell/ui';

/**
 * 登录页（真实网关场景）。
 *
 * 两种登录方式，与真实系统一致：
 *   - **用户登录**（默认展示）：运营 / 客户管理员用账号 + 口令登录；
 *   - **密钥登录**：客户应用用管理员创建凭证时拿到的 sk- 访问密钥登录。
 *
 * 两个页面可以自由切换：
 *   - 用户登录页的**登录按钮正下方**放一个「使用访问密钥登录」超链接；
 *   - 密钥登录页的登录按钮下方放一个「返回用户登录」超链接。
 *
 * 口令与密钥**都交给后端校验**，前端只展示后端返回的错误码与文案。
 */
type LoginMode = 'user' | 'secret';

export const LoginScreen: React.FC<{ initialMode?: LoginMode }> = ({ initialMode = 'user' }) => {
  const { loginWithPassword, loginWithSecret, switching, error } = useSession();

  const [mode, setMode] = useState<LoginMode>(initialMode);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [secret, setSecret] = useState('');
  const [localError, setLocalError] = useState<string | null>(null);

  const switchMode = (next: LoginMode) => {
    setMode(next);
    setLocalError(null);
  };

  const submitUser = async (e: React.FormEvent) => {
    e.preventDefault();
    setLocalError(null);
    try {
      await loginWithPassword(username, password);
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : '登录失败');
    }
  };

  const submitSecret = async (e: React.FormEvent) => {
    e.preventDefault();
    setLocalError(null);
    try {
      await loginWithSecret(secret);
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : '登录失败');
    }
  };

  const banner = localError ?? error;

  return (
    <div className="min-h-screen bg-[#f4f6f5] flex items-center justify-center px-5 py-10">
      <div className="w-full max-w-[420px]">
        <div className="flex items-center gap-3 mb-6">
          <div className="w-10 h-10 rounded-xl bg-[#1f3a30] flex items-center justify-center">
            <Cpu className="w-5 h-5 text-white" strokeWidth={1.8} />
          </div>
          <div>
            <div className="text-[17px] font-semibold text-slate-900">配额治理后台</div>
            <div className="text-[11px] tracking-[0.16em] text-slate-400">
              JINGHUA MODEL GATEWAY
            </div>
          </div>
        </div>

        {mode === 'user' ? (
          /* -------------------- 用户登录（默认展示） -------------------- */
          <form
            onSubmit={submitUser}
            className="bg-white rounded-2xl border border-slate-200/80 shadow-sm p-6"
          >
            <div className="text-[14px] font-semibold text-slate-900">用户登录</div>
            <div className="text-[12px] text-slate-400 mt-1">
              平台运营与客户管理员使用账号口令登录。
            </div>

            <div className="mt-5 space-y-3">
              <LabeledInput
                label="账号"
                value={username}
                onChange={setUsername}
                placeholder="ops / admin-a / admin-b"
                autoComplete="username"
                mono
              />
              <LabeledInput
                label="口令"
                value={password}
                onChange={setPassword}
                placeholder="请输入口令"
                type="password"
                autoComplete="current-password"
              />
            </div>

            <button type="submit" className="mt-4 w-full" disabled={switching}>
              <PrimaryBtn disabled={switching}>
                <span className="inline-flex items-center gap-1.5">
                  <LogIn className="w-3.5 h-3.5" />
                  {switching ? '登录中…' : '登录'}
                </span>
              </PrimaryBtn>
            </button>

            {/* 登录按钮正下方：切换到密钥登录 */}
            <div className="mt-3 text-center">
              <button
                type="button"
                onClick={() => switchMode('secret')}
                className="inline-flex items-center gap-1 text-[12.5px] text-[#2f5bd7] hover:underline underline-offset-2 transition-colors"
              >
                <KeyRound className="w-3.5 h-3.5" />
                使用访问密钥登录
              </button>
            </div>

            <div className="mt-4">
              <ErrorBanner error={banner ? { code: 'LOGIN_FAILED', message: banner } : null} />
            </div>
          </form>
        ) : (
          /* -------------------- 密钥登录 -------------------- */
          <form
            onSubmit={submitSecret}
            className="bg-white rounded-2xl border border-slate-200/80 shadow-sm p-6"
          >
            <div className="text-[14px] font-semibold text-slate-900">访问密钥登录</div>
            <div className="text-[12px] text-slate-400 mt-1">
              客户应用使用管理员创建凭证时返回的 sk- 密钥登录；密钥绑定租户，越权由后端拦截。
            </div>

            <div className="mt-5 space-y-3">
              <LabeledInput
                label="访问密钥"
                value={secret}
                onChange={setSecret}
                placeholder="sk-xxxxxxxx-xxxx"
                mono
              />
            </div>

            <button type="submit" className="mt-4 w-full" disabled={switching}>
              <PrimaryBtn disabled={switching}>
                <span className="inline-flex items-center gap-1.5">
                  <KeyRound className="w-3.5 h-3.5" />
                  {switching ? '登录中…' : '登录'}
                </span>
              </PrimaryBtn>
            </button>

            {/* 登录按钮正下方：切回用户登录 */}
            <div className="mt-3 text-center">
              <button
                type="button"
                onClick={() => switchMode('user')}
                className="inline-flex items-center gap-1 text-[12.5px] text-[#2f5bd7] hover:underline underline-offset-2 transition-colors"
              >
                <User className="w-3.5 h-3.5" />
                返回用户登录
              </button>
            </div>

            <div className="mt-4">
              <ErrorBanner error={banner ? { code: 'LOGIN_FAILED', message: banner } : null} />
            </div>
          </form>
        )}

        <div className="mt-4 flex items-start gap-2 text-[11.5px] text-slate-400">
          <Lock className="w-3.5 h-3.5 mt-0.5 shrink-0" />
          <span>
            口令与访问密钥均由服务端校验，前端不做任何本地比对；
            越权与额度判定也全部在后端完成。
          </span>
        </div>
      </div>
    </div>
  );
};
