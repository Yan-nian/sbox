import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Check,
  Copy,
  Download,
  ExternalLink,
  FileCode2,
  Globe,
  Info,
  Link2,
  QrCode as QrCodeIcon,
  RefreshCw,
  Server,
  Users,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { getBasePath } from "@/lib/basePath";
import { copyToClipboard } from "@/lib/clipboard";
import { cn } from "@/lib/cn";
import { useNode } from "@/contexts/NodeContext";
import { useToast } from "@/components/ui/Toast";
import { Badge, Button, Modal, Panel, Select } from "@/components/ui/primitives";
import { QrCode } from "@/components/QrCode";
import type {
  MemberItem,
  SubFormat,
  SubscriptionNodeItem,
  SubscriptionPreviewResponse,
} from "@/types/api";

const FORMAT_TABS: { id: SubFormat; label: string; ext: string; mime: string }[] = [
  { id: "base64", label: "自适应 / Base64", ext: "txt", mime: "text/plain;charset=utf-8" },
  { id: "uri", label: "明文 URI 列表", ext: "txt", mime: "text/plain;charset=utf-8" },
  { id: "singbox", label: "sing-box JSON", ext: "json", mime: "application/json;charset=utf-8" },
  { id: "clash", label: "Clash Mihomo YAML", ext: "yaml", mime: "text/yaml;charset=utf-8" },
];

type SubQrScheme = "loon" | "raw" | "shadowrocket" | "singbox" | "clash";

const SUB_QR_SCHEMES: {
  id: SubQrScheme;
  label: string;
  actionLabel: string;
  hint: string;
}[] = [
  {
    id: "loon",
    label: "Loon",
    actionLabel: "唤起 Loon 导入",
    hint: "loon://import?sub=...（iOS Loon 扫码或点击直接导入）",
  },
  {
    id: "raw",
    label: "通用链接",
    actionLabel: "打开订阅链接",
    hint: "原始 HTTP(S) 订阅地址，兼容所有客户端粘贴/扫码",
  },
  {
    id: "shadowrocket",
    label: "Shadowrocket",
    actionLabel: "唤起 Shadowrocket",
    hint: "sub://<base64>（小火箭扫码一键添加订阅）",
  },
  {
    id: "singbox",
    label: "sing-box",
    actionLabel: "唤起 sing-box",
    hint: "sing-box://import-remote-profile?url=...",
  },
  {
    id: "clash",
    label: "Clash",
    actionLabel: "唤起 Clash",
    hint: "clash://install-config?url=...",
  },
];

function utf8ToBase64(input: string): string {
  const bytes = new TextEncoder().encode(input);
  let binary = "";
  for (let i = 0; i < bytes.length; i += 1) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary);
}

function buildSchemeUrl(scheme: SubQrScheme, subUrl: string): string {
  if (!subUrl) return "";
  switch (scheme) {
    case "loon":
      return `loon://import?sub=${encodeURIComponent(subUrl)}`;
    case "shadowrocket":
      return `sub://${utf8ToBase64(subUrl)}`;
    case "singbox":
      return `sing-box://import-remote-profile?url=${encodeURIComponent(subUrl)}`;
    case "clash":
      return `clash://install-config?url=${encodeURIComponent(subUrl)}`;
    case "raw":
    default:
      return subUrl;
  }
}

function isLoopbackHostname(hostname: string): boolean {
  const lower = hostname.toLowerCase();
  return lower === "127.0.0.1" || lower === "localhost" || lower === "::1" || lower === "[::1]";
}

