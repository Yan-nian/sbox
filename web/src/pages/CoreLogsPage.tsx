import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  Download,
  FileCode2,
  History,
  Play,
  RefreshCw,
  RotateCcw,
  RotateCw,
  Square,
  Terminal,
  Upload,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import { formatBytes, formatDateTime, formatUptime } from "@/lib/format";
import { useNode } from "@/contexts/NodeContext";
import { useToast } from "@/components/ui/Toast";
import { Badge, Button, Input, Panel, StatusDot } from "@/components/ui/primitives";
import { LogTerminal } from "@/components/LogTerminal";
import { CoreConfigEditor } from "@/components/CoreConfigEditor";
import { NodeScopeBanner } from "@/components/NodeScopeBanner";
import type { DashboardLiveState } from "@/hooks/useDashboardStream";
import type { AppRoute } from "@/components/AppShell";
import type { ReleaseInfo, UpgradeResult } from "@/types/api";

type CoreTab = "logs" | "versions" | "config";

interface CoreLogsPageProps {
  live: DashboardLiveState;
  onNavigate?: (route: AppRoute) => void;
}

export function CoreLogsPage({ live, onNavigate }: CoreLogsPageProps) {
  const { core, refresh } = live;
  const { activeNodeId, activeNode } = useNode();
  const toast = useToast();

  const nodeOffline = activeNode ? !activeNode.online : false;

  const [activeTab, setActiveTab] = useState<CoreTab>(
    core?.binary_exists === false ? "versions" : "logs",
  );
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [confirmRollback, setConfirmRollback] = useState(false);

  // Release switcher state
  const [includePrerelease, setIncludePrerelease] = useState(false);
  const [releases, setReleases] = useState<ReleaseInfo[]>([]);
  const [loadingReleases, setLoadingReleases] = useState(false);
  const [releaseError, setReleaseError] = useState<string | null>(null);
  const [proxyOverride, setProxyOverride] = useState("");
  const [upgradingTag, setUpgradingTag] = useState<string | null>(null);
  const [upgradeResult, setUpgradeResult] = useState<UpgradeResult | null>(null);

  // Manual upload state
  const [dragOver, setDragOver] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const loadReleases = useCallback(
    async (prerelease: boolean) => {
      if (nodeOffline) {
        setReleases([]);
        setReleaseError("当前远程节点离线，无法查询对应架构的 Release 发行包。");
        return;
      }
      setLoadingReleases(true);
      setReleaseError(null);
      try {
        const res = await api.listReleases(prerelease, activeNodeId);
        setReleases(Array.isArray(res.releases) ? res.releases : []);
      } catch (err) {
        setReleaseError(
          err instanceof ApiError
            ? err.message
            : "无法拉取 GitHub Releases，请检查网络或在设置中配置 GitHub 加速代理。",
        );
      } finally {
        setLoadingReleases(false);
      }
    },
    [activeNodeId, nodeOffline],
  );

  // Reset per-node release cache & diagnostics when active node changes
  useEffect(() => {
    setReleases([]);
    setReleaseError(null);
    setUpgradeResult(null);
    setConfirmRollback(false);
  }, [activeNodeId]);

  useEffect(() => {
    if (activeTab === "versions" && releases.length === 0 && !releaseError) {
      void loadReleases(includePrerelease);
    }
  }, [activeTab, includePrerelease, releases.length, releaseError, loadReleases]);

  const handleTogglePrerelease = (next: boolean) => {
    setIncludePrerelease(next);
    void loadReleases(next);
  };

  const runLifecycle = async (
    action: "start" | "stop" | "reload" | "restart" | "rollback",
    label: string,
  ) => {
    setBusyAction(action);
    try {
      if (action === "start") await api.startCore(activeNodeId);
      if (action === "stop") await api.stopCore(activeNodeId);
      if (action === "reload") await api.reloadCore(activeNodeId);
      if (action === "restart") await api.restartCore(activeNodeId);
      if (action === "rollback") {
        await api.rollbackCore(activeNodeId);
        setConfirmRollback(false);
      }
      toast.success(`已${label}`);
      await refresh();
    } catch (err) {
      toast.error(`${label}失败`, err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyAction(null);
    }
  };

  const handleUpgrade = async (rel: ReleaseInfo) => {
    setUpgradingTag(rel.tag_name);
    setUpgradeResult(null);
    try {
      const res = await api.upgradeCore(
        {
          tag_name: rel.tag_name,
          download_url: rel.download_url || undefined,
          proxy_url: proxyOverride.trim() || undefined,
        },
        activeNodeId,
      );
      setUpgradeResult(res);
      if (res.success) {
        toast.success(`已切换至 ${res.to_version || rel.tag_name}`, res.message);
      } else if (res.rolled_back) {
        toast.error("新版本探活失败，已自动回滚", res.message);
      } else {
        toast.error("升级未完成", res.message);
      }
      await refresh();
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : "升级请求失败";
      toast.error("升级失败", msg);
    } finally {
      setUpgradingTag(null);
    }
  };

  const handleUpload = async () => {
    if (!selectedFile) return;
    setUploading(true);
    setUpgradeResult(null);
    try {
      const res = await api.uploadCoreBinary(selectedFile, activeNodeId);
      setUpgradeResult(res);
      if (res.success) {
        toast.success("自定义核心替换成功", res.message);
        setSelectedFile(null);
      } else if (res.rolled_back) {
        toast.error("探活失败，已自动回滚旧版本", res.message);
      } else {
        toast.error("上传替换失败", res.message);
      }
      await refresh();
    } catch (err) {
      toast.error("上传失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setUploading(false);
    }
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    if (nodeOffline) return;
    const file = e.dataTransfer.files?.[0];
    if (file) setSelectedFile(file);
  };

  const coreRunning = Boolean(core?.running) && !nodeOffline;
  const busyUpgrade = upgradingTag !== null || uploading || Boolean(core?.upgrading);

  return (
    <div className="space-y-5">
      <NodeScopeBanner
        onNavigate={onNavigate}
        offlineHint="当前远程节点离线，无法实时控制核心、拉取日志或上传二进制；但您仍可预览和编辑该节点的 JSON 覆写补丁（重连后自动同步）。"
      />

      {/* Top Core Status & Quick Lifecycle Bar */}
      <Panel bodyClassName="p-4">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex flex-wrap items-center gap-4">
            <div className="flex items-center gap-2.5">
              <StatusDot
                state={
                  nodeOffline
                    ? "idle"
                    : busyUpgrade
                      ? "warn"
                      : coreRunning
                        ? "ok"
                        : core?.last_error
                          ? "danger"
                          : "idle"
                }
                className="h-2.5 w-2.5"
              />
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-base font-semibold text-ink">
                    {core?.version || (nodeOffline ? "节点离线" : "sing-box 未安装")}
                  </span>
                  <Badge tone={nodeOffline ? "warn" : coreRunning ? "ok" : "neutral"}>
                    {nodeOffline ? "离线" : coreRunning ? `PID ${core?.pid}` : "未运行"}
                  </Badge>
                  {core?.has_backup && !nodeOffline && (
                    <Badge tone="accent">存在可回滚备份</Badge>
                  )}
                </div>
                <div className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-[11px] text-ink-muted">
                  <span>{core?.binary_path || "-"}</span>
                  {coreRunning && <span>已运行 {formatUptime(core?.uptime_sec)}</span>}
                </div>
              </div>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            {coreRunning ? (
              <Button
                variant="danger"
                size="sm"
                loading={busyAction === "stop"}
                disabled={busyAction !== null || busyUpgrade || nodeOffline}
                onClick={() => void runLifecycle("stop", "停止核心")}
              >
                <Square className="h-3.5 w-3.5" />
                停止
              </Button>
            ) : (
              <Button
                variant="primary"
                size="sm"
                loading={busyAction === "start"}
                disabled={busyAction !== null || !core?.binary_exists || busyUpgrade || nodeOffline}
                onClick={() => void runLifecycle("start", "启动核心")}
              >
                <Play className="h-3.5 w-3.5" />
                启动
              </Button>
            )}

            <Button
              variant="secondary"
              size="sm"
              loading={busyAction === "reload"}
              disabled={busyAction !== null || !coreRunning || busyUpgrade || nodeOffline}
              onClick={() => void runLifecycle("reload", "热重载配置")}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              热重载
            </Button>

            <Button
              variant="outline"
              size="sm"
              loading={busyAction === "restart"}
              disabled={busyAction !== null || !core?.binary_exists || busyUpgrade || nodeOffline}
              onClick={() => void runLifecycle("restart", "重启核心")}
            >
              <RotateCw className="h-3.5 w-3.5" />
              重启
            </Button>

            {core?.has_backup && !nodeOffline && (
              confirmRollback ? (
                <div className="flex items-center gap-1.5 rounded-md border border-warn/45 bg-warn/10 px-2 py-1">
                  <span className="text-xs text-warn">确认回滚至上一备份？</span>
                  <Button
                    variant="danger"
                    size="xs"
                    loading={busyAction === "rollback"}
                    onClick={() => void runLifecycle("rollback", "回滚上一版本")}
                  >
                    确认回滚
                  </Button>
                  <Button
                    variant="ghost"
                    size="xs"
                    onClick={() => setConfirmRollback(false)}
                  >
                    取消
                  </Button>
                </div>
              ) : (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busyAction !== null || busyUpgrade}
                  onClick={() => setConfirmRollback(true)}
                >
                  <RotateCcw className="h-3.5 w-3.5 text-warn" />
                  回滚备份
                </Button>
              )
            )}
          </div>
        </div>
      </Panel>

      {/* Upgrade / Pre-check / Auto-Rollback Result Diagnostics */}
      {upgradeResult && (
        <div
          role="status"
          className={cn(
            "rounded-lg border p-4 text-xs",
            upgradeResult.success
              ? "border-ok/40 bg-ok/10"
              : upgradeResult.rolled_back
                ? "border-warn/45 bg-warn/10"
                : "border-danger/45 bg-danger/10",
          )}
        >
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2 font-medium text-ink">
              {upgradeResult.success ? (
                <CheckCircle2 className="h-4 w-4 text-ok" />
              ) : (
                <AlertTriangle
                  className={cn(
                    "h-4 w-4",
                    upgradeResult.rolled_back ? "text-warn" : "text-danger",
                  )}
                />
              )}
              <span>
                {upgradeResult.success
                  ? "原子升级成功"
                  : upgradeResult.rolled_back
                    ? "新版本启动异常，已自动回滚至原核心"
                    : "配置预检或下载未通过（原服务未中断）"}
              </span>
              {(upgradeResult.from_version || upgradeResult.to_version) && (
                <span className="font-mono text-ink-secondary">
                  {upgradeResult.from_version || "无"} → {upgradeResult.to_version || "-"}
                </span>
              )}
            </div>
            <button
              type="button"
              onClick={() => setUpgradeResult(null)}
              className="text-ink-muted hover:text-ink"
            >
              关闭
            </button>
          </div>

          {upgradeResult.message && (
            <p className="mt-1.5 text-ink-secondary">{upgradeResult.message}</p>
          )}

          {upgradeResult.check_output && (
            <pre className="mt-2.5 max-h-44 overflow-auto rounded border border-hairline bg-recessed p-2.5 font-mono text-[11px] text-ink">
              {upgradeResult.check_output}
            </pre>
          )}
        </div>
      )}

      {/* Segmented navigation tabs */}
      <div className="flex flex-wrap items-center gap-1 border-b border-hairline">
        {(
          [
            { id: "logs", label: "实时日志终端", icon: Terminal },
            { id: "versions", label: "版本升降级与二进制上传", icon: History },
            { id: "config", label: "配置预览与 JSON 覆写", icon: FileCode2 },
          ] as const
        ).map((tab) => {
          const Icon = tab.icon;
          const active = activeTab === tab.id;
          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                "-mb-px inline-flex items-center gap-2 border-b-2 px-4 py-2.5 text-sm font-medium transition-colors",
                active
                  ? "border-accent text-accent"
                  : "border-transparent text-ink-secondary hover:text-ink",
              )}
            >
              <Icon className="h-4 w-4" />
              {tab.label}
            </button>
          );
        })}
      </div>

      {/* Tab content */}
      {activeTab === "logs" && (
        <Panel
          title={activeNode ? `实时日志流 · ${activeNode.name}` : "实时核心与面板日志"}
          description="通过 SSE 实时接收目标节点 sing-box 与 Agent/面板守护器输出，支持按级别和关键词过滤"
        >
          <LogTerminal nodeId={activeNodeId} offline={nodeOffline} />
        </Panel>
      )}

      {activeTab === "versions" && (
        <div className="grid gap-5 lg:grid-cols-12">
          {/* GitHub Releases Switcher */}
          <Panel
            className="lg:col-span-8"
            title="SagerNet/sing-box 官方版本列表"
            description="四步原子切换：下载解压 → sing-box check 预检 → 备份替换 → 5 秒探活与自动回滚"
            actions={
              <div className="flex items-center gap-2">
                <div className="inline-flex rounded-md border border-hairline bg-recessed p-0.5 text-xs">
                  <button
                    type="button"
                    disabled={nodeOffline}
                    onClick={() => handleTogglePrerelease(false)}
                    className={cn(
                      "rounded px-2.5 py-1 transition-colors",
                      !includePrerelease
                        ? "bg-surface font-medium text-ink"
                        : "text-ink-muted hover:text-ink",
                    )}
                  >
                    Stable 正式版
                  </button>
                  <button
                    type="button"
                    disabled={nodeOffline}
                    onClick={() => handleTogglePrerelease(true)}
                    className={cn(
                      "rounded px-2.5 py-1 transition-colors",
                      includePrerelease
                        ? "bg-surface font-medium text-ink"
                        : "text-ink-muted hover:text-ink",
                    )}
                  >
                    含 Pre-release
                  </button>
                </div>

                <Button
                  variant="outline"
                  size="sm"
                  disabled={nodeOffline}
                  loading={loadingReleases}
                  onClick={() => void loadReleases(includePrerelease)}
                >
                  <RefreshCw className="h-3.5 w-3.5" />
                  刷新列表
                </Button>
              </div>
            }
          >
            <div className="mb-3.5 flex flex-wrap items-center gap-2">
              <Input
                value={proxyOverride}
                onChange={(e) => setProxyOverride(e.target.value)}
                disabled={nodeOffline}
                placeholder="可选：临时 GitHub 下载加速前缀（如 https://ghfast.top/），留空沿用节点/系统设置"
                className="h-8 flex-1 font-mono text-xs"
              />
            </div>

            {releaseError && (
              <div className="mb-3 rounded-md border border-danger/40 bg-danger/10 p-3 text-xs text-danger">
                {releaseError}
              </div>
            )}

            <div className="overflow-x-auto rounded-md border border-hairline">
              <table className="w-full text-left text-xs">
                <thead className="border-b border-hairline bg-recessed text-ink-secondary">
                  <tr>
                    <th className="px-3 py-2 font-medium">版本号</th>
                    <th className="px-3 py-2 font-medium">发行包</th>
                    <th className="px-3 py-2 font-medium">发布时间</th>
                    <th className="px-3 py-2 text-right font-medium">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-hairline">
                  {loadingReleases ? (
                    <tr>
                      <td colSpan={4} className="px-3 py-8 text-center text-ink-muted">
                        正在从 GitHub 拉取发布列表...
                      </td>
                    </tr>
                  ) : releases.length === 0 ? (
                    <tr>
                      <td colSpan={4} className="px-3 py-8 text-center text-ink-muted">
                        {nodeOffline
                          ? "当前节点离线，无法查询并下发升级"
                          : "暂无匹配当前架构的版本记录，请点击刷新或检查网络代理设置。"}
                      </td>
                    </tr>
                  ) : (
                    releases.map((rel) => {
                      const cleanCoreVer = (core?.version || "").replace(/^v/, "");
                      const cleanTag = rel.tag_name.replace(/^v/, "");
                      const isCurrent = Boolean(cleanCoreVer) && cleanCoreVer === cleanTag;
                      return (
                        <tr key={rel.tag_name} className="hover:bg-surface-hover/60">
                          <td className="px-3 py-2.5">
                            <div className="flex items-center gap-2">
                              <span className="font-mono font-semibold text-ink">
                                {rel.tag_name}
                              </span>
                              {rel.prerelease ? (
                                <Badge tone="warn">Pre</Badge>
                              ) : (
                                <Badge tone="neutral">Stable</Badge>
                              )}
                              {isCurrent && <Badge tone="ok">当前</Badge>}
                            </div>
                          </td>
                          <td className="px-3 py-2.5 font-mono text-ink-secondary">
                            <div className="max-w-[240px] truncate" title={rel.asset_name}>
                              {rel.asset_name || "-"}
                            </div>
                            {rel.size > 0 && (
                              <div className="text-[10px] text-ink-muted tabular-nums">
                                {formatBytes(rel.size)}
                              </div>
                            )}
                          </td>
                          <td className="px-3 py-2.5 font-mono text-ink-muted tabular-nums">
                            {formatDateTime(rel.published_at)}
                          </td>
                          <td className="px-3 py-2.5 text-right">
                            <Button
                              variant={isCurrent ? "ghost" : "secondary"}
                              size="xs"
                              loading={upgradingTag === rel.tag_name}
                              disabled={busyUpgrade || !rel.download_url || nodeOffline}
                              onClick={() => void handleUpgrade(rel)}
                            >
                              <Download className="h-3 w-3" />
                              {isCurrent ? "重新安装" : "安装 / 切换"}
                            </Button>
                          </td>
                        </tr>
                      );
                    })
                  )}
                </tbody>
              </table>
            </div>
          </Panel>

          {/* Manual binary upload dropzone */}
          <Panel
            className="lg:col-span-4"
            title="手动上传自定义核心"
            description="支持自编译二进制文件或官方 .tar.gz / .zip 压缩包"
          >
            <div
              onDragOver={(e) => {
                e.preventDefault();
                if (!nodeOffline) setDragOver(true);
              }}
              onDragLeave={() => setDragOver(false)}
              onDrop={handleDrop}
              onClick={() => !nodeOffline && fileInputRef.current?.click()}
              className={cn(
                "flex flex-col items-center justify-center rounded-md border border-dashed p-6 text-center transition-colors",
                nodeOffline
                  ? "cursor-not-allowed border-hairline bg-recessed opacity-55"
                  : dragOver
                    ? "cursor-pointer border-accent bg-accent-muted"
                    : "cursor-pointer border-hairline-strong bg-recessed hover:border-accent/60",
              )}
            >
              <Upload className="h-7 w-7 text-ink-muted" />
              <p className="mt-2.5 text-xs font-medium text-ink">
                {nodeOffline ? "节点离线，暂不可上传" : "拖拽核心文件到此处，或点击选择文件"}
              </p>
              <p className="mt-1 text-[11px] text-ink-muted">
                上传后自动推送至目标节点执行预检与 5 秒探活校验
              </p>
              <input
                ref={fileInputRef}
                type="file"
                disabled={nodeOffline}
                className="hidden"
                onChange={(e) => setSelectedFile(e.target.files?.[0] ?? null)}
              />
            </div>

            {selectedFile && !nodeOffline && (
              <div className="mt-3 rounded-md border border-hairline bg-recessed p-3 text-xs">
                <div className="truncate font-mono font-medium text-ink">
                  {selectedFile.name}
                </div>
                <div className="mt-0.5 font-mono text-[11px] text-ink-muted tabular-nums">
                  {formatBytes(selectedFile.size)}
                </div>
                <div className="mt-3 flex items-center gap-2">
                  <Button
                    variant="primary"
                    size="sm"
                    className="flex-1"
                    loading={uploading}
                    disabled={busyUpgrade}
                    onClick={() => void handleUpload()}
                  >
                    上传并原子替换
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={uploading}
                    onClick={() => setSelectedFile(null)}
                  >
                    移除
                  </Button>
                </div>
              </div>
            )}

            <div className="mt-4 space-y-1.5 border-t border-hairline pt-3 text-[11px] text-ink-muted">
              <p>升级安全保障流程：</p>
              <ol className="list-inside list-decimal space-y-1">
                <li>解压并校验可执行权限至节点临时 staging 路径</li>
                <li>调用新版核心执行 check 预检现有 config.json</li>
                <li>备份原二进制为 sing-box.bak 后原子替换启动</li>
                <li>5 秒内探活 Clash API，失败自动恢复原版</li>
              </ol>
            </div>
          </Panel>
        </div>
      )}

      {activeTab === "config" && <CoreConfigEditor nodeId={activeNodeId} />}
    </div>
  );
}
