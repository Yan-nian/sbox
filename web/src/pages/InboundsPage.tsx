import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  AlertTriangle,
  Check,
  CheckCircle2,
  Copy,
  Edit3,
  Globe,
  Link2,
  Loader2,
  Plus,
  Radar,
  Radio,
  RefreshCw,
  Shield,
  Trash2,
  Users,
  WifiOff,
  XCircle,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { cn } from "@/lib/cn";
import { formatBytes } from "@/lib/format";
import { useNode } from "@/contexts/NodeContext";
import { useToast } from "@/components/ui/Toast";
import {
  Badge,
  Button,
  Modal,
  Panel,
  StatusDot,
  Switch,
} from "@/components/ui/primitives";
import { InboundModal, PROTOCOL_OPTIONS } from "@/components/InboundModal";
import { NodeScopeBanner } from "@/components/NodeScopeBanner";
import type { AppRoute } from "@/components/AppShell";
import type {
  CertificateItem,
  InboundItem,
  InboundLinkItem,
  InboundPortCheckResult,
  MemberItem,
} from "@/types/api";

function describeSecurity(ib: InboundItem, certMap: Map<number, CertificateItem>): string {
  const s = ib.settings || {};
  if (ib.protocol === "vless") {
    if ((s.tls_mode || "reality") === "reality") {
      return `Reality · ${s.reality_handshake_server || s.server_name || "SNI"}`;
    }
    if (s.tls_mode === "tls") {
      const cert = ib.certificate_id ? certMap.get(ib.certificate_id) : null;
      return `TLS · ${cert?.name || "证书"}`;
    }
    return "无 TLS";
  }
  if (ib.protocol === "shadowtls") {
    return `ShadowTLS v3 · ${s.handshake_server || "SNI"} → ${s.detour_protocol || "shadowsocks"}`;
  }
  if (ib.protocol === "hysteria2" || ib.protocol === "tuic" || ib.protocol === "anytls" || ib.protocol === "trojan") {
    const cert = ib.certificate_id ? certMap.get(ib.certificate_id) : null;
    const extra =
      ib.protocol === "hysteria2" && s.server_ports
        ? ` · 跳端口 ${s.server_ports}`
        : ib.protocol === "tuic" && s.congestion_control
          ? ` · ${s.congestion_control.toUpperCase()}`
          : "";
    return `TLS (${cert?.name || "未选证书"})${extra}`;
  }
  if (ib.protocol === "shadowsocks") {
    return String(s.method || "ss-2022");
  }
  if (ib.protocol === "direct") {
    return `转发 → ${s.override_address || "-"}:${s.override_port || "-"}`;
  }
  return s.auth_enabled === false ? "无认证" : "成员认证";
}