export function ClientSubTab() {
  const toast = useToast();
  const { nodes: clusterNodes } = useNode();
  const [members, setMembers] = useState<MemberItem[]>([]);
  const [selectedMemberId, setSelectedMemberId] = useState<number>(0);
  const [format, setFormat] = useState<SubFormat>("base64");
  const [useAutoUrl, setUseAutoUrl] = useState(true);
  const [qrScheme, setQrScheme] = useState<SubQrScheme>("loon");
  const [qrModalNode, setQrModalNode] = useState<SubscriptionNodeItem | null>(null);
  const [loadingMembers, setLoadingMembers] = useState(true);
  const [loadingPreview, setLoadingPreview] = useState(false);
  const [preview, setPreview] = useState<SubscriptionPreviewResponse | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  const loadMembers = useCallback(async () => {
    setLoadingMembers(true);
    try {
      const res = await api.listMembers();
      const list = Array.isArray(res.members) ? res.members : [];
      setMembers(list);
      if (list.length > 0) {
        setSelectedMemberId((curr) =>
          curr && list.some((m) => m.id === curr)
            ? curr
            : (list.find((m) => m.enabled)?.id ?? list[0].id),
        );
      }
    } catch (err) {
      toast.error("加载成员列表失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setLoadingMembers(false);
    }
  }, [toast]);

  const loadPreview = useCallback(
    async (memberId: number, fmt: SubFormat) => {
      if (!memberId) {
        setPreview(null);
        return;
      }
      setLoadingPreview(true);
      setPreviewError(null);
      try {
        const res = await api.previewSubscription(memberId, fmt);
        setPreview(res);
      } catch (err) {
        setPreview(null);
        setPreviewError(err instanceof ApiError ? err.message : "生成订阅预览失败");
      } finally {
        setLoadingPreview(false);
      }
    },
    [],
  );

  useEffect(() => {
    void loadMembers();
  }, [loadMembers]);

  useEffect(() => {
    if (selectedMemberId > 0) {
      void loadPreview(selectedMemberId, format);
    }
  }, [selectedMemberId, format, loadPreview]);

  const selectedMember = useMemo(
    () => members.find((m) => m.id === selectedMemberId) ?? null,
    [members, selectedMemberId],
  );

  const localNode = useMemo(
    () => clusterNodes.find((n) => n.is_local) ?? clusterNodes[0] ?? null,
    [clusterNodes],
  );

  const { effectiveOrigin, isLoopbackHost, substitutedPublicIp } = useMemo(() => {
    if (typeof window === "undefined") {
      return { effectiveOrigin: "", isLoopbackHost: false, substitutedPublicIp: "" };
    }
    const { protocol, hostname, port, origin } = window.location;
    const loopback = isLoopbackHostname(hostname);
    const pubIpv4 = localNode?.public_ipv4?.trim() || "";
    if (loopback && pubIpv4) {
      const formattedHost = pubIpv4.includes(":") && !pubIpv4.startsWith("[") ? `[${pubIpv4}]` : pubIpv4;
      const portSuffix = port ? `:${port}` : "";
      return {
        effectiveOrigin: `${protocol}//${formattedHost}${portSuffix}`,
        isLoopbackHost: true,
        substitutedPublicIp: pubIpv4,
      };
    }
    return {
      effectiveOrigin: origin,
      isLoopbackHost: loopback,
      substitutedPublicIp: "",
    };
  }, [localNode?.public_ipv4]);

  const fullSubUrl = useMemo(() => {
    if (!selectedMember?.sub_token) return "";
    const base = getBasePath();
    if (useAutoUrl) {
      return `${effectiveOrigin}${base}/sub/${selectedMember.sub_token}?format=auto`;
    }
    if (preview?.sub_url) {
      if (/^https?:\/\//i.test(preview.sub_url)) {
        if (substitutedPublicIp) {
          try {
            const parsed = new URL(preview.sub_url);
            if (isLoopbackHostname(parsed.hostname)) {
              parsed.hostname = substitutedPublicIp;
              return parsed.toString();
            }
          } catch {
            // Fall back to raw URL
          }
        }
        return preview.sub_url;
      }
      return `${effectiveOrigin}${preview.sub_url.startsWith("/") ? "" : "/"}${preview.sub_url}`;
    }
    return `${effectiveOrigin}${base}/sub/${selectedMember.sub_token}?format=${format}`;
  }, [selectedMember, useAutoUrl, preview?.sub_url, format, effectiveOrigin, substitutedPublicIp]);

  const activeQrSchemeMeta = useMemo(
    () => SUB_QR_SCHEMES.find((s) => s.id === qrScheme) ?? SUB_QR_SCHEMES[0],
    [qrScheme],
  );

  const subQrPayload = useMemo(
    () => buildSchemeUrl(qrScheme, fullSubUrl),
    [qrScheme, fullSubUrl],
  );

  const loonImportUrl = useMemo(
    () => buildSchemeUrl("loon", fullSubUrl),
    [fullSubUrl],
  );

  const copyValue = async (key: string, val: string, label: string) => {
    if (!val) return;
    const ok = await copyToClipboard(val);
    if (ok) {
      setCopiedKey(key);
      toast.info(`已复制${label}`);
      window.setTimeout(() => setCopiedKey(null), 1600);
    } else {
      toast.error("复制失败");
    }
  };

  const handleDownload = () => {
    if (!preview?.content) return;
    const fmtMeta = FORMAT_TABS.find((f) => f.id === format) ?? FORMAT_TABS[0];
    const memberSlug = (selectedMember?.name || "member").replace(/[^a-zA-Z0-9_-]/g, "_");
    const filename = `sbox-${memberSlug}-${format}.${fmtMeta.ext}`;
    const blob = new Blob([preview.content], { type: fmtMeta.mime });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
    toast.success("已下载客户端配置文件", filename);
  };

  const nodes = preview?.nodes ?? [];

  return (
    <div className="space-y-5">
      {/* Control Bar: Member Picker + Format Switcher */}
      <Panel bodyClassName="p-4">
        <div className="flex flex-col gap-3.5 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex flex-wrap items-center gap-3">
            <div className="flex items-center gap-2 text-xs text-ink-secondary">
              <Users className="h-4 w-4 text-accent" />
              <span>选择订阅成员</span>
            </div>
            <Select
              value={selectedMemberId ? String(selectedMemberId) : ""}
              disabled={loadingMembers || members.length === 0}
              onChange={(e) => setSelectedMemberId(Number.parseInt(e.target.value, 10) || 0)}
              className="w-52 font-mono text-xs"
              aria-label="选择订阅成员"
            >
              {members.length === 0 ? (
                <option value="">请先创建成员</option>
              ) : (
                members.map((m) => (
                  <option key={m.id} value={String(m.id)}>
                    {m.name} {m.enabled ? "" : "(已停用)"}
                  </option>
                ))
              )}
            </Select>

            {selectedMember && (
              <Badge tone={selectedMember.enabled ? "ok" : "warn"}>
                {selectedMember.enabled ? "订阅有效" : "成员已停用（拉取将返回 404）"}
              </Badge>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <div className="inline-flex flex-wrap rounded-md border border-hairline bg-recessed p-0.5 text-xs">
              {FORMAT_TABS.map((t) => (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => setFormat(t.id)}
                  className={cn(
                    "rounded px-2.5 py-1 transition-colors",
                    format === t.id
                      ? "bg-surface font-medium text-accent"
                      : "text-ink-muted hover:text-ink",
                  )}
                >
                  {t.label}
                </button>
              ))}
            </div>

            <Button
              variant="outline"
              size="sm"
              loading={loadingPreview}
              disabled={!selectedMemberId}
              onClick={() => void loadPreview(selectedMemberId, format)}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              刷新
            </Button>
          </div>
        </div>
      </Panel>

      {members.length === 0 && !loadingMembers ? (
        <Panel>
          <div className="py-10 text-center text-xs text-ink-muted">
            尚未创建任何成员。请先前往「成员管理」创建成员后再分发客户端订阅链接。
          </div>
        </Panel>
      ) : (
        <>
          {/* Subscription Link + QR Code Card */}
          <Panel
            title="全集群客户端订阅链接与扫码导入"
            description="自动聚合集群内全部在线/配置节点下该成员有权限的入站（含多中转 extra_entries 派生节点）"
            actions={
              <div className="inline-flex rounded-md border border-hairline bg-recessed p-0.5 text-[11px]">
                <button
                  type="button"
                  onClick={() => setUseAutoUrl(true)}
                  className={cn(
                    "rounded px-2 py-0.5 transition-colors",
                    useAutoUrl
                      ? "bg-surface font-medium text-accent"
                      : "text-ink-muted hover:text-ink",
                  )}
                >
                  UA 自适应链接
                </button>
                <button
                  type="button"
                  onClick={() => setUseAutoUrl(false)}
                  className={cn(
                    "rounded px-2 py-0.5 transition-colors",
                    !useAutoUrl
                      ? "bg-surface font-medium text-accent"
                      : "text-ink-muted hover:text-ink",
                  )}
                >
                  固定 {format} 格式
                </button>
              </div>
            }
          >
            <div className="flex flex-col gap-6 lg:flex-row lg:items-start lg:justify-between">
              <div className="min-w-0 flex-1 space-y-3.5">
                <div>
                  <div className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-ink-secondary">
                    <Link2 className="h-3.5 w-3.5 text-accent" />
                    <span>
                      {useAutoUrl
                        ? "通用订阅地址（按客户端 User-Agent 自动识别 Loon / sing-box / Clash / Shadowrocket / v2rayN）"
                        : `显式格式订阅地址 (format=${format})`}
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 truncate rounded-md border border-hairline bg-recessed px-3 py-2 font-mono text-xs text-ink">
                      {fullSubUrl || "-"}
                    </code>
                    <Button
                      variant="primary"
                      size="md"
                      disabled={!fullSubUrl}
                      onClick={() => void copyValue("sub-url", fullSubUrl, "订阅链接")}
                    >
                      {copiedKey === "sub-url" ? (
                        <Check className="h-3.5 w-3.5" />
                      ) : (
                        <Copy className="h-3.5 w-3.5" />
                      )}
                      复制链接
                    </Button>
                  </div>
                </div>

                {isLoopbackHost && (
                  <div className="flex items-start gap-2 rounded-md border border-accent/35 bg-accent/10 px-3 py-2 text-[11px] leading-relaxed text-ink-secondary">
                    <Info className="mt-0.5 h-3.5 w-3.5 shrink-0 text-accent" />
                    <div>
                      {substitutedPublicIp ? (
                        <>
                          检测到当前经由本地回环 / SSH 隧道访问，已自动将订阅地址替换为本机公网 IP{" "}
                          <code className="font-mono text-ink">{substitutedPublicIp}</code>。
                          若云主机安全组未放行面板端口，手机无法直连拉取订阅，可直接扫描下方「单节点二维码」或通过「远端同步推送」推送至 Sub-Store。
                        </>
                      ) : (
                        <>
                          检测到当前通过 <code className="font-mono text-ink">127.0.0.1</code>{" "}
                          访问且尚未配置本机公网 IP；可前往「安全与设置」点击自动探测，或直接扫描下方「单节点二维码」/ 推送至 Sub-Store。
                        </>
                      )}
                    </div>
                  </div>
                )}

                <div className="flex flex-wrap items-center gap-2.5 text-xs">
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={!preview?.content}
                    onClick={handleDownload}
                  >
                    <Download className="h-3.5 w-3.5" />
                    下载配置文件 (.{FORMAT_TABS.find((f) => f.id === format)?.ext})
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!preview?.content}
                    onClick={() =>
                      preview?.content &&
                      void copyValue("raw-content", preview.content, "完整配置内容")
                    }
                  >
                    {copiedKey === "raw-content" ? (
                      <Check className="h-3.5 w-3.5 text-ok" />
                    ) : (
                      <FileCode2 className="h-3.5 w-3.5" />
                    )}
                    复制全部内容
                  </Button>
                  {fullSubUrl && (
                    <a
                      href={loonImportUrl}
                      className="inline-flex h-8 items-center justify-center gap-1.5 rounded-md border border-accent/45 bg-accent/10 px-3 text-xs font-medium text-accent transition-colors hover:bg-accent/20"
                    >
                      <ExternalLink className="h-3.5 w-3.5" />
                      一键导入 Loon
                    </a>
                  )}
                  <Badge tone="accent" className="font-mono tabular-nums">
                    聚合 {nodes.length} 条节点
                  </Badge>
                </div>

                <p className="text-[11px] leading-relaxed text-ink-muted">
                  响应头自动附带标准{" "}
                  <code className="font-mono text-ink-secondary">Subscription-Userinfo</code>{" "}
                  以在客户端展示成员已用上下行流量。该接口通过专属{" "}
                  <code className="font-mono text-ink-secondary">sub_token</code> 鉴权，无需管理员登录态。
                  {!isLoopbackHost && " 若面板端口受安全组限制，也可扫描下方单节点二维码或推送至 Sub-Store。"}
                </p>
              </div>

              {/* Subscription QR Code + Scheme Switcher */}
              <div className="flex shrink-0 flex-col items-center gap-2.5 rounded-md border border-hairline bg-recessed/60 p-3.5 lg:w-[272px]">
                <div className="flex flex-wrap items-center justify-center gap-1 rounded-md border border-hairline bg-recessed p-0.5 text-[11px]">
                  {SUB_QR_SCHEMES.map((scheme) => (
                    <button
                      key={scheme.id}
                      type="button"
                      onClick={() => setQrScheme(scheme.id)}
                      className={cn(
                        "rounded px-2 py-0.5 transition-colors",
                        qrScheme === scheme.id
                          ? "bg-surface font-medium text-accent"
                          : "text-ink-muted hover:text-ink",
                      )}
                    >
                      {scheme.label}
                    </button>
                  ))}
                </div>

                {subQrPayload && (
                  <QrCode
                    value={subQrPayload}
                    size={168}
                    label={`${activeQrSchemeMeta.label} 订阅二维码`}
                  />
                )}

                <div className="text-center text-[10px] leading-snug text-ink-muted">
                  {activeQrSchemeMeta.hint}
                </div>

                {subQrPayload && (
                  <div className="flex w-full items-center justify-center gap-1.5">
                    <a
                      href={subQrPayload}
                      target={qrScheme === "raw" ? "_blank" : undefined}
                      rel={qrScheme === "raw" ? "noreferrer" : undefined}
                      className="inline-flex h-7 flex-1 items-center justify-center gap-1 rounded border border-hairline-strong bg-surface px-2.5 text-[11px] font-medium text-ink transition-colors hover:border-accent/50 hover:text-accent"
                    >
                      <ExternalLink className="h-3 w-3" />
                      {activeQrSchemeMeta.actionLabel}
                    </a>
                    <Button
                      variant="outline"
                      size="xs"
                      onClick={() =>
                        void copyValue(
                          `qr-${qrScheme}`,
                          subQrPayload,
                          `${activeQrSchemeMeta.label} 导入链接`,
                        )
                      }
                      title="复制当前二维码内容"
                    >
                      {copiedKey === `qr-${qrScheme}` ? (
                        <Check className="h-3 w-3 text-ok" />
                      ) : (
                        <Copy className="h-3 w-3" />
                      )}
                    </Button>
                  </div>
                )}
              </div>
            </div>
          </Panel>

          {previewError && (
            <div className="rounded-md border border-danger/45 bg-danger/10 p-3.5 text-xs text-danger">
              {previewError}
            </div>
          )}

          {/* Aggregated Multi-Node Breakdown Table */}
          <Panel
            title="全集群聚合节点明细"
            description="展示当前成员可访问的全部 VPS 节点与协议条目，支持单节点扫码导入或快速复制 URI"
            bodyClassName="p-0"
          >
            <div className="overflow-x-auto">
              <table className="w-full text-left text-xs">
                <thead className="border-b border-hairline bg-recessed text-ink-secondary">
                  <tr>
                    <th className="px-4 py-2.5 font-medium">来源 VPS 节点</th>
                    <th className="px-3 py-2.5 font-medium">协议</th>
                    <th className="px-3 py-2.5 font-medium">客户端显示名称</th>
                    <th className="px-3 py-2.5 font-medium">节点 URI 预览</th>
                    <th className="px-4 py-2.5 text-right font-medium">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-hairline">
                  {loadingPreview && nodes.length === 0 ? (
                    <tr>
                      <td colSpan={5} className="px-4 py-10 text-center text-ink-muted">
                        正在渲染集群节点列表...
                      </td>
                    </tr>
                  ) : nodes.length === 0 ? (
                    <tr>
                      <td colSpan={5} className="px-4 py-10 text-center text-ink-muted">
                        该成员当前暂无可用节点（请确认各 VPS 已启用入站并绑定了该成员）。
                      </td>
                    </tr>
                  ) : (
                    nodes.map((node, idx) => (
                      <tr key={`${node.node_name}-${node.name}-${idx}`} className="hover:bg-surface-hover/60">
                        <td className="px-4 py-2.5">
                          <Badge tone="neutral" className="font-mono">
                            <Server className="h-3 w-3" />
                            {node.node_name || "local"}
                          </Badge>
                        </td>
                        <td className="px-3 py-2.5">
                          <Badge tone="accent" className="font-mono uppercase">
                            {node.protocol}
                          </Badge>
                        </td>
                        <td className="px-3 py-2.5 font-medium text-ink">{node.name}</td>
                        <td className="px-3 py-2.5">
                          <code
                            className="block max-w-[340px] truncate font-mono text-[11px] text-ink-muted"
                            title={node.uri}
                          >
                            {node.uri || "(仅 JSON/YAML 格式支持)"}
                          </code>
                        </td>
                        <td className="px-4 py-2.5 text-right">
                          {node.uri ? (
                            <div className="inline-flex items-center justify-end gap-1.5">
                              <Button
                                variant="secondary"
                                size="xs"
                                onClick={() => setQrModalNode(node)}
                              >
                                <QrCodeIcon className="h-3 w-3" />
                                二维码
                              </Button>
                              <Button
                                variant="outline"
                                size="xs"
                                onClick={() =>
                                  void copyValue(`uri-${idx}`, node.uri, `节点 ${node.name} URI`)
                                }
                              >
                                {copiedKey === `uri-${idx}` ? (
                                  <Check className="h-3 w-3 text-ok" />
                                ) : (
                                  <Copy className="h-3 w-3" />
                                )}
                                复制 URI
                              </Button>
                            </div>
                          ) : (
                            <span className="text-ink-muted">-</span>
                          )}
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </Panel>

          {/* Raw Rendered Content Preview */}
          <Panel
            title={
              <span className="flex items-center gap-2">
                <Globe className="h-4 w-4 text-accent" />
                <span>渲染内容实时预览 ({format})</span>
              </span>
            }
            actions={
              <div className="flex items-center gap-2">
                {preview?.content && (
                  <span className="font-mono text-[11px] text-ink-muted tabular-nums">
                    {preview.content.length} 字符
                  </span>
                )}
                <Button
                  variant="outline"
                  size="xs"
                  disabled={!preview?.content}
                  onClick={() =>
                    preview?.content &&
                    void copyValue("raw-box", preview.content, "渲染内容")
                  }
                >
                  {copiedKey === "raw-box" ? (
                    <Check className="h-3 w-3 text-ok" />
                  ) : (
                    <Copy className="h-3 w-3" />
                  )}
                  复制
                </Button>
              </div>
            }
          >
            <pre className="max-h-[420px] overflow-auto rounded-md border border-hairline bg-recessed p-3.5 font-mono text-xs leading-relaxed text-ink">
              {loadingPreview
                ? "正在生成订阅内容..."
                : preview?.content || "暂无可预览内容"}
            </pre>
          </Panel>
        </>
      )}

      {/* Per-Node QR Code Modal */}
      <Modal
        open={qrModalNode !== null}
        onClose={() => setQrModalNode(null)}
        title={qrModalNode ? `节点扫码导入 · ${qrModalNode.name}` : "节点扫码导入"}
        size="md"
        footer={
          <>
            <Button variant="ghost" size="sm" onClick={() => setQrModalNode(null)}>
              关闭
            </Button>
            {qrModalNode?.uri && (
              <Button
                variant="primary"
                size="sm"
                onClick={() =>
                  void copyValue("modal-node-uri", qrModalNode.uri, `节点 ${qrModalNode.name} URI`)
                }
              >
                {copiedKey === "modal-node-uri" ? (
                  <Check className="h-3.5 w-3.5" />
                ) : (
                  <Copy className="h-3.5 w-3.5" />
                )}
                复制 URI
              </Button>
            )}
          </>
        }
      >
        {qrModalNode && (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center justify-center gap-2">
              <Badge tone="neutral" className="font-mono">
                <Server className="h-3 w-3" />
                {qrModalNode.node_name || "local"}
              </Badge>
              <Badge tone="accent" className="font-mono uppercase">
                {qrModalNode.protocol}
              </Badge>
              <span className="text-xs font-medium text-ink">{qrModalNode.name}</span>
            </div>

            <div className="flex justify-center">
              <QrCode
                value={qrModalNode.uri}
                size={240}
                label={`${qrModalNode.name} 节点二维码`}
              />
            </div>

            <div className="rounded-md border border-ok/35 bg-ok/10 px-3 py-2 text-center text-xs text-ink-secondary">
              Loon / Shadowrocket / v2rayNG 直接扫码导入此节点（无需手机访问面板端口）
            </div>

            <div className="space-y-1.5">
              <div className="text-[11px] text-ink-muted">原始节点 URI</div>
              <pre className="max-h-28 overflow-auto whitespace-pre-wrap break-all rounded-md border border-hairline bg-recessed p-2.5 font-mono text-[11px] leading-relaxed text-ink">
                {qrModalNode.uri}
              </pre>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
