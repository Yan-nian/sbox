import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  KeyRound,
  Plus,
  Radar,
  Sparkles,
  Trash2,
  XCircle,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import { useToast } from "@/components/ui/Toast";
import {
  Badge,
  Button,
  Field,
  Input,
  Modal,
  Select,
  Switch,
  Textarea,
} from "@/components/ui/primitives";
import type {
  CertificateItem,
  ExtraEntry,
  InboundItem,
  InboundProtocol,
  InboundSettings,
  MemberBindMode,
  MemberItem,
  RealityProbeResult,
  SaveInboundRequest,
} from "@/types/api";

export const PROTOCOL_OPTIONS: {
  id: InboundProtocol;
  label: string;
  desc: string;
  badgeTone: "accent" | "ok" | "warn" | "uplink" | "neutral";
}[] = [
  { id: "vless", label: "VLESS", desc: "Vision / Reality / TLS", badgeTone: "accent" },
  { id: "hysteria2", label: "Hysteria2", desc: "QUIC 拥塞加速 + 跳端口", badgeTone: "uplink" },
  { id: "tuic", label: "TUIC v5", desc: "QUIC 0-RTT UDP 中继", badgeTone: "ok" },
  { id: "anytls", label: "AnyTLS", desc: "1.12+ 原生填充抗指纹", badgeTone: "accent" },
  { id: "shadowsocks", label: "SS-2022", desc: "EIH 单端口多成员", badgeTone: "warn" },
  { id: "trojan", label: "Trojan", desc: "标准 TLS / WS / gRPC", badgeTone: "ok" },
  { id: "shadowtls", label: "ShadowTLS v3", desc: "TLS 伪装 + 联动 Detour", badgeTone: "uplink" },
  { id: "snell", label: "Snell v5", desc: "原生 sing-box 1.14+ · 适配 Loon / Surge", badgeTone: "ok" },
  { id: "direct", label: "Direct", desc: "TCP/UDP 端口中转", badgeTone: "neutral" },
  { id: "mixed", label: "Mixed", desc: "HTTP + SOCKS5 混合", badgeTone: "neutral" },
  { id: "socks", label: "SOCKS5", desc: "本地/代理 SOCKS5", badgeTone: "neutral" },
  { id: "http", label: "HTTP", desc: "HTTP/HTTPS 代理", badgeTone: "neutral" },
];

function requiresCert(protocol: InboundProtocol, settings: InboundSettings): boolean {
  if (protocol === "hysteria2" || protocol === "tuic" || protocol === "anytls" || protocol === "trojan") {
    return true;
  }
  if (protocol === "vless" && settings.tls_mode === "tls") {
    return true;
  }
  return false;
}

function generateSnellPsk(length = 31): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  const out: string[] = [];
  if (typeof crypto !== "undefined" && typeof crypto.getRandomValues === "function") {
    const buf = new Uint8Array(length);
    crypto.getRandomValues(buf);
    for (let i = 0; i < length; i += 1) {
      out.push(alphabet[buf[i] % alphabet.length]);
    }
    return out.join("");
  }
  for (let i = 0; i < length; i += 1) {
    out.push(alphabet[Math.floor(Math.random() * alphabet.length)]);
  }
  return out.join("");
}

function defaultInboundName(proto: InboundProtocol): string {
  if (proto === "vless") return "VLESS Reality";
  const meta = PROTOCOL_OPTIONS.find((p) => p.id === proto);
  return meta ? `${meta.label} 节点` : proto;
}

interface InboundModalProps {
  open: boolean;
  nodeId: number;
  nodeName?: string;
  nodeOffline?: boolean;
  editing: InboundItem | null;
  certificates: CertificateItem[];
  members: MemberItem[];
  onClose: () => void;
  onSaved: (saved?: InboundItem) => Promise<void> | void;
}

