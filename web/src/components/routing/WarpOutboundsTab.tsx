import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Cloud,
  Code2,
  Edit3,
  Plus,
  RefreshCw,
  ShieldCheck,
  Sparkles,
  Trash2,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
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
  Textarea,
} from "@/components/ui/primitives";
import type {
  ExtraOutboundItem,
  ExtraOutboundSettings,
  ExtraOutboundType,
} from "@/types/api";

const OUTBOUND_TYPE_META: Record<
  ExtraOutboundType,
  { label: string; tone: "accent" | "ok" | "warn" | "uplink" | "neutral" }
> = {
  warp: { label: "WARP Endpoint", tone: "uplink" },
  wireguard: { label: "WireGuard Endpoint", tone: "accent" },
  socks: { label: "SOCKS5", tone: "ok" },
  http: { label: "HTTP/HTTPS", tone: "warn" },
  raw: { label: "Raw JSON", tone: "neutral" },
};

function summarizeOutbound(ob: ExtraOutboundItem): string {
  const s = ob.settings || {};
  if (ob.type === "warp" || ob.type === "wireguard") {
    const ep = s.endpoint_host
      ? `${s.endpoint_host}:${s.endpoint_port || 2408}`
      : "engage.cloudflareclient.com:2408";
    const ips = [s.ipv6_only ? "" : s.local_ipv4, s.local_ipv6].filter(Boolean).join(" / ");
    return ips ? `${ep} · ${ips}` : ep;
  }
  if (ob.type === "socks" || ob.type === "http") {
    const auth = s.username ? `${s.username}@` : "";
    const tls = ob.type === "http" && s.tls ? " (TLS)" : "";
    return `${auth}${s.server || "-"}:${s.server_port || "-"}${tls}`;
  }
  const t = typeof s.type === "string" ? s.type : "custom";
  return `sing-box type="${t}"`;
}

interface WarpOutboundsTabProps {
  onOutboundsChanged?: () => void;
}

