import React, { useCallback, useEffect, useState } from "react";
import {
  AlertTriangle,
  Check,
  CheckCircle2,
  Copy,
  FileKey2,
  Globe,
  KeyRound,
  Plus,
  Radar,
  RefreshCw,
  Shield,
  ShieldCheck,
  Trash2,
  XCircle,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { cn } from "@/lib/cn";
import { formatDateTime } from "@/lib/format";
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
  Switch,
} from "@/components/ui/primitives";
import { NodeScopeBanner } from "@/components/NodeScopeBanner";
import type { AppRoute } from "@/components/AppShell";
import type {
  ACMEChallenge,
  CertificateItem,
  CertificateType,
  DNSProvider,
  RealityKeypair,
  RealityProbeResult,
} from "@/types/api";

type CreateTab = "acme" | "self_signed" | "external";

const CERT_TYPE_LABELS: Record<CertificateType, { label: string; tone: "accent" | "ok" | "warn" | "neutral" }> = {
  acme_http: { label: "ACME HTTP-01", tone: "accent" },
  acme_dns: { label: "ACME DNS-01", tone: "ok" },
  self_signed: { label: "自签 ECDSA", tone: "warn" },
  external: { label: "外部路径", tone: "neutral" },
};

const REALITY_PRESETS = [
  "www.microsoft.com",
  "www.apple.com",
  "swdist.apple.com",
  "www.cloudflare.com",
];

