import { useCallback, useEffect, useState } from "react";
import { AlertCircle, Loader2, RefreshCw } from "lucide-react";
import { buildAppUrl, getRelativeRoute } from "@/lib/basePath";
import { ThemeProvider } from "@/contexts/ThemeContext";
import { AuthProvider, useAuth } from "@/contexts/AuthContext";
import { NodeProvider, useNode } from "@/contexts/NodeContext";
import { ToastProvider } from "@/components/ui/Toast";
import { Button } from "@/components/ui/primitives";
import { AppShell, type AppRoute } from "@/components/AppShell";
import { useDashboardStream } from "@/hooks/useDashboardStream";
import { SetupPage } from "@/pages/SetupPage";
import { LoginPage } from "@/pages/LoginPage";
import { DashboardPage } from "@/pages/DashboardPage";
import { NodesPage } from "@/pages/NodesPage";
import { InboundsPage } from "@/pages/InboundsPage";
import { MembersPage } from "@/pages/MembersPage";
import { CertificatesPage } from "@/pages/CertificatesPage";
import { CoreLogsPage } from "@/pages/CoreLogsPage";
import { ConnectionsPage } from "@/pages/ConnectionsPage";
import { SettingsPage } from "@/pages/SettingsPage";
import { RoadmapPage } from "@/pages/RoadmapPage";

const VALID_ROUTES: ReadonlySet<AppRoute> = new Set([
  "/",
  "/nodes",
  "/inbounds",
  "/members",
  "/certs",
  "/core",
  "/connections",
  "/settings",
  "/routing",
  "/sync",
]);

function parseRoute(): AppRoute {
  const rel = getRelativeRoute() as AppRoute;
  return VALID_ROUTES.has(rel) ? rel : "/";
}

function WorkspaceShell() {
  const [route, setRoute] = useState<AppRoute>(() => parseRoute());
  const { activeNodeId } = useNode();
  const live = useDashboardStream(true, activeNodeId);

  useEffect(() => {
    const onPopState = () => setRoute(parseRoute());
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  const navigate = useCallback((next: AppRoute) => {
    setRoute(next);
    const targetUrl = buildAppUrl(next);
    if (window.location.pathname !== targetUrl) {
      window.history.pushState(null, "", targetUrl);
    }
  }, []);

  return (
    <AppShell
      route={route}
      onNavigate={navigate}
      core={live.core}
      metrics={live.metrics}
      sseConnected={live.connected}
    >
      {route === "/" && <DashboardPage live={live} onNavigate={navigate} />}
      {route === "/nodes" && <NodesPage onNavigate={navigate} />}
      {route === "/inbounds" && <InboundsPage onNavigate={navigate} />}
      {route === "/members" && <MembersPage />}
      {route === "/certs" && <CertificatesPage onNavigate={navigate} />}
      {route === "/core" && <CoreLogsPage live={live} onNavigate={navigate} />}
      {route === "/connections" && <ConnectionsPage onNavigate={navigate} />}
      {route === "/settings" && <SettingsPage />}
      {(route === "/routing" || route === "/sync") && (
        <RoadmapPage route={route} onNavigate={navigate} />
      )}
    </AppShell>
  );
}

function AuthenticatedWorkspace() {
  return (
    <NodeProvider>
      <WorkspaceShell />
    </NodeProvider>
  );
}

function AuthGate() {
  const { loading, error, status, refresh } = useAuth();

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-canvas text-ink-secondary">
        <div className="flex items-center gap-2.5 text-sm">
          <Loader2 className="h-4 w-4 animate-spin text-accent" />
          <span className="font-mono">正在连接 sbox-panel 控制面...</span>
        </div>
      </div>
    );
  }

  if (error && !status) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-canvas p-4 text-ink">
        <div className="w-full max-w-md rounded-lg border border-hairline bg-surface p-6 text-center">
          <AlertCircle className="mx-auto h-7 w-7 text-danger" />
          <h1 className="mt-3 text-base font-semibold">无法连接至面板后端</h1>
          <p className="mt-1.5 text-xs text-ink-secondary">{error}</p>
          <Button
            variant="secondary"
            size="sm"
            className="mt-4"
            onClick={() => void refresh()}
          >
            <RefreshCw className="h-3.5 w-3.5" />
            重新检测
          </Button>
        </div>
      </div>
    );
  }

  if (!status?.initialized) {
    return <SetupPage />;
  }

  if (!status.authenticated) {
    return <LoginPage />;
  }

  return <AuthenticatedWorkspace />;
}

export function App() {
  return (
    <ThemeProvider>
      <ToastProvider>
        <AuthProvider>
          <AuthGate />
        </AuthProvider>
      </ToastProvider>
    </ThemeProvider>
  );
}