export function InboundsPage({ onNavigate }: { onNavigate?: (route: AppRoute) => void }) {
  const { activeNodeId, activeNode } = useNode();
  const toast = useToast();
  const [inbounds, setInbounds] = useState<InboundItem[]>([]);
  const [certificates, setCertificates] = useState<CertificateItem[]>([]);
  const [members, setMembers] = useState<MemberItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);

  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<InboundItem | null>(null);

  // Quick-copy node links modal
  const [linksTarget, setLinksTarget] = useState<InboundItem | null>(null);
  const [linkItems, setLinkItems] = useState<InboundLinkItem[]>([]);
  const [linksLoading, setLinksLoading] = useState(false);
  const [linksError, setLinksError] = useState<string | null>(null);
  const [linkFormat, setLinkFormat] = useState<"uri" | "loon">("uri");
  const [copiedLinkKey, setCopiedLinkKey] = useState<string | null>(null);

  // Cross-node port reachability check state
  const [portResults, setPortResults] = useState<Record<number, InboundPortCheckResult>>({});
  const [checkingPorts, setCheckingPorts] = useState<Record<number, boolean>>({});
  const [checkingAllPorts, setCheckingAllPorts] = useState(false);
  const [expandedHintId, setExpandedHintId] = useState<number | null>(null);
  const pendingTimersRef = useRef<number[]>([]);

  const nodeOffline = activeNode ? !activeNode.online : false;

  useEffect(() => {
    setPortResults({});
    setCheckingPorts({});
    setExpandedHintId(null);
  }, [activeNodeId]);

  useEffect(() => {
    const timers = pendingTimersRef.current;
    return () => {
      for (const t of timers) window.clearTimeout(t);
    };
  }, []);

  const loadAll = useCallback(async () => {
    setLoading(true);
    try {
      const [ibRes, certRes, memRes] = await Promise.all([
        api.listInbounds(activeNodeId),
        api.listCertificates(activeNodeId).catch(() => ({ certificates: [] })),
        api.listMembers().catch(() => ({ members: [] })),
      ]);
      setInbounds(Array.isArray(ibRes.inbounds) ? ibRes.inbounds : []);
      setCertificates(Array.isArray(certRes.certificates) ? certRes.certificates : []);
      setMembers(Array.isArray(memRes.members) ? memRes.members : []);
    } catch (err) {
      toast.error("加载入站节点失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setLoading(false);
    }
  }, [activeNodeId, toast]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  const certMap = useMemo(() => {
    const map = new Map<number, CertificateItem>();
    for (const c of certificates) map.set(c.id, c);
    return map;
  }, [certificates]);

  const summary = useMemo(() => {
    let active = 0;
    let up = 0;
    let down = 0;
    for (const ib of inbounds) {
      if (ib.enabled) active += 1;
      up += ib.upload_bytes || 0;
      down += ib.download_bytes || 0;
    }
    return { active, up, down };
  }, [inbounds]);

  const runCheckSinglePort = useCallback(
    async (id: number, showToast = false) => {
      setCheckingPorts((prev) => ({ ...prev, [id]: true }));
      try {
        const res = await api.checkInboundPort(id);
        setPortResults((prev) => ({ ...prev, [id]: res }));
        if (showToast) {
          if (res.status === "open") {
            toast.success(
              `端口 ${res.port} 公网通 · ${res.latency_ms}ms`,
              res.probed_by_node_name
                ? `由 ${res.probed_by_node_name} 跨机探测`
                : res.message,
            );
          } else if (res.status === "local_only") {
            toast.error(`端口 ${res.port} 仅本机监听 · 公网未通`, res.hint || res.message);
          } else if (res.status === "not_listening") {
            toast.error(`端口 ${res.port} 未监听`, res.hint || res.message);
          } else if (res.status === "node_offline") {
            toast.error("节点离线，无法探测端口", res.message);
          } else {
            toast.info(`端口 ${res.port} 本机已监听`, res.hint || res.message);
          }
        }
      } catch (err) {
        if (showToast) {
          toast.error("端口检测失败", err instanceof ApiError ? err.message : undefined);
        }
      } finally {
        setCheckingPorts((prev) => ({ ...prev, [id]: false }));
      }
    },
    [toast],
  );

  const scheduleAutoPortCheck = useCallback(
    (id: number) => {
      setCheckingPorts((prev) => ({ ...prev, [id]: true }));
      const timer = window.setTimeout(() => {
        pendingTimersRef.current = pendingTimersRef.current.filter((t) => t !== timer);
        void runCheckSinglePort(id, false);
      }, 1200);
      pendingTimersRef.current.push(timer);
    },
    [runCheckSinglePort],
  );

  const runCheckAllPorts = async () => {
    const enabledIds = inbounds.filter((ib) => ib.enabled).map((ib) => ib.id);
    if (enabledIds.length === 0) return;

    setCheckingAllPorts(true);
    setCheckingPorts((prev) => {
      const next = { ...prev };
      for (const id of enabledIds) next[id] = true;
      return next;
    });

    try {
      const res = await api.checkAllInboundPorts(activeNodeId);
      const list = Array.isArray(res.results) ? res.results : [];
      setPortResults((prev) => {
        const next = { ...prev };
        for (const item of list) {
          next[item.inbound_id] = item;
        }
        return next;
      });
      const openCount = list.filter((r) => r.status === "open").length;
      const localOnlyCount = list.filter((r) => r.status === "local_only").length;
      const failCount = list.filter((r) => r.status === "not_listening").length;
      if (localOnlyCount > 0 || failCount > 0) {
        toast.info(
          `端口检测完成 (${list.length} 个入站)`,
          `公网通 ${openCount} · 仅本机监听 ${localOnlyCount} · 未监听 ${failCount}`,
        );
      } else {
        toast.success(`已完成 ${list.length} 个入站端口检测`);
      }
    } catch (err) {
      toast.error("批量检测端口失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setCheckingAllPorts(false);
      setCheckingPorts((prev) => {
        const next = { ...prev };
        for (const id of enabledIds) next[id] = false;
        return next;
      });
    }
  };

  const handleToggle = async (ib: InboundItem) => {
    setBusyId(ib.id);
    try {
      const updated = await api.toggleInbound(ib.id);
      setInbounds((prev) => prev.map((item) => (item.id === ib.id ? updated : item)));
      toast.success(
        updated.enabled ? `已启用入站 ${ib.name}` : `已停用入站 ${ib.name}`,
        "配置已重新编译并应用",
      );
      if (updated.enabled && !nodeOffline) {
        scheduleAutoPortCheck(updated.id);
      } else if (!updated.enabled) {
        setPortResults((prev) => {
          const next = { ...prev };
          delete next[ib.id];
          return next;
        });
      }
    } catch (err) {
      toast.error("切换入站状态失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyId(null);
    }
  };

  const handleInboundSaved = async (saved?: InboundItem) => {
    await loadAll();
    if (saved && saved.enabled && !nodeOffline) {
      scheduleAutoPortCheck(saved.id);
    }
  };

  const handleDelete = async (ib: InboundItem) => {
    setBusyId(ib.id);
    try {
      await api.deleteInbound(ib.id);
      setInbounds((prev) => prev.filter((item) => item.id !== ib.id));
      setConfirmDeleteId(null);
      toast.success("已删除入站节点", ib.name);
    } catch (err) {
      toast.error("删除入站失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyId(null);
    }
  };

  const openLinksModal = async (ib: InboundItem) => {
    setLinksTarget(ib);
    setLinkItems([]);
    setLinksError(null);
    setCopiedLinkKey(null);
    setLinksLoading(true);
    try {
      const res = await api.getInboundLinks(ib.id);
      setLinkItems(Array.isArray(res.items) ? res.items : []);
    } catch (err) {
      setLinksError(err instanceof ApiError ? err.message : "获取节点链接失败");
    } finally {
      setLinksLoading(false);
    }
  };

  const copyLinkValue = async (key: string, val: string, label: string) => {
    if (!val) return;
    const ok = await copyToClipboard(val);
    if (ok) {
      setCopiedLinkKey(key);
      toast.info(`已复制${label}`);
      window.setTimeout(() => setCopiedLinkKey(null), 1600);
    } else {
      toast.error("复制失败");
    }
  };

  return (
    <div className="space-y-5">
      <NodeScopeBanner
        onNavigate={onNavigate}
        offlineHint="当前远程节点离线：您仍可编辑和保存该节点的入站配置，Agent 重新上线后主控将自动推送并热重载。"
      />

      {/* Unified summary strip */}
      <div className="grid divide-y divide-hairline overflow-hidden rounded-lg border border-hairline bg-surface sm:grid-cols-4 sm:divide-x sm:divide-y-0">
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">入站节点总数</div>
          <div className="mt-1 font-mono text-lg font-semibold text-ink tabular-nums">
            {inbounds.length}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">活跃监听中</div>
          <div className="mt-1 font-mono text-lg font-semibold text-ok tabular-nums">
            {summary.active}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">入站累计上行</div>
          <div className="mt-1 font-mono text-lg font-semibold text-uplink tabular-nums">
            {formatBytes(summary.up)}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">入站累计下行</div>
          <div className="mt-1 font-mono text-lg font-semibold text-accent tabular-nums">
            {formatBytes(summary.down)}
          </div>
        </div>
      </div>

      <Panel
        title="多协议入站节点栈"
        description="支持 VLESS Reality/Vision、Hysteria2、TUIC v5、AnyTLS、SS-2022、Trojan、ShadowTLS v3 与端口转发"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="secondary"
              size="sm"
              loading={checkingAllPorts}
              disabled={loading || summary.active === 0 || nodeOffline || checkingAllPorts}
              onClick={() => void runCheckAllPorts()}
              title="检测当前节点所有已启用入站的本地监听与跨机公网连通性"
            >
              <Radar className="h-3.5 w-3.5" />
              检测全部端口
            </Button>
            <Button
              variant="outline"
              size="sm"
              loading={loading}
              onClick={() => void loadAll()}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              刷新
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={() => {
                setEditing(null);
                setModalOpen(true);
              }}
            >
              <Plus className="h-3.5 w-3.5" />
              新建入站
            </Button>
          </div>
        }
        bodyClassName="p-0"
      >
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="border-b border-hairline bg-recessed text-ink-secondary">
              <tr>
                <th className="px-4 py-2.5 font-medium">启停与节点名称</th>
                <th className="px-3 py-2.5 font-medium">协议与安全层</th>
                <th className="px-3 py-2.5 font-medium">监听与外部入口</th>
                <th className="px-3 py-2.5 font-medium">成员绑定</th>
                <th className="px-3 py-2.5 text-right font-medium">累计流量</th>
                <th className="px-4 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {loading && inbounds.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-12 text-center text-ink-muted">
                    正在加载入站列表...
                  </td>
                </tr>
              ) : inbounds.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-12 text-center">
                    <Radio className="mx-auto h-7 w-7 text-ink-muted" />
                    <p className="mt-2 text-xs text-ink-secondary">尚未配置任何入站节点</p>
                    <p className="mt-1 text-[11px] text-ink-muted">
                      新建入站时将自动生成默认密钥与端口，并在保存后即时编译生效。
                    </p>
                    <Button
                      variant="secondary"
                      size="sm"
                      className="mt-3"
                      onClick={() => {
                        setEditing(null);
                        setModalOpen(true);
                      }}
                    >
                      <Plus className="h-3.5 w-3.5" />
                      创建首个入站节点
                    </Button>
                  </td>
                </tr>
              ) : (
                inbounds.map((ib) => {
                  const protoMeta = PROTOCOL_OPTIONS.find((p) => p.id === ib.protocol);
                  const isBusy = busyId === ib.id;
                  const extraCount = (ib.extra_entries || []).length;

                  return (
                    <tr
                      key={ib.id}
                      className={cn(
                        "hover:bg-surface-hover/60",
                        !ib.enabled && "opacity-65",
                      )}
                    >
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-3">
                          <Switch
                            checked={ib.enabled}
                            disabled={isBusy}
                            onChange={() => void handleToggle(ib)}
                            label={`切换入站 ${ib.name}`}
                          />
                          <div>
                            <div className="flex items-center gap-2">
                              <StatusDot state={ib.enabled ? "ok" : "idle"} />
                              <span className="font-medium text-ink">{ib.name}</span>
                            </div>
                            <div className="mt-0.5 font-mono text-[11px] text-ink-muted">
                              tag: {ib.tag}
                            </div>
                          </div>
                        </div>
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <Badge tone={protoMeta?.badgeTone ?? "neutral"}>
                            {protoMeta?.label ?? ib.protocol.toUpperCase()}
                          </Badge>
                          {ib.settings?.transport && ib.settings.transport !== "tcp" && (
                            <Badge tone="neutral">
                              {String(ib.settings.transport).toUpperCase()}
                            </Badge>
                          )}
                        </div>
                        <div className="mt-1 flex items-center gap-1 font-mono text-[11px] text-ink-secondary">
                          <Shield className="h-3 w-3 shrink-0 text-ink-muted" />
                          <span className="max-w-[230px] truncate">
                            {describeSecurity(ib, certMap)}
                          </span>
                        </div>
                      </td>

                      <td className="px-3 py-3 font-mono">
                        <div className="text-ink tabular-nums">
                          {ib.listen === "::" ? `[::]:${ib.listen_port}` : `${ib.listen}:${ib.listen_port}`}
                        </div>
                        {(ib.external_host || extraCount > 0) && (
                          <div className="mt-1 flex flex-wrap items-center gap-1.5 text-[11px] text-ink-muted">
                            <Globe className="h-3 w-3 shrink-0" />
                            {ib.external_host && (
                              <span>
                                {ib.external_host}:{ib.external_port || ib.listen_port}
                              </span>
                            )}
                            {extraCount > 0 && (
                              <Badge tone="accent">+{extraCount} 中转</Badge>
                            )}
                          </div>
                        )}

                        {/* Port Reachability Status Badge & Quick Re-Check */}
                        {ib.enabled && (
                          <div className="mt-1.5 space-y-1 font-sans">
                            <div className="flex flex-wrap items-center gap-1.5">
                              {checkingPorts[ib.id] ? (
                                <Badge tone="accent">
                                  <Loader2 className="h-3 w-3 animate-spin" />
                                  测端口中...
                                </Badge>
                              ) : portResults[ib.id] ? (
                                (() => {
                                  const pr = portResults[ib.id];
                                  const tooltip =
                                    pr.status === "open"
                                      ? pr.probed_by_node_name
                                        ? `由 ${pr.probed_by_node_name} 跨机探测 (${pr.message || `${pr.latency_ms}ms`})`
                                        : pr.message || "公网连通"
                                      : [pr.message, pr.hint].filter(Boolean).join(" · ");

                                  const badgeTone =
                                    pr.status === "open"
                                      ? "ok"
                                      : pr.status === "local_only"
                                        ? "warn"
                                        : pr.status === "local_unverified"
                                          ? "accent"
                                          : pr.status === "not_listening"
                                            ? "danger"
                                            : "neutral";

                                  return (
                                    <button
                                      type="button"
                                      onClick={() =>
                                        setExpandedHintId((curr) =>
                                          curr === ib.id ? null : ib.id,
                                        )
                                      }
                                      title={tooltip}
                                      className="inline-flex text-left focus:outline-none"
                                    >
                                      <Badge tone={badgeTone} className="cursor-pointer">
                                        {pr.status === "open" && (
                                          <>
                                            <CheckCircle2 className="h-3 w-3" />
                                            <span className="font-mono tabular-nums">
                                              公网通 · {pr.latency_ms}ms
                                            </span>
                                          </>
                                        )}
                                        {pr.status === "local_only" && (
                                          <>
                                            <AlertTriangle className="h-3 w-3" />
                                            <span>仅本机监听 · 公网未通</span>
                                          </>
                                        )}
                                        {pr.status === "local_unverified" && (
                                          <>
                                            <CheckCircle2 className="h-3 w-3" />
                                            <span>本机已监听</span>
                                          </>
                                        )}
                                        {pr.status === "not_listening" && (
                                          <>
                                            <XCircle className="h-3 w-3" />
                                            <span>未监听</span>
                                          </>
                                        )}
                                        {pr.status === "node_offline" && (
                                          <>
                                            <WifiOff className="h-3 w-3" />
                                            <span>节点离线</span>
                                          </>
                                        )}
                                      </Badge>
                                    </button>
                                  );
                                })()
                              ) : null}

                              <button
                                type="button"
                                disabled={Boolean(checkingPorts[ib.id]) || nodeOffline}
                                onClick={() => void runCheckSinglePort(ib.id, true)}
                                title={portResults[ib.id] ? "重新检测端口连通性" : "检测端口监听与公网放行状态"}
                                className="inline-flex items-center gap-1 rounded border border-hairline bg-recessed px-1.5 py-0.5 text-[10px] font-medium text-ink-secondary transition-colors hover:border-accent/50 hover:text-accent disabled:pointer-events-none disabled:opacity-45"
                              >
                                <Radar className="h-3 w-3" />
                                <span>测端口</span>
                              </button>
                            </div>

                            {/* Always show actionable firewall hint for local_only / not_listening, or when clicked */}
                            {!checkingPorts[ib.id] &&
                              portResults[ib.id] &&
                              (portResults[ib.id].status === "local_only" ||
                                portResults[ib.id].status === "not_listening" ||
                                expandedHintId === ib.id) && (
                                <div
                                  className={cn(
                                    "max-w-[260px] text-[10px] leading-snug",
                                    portResults[ib.id].status === "local_only"
                                      ? "text-warn"
                                      : portResults[ib.id].status === "not_listening"
                                        ? "text-danger"
                                        : "text-ink-muted",
                                  )}
                                >
                                  {portResults[ib.id].hint || portResults[ib.id].message}
                                </div>
                              )}
                          </div>
                        )}
                      </td>

                      <td className="px-3 py-3">
                        {ib.protocol === "direct" ? (
                          <span className="text-ink-muted">端口透传</span>
                        ) : ib.member_mode === "selected" ? (
                          <div className="flex items-center gap-1.5 text-ink-secondary">
                            <Users className="h-3.5 w-3.5 text-accent" />
                            <span>指定 {(ib.member_ids || []).length} 位成员</span>
                          </div>
                        ) : (
                          <div className="flex items-center gap-1.5 text-ink-secondary">
                            <Users className="h-3.5 w-3.5 text-ink-muted" />
                            <span>全部启用成员</span>
                          </div>
                        )}
                      </td>

                      <td className="px-3 py-3 text-right font-mono tabular-nums">
                        <div className="text-uplink">↑ {formatBytes(ib.upload_bytes)}</div>
                        <div className="text-accent">↓ {formatBytes(ib.download_bytes)}</div>
                      </td>

                      <td className="px-4 py-3 text-right">
                        <div className="inline-flex items-center gap-1.5">
                          {ib.protocol !== "direct" && (
                            <Button
                              variant="secondary"
                              size="xs"
                              disabled={isBusy}
                              onClick={() => void openLinksModal(ib)}
                            >
                              <Link2 className="h-3 w-3" />
                              复制节点
                            </Button>
                          )}

                          <Button
                            variant="outline"
                            size="xs"
                            disabled={isBusy}
                            onClick={() => {
                              setEditing(ib);
                              setModalOpen(true);
                            }}
                          >
                            <Edit3 className="h-3 w-3" />
                            编辑
                          </Button>

                          {confirmDeleteId === ib.id ? (
                            <div className="inline-flex items-center gap-1 rounded border border-danger/45 bg-danger/10 px-2 py-0.5">
                              <span className="text-[11px] text-danger">确认删除？</span>
                              <Button
                                variant="danger"
                                size="xs"
                                loading={isBusy}
                                onClick={() => void handleDelete(ib)}
                              >
                                删除
                              </Button>
                              <Button
                                variant="ghost"
                                size="xs"
                                onClick={() => setConfirmDeleteId(null)}
                              >
                                取消
                              </Button>
                            </div>
                          ) : (
                            <Button
                              variant="danger"
                              size="xs"
                              disabled={isBusy}
                              onClick={() => setConfirmDeleteId(ib.id)}
                            >
                              <Trash2 className="h-3 w-3" />
                              删除
                            </Button>
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

      <InboundModal
        open={modalOpen}
        nodeId={activeNodeId}
        nodeName={activeNode?.name}
        nodeOffline={nodeOffline}
        editing={editing}
        certificates={certificates}
        members={members}
        onClose={() => setModalOpen(false)}
        onSaved={handleInboundSaved}
      />

      {/* Quick Copy Node Links Modal */}
      <Modal
        open={linksTarget !== null}
        onClose={() => setLinksTarget(null)}
        title={linksTarget ? `复制节点链接 · ${linksTarget.name}` : "复制节点链接"}
        description="支持通用标准 URI 与原生 Loon 节点配置行，点击即复制到剪贴板"
        size="lg"
        footer={
          <Button variant="ghost" size="sm" onClick={() => setLinksTarget(null)}>
            关闭
          </Button>
        }
      >
        <div className="space-y-4">
          {/* Format Toggle + One-Click Bar */}
          <div className="flex flex-wrap items-center justify-between gap-2.5">
            <div className="inline-flex rounded-md border border-hairline bg-recessed p-0.5 text-xs">
              <button
                type="button"
                onClick={() => setLinkFormat("uri")}
                className={cn(
                  "rounded px-2.5 py-1 transition-colors",
                  linkFormat === "uri"
                    ? "bg-surface font-medium text-accent"
                    : "text-ink-muted hover:text-ink",
                )}
              >
                通用 URI (vless:// ...)
              </button>
              <button
                type="button"
                onClick={() => setLinkFormat("loon")}
                className={cn(
                  "rounded px-2.5 py-1 transition-colors",
                  linkFormat === "loon"
                    ? "bg-surface font-medium text-accent"
                    : "text-ink-muted hover:text-ink",
                )}
              >
                Loon 原生行 (节点 = VLESS, ...)
              </button>
            </div>

            {!linksLoading && !linksError && linkItems.length === 1 && (
              <div className="flex flex-wrap items-center gap-1.5">
                <Button
                  variant="primary"
                  size="xs"
                  disabled={!linkItems[0].uri}
                  onClick={() =>
                    void copyLinkValue("quick-uri", linkItems[0].uri, "通用 URI")
                  }
                >
                  {copiedLinkKey === "quick-uri" ? (
                    <Check className="h-3 w-3" />
                  ) : (
                    <Copy className="h-3 w-3" />
                  )}
                  {copiedLinkKey === "quick-uri" ? "已复制 URI" : "一键复制 URI"}
                </Button>
                <Button
                  variant="secondary"
                  size="xs"
                  disabled={!linkItems[0].loon}
                  onClick={() =>
                    void copyLinkValue("quick-loon", linkItems[0].loon, "Loon 原生行")
                  }
                >
                  {copiedLinkKey === "quick-loon" ? (
                    <Check className="h-3 w-3 text-ok" />
                  ) : (
                    <Copy className="h-3 w-3" />
                  )}
                  {copiedLinkKey === "quick-loon" ? "已复制 Loon 行" : "一键复制 Loon 行"}
                </Button>
              </div>
            )}

            {!linksLoading && !linksError && linkItems.length > 1 && (
              <Button
                variant="outline"
                size="xs"
                onClick={() => {
                  const joined = linkItems
                    .map((item) => (linkFormat === "loon" ? item.loon : item.uri))
                    .filter(Boolean)
                    .join("\n");
                  void copyLinkValue(
                    `all-${linkFormat}`,
                    joined,
                    linkFormat === "loon" ? "全部 Loon 节点行" : "全部节点 URI",
                  );
                }}
              >
                {copiedLinkKey === `all-${linkFormat}` ? (
                  <Check className="h-3 w-3 text-ok" />
                ) : (
                  <Copy className="h-3 w-3" />
                )}
                {copiedLinkKey === `all-${linkFormat}`
                  ? "已复制全部"
                  : `复制全部 (${linkItems.length})`}
              </Button>
            )}
          </div>

          {linksLoading ? (
            <div className="flex items-center justify-center gap-2 py-10 text-xs text-ink-muted">
              <Loader2 className="h-4 w-4 animate-spin text-accent" />
              <span>正在生成节点链接...</span>
            </div>
          ) : linksError ? (
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-danger/45 bg-danger/10 p-3 text-xs text-danger">
              <span>{linksError}</span>
              {linksTarget && (
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() => void openLinksModal(linksTarget)}
                >
                  <RefreshCw className="h-3 w-3" />
                  重试
                </Button>
              )}
            </div>
          ) : linkItems.length === 0 ? (
            <div className="rounded-md border border-dashed border-hairline-strong bg-recessed py-9 text-center text-xs text-ink-muted">
              暂无可复制的节点链接（请先在「成员管理」创建并启用成员，并确认此入站已绑定成员）。
            </div>
          ) : (
            <div className="max-h-[420px] space-y-2.5 overflow-y-auto pr-0.5">
              {linkItems.map((item, idx) => {
                const value = linkFormat === "loon" ? item.loon : item.uri;
                const copyKey = `item-${idx}-${linkFormat}`;
                const isCopied = copiedLinkKey === copyKey;
                return (
                  <div
                    key={`${item.member_id}-${item.name}-${idx}`}
                    className="rounded-md border border-hairline bg-recessed/50 p-3"
                  >
                    <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                      <div className="flex flex-wrap items-center gap-2 text-xs">
                        <Badge tone="neutral" className="font-mono">
                          <Users className="h-3 w-3" />
                          {item.member_name}
                        </Badge>
                        <span className="font-medium text-ink">{item.name}</span>
                      </div>

                      <Button
                        variant={isCopied ? "secondary" : "outline"}
                        size="xs"
                        disabled={!value}
                        onClick={() =>
                          void copyLinkValue(
                            copyKey,
                            value,
                            `${item.name} (${linkFormat === "loon" ? "Loon 行" : "URI"})`,
                          )
                        }
                      >
                        {isCopied ? (
                          <Check className="h-3 w-3 text-ok" />
                        ) : (
                          <Copy className="h-3 w-3" />
                        )}
                        {isCopied ? "已复制" : "复制"}
                      </Button>
                    </div>

                    {value ? (
                      <pre className="max-h-24 overflow-auto whitespace-pre-wrap break-all rounded border border-hairline bg-recessed px-2.5 py-2 font-mono text-[11px] leading-relaxed text-ink select-all">
                        {value}
                      </pre>
                    ) : (
                      <div className="rounded border border-hairline bg-recessed px-2.5 py-2 text-[11px] text-ink-muted">
                        当前协议暂不支持{linkFormat === "loon" ? " Loon 原生行" : "通用 URI"}格式
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </Modal>
    </div>
  );
}
