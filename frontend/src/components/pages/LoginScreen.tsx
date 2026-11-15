import React, { useState } from 'react';
import { Cpu, KeyRound, LogIn, UserCog } from 'lucide-react';
import { useSession, ROLE_LABEL, type AccountDef } from '@/context/SessionContext';
import { ErrorBanner } from '@/components/common/ErrorBanner';
import { GhostBtn, LabeledInput, Modal, PrimaryBtn } from '@/components/shell/ui';

/**
 * 登录 / 账号选择页。
 *
 * 账号决定角色，角色决定可见页面与数据范围；登录后顶栏中间的账号切换器可随时切换。
 */
export const LoginScreen: React.FC = () => {
  const { accounts, switchAccount, switching, error } = useSession();
  const [pending, setPending] = useState<AccountDef | null>(null);
  const [secret, setSecret] = useState('');
  const [localError, setLocalError] = useState<string | null>(null);

  const login = async (account: AccountDef, secretValue?: string) => {
    setLocalError(null);
    try {
      await switchAccount(account.id, secretValue);
    } catch (e) {
      setLocalError(e instanceof Error ? e.message : '登录失败');
    }
  };

  return (
    <div className="min-h-screen bg-[#f4f6f5] flex items-center justify-center px-5 py-10">
      <div className="w-full max-w-[720px]">
        <div className="flex items-center gap-3 mb-6">
          <div className="w-10 h-10 rounded-xl bg-[#1f3a30] flex items-center justify-center">
            <Cpu className="w-5 h-5 text-white" strokeWidth={1.8} />
          </div>
          <div>
            <div className="text-[17px] font-semibold text-slate-900">配额治理后台</div>
            <div className="text-[11px] tracking-[0.16em] text-slate-400">
              JINGHUA MODEL GATEWAY · 企业模型服务配额治理
            </div>
          </div>
        </div>

        <div className="bg-white rounded-2xl border border-slate-200/80 shadow-sm p-6">
          <div className="text-[14px] font-semibold text-slate-900">选择账号登录</div>
          <div className="text-[12px] text-slate-400 mt-1">
            账号决定角色，角色决定可见页面集合与数据可见范围；越权拦截全部在后端。
          </div>

          <div className="mt-5 grid grid-cols-1 md:grid-cols-2 gap-3">
            {accounts.map(account => (
              <div
                key={account.id}
                className="rounded-xl border border-slate-200/80 p-4 flex flex-col gap-2"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="text-[13.5px] font-medium text-slate-900">{account.displayName}</span>
                  <span className="text-[10.5px] text-slate-400">{ROLE_LABEL[account.role]}</span>
                </div>
                <div className="text-[11.5px] text-slate-500 leading-relaxed">{account.description}</div>
                <div className="text-[11px] text-slate-400">数据范围：{account.scope}</div>
                <div className="mt-1">
                  {account.loginKind === 'password' ? (
                    <PrimaryBtn onClick={() => login(account)} disabled={switching}>
                      <span className="inline-flex items-center gap-1.5">
                        <LogIn className="w-3.5 h-3.5" />
                        {switching ? '登录中…' : '登录'}
                      </span>
                    </PrimaryBtn>
                  ) : (
                    <GhostBtn onClick={() => setPending(account)}>
                      <span className="inline-flex items-center gap-1.5">
                        <KeyRound className="w-3.5 h-3.5" />
                        用 sk- 密钥登录
                      </span>
                    </GhostBtn>
                  )}
                </div>
              </div>
            ))}
          </div>

          <div className="mt-5">
            <ErrorBanner error={error ? { code: 'LOGIN_FAILED', message: error } : null} />
            <ErrorBanner error={localError ? { code: 'LOGIN_FAILED', message: localError } : null} />
          </div>
        </div>

        <div className="mt-4 flex items-center gap-2 text-[11.5px] text-slate-400">
          <UserCog className="w-3.5 h-3.5" />
          <span>
            运营 / 管理员使用账号口令登录；客户应用使用管理员创建凭证时返回的 sk- 密钥登录。
          </span>
        </div>
      </div>

      <Modal
        open={Boolean(pending)}
        title={`登录 ${pending?.displayName ?? ''}`}
        subtitle="客户应用以访问密钥登录"
        onClose={() => setPending(null)}
        footer={
          <>
            <GhostBtn onClick={() => setPending(null)}>取消</GhostBtn>
            <PrimaryBtn
              onClick={async () => {
                if (!pending) return;
                await login(pending, secret);
                setPending(null);
              }}
              disabled={switching}
            >
              {switching ? '登录中…' : '登录'}
            </PrimaryBtn>
          </>
        }
      >
        <LabeledInput
          label="访问密钥（sk-…）"
          value={secret}
          onChange={setSecret}
          mono
          placeholder="sk-xxxxxxxx-xxxx"
          hint="密钥由客户管理员创建凭证时返回一次"
        />
      </Modal>
    </div>
  );
};
