import { useState } from "react";
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  Check,
  Copy,
  Cpu,
  HardDrive,
  Network,
  Play,
  RefreshCw,
  RotateCw,
  Square,
  Timer,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { formatBytes, formatUptime } from "@/lib/format";
import { useToast } from "@/components/ui/Toast";
import { Badge, Button, Panel, StatusDot } from "@/components/ui/primitives";
import { ThroughputChart } from "@/components/ThroughputChart";
import { TrafficHistoryPanel } from "@/components/TrafficHistoryPanel";
import type { DashboardLiveState } from "@/hooks/useDashboardStream";
import type { AppRoute } from "@/components/AppShell";

interface DashboardPageProps {
  live: DashboardLiveState;
  onNavigate: (route: AppRoute) => void;
}

export function DashboardPage({ live, onNavigate }: DashboardPageProps) {
  const { core, metrics, system, history, connected, refresh } = live;
  const toast = useToast();
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  const runCoreAction = async (
    action: "start" | "stop" | "reload" | "restart",
    label: string,
  ) => {
    setBusyAction(action);
    try {
      if (action === "start") await api.startCore();
      if (action === "stop") await api.stopCore();
      if (action === "reload") await api.reloadCore();
      if (action === "restart") await api.restartCore();
      toast.success(`已${label}`);
      await refresh();
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : `${label}失败`;
      toast.error(`${label}失败`, msg);
    } finally {
      setBusyAction(null);
    }
  };

  const copyValue = async (key: string, val: string) => {
    try {
      await navigator.clipboard.writeText(val);
      setCopiedKey(key);
      toast.info("已复制到剪贴板", val);
      window.setTimeout(() => setCopiedKey(null), 1800);
    } catch {
      toast.error("复制失败", "当前浏览器上下文不支持剪贴板写入");
    }
  };

  const coreRunning = Boolean(core?.running);
  const binaryMissing = core?.binary_exists === false;

  return (
    <div className="space-y-5">
      {/* Primary asymmetric hero: Oscilloscope (8 cols) + Core Control Deck (4 cols) */}
      <div className="grid gap-5 lg:grid-cols-12">
        <Panel
          className="lg:col-span-8"
          title="实时网络吞吐"
          description="通过本地回环 Clash API 采集 sing-box 实时流量速率"
          actions={
            <div className="flex items-center gap-2">
              <Badge tone={connected ? "ok" : "warn"}>
                <StatusDot state={connected ? "ok" : "warn"} className="h-1.5 w-1.5" />
                {connected ? "SSE 已连接" : "重连中"}
              </Badge>
              {metrics && (
                <Badge tone={metrics.core_connected ? "accent" : "neutral"}>
                  {metrics.core_connected ? "Clash API 在线" : "Clash API 离线"}
                </Badge>
              )}
            </div>
          }
        >
          <ThroughputChart history={history} />

          <div className="mt-4 grid grid-cols-2 gap-3 border-t border-hairline pt-3.5 sm:grid-cols-4">
            <div>
              <div className="text-[11px] text-ink-muted">本次会话上行</div>
              <div className="mt-0.5 font-mono text-sm font-medium text-ink tabular-nums">
                {formatBytes(metrics?.total_upload)}
              </div>
            </div>
            <div>
              <div className="text-[11px] text-ink-muted">本次会话下行</div>
              <div className="mt-0.5 font-mono text-sm font-medium text-ink tabular-nums">
                {formatBytes(metrics?.total_download)}
              </div>
            </div>
            <div>
              <div className="text-[11px] text-ink-muted">活跃连接</div>
              <div className="mt-0.5 font-mono text-sm font-medium text-ink tabular-nums">
                {metrics?.active_connections ?? 0}
              </div>
            </div>
            <div>
              <div className="text-[11px] text-ink-muted">Clash API 地址</div>
              <div className="mt-0.5 truncate font-mono text-xs text-ink-secondary">
                {core?.clash_api_addr || "未启动"}
              </div>
            </div>
          </div>
        </Panel>

        <Panel
          className="flex flex-col lg:col-span-4"
          bodyClassName="flex flex-1 flex-col justify-between gap-4"
          title="sing-box 核心状态"
          actions={
            <Badge
              tone={
                core?.upgrading
                  ? "warn"
                  : coreRunning
                    ? "ok"
                    : binaryMissing
                      ? "warn"
                      : "neutral"
              }
            >
              <StatusDot
                state={
                  core?.upgrading
                    ? "warn"
                    : coreRunning
                      ? "ok"
                      : core?.last_error
                        ? "danger"
                        : "idle"
                }
                className="h-1.5 w-1.5"
              />
              {core?.upgrading
                ? "升级中"
                : coreRunning
                  ? "运行中"
                  : binaryMissing
                    ? "未安装"
                    : "已停止"}
            </Badge>
          }
        >
          <div className="space-y-3">
            <div className="rounded-md border border-hairline bg-recessed p-3">
              <div className="flex items-baseline justify-between gap-2">
                <span className="text-xs text-ink-secondary">当前版本</span>
                <span className="font-mono text-sm font-semibold text-ink">
                  {core?.version || (binaryMissing ? "未检测到二进制" : "未知版本")}
                </span>
              </div>
              <div className="mt-2 flex items-baseline justify-between gap-2 text-xs">
                <span className="text-ink-muted">进程 PID</span>
                <span className="font-mono text-ink-secondary tabular-nums">
                  {core?.running && core.pid ? core.pid : "-"}
                </span>
              </div>
              <div className="mt-1.5 flex items-baseline justify-between gap-2 text-xs">
                <span className="text-ink-muted">持续运行</span>
                <span className="font-mono text-ink-secondary tabular-nums">
                  {core?.running ? formatUptime(core.uptime_sec) : "-"}
                </span>
              </div>
            </div>

            {binaryMissing && (
              <div className="rounded-md border border-warn/40 bg-warn/10 p-3 text-xs">
                <div className="flex items-center gap-1.5 font-medium text-warn">
                  <AlertTriangle className="h-3.5 w-3.5" />
                  尚未安装 sing-box 核心二进制
                </div>
                <p className="mt-1 text-ink-secondary">
                  前往「核心与日志」可从 GitHub Releases 一键下载官方核心或手动上传二进制文件。
                </p>
                <Button
                  variant="secondary"
                  size="xs"
                  className="mt-2.5"
                  onClick={() => onNavigate("/core")}
                >
                  前往安装核心
                </Button>
              </div>
            )}

            {core?.last_error && (
              <div className="rounded-md border border-danger/40 bg-danger/10 p-2.5 text-xs text-danger">
                <div className="font-medium">最近核心错误</div>
                <p className="mt-0.5 break-all font-mono text-[11px]">{core.last_error}</p>
              </div>
            )}
          </div>

          <div className="grid grid-cols-2 gap-2 border-t border-hairline pt-3.5">
            {coreRunning ? (
              <Button
                variant="danger"
                size="sm"
                loading={busyAction === "stop"}
                disabled={busyAction !== null || core?.upgrading}
                onClick={() => void runCoreAction("stop", "停止核心")}
              >
                <Square className="h-3.5 w-3.5" />
                停止核心
              </Button>
            ) : (
              <Button
                variant="primary"
                size="sm"
                loading={busyAction === "start"}
                disabled={busyAction !== null || binaryMissing || core?.upgrading}
                onClick={() => void runCoreAction("start", "启动核心")}
              >
                <Play className="h-3.5 w-3.5" />
                启动核心
              </Button>
            )}

            <Button
              variant="secondary"
              size="sm"
              loading={busyAction === "reload"}
              disabled={busyAction !== null || !coreRunning || core?.upgrading}
              onClick={() => void runCoreAction("reload", "热重载配置")}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              热重载
            </Button>

            <Button
              variant="outline"
              size="sm"
              loading={busyAction === "restart"}
              disabled={busyAction !== null || binaryMissing || core?.upgrading}
              onClick={() => void runCoreAction("restart", "重启核心")}
            >
              <RotateCw className="h-3.5 w-3.5" />
              重启核心
            </Button>

            <Button
              variant="ghost"
              size="sm"
              onClick={() => onNavigate("/core")}
            >
              版本与日志
              <ArrowUpRight className="h-3.5 w-3.5" />
            </Button>
          </div>
        </Panel>
      </div>

      {/* Unified hairline telemetry strip (4 structured metric cells) */}
      <div className="grid divide-y divide-hairline overflow-hidden rounded-lg border border-hairline bg-surface sm:grid-cols-2 sm:divide-y-0 lg:grid-cols-4 lg:divide-x">
        <div className="p-4">
          <div className="flex items-center justify-between text-xs text-ink-secondary">
            <span>核心内存占用</span>
            <Cpu className="h-3.5 w-3.5 text-ink-muted" />
          </div>
          <div className="mt-2 font-mono text-xl font-semibold text-ink tabular-nums">
            {formatBytes(metrics?.memory_inuse)}
          </div>
          <div className="mt-1 font-mono text-[11px] text-ink-muted tabular-nums">
            面板进程: {formatBytes(system?.panel_mem_bytes)}
          </div>
        </div>

        <div className="border-hairline p-4 sm:border-l lg:border-l-0">
          <div className="flex items-center justify-between text-xs text-ink-secondary">
            <span>活跃代理连接</span>
            <Network className="h-3.5 w-3.5 text-ink-muted" />
          </div>
          <div className="mt-2 flex items-baseline justify-between">
            <span className="font-mono text-xl font-semibold text-ink tabular-nums">
              {metrics?.active_connections ?? 0}
            </span>
            <button
              type="button"
              onClick={() => onNavigate("/connections")}
              className="text-xs text-accent hover:underline"
            >
              查看连接
            </button>
          </div>
          <div className="mt-1 text-[11px] text-ink-muted">
            支持按入站与成员实时审计踢断
          </div>
        </div>

        <div className="border-hairline p-4 sm:border-t lg:border-t-0">
          <div className="flex items-center justify-between text-xs text-ink-secondary">
            <span>面板运行时长</span>
            <Timer className="h-3.5 w-3.5 text-ink-muted" />
          </div>
          <div className="mt-2 font-mono text-xl font-semibold text-ink tabular-nums">
            {formatUptime(system?.panel_uptime_sec)}
          </div>
          <div className="mt-1 font-mono text-[11px] text-ink-muted tabular-nums">
            核心运行: {core?.running ? formatUptime(core.uptime_sec) : "未运行"}
          </div>
        </div>

        <div className="border-hairline p-4 sm:border-l sm:border-t lg:border-l-0 lg:border-t-0">
          <div className="flex items-center justify-between text-xs text-ink-secondary">
            <span>累计会话总流量</span>
            <Activity className="h-3.5 w-3.5 text-ink-muted" />
          </div>
          <div className="mt-2 font-mono text-xl font-semibold text-ink tabular-nums">
            {formatBytes((metrics?.total_upload ?? 0) + (metrics?.total_download ?? 0))}
          </div>
          <div className="mt-1 font-mono text-[11px] text-ink-muted tabular-nums">
            ↑ {formatBytes(metrics?.total_upload)} / ↓ {formatBytes(metrics?.total_download)}
          </div>
        </div>
      </div>

      {/* Historical Traffic Trend & Member/Inbound Breakdown */}
      <TrafficHistoryPanel />

      {/* Host identity & Public IP endpoints */}
      <div className="grid gap-5 lg:grid-cols-12">
        <Panel
          className="lg:col-span-7"
          title="宿主机与运行环境"
          actions={
            <Button variant="ghost" size="xs" onClick={() => void refresh()}>
              <RefreshCw className="h-3 w-3" />
              刷新
            </Button>
          }
        >
          <dl className="divide-y divide-hairline text-xs">
            <div className="flex items-center justify-between gap-4 py-2 first:pt-0">
              <dt className="text-ink-secondary">主机名 (Hostname)</dt>
              <dd className="font-mono text-ink">{system?.hostname || "-"}</dd>
            </div>
            <div className="flex items-center justify-between gap-4 py-2">
              <dt className="text-ink-secondary">系统平台与架构</dt>
              <dd className="font-mono text-ink">
                {system ? `${system.os} / ${system.arch}` : "-"}
              </dd>
            </div>
            <div className="flex items-center justify-between gap-4 py-2">
              <dt className="text-ink-secondary">面板版本与 Go 运行时</dt>
              <dd className="font-mono text-ink">
                {system ? `${system.panel_version || "dev"} (${system.go_version})` : "-"}
              </dd>
            </div>
            <div className="flex items-center justify-between gap-4 py-2 last:pb-0">
              <dt className="flex items-center gap-1.5 text-ink-secondary">
                <HardDrive className="h-3.5 w-3.5 text-ink-muted" />
                核心二进制路径
              </dt>
              <dd className="max-w-[65%] truncate font-mono text-ink-secondary" title={core?.binary_path}>
                {core?.binary_path || "-"}
              </dd>
            </div>
          </dl>
        </Panel>

        <Panel
          className="lg:col-span-5"
          title="公网出口地址"
          description="用于节点订阅生成时的默认连接地址，可在设置中手动覆盖"
          actions={
            <Button variant="ghost" size="xs" onClick={() => onNavigate("/settings")}>
              修改覆写
            </Button>
          }
        >
          <div className="space-y-3">
            <div className="flex items-center justify-between gap-2 rounded-md border border-hairline bg-recessed px-3 py-2.5">
              <div className="min-w-0">
                <div className="text-[11px] text-ink-muted">公网 IPv4</div>
                <div className="mt-0.5 truncate font-mono text-sm text-ink">
                  {system?.public_ipv4 || "未探测 / 未配置"}
                </div>
              </div>
              {system?.public_ipv4 && (
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => void copyValue("ipv4", system.public_ipv4)}
                  title="复制 IPv4"
                  aria-label="复制公网 IPv4"
                >
                  {copiedKey === "ipv4" ? (
                    <Check className="h-3.5 w-3.5 text-ok" />
                  ) : (
                    <Copy className="h-3.5 w-3.5" />
                  )}
                </Button>
              )}
            </div>

            <div className="flex items-center justify-between gap-2 rounded-md border border-hairline bg-recessed px-3 py-2.5">
              <div className="min-w-0">
                <div className="text-[11px] text-ink-muted">公网 IPv6</div>
                <div className="mt-0.5 truncate font-mono text-sm text-ink">
                  {system?.public_ipv6 || "未探测 / 未配置"}
                </div>
              </div>
              {system?.public_ipv6 && (
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => void copyValue("ipv6", system.public_ipv6)}
                  title="复制 IPv6"
                  aria-label="复制公网 IPv6"
                >
                  {copiedKey === "ipv6" ? (
                    <Check className="h-3.5 w-3.5 text-ok" />
                  ) : (
                    <Copy className="h-3.5 w-3.5" />
                  )}
                </Button>
              )}
            </div>
          </div>
        </Panel>
      </div>
    </div>
  );
}
