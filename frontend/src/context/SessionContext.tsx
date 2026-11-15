import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode
} from 'react';
import {
  loginAdmin,
  loginCredential,
  logout as apiLogout,
  whoami,
  type LoginResult
} from '@/api/endpoints';
import { getToken, setToken } from '@/api/client';
import type { Identity, Role } from '@/types';

/* ==================================================================
 * 账号 / 会话上下文
 *
 * 单一后台端：顶栏中间的账号切换器决定当前身份，
 * 身份决定角色，角色决定 ① 可见页面集合 ② 数据可见范围。
 * 所有数据都由后端返回，前端不做任何业务判定。
 * ================================================================== */

export interface AccountDef {
  id: string;
  displayName: string;
  role: Role;
  /** 登录方式：账号口令 / 凭证密钥 */
  loginKind: 'password' | 'secret';
  username?: string;
  password?: string;
  /** 演示用的租户说明 */
  scope: string;
  description: string;
}

export const ROLE_LABEL: Record<Role, string> = {
  OPS_ADMIN: '平台运营',
  TENANT_ADMIN: '客户管理员',
  TENANT_APP: '客户应用'
};

export const ROLE_SCOPE: Record<Role, string> = {
  OPS_ADMIN: '数据范围：全部租户',
  TENANT_ADMIN: '数据范围：仅本企业租户',
  TENANT_APP: '数据范围：仅本凭证'
};

export const ROLE_PERMISSIONS: Record<Role, string[]> = {
  OPS_ADMIN: ['租户开通与停用', '配额增调与降额拦截', '全租户用量核查', '迟到响应诊断'],
  TENANT_ADMIN: ['管理本租户凭证', '查看共享配额大盘', '穿透本租户调用流水', '紧急停用泄露凭证'],
  TENANT_APP: ['使用凭证发起调用', '查询自身凭证请求', '携带租户内唯一编号', '无权查看他人数据']
};

/** 预置演示账号（账号名 / 口令来自后端 seedData，属公开演示值）。 */
export const ACCOUNTS: AccountDef[] = [
  {
    id: 'ops',
    displayName: '李运营',
    role: 'OPS_ADMIN',
    loginKind: 'password',
    username: 'ops',
    password: 'ops-secret',
    scope: '全平台',
    description: '平台运营：开通/停用租户、调整日额度、跨租户核查与迟到诊断'
  },
  {
    id: 'admin-a',
    displayName: '张总（极客智联）',
    role: 'TENANT_ADMIN',
    loginKind: 'password',
    username: 'admin-a',
    password: 'admin-a-secret',
    scope: '租户 A',
    description: '客户管理员 A：管理租户 A 凭证、查看共享额度与本租户全量流水'
  },
  {
    id: 'admin-b',
    displayName: '王工（智汇数安）',
    role: 'TENANT_ADMIN',
    loginKind: 'password',
    username: 'admin-b',
    password: 'admin-b-secret',
    scope: '租户 B',
    description: '客户管理员 B：与租户 A 数据完全隔离'
  },
  {
    id: 'app-a1',
    displayName: '智联客服机器人 App-A1',
    role: 'TENANT_APP',
    loginKind: 'secret',
    scope: '租户 A · 凭证 A1',
    description: '持 sk- 密钥发起调用，仅可查询自身凭证创建的流水'
  },
  {
    id: 'app-a2',
    displayName: '智联质检分析助手 App-A2',
    role: 'TENANT_APP',
    loginKind: 'secret',
    scope: '租户 A · 凭证 A2',
    description: '与 A1 共享租户日额度，但流水互相不可见'
  },
  {
    id: 'app-b1',
    displayName: '风控决策引擎 App-B1',
    role: 'TENANT_APP',
    loginKind: 'secret',
    scope: '租户 B · 凭证 B1',
    description: '租户 B 的应用，与租户 A 完全隔离'
  }
];

const SECRET_STORAGE_PREFIX = 'mq.secret.';

function readSecret(accountId: string): string {
  try {
    return localStorage.getItem(SECRET_STORAGE_PREFIX + accountId) ?? '';
  } catch {
    return '';
  }
}

function writeSecret(accountId: string, secret: string): void {
  try {
    localStorage.setItem(SECRET_STORAGE_PREFIX + accountId, secret);
  } catch {
    /* 忽略存储失败 */
  }
}

interface SessionContextValue {
  ready: boolean;
  identity: Identity | null;
  role: Role | null;
  accounts: AccountDef[];
  activeAccountId: string | null;
  switching: boolean;
  error: string | null;
  /** 切换账号；应用账号需提供 sk- 密钥 */
  switchAccount: (accountId: string, secret?: string) => Promise<void>;
  signOut: () => Promise<void>;
  /** 应用账号已记住的密钥（用于提示是否已填写） */
  hasSecret: (accountId: string) => boolean;
}

const SessionContext = createContext<SessionContextValue | undefined>(undefined);

export const SessionProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [activeAccountId, setActiveAccountId] = useState<string | null>(null);
  const [ready, setReady] = useState(false);
  const [switching, setSwitching] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 启动时用已保存的令牌恢复会话
  useEffect(() => {
    let cancelled = false;
    const token = getToken();
    if (!token) {
      setReady(true);
      return;
    }
    whoami()
      .then(id => {
        if (cancelled) return;
        setIdentity(id);
        const matched = ACCOUNTS.find(a => {
          if (a.role !== id.role) return false;
          if (a.loginKind === 'password') return a.username === id.displayName;
          return false;
        });
        setActiveAccountId(matched?.id ?? null);
      })
      .catch(() => {
        setToken(null);
      })
      .finally(() => {
        if (!cancelled) setReady(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const switchAccount = useCallback(async (accountId: string, secret?: string) => {
    const account = ACCOUNTS.find(a => a.id === accountId);
    if (!account) return;
    setSwitching(true);
    setError(null);
    try {
      let result: LoginResult;
      if (account.loginKind === 'secret') {
        const value = (secret ?? readSecret(accountId)).trim();
        if (!value) {
          throw new Error('请输入该应用的 sk- 访问密钥');
        }
        writeSecret(accountId, value);
        result = await loginCredential(value);
      } else {
        result = await loginAdmin(account.username ?? '', account.password ?? '');
      }
      setIdentity(result.identity);
      setActiveAccountId(account.id);
    } catch (e) {
      const message = e instanceof Error ? e.message : '登录失败';
      setError(message);
      throw e;
    } finally {
      setSwitching(false);
    }
  }, []);

  const signOut = useCallback(async () => {
    try {
      await apiLogout();
    } catch {
      /* 忽略登出失败，前端仍清空本地令牌 */
    }
    setIdentity(null);
    setActiveAccountId(null);
    setToken(null);
  }, []);

  const value = useMemo<SessionContextValue>(
    () => ({
      ready,
      identity,
      role: identity?.role ?? null,
      accounts: ACCOUNTS,
      activeAccountId,
      switching,
      error,
      switchAccount,
      signOut,
      hasSecret: (accountId: string) => readSecret(accountId).length > 0
    }),
    [ready, identity, activeAccountId, switching, error, switchAccount, signOut]
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
};

export function useSession(): SessionContextValue {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error('useSession 必须在 SessionProvider 内使用');
  return ctx;
}
