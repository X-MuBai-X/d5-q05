import React, { useEffect, useMemo, useState } from 'react';
import { SessionProvider, useSession } from './context/SessionContext';
import { AppShell, allowedPagesFor, ROLE_HOME, type PageId } from './components/shell/AppShell';
import { OpsOverview } from './components/pages/OpsOverview';
import { ReconcilePage } from './components/pages/ReconcilePage';
import { TenantConsole } from './components/pages/TenantConsole';
import { AppConsole } from './components/pages/AppConsole';
import { GatewayFlowPage, SequencePage } from './components/pages/FlowPages';
import { LoginScreen } from './components/pages/LoginScreen';
import { LoadingHint } from './components/shell/ui';

/**
 * 单一后台端。
 *
 * 账号 → 角色 → ① 可见页面集合 ② 数据可见范围。
 * 除角色差异外，页面之间没有任何其它差异。
 */
const Workspace: React.FC = () => {
  const { identity, role, ready } = useSession();
  const [page, setPage] = useState<PageId>('OPS_OVERVIEW');

  const allowed = useMemo(() => (role ? new Set(allowedPagesFor(role)) : new Set<PageId>()), [role]);

  // 角色切换后若当前页面不可见，回到该角色的默认首页
  useEffect(() => {
    if (!role) return;
    if (!allowed.has(page)) setPage(ROLE_HOME[role]);
  }, [allowed, page, role]);

  if (!ready) {
    return (
      <div className="min-h-screen bg-[#f4f6f5] flex items-center justify-center">
        <LoadingHint text="正在恢复会话…" />
      </div>
    );
  }

  if (!identity || !role) return <LoginScreen />;

  const renderPage = () => {
    switch (page) {
      case 'OPS_OVERVIEW':
        return <OpsOverview />;
      case 'RECONCILE':
        return <ReconcilePage />;
      case 'TENANT_CONSOLE':
        return <TenantConsole />;
      case 'APP_CONSOLE':
        return <AppConsole />;
      case 'GATEWAY_FLOW':
        return <GatewayFlowPage />;
      case 'SEQUENCE':
        return <SequencePage />;
      default:
        return null;
    }
  };

  return (
    <AppShell activePage={page} onNavigate={setPage}>
      {renderPage()}
    </AppShell>
  );
};

export default function App() {
  return (
    <SessionProvider>
      <Workspace />
    </SessionProvider>
  );
}
