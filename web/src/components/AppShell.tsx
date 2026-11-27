import React, { useEffect, useRef, useState } from "react";
import {
  Activity,
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CloudUpload,
  Cpu,
  GitBranch,
  KeyRound,
  LogOut,
  Menu,
  Moon,
  Network,
  Radio,
  Server,
  Settings,
  Shield,
  Sun,
  Users,
  X,
} from "lucide-react";
import { cn } from "@/lib/cn";
import { formatSpeed } from "@/lib/format";
import { useTheme } from "@/contexts/ThemeContext";
import { useAuth } from "@/contexts/AuthContext";
import { useNode } from "@/contexts/NodeContext";
import { Badge, Button, StatusDot } from "@/components/ui/primitives";
import type { CoreStatus, MetricsSnapshot } from "@/types/api";

export type ViewMode = "master" | "node";

export type MasterRoute = "/" | "/members" | "/subscriptions" | "/settings";

export type NodeRoute =
  | "/node/dashboard"
  | "/inbounds"
  | "/certs"
  | "/routing"
  | "/core"
  | "/connections";

export type AppRoute = MasterRoute | NodeRoute;

interface NavItem {
  route: AppRoute;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
}

export const MASTER_NAV_ITEMS: NavItem[] = [
  { route: "/", label: "集群与节点", icon: Server },
  { route: "/members", label: "成员管理", icon: Users },
  { route: "/subscriptions", label: "远端同步", icon: CloudUpload },
  { route: "/settings", label: "安全与设置", icon: Settings },
];

export const NODE_NAV_ITEMS: NavItem[] = [
  { route: "/node/dashboard", label: "节点仪表盘", icon: Activity },
  { route: "/inbounds", label: "入站节点", icon: Radio },
  { route: "/certs", label: "证书中心", icon: Shield },
  { route: "/routing", label: "出站与分流", icon: GitBranch },
  { route: "/core", label: "核心与日志", icon: Cpu },
  { route: "/connections", label: "活跃连接", icon: Network },
];

const MASTER_ROUTE_SET: ReadonlySet<AppRoute> = new Set([
  "/",
  "/members",
  "/subscriptions",
  "/settings",
]);

export function getViewMode(route: AppRoute): ViewMode {
  return MASTER_ROUTE_SET.has(route) ? "master" : "node";
}

const ROUTE_TITLES: Record<AppRoute, string> = {
  "/": "集群与节点",
  "/members": "成员管理",
  "/subscriptions": "远端同步",
  "/settings": "安全与设置",
  "/node/dashboard": "节点仪表盘",
  "/inbounds": "入站节点",
  "/certs": "证书中心",
  "/routing": "出站与分流",
  "/core": "核心与日志",
  "/connections": "活跃连接",
};

interface AppShellProps {
  route: AppRoute;
  onNavigate: (route: AppRoute) => void;
  core: CoreStatus | null;
  metrics: MetricsSnapshot | null;
  sseConnected: boolean;
  children: React.ReactNode;
}

