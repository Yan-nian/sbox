import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Check,
  Copy,
  Download,
  FileCode2,
  Globe,
  Link2,
  RefreshCw,
  Server,
  Users,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { getBasePath } from "@/lib/basePath";
import { cn } from "@/lib/cn";
import { useToast } from "@/components/ui/Toast";
import { Badge, Button, Panel, Select } from "@/components/ui/primitives";
import { QrCode } from "@/components/QrCode";
import type {
  MemberItem,
  SubFormat,
  SubscriptionPreviewResponse,
} from "@/types/api";

const FORMAT_TABS: { id: SubFormat; label: string; ext: string; mime: string }[] = [
  { id: "base64", label: "自适应 / Base64", ext: "txt", mime: "text/plain;charset=utf-8" },
  { id: "uri", label: "明文 URI 列表", ext: "txt", mime: "text/plain;charset=utf-8" },
  { id: "singbox", label: "sing-box JSON", ext: "json", mime: "application/json;charset=utf-8" },
  { id: "clash", label: "Clash Mihomo YAML", ext: "yaml", mime: "text/yaml;charset=utf-8" },
];

export function ClientSubTab() {
  const toast = useToast();
  const [members, setMembers] = useState<MemberItem[]>([]);
  const [selectedMemberId, setSelectedMemberId] = useState<number>(0);
  const [format, setFormat] = useState<SubFormat>("base64");
  const [useAutoUrl, setUseAutoUrl] = useState(true);
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

  const fullSubUrl = useMemo(() => {
    if (!selectedMember?.sub_token) return "";
    const origin = typeof window !== "undefined" ? window.location.origin : "";
    const base = getBasePath();
    if (useAutoUrl) {
      return `${origin}${base}/sub/${selectedMember.sub_token}?format=auto`;
    }
    if (preview?.sub_url) {
      if (/^https?:\/\//i.test(preview.sub_url)) return preview.sub_url;
      return `${origin}${preview.sub_url.startsWith("/") ? "" : "/"}${preview.sub_url}`;
    }
    return `${origin}${base}/sub/${selectedMember.sub_token}?format=${format}`;
  }, [selectedMember, useAutoUrl, preview?.sub_url, format]);

  const copyValue = async (key: string, val: string, label: string) => {
    if (!val) return;
    try {
      await navigator.clipboard.writeText(val);
      setCopiedKey(key);
      toast.info(`已复制${label}`);
      window.setTimeout(() => setCopiedKey(null), 1600);
    } catch {
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
            <div className="flex flex-col gap-5 md:flex-row md:items-center md:justify-between">
              <div className="min-w-0 flex-1 space-y-3.5">
                <div>
                  <div className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-ink-secondary">
                    <Link2 className="h-3.5 w-3.5 text-accent" />
                    <span>
                      {useAutoUrl
                        ? "通用订阅地址（按客户端 User-Agent 自动识别 sing-box / Clash / Shadowrocket / v2rayN）"
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
                  <Badge tone="accent" className="font-mono tabular-nums">
                    聚合 {nodes.length} 条节点
                  </Badge>
                </div>

                <p className="text-[11px] leading-relaxed text-ink-muted">
                  响应头自动附带标准{" "}
                  <code className="font-mono text-ink-secondary">Subscription-Userinfo</code>{" "}
                  以在客户端展示成员已用上下行流量。该接口通过专属{" "}
                  <code className="font-mono text-ink-secondary">sub_token</code> 鉴权，无需管理员登录态。
                </p>
              </div>

              <div className="flex shrink-0 flex-col items-center gap-1.5">
                {fullSubUrl && <QrCode value={fullSubUrl} size={164} label="客户端订阅二维码" />}
                <span className="text-[10px] text-ink-muted">客户端扫码直接导入</span>
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
            description="展示当前成员可访问的全部 VPS 节点与协议条目，支持单条 URI 快速复制"
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
    </div>
  );
}
