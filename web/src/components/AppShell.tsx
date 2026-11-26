import React, { useState } from "react";
import {
  Activity,
  ArrowDown,
  ArrowUp,
  ChevronLeft,
  ChevronRight,
  Cpu,
  GitBranch,
  Globe,
  KeyRound,
  LogOut,
  Menu,
  Moon,
  Network,
  Radio,
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
import { Button, StatusDot } from "@/components/ui/primitives";
import type { CoreStatus, MetricsSnapshot } from "@/types/api";

export type AppRoute =
  | "/"
  | "/core"
  | "/connections"
  | "/settings"
  | "/inbounds"
  | "/members"
  | "/certs"
  | "/routing"
  | "/sync";

interface NavItem {
  route: AppRoute;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  milestone?: string;
}

export const ACTIVE_NAV_ITEMS: NavItem[] = [
  { route: "/", label: "仪表盘", icon: Activity },
  { route: "/inbounds", label: "入站节点", icon: Radio },
  { route: "/members", label: "成员管理", icon: Users },
  { route: "/certs", label: "证书中心", icon: Shield },
  { route: "/core", label: "核心与日志", icon: Cpu },
  { route: "/connections", label: "活跃连接", icon: Network },
  { route: "/settings", label: "安全与设置", icon: Settings },
];

export const ROADMAP_NAV_ITEMS: NavItem[] = [
  { route: "/routing", label: "出站与分流", icon: GitBranch, milestone: "M3" },
  { route: "/sync", label: "订阅与同步", icon: Globe, milestone: "M4" },
];

const ROUTE_TITLES: Record<AppRoute, string> = {
  "/": "仪表盘",
  "/core": "核心与日志",
  "/connections": "活跃连接",
  "/settings": "安全与设置",
  "/inbounds": "入站节点",
  "/members": "成员管理",
  "/certs": "证书中心",
  "/routing": "出站与分流",
  "/sync": "订阅与同步",
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
  const { theme, toggleTheme } = useTheme();
  const { status, logout } = useAuth();

  const handleSelect = (target: AppRoute) => {
    onNavigate(target);
    setMobileOpen(false);
  };

  const coreState = core?.upgrading
    ? "warn"
    : core?.running
      ? "ok"
      : core?.last_error
        ? "danger"
        : "idle";

  const coreLabel = core?.upgrading
    ? "核心升级中"
    : core?.running
      ? core.version
        ? `运行中 · ${core.version}`
        : "运行中"
      : core?.binary_exists === false
        ? "未安装核心"
        : "已停止";

  const renderSidebarContent = (isCompact: boolean) => (
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
                sing-box 1.12+
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

      {/* Navigation groups */}
      <nav className="flex-1 space-y-5 overflow-y-auto px-2.5 py-4" aria-label="主导航">
        <div>
          {!isCompact && (
            <div className="mb-1.5 px-2.5 text-[11px] font-medium text-ink-muted">
              核心控制
            </div>
          )}
          <div className="space-y-0.5">
            {ACTIVE_NAV_ITEMS.map((item) => {
              const Icon = item.icon;
              const active = route === item.route;
              return (
                <button
                  key={item.route}
                  type="button"
                  onClick={() => handleSelect(item.route)}
                  title={isCompact ? item.label : undefined}
                  className={cn(
                    "flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-sm transition-colors",
                    isCompact && "justify-center px-0",
                    active
                      ? "bg-accent-muted font-medium text-accent"
                      : "text-ink-secondary hover:bg-surface-hover hover:text-ink",
                  )}
                >
                  <Icon className="h-4 w-4 shrink-0" />
                  {!isCompact && <span className="truncate">{item.label}</span>}
                </button>
              );
            })}
          </div>
        </div>

        <div className="border-t border-hairline pt-4">
          {!isCompact && (
            <div className="mb-1.5 px-2.5 text-[11px] font-medium text-ink-muted">
              规划模块
            </div>
          )}
          <div className="space-y-0.5">
            {ROADMAP_NAV_ITEMS.map((item) => {
              const Icon = item.icon;
              const active = route === item.route;
              return (
                <button
                  key={item.route}
                  type="button"
                  onClick={() => handleSelect(item.route)}
                  title={isCompact ? `${item.label} (${item.milestone})` : undefined}
                  className={cn(
                    "flex w-full items-center justify-between rounded-md px-2.5 py-1.5 text-sm transition-colors",
                    isCompact && "justify-center px-0 py-2",
                    active
                      ? "bg-surface-hover font-medium text-ink"
                      : "text-ink-muted hover:bg-surface-hover hover:text-ink-secondary",
                  )}
                >
                  <span className="flex items-center gap-2.5 truncate">
                    <Icon className="h-4 w-4 shrink-0" />
                    {!isCompact && <span className="truncate">{item.label}</span>}
                  </span>
                  {!isCompact && item.milestone && (
                    <span className="rounded border border-hairline px-1 py-0.2 font-mono text-[10px] text-ink-muted">
                      {item.milestone}
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
        <div className="sticky top-0 h-screen">{renderSidebarContent(collapsed)}</div>
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
            {renderSidebarContent(false)}
          </aside>
        </div>
      )}

      {/* Main viewport */}
      <div className="flex min-w-0 flex-1 flex-col">
        {/* Top instrument utility strip */}
        <header className="sticky top-0 z-20 flex h-14 items-center justify-between gap-3 border-b border-hairline bg-surface/95 px-4 backdrop-blur-sm sm:px-6">
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
          </div>

          <div className="flex items-center gap-2 sm:gap-3">
            {/* Compact live speed pill */}
            <div className="hidden items-center gap-3 rounded-md border border-hairline bg-recessed px-2.5 py-1 font-mono text-xs tabular-nums md:flex">
              <span className="flex items-center gap-1 text-uplink" title="实时上行速率">
                <ArrowUp className="h-3 w-3" />
                {formatSpeed(metrics?.up_speed)}
              </span>
              <span className="h-3 w-px bg-hairline" />
              <span className="flex items-center gap-1 text-accent" title="实时下行速率">
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

            {/* Core status indicator */}
            <button
              type="button"
              onClick={() => handleSelect("/core")}
              className="flex items-center gap-2 rounded-md border border-hairline bg-recessed px-2.5 py-1 text-xs text-ink hover:border-hairline-strong"
            >
              <StatusDot state={coreState} />
              <span className="max-w-[140px] truncate font-mono">{coreLabel}</span>
            </button>

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