export function AppShell({
  route,
  onNavigate,
  core,
  metrics,
  sseConnected,
  children,
}: AppShellProps) {
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [nodeMenuOpen, setNodeMenuOpen] = useState(false);
  const desktopSwitcherRef = useRef<HTMLDivElement | null>(null);
  const mobileSwitcherRef = useRef<HTMLDivElement | null>(null);

  const { theme, toggleTheme } = useTheme();
  const { status, logout } = useAuth();
  const { nodes, activeNode, activeNodeId, setActiveNodeId } = useNode();

  const viewMode = getViewMode(route);
  const navItems = viewMode === "master" ? MASTER_NAV_ITEMS : NODE_NAV_ITEMS;

  useEffect(() => {
    setNodeMenuOpen(false);
  }, [viewMode]);

  useEffect(() => {
    if (!nodeMenuOpen) return;
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node;
      const insideDesktop = desktopSwitcherRef.current?.contains(target);
      const insideMobile = mobileSwitcherRef.current?.contains(target);
      if (!insideDesktop && !insideMobile) {
        setNodeMenuOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setNodeMenuOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [nodeMenuOpen]);

  const handleSelect = (target: AppRoute) => {
    onNavigate(target);
    setMobileOpen(false);
  };

  const nodeOffline = activeNode ? !activeNode.online : false;

  const coreState = nodeOffline
    ? "idle"
    : core?.upgrading
      ? "warn"
      : core?.running
        ? "ok"
        : core?.last_error
          ? "danger"
          : "idle";

  const coreLabel = nodeOffline
    ? "节点离线"
    : core?.upgrading
      ? "核心升级中"
      : core?.running
        ? core.version
          ? `运行中 · ${core.version}`
          : "运行中"
        : core?.binary_exists === false
          ? "未安装核心"
          : "已停止";

  const onlineCount = nodes.filter((n) => n.online).length;

  const renderSidebarContent = (
    isCompact: boolean,
    switcherRef: React.RefObject<HTMLDivElement | null>,
  ) => (
    <div className="flex h-full flex-col">
      {/* Brand header */}
      <div
        className={cn(
          "flex h-14 items-center border-b border-hairline px-3.5",
          isCompact ? "justify-center" : "justify-between",
        )}
      >
        <button
          type="button"
          onClick={() => handleSelect("/")}
          className="flex items-center gap-2.5 text-left focus:outline-none"
          title="返回主控集群面板"
        >
          <svg className="h-7 w-7 shrink-0" viewBox="0 0 32 32" aria-hidden="true">
            <rect
              width="32"
              height="32"
              rx="6"
              className="fill-recessed stroke-hairline-strong"
              strokeWidth="1.2"
            />
            <path
              d="M8.5 21L13.8 11.5L18.2 18L23 11"
              fill="none"
              className="stroke-accent"
              strokeWidth="2.3"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
            <circle cx="23" cy="11" r="2.2" className="fill-ok" />
          </svg>
          {!isCompact && (
            <div className="min-w-0">
              <div className="font-mono text-sm font-semibold tracking-tight text-ink">
                sbox-panel
              </div>
              <div className="truncate font-mono text-[10px] text-ink-muted">
                {viewMode === "master"
                  ? nodes.length > 1
                    ? `主控集群 · ${onlineCount}/${nodes.length} 在线`
                    : "主控集群 · sing-box"
                  : `节点视图 · ${activeNode?.name || "local"}`}
              </div>
            </div>
          )}
        </button>

        {!isCompact && (
          <button
            type="button"
            onClick={() => setCollapsed(true)}
            className="hidden rounded p-1 text-ink-muted hover:bg-surface-hover hover:text-ink lg:inline-flex"
            title="收起侧栏"
            aria-label="收起侧栏"
          >
            <ChevronLeft className="h-4 w-4" />
          </button>
        )}
      </div>

      {/* Node Mode Context Bar: Back to Master + Inline Active Node Quick-Switcher */}
      {viewMode === "node" && (
        <div
          className={cn(
            "border-b border-hairline",
            isCompact ? "flex flex-col items-center gap-2 p-2" : "space-y-2 p-2.5",
          )}
        >
          <button
            type="button"
            onClick={() => handleSelect("/")}
            title="返回主控面板"
            className={cn(
              "flex items-center gap-2 rounded-md border border-hairline bg-recessed text-xs font-medium text-ink-secondary transition-colors hover:border-hairline-strong hover:bg-surface-hover hover:text-ink",
              isCompact ? "h-8 w-8 justify-center p-0" : "w-full px-2.5 py-1.5",
            )}
          >
            <ArrowLeft className="h-3.5 w-3.5 shrink-0 text-accent" />
            {!isCompact && <span>返回主控面板</span>}
          </button>

          {activeNode && (
            <div ref={switcherRef} className="relative w-full">
              <button
                type="button"
                onClick={() => setNodeMenuOpen((o) => !o)}
                title={isCompact ? `切换节点 (${activeNode.name})` : undefined}
                aria-expanded={nodeMenuOpen}
                aria-haspopup="listbox"
                className={cn(
                  "flex items-center justify-between gap-2 rounded-md border transition-colors",
                  isCompact ? "mx-auto h-8 w-8 justify-center p-0" : "w-full px-2.5 py-2 text-left",
                  nodeMenuOpen
                    ? "border-accent bg-accent-muted text-ink"
                    : "border-hairline-strong bg-recessed/90 text-ink hover:border-accent/60",
                )}
              >
                {isCompact ? (
                  <StatusDot state={activeNode.online ? "ok" : "idle"} />
                ) : (
                  <>
                    <div className="flex min-w-0 items-center gap-2">
                      <StatusDot state={activeNode.online ? "ok" : "idle"} />
                      <div className="min-w-0">
                        <div className="flex items-center gap-1.5">
                          <span className="truncate font-mono text-xs font-semibold text-ink">
                            {activeNode.name}
                          </span>
                          <span className="shrink-0 rounded border border-hairline bg-surface px-1 py-0.2 font-mono text-[10px] text-ink-secondary">
                            {activeNode.is_local ? "本机" : "远端"}
                          </span>
                        </div>
                        <div className="mt-0.5 truncate font-mono text-[10px] text-ink-muted">
                          {activeNode.public_ipv4 || (activeNode.online ? "在线" : "离线")}
                        </div>
                      </div>
                    </div>
                    <ChevronDown
                      className={cn(
                        "h-3.5 w-3.5 shrink-0 text-ink-muted transition-transform",
                        nodeMenuOpen && "rotate-180",
                      )}
                    />
                  </>
                )}
              </button>

              {nodeMenuOpen && (
                <div
                  role="listbox"
                  className={cn(
                    "absolute z-40 mt-1.5 w-64 overflow-hidden rounded-lg border border-hairline-strong bg-surface shadow-2xl",
                    isCompact ? "left-full top-0 ml-2 mt-0" : "left-0 right-0 top-full w-auto min-w-[224px]",
                  )}
                >
                  <div className="flex items-center justify-between border-b border-hairline bg-recessed px-3 py-2 text-[11px] text-ink-secondary">
                    <span>快速切换管理节点</span>
                    <span className="font-mono tabular-nums">
                      {onlineCount}/{nodes.length} 在线
                    </span>
                  </div>

                  <div className="max-h-64 overflow-y-auto p-1">
                    {nodes.map((n) => {
                      const selected = n.id === activeNodeId;
                      return (
                        <button
                          key={n.id}
                          type="button"
                          role="option"
                          aria-selected={selected}
                          onClick={() => {
                            setActiveNodeId(n.id);
                            setNodeMenuOpen(false);
                            setMobileOpen(false);
                          }}
                          className={cn(
                            "flex w-full items-center justify-between gap-2 rounded-md px-2.5 py-2 text-left text-xs transition-colors",
                            selected
                              ? "bg-accent-muted text-ink"
                              : "text-ink-secondary hover:bg-surface-hover hover:text-ink",
                          )}
                        >
                          <div className="flex min-w-0 items-center gap-2">
                            <StatusDot state={n.online ? "ok" : "idle"} />
                            <div className="min-w-0">
                              <div className="flex items-center gap-1.5">
                                <span className="truncate font-mono font-medium text-ink">
                                  {n.name}
                                </span>
                                <Badge
                                  tone={n.is_local ? "accent" : "neutral"}
                                  className="px-1 py-0 text-[10px]"
                                >
                                  {n.is_local ? "本机" : "远端"}
                                </Badge>
                              </div>
                              <div className="mt-0.5 truncate font-mono text-[10px] text-ink-muted">
                                {n.online
                                  ? `${n.public_ipv4 || `${n.os || "?"}/${n.arch || "?"}`} · ${n.core_version || "无核心"}`
                                  : "Agent 离线"}
                              </div>
                            </div>
                          </div>
                          {selected && <Check className="h-3.5 w-3.5 shrink-0 text-accent" />}
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* Navigation list (strictly separated by viewMode) */}
      <nav
        className="flex-1 space-y-5 overflow-y-auto px-2.5 py-4"
        aria-label={viewMode === "master" ? "主控集群导航" : "节点控制导航"}
      >
        <div>
          {!isCompact && (
            <div className="mb-1.5 px-2.5 text-[11px] font-medium text-ink-muted">
              {viewMode === "master" ? "主控集群" : "节点控制"}
            </div>
          )}
          <div className="space-y-0.5">
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = route === item.route;
              return (
                <button
                  key={item.route}
                  type="button"
                  onClick={() => handleSelect(item.route)}
                  title={isCompact ? item.label : undefined}
                  className={cn(
                    "flex w-full items-center justify-between gap-2.5 rounded-md px-2.5 py-2 text-sm transition-colors",
                    isCompact && "justify-center px-0",
                    active
                      ? "bg-accent-muted font-medium text-accent"
                      : "text-ink-secondary hover:bg-surface-hover hover:text-ink",
                  )}
                >
                  <span className="flex items-center gap-2.5 truncate">
                    <Icon className="h-4 w-4 shrink-0" />
                    {!isCompact && <span className="truncate">{item.label}</span>}
                  </span>
                  {!isCompact && item.route === "/" && nodes.length > 0 && (
                    <span className="rounded border border-hairline bg-recessed px-1.5 py-0.2 font-mono text-[10px] text-ink-secondary tabular-nums">
                      {onlineCount}/{nodes.length}
                    </span>
                  )}
                </button>
              );
            })}
          </div>
        </div>
      </nav>

      {/* Footer account strip */}
      <div className="border-t border-hairline p-2.5">
        {isCompact ? (
          <div className="flex flex-col items-center gap-1.5">
            <button
              type="button"
              onClick={() => setCollapsed(false)}
              className="hidden rounded p-1.5 text-ink-muted hover:bg-surface-hover hover:text-ink lg:inline-flex"
              title="展开侧栏"
              aria-label="展开侧栏"
            >
              <ChevronRight className="h-4 w-4" />
            </button>
            <button
              type="button"
              onClick={() => void logout()}
              className="rounded p-1.5 text-ink-muted hover:bg-surface-hover hover:text-danger"
              title="退出登录"
              aria-label="退出登录"
            >
              <LogOut className="h-4 w-4" />
            </button>
          </div>
        ) : (
          <div className="flex items-center justify-between gap-2 rounded-md bg-recessed px-2.5 py-2">
            <div className="flex min-w-0 items-center gap-2">
              <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded border border-hairline bg-surface font-mono text-xs font-semibold text-accent">
                {(status?.username || "A").slice(0, 1).toUpperCase()}
              </div>
              <div className="min-w-0">
                <div className="truncate text-xs font-medium text-ink">
                  {status?.username || "admin"}
                </div>
                <div className="flex items-center gap-1 text-[10px] text-ink-muted">
                  <KeyRound className="h-2.5 w-2.5" />
                  <span>{status?.totp_enabled ? "2FA 已启用" : "单因素登录"}</span>
                </div>
              </div>
            </div>
            <button
              type="button"
              onClick={() => void logout()}
              className="rounded p-1.5 text-ink-muted hover:bg-surface hover:text-danger"
              title="退出登录"
              aria-label="退出登录"
            >
              <LogOut className="h-3.5 w-3.5" />
            </button>
          </div>
        )}
      </div>
    </div>
  );

  return (
    <div className="flex min-h-screen bg-canvas text-ink">
      {/* Desktop sidebar */}
      <aside
        className={cn(
          "hidden shrink-0 border-r border-hairline bg-surface transition-[width] duration-150 lg:block",
          collapsed ? "w-16" : "w-60",
        )}
      >
        <div className="sticky top-0 h-screen">
          {renderSidebarContent(collapsed, desktopSwitcherRef)}
        </div>
      </aside>

      {/* Mobile drawer */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 flex lg:hidden">
          <div
            className="fixed inset-0 bg-black/55 backdrop-blur-[1px]"
            onClick={() => setMobileOpen(false)}
          />
          <aside className="relative z-50 w-64 border-r border-hairline bg-surface">
            <button
              type="button"
              onClick={() => setMobileOpen(false)}
              className="absolute right-3 top-3.5 rounded p-1 text-ink-muted hover:text-ink"
              aria-label="关闭菜单"
            >
              <X className="h-4 w-4" />
            </button>
            {renderSidebarContent(false, mobileSwitcherRef)}
          </aside>
        </div>
      )}

      {/* Main viewport */}
      <div className="flex min-w-0 flex-1 flex-col">
        {/* Top instrument utility strip (strictly separated by Master vs Node mode) */}
        <header className="sticky top-0 z-20 flex h-14 items-center justify-between gap-2.5 border-b border-hairline bg-surface/95 px-4 backdrop-blur-sm sm:px-6">
          <div className="flex min-w-0 items-center gap-3">
            <button
              type="button"
              onClick={() => setMobileOpen(true)}
              className="rounded-md p-1.5 text-ink-secondary hover:bg-surface-hover hover:text-ink lg:hidden"
              aria-label="打开导航菜单"
            >
              <Menu className="h-5 w-5" />
            </button>
            <h1 className="truncate text-sm font-semibold text-ink sm:text-base">
              {ROUTE_TITLES[route]}
            </h1>

            {/* Node Mode active node identity badge */}
            {viewMode === "node" && activeNode && (
              <div className="inline-flex items-center gap-1.5 rounded-md border border-hairline bg-recessed px-2 py-0.5 text-xs">
                <StatusDot state={activeNode.online ? "ok" : "idle"} className="h-1.5 w-1.5" />
                <span className="max-w-[120px] truncate font-mono font-medium text-ink sm:max-w-[160px]">
                  {activeNode.name}
                </span>
                <span className="hidden rounded border border-hairline bg-surface px-1 text-[10px] text-ink-secondary sm:inline">
                  {activeNode.is_local ? "本机" : "远端"}
                </span>
              </div>
            )}
          </div>

          <div className="flex items-center gap-2 sm:gap-3">
            {viewMode === "master" ? (
              /* Master Mode: clean cluster online nodes status chip only */
              <button
                type="button"
                onClick={() => handleSelect("/")}
                className="inline-flex items-center gap-2 rounded-md border border-hairline bg-recessed px-2.5 py-1 text-xs text-ink hover:border-hairline-strong"
                title="查看集群节点状态"
              >
                <StatusDot state={onlineCount > 0 ? "ok" : "idle"} />
                <span className="font-mono tabular-nums">
                  {onlineCount}/{nodes.length} 在线
                </span>
              </button>
            ) : (
              /* Node Mode: live TX/RX speed pill + node sing-box core status LED */
              <>
                <div className="hidden items-center gap-3 rounded-md border border-hairline bg-recessed px-2.5 py-1 font-mono text-xs tabular-nums xl:flex">
                  <span className="flex items-center gap-1 text-uplink" title="当前节点实时上行速率">
                    <ArrowUp className="h-3 w-3" />
                    {formatSpeed(metrics?.up_speed)}
                  </span>
                  <span className="h-3 w-px bg-hairline" />
                  <span className="flex items-center gap-1 text-accent" title="当前节点实时下行速率">
                    <ArrowDown className="h-3 w-3" />
                    {formatSpeed(metrics?.down_speed)}
                  </span>
                  <span className="h-3 w-px bg-hairline" />
                  <span
                    className="flex items-center gap-1 text-[11px] text-ink-muted"
                    title={sseConnected ? "SSE 实时推送已连接" : "SSE 正在重连"}
                  >
                    <StatusDot state={sseConnected ? "ok" : "warn"} className="h-1.5 w-1.5" />
                    SSE
                  </span>
                </div>

                <button
                  type="button"
                  onClick={() => handleSelect("/core")}
                  className="hidden items-center gap-2 rounded-md border border-hairline bg-recessed px-2.5 py-1 text-xs text-ink hover:border-hairline-strong sm:flex"
                >
                  <StatusDot state={coreState} />
                  <span className="max-w-[140px] truncate font-mono">{coreLabel}</span>
                </button>
              </>
            )}

            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={toggleTheme}
              title={theme === "dark" ? "切换到浅色界面" : "切换到深色界面"}
              aria-label="切换配色模式"
            >
              {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </Button>
          </div>
        </header>

        <main className="mx-auto w-full max-w-7xl flex-1 px-4 py-5 sm:px-6 sm:py-6">
          {children}
        </main>
      </div>
    </div>
  );
}
