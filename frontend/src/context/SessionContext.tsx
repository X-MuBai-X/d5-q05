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
 *
 * 登录入口有两种（与真实网关场景一致）：
 *   - 用户登录：运营 / 客户管理员用**账号 + 口令**登录；
 *   - 密钥登录：客户应用用管理员创建凭证时拿到的 **sk- 密钥**登录。
 * 两种登录都直接向后端校验，前端不预置口令、不做本地比对。
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

/**
 * 预置演示账号（账号名 / 口令来自后端 seedData，属公开演示值）。
 *
 * 这里**只用于顶栏的账号切换器**（演示时在不同角色间快速切换），
 * 登录页本身不预置口令：用户必须自己输入账号与口令，由后端校验。
 */
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
  /** 用户登录：用账号 + 口令登录（口令由后端校验） */
  loginWithPassword: (username: string, password: string) => Promise<void>;
  /** 密钥登录：用 sk- 访问密钥登录（密钥由后端校验） */
  loginWithSecret: (secret: string) => Promise<void>;
  signOut: () => Promise<void>;
  /** 应用账号已记住的密钥（用于提示是否已填写） */
  hasSecret: (accountId: string) => boolean;
}

const SessionContext = createContext<SessionContextValue | undefined>(undefined);

/**
 * 由后端返回的身份反查出一条「账号」记录，供顶栏切换器显示与切换。
 *
 * 优先匹配预置账号（演示时能显示中文角色名与数据范围说明）；
 * 匹配不到（例如用不在预置表里的账号登录）就按身份现场生成一条，
 * 保证登录成功后顶栏依然能正确显示当前身份。
 */
function accountFor(identity: Identity): AccountDef {
  const preset = ACCOUNTS.find(a => {
    if (a.role !== identity.role) return false;
    if (a.loginKind === 'password') return a.username === identity.subjectId;
    return false;
  });
  if (preset) return preset;

  const dynamicId = `${identity.role.toLowerCase()}:${identity.subjectId}`;
  return {
    id: dynamicId,
    displayName: identity.displayName || identity.subjectId,
    role: identity.role,
    loginKind: identity.role === 'TENANT_APP' ? 'secret' : 'password',
    username: identity.role === 'TENANT_APP' ? undefined : identity.subjectId,
    scope: identity.tenantName || identity.tenantCode || ROLE_SCOPE[identity.role].replace('数据范围：', ''),
    description: `${ROLE_LABEL[identity.role]}：${identity.subjectId}`
  };
}

export const SessionProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [activeAccountId, setActiveAccountId] = useState<string | null>(null);
  const [ready, setReady] = useState(false);
  const [switching, setSwitching] = useState(false);
  const [error, setError] = useState<string | null>(null);
  /** 登录后按身份生成的账号条目（不在预置表里的账号也能正常显示） */
  const [dynamicAccount, setDynamicAccount] = useState<AccountDef | null>(null);

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
        const account = accountFor(id);
        setActiveAccountId(account.id);
        if (!ACCOUNTS.some(a => a.id === account.id)) setDynamicAccount(account);
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

  /**
   * 用户登录：输入账号与口令，由**后端**校验。
   *
   * 前端不预置口令、不做本地比对 —— 口令错了就是后端返回 401，
   * 前端只负责把后端给的错误码与文案原样展示。
   */
  const loginWithPassword = useCallback(async (username: string, password: string) => {
    const name = username.trim();
    if (!name) {
      const message = '请输入账号';
      setError(message);
      throw new Error(message);
    }
    if (!password) {
      const message = '请输入口令';
      setError(message);
      throw new Error(message);
    }
    setSwitching(true);
    setError(null);
    try {
      const result = await loginAdmin(name, password);
      setIdentity(result.identity);
      const account = accountFor(result.identity);
      setActiveAccountId(account.id);
      if (!ACCOUNTS.some(a => a.id === account.id)) setDynamicAccount(account);
    } catch (e) {
      const message = e instanceof Error ? e.message : '登录失败';
      setError(message);
      throw e;
    } finally {
      setSwitching(false);
    }
  }, []);

  /**
   * 密钥登录：输入 sk- 访问密钥，由**后端**校验。
   *
   * 密钥本身决定身份（凭证绑定租户），因此这里不需要用户再选应用；
   * 登录成功后按后端返回的身份确定角色与数据范围。
   */
  const loginWithSecret = useCallback(async (secret: string) => {
    const value = secret.trim();
    if (!value) {
      const message = '请输入访问密钥';
      setError(message);
      throw new Error(message);
    }
    setSwitching(true);
    setError(null);
    try {
      const result = await loginCredential(value);
      setIdentity(result.identity);
      const account = accountFor(result.identity);
      // 密钥按「身份」记住：下次用同一个密钥登录时可直接切换
      writeSecret(account.id, value);
      setActiveAccountId(account.id);
      if (!ACCOUNTS.some(a => a.id === account.id)) setDynamicAccount(account);
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

  const accounts = useMemo(
    () => (dynamicAccount ? [...ACCOUNTS, dynamicAccount] : ACCOUNTS),
    [dynamicAccount]
  );

  const value = useMemo<SessionContextValue>(
    () => ({
      ready,
      identity,
      role: identity?.role ?? null,
      accounts,
      activeAccountId,
      switching,
      error,
      switchAccount,
      loginWithPassword,
      loginWithSecret,
      signOut,
      hasSecret: (accountId: string) => readSecret(accountId).length > 0
    }),
    [
      ready,
      identity,
      accounts,
      activeAccountId,
      switching,
      error,
      switchAccount,
      loginWithPassword,
      loginWithSecret,
      signOut
    ]
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
};

export function useSession(): SessionContextValue {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error('useSession 必须在 SessionProvider 内使用');
  return ctx;
}