export function InboundModal({
  open,
  nodeId,
  nodeName,
  nodeOffline = false,
  editing,
  certificates,
  members,
  onClose,
  onSaved,
}: InboundModalProps) {
  const toast = useToast();

  const [protocol, setProtocol] = useState<InboundProtocol>("vless");
  const [name, setName] = useState("");
  const [tag, setTag] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [listen, setListen] = useState("::");
  const [listenPort, setListenPort] = useState("24443");
  const [settings, setSettings] = useState<InboundSettings>({});
  const [certificateId, setCertificateId] = useState<string>("");
  const [memberMode, setMemberMode] = useState<MemberBindMode>("all");
  const [memberIds, setMemberIds] = useState<number[]>([]);
  const [externalHost, setExternalHost] = useState("");
  const [externalPort, setExternalPort] = useState("");
  const [extraEntries, setExtraEntries] = useState<ExtraEntry[]>([]);

  const [loadingDefaults, setLoadingDefaults] = useState(false);
  const [saving, setSaving] = useState(false);
  const [keygenBusy, setKeygenBusy] = useState(false);
  const [probing, setProbing] = useState(false);
  const [probeResult, setProbeResult] = useState<RealityProbeResult | null>(null);
  const nameManuallyEditedRef = useRef(false);
  const tagManuallyEditedRef = useRef(false);
  const defaultsReqSeqRef = useRef(0);

  const loadProtocolDefaults = useCallback(
    async (proto: InboundProtocol, prevProto?: InboundProtocol) => {
      const reqSeq = ++defaultsReqSeqRef.current;
      setLoadingDefaults(true);
      setProbeResult(null);
      // Immediately clear previous protocol settings so stale keys never survive switches
      setSettings({});
      try {
        const def = await api.getInboundDefaults(proto);
        if (reqSeq !== defaultsReqSeqRef.current) return;
        const freshTag = def.tag || `${proto}-in`;
        setTag((curr) => {
          if (
            !tagManuallyEditedRef.current ||
            !curr.trim() ||
            (prevProto && curr.trim().toLowerCase().startsWith(`${prevProto}-`))
          ) {
            return freshTag;
          }
          return curr;
        });
        setListen(def.listen || "::");
        setListenPort(def.listen_port ? String(def.listen_port) : "24443");
        setSettings({ ...(def.settings || {}) });
        setName((curr) => {
          if (
            !nameManuallyEditedRef.current ||
            !curr.trim() ||
            (prevProto && curr.trim().toLowerCase().startsWith(`${prevProto}-`))
          ) {
            return defaultInboundName(proto);
          }
          return curr;
        });
      } catch (err) {
        if (reqSeq !== defaultsReqSeqRef.current) return;
        toast.error(
          "拉取协议默认参数失败",
          err instanceof ApiError ? err.message : undefined,
        );
      } finally {
        if (reqSeq === defaultsReqSeqRef.current) {
          setLoadingDefaults(false);
        }
      }
    },
    [toast],
  );

  useEffect(() => {
    if (!open) return;
    setProbeResult(null);
    if (editing) {
      nameManuallyEditedRef.current = true;
      tagManuallyEditedRef.current = true;
      setProtocol(editing.protocol);
      setName(editing.name);
      setTag(editing.tag);
      setEnabled(editing.enabled);
      setListen(editing.listen || "::");
      setListenPort(String(editing.listen_port));
      setSettings({ ...(editing.settings || {}) });
      setCertificateId(editing.certificate_id ? String(editing.certificate_id) : "");
      setMemberMode(editing.member_mode || "all");
      setMemberIds(Array.isArray(editing.member_ids) ? editing.member_ids : []);
      setExternalHost(editing.external_host || "");
      setExternalPort(editing.external_port ? String(editing.external_port) : "");
      setExtraEntries(Array.isArray(editing.extra_entries) ? editing.extra_entries : []);
    } else {
      nameManuallyEditedRef.current = false;
      tagManuallyEditedRef.current = false;
      setProtocol("vless");
      setName(defaultInboundName("vless"));
      setTag("vless-in");
      setEnabled(true);
      setCertificateId(certificates[0] ? String(certificates[0].id) : "");
      setMemberMode("all");
      setMemberIds([]);
      setExternalHost("");
      setExternalPort("");
      setExtraEntries([]);
      void loadProtocolDefaults("vless");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, editing]);

  const handleSelectProtocol = (next: InboundProtocol) => {
    if (editing || next === protocol) return;
    const prevProto = protocol;
    setProtocol(next);
    if (
      !nameManuallyEditedRef.current ||
      !name.trim() ||
      name.trim().toLowerCase().startsWith(`${prevProto}-`)
    ) {
      setName(defaultInboundName(next));
      nameManuallyEditedRef.current = false;
    }
    if (
      !tagManuallyEditedRef.current ||
      !tag.trim() ||
      tag.trim().toLowerCase().startsWith(`${prevProto}-`)
    ) {
      setTag(`${next}-in`);
      tagManuallyEditedRef.current = false;
    }
    void loadProtocolDefaults(next, prevProto);
  };

  const patchSettings = (patch: Partial<InboundSettings>) => {
    setSettings((prev) => ({ ...prev, ...patch }));
  };

  const handleGenReality = async () => {
    setKeygenBusy(true);
    try {
      const kp = await api.generateRealityKeypair();
      patchSettings({
        reality_private_key: kp.private_key,
        reality_public_key: kp.public_key,
        reality_short_ids: [kp.short_id],
      });
      toast.success("已刷新 Reality X25519 密钥与 ShortID");
    } catch (err) {
      toast.error("生成 Reality 密钥失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setKeygenBusy(false);
    }
  };

  const handleProbeReality = async () => {
    const host = String(settings.reality_handshake_server || "").trim();
    const port = Number(settings.reality_handshake_port || 443);
    if (!host) {
      toast.error("请先输入伪装目标域名");
      return;
    }
    setProbing(true);
    try {
      const res = await api.probeRealityTarget(`${host}:${port}`, nodeId);
      setProbeResult(res);
    } catch (err) {
      toast.error("探测目标失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setProbing(false);
    }
  };

  const handleGenSS2022 = async (field: "server_password" | "ss_server_password", method: string) => {
    setKeygenBusy(true);
    try {
      const res = await api.generateSS2022Key(method || "2022-blake3-aes-128-gcm");
      patchSettings({ [field]: res.key });
      toast.success("已生成 SS-2022 服务端密钥");
    } catch (err) {
      toast.error("生成密钥失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setKeygenBusy(false);
    }
  };

  const toggleMemberSelection = (id: number) => {
    setMemberIds((prev) =>
      prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id],
    );
  };

  const addExtraEntry = () => {
    setExtraEntries((prev) => [...prev, { name: "中转入口", host: "", port: Number(listenPort) || 443 }]);
  };

  const updateExtraEntry = (idx: number, patch: Partial<ExtraEntry>) => {
    setExtraEntries((prev) =>
      prev.map((row, i) => (i === idx ? { ...row, ...patch } : row)),
    );
  };

  const removeExtraEntry = (idx: number) => {
    setExtraEntries((prev) => prev.filter((_, i) => i !== idx));
  };

  const needsCert = requiresCert(protocol, settings);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const portNum = Number.parseInt(listenPort, 10);
    if (Number.isNaN(portNum) || portNum < 1 || portNum > 65535) {
      toast.error("监听端口必须在 1–65535 之间");
      return;
    }
    if (needsCert && !certificateId) {
      toast.error("当前协议需要绑定 TLS 证书", "请先选择证书或前往「证书中心」签发/自签证书");
      return;
    }

    const cleanedExtra = extraEntries
      .filter((entry) => entry.host.trim() !== "")
      .map((entry) => ({
        name: entry.name.trim() || entry.host.trim(),
        host: entry.host.trim(),
        port: Number(entry.port) > 0 ? Number(entry.port) : portNum,
      }));

    const extPortNum = externalPort.trim() ? Number.parseInt(externalPort, 10) : 0;

    // Normalize protocol settings before submission
    let finalSettings: InboundSettings = { ...settings };
    if (protocol === "vless") {
      const tr = finalSettings.transport || "tcp";
      if (tr !== "tcp") {
        finalSettings.flow = "";
      }
      if (finalSettings.tls_mode === "reality" && !finalSettings.server_name && finalSettings.reality_handshake_server) {
        finalSettings.server_name = finalSettings.reality_handshake_server;
      }
    } else if (protocol === "snell") {
      const psk = String(finalSettings.psk || "").trim();
      if (!psk) {
        toast.error("请输入或随机生成 Snell 预共享密钥 (PSK)");
        return;
      }
      const obfsMode = finalSettings.obfs_mode === "http" ? "http" : "none";
      finalSettings = {
        version: Number(finalSettings.version) === 4 ? 4 : 5,
        psk,
        obfs_mode: obfsMode,
        ...(obfsMode === "http"
          ? { obfs_host: String(finalSettings.obfs_host || "").trim() || "www.bing.com" }
          : {}),
        udp: finalSettings.udp !== false,
      };
    }

    const payload: SaveInboundRequest = {
      node_id: editing?.node_id ?? nodeId,
      name: name.trim() || tag.trim() || `${protocol}-${portNum}`,
      tag: tag.trim() || undefined,
      protocol,
      enabled,
      listen: listen.trim() || "::",
      listen_port: portNum,
      settings: finalSettings,
      certificate_id: needsCert && certificateId ? Number(certificateId) : 0,
      member_mode: protocol === "direct" ? "all" : memberMode,
      member_ids: protocol === "direct" ? [] : memberIds,
      external_host: externalHost.trim(),
      external_port: Number.isFinite(extPortNum) && extPortNum > 0 ? extPortNum : 0,
      extra_entries: cleanedExtra,
    };

    setSaving(true);
    try {
      let saved: InboundItem;
      if (editing) {
        saved = await api.updateInbound(editing.id, payload);
        toast.success("入站节点已更新", "已重新编译 sing-box 配置");
      } else {
        saved = await api.createInbound(payload);
        toast.success("入站节点已创建", "已自动编译并应用至 sing-box");
      }
      onClose();
      await onSaved(saved);
    } catch (err) {
      toast.error(
        editing ? "更新入站失败" : "创建入站失败",
        err instanceof ApiError ? err.message : undefined,
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={() => !saving && onClose()}
      title={editing ? `编辑入站：${editing.name}` : `新建多协议入站节点${nodeName ? ` · ${nodeName}` : ""}`}
      description={
        nodeOffline
          ? "当前目标节点离线：保存至主控数据库，Agent 重连后将自动编译下发"
          : "自动编译为现代 sing-box 1.12+ 配置并触发热重载"
      }
      size="xl"
      footer={
        <>
          <Button variant="ghost" size="sm" disabled={saving} onClick={onClose}>
            取消
          </Button>
          <Button
            type="submit"
            form="inbound-editor-form"
            variant="primary"
            size="sm"
            loading={saving}
            disabled={loadingDefaults}
          >
            {editing ? "保存并热重载" : "创建并应用"}
          </Button>
        </>
      }
    >
      <form id="inbound-editor-form" onSubmit={handleSubmit} className="space-y-5">
        {/* Protocol Selector */}
        {!editing && (
          <div>
            <div className="mb-2 flex items-center justify-between text-xs">
              <span className="font-medium text-ink-secondary">选择入站协议</span>
              {loadingDefaults && (
                <span className="font-mono text-[11px] text-accent">
                  正在生成默认密钥与端口...
                </span>
              )}
            </div>
            <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-3 md:grid-cols-4">
              {PROTOCOL_OPTIONS.map((opt) => {
                const active = protocol === opt.id;
                return (
                  <button
                    key={opt.id}
                    type="button"
                    onClick={() => handleSelectProtocol(opt.id)}
                    className={cn(
                      "rounded-md border px-2.5 py-2 text-left transition-colors",
                      active
                        ? "border-accent bg-accent-muted"
                        : "border-hairline bg-recessed hover:border-hairline-strong",
                    )}
                  >
                    <div
                      className={cn(
                        "font-mono text-xs font-semibold",
                        active ? "text-accent" : "text-ink",
                      )}
                    >
                      {opt.label}
                    </div>
                    <div className="mt-0.5 truncate text-[10px] text-ink-muted">
                      {opt.desc}
                    </div>
                  </button>
                );
              })}
            </div>
          </div>
        )}

        {/* Base Metadata Row */}
        <div className="grid gap-3.5 sm:grid-cols-2 lg:grid-cols-4">
          <Field label="节点名称" htmlFor="in-name">
            <Input
              id="in-name"
              value={name}
              onChange={(e) => {
                nameManuallyEditedRef.current = true;
                setName(e.target.value);
              }}
              placeholder="JP VLESS Reality"
              required
            />
          </Field>
          <Field label="配置标签 (tag)" htmlFor="in-tag" hint="唯一标识，留空自动生成">
            <Input
              id="in-tag"
              value={tag}
              onChange={(e) => {
                tagManuallyEditedRef.current = true;
                setTag(e.target.value);
              }}
              placeholder="vless-in"
              className="font-mono"
            />
          </Field>
          <Field label="监听地址" htmlFor="in-listen">
            <Input
              id="in-listen"
              value={listen}
              onChange={(e) => setListen(e.target.value)}
              placeholder="::"
              className="font-mono"
            />
          </Field>
          <Field label="监听端口" htmlFor="in-port">
            <Input
              id="in-port"
              type="number"
              min={1}
              max={65535}
              value={listenPort}
              onChange={(e) => setListenPort(e.target.value)}
              className="font-mono"
              required
            />
          </Field>
        </div>

        {/* Protocol Specific Settings */}
        <div className="space-y-4 rounded-md border border-hairline bg-recessed/70 p-4">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-hairline pb-2.5">
            <span className="font-mono text-xs font-semibold text-ink">
              协议专属参数 · {protocol.toUpperCase()}
            </span>
            <label className="flex items-center gap-2 text-xs text-ink-secondary">
              <span>启用节点</span>
              <Switch checked={enabled} onChange={setEnabled} label="启用此入站节点" />
            </label>
          </div>

          {/* VLESS */}
          {protocol === "vless" && (
            <div className="space-y-3.5">
              <div className="grid gap-3.5 sm:grid-cols-3">
                <Field label="加密传输安全层 (TLS Mode)" htmlFor="vless-tls">
                  <Select
                    id="vless-tls"
                    value={settings.tls_mode || "reality"}
                    onChange={(e) =>
                      patchSettings({
                        tls_mode: e.target.value as "reality" | "tls" | "none",
                      })
                    }
                  >
                    <option value="reality">Reality（推荐，免域名抗检测）</option>
                    <option value="tls">标准 TLS（需绑定证书）</option>
                    <option value="none">无 TLS（仅限中转/明文）</option>
                  </Select>
                </Field>

                <Field label="底层传输协议 (Transport)" htmlFor="vless-transport">
                  <Select
                    id="vless-transport"
                    value={settings.transport || "tcp"}
                    onChange={(e) => {
                      const tr = e.target.value as "tcp" | "ws" | "httpupgrade" | "grpc";
                      patchSettings({
                        transport: tr,
                        flow: tr === "tcp" ? settings.flow ?? "xtls-rprx-vision" : "",
                      });
                    }}
                  >
                    <option value="tcp">TCP（支持 XTLS-Vision）</option>
                    <option value="ws">WebSocket (WS)</option>
                    <option value="httpupgrade">HTTPUpgrade</option>
                    <option value="grpc">gRPC</option>
                  </Select>
                </Field>

                <Field label="流控模式 (Flow)" htmlFor="vless-flow">
                  <Select
                    id="vless-flow"
                    disabled={(settings.transport || "tcp") !== "tcp"}
                    value={
                      (settings.transport || "tcp") === "tcp"
                        ? settings.flow ?? "xtls-rprx-vision"
                        : ""
                    }
                    onChange={(e) => patchSettings({ flow: e.target.value })}
                  >
                    <option value="xtls-rprx-vision">xtls-rprx-vision</option>
                    <option value="">无流控</option>
                  </Select>
                </Field>
              </div>

              {(settings.transport === "ws" || settings.transport === "httpupgrade") && (
                <div className="grid gap-3.5 sm:grid-cols-2">
                  <Field label="路径 (Path)" htmlFor="vless-wspath">
                    <Input
                      id="vless-wspath"
                      value={settings.ws_path || ""}
                      onChange={(e) => patchSettings({ ws_path: e.target.value })}
                      placeholder="/ws"
                      className="font-mono"
                    />
                  </Field>
                  <Field label="Host 头（可选）" htmlFor="vless-wshost">
                    <Input
                      id="vless-wshost"
                      value={settings.ws_host || ""}
                      onChange={(e) => patchSettings({ ws_host: e.target.value })}
                      placeholder="cdn.example.com"
                      className="font-mono"
                    />
                  </Field>
                </div>
              )}

              {settings.transport === "grpc" && (
                <Field label="gRPC Service Name" htmlFor="vless-grpc">
                  <Input
                    id="vless-grpc"
                    value={settings.grpc_service_name || ""}
                    onChange={(e) => patchSettings({ grpc_service_name: e.target.value })}
                    placeholder="grpc-svc"
                    className="font-mono"
                  />
                </Field>
              )}

              {(settings.tls_mode || "reality") === "reality" && (
                <div className="space-y-3 rounded-md border border-hairline bg-surface p-3.5">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="text-xs font-medium text-ink">Reality 伪装与密钥配置</span>
                    <div className="flex items-center gap-2">
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        loading={probing}
                        onClick={() => void handleProbeReality()}
                      >
                        <Radar className="h-3 w-3" />
                        探测目标 SNI
                      </Button>
                      <Button
                        type="button"
                        variant="secondary"
                        size="xs"
                        loading={keygenBusy}
                        onClick={() => void handleGenReality()}
                      >
                        <Sparkles className="h-3 w-3" />
                        重新生成密钥对
                      </Button>
                    </div>
                  </div>

                  <div className="grid gap-3 sm:grid-cols-3">
                    <Field label="伪装握手目标 (Handshake Server)" htmlFor="r-host">
                      <Input
                        id="r-host"
                        value={settings.reality_handshake_server || ""}
                        onChange={(e) =>
                          patchSettings({
                            reality_handshake_server: e.target.value,
                            server_name: e.target.value,
                          })
                        }
                        placeholder="www.microsoft.com"
                        className="font-mono"
                      />
                    </Field>
                    <Field label="握手端口" htmlFor="r-port">
                      <Input
                        id="r-port"
                        type="number"
                        value={String(settings.reality_handshake_port ?? 443)}
                        onChange={(e) =>
                          patchSettings({
                            reality_handshake_port: Number.parseInt(e.target.value, 10) || 443,
                          })
                        }
                        className="font-mono"
                      />
                    </Field>
                    <Field label="SNI (server_name)" htmlFor="r-sni">
                      <Input
                        id="r-sni"
                        value={settings.server_name || ""}
                        onChange={(e) => patchSettings({ server_name: e.target.value })}
                        placeholder="同握手目标"
                        className="font-mono"
                      />
                    </Field>
                  </div>

                  {probeResult && (
                    <div className="flex flex-wrap items-center justify-between gap-2 rounded border border-hairline bg-recessed px-3 py-2 text-[11px]">
                      <span className="flex items-center gap-1.5 font-mono">
                        {probeResult.reachable && probeResult.tls13 && probeResult.h2 ? (
                          <CheckCircle2 className="h-3.5 w-3.5 text-ok" />
                        ) : (
                          <XCircle className="h-3.5 w-3.5 text-warn" />
                        )}
                        {probeResult.error
                          ? probeResult.error
                          : `${probeResult.target} (${probeResult.latency_ms}ms)`}
                      </span>
                      <div className="flex items-center gap-1.5">
                        <Badge tone={probeResult.tls13 ? "ok" : "danger"}>
                          {probeResult.tls13 ? "TLS 1.3" : "无 TLS 1.3"}
                        </Badge>
                        <Badge tone={probeResult.h2 ? "ok" : "warn"}>
                          {probeResult.h2 ? "H2" : "无 H2"}
                        </Badge>
                      </div>
                    </div>
                  )}

                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label="Reality 私钥 (Private Key)" htmlFor="r-priv">
                      <Input
                        id="r-priv"
                        value={settings.reality_private_key || ""}
                        onChange={(e) =>
                          patchSettings({ reality_private_key: e.target.value })
                        }
                        className="font-mono text-xs"
                        required
                      />
                    </Field>
                    <Field label="Reality 公钥 (Public Key，供客户端订阅)" htmlFor="r-pub">
                      <Input
                        id="r-pub"
                        value={settings.reality_public_key || ""}
                        onChange={(e) =>
                          patchSettings({ reality_public_key: e.target.value })
                        }
                        className="font-mono text-xs"
                      />
                    </Field>
                  </div>

                  <Field
                    label="Short IDs（英文逗号分隔的 16 进制字符串）"
                    htmlFor="r-sids"
                  >
                    <Input
                      id="r-sids"
                      value={(settings.reality_short_ids || []).join(", ")}
                      onChange={(e) =>
                        patchSettings({
                          reality_short_ids: e.target.value
                            .split(",")
                            .map((s) => s.trim())
                            .filter(Boolean),
                        })
                      }
                      placeholder="0123456789abcdef"
                      className="font-mono text-xs"
                    />
                  </Field>
                </div>
              )}

              {settings.tls_mode === "tls" && (
                <Field label="TLS SNI 域名（可选）" htmlFor="vless-sni">
                  <Input
                    id="vless-sni"
                    value={settings.server_name || ""}
                    onChange={(e) => patchSettings({ server_name: e.target.value })}
                    placeholder="node.example.com"
                    className="font-mono"
                  />
                </Field>
              )}
            </div>
          )}

          {/* Hysteria2 */}
          {protocol === "hysteria2" && (
            <div className="space-y-3.5">
              <div className="grid gap-3.5 sm:grid-cols-3">
                <Field label="上行带宽上限 (up_mbps)" htmlFor="hy2-up" hint="0 表示不限制">
                  <Input
                    id="hy2-up"
                    type="number"
                    min={0}
                    value={String(settings.up_mbps ?? 0)}
                    onChange={(e) =>
                      patchSettings({ up_mbps: Number.parseInt(e.target.value, 10) || 0 })
                    }
                    className="font-mono"
                  />
                </Field>
                <Field label="下行带宽上限 (down_mbps)" htmlFor="hy2-down" hint="0 表示不限制">
                  <Input
                    id="hy2-down"
                    type="number"
                    min={0}
                    value={String(settings.down_mbps ?? 0)}
                    onChange={(e) =>
                      patchSettings({ down_mbps: Number.parseInt(e.target.value, 10) || 0 })
                    }
                    className="font-mono"
                  />
                </Field>
                <Field
                  label="端口跳跃范围 (server_ports)"
                  htmlFor="hy2-ports"
                  hint="写入客户端订阅，例如 20000-30000"
                >
                  <Input
                    id="hy2-ports"
                    value={settings.server_ports || ""}
                    onChange={(e) => patchSettings({ server_ports: e.target.value })}
                    placeholder="20000-30000"
                    className="font-mono"
                  />
                </Field>
              </div>
              <div className="grid gap-3.5 sm:grid-cols-2">
                <Field label="Salamander 混淆密码（可选）" htmlFor="hy2-obfs">
                  <Input
                    id="hy2-obfs"
                    value={settings.obfs_password || ""}
                    onChange={(e) => patchSettings({ obfs_password: e.target.value })}
                    placeholder="留空不开启混淆"
                    className="font-mono"
                  />
                </Field>
                <Field label="伪装回落 URL (masquerade)" htmlFor="hy2-masq">
                  <Input
                    id="hy2-masq"
                    value={settings.masquerade || ""}
                    onChange={(e) => patchSettings({ masquerade: e.target.value })}
                    placeholder="https://bing.com"
                    className="font-mono"
                  />
                </Field>
              </div>
              <label className="flex items-center gap-2.5 text-xs text-ink-secondary">
                <Switch
                  checked={Boolean(settings.ignore_client_bandwidth)}
                  onChange={(v) => patchSettings({ ignore_client_bandwidth: v })}
                  label="忽略客户端带宽声明"
                />
                <span>忽略客户端声明带宽 (ignore_client_bandwidth)，强制启用 BBR 拥塞控制</span>
              </label>
            </div>
          )}

          {/* TUIC v5 */}
          {protocol === "tuic" && (
            <div className="space-y-3.5">
              <div className="grid gap-3.5 sm:grid-cols-3">
                <Field label="拥塞控制算法" htmlFor="tuic-cc">
                  <Select
                    id="tuic-cc"
                    value={settings.congestion_control || "bbr"}
                    onChange={(e) =>
                      patchSettings({
                        congestion_control: e.target.value as "bbr" | "cubic" | "new_reno",
                      })
                    }
                  >
                    <option value="bbr">BBR（推荐）</option>
                    <option value="cubic">Cubic</option>
                    <option value="new_reno">New Reno</option>
                  </Select>
                </Field>
                <Field label="认证超时 (auth_timeout)" htmlFor="tuic-auth">
                  <Input
                    id="tuic-auth"
                    value={settings.auth_timeout || "3s"}
                    onChange={(e) => patchSettings({ auth_timeout: e.target.value })}
                    className="font-mono"
                  />
                </Field>
                <Field label="心跳间隔 (heartbeat)" htmlFor="tuic-hb">
                  <Input
                    id="tuic-hb"
                    value={settings.heartbeat || "10s"}
                    onChange={(e) => patchSettings({ heartbeat: e.target.value })}
                    className="font-mono"
                  />
                </Field>
              </div>
              <label className="flex items-center gap-2.5 text-xs text-ink-secondary">
                <Switch
                  checked={Boolean(settings.zero_rtt_handshake)}
                  onChange={(v) => patchSettings({ zero_rtt_handshake: v })}
                  label="启用 0-RTT 握手"
                />
                <span>启用 QUIC 0-RTT 握手 (zero_rtt_handshake)</span>
              </label>
            </div>
          )}

          {/* AnyTLS */}
          {protocol === "anytls" && (
            <Field
              label="AnyTLS 填充方案 (padding_scheme，每行一条规则，留空采用默认填充)"
              htmlFor="anytls-pad"
            >
              <Textarea
                id="anytls-pad"
                rows={4}
                value={(settings.padding_scheme || []).join("\n")}
                onChange={(e) =>
                  patchSettings({
                    padding_scheme: e.target.value
                      .split("\n")
                      .map((l) => l.trim())
                      .filter(Boolean),
                  })
                }
                placeholder={"stop=8\n0=30-30\n1=100-400"}
                className="font-mono text-xs"
              />
            </Field>
          )}

          {/* Shadowsocks */}
          {protocol === "shadowsocks" && (
            <div className="space-y-3.5">
              <div className="grid gap-3.5 sm:grid-cols-2">
                <Field label="加密算法 (method)" htmlFor="ss-method">
                  <Select
                    id="ss-method"
                    value={settings.method || "2022-blake3-aes-128-gcm"}
                    onChange={(e) => patchSettings({ method: e.target.value })}
                  >
                    <option value="2022-blake3-aes-128-gcm">
                      2022-blake3-aes-128-gcm (EIH 多成员)
                    </option>
                    <option value="2022-blake3-aes-256-gcm">
                      2022-blake3-aes-256-gcm (EIH 多成员)
                    </option>
                    <option value="aes-256-gcm">aes-256-gcm (经典 AEAD)</option>
                    <option value="chacha20-ietf-poly1305">
                      chacha20-ietf-poly1305 (经典 AEAD)
                    </option>
                  </Select>
                </Field>
                <Field label="监听网络类型 (network)" htmlFor="ss-net">
                  <Select
                    id="ss-net"
                    value={settings.network || ""}
                    onChange={(e) => patchSettings({ network: e.target.value })}
                  >
                    <option value="">TCP + UDP（默认）</option>
                    <option value="tcp">仅 TCP</option>
                    <option value="udp">仅 UDP</option>
                  </Select>
                </Field>
              </div>
              <Field
                label="服务端主密钥 (server_password)"
                htmlFor="ss-pwd"
                hint="SS-2022 需使用与算法匹配的 16B / 32B Base64 密钥"
              >
                <div className="flex gap-2">
                  <Input
                    id="ss-pwd"
                    value={settings.server_password || ""}
                    onChange={(e) => patchSettings({ server_password: e.target.value })}
                    className="flex-1 font-mono text-xs"
                    required
                  />
                  <Button
                    type="button"
                    variant="secondary"
                    size="md"
                    loading={keygenBusy}
                    onClick={() =>
                      void handleGenSS2022(
                        "server_password",
                        settings.method || "2022-blake3-aes-128-gcm",
                      )
                    }
                  >
                    <KeyRound className="h-3.5 w-3.5" />
                    生成密钥
                  </Button>
                </div>
              </Field>
            </div>
          )}

          {/* Trojan */}
          {protocol === "trojan" && (
            <div className="space-y-3.5">
              <div className="grid gap-3.5 sm:grid-cols-2">
                <Field label="传输方式 (Transport)" htmlFor="trojan-tr">
                  <Select
                    id="trojan-tr"
                    value={settings.transport || "tcp"}
                    onChange={(e) =>
                      patchSettings({
                        transport: e.target.value as "tcp" | "ws" | "grpc",
                      })
                    }
                  >
                    <option value="tcp">标准 TCP</option>
                    <option value="ws">WebSocket</option>
                    <option value="grpc">gRPC</option>
                  </Select>
                </Field>
                <Field label="TLS SNI 域名（可选）" htmlFor="trojan-sni">
                  <Input
                    id="trojan-sni"
                    value={settings.server_name || ""}
                    onChange={(e) => patchSettings({ server_name: e.target.value })}
                    placeholder="node.example.com"
                    className="font-mono"
                  />
                </Field>
              </div>
              {settings.transport === "ws" && (
                <div className="grid gap-3.5 sm:grid-cols-2">
                  <Field label="WebSocket Path" htmlFor="trojan-wspath">
                    <Input
                      id="trojan-wspath"
                      value={settings.ws_path || "/trojan"}
                      onChange={(e) => patchSettings({ ws_path: e.target.value })}
                      className="font-mono"
                    />
                  </Field>
                  <Field label="WebSocket Host（可选）" htmlFor="trojan-wshost">
                    <Input
                      id="trojan-wshost"
                      value={settings.ws_host || ""}
                      onChange={(e) => patchSettings({ ws_host: e.target.value })}
                      className="font-mono"
                    />
                  </Field>
                </div>
              )}
              {settings.transport === "grpc" && (
                <Field label="gRPC Service Name" htmlFor="trojan-grpc">
                  <Input
                    id="trojan-grpc"
                    value={settings.grpc_service_name || "trojan-grpc"}
                    onChange={(e) =>
                      patchSettings({ grpc_service_name: e.target.value })
                    }
                    className="font-mono"
                  />
                </Field>
              )}
            </div>
          )}

          {/* ShadowTLS v3 */}
          {protocol === "shadowtls" && (
            <div className="space-y-3.5">
              <div className="grid gap-3.5 sm:grid-cols-3">
                <Field label="伪装握手域名 (handshake_server)" htmlFor="stls-host">
                  <Input
                    id="stls-host"
                    value={settings.handshake_server || "www.microsoft.com"}
                    onChange={(e) => patchSettings({ handshake_server: e.target.value })}
                    className="font-mono"
                    required
                  />
                </Field>
                <Field label="握手端口" htmlFor="stls-port">
                  <Input
                    id="stls-port"
                    type="number"
                    value={String(settings.handshake_port ?? 443)}
                    onChange={(e) =>
                      patchSettings({
                        handshake_port: Number.parseInt(e.target.value, 10) || 443,
                      })
                    }
                    className="font-mono"
                  />
                </Field>
                <Field label="底层联动协议 (detour_protocol)" htmlFor="stls-detour">
                  <Select
                    id="stls-detour"
                    value={settings.detour_protocol || "shadowsocks"}
                    onChange={(e) =>
                      patchSettings({
                        detour_protocol: e.target.value as "shadowsocks" | "vless",
                      })
                    }
                  >
                    <option value="shadowsocks">Shadowsocks 2022（推荐）</option>
                    <option value="vless">VLESS</option>
                  </Select>
                </Field>
              </div>

              {(settings.detour_protocol || "shadowsocks") === "shadowsocks" && (
                <div className="grid gap-3.5 sm:grid-cols-2">
                  <Field label="底层 SS 加密算法" htmlFor="stls-ss-method">
                    <Select
                      id="stls-ss-method"
                      value={settings.ss_method || "2022-blake3-aes-128-gcm"}
                      onChange={(e) => patchSettings({ ss_method: e.target.value })}
                    >
                      <option value="2022-blake3-aes-128-gcm">2022-blake3-aes-128-gcm</option>
                      <option value="2022-blake3-aes-256-gcm">2022-blake3-aes-256-gcm</option>
                      <option value="aes-256-gcm">aes-256-gcm</option>
                    </Select>
                  </Field>
                  <Field label="底层 SS 服务端密钥" htmlFor="stls-ss-pwd">
                    <div className="flex gap-2">
                      <Input
                        id="stls-ss-pwd"
                        value={settings.ss_server_password || ""}
                        onChange={(e) =>
                          patchSettings({ ss_server_password: e.target.value })
                        }
                        className="flex-1 font-mono text-xs"
                        required
                      />
                      <Button
                        type="button"
                        variant="secondary"
                        size="md"
                        loading={keygenBusy}
                        onClick={() =>
                          void handleGenSS2022(
                            "ss_server_password",
                            settings.ss_method || "2022-blake3-aes-128-gcm",
                          )
                        }
                      >
                        <KeyRound className="h-3.5 w-3.5" />
                        生成
                      </Button>
                    </div>
                  </Field>
                </div>
              )}

              <label className="flex items-center gap-2.5 text-xs text-ink-secondary">
                <Switch
                  checked={settings.strict_mode !== false}
                  onChange={(v) => patchSettings({ strict_mode: v })}
                  label="启用 ShadowTLS 严格模式"
                />
                <span>启用严格 TLS 1.3 验证模式 (strict_mode)</span>
              </label>
            </div>
          )}

          {/* Snell v5 */}
          {protocol === "snell" && (
            <div className="space-y-3.5">
              <div className="grid gap-3.5 sm:grid-cols-2">
                <Field label="协议版本 (version)" htmlFor="snell-ver">
                  <Select
                    id="snell-ver"
                    value={String(Number(settings.version) === 4 ? 4 : 5)}
                    onChange={(e) =>
                      patchSettings({ version: Number.parseInt(e.target.value, 10) === 4 ? 4 : 5 })
                    }
                  >
                    <option value="5">5 (推荐，兼容 v4/v5)</option>
                    <option value="4">4</option>
                  </Select>
                </Field>
                <Field label="混淆模式 (obfs_mode)" htmlFor="snell-obfs">
                  <Select
                    id="snell-obfs"
                    value={settings.obfs_mode === "http" ? "http" : "none"}
                    onChange={(e) => {
                      const nextMode = e.target.value === "http" ? "http" : "none";
                      patchSettings({
                        obfs_mode: nextMode,
                        ...(nextMode === "http" && !settings.obfs_host
                          ? { obfs_host: "www.bing.com" }
                          : {}),
                      });
                    }}
                  >
                    <option value="none">无混淆 (none)</option>
                    <option value="http">HTTP 混淆 (http)</option>
                  </Select>
                </Field>
              </div>

              <Field
                label="预共享密钥 (psk)"
                htmlFor="snell-psk"
                hint="推荐使用 31 位随机字母数字密钥，适配 Loon / Surge / sing-box 1.14+"
              >
                <div className="flex gap-2">
                  <Input
                    id="snell-psk"
                    value={settings.psk || ""}
                    onChange={(e) => patchSettings({ psk: e.target.value })}
                    placeholder="点击右侧随机生成 31 位 PSK"
                    className="flex-1 font-mono text-xs"
                    required
                  />
                  <Button
                    type="button"
                    variant="secondary"
                    size="md"
                    onClick={() => {
                      patchSettings({ psk: generateSnellPsk(31) });
                      toast.success("已随机生成 31 位 Snell PSK");
                    }}
                  >
                    <KeyRound className="h-3.5 w-3.5" />
                    随机生成 PSK
                  </Button>
                </div>
              </Field>

              {settings.obfs_mode === "http" && (
                <Field label="HTTP 混淆 Host (obfs_host)" htmlFor="snell-obfs-host">
                  <Input
                    id="snell-obfs-host"
                    value={settings.obfs_host ?? "www.bing.com"}
                    onChange={(e) => patchSettings({ obfs_host: e.target.value })}
                    placeholder="www.bing.com"
                    className="font-mono text-xs"
                  />
                </Field>
              )}

              <label className="flex items-center gap-2.5 text-xs text-ink-secondary">
                <Switch
                  checked={settings.udp !== false}
                  onChange={(v) => patchSettings({ udp: v })}
                  label="客户端启用 UDP"
                />
                <span>客户端启用 UDP 转发 (udp=true，写入 Loon / Surge / Clash 订阅)</span>
              </label>
            </div>
          )}

          {/* Direct */}
          {protocol === "direct" && (
            <div className="grid gap-3.5 sm:grid-cols-3">
              <Field label="转发目标地址 (override_address)" htmlFor="dir-addr">
                <Input
                  id="dir-addr"
                  value={settings.override_address || ""}
                  onChange={(e) => patchSettings({ override_address: e.target.value })}
                  placeholder="1.1.1.1"
                  className="font-mono"
                  required
                />
              </Field>
              <Field label="转发目标端口 (override_port)" htmlFor="dir-port">
                <Input
                  id="dir-port"
                  type="number"
                  min={1}
                  max={65535}
                  value={String(settings.override_port ?? 443)}
                  onChange={(e) =>
                    patchSettings({
                      override_port: Number.parseInt(e.target.value, 10) || 443,
                    })
                  }
                  className="font-mono"
                  required
                />
              </Field>
              <Field label="转发协议 (network)" htmlFor="dir-net">
                <Select
                  id="dir-net"
                  value={settings.network || ""}
                  onChange={(e) => patchSettings({ network: e.target.value })}
                >
                  <option value="">TCP + UDP</option>
                  <option value="tcp">仅 TCP</option>
                  <option value="udp">仅 UDP</option>
                </Select>
              </Field>
            </div>
          )}

          {/* Mixed / SOCKS / HTTP */}
          {(protocol === "mixed" || protocol === "socks" || protocol === "http") && (
            <label className="flex items-center gap-2.5 text-xs text-ink-secondary">
              <Switch
                checked={settings.auth_enabled !== false}
                onChange={(v) => patchSettings({ auth_enabled: v })}
                label="要求成员账号密码认证"
              />
              <span>启用用户名/密码认证（使用已绑定成员的 Name 与 Password）</span>
            </label>
          )}

          {/* Certificate Selector when required */}
          {needsCert && (
            <div className="border-t border-hairline pt-3.5">
              <Field
                label="绑定 TLS 证书（必填）"
                htmlFor="in-cert"
                hint="来自「证书中心」，自动续期时将同步触发核心热重载"
              >
                {certificates.length === 0 ? (
                  <div className="flex items-center gap-2 rounded-md border border-warn/45 bg-warn/10 px-3 py-2 text-xs text-warn">
                    <AlertTriangle className="h-4 w-4 shrink-0" />
                    <span>尚未配置任何证书，请先前往「证书中心」签发或一键生成自签证书。</span>
                  </div>
                ) : (
                  <Select
                    id="in-cert"
                    value={certificateId}
                    onChange={(e) => setCertificateId(e.target.value)}
                    required
                  >
                    <option value="">请选择一张证书...</option>
                    {certificates.map((c) => (
                      <option key={c.id} value={String(c.id)}>
                        {c.name} ({c.type}) — {(c.domains || []).join(", ") || c.cert_path}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
            </div>
          )}
        </div>

        {/* Member Binding Section */}
        {protocol !== "direct" && (
          <div className="space-y-3 rounded-md border border-hairline bg-surface p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <div className="text-xs font-semibold text-ink">成员凭据绑定</div>
                <div className="text-[11px] text-ink-muted">
                  选择允许接入此节点的成员，连接流量将自动归属到对应成员名下
                </div>
              </div>
              <div className="inline-flex rounded-md border border-hairline bg-recessed p-0.5 text-xs">
                <button
                  type="button"
                  onClick={() => setMemberMode("all")}
                  className={cn(
                    "rounded px-2.5 py-1 transition-colors",
                    memberMode === "all"
                      ? "bg-surface font-medium text-accent"
                      : "text-ink-muted hover:text-ink",
                  )}
                >
                  全部启用成员
                </button>
                <button
                  type="button"
                  onClick={() => setMemberMode("selected")}
                  className={cn(
                    "rounded px-2.5 py-1 transition-colors",
                    memberMode === "selected"
                      ? "bg-surface font-medium text-accent"
                      : "text-ink-muted hover:text-ink",
                  )}
                >
                  指定成员 ({memberIds.length})
                </button>
              </div>
            </div>

            {memberMode === "selected" && (
              <div className="flex flex-wrap gap-1.5 pt-1">
                {members.length === 0 ? (
                  <span className="text-xs text-ink-muted">
                    暂无成员，请前往「成员管理」创建成员。
                  </span>
                ) : (
                  members.map((m) => {
                    const selected = memberIds.includes(m.id);
                    return (
                      <button
                        key={m.id}
                        type="button"
                        onClick={() => toggleMemberSelection(m.id)}
                        className={cn(
                          "inline-flex items-center gap-1.5 rounded border px-2.5 py-1 font-mono text-xs transition-colors",
                          selected
                            ? "border-accent bg-accent-muted text-accent"
                            : "border-hairline bg-recessed text-ink-secondary hover:text-ink",
                        )}
                      >
                        <span>{m.name}</span>
                        {!m.enabled && <span className="text-[10px] text-ink-muted">(停用)</span>}
                      </button>
                    );
                  })
                )}
              </div>
            )}
          </div>
        )}

        {/* External Host & Multi-Relay Extra Entries */}
        <div className="space-y-3 rounded-md border border-hairline bg-surface p-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <div className="text-xs font-semibold text-ink">外部订阅地址与多中转入口</div>
              <div className="text-[11px] text-ink-muted">
                覆盖默认公网 IP，或附加多条国内中转 / CDN 入口（自动派生多条客户端节点）
              </div>
            </div>
            <Button type="button" variant="outline" size="xs" onClick={addExtraEntry}>
              <Plus className="h-3 w-3" />
              添加中转入口
            </Button>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="外部连接域名/IP 覆写（可选）" htmlFor="in-ext-host">
              <Input
                id="in-ext-host"
                value={externalHost}
                onChange={(e) => setExternalHost(e.target.value)}
                placeholder="留空默认使用面板公网 IP"
                className="font-mono text-xs"
              />
            </Field>
            <Field label="外部连接端口覆写（可选）" htmlFor="in-ext-port">
              <Input
                id="in-ext-port"
                type="number"
                min={0}
                max={65535}
                value={externalPort}
                onChange={(e) => setExternalPort(e.target.value)}
                placeholder="留空同监听端口"
                className="font-mono text-xs"
              />
            </Field>
          </div>

          {extraEntries.length > 0 && (
            <div className="space-y-2 border-t border-hairline pt-3">
              {extraEntries.map((entry, idx) => (
                <div key={idx} className="grid grid-cols-12 items-center gap-2">
                  <Input
                    value={entry.name}
                    onChange={(e) => updateExtraEntry(idx, { name: e.target.value })}
                    placeholder="备注（如 广港专线）"
                    className="col-span-4 h-8 text-xs"
                  />
                  <Input
                    value={entry.host}
                    onChange={(e) => updateExtraEntry(idx, { host: e.target.value })}
                    placeholder="relay.example.com"
                    className="col-span-5 h-8 font-mono text-xs"
                  />
                  <Input
                    type="number"
                    min={1}
                    max={65535}
                    value={String(entry.port || "")}
                    onChange={(e) =>
                      updateExtraEntry(idx, {
                        port: Number.parseInt(e.target.value, 10) || 443,
                      })
                    }
                    placeholder="端口"
                    className="col-span-2 h-8 font-mono text-xs"
                  />
                  <div className="col-span-1 flex justify-end">
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onClick={() => removeExtraEntry(idx)}
                      aria-label="删除中转入口"
                    >
                      <Trash2 className="h-3.5 w-3.5 text-danger" />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </form>
    </Modal>
  );
}
