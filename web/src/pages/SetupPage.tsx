import React, { useState } from "react";
import { AlertCircle, Check, Lock, Moon, Server, ShieldCheck, Sun } from "lucide-react";
import { api, ApiError, setAuthToken } from "@/lib/api";
import { buildAppUrl, getBasePath, normalizeBasePath } from "@/lib/basePath";
import { useAuth } from "@/contexts/AuthContext";
import { useTheme } from "@/contexts/ThemeContext";
import { Button, Field, Input } from "@/components/ui/primitives";

function evaluatePasswordStrength(pwd: string): { score: number; label: string } {
  if (!pwd) return { score: 0, label: "" };
  let score = 0;
  if (pwd.length >= 8) score += 1;
  if (pwd.length >= 12) score += 1;
  if (/[a-z]/.test(pwd) && /[A-Z]/.test(pwd)) score += 1;
  if (/\d/.test(pwd) && /[^a-zA-Z0-9]/.test(pwd)) score += 1;
  const labels = ["过短", "较弱", "一般", "良好", "强"];
  return { score, label: labels[score] };
}

export function SetupPage() {
  const { status, refresh } = useAuth();
  const { theme, toggleTheme } = useTheme();
  const currentBase = normalizeBasePath(status?.base_path || getBasePath());

  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [instanceId, setInstanceId] = useState("");
  const [webBasePath, setWebBasePath] = useState(currentBase);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const strength = evaluatePasswordStrength(password);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    const trimmedUser = username.trim();
    if (!trimmedUser) {
      setError("请输入管理员用户名。");
      return;
    }
    if (password.length < 6) {
      setError("管理员密码至少需要 6 位字符。");
      return;
    }
    if (password !== confirmPassword) {
      setError("两次输入的密码不一致。");
      return;
    }

    setSubmitting(true);
    try {
      const normalizedPath = webBasePath.trim()
        ? normalizeBasePath(webBasePath)
        : undefined;
      const res = await api.setup({
        username: trimmedUser,
        password,
        instance_id: instanceId.trim() || undefined,
        web_base_path: normalizedPath,
      });

      if (res.token) {
        setAuthToken(res.token);
      }

      const targetBase = normalizeBasePath(res.base_path || normalizedPath || currentBase);
      if (targetBase !== getBasePath()) {
        window.location.assign(buildAppUrl("/", targetBase));
        return;
      }
      await refresh();
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError("初始化失败，请检查面板服务日志。");
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="min-h-screen bg-canvas text-ink">
      <div className="mx-auto flex min-h-screen max-w-5xl flex-col justify-center px-4 py-10 sm:px-6">
        <div className="mb-6 flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <svg className="h-7 w-7" viewBox="0 0 32 32" aria-hidden="true">
              <rect width="32" height="32" rx="6" className="fill-surface stroke-hairline-strong" strokeWidth="1.2" />
              <path
                d="M8.5 21L13.8 11.5L18.2 18L23 11"
                fill="none"
                className="stroke-accent"
                strokeWidth="2.3"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
              <circle cx="23" cy="11" r="2.2" className="fill-ok" />
            </svg>
            <div>
              <span className="font-mono text-sm font-semibold tracking-tight text-ink">
                sbox-panel
              </span>
              <span className="ml-2 rounded border border-hairline bg-surface px-1.5 py-0.5 font-mono text-[11px] text-ink-secondary">
                初始化向导
              </span>
            </div>
          </div>

          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={toggleTheme}
            title={theme === "dark" ? "切换到浅色界面" : "切换到深色界面"}
            aria-label="切换配色模式"
          >
            {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </Button>
        </div>

        <div className="grid overflow-hidden rounded-lg border border-hairline bg-surface lg:grid-cols-12">
          {/* Left architectural specification rail */}
          <aside className="flex flex-col justify-between border-b border-hairline bg-recessed p-6 lg:col-span-5 lg:border-b-0 lg:border-r">
            <div className="space-y-5">
              <div>
                <h1 className="text-xl font-semibold tracking-tight text-ink">
                  配置当前节点控制面
                </h1>
                <p className="mt-2 text-sm leading-relaxed text-ink-secondary">
                  创建管理员凭据并确认安全入口路径。完成后即可接管本机 sing-box 核心进程、查看实时遥测与在线升级版本。
                </p>
              </div>

              <div className="space-y-3 border-t border-hairline pt-5 text-xs">
                <div className="flex items-start gap-2.5">
                  <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-ok" />
                  <div>
                    <p className="font-medium text-ink">安全入口隔离</p>
                    <p className="mt-0.5 text-ink-secondary">
                      访问根路径或未知路径将返回无指纹 404，避免公网扫描暴露面板特征。
                    </p>
                  </div>
                </div>
                <div className="flex items-start gap-2.5">
                  <Server className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
                  <div>
                    <p className="font-medium text-ink">独立核心托管</p>
                    <p className="mt-0.5 text-ink-secondary">
                      兼容官方 sing-box 1.12+ 二进制，支持升级前配置预检与探活失败自动回滚。
                    </p>
                  </div>
                </div>
                <div className="flex items-start gap-2.5">
                  <Lock className="mt-0.5 h-4 w-4 shrink-0 text-uplink" />
                  <div>
                    <p className="font-medium text-ink">本地 SQLite 持久化</p>
                    <p className="mt-0.5 text-ink-secondary">
                      忘记密码或路径时可通过 SSH 执行{" "}
                      <code className="rounded bg-surface px-1 py-0.5 font-mono text-[11px] text-ink">
                        sbox-panel show-url
                      </code>{" "}
                      或{" "}
                      <code className="rounded bg-surface px-1 py-0.5 font-mono text-[11px] text-ink">
                        reset-auth
                      </code>
                      。
                    </p>
                  </div>
                </div>
              </div>
            </div>

            <div className="mt-6 rounded-md border border-hairline bg-surface p-3">
              <div className="flex items-center justify-between text-xs text-ink-secondary">
                <span>当前入口路径</span>
                <span className="font-mono text-ink">{currentBase || "/"}</span>
              </div>
            </div>
          </aside>

          {/* Right initialization form */}
          <form onSubmit={handleSubmit} className="space-y-5 p-6 sm:p-7 lg:col-span-7">
            {error && (
              <div
                role="alert"
                className="flex items-start gap-2.5 rounded-md border border-danger/40 bg-danger/10 px-3.5 py-2.5 text-xs text-danger"
              >
                <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                <span>{error}</span>
              </div>
            )}

            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="管理员用户名" htmlFor="setup-username">
                <Input
                  id="setup-username"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  autoComplete="username"
                  placeholder="admin"
                  required
                />
              </Field>

              <Field
                label="实例标识（可选）"
                htmlFor="setup-instance-id"
                hint="如 jp-tokyo-1，用于标识当前 VPS 节点"
              >
                <Input
                  id="setup-instance-id"
                  value={instanceId}
                  onChange={(e) => setInstanceId(e.target.value)}
                  placeholder="jp-tokyo-1"
                  className="font-mono"
                />
              </Field>
            </div>

            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="管理员密码" htmlFor="setup-password" hint="至少 8 位，建议包含大小写与数字">
                <Input
                  id="setup-password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="new-password"
                  placeholder="输入高强度密码"
                  required
                />
                {password && (
                  <div className="mt-1.5 flex items-center gap-2">
                    <div className="flex flex-1 gap-1">
                      {[1, 2, 3, 4].map((level) => (
                        <span
                          key={level}
                          className={`h-1 flex-1 rounded-sm ${
                            strength.score >= level
                              ? strength.score <= 1
                                ? "bg-danger"
                                : strength.score === 2
                                  ? "bg-warn"
                                  : "bg-ok"
                              : "bg-hairline"
                          }`}
                        />
                      ))}
                    </div>
                    <span className="text-[11px] text-ink-secondary">{strength.label}</span>
                  </div>
                )}
              </Field>

              <Field label="确认密码" htmlFor="setup-confirm-password">
                <Input
                  id="setup-confirm-password"
                  type="password"
                  value={confirmPassword}
                  onChange={(e) => setConfirmPassword(e.target.value)}
                  autoComplete="new-password"
                  placeholder="再次输入密码"
                  required
                />
              </Field>
            </div>

            <Field
              label="安全访问路径 (Web Base Path)"
              htmlFor="setup-base-path"
              hint="保留随机生成的路径即可，或自定义以 / 开头的高熵路径；变更后自动跳转。"
            >
              <Input
                id="setup-base-path"
                value={webBasePath}
                onChange={(e) => setWebBasePath(e.target.value)}
                placeholder="/sbox-9f3k2a"
                className="font-mono"
              />
            </Field>

            <div className="flex items-center justify-between border-t border-hairline pt-4">
              <span className="flex items-center gap-1.5 text-xs text-ink-muted">
                <Check className="h-3.5 w-3.5 text-ok" />
                初始化后可在「安全与设置」启用 TOTP 两步验证
              </span>
              <Button type="submit" variant="primary" loading={submitting}>
                完成初始化并进入面板
              </Button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}