function parseDomainList(raw: string): string[] {
  return raw
    .split(/[\s,;\n]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

function getDaysRemaining(notAfter?: string): number | null {
  if (!notAfter) return null;
  const ts = new Date(notAfter).getTime();
  if (Number.isNaN(ts)) return null;
  return Math.ceil((ts - Date.now()) / 86400_000);
}

export function CertificatesPage({ onNavigate }: { onNavigate?: (route: AppRoute) => void }) {
  const { activeNodeId, activeNode } = useNode();
  const toast = useToast();
  const [certs, setCerts] = useState<CertificateItem[]>([]);
  const [loading, setLoading] = useState(true);

  const nodeOffline = activeNode ? !activeNode.online : false;
  const [renewingId, setRenewingId] = useState<number | null>(null);
  const [deletingId, setDeletingId] = useState<number | null>(null);
  const [forceDeleteTarget, setForceDeleteTarget] = useState<{
    cert: CertificateItem;
    reason: string;
  } | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  // Create Modal state
  const [modalOpen, setModalOpen] = useState(false);
  const [createTab, setCreateTab] = useState<CreateTab>("self_signed");
  const [submitting, setSubmitting] = useState(false);

  // ACME fields
  const [acmeName, setAcmeName] = useState("");
  const [acmeEmail, setAcmeEmail] = useState("");
  const [acmeDomains, setAcmeDomains] = useState("");
  const [acmeChallenge, setAcmeChallenge] = useState<ACMEChallenge>("dns01");
  const [acmeHttpPort, setAcmeHttpPort] = useState("80");
  const [dnsProvider, setDnsProvider] = useState<DNSProvider>("cloudflare");
  const [cfToken, setCfToken] = useState("");
  const [cfEmail, setCfEmail] = useState("");
  const [cfApiKey, setCfApiKey] = useState("");
  const [aliAccessKey, setAliAccessKey] = useState("");
  const [aliSecretKey, setAliSecretKey] = useState("");
  const [dnspodApiKey, setDnspodApiKey] = useState("");
  const [acmeAutoRenew, setAcmeAutoRenew] = useState(true);
  const [acmeStaging, setAcmeStaging] = useState(false);

  // Self-signed fields
  const [ssName, setSsName] = useState("");
  const [ssDomains, setSsDomains] = useState("www.bing.com");
  const [ssValidDays, setSsValidDays] = useState("3650");

  // External fields
  const [extName, setExtName] = useState("");
  const [extCertPath, setExtCertPath] = useState("");
  const [extKeyPath, setExtKeyPath] = useState("");

  // Reality toolbox state
  const [keypair, setKeypair] = useState<RealityKeypair | null>(null);
  const [generatingKeys, setGeneratingKeys] = useState(false);
  const [probeTarget, setProbeTarget] = useState("www.microsoft.com:443");
  const [probing, setProbing] = useState(false);
  const [probeResult, setProbeResult] = useState<RealityProbeResult | null>(null);

  const loadCertificates = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.listCertificates(activeNodeId);
      setCerts(Array.isArray(res.certificates) ? res.certificates : []);
    } catch (err) {
      toast.error("加载证书列表失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setLoading(false);
    }
  }, [activeNodeId, toast]);

  useEffect(() => {
    void loadCertificates();
  }, [loadCertificates]);

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

  const resetModalForm = () => {
    setAcmeName("");
    setAcmeEmail("");
    setAcmeDomains("");
    setCfToken("");
    setCfEmail("");
    setCfApiKey("");
    setAliAccessKey("");
    setAliSecretKey("");
    setDnspodApiKey("");
    setSsName("");
    setSsDomains("www.bing.com");
    setSsValidDays("3650");
    setExtName("");
    setExtCertPath("");
    setExtKeyPath("");
  };

  const handleCreateSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      if (createTab === "self_signed") {
        const name = ssName.trim() || "self-signed";
        const domains = parseDomainList(ssDomains);
        const days = Number.parseInt(ssValidDays, 10);
        await api.createSelfSignedCert({
          node_id: activeNodeId,
          name,
          domains: domains.length > 0 ? domains : ["www.bing.com"],
          valid_days: Number.isFinite(days) && days > 0 ? days : 3650,
        });
        toast.success("自签证书已生成", name);
      } else if (createTab === "external") {
        if (!extName.trim() || !extCertPath.trim() || !extKeyPath.trim()) {
          toast.error("请完整填写证书名称、公钥路径和私钥路径");
          return;
        }
        await api.createExternalCert({
          node_id: activeNodeId,
          name: extName.trim(),
          cert_path: extCertPath.trim(),
          key_path: extKeyPath.trim(),
        });
        toast.success("外部证书已导入", extName.trim());
      } else {
        const domains = parseDomainList(acmeDomains);
        if (!acmeName.trim() || !acmeEmail.trim() || domains.length === 0) {
          toast.error("请填写名称、ACME 邮箱以及至少一个域名");
          return;
        }
        const dns_creds: Record<string, string> = {};
        if (acmeChallenge === "dns01") {
          if (dnsProvider === "cloudflare") {
            if (cfToken.trim()) dns_creds.api_token = cfToken.trim();
            if (cfEmail.trim()) dns_creds.email = cfEmail.trim();
            if (cfApiKey.trim()) dns_creds.api_key = cfApiKey.trim();
          } else if (dnsProvider === "alidns") {
            dns_creds.access_key = aliAccessKey.trim();
            dns_creds.secret_key = aliSecretKey.trim();
          } else if (dnsProvider === "dnspod") {
            dns_creds.api_key = dnspodApiKey.trim();
          }
        }
        await api.createACMECert({
          node_id: activeNodeId,
          name: acmeName.trim(),
          email: acmeEmail.trim(),
          domains,
          challenge: acmeChallenge,
          http_port: acmeChallenge === "http01" ? acmeHttpPort.trim() || "80" : undefined,
          dns_provider: acmeChallenge === "dns01" ? dnsProvider : undefined,
          dns_creds: acmeChallenge === "dns01" ? dns_creds : undefined,
          auto_renew: acmeAutoRenew,
          staging: acmeStaging,
        });
        toast.success("ACME 证书签发成功", domains.join(", "));
      }

      setModalOpen(false);
      resetModalForm();
      await loadCertificates();
    } catch (err) {
      toast.error("创建证书失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setSubmitting(false);
    }
  };

  const handleRenew = async (cert: CertificateItem) => {
    setRenewingId(cert.id);
    try {
      await api.renewCertificate(cert.id);
      toast.success("证书续期成功", cert.name);
      await loadCertificates();
    } catch (err) {
      toast.error("证书续期失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setRenewingId(null);
    }
  };

  const handleDelete = async (cert: CertificateItem, force = false) => {
    setDeletingId(cert.id);
    try {
      await api.deleteCertificate(cert.id, force);
      setForceDeleteTarget(null);
      toast.success("已删除证书", cert.name);
      await loadCertificates();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && !force) {
        setForceDeleteTarget({ cert, reason: err.message });
      } else {
        toast.error("删除证书失败", err instanceof ApiError ? err.message : undefined);
      }
    } finally {
      setDeletingId(null);
    }
  };

  const handleGenerateReality = async () => {
    setGeneratingKeys(true);
    try {
      const kp = await api.generateRealityKeypair();
      setKeypair(kp);
      toast.success("已生成 X25519 Reality 密钥对");
    } catch (err) {
      toast.error("生成密钥失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setGeneratingKeys(false);
    }
  };

  const handleProbeReality = async (e?: React.FormEvent, customTarget?: string) => {
    e?.preventDefault();
    const target = (customTarget ?? probeTarget).trim();
    if (!target) return;
    if (customTarget) setProbeTarget(customTarget);
    setProbing(true);
    try {
      const res = await api.probeRealityTarget(target, activeNodeId);
      setProbeResult(res);
    } catch (err) {
      toast.error("探测目标域名失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setProbing(false);
    }
  };

  const expiringCount = certs.filter((c) => {
    const days = getDaysRemaining(c.not_after);
    return days !== null && days <= 30;
  }).length;
  const autoRenewCount = certs.filter((c) => c.auto_renew).length;

  return (
    <div className="space-y-5">
      <NodeScopeBanner
        onNavigate={onNavigate}
        offlineHint="当前远程节点离线：暂不可在该节点上签发/续期证书或发起 SNI 探测，请待 Agent 上线后操作。"
      />

      {/* Summary Strip */}
      <div className="grid divide-y divide-hairline overflow-hidden rounded-lg border border-hairline bg-surface sm:grid-cols-3 sm:divide-x sm:divide-y-0">
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">托管证书总数</div>
          <div className="mt-1 font-mono text-lg font-semibold text-ink tabular-nums">
            {certs.length}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">开启自动续期</div>
          <div className="mt-1 font-mono text-lg font-semibold text-ok tabular-nums">
            {autoRenewCount}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">30 天内到期 / 已过期</div>
          <div
            className={cn(
              "mt-1 font-mono text-lg font-semibold tabular-nums",
              expiringCount > 0 ? "text-warn" : "text-ink",
            )}
          >
            {expiringCount}
          </div>
        </div>
      </div>

      {/* Certificates Table Panel */}
      <Panel
        title="统一证书仓库"
        description="签发、导入并复用至 Hysteria2 / TUIC / AnyTLS / Trojan / VLESS TLS 及面板原生 HTTPS"
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              loading={loading}
              onClick={() => void loadCertificates()}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              刷新
            </Button>
            <Button
              variant="primary"
              size="sm"
              disabled={nodeOffline}
              onClick={() => {
                setCreateTab("self_signed");
                setModalOpen(true);
              }}
            >
              <Plus className="h-3.5 w-3.5" />
              添加证书
            </Button>
          </div>
        }
        bodyClassName="p-0"
      >
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="border-b border-hairline bg-recessed text-ink-secondary">
              <tr>
                <th className="px-4 py-2.5 font-medium">证书名称与类型</th>
                <th className="px-3 py-2.5 font-medium">绑定域名 (SAN)</th>
                <th className="px-3 py-2.5 font-medium">有效期与签发者</th>
                <th className="px-3 py-2.5 font-medium">SHA-256 指纹</th>
                <th className="px-4 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {loading && certs.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-12 text-center text-ink-muted">
                    正在加载证书列表...
                  </td>
                </tr>
              ) : certs.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-12 text-center">
                    <Shield className="mx-auto h-7 w-7 text-ink-muted" />
                    <p className="mt-2 text-xs text-ink-secondary">尚未配置任何 TLS 证书</p>
                    <p className="mt-1 text-[11px] text-ink-muted">
                      无域名场景可一键生成自签 ECDSA 证书；有域名支持 ACME DNS-01 / HTTP-01 自动签发。
                    </p>
                    <Button
                      variant="secondary"
                      size="sm"
                      className="mt-3"
                      onClick={() => {
                        setCreateTab("self_signed");
                        setModalOpen(true);
                      }}
                    >
                      <Plus className="h-3.5 w-3.5" />
                      立即创建首张证书
                    </Button>
                  </td>
                </tr>
              ) : (
                certs.map((cert) => {
                  const typeMeta = CERT_TYPE_LABELS[cert.type] ?? {
                    label: cert.type,
                    tone: "neutral" as const,
                  };
                  const days = getDaysRemaining(cert.not_after);
                  const expired = days !== null && days <= 0;
                  const expiringSoon = days !== null && days > 0 && days <= 30;
                  const fpShort = cert.fingerprint
                    ? `${cert.fingerprint.slice(0, 14)}…${cert.fingerprint.slice(-8)}`
                    : "-";

                  return (
                    <tr key={cert.id} className="hover:bg-surface-hover/60">
                      <td className="px-4 py-3">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="font-medium text-ink">{cert.name}</span>
                          <Badge tone={typeMeta.tone}>{typeMeta.label}</Badge>
                          {cert.auto_renew && <Badge tone="ok">自动续期</Badge>}
                          {cert.dns_provider && (
                            <span className="font-mono text-[11px] text-ink-muted">
                              {cert.dns_provider}
                            </span>
                          )}
                        </div>
                        <div
                          className="mt-1 max-w-[280px] truncate font-mono text-[11px] text-ink-muted"
                          title={cert.cert_path}
                        >
                          {cert.cert_path}
                        </div>
                        {cert.last_error && (
                          <div className="mt-1 flex items-center gap-1 text-[11px] text-danger">
                            <AlertTriangle className="h-3 w-3 shrink-0" />
                            <span className="truncate max-w-[280px]" title={cert.last_error}>
                              {cert.last_error}
                            </span>
                          </div>
                        )}
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex max-w-[240px] flex-wrap gap-1">
                          {(cert.domains || []).length > 0 ? (
                            cert.domains.map((d) => (
                              <span
                                key={d}
                                className="rounded border border-hairline bg-recessed px-1.5 py-0.5 font-mono text-[11px] text-ink"
                              >
                                {d}
                              </span>
                            ))
                          ) : (
                            <span className="text-ink-muted">-</span>
                          )}
                        </div>
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex items-center gap-1.5 font-mono tabular-nums">
                          {days !== null ? (
                            <Badge tone={expired ? "danger" : expiringSoon ? "warn" : "ok"}>
                              {expired ? "已过期" : `剩余 ${days} 天`}
                            </Badge>
                          ) : (
                            <span className="text-ink-muted">未知</span>
                          )}
                        </div>
                        <div className="mt-1 font-mono text-[11px] text-ink-muted tabular-nums">
                          {cert.not_after ? formatDateTime(cert.not_after) : "-"}
                        </div>
                        {cert.issuer && (
                          <div className="mt-0.5 max-w-[180px] truncate text-[11px] text-ink-secondary">
                            {cert.issuer}
                          </div>
                        )}
                      </td>

                      <td className="px-3 py-3">
                        {cert.fingerprint ? (
                          <button
                            type="button"
                            onClick={() =>
                              void copyValue(`fp-${cert.id}`, cert.fingerprint!, "证书指纹")
                            }
                            className="inline-flex items-center gap-1.5 rounded border border-hairline bg-recessed px-2 py-1 font-mono text-[11px] text-ink-secondary hover:border-hairline-strong hover:text-ink"
                            title={cert.fingerprint}
                          >
                            <span>{fpShort}</span>
                            {copiedKey === `fp-${cert.id}` ? (
                              <Check className="h-3 w-3 text-ok" />
                            ) : (
                              <Copy className="h-3 w-3" />
                            )}
                          </button>
                        ) : (
                          <span className="text-ink-muted">-</span>
                        )}
                      </td>

                      <td className="px-4 py-3 text-right">
                        <div className="inline-flex items-center gap-1.5">
                          <Button
                            variant="secondary"
                            size="xs"
                            loading={renewingId === cert.id}
                            disabled={renewingId !== null || deletingId !== null || nodeOffline}
                            onClick={() => void handleRenew(cert)}
                          >
                            <RefreshCw className="h-3 w-3" />
                            {cert.type === "external" ? "重检" : "续期"}
                          </Button>
                          <Button
                            variant="danger"
                            size="xs"
                            loading={deletingId === cert.id}
                            disabled={renewingId !== null || deletingId !== null}
                            onClick={() => void handleDelete(cert, false)}
                          >
                            <Trash2 className="h-3 w-3" />
                            删除
                          </Button>
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

      {/* Reality Keypair & SNI Probe Toolbox */}
      <div className="grid gap-5 lg:grid-cols-12">
        <Panel
          className="lg:col-span-6"
          title="Reality X25519 密钥与 ShortID 生成器"
          description="输出与 sing-box generate reality-keypair 完全一致的 RawURL Base64 格式"
          actions={
            <Button
              variant="primary"
              size="sm"
              loading={generatingKeys}
              onClick={() => void handleGenerateReality()}
            >
              <KeyRound className="h-3.5 w-3.5" />
              生成新密钥对
            </Button>
          }
        >
          {keypair ? (
            <div className="space-y-3">
              {[
                { key: "priv", label: "服务端私钥 (Private Key)", val: keypair.private_key },
                { key: "pub", label: "客户端公钥 (Public Key)", val: keypair.public_key },
                { key: "sid", label: "随机 Short ID (8 字节 Hex)", val: keypair.short_id },
              ].map((row) => (
                <div key={row.key}>
                  <div className="mb-1 text-[11px] text-ink-muted">{row.label}</div>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 truncate rounded border border-hairline bg-recessed px-2.5 py-1.5 font-mono text-xs text-ink">
                      {row.val}
                    </code>
                    <Button
                      variant="outline"
                      size="icon"
                      onClick={() => void copyValue(`rk-${row.key}`, row.val, row.label)}
                      aria-label={`复制${row.label}`}
                    >
                      {copiedKey === `rk-${row.key}` ? (
                        <Check className="h-3.5 w-3.5 text-ok" />
                      ) : (
                        <Copy className="h-3.5 w-3.5" />
                      )}
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center rounded-md border border-dashed border-hairline-strong bg-recessed py-9 text-center">
              <FileKey2 className="h-6 w-6 text-ink-muted" />
              <p className="mt-2 text-xs text-ink-secondary">
                点击右上角按钮即时生成 X25519 公私钥对与 16 位 ShortID
              </p>
            </div>
          )}
        </Panel>

        <Panel
          className="lg:col-span-6"
          title="Reality 目标伪装域名 (SNI) 探测"
          description="从当前 VPS 本地发起 TLS 握手，检测目标是否支持 TLS 1.3 + HTTP/2"
        >
          <form onSubmit={(e) => void handleProbeReality(e)} className="space-y-3">
            <div className="flex gap-2">
              <Input
                value={probeTarget}
                onChange={(e) => setProbeTarget(e.target.value)}
                placeholder="例如 www.microsoft.com:443"
                className="flex-1 font-mono text-xs"
              />
              <Button type="submit" variant="secondary" size="md" loading={probing} disabled={nodeOffline}>
                <Radar className="h-3.5 w-3.5" />
                探测握手
              </Button>
            </div>

            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-[11px] text-ink-muted">常用预设:</span>
              {REALITY_PRESETS.map((preset) => (
                <button
                  key={preset}
                  type="button"
                  onClick={() => void handleProbeReality(undefined, preset)}
                  className="rounded border border-hairline bg-recessed px-2 py-0.5 font-mono text-[11px] text-ink-secondary hover:border-accent/50 hover:text-ink"
                >
                  {preset}
                </button>
              ))}
            </div>
          </form>

          {probeResult && (
            <div
              className={cn(
                "mt-4 rounded-md border p-3.5 text-xs",
                probeResult.reachable && probeResult.tls13 && probeResult.h2
                  ? "border-ok/40 bg-ok/10"
                  : probeResult.reachable
                    ? "border-warn/45 bg-warn/10"
                    : "border-danger/45 bg-danger/10",
              )}
            >
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex items-center gap-2 font-medium text-ink">
                  {probeResult.reachable && probeResult.tls13 && probeResult.h2 ? (
                    <CheckCircle2 className="h-4 w-4 text-ok" />
                  ) : (
                    <XCircle
                      className={cn(
                        "h-4 w-4",
                        probeResult.reachable ? "text-warn" : "text-danger",
                      )}
                    />
                  )}
                  <span className="font-mono">{probeResult.target}</span>
                </div>
                <div className="flex items-center gap-1.5 font-mono">
                  <Badge tone={probeResult.tls13 ? "ok" : "danger"}>
                    {probeResult.tls13 ? "TLS 1.3" : "非 TLS 1.3"}
                  </Badge>
                  <Badge tone={probeResult.h2 ? "ok" : "warn"}>
                    {probeResult.h2 ? "H2" : "非 H2"}
                  </Badge>
                  {probeResult.reachable && (
                    <Badge tone="neutral">{probeResult.latency_ms} ms</Badge>
                  )}
                </div>
              </div>

              {probeResult.error ? (
                <p className="mt-2 font-mono text-[11px] text-danger">{probeResult.error}</p>
              ) : (
                <dl className="mt-2.5 grid gap-1.5 border-t border-hairline/70 pt-2.5 text-[11px] sm:grid-cols-2">
                  <div>
                    <span className="text-ink-muted">证书主体: </span>
                    <span className="font-mono text-ink">
                      {probeResult.server_name || "-"}
                    </span>
                  </div>
                  <div>
                    <span className="text-ink-muted">签发机构: </span>
                    <span className="font-mono text-ink-secondary">
                      {probeResult.issuer || "-"}
                    </span>
                  </div>
                  {probeResult.not_after && (
                    <div className="sm:col-span-2">
                      <span className="text-ink-muted">到期时间: </span>
                      <span className="font-mono text-ink-secondary">
                        {formatDateTime(probeResult.not_after)}
                      </span>
                    </div>
                  )}
                </dl>
              )}
            </div>
          )}
        </Panel>
      </div>

      {/* Create Certificate Modal */}
      <Modal
        open={modalOpen}
        onClose={() => !submitting && setModalOpen(false)}
        title="添加 TLS 证书"
        description="选择 ACME 自动签发、一键自签 ECDSA 或导入宿主机已有证书文件"
        footer={
          <>
            <Button
              variant="ghost"
              size="sm"
              disabled={submitting}
              onClick={() => setModalOpen(false)}
            >
              取消
            </Button>
            <Button
              type="submit"
              form="cert-create-form"
              variant="primary"
              size="sm"
              loading={submitting}
            >
              {createTab === "acme"
                ? "开始 ACME 签发"
                : createTab === "self_signed"
                  ? "生成自签证书"
                  : "校验并导入"}
            </Button>
          </>
        }
      >
        <div className="mb-4 grid grid-cols-3 gap-1 rounded-md border border-hairline bg-recessed p-1 text-xs">
          {(
            [
              { id: "acme", label: "ACME 自动签发", icon: Globe },
              { id: "self_signed", label: "一键自签证书", icon: ShieldCheck },
              { id: "external", label: "外部路径导入", icon: FileKey2 },
            ] as const
          ).map((t) => {
            const Icon = t.icon;
            return (
              <button
                key={t.id}
                type="button"
                onClick={() => setCreateTab(t.id)}
                className={cn(
                  "flex items-center justify-center gap-1.5 rounded px-2.5 py-2 font-medium transition-colors",
                  createTab === t.id
                    ? "bg-surface text-accent shadow-sm"
                    : "text-ink-secondary hover:text-ink",
                )}
              >
                <Icon className="h-3.5 w-3.5" />
                {t.label}
              </button>
            );
          })}
        </div>

        <form id="cert-create-form" onSubmit={handleCreateSubmit} className="space-y-4">
          {createTab === "acme" && (
            <>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="证书名称" htmlFor="acme-name">
                  <Input
                    id="acme-name"
                    value={acmeName}
                    onChange={(e) => setAcmeName(e.target.value)}
                    placeholder="main-wildcard"
                    required
                  />
                </Field>
                <Field label="ACME 注册邮箱" htmlFor="acme-email">
                  <Input
                    id="acme-email"
                    type="email"
                    value={acmeEmail}
                    onChange={(e) => setAcmeEmail(e.target.value)}
                    placeholder="admin@example.com"
                    required
                  />
                </Field>
              </div>

              <Field
                label="域名列表"
                htmlFor="acme-domains"
                hint="多个域名用逗号或空格分隔；DNS-01 支持泛域名（如 *.example.com）"
              >
                <Input
                  id="acme-domains"
                  value={acmeDomains}
                  onChange={(e) => setAcmeDomains(e.target.value)}
                  placeholder="node.example.com, *.node.example.com"
                  className="font-mono"
                  required
                />
              </Field>

              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="验证方式 (Challenge)" htmlFor="acme-challenge">
                  <Select
                    id="acme-challenge"
                    value={acmeChallenge}
                    onChange={(e) => setAcmeChallenge(e.target.value as ACMEChallenge)}
                  >
                    <option value="dns01">DNS-01 API（推荐，无需占用 80 端口）</option>
                    <option value="http01">HTTP-01（临时监听 80 端口验证）</option>
                  </Select>
                </Field>

                {acmeChallenge === "http01" ? (
                  <Field
                    label="HTTP-01 验证端口"
                    htmlFor="acme-http-port"
                    hint="标准 Let's Encrypt 要求公网 80 端口可达"
                  >
                    <Input
                      id="acme-http-port"
                      value={acmeHttpPort}
                      onChange={(e) => setAcmeHttpPort(e.target.value)}
                      placeholder="80"
                      className="font-mono"
                    />
                  </Field>
                ) : (
                  <Field label="DNS 服务商" htmlFor="acme-dns-provider">
                    <Select
                      id="acme-dns-provider"
                      value={dnsProvider}
                      onChange={(e) => setDnsProvider(e.target.value as DNSProvider)}
                    >
                      <option value="cloudflare">Cloudflare</option>
                      <option value="alidns">阿里云 DNS (AliDNS)</option>
                      <option value="dnspod">腾讯云 DNSPod</option>
                    </Select>
                  </Field>
                )}
              </div>

              {acmeChallenge === "dns01" && (
                <div className="space-y-3 rounded-md border border-hairline bg-recessed p-3.5">
                  <div className="text-xs font-medium text-ink">
                    {dnsProvider === "cloudflare"
                      ? "Cloudflare API 凭据"
                      : dnsProvider === "alidns"
                        ? "阿里云 AccessKey 凭据"
                        : "DNSPod API 密钥"}
                  </div>

                  {dnsProvider === "cloudflare" && (
                    <div className="space-y-3">
                      <Field
                        label="DNS API Token（推荐，Zone:DNS:Edit 权限）"
                        htmlFor="cf-token"
                      >
                        <Input
                          id="cf-token"
                          type="password"
                          value={cfToken}
                          onChange={(e) => setCfToken(e.target.value)}
                          placeholder="输入 Cloudflare Scoped API Token"
                          className="font-mono"
                        />
                      </Field>
                      <div className="grid gap-3 sm:grid-cols-2">
                        <Field label="或 Global API Email（可选）" htmlFor="cf-email">
                          <Input
                            id="cf-email"
                            value={cfEmail}
                            onChange={(e) => setCfEmail(e.target.value)}
                            placeholder="留空优先使用 Token"
                          />
                        </Field>
                        <Field label="Global API Key（可选）" htmlFor="cf-key">
                          <Input
                            id="cf-key"
                            type="password"
                            value={cfApiKey}
                            onChange={(e) => setCfApiKey(e.target.value)}
                            className="font-mono"
                          />
                        </Field>
                      </div>
                    </div>
                  )}

                  {dnsProvider === "alidns" && (
                    <div className="grid gap-3 sm:grid-cols-2">
                      <Field label="AccessKey ID" htmlFor="ali-ak">
                        <Input
                          id="ali-ak"
                          value={aliAccessKey}
                          onChange={(e) => setAliAccessKey(e.target.value)}
                          className="font-mono"
                          required
                        />
                      </Field>
                      <Field label="AccessKey Secret" htmlFor="ali-sk">
                        <Input
                          id="ali-sk"
                          type="password"
                          value={aliSecretKey}
                          onChange={(e) => setAliSecretKey(e.target.value)}
                          className="font-mono"
                          required
                        />
                      </Field>
                    </div>
                  )}

                  {dnsProvider === "dnspod" && (
                    <Field
                      label="DNSPod API Key"
                      htmlFor="dnspod-key"
                      hint="格式为 ID,Token（用英文逗号拼接）"
                    >
                      <Input
                        id="dnspod-key"
                        type="password"
                        value={dnspodApiKey}
                        onChange={(e) => setDnspodApiKey(e.target.value)}
                        placeholder="123456,abcdef0123456789..."
                        className="font-mono"
                        required
                      />
                    </Field>
                  )}
                </div>
              )}

              <div className="flex flex-wrap items-center justify-between gap-4 rounded-md border border-hairline bg-recessed px-3.5 py-2.5 text-xs">
                <label className="flex items-center gap-2.5">
                  <Switch
                    checked={acmeAutoRenew}
                    onChange={setAcmeAutoRenew}
                    label="开启到期前 30 天自动续期"
                  />
                  <span className="text-ink">剩余不足 30 天自动无感续期</span>
                </label>

                <label className="flex items-center gap-2.5">
                  <Switch
                    checked={acmeStaging}
                    onChange={setAcmeStaging}
                    label="使用 Let's Encrypt Staging 测试环境"
                  />
                  <span className="text-ink-secondary">Let's Encrypt Staging 测试</span>
                </label>
              </div>
            </>
          )}

          {createTab === "self_signed" && (
            <>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="证书名称" htmlFor="ss-name">
                  <Input
                    id="ss-name"
                    value={ssName}
                    onChange={(e) => setSsName(e.target.value)}
                    placeholder="hy2-self-signed"
                    required
                  />
                </Field>
                <Field label="有效天数" htmlFor="ss-days">
                  <Input
                    id="ss-days"
                    type="number"
                    min={1}
                    max={7300}
                    value={ssValidDays}
                    onChange={(e) => setSsValidDays(e.target.value)}
                    className="font-mono"
                  />
                </Field>
              </div>

              <Field
                label="绑定域名或公网 IP (SAN)"
                htmlFor="ss-domains"
                hint="多个条目用逗号分隔；默认 www.bing.com（仅作 TLS SNI，订阅连接自动使用节点公网 IP）"
              >
                <Input
                  id="ss-domains"
                  value={ssDomains}
                  onChange={(e) => setSsDomains(e.target.value)}
                  placeholder="www.bing.com"
                  className="font-mono"
                />
              </Field>
            </>
          )}

          {createTab === "external" && (
            <>
              <Field label="证书名称" htmlFor="ext-name">
                <Input
                  id="ext-name"
                  value={extName}
                  onChange={(e) => setExtName(e.target.value)}
                  placeholder="openresty-wildcard"
                  required
                />
              </Field>
              <Field
                label="宿主机证书链文件路径 (fullchain.pem)"
                htmlFor="ext-cert"
                hint="面板将校验文件可读性及公私钥匹配度"
              >
                <Input
                  id="ext-cert"
                  value={extCertPath}
                  onChange={(e) => setExtCertPath(e.target.value)}
                  placeholder="/etc/ssl/certs/fullchain.pem"
                  className="font-mono"
                  required
                />
              </Field>
              <Field label="宿主机私钥文件路径 (privkey.pem)" htmlFor="ext-key">
                <Input
                  id="ext-key"
                  value={extKeyPath}
                  onChange={(e) => setExtKeyPath(e.target.value)}
                  placeholder="/etc/ssl/private/privkey.pem"
                  className="font-mono"
                  required
                />
              </Field>
            </>
          )}
        </form>
      </Modal>

      {/* Force delete confirmation modal on HTTP 409 conflict */}
      <Modal
        open={forceDeleteTarget !== null}
        onClose={() => setForceDeleteTarget(null)}
        title="证书仍被引用"
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
              loading={deletingId === forceDeleteTarget?.cert.id}
              onClick={() =>
                forceDeleteTarget && void handleDelete(forceDeleteTarget.cert, true)
              }
            >
              强制解除关联并删除
            </Button>
          </>
        }
      >
        <p className="text-xs leading-relaxed text-ink-secondary">
          证书 <strong className="font-mono text-ink">{forceDeleteTarget?.cert.name}</strong>{" "}
          当前正被入站节点或面板 HTTPS 绑定。强制删除将清空关联引用，对应 TLS 入站需重新指定证书后才能正常工作。
        </p>
      </Modal>
    </div>
  );
}
