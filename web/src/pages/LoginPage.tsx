import React, { useState } from "react";
import { AlertCircle, KeyRound, Moon, ShieldAlert, Sun } from "lucide-react";
import { api, ApiError, setAuthToken } from "@/lib/api";
import { getBasePath } from "@/lib/basePath";
import { useAuth } from "@/contexts/AuthContext";
import { useTheme } from "@/contexts/ThemeContext";
import { Button, Field, Input } from "@/components/ui/primitives";

export function LoginPage() {
  const { status, refresh } = useAuth();
  const { theme, toggleTheme } = useTheme();

  const [username, setUsername] = useState(status?.username || "admin");
  const [password, setPassword] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [needsTotp, setNeedsTotp] = useState(Boolean(status?.totp_enabled));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [rateLimited, setRateLimited] = useState<string | null>(null);

  const basePath = status?.base_path || getBasePath() || "/";

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setRateLimited(null);

    if (!username.trim() || !password) {
      setError("请输入管理员用户名与密码。");
      return;
    }
    if (needsTotp && totpCode.trim().length < 6) {
      setError("请输入 6 位动态验证码。");
      return;
    }

    setSubmitting(true);
    try {
      const res = await api.login({
        username: username.trim(),
        password,
        totp_code: totpCode.trim() || undefined,
      });

      if (res.requires_totp) {
        setNeedsTotp(true);
        setError(null);
        return;
      }

      if (res.token) {
        setAuthToken(res.token);
      }
      await refresh();
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 429) {
          const retryHint = err.retryAfter ? `（请在约 ${err.retryAfter} 秒后重试）` : "，请稍后再试";
          setRateLimited(`当前来源 IP 登录尝试过于频繁，已触发防爆破限流${retryHint}。`);
        } else {
          setError(err.message);
        }
      } else {
        setError("无法连接至面板服务，请检查网络或服务状态。");
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex min-h-screen flex-col bg-canvas text-ink">
      <div className="flex items-center justify-end p-4">
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

      <main className="mx-auto flex w-full max-w-md flex-1 flex-col justify-center px-4 pb-16">
        <div className="mb-6 flex items-center gap-3">
          <svg className="h-8 w-8 shrink-0" viewBox="0 0 32 32" aria-hidden="true">
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
            <h1 className="font-mono text-base font-semibold tracking-tight text-ink">
              sbox-panel
            </h1>
            <p className="text-xs text-ink-secondary">
              节点控制面身份验证 · <span className="font-mono">{basePath}</span>
            </p>
          </div>
        </div>

        <div className="rounded-lg border border-hairline bg-surface p-6">
          {rateLimited && (
            <div
              role="alert"
              className="mb-4 flex items-start gap-2.5 rounded-md border border-warn/45 bg-warn/10 p-3 text-xs text-warn"
            >
              <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" />
              <div>
                <p className="font-medium">登录请求受限 (HTTP 429)</p>
                <p className="mt-1 text-ink-secondary">{rateLimited}</p>
              </div>
            </div>
          )}

          {error && (
            <div
              role="alert"
              className="mb-4 flex items-start gap-2.5 rounded-md border border-danger/45 bg-danger/10 p-3 text-xs text-danger"
            >
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{error}</span>
            </div>
          )}

          <form onSubmit={handleSubmit} className="space-y-4">
            <Field label="管理员用户名" htmlFor="login-username">
              <Input
                id="login-username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                placeholder="admin"
                required
              />
            </Field>

            <Field label="密码" htmlFor="login-password">
              <Input
                id="login-password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                placeholder="输入管理员密码"
                required
              />
            </Field>

            {needsTotp ? (
              <div className="rounded-md border border-accent/35 bg-accent-muted p-3.5">
                <Field
                  label="两步验证码 (TOTP 6 位数字)"
                  htmlFor="login-totp"
                  hint="打开验证器应用输入当前动态口令"
                >
                  <div className="relative">
                    <KeyRound className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-accent" />
                    <Input
                      id="login-totp"
                      value={totpCode}
                      onChange={(e) =>
                        setTotpCode(e.target.value.replace(/\D/g, "").slice(0, 6))
                      }
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      placeholder="000000"
                      autoFocus
                      className="pl-9 font-mono text-base tracking-[0.3em]"
                    />
                  </div>
                </Field>
              </div>
            ) : (
              <button
                type="button"
                onClick={() => setNeedsTotp(true)}
                className="text-xs text-ink-muted hover:text-accent"
              >
                已启用两步验证？直接输入动态口令
              </button>
            )}

            <Button type="submit" variant="primary" className="w-full" loading={submitting}>
              {needsTotp ? "验证并登录" : "登录控制台"}
            </Button>
          </form>
        </div>

        <p className="mt-4 text-center text-xs text-ink-muted">
          遗失管理员凭据时请在宿主机执行{" "}
          <code className="rounded border border-hairline bg-surface px-1.5 py-0.5 font-mono text-[11px] text-ink-secondary">
            sbox-panel reset-auth
          </code>
        </p>
      </main>
    </div>
  );
}