export function WarpOutboundsTab({ onOutboundsChanged }: WarpOutboundsTabProps) {
  const { activeNodeId, activeNode } = useNode();
  const toast = useToast();

  const [outbounds, setOutbounds] = useState<ExtraOutboundItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);

  // One-click WARP registration state
  const [warpName, setWarpName] = useState("");
  const [warpTag, setWarpTag] = useState("warp");
  const [warpIpv6Only, setWarpIpv6Only] = useState(false);
  const [warpMtu, setWarpMtu] = useState("1280");
  const [warpProxy, setWarpProxy] = useState("");
  const [registeringWarp, setRegisteringWarp] = useState(false);

  // Add/Edit extra outbound modal
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<ExtraOutboundItem | null>(null);
  const [obType, setObType] = useState<ExtraOutboundType>("socks");
  const [obName, setObName] = useState("");
  const [obTag, setObTag] = useState("");
  const [obEnabled, setObEnabled] = useState(true);
  const [settings, setSettings] = useState<ExtraOutboundSettings>({});
  const [rawJson, setRawJson] = useState('{\n  "type": "socks",\n  "server": "127.0.0.1",\n  "server_port": 1080\n}');
  const [saving, setSaving] = useState(false);

  const loadOutbounds = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.listOutbounds(activeNodeId);
      setOutbounds(Array.isArray(res.outbounds) ? res.outbounds : []);
    } catch (err) {
      toast.error("加载自定义出站失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setLoading(false);
    }
  }, [activeNodeId, toast]);

  useEffect(() => {
    void loadOutbounds();
  }, [loadOutbounds]);

  const warpEndpoints = useMemo(
    () => outbounds.filter((o) => o.type === "warp"),
    [outbounds],
  );

  const handleRegisterWARP = async (e: React.FormEvent) => {
    e.preventDefault();
    setRegisteringWarp(true);
    try {
      const parsedMtu = Number.parseInt(warpMtu, 10);
      const res = await api.registerWARP({
        node_id: activeNodeId,
        name: warpName.trim() || undefined,
        tag: warpTag.trim() || undefined,
        ipv6_only: warpIpv6Only,
        mtu: Number.isFinite(parsedMtu) && parsedMtu >= 1200 ? parsedMtu : 1280,
        proxy_url: warpProxy.trim() || undefined,
      });
      toast.success(
        "Cloudflare WARP 账户注册成功",
        `已创建原生 WireGuard Endpoint：${res.outbound?.tag || warpTag}`,
      );
      setWarpName("");
      await loadOutbounds();
      onOutboundsChanged?.();
    } catch (err) {
      toast.error("WARP 注册失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setRegisteringWarp(false);
    }
  };

  const openCreateModal = (initialType: ExtraOutboundType = "socks") => {
    setEditing(null);
    setObType(initialType);
    setObName("");
    setObTag("");
    setObEnabled(true);
    if (initialType === "wireguard") {
      setSettings({
        endpoint_host: "engage.cloudflareclient.com",
        endpoint_port: 2408,
        mtu: 1280,
        ipv6_only: false,
      });
    } else if (initialType === "http") {
      setSettings({ server: "", server_port: 8080, tls: false, insecure: false });
    } else {
      setSettings({ server: "", server_port: 1080 });
    }
    setRawJson('{\n  "type": "socks",\n  "server": "127.0.0.1",\n  "server_port": 1080\n}');
    setModalOpen(true);
  };

  const openEditModal = (ob: ExtraOutboundItem) => {
    setEditing(ob);
    setObType(ob.type);
    setObName(ob.name);
    setObTag(ob.tag);
    setObEnabled(ob.enabled);
    setSettings({ ...(ob.settings || {}) });
    if (ob.type === "raw") {
      setRawJson(JSON.stringify(ob.settings || {}, null, 2));
    }
    setModalOpen(true);
  };

  const patchSettings = (patch: Partial<ExtraOutboundSettings>) => {
    setSettings((prev) => ({ ...prev, ...patch }));
  };

  const formatRawJson = () => {
    try {
      const parsed = JSON.parse(rawJson);
      setRawJson(JSON.stringify(parsed, null, 2));
    } catch (err) {
      toast.error("JSON 语法错误", err instanceof Error ? err.message : undefined);
    }
  };

  const handleSaveOutbound = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!obName.trim()) {
      toast.error("请输入出站名称");
      return;
    }

    let finalSettings: ExtraOutboundSettings = { ...settings };
    if (obType === "raw") {
      try {
        const parsed = JSON.parse(rawJson);
        if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
          toast.error("Raw JSON 必须是一个有效的 JSON 对象");
          return;
        }
        finalSettings = parsed as ExtraOutboundSettings;
      } catch (err) {
        toast.error("Raw JSON 语法错误", err instanceof Error ? err.message : undefined);
        return;
      }
    }

    setSaving(true);
    try {
      const payload = {
        node_id: editing?.node_id ?? activeNodeId,
        name: obName.trim(),
        tag: obTag.trim() || undefined,
        type: obType,
        enabled: obEnabled,
        settings: finalSettings,
      };
      if (editing) {
        await api.updateOutbound(editing.id, payload);
        toast.success("自定义出站已更新", obName.trim());
      } else {
        await api.createOutbound(payload);
        toast.success("自定义出站已创建", obName.trim());
      }
      setModalOpen(false);
      await loadOutbounds();
      onOutboundsChanged?.();
    } catch (err) {
      toast.error(
        editing ? "更新出站失败" : "创建出站失败",
        err instanceof ApiError ? err.message : undefined,
      );
    } finally {
      setSaving(false);
    }
  };

  const handleToggle = async (ob: ExtraOutboundItem) => {
    setBusyId(ob.id);
    try {
      const updated = await api.toggleOutbound(ob.id);
      setOutbounds((prev) => prev.map((item) => (item.id === ob.id ? updated : item)));
      toast.success(updated.enabled ? `已启用出站 ${ob.tag}` : `已停用出站 ${ob.tag}`);
      onOutboundsChanged?.();
    } catch (err) {
      toast.error("切换状态失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyId(null);
    }
  };

  const handleDelete = async (ob: ExtraOutboundItem) => {
    setBusyId(ob.id);
    try {
      await api.deleteOutbound(ob.id);
      setConfirmDeleteId(null);
      toast.success("已删除出站", ob.name);
      await loadOutbounds();
      onOutboundsChanged?.();
    } catch (err) {
      toast.error("删除出站失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div className="space-y-5">
      {/* Featured One-Click Cloudflare WARP Registration Hero */}
      <Panel
        title={
          <span className="flex items-center gap-2">
            <Cloud className="h-4 w-4 text-uplink" />
            <span>一键免费注册 Cloudflare WARP 原生端点</span>
            <Badge tone="uplink">sing-box 1.12+ endpoints</Badge>
          </span>
        }
        description="自动向 Cloudflare 注册免费 WARP WireGuard 密钥与 Reserved 标识，直接编译为现代 WireGuard endpoints，一键解锁 AI 服务与流媒体"
      >
        <div className="grid gap-5 lg:grid-cols-12">
          {/* Left architectural description & active WARP endpoints preview */}
          <div className="space-y-3 lg:col-span-5">
            <div className="rounded-md border border-hairline bg-recessed p-3.5 text-xs leading-relaxed text-ink-secondary">
              <div className="flex items-center gap-1.5 font-medium text-ink">
                <ShieldCheck className="h-4 w-4 text-ok" />
                零旧版废弃字段，直接用于路由分流
              </div>
              <p className="mt-1.5">
                注册完成后可在「可视化分流规则」中将 AI 服务（OpenAI / Claude / Gemini）或流媒体预设一键指向{" "}
                <code className="rounded bg-surface px-1 py-0.5 font-mono text-ink">warp</code>{" "}
                出口，彻底解决机房 IP 验证码与送中问题。
              </p>
            </div>

            {warpEndpoints.length > 0 && (
              <div className="space-y-2">
                <div className="text-[11px] font-medium text-ink-muted">
                  当前节点已有 WARP 端点 ({warpEndpoints.length})
                </div>
                {warpEndpoints.map((w) => (
                  <div
                    key={w.id}
                    className="rounded-md border border-uplink/35 bg-uplink/10 px-3 py-2.5 text-xs"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-mono font-semibold text-ink">
                        {w.tag} <span className="font-normal text-ink-secondary">({w.name})</span>
                      </span>
                      <div className="flex items-center gap-1.5">
                        {w.settings?.ipv6_only && <Badge tone="accent">IPv6 Only</Badge>}
                        <Badge tone={w.enabled ? "ok" : "neutral"}>
                          {w.enabled ? "已启用" : "已停用"}
                        </Badge>
                      </div>
                    </div>
                    <div className="mt-1.5 space-y-0.5 font-mono text-[11px] text-ink-secondary">
                      <div>
                        Endpoint:{" "}
                        {w.settings?.endpoint_host || "engage.cloudflareclient.com"}:
                        {w.settings?.endpoint_port || 2408}
                      </div>
                      <div className="flex flex-wrap gap-x-3">
                        {!w.settings?.ipv6_only && w.settings?.local_ipv4 && (
                          <span>IPv4: {w.settings.local_ipv4}</span>
                        )}
                        {w.settings?.local_ipv6 && (
                          <span className="truncate max-w-[240px]" title={w.settings.local_ipv6}>
                            IPv6: {w.settings.local_ipv6}
                          </span>
                        )}
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Right one-click registration form */}
          <form
            onSubmit={handleRegisterWARP}
            className="space-y-3.5 rounded-md border border-hairline bg-recessed/60 p-4 lg:col-span-7"
          >
            <div className="grid gap-3 sm:grid-cols-3">
              <Field label="显示名称（可选）" htmlFor="warp-name">
                <Input
                  id="warp-name"
                  value={warpName}
                  onChange={(e) => setWarpName(e.target.value)}
                  placeholder="Cloudflare WARP"
                />
              </Field>
              <Field label="出站标签 (tag)" htmlFor="warp-tag">
                <Input
                  id="warp-tag"
                  value={warpTag}
                  onChange={(e) => setWarpTag(e.target.value)}
                  placeholder="warp"
                  className="font-mono"
                />
              </Field>
              <Field label="MTU" htmlFor="warp-mtu">
                <Input
                  id="warp-mtu"
                  type="number"
                  min={1200}
                  max={1500}
                  value={warpMtu}
                  onChange={(e) => setWarpMtu(e.target.value)}
                  className="font-mono"
                />
              </Field>
            </div>

            <Field
              label="注册请求代理 URL（可选）"
              htmlFor="warp-proxy"
              hint="仅用于主控访问 Cloudflare 注册接口；留空直连注册"
            >
              <Input
                id="warp-proxy"
                value={warpProxy}
                onChange={(e) => setWarpProxy(e.target.value)}
                placeholder="http://127.0.0.1:7890"
                className="font-mono text-xs"
              />
            </Field>

            <div className="flex flex-wrap items-center justify-between gap-3 pt-1">
              <label className="flex items-center gap-2.5 text-xs text-ink">
                <Switch
                  checked={warpIpv6Only}
                  onChange={setWarpIpv6Only}
                  label="仅使用 IPv6 地址出口"
                />
                <span>仅 IPv6 出口 (ipv6_only)</span>
              </label>

              <Button type="submit" variant="primary" size="md" loading={registeringWarp}>
                <Sparkles className="h-3.5 w-3.5" />
                一键免费注册 WARP
              </Button>
            </div>
          </form>
        </div>
      </Panel>

      {/* Extra Outbounds Table */}
      <Panel
        title={`自定义出站与落地端点 · ${activeNode?.name || `#${activeNodeId}`}`}
        description="支持 WARP、手动 WireGuard、SOCKS5、HTTP/HTTPS 代理及原始 sing-box JSON 出站"
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              loading={loading}
              onClick={() => void loadOutbounds()}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              刷新
            </Button>
            <Button variant="primary" size="sm" onClick={() => openCreateModal("socks")}>
              <Plus className="h-3.5 w-3.5" />
              添加自定义出站
            </Button>
          </div>
        }
        bodyClassName="p-0"
      >
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="border-b border-hairline bg-recessed text-ink-secondary">
              <tr>
                <th className="px-4 py-2.5 font-medium">启停与名称</th>
                <th className="px-3 py-2.5 font-medium">类型与标签</th>
                <th className="px-3 py-2.5 font-medium">目标端点参数</th>
                <th className="px-4 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {loading && outbounds.length === 0 ? (
                <tr>
                  <td colSpan={4} className="px-4 py-10 text-center text-ink-muted">
                    正在加载出站列表...
                  </td>
                </tr>
              ) : outbounds.length === 0 ? (
                <tr>
                  <td colSpan={4} className="px-4 py-10 text-center text-ink-muted">
                    当前节点暂无额外出站，可点击上方「一键免费注册 WARP」或右上角手动添加 SOCKS5 / WireGuard 落地。
                  </td>
                </tr>
              ) : (
                outbounds.map((ob) => {
                  const meta = OUTBOUND_TYPE_META[ob.type] ?? {
                    label: ob.type,
                    tone: "neutral" as const,
                  };
                  const isBusy = busyId === ob.id;

                  return (
                    <tr
                      key={ob.id}
                      className={cn(
                        "hover:bg-surface-hover/60",
                        !ob.enabled && "opacity-65",
                      )}
                    >
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-3">
                          <Switch
                            checked={ob.enabled}
                            disabled={isBusy}
                            onChange={() => void handleToggle(ob)}
                            label={`切换出站 ${ob.name}`}
                          />
                          <div>
                            <div className="flex items-center gap-2">
                              <StatusDot state={ob.enabled ? "ok" : "idle"} />
                              <span className="font-medium text-ink">{ob.name}</span>
                            </div>
                          </div>
                        </div>
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <Badge tone={meta.tone}>{meta.label}</Badge>
                          <code className="rounded border border-hairline bg-recessed px-1.5 py-0.5 font-mono text-[11px] text-ink">
                            {ob.tag}
                          </code>
                        </div>
                      </td>

                      <td className="px-3 py-3 font-mono text-[11px] text-ink-secondary">
                        <div className="max-w-[380px] truncate" title={summarizeOutbound(ob)}>
                          {summarizeOutbound(ob)}
                        </div>
                      </td>

                      <td className="px-4 py-3 text-right">
                        <div className="inline-flex items-center gap-1.5">
                          <Button
                            variant="outline"
                            size="xs"
                            disabled={isBusy}
                            onClick={() => openEditModal(ob)}
                          >
                            <Edit3 className="h-3 w-3" />
                            编辑
                          </Button>

                          {confirmDeleteId === ob.id ? (
                            <div className="inline-flex items-center gap-1 rounded border border-danger/45 bg-danger/10 px-2 py-0.5">
                              <span className="text-[11px] text-danger">删除？</span>
                              <Button
                                variant="danger"
                                size="xs"
                                loading={isBusy}
                                onClick={() => void handleDelete(ob)}
                              >
                                确认
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
                              onClick={() => setConfirmDeleteId(ob.id)}
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

      {/* Add/Edit Extra Outbound Modal */}
      <Modal
        open={modalOpen}
        onClose={() => !saving && setModalOpen(false)}
        title={editing ? `编辑出站：${editing.name}` : "添加自定义出站 / 端点"}
        description="支持 SOCKS5、HTTP/HTTPS、手动 WireGuard Endpoint 及原始 sing-box JSON 对象"
        size="lg"
        footer={
          <>
            <Button
              variant="ghost"
              size="sm"
              disabled={saving}
              onClick={() => setModalOpen(false)}
            >
              取消
            </Button>
            <Button
              type="submit"
              form="extra-outbound-form"
              variant="primary"
              size="sm"
              loading={saving}
            >
              {editing ? "保存修改" : "创建并应用"}
            </Button>
          </>
        }
      >
        <form id="extra-outbound-form" onSubmit={handleSaveOutbound} className="space-y-4">
          <div className="grid gap-3.5 sm:grid-cols-3">
            <Field label="出站类型" htmlFor="ob-type">
              <Select
                id="ob-type"
                disabled={editing?.type === "warp"}
                value={obType}
                onChange={(e) => setObType(e.target.value as ExtraOutboundType)}
              >
                {editing?.type === "warp" && <option value="warp">Cloudflare WARP</option>}
                <option value="socks">SOCKS5 落地代理</option>
                <option value="http">HTTP / HTTPS 代理</option>
                <option value="wireguard">WireGuard Endpoint</option>
                <option value="raw">Raw sing-box JSON</option>
              </Select>
            </Field>
            <Field label="名称" htmlFor="ob-name">
              <Input
                id="ob-name"
                value={obName}
                onChange={(e) => setObName(e.target.value)}
                placeholder="例如：住宅 SOCKS5"
                required
              />
            </Field>
            <Field label="配置标签 (tag)" htmlFor="ob-tag" hint="留空自动生成">
              <Input
                id="ob-tag"
                value={obTag}
                onChange={(e) => setObTag(e.target.value)}
                placeholder="res-socks"
                className="font-mono"
              />
            </Field>
          </div>

          {(obType === "socks" || obType === "http") && (
            <div className="space-y-3.5 rounded-md border border-hairline bg-recessed p-4">
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="sm:col-span-2">
                  <Field label="服务器地址 (server)" htmlFor="ob-server">
                    <Input
                      id="ob-server"
                      value={settings.server || ""}
                      onChange={(e) => patchSettings({ server: e.target.value })}
                      placeholder="1.2.3.4 或 proxy.example.com"
                      className="font-mono"
                      required
                    />
                  </Field>
                </div>
                <Field label="端口 (server_port)" htmlFor="ob-port">
                  <Input
                    id="ob-port"
                    type="number"
                    min={1}
                    max={65535}
                    value={String(settings.server_port ?? "")}
                    onChange={(e) =>
                      patchSettings({
                        server_port: Number.parseInt(e.target.value, 10) || 1080,
                      })
                    }
                    className="font-mono"
                    required
                  />
                </Field>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="用户名（可选）" htmlFor="ob-user">
                  <Input
                    id="ob-user"
                    value={settings.username || ""}
                    onChange={(e) => patchSettings({ username: e.target.value })}
                    className="font-mono"
                  />
                </Field>
                <Field label="密码（可选）" htmlFor="ob-pass">
                  <Input
                    id="ob-pass"
                    type="password"
                    value={settings.password || ""}
                    onChange={(e) => patchSettings({ password: e.target.value })}
                    className="font-mono"
                  />
                </Field>
              </div>
              {obType === "http" && (
                <div className="space-y-3 border-t border-hairline pt-3">
                  <div className="flex flex-wrap items-center gap-5 text-xs">
                    <label className="flex items-center gap-2">
                      <Switch
                        checked={Boolean(settings.tls)}
                        onChange={(v) => patchSettings({ tls: v })}
                        label="启用 HTTPS TLS"
                      />
                      <span>启用 TLS (HTTPS 代理)</span>
                    </label>
                    {settings.tls && (
                      <label className="flex items-center gap-2">
                        <Switch
                          checked={Boolean(settings.insecure)}
                          onChange={(v) => patchSettings({ insecure: v })}
                          label="跳过证书验证"
                        />
                        <span className="text-ink-secondary">跳过证书校验 (insecure)</span>
                      </label>
                    )}
                  </div>
                  {settings.tls && (
                    <Field label="SNI (server_name，可选)" htmlFor="ob-sni">
                      <Input
                        id="ob-sni"
                        value={settings.server_name || ""}
                        onChange={(e) => patchSettings({ server_name: e.target.value })}
                        className="font-mono"
                      />
                    </Field>
                  )}
                </div>
              )}
            </div>
          )}

          {(obType === "wireguard" || obType === "warp") && (
            <div className="space-y-3.5 rounded-md border border-hairline bg-recessed p-4">
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="sm:col-span-2">
                  <Field label="对端服务器 (endpoint_host)" htmlFor="wg-host">
                    <Input
                      id="wg-host"
                      value={settings.endpoint_host || ""}
                      onChange={(e) => patchSettings({ endpoint_host: e.target.value })}
                      placeholder="engage.cloudflareclient.com"
                      className="font-mono"
                      required
                    />
                  </Field>
                </div>
                <Field label="对端端口" htmlFor="wg-port">
                  <Input
                    id="wg-port"
                    type="number"
                    min={1}
                    max={65535}
                    value={String(settings.endpoint_port ?? 2408)}
                    onChange={(e) =>
                      patchSettings({
                        endpoint_port: Number.parseInt(e.target.value, 10) || 2408,
                      })
                    }
                    className="font-mono"
                    required
                  />
                </Field>
              </div>

              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="本端私钥 (private_key)" htmlFor="wg-priv">
                  <Input
                    id="wg-priv"
                    value={settings.private_key || ""}
                    onChange={(e) => patchSettings({ private_key: e.target.value })}
                    className="font-mono text-xs"
                    required
                  />
                </Field>
                <Field label="对端公钥 (peer_public_key)" htmlFor="wg-pub">
                  <Input
                    id="wg-pub"
                    value={settings.peer_public_key || settings.public_key || ""}
                    onChange={(e) => patchSettings({ peer_public_key: e.target.value })}
                    className="font-mono text-xs"
                    required
                  />
                </Field>
              </div>

              <div className="grid gap-3 sm:grid-cols-3">
                <Field label="本地 IPv4 CIDR" htmlFor="wg-v4">
                  <Input
                    id="wg-v4"
                    value={settings.local_ipv4 || ""}
                    onChange={(e) => patchSettings({ local_ipv4: e.target.value })}
                    placeholder="172.16.0.2/32"
                    className="font-mono text-xs"
                  />
                </Field>
                <Field label="本地 IPv6 CIDR" htmlFor="wg-v6">
                  <Input
                    id="wg-v6"
                    value={settings.local_ipv6 || ""}
                    onChange={(e) => patchSettings({ local_ipv6: e.target.value })}
                    placeholder="2606:4700:.../128"
                    className="font-mono text-xs"
                  />
                </Field>
                <Field label="MTU" htmlFor="wg-mtu">
                  <Input
                    id="wg-mtu"
                    type="number"
                    value={String(settings.mtu ?? 1280)}
                    onChange={(e) =>
                      patchSettings({ mtu: Number.parseInt(e.target.value, 10) || 1280 })
                    }
                    className="font-mono"
                  />
                </Field>
              </div>

              <div className="grid gap-3 sm:grid-cols-2">
                <Field
                  label="Reserved 字节（可选，3 个整数以逗号分隔）"
                  htmlFor="wg-res"
                >
                  <Input
                    id="wg-res"
                    value={(settings.reserved || []).join(", ")}
                    onChange={(e) => {
                      const nums = e.target.value
                        .split(",")
                        .map((s) => Number.parseInt(s.trim(), 10))
                        .filter((n) => Number.isFinite(n) && n >= 0 && n <= 255);
                      patchSettings({ reserved: nums });
                    }}
                    placeholder="例如：12, 34, 56"
                    className="font-mono text-xs"
                  />
                </Field>
                <div className="flex items-end pb-2">
                  <label className="flex items-center gap-2 text-xs text-ink">
                    <Switch
                      checked={Boolean(settings.ipv6_only)}
                      onChange={(v) => patchSettings({ ipv6_only: v })}
                      label="仅 IPv6 出口"
                    />
                    <span>仅 IPv6 地址出口 (ipv6_only)</span>
                  </label>
                </div>
              </div>
            </div>
          )}

          {obType === "raw" && (
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <span className="text-xs font-medium text-ink-secondary">
                  原始 sing-box 出站 JSON 对象（tag 将自动覆盖为上方配置标签）
                </span>
                <Button type="button" variant="ghost" size="xs" onClick={formatRawJson}>
                  <Code2 className="h-3.5 w-3.5" />
                  格式化
                </Button>
              </div>
              <Textarea
                rows={10}
                spellCheck={false}
                value={rawJson}
                onChange={(e) => setRawJson(e.target.value)}
                className="font-mono text-xs"
              />
            </div>
          )}

          <div className="flex items-center justify-between rounded-md border border-hairline bg-recessed px-3.5 py-2.5 text-xs">
            <span className="text-ink">启用该出站并写入编译配置</span>
            <Switch checked={obEnabled} onChange={setObEnabled} label="启用出站" />
          </div>
        </form>
      </Modal>
    </div>
  );
}
