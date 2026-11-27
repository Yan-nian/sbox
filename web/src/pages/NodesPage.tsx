import React, { useCallback, useMemo, useState } from "react";
import {
  AlertTriangle,
  ArrowUpRight,
  Check,
  CheckCircle2,
  Copy,
  Download,
  Edit3,
  KeyRound,
  Plus,
  RefreshCw,
  ShieldCheck,
  Terminal,
  Trash2,
  XCircle,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { getBasePath } from "@/lib/basePath";
import { copyToClipboard } from "@/lib/clipboard";
import { cn } from "@/lib/cn";
import { formatBytes, formatDateTime, formatSpeed } from "@/lib/format";
import { useNode } from "@/contexts/NodeContext";
import { useToast } from "@/components/ui/Toast";
import {
  Badge,
  Button,
  Field,
  Input,
  Modal,
  Panel,
  Select,
  StatusDot,
  Switch,
} from "@/components/ui/primitives";
import type { AppRoute } from "@/components/AppShell";
import type {
  NodeUpgradeBatchItem,
  NodeView,
  ReleaseInfo,
} from "@/types/api";

interface OnboardingCreds {
  nodeName: string;
  token: string;
  installCommand: string;
}

interface NodesPageProps {
  onNavigate: (route: AppRoute) => void;
}

export function NodesPage({ onNavigate }: NodesPageProps) {
  const { nodes, activeNodeId, setActiveNodeId, refreshNodes, loading } = useNode();
  const toast = useToast();

  const [refreshing, setRefreshing] = useState(false);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  // Add remote node modal
  const [addOpen, setAddOpen] = useState(false);
  const [addName, setAddName] = useState("");
  const [addIpv4, setAddIpv4] = useState("");
  const [addIpv6, setAddIpv6] = useState("");
  const [addNote, setAddNote] = useState("");
  const [creating, setCreating] = useState(false);

  // Onboarding command modal (shown after create OR token reset)
  const [onboarding, setOnboarding] = useState<OnboardingCreds | null>(null);
  const [appendInsecure, setAppendInsecure] = useState(false);

  // Edit node modal
  const [editTarget, setEditTarget] = useState<NodeView | null>(null);
  const [editName, setEditName] = useState("");
  const [editIpv4, setEditIpv4] = useState("");
  const [editIpv6, setEditIpv6] = useState("");
  const [editLogLevel, setEditLogLevel] = useState("info");
  const [editProxy, setEditProxy] = useState("");
  const [editNote, setEditNote] = useState("");
  const [savingEdit, setSavingEdit] = useState(false);

  // Reset token confirm / Delete conflict
  const [resetConfirmNode, setResetConfirmNode] = useState<NodeView | null>(null);
  const [resettingToken, setResettingToken] = useState(false);
  const [deletingId, setDeletingId] = useState<number | null>(null);
  const [forceDeleteTarget, setForceDeleteTarget] = useState<{
    node: NodeView;
    reason: string;
  } | null>(null);

  // Cluster-wide sing-box upgrade modal
  const [batchOpen, setBatchOpen] = useState(false);
  const [batchPrerelease, setBatchPrerelease] = useState(false);
  const [batchReleases, setBatchReleases] = useState<ReleaseInfo[]>([]);
  const [loadingBatchReleases, setLoadingBatchReleases] = useState(false);
  const [batchTag, setBatchTag] = useState("");
  const [batchProxy, setBatchProxy] = useState("");
  const [upgradingAll, setUpgradingAll] = useState(false);
  const [batchResults, setBatchResults] = useState<NodeUpgradeBatchItem[] | null>(null);

  const summary = useMemo(() => {
    let online = 0;
    let totalUp = 0;
    let totalDown = 0;
    let totalConns = 0;
    for (const n of nodes) {
      if (n.online) online += 1;
      totalUp += n.up_speed || 0;
      totalDown += n.down_speed || 0;
      totalConns += n.active_connections || 0;
    }
    return { online, totalUp, totalDown, totalConns };
  }, [nodes]);

  const handleRefresh = async () => {
    setRefreshing(true);
    try {
      await refreshNodes();
    } finally {
      setRefreshing(false);
    }
  };

  const copyValue = async (key: string, val: string, label: string) => {
    const ok = await copyToClipboard(val);
    if (ok) {
      setCopiedKey(key);
      toast.info(`已复制${label}`);
      window.setTimeout(() => setCopiedKey(null), 1600);
    } else {
      toast.error("复制失败");
    }
  };

  const handleCreateNode = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = addName.trim();
    if (!trimmed) {
      toast.error("请输入节点名称");
      return;
    }
    setCreating(true);
    try {
      const res = await api.createNode({
        name: trimmed,
        public_ipv4: addIpv4.trim() || undefined,
        public_ipv6: addIpv6.trim() || undefined,
        note: addNote.trim() || undefined,
      });
      setAddOpen(false);
      setAddName("");
      setAddIpv4("");
      setAddIpv6("");
      setAddNote("");
      setAppendInsecure(false);
      setOnboarding({
        nodeName: res.node.name,
        token: res.token,
        installCommand: res.install_command,
      });
      toast.success("已创建远程节点", res.node.name);
      await refreshNodes();
    } catch (err) {
      toast.error("添加节点失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setCreating(false);
    }
  };

  const openEditModal = (n: NodeView) => {
    setEditTarget(n);
    setEditName(n.name);
    setEditIpv4(n.public_ipv4 || "");
    setEditIpv6(n.public_ipv6 || "");
    setEditLogLevel(n.log_level || "info");
    setEditProxy(n.github_proxy || "");
    setEditNote(n.note || "");
  };

  const handleSaveEdit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editTarget) return;
    if (!editName.trim()) {
      toast.error("节点名称不能为空");
      return;
    }
    setSavingEdit(true);
    try {
      await api.updateNode(editTarget.id, {
        name: editName.trim(),
        public_ipv4: editIpv4.trim(),
        public_ipv6: editIpv6.trim(),
        log_level: editLogLevel,
        github_proxy: editProxy.trim(),
        note: editNote.trim(),
      });
      setEditTarget(null);
      toast.success("节点配置已保存", editName.trim());
      await refreshNodes();
    } catch (err) {
      toast.error("更新节点失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setSavingEdit(false);
    }
  };

  const handleConfirmResetToken = async () => {
    if (!resetConfirmNode) return;
    setResettingToken(true);
    try {
      const res = await api.resetNodeToken(resetConfirmNode.id);
      const nodeName = resetConfirmNode.name;
      setResetConfirmNode(null);
      setAppendInsecure(false);
      setOnboarding({
        nodeName,
        token: res.token,
        installCommand: res.install_command,
      });
      toast.success("已重置节点接入密钥", "旧连接已断开，请使用新命令重新接入 Agent");
      await refreshNodes();
    } catch (err) {
      toast.error("重置密钥失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setResettingToken(false);
    }
  };

  const handleDeleteNode = async (node: NodeView, force = false) => {
    setDeletingId(node.id);
    try {
      await api.deleteNode(node.id, force);
      setForceDeleteTarget(null);
      toast.success("已删除远程节点", node.name);
      await refreshNodes();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && !force) {
        setForceDeleteTarget({ node, reason: err.message });
      } else {
        toast.error("删除节点失败", err instanceof ApiError ? err.message : undefined);
      }
    } finally {
      setDeletingId(null);
    }
  };

  const loadBatchReleases = useCallback(
    async (pre: boolean) => {
      setLoadingBatchReleases(true);
      try {
        const res = await api.listReleases(pre, 1);
        const list = Array.isArray(res.releases) ? res.releases : [];
        setBatchReleases(list);
        if (list.length > 0 && !batchTag) {
          setBatchTag(list[0].tag_name);
        }
      } catch (err) {
        toast.error("获取 sing-box 版本列表失败", err instanceof ApiError ? err.message : undefined);
      } finally {
        setLoadingBatchReleases(false);
      }
    },
    [batchTag, toast],
  );

  const openBatchModal = () => {
    setBatchResults(null);
    setBatchOpen(true);
    if (batchReleases.length === 0) {
      void loadBatchReleases(batchPrerelease);
    }
  };

  const handleBatchUpgrade = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!batchTag.trim()) {
      toast.error("请选择或输入目标 sing-box 版本标签");
      return;
    }
    setUpgradingAll(true);
    setBatchResults(null);
    try {
      const res = await api.upgradeAllNodes({
        tag_name: batchTag.trim(),
        proxy_url: batchProxy.trim() || undefined,
      });
      const results = Array.isArray(res.results) ? res.results : [];
      setBatchResults(results);
      const okCount = results.filter((r) => r.result?.success).length;
      if (okCount === results.length && results.length > 0) {
        toast.success(`全集群 ${okCount} 个在线节点升级成功`);
      } else {
        toast.info(`集群升级完成：${okCount}/${results.length} 个节点成功`);
      }
      await refreshNodes();
    } catch (err) {
      toast.error("全集群升级请求失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setUpgradingAll(false);
    }
  };

  // Compute display install & manual agent commands
  const onboardingCommands = useMemo(() => {
    if (!onboarding) return null;
    const baseCmd = onboarding.installCommand.trim();
    const installCmd =
      appendInsecure && !baseCmd.includes("--insecure")
        ? `${baseCmd} --insecure`
        : baseCmd;

    const masterOrigin =
      typeof window !== "undefined"
        ? `${window.location.origin}${getBasePath()}`
        : "https://<master-host>:18080";
    const manualCmd = `sbox-panel agent --master "${masterOrigin}" --token "${onboarding.token}" --data-dir /var/lib/sbox-agent${appendInsecure ? " --insecure" : ""}`;
    return { installCmd, manualCmd };
  }, [onboarding, appendInsecure]);

  return (
    <div className="space-y-5">
      {/* Cluster Summary Strip */}
      <div className="grid divide-y divide-hairline overflow-hidden rounded-lg border border-hairline bg-surface sm:grid-cols-4 sm:divide-x sm:divide-y-0">
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">在线节点 / 总节点</div>
          <div className="mt-1 font-mono text-lg font-semibold text-ink tabular-nums">
            <span className="text-ok">{summary.online}</span>
            <span className="mx-1 text-ink-muted">/</span>
            <span>{nodes.length}</span>
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">集群实时下行汇总</div>
          <div className="mt-1 font-mono text-lg font-semibold text-accent tabular-nums">
            {formatSpeed(summary.totalDown)}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">集群实时上行汇总</div>
          <div className="mt-1 font-mono text-lg font-semibold text-uplink tabular-nums">
            {formatSpeed(summary.totalUp)}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">集群活跃连接总数</div>
          <div className="mt-1 font-mono text-lg font-semibold text-ink tabular-nums">
            {summary.totalConns}
          </div>
        </div>
      </div>

      {/* Nodes Inventory Panel */}
      <Panel
        title="多 VPS 集群节点管理"
        description="同二进制反向 WebSocket 架构：被管 VPS 仅运行极简 Agent 主动回连主控，无需开放面板端口，断网零中断"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              loading={refreshing || loading}
              onClick={() => void handleRefresh()}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              刷新状态
            </Button>
            <Button
              variant="secondary"
              size="sm"
              disabled={summary.online === 0}
              onClick={openBatchModal}
            >
              <Download className="h-3.5 w-3.5" />
              全集群升级 sing-box
            </Button>
            <Button variant="primary" size="sm" onClick={() => setAddOpen(true)}>
              <Plus className="h-3.5 w-3.5" />
              添加远程节点
            </Button>
          </div>
        }
        bodyClassName="p-0"
      >
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="border-b border-hairline bg-recessed text-ink-secondary">
              <tr>
                <th className="px-4 py-2.5 font-medium">节点与状态</th>
                <th className="px-3 py-2.5 font-medium">平台 / Agent / 核心</th>
                <th className="px-3 py-2.5 font-medium">公网地址</th>
                <th className="px-3 py-2.5 text-right font-medium">实时速率与内存</th>
                <th className="px-3 py-2.5 text-right font-medium">入站 / 连接</th>
                <th className="px-4 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {nodes.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-12 text-center text-ink-muted">
                    {loading ? "正在加载集群节点..." : "未发现已注册节点"}
                  </td>
                </tr>
              ) : (
                nodes.map((node) => {
                  const isCurrent = node.id === activeNodeId;
                  return (
                    <tr
                      key={node.id}
                      className={cn(
                        "hover:bg-surface-hover/60",
                        isCurrent && "bg-accent-muted/35",
                      )}
                    >
                      <td className="px-4 py-3">
                        <div className="flex flex-wrap items-center gap-2">
                          <StatusDot state={node.online ? "ok" : "idle"} />
                          <span className="font-mono text-sm font-semibold text-ink">
                            {node.name}
                          </span>
                          <Badge tone={node.is_local ? "accent" : "neutral"}>
                            {node.is_local ? "本机主控" : "远端 Agent"}
                          </Badge>
                          {isCurrent && <Badge tone="ok">当前视图</Badge>}
                        </div>
                        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-ink-muted">
                          {node.note && <span>{node.note}</span>}
                          {!node.online && node.last_seen_at && (
                            <span className="font-mono">
                              上次在线: {formatDateTime(node.last_seen_at)}
                            </span>
                          )}
                          {!node.online && !node.last_seen_at && (
                            <span>等待 Agent 首次回连</span>
                          )}
                        </div>
                      </td>

                      <td className="px-3 py-3 font-mono">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <Badge tone={node.core_running ? "ok" : "neutral"}>
                            {node.core_running
                              ? `sing-box ${node.core_version || "运行中"}`
                              : node.core_version
                                ? `已停止 (${node.core_version})`
                                : "未安装核心"}
                          </Badge>
                        </div>
                        <div className="mt-1 text-[11px] text-ink-muted">
                          {node.os || "-"} / {node.arch || "-"}
                          {node.agent_version ? ` · v${node.agent_version.replace(/^v/, "")}` : ""}
                        </div>
                      </td>

                      <td className="px-3 py-3 font-mono text-[11px]">
                        <div className="text-ink">{node.public_ipv4 || "-"}</div>
                        {node.public_ipv6 && (
                          <div className="mt-0.5 max-w-[170px] truncate text-ink-muted" title={node.public_ipv6}>
                            {node.public_ipv6}
                          </div>
                        )}
                      </td>

                      <td className="px-3 py-3 text-right font-mono tabular-nums">
                        <div className="text-accent">↓ {formatSpeed(node.down_speed)}</div>
                        <div className="text-uplink">↑ {formatSpeed(node.up_speed)}</div>
                        <div className="mt-0.5 text-[10px] text-ink-muted">
                          RAM: {formatBytes(node.mem_bytes)}
                        </div>
                      </td>

                      <td className="px-3 py-3 text-right font-mono tabular-nums">
                        <div className="text-ink">{node.inbound_count} 入站</div>
                        <div className="mt-0.5 text-[11px] text-ink-muted">
                          {node.active_connections} 活跃连接
                        </div>
                      </td>

                      <td className="px-4 py-3 text-right">
                        <div className="inline-flex flex-wrap items-center justify-end gap-1.5">
                          <Button
                            variant={isCurrent ? "ghost" : "secondary"}
                            size="xs"
                            onClick={() => {
                              setActiveNodeId(node.id);
                              onNavigate("/");
                            }}
                          >
                            <ArrowUpRight className="h-3 w-3" />
                            管理
                          </Button>

                          <Button
                            variant="outline"
                            size="xs"
                            onClick={() => openEditModal(node)}
                          >
                            <Edit3 className="h-3 w-3" />
                            编辑
                          </Button>

                          {!node.is_local && (
                            <>
                              <Button
                                variant="outline"
                                size="xs"
                                onClick={() => setResetConfirmNode(node)}
                                title="重置接入密钥并生成新的 Agent 安装命令"
                              >
                                <KeyRound className="h-3 w-3 text-warn" />
                                接入密钥
                              </Button>

                              <Button
                                variant="danger"
                                size="xs"
                                loading={deletingId === node.id}
                                disabled={deletingId !== null}
                                onClick={() => void handleDeleteNode(node, false)}
                              >
                                <Trash2 className="h-3 w-3" />
                                删除
                              </Button>
                            </>
                          )}
                        </div>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </Panel>

      {/* Add Remote Node Modal */}
      <Modal
        open={addOpen}
        onClose={() => !creating && setAddOpen(false)}
        title="添加远程受控 VPS 节点"
        description="创建后生成专属 64 位接入密钥与一键部署命令，被管 VPS 无需开放任何面板入站端口"
        size="md"
        footer={
          <>
            <Button
              variant="ghost"
              size="sm"
              disabled={creating}
              onClick={() => setAddOpen(false)}
            >
              取消
            </Button>
            <Button
              type="submit"
              form="add-node-form"
              variant="primary"
              size="sm"
              loading={creating}
            >
              创建并生成接入命令
            </Button>
          </>
        }
      >
        <form id="add-node-form" onSubmit={handleCreateNode} className="space-y-4">
          <Field
            label="节点名称"
            htmlFor="node-add-name"
            hint="唯一标识，例如 jp-tokyo、us-lax、hk-cmi"
          >
            <Input
              id="node-add-name"
              value={addName}
              onChange={(e) => setAddName(e.target.value)}
              placeholder="jp-tokyo"
              className="font-mono"
              required
            />
          </Field>

          <div className="grid gap-3.5 sm:grid-cols-2">
            <Field label="公网 IPv4（可选）" htmlFor="node-add-v4" hint="留空在 Agent 回连后自动上报">
              <Input
                id="node-add-v4"
                value={addIpv4}
                onChange={(e) => setAddIpv4(e.target.value)}
                placeholder="自动上报"
                className="font-mono"
              />
            </Field>
            <Field label="公网 IPv6（可选）" htmlFor="node-add-v6">
              <Input
                id="node-add-v6"
                value={addIpv6}
                onChange={(e) => setAddIpv6(e.target.value)}
                placeholder="自动上报"
                className="font-mono"
              />
            </Field>
          </div>

          <Field label="备注（可选）" htmlFor="node-add-note">
            <Input
              id="node-add-note"
              value={addNote}
              onChange={(e) => setAddNote(e.target.value)}
              placeholder="东京 IIJ 线路 · 1C1G"
            />
          </Field>
        </form>
      </Modal>

      {/* Onboarding Command Modal (after create or token reset) */}
      <Modal
        open={onboarding !== null}
        onClose={() => setOnboarding(null)}
        title={`远程节点接入命令 · ${onboarding?.nodeName ?? ""}`}
        description="密钥仅展示一次；在目标 VPS 上以 root 执行下方命令即可完成受控 Agent 部署"
        size="lg"
        footer={
          <Button variant="primary" size="sm" onClick={() => setOnboarding(null)}>
            我已保存命令
          </Button>
        }
      >
        {onboarding && onboardingCommands && (
          <div className="space-y-4 text-xs">
            <div className="flex items-start gap-2.5 rounded-md border border-ok/35 bg-ok/10 p-3">
              <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-ok" />
              <div className="text-ink-secondary leading-relaxed">
                <span className="font-medium text-ink">零入站暴露 & 断网自治：</span>
                被管 VPS 无需放行任何管理端口。Agent 启动后通过加密 WebSocket 主动回连主控；即使主控重启，节点本地{" "}
                <code className="font-mono text-ink">sing-box</code> 仍保持独立运行不中断。
              </div>
            </div>

            <div className="flex items-center justify-between rounded-md border border-hairline bg-recessed px-3.5 py-2.5">
              <div>
                <div className="font-medium text-ink">主控使用自签 HTTPS 证书 (--insecure)</div>
                <div className="text-[11px] text-ink-muted">
                  若主控启用了自签 TLS 证书，开启此选项将在命令中追加 --insecure
                </div>
              </div>
              <Switch
                checked={appendInsecure}
                onChange={setAppendInsecure}
                label="追加 --insecure 参数"
              />
            </div>

            {/* One-click install command */}
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <span className="flex items-center gap-1.5 font-medium text-ink">
                  <Terminal className="h-3.5 w-3.5 text-accent" />
                  一键安装并注册 systemd Agent 服务（推荐）
                </span>
                <Button
                  variant="secondary"
                  size="xs"
                  onClick={() =>
                    void copyValue("onb-install", onboardingCommands.installCmd, "一键接入命令")
                  }
                >
                  {copiedKey === "onb-install" ? (
                    <Check className="h-3 w-3 text-ok" />
                  ) : (
                    <Copy className="h-3 w-3" />
                  )}
                  复制命令
                </Button>
              </div>
              <pre className="overflow-x-auto rounded-md border border-hairline bg-recessed p-3 font-mono text-[11px] leading-relaxed text-ink">
                {onboardingCommands.installCmd}
              </pre>
            </div>

            {/* Manual binary invocation */}
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <span className="font-medium text-ink-secondary">
                  手动命令行启动（已有 sbox-panel 二进制时）
                </span>
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() =>
                    void copyValue("onb-manual", onboardingCommands.manualCmd, "手动启动命令")
                  }
                >
                  {copiedKey === "onb-manual" ? (
                    <Check className="h-3 w-3 text-ok" />
                  ) : (
                    <Copy className="h-3 w-3" />
                  )}
                  复制
                </Button>
              </div>
              <pre className="overflow-x-auto rounded-md border border-hairline bg-recessed p-3 font-mono text-[11px] leading-relaxed text-ink-secondary">
                {onboardingCommands.manualCmd}
              </pre>
            </div>

            {/* Raw node token */}
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <span className="text-ink-muted">节点接入密钥 (SBOX_TOKEN)</span>
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => void copyValue("onb-token", onboarding.token, "节点密钥")}
                >
                  {copiedKey === "onb-token" ? (
                    <Check className="h-3 w-3 text-ok" />
                  ) : (
                    <Copy className="h-3 w-3" />
                  )}
                  复制密钥
                </Button>
              </div>
              <code className="block truncate rounded border border-hairline bg-recessed px-3 py-2 font-mono text-[11px] text-ink">
                {onboarding.token}
              </code>
            </div>
          </div>
        )}
      </Modal>

      {/* Edit Node Modal */}
      <Modal
        open={editTarget !== null}
        onClose={() => !savingEdit && setEditTarget(null)}
        title={`编辑节点配置 · ${editTarget?.name ?? ""}`}
        size="md"
        footer={
          <>
            <Button
              variant="ghost"
              size="sm"
              disabled={savingEdit}
              onClick={() => setEditTarget(null)}
            >
              取消
            </Button>
            <Button
              type="submit"
              form="edit-node-form"
              variant="primary"
              size="sm"
              loading={savingEdit}
            >
              保存修改
            </Button>
          </>
        }
      >
        <form id="edit-node-form" onSubmit={handleSaveEdit} className="space-y-4">
          <div className="grid gap-3.5 sm:grid-cols-2">
            <Field label="节点名称" htmlFor="node-edit-name">
              <Input
                id="node-edit-name"
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
                className="font-mono"
                required
              />
            </Field>
            <Field label="sing-box 日志级别" htmlFor="node-edit-log">
              <Select
                id="node-edit-log"
                value={editLogLevel}
                onChange={(e) => setEditLogLevel(e.target.value)}
              >
                <option value="trace">trace</option>
                <option value="debug">debug</option>
                <option value="info">info</option>
                <option value="warn">warn</option>
                <option value="error">error</option>
                <option value="fatal">fatal</option>
              </Select>
            </Field>
          </div>

          <div className="grid gap-3.5 sm:grid-cols-2">
            <Field label="公网 IPv4 覆写" htmlFor="node-edit-v4">
              <Input
                id="node-edit-v4"
                value={editIpv4}
                onChange={(e) => setEditIpv4(e.target.value)}
                className="font-mono"
              />
            </Field>
            <Field label="公网 IPv6 覆写" htmlFor="node-edit-v6">
              <Input
                id="node-edit-v6"
                value={editIpv6}
                onChange={(e) => setEditIpv6(e.target.value)}
                className="font-mono"
              />
            </Field>
          </div>

          <Field
            label="节点专属 GitHub 下载代理（可选）"
            htmlFor="node-edit-proxy"
            hint="留空沿用主控全局设置，例如 https://ghfast.top/"
          >
            <Input
              id="node-edit-proxy"
              value={editProxy}
              onChange={(e) => setEditProxy(e.target.value)}
              className="font-mono"
            />
          </Field>

          <Field label="备注" htmlFor="node-edit-note">
            <Input
              id="node-edit-note"
              value={editNote}
              onChange={(e) => setEditNote(e.target.value)}
            />
          </Field>
        </form>
      </Modal>

      {/* Reset Node Token Confirm Modal */}
      <Modal
        open={resetConfirmNode !== null}
        onClose={() => !resettingToken && setResetConfirmNode(null)}
        title="重置远程节点接入密钥"
        description={`节点：${resetConfirmNode?.name ?? ""}`}
        size="md"
        footer={
          <>
            <Button
              variant="ghost"
              size="sm"
              disabled={resettingToken}
              onClick={() => setResetConfirmNode(null)}
            >
              取消
            </Button>
            <Button
              variant="danger"
              size="sm"
              loading={resettingToken}
              onClick={() => void handleConfirmResetToken()}
            >
              确认重置并生成新命令
            </Button>
          </>
        }
      >
        <p className="text-xs leading-relaxed text-ink-secondary">
          重置密钥后，旧密钥将立即失效并主动断开当前 Agent WebSocket 连接（该节点本地运行的 sing-box 不受影响）。您需要在目标 VPS 上使用新生成的命令或更新{" "}
          <code className="font-mono text-ink">/etc/sbox-agent.env</code> 重新接入。
        </p>
      </Modal>

      {/* Force Delete Node Modal (HTTP 409) */}
      <Modal
        open={forceDeleteTarget !== null}
        onClose={() => setForceDeleteTarget(null)}
        title="节点下仍有入站或证书"
        description={forceDeleteTarget?.reason}
        size="md"
        footer={
          <>
            <Button variant="ghost" size="sm" onClick={() => setForceDeleteTarget(null)}>
              取消
            </Button>
            <Button
              variant="danger"
              size="sm"
              loading={deletingId === forceDeleteTarget?.node.id}
              onClick={() =>
                forceDeleteTarget && void handleDeleteNode(forceDeleteTarget.node, true)
              }
            >
              强制删除节点及其资源
            </Button>
          </>
        }
      >
        <p className="text-xs leading-relaxed text-ink-secondary">
          远程节点 <strong className="font-mono text-ink">{forceDeleteTarget?.node.name}</strong>{" "}
          仍关联有入站节点或托管证书。强制删除将断开 Agent 并从主控数据库中一并移除该节点的入站与证书记录。
        </p>
      </Modal>

      {/* Batch Cluster-Wide sing-box Upgrade Modal */}
      <Modal
        open={batchOpen}
        onClose={() => !upgradingAll && setBatchOpen(false)}
        title="全集群批量升级 sing-box 核心"
        description={`将向当前全部 ${summary.online} 个在线节点并发下发原子升级任务（每个节点独立预检与自动回滚）`}
        size="lg"
        footer={
          <>
            <Button
              variant="ghost"
              size="sm"
              disabled={upgradingAll}
              onClick={() => setBatchOpen(false)}
            >
              关闭
            </Button>
            <Button
              type="submit"
              form="batch-upgrade-form"
              variant="primary"
              size="sm"
              loading={upgradingAll}
              disabled={summary.online === 0}
            >
              <Download className="h-3.5 w-3.5" />
              开始全集群升级 ({summary.online} 节点)
            </Button>
          </>
        }
      >
        <form id="batch-upgrade-form" onSubmit={handleBatchUpgrade} className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="inline-flex rounded-md border border-hairline bg-recessed p-0.5 text-xs">
              <button
                type="button"
                onClick={() => {
                  setBatchPrerelease(false);
                  void loadBatchReleases(false);
                }}
                className={cn(
                  "rounded px-2.5 py-1 transition-colors",
                  !batchPrerelease
                    ? "bg-surface font-medium text-ink"
                    : "text-ink-muted hover:text-ink",
                )}
              >
                Stable 正式版
              </button>
              <button
                type="button"
                onClick={() => {
                  setBatchPrerelease(true);
                  void loadBatchReleases(true);
                }}
                className={cn(
                  "rounded px-2.5 py-1 transition-colors",
                  batchPrerelease
                    ? "bg-surface font-medium text-ink"
                    : "text-ink-muted hover:text-ink",
                )}
              >
                含 Pre-release
              </button>
            </div>

            <Button
              type="button"
              variant="outline"
              size="xs"
              loading={loadingBatchReleases}
              onClick={() => void loadBatchReleases(batchPrerelease)}
            >
              <RefreshCw className="h-3 w-3" />
              刷新 Release
            </Button>
          </div>

          <div className="grid gap-3.5 sm:grid-cols-2">
            <Field label="目标版本标签 (tag_name)" htmlFor="batch-tag">
              {batchReleases.length > 0 ? (
                <Select
                  id="batch-tag"
                  value={batchTag}
                  onChange={(e) => setBatchTag(e.target.value)}
                >
                  {batchReleases.map((rel) => (
                    <option key={rel.tag_name} value={rel.tag_name}>
                      {rel.tag_name} {rel.prerelease ? "(Pre-release)" : "(Stable)"}
                    </option>
                  ))}
                </Select>
              ) : (
                <Input
                  id="batch-tag"
                  value={batchTag}
                  onChange={(e) => setBatchTag(e.target.value)}
                  placeholder="例如 v1.12.0"
                  className="font-mono"
                  required
                />
              )}
            </Field>

            <Field
              label="临时下载加速代理（可选）"
              htmlFor="batch-proxy"
              hint="留空沿用各节点配置的 GitHub 代理"
            >
              <Input
                id="batch-proxy"
                value={batchProxy}
                onChange={(e) => setBatchProxy(e.target.value)}
                placeholder="https://ghfast.top/"
                className="font-mono"
              />
            </Field>
          </div>

          {batchResults && (
            <div className="space-y-2 border-t border-hairline pt-4">
              <div className="text-xs font-medium text-ink">各节点升级结果</div>
              <div className="overflow-x-auto rounded-md border border-hairline">
                <table className="w-full text-left text-xs">
                  <thead className="border-b border-hairline bg-recessed text-ink-secondary">
                    <tr>
                      <th className="px-3 py-2 font-medium">节点</th>
                      <th className="px-3 py-2 font-medium">版本变更</th>
                      <th className="px-3 py-2 font-medium">结果</th>
                      <th className="px-3 py-2 font-medium">详情</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-hairline">
                    {batchResults.map((item) => {
                      const r = item.result;
                      return (
                        <tr key={item.node_id}>
                          <td className="px-3 py-2 font-mono font-medium text-ink">
                            {item.node_name}
                          </td>
                          <td className="px-3 py-2 font-mono text-ink-secondary">
                            {r?.from_version || "-"} → {r?.to_version || batchTag}
                          </td>
                          <td className="px-3 py-2">
                            {r?.success ? (
                              <Badge tone="ok">
                                <CheckCircle2 className="h-3 w-3" />
                                成功
                              </Badge>
                            ) : r?.rolled_back ? (
                              <Badge tone="warn">
                                <AlertTriangle className="h-3 w-3" />
                                已自动回滚
                              </Badge>
                            ) : (
                              <Badge tone="danger">
                                <XCircle className="h-3 w-3" />
                                失败
                              </Badge>
                            )}
                          </td>
                          <td className="px-3 py-2 text-ink-secondary">
                            <div>{r?.message || "-"}</div>
                            {r?.check_output && (
                              <pre className="mt-1 max-h-20 overflow-auto rounded bg-recessed p-1.5 font-mono text-[10px]">
                                {r.check_output}
                              </pre>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </form>
      </Modal>
    </div>
  );
}
