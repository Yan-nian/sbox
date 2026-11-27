import React, { useCallback, useEffect, useState } from "react";
import {
  Check,
  Copy,
  KeyRound,
  Lock,
  Save,
  ShieldCheck,
  ShieldOff,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { buildAppUrl, getBasePath, normalizeBasePath } from "@/lib/basePath";
import { useAuth } from "@/contexts/AuthContext";
import { useToast } from "@/components/ui/Toast";
import { Badge, Button, Field, Input, Panel, Select } from "@/components/ui/primitives";
import { QrCode } from "@/components/QrCode";
import { BackupRecoverySection } from "@/components/BackupRecoverySection";
import type { CertificateItem, SettingsData, TOTPSetupResponse } from "@/types/api";

export function SettingsPage() {
  const { refresh: refreshAuth } = useAuth();
  const toast = useToast();

  // Global settings form
  const [loading, setLoading] = useState(true);
  const [savingSettings, setSavingSettings] = useState(false);
  const [settings, setSettings] = useState<SettingsData | null>(null);
  const [instanceId, setInstanceId] = useState("");
  const [webBasePath, setWebBasePath] = useState("");
  const [listenPort, setListenPort] = useState("");
  const [publicIpv4, setPublicIpv4] = useState("");
  const [publicIpv6, setPublicIpv6] = useState("");
  const [githubProxy, setGithubProxy] = useState("");
  const [logLevel, setLogLevel] = useState("info");
  const [panelCertId, setPanelCertId] = useState("0");
  const [certificates, setCertificates] = useState<CertificateItem[]>([]);

  // Password form
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [savingPassword, setSavingPassword] = useState(false);

  // TOTP enrollment & disable state
  const [totpSetup, setTotpSetup] = useState<TOTPSetupResponse | null>(null);
  const [totpLoading, setTotpLoading] = useState(false);
  const [enableCode, setEnableCode] = useState("");
  const [disablePassword, setDisablePassword] = useState("");
  const [disableCode, setDisableCode] = useState("");
  const [copiedSecret, setCopiedSecret] = useState(false);

  const loadSettings = useCallback(async () => {
    setLoading(true);
    try {
      const [data, certRes] = await Promise.all([
        api.getSettings(),
        api.listCertificates(1).catch(() => ({ certificates: [] })),
      ]);
      setSettings(data);
      setCertificates(Array.isArray(certRes.certificates) ? certRes.certificates : []);
      setInstanceId(data.instance_id || "");
      setWebBasePath(data.web_base_path || "");
      setListenPort(data.listen_port ? String(data.listen_port) : "");
      setPublicIpv4(data.public_ipv4 || "");
      setPublicIpv6(data.public_ipv6 || "");
      setGithubProxy(data.github_proxy || "");
      setLogLevel(data.log_level || "info");
      setPanelCertId(String(data.panel_cert_id || 0));
    } catch (err) {
      toast.error("加载系统设置失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setLoading(false);
    }
  }, [toast]);

  useEffect(() => {
    void loadSettings();
  }, [loadSettings]);

  const handleSaveSettings = async (e: React.FormEvent) => {
    e.preventDefault();
    setSavingSettings(true);
    try {
      const normalizedPath = normalizeBasePath(webBasePath);
      if (webBasePath.trim() && (!normalizedPath || normalizedPath === "/api" || normalizedPath.startsWith("/api/"))) {
        toast.error("安全访问路径无效", "路径必须为非根路径（如 /sbox-9f3k2a），且不能以 /api 开头");
        return;
      }
      const parsedPort = listenPort.trim() ? Number.parseInt(listenPort.trim(), 10) : undefined;
      if (parsedPort !== undefined && (Number.isNaN(parsedPort) || parsedPort < 1 || parsedPort > 65535)) {
        toast.error("端口无效", "监听端口必须在 1–65535 之间");
        return;
      }

      const updated = await api.updateSettings({
        instance_id: instanceId.trim() || undefined,
        web_base_path: normalizedPath || undefined,
        listen_port: parsedPort,
        public_ipv4: publicIpv4.trim(),
        public_ipv6: publicIpv6.trim(),
        github_proxy: githubProxy.trim(),
        log_level: logLevel,
        panel_cert_id: Number.parseInt(panelCertId, 10) || 0,
      });

      setSettings(updated);
      const activePath = getBasePath();
      const targetPath = normalizeBasePath(updated.web_base_path || normalizedPath);

      if (targetPath !== activePath) {
        toast.success("安全访问路径已更新", `即将跳转至 ${targetPath || "/"}`);
        window.setTimeout(() => {
          window.location.assign(buildAppUrl("/settings", targetPath));
        }, 650);
        return;
      }

      toast.success("系统设置已保存");
    } catch (err) {
      toast.error("保存设置失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setSavingSettings(false);
    }
  };

  const handleChangePassword = async (e: React.FormEvent) => {
    e.preventDefault();
    if (newPassword.length < 6) {
      toast.error("新密码长度不足", "密码至少需要 6 位字符");
      return;
    }
    if (newPassword !== confirmPassword) {
      toast.error("两次输入的新密码不一致");
      return;
    }

    setSavingPassword(true);
    try {
      await api.changePassword({
        current_password: currentPassword,
        new_password: newPassword,
      });
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      toast.success("管理员密码已更新");
    } catch (err) {
      toast.error("修改密码失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setSavingPassword(false);
    }
  };

  const handleStartTotp = async () => {
    setTotpLoading(true);
    try {
      const res = await api.setupTOTP();
      setTotpSetup(res);
      setEnableCode("");
    } catch (err) {
      toast.error("生成两步验证密钥失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setTotpLoading(false);
    }
  };

  const handleEnableTotp = async (e: React.FormEvent) => {
    e.preventDefault();
    if (enableCode.trim().length < 6) {
      toast.error("请输入 6 位动态验证码");
      return;
    }
    setTotpLoading(true);
    try {
      await api.enableTOTP({ code: enableCode.trim() });
      setTotpSetup(null);
      setEnableCode("");
      toast.success("已启用 TOTP 两步验证");
      await Promise.all([loadSettings(), refreshAuth()]);
    } catch (err) {
      toast.error("启用两步验证失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setTotpLoading(false);
    }
  };

  const handleDisableTotp = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!disablePassword || disableCode.trim().length < 6) {
      toast.error("请输入当前管理员密码和 6 位动态验证码");
      return;
    }
    setTotpLoading(true);
    try {
      await api.disableTOTP({
        password: disablePassword,
        code: disableCode.trim(),
      });
      setDisablePassword("");
      setDisableCode("");
      toast.success("已关闭 TOTP 两步验证");
      await Promise.all([loadSettings(), refreshAuth()]);
    } catch (err) {
      toast.error("关闭两步验证失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setTotpLoading(false);
    }
  };

  const copySecret = async () => {
    if (!totpSetup?.secret) return;
    try {
      await navigator.clipboard.writeText(totpSetup.secret);
      setCopiedSecret(true);
      toast.info("已复制 TOTP 手动密钥");
      window.setTimeout(() => setCopiedSecret(false), 1800);
    } catch {
      toast.error("复制失败");
    }
  };

  return (
    <div className="space-y-5">
      {/* Instance, Security Path & Network Settings */}
      <Panel
        title="实例标识与安全入口配置"
        description="动态修改安全访问路径后将立即生效并自动重定向当前浏览器"
      >
        {loading ? (
          <div className="py-8 text-center text-xs text-ink-muted">正在加载配置...</div>
        ) : (
          <form onSubmit={handleSaveSettings} className="space-y-4">
            <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
              <Field
                label="实例标识 (instance_id)"
                htmlFor="set-instance-id"
                hint="用于 Gist / Sub-Store 多机同步区分当前节点"
              >
                <Input
                  id="set-instance-id"
                  value={instanceId}
                  onChange={(e) => setInstanceId(e.target.value)}
                  placeholder="jp-tokyo-1"
                  className="font-mono"
                />
              </Field>

              <Field
                label="安全访问路径 (Web Base Path)"
                htmlFor="set-base-path"
                hint="保存后立刻切换路由，旧路径将返回无指纹 404"
              >
                <Input
                  id="set-base-path"
                  value={webBasePath}
                  onChange={(e) => setWebBasePath(e.target.value)}
                  placeholder="/sbox-9f3k2a"
                  className="font-mono"
                />
              </Field>

              <Field
                label="监听端口 (Listen Port)"
                htmlFor="set-listen-port"
                hint="修改端口后需重启面板服务或通过 CLI 生效"
              >
                <Input
                  id="set-listen-port"
                  type="number"
                  min={1}
                  max={65535}
                  value={listenPort}
                  onChange={(e) => setListenPort(e.target.value)}
                  placeholder="18080"
                  className="font-mono"
                />
              </Field>

              <Field
                label="公网 IPv4 覆写"
                htmlFor="set-ipv4"
                hint="留空时自动探测宿主机公网 IPv4"
              >
                <Input
                  id="set-ipv4"
                  value={publicIpv4}
                  onChange={(e) => setPublicIpv4(e.target.value)}
                  placeholder="自动探测"
                  className="font-mono"
                />
              </Field>

              <Field
                label="公网 IPv6 覆写"
                htmlFor="set-ipv6"
                hint="留空时自动探测宿主机公网 IPv6"
              >
                <Input
                  id="set-ipv6"
                  value={publicIpv6}
                  onChange={(e) => setPublicIpv6(e.target.value)}
                  placeholder="自动探测"
                  className="font-mono"
                />
              </Field>

              <Field
                label="核心默认日志级别"
                htmlFor="set-log-level"
                hint="写入编译后的 sing-box config.json"
              >
                <Select
                  id="set-log-level"
                  value={logLevel}
                  onChange={(e) => setLogLevel(e.target.value)}
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

            <div className="grid gap-4 md:grid-cols-2">
              <Field
                label="GitHub 下载加速代理 (GitHub Proxy)"
                htmlFor="set-github-proxy"
                hint="可选，用于加速拉取 sing-box Release 二进制（例如 https://ghfast.top/）"
              >
                <Input
                  id="set-github-proxy"
                  value={githubProxy}
                  onChange={(e) => setGithubProxy(e.target.value)}
                  placeholder="https://ghfast.top/"
                  className="font-mono"
                />
              </Field>

              <Field
                label="面板原生 HTTPS 证书 (panel_cert_id)"
                htmlFor="set-panel-cert"
                hint="绑定来自「证书中心」的域名证书以启用面板原生 TLS；切换 HTTP/HTTPS 后需重启生效"
              >
                <Select
                  id="set-panel-cert"
                  value={panelCertId}
                  onChange={(e) => setPanelCertId(e.target.value)}
                >
                  <option value="0">禁用 HTTPS（仅 HTTP 监听）</option>
                  {certificates.map((cert) => (
                    <option key={cert.id} value={String(cert.id)}>
                      #{cert.id} {cert.name} — {(cert.domains || []).join(", ") || cert.type}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>

            <div className="flex justify-end border-t border-hairline pt-4">
              <Button type="submit" variant="primary" loading={savingSettings}>
                <Save className="h-3.5 w-3.5" />
                保存系统设置
              </Button>
            </div>
          </form>
        )}
      </Panel>

      {/* Bottom Security Row: TOTP 2FA + Change Password */}
      <div className="grid gap-5 lg:grid-cols-12">
        {/* TOTP 2FA Panel */}
        <Panel
          className="lg:col-span-7"
          title="两步验证 (TOTP 2FA)"
          description="绑定 Google Authenticator、1Password 或 Bitwarden 等动态口令应用加固登录"
          actions={
            <Badge tone={settings?.totp_enabled ? "ok" : "neutral"}>
              {settings?.totp_enabled ? (
                <>
                  <ShieldCheck className="h-3 w-3" />
                  已启用
                </>
              ) : (
                <>
                  <ShieldOff className="h-3 w-3" />
                  未启用
                </>
              )}
            </Badge>
          }
        >
          {settings?.totp_enabled ? (
            <form onSubmit={handleDisableTotp} className="space-y-4">
              <div className="rounded-md border border-ok/35 bg-ok/10 p-3 text-xs text-ink-secondary">
                当前管理员账户已受 TOTP 两步验证保护。如需解绑，请提供管理员密码及当前有效动态口令。
              </div>

              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="当前管理员密码" htmlFor="totp-disable-pwd">
                  <Input
                    id="totp-disable-pwd"
                    type="password"
                    value={disablePassword}
                    onChange={(e) => setDisablePassword(e.target.value)}
                    autoComplete="current-password"
                    placeholder="输入管理员密码"
                    required
                  />
                </Field>

                <Field label="当前 6 位动态验证码" htmlFor="totp-disable-code">
                  <Input
                    id="totp-disable-code"
                    value={disableCode}
                    onChange={(e) =>
                      setDisableCode(e.target.value.replace(/\D/g, "").slice(0, 6))
                    }
                    inputMode="numeric"
                    placeholder="000000"
                    className="font-mono tracking-[0.25em]"
                    required
                  />
                </Field>
              </div>

              <div className="flex justify-end">
                <Button type="submit" variant="danger" loading={totpLoading}>
                  关闭两步验证
                </Button>
              </div>
            </form>
          ) : totpSetup ? (
            <div className="space-y-4">
              <div className="flex flex-col gap-5 sm:flex-row sm:items-start">
                <div className="shrink-0">
                  <QrCode value={totpSetup.otpauth_url} size={172} />
                </div>

                <div className="min-w-0 flex-1 space-y-3">
                  <div className="text-xs leading-relaxed text-ink-secondary">
                    请使用验证器应用扫描左侧二维码，或手动录入下方 Base32 密钥，随后输入生成的 6 位验证码完成绑定。
                  </div>

                  <div>
                    <div className="text-[11px] text-ink-muted">手动录入密钥 (Secret)</div>
                    <div className="mt-1 flex items-center gap-2">
                      <code className="flex-1 truncate rounded border border-hairline bg-recessed px-2.5 py-1.5 font-mono text-xs text-ink">
                        {totpSetup.secret}
                      </code>
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        onClick={() => void copySecret()}
                        title="复制密钥"
                        aria-label="复制手动密钥"
                      >
                        {copiedSecret ? (
                          <Check className="h-3.5 w-3.5 text-ok" />
                        ) : (
                          <Copy className="h-3.5 w-3.5" />
                        )}
                      </Button>
                    </div>
                  </div>

                  <form onSubmit={handleEnableTotp} className="space-y-3 pt-1">
                    <Field label="输入验证器显示的 6 位验证码" htmlFor="totp-enable-code">
                      <Input
                        id="totp-enable-code"
                        value={enableCode}
                        onChange={(e) =>
                          setEnableCode(e.target.value.replace(/\D/g, "").slice(0, 6))
                        }
                        inputMode="numeric"
                        autoComplete="one-time-code"
                        placeholder="000000"
                        className="font-mono text-base tracking-[0.3em]"
                        required
                      />
                    </Field>
                    <div className="flex items-center gap-2">
                      <Button type="submit" variant="primary" size="sm" loading={totpLoading}>
                        验证并启用 2FA
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={() => setTotpSetup(null)}
                      >
                        取消
                      </Button>
                    </div>
                  </form>
                </div>
              </div>
            </div>
          ) : (
            <div className="flex flex-col items-start justify-between gap-4 sm:flex-row sm:items-center">
              <div className="text-xs leading-relaxed text-ink-secondary">
                启用后每次登录控制台除密码外还需提供 30 秒动态口令，有效抵御凭据泄露风险。如遗失设备可在宿主机执行{" "}
                <code className="rounded bg-recessed px-1 py-0.5 font-mono text-ink">
                  sbox-panel reset-auth
                </code>{" "}
                重置。
              </div>
              <Button
                variant="secondary"
                size="sm"
                loading={totpLoading}
                onClick={() => void handleStartTotp()}
              >
                <KeyRound className="h-3.5 w-3.5" />
                开始绑定 2FA
              </Button>
            </div>
          )}
        </Panel>

        {/* Password Change Panel */}
        <Panel
          className="lg:col-span-5"
          title="修改管理员密码"
          description="建议使用 12 位以上随机密码"
        >
          <form onSubmit={handleChangePassword} className="space-y-3.5">
            <Field label="当前密码" htmlFor="pwd-current">
              <Input
                id="pwd-current"
                type="password"
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
                autoComplete="current-password"
                required
              />
            </Field>

            <Field label="新密码" htmlFor="pwd-new" hint="至少 8 位字符">
              <Input
                id="pwd-new"
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                autoComplete="new-password"
                required
              />
            </Field>

            <Field label="确认新密码" htmlFor="pwd-confirm">
              <Input
                id="pwd-confirm"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                autoComplete="new-password"
                required
              />
            </Field>

            <div className="flex justify-end pt-1">
              <Button type="submit" variant="secondary" loading={savingPassword}>
                <Lock className="h-3.5 w-3.5" />
                更新密码
              </Button>
            </div>
          </form>
        </Panel>
      </div>

      {/* Milestone 5: Local & Remote AES-256-GCM Backup & Disaster Recovery */}
      <BackupRecoverySection />
    </div>
  );
}
