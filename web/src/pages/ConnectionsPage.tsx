import { useCallback, useEffect, useMemo, useState } from "react";
import { Pause, Play, RefreshCw, Search, Unplug } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { formatBytes, formatRelativeAge } from "@/lib/format";
import { useToast } from "@/components/ui/Toast";
import { Badge, Button, Input, Panel } from "@/components/ui/primitives";
import type { ConnectionItem } from "@/types/api";

export function ConnectionsPage() {
  const toast = useToast();
  const [connections, setConnections] = useState<ConnectionItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [paused, setPaused] = useState(false);
  const [search, setSearch] = useState("");
  const [closingId, setClosingId] = useState<string | null>(null);

  const fetchConnections = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      const res = await api.getConnections();
      setConnections(Array.isArray(res.connections) ? res.connections : []);
      setError(null);
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "无法获取活跃连接（请确认 sing-box 核心运行中）",
      );
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchConnections(false);
  }, [fetchConnections]);

  useEffect(() => {
    if (paused) return;
    const timer = window.setInterval(() => {
      void fetchConnections(true);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [paused, fetchConnections]);

  const handleClose = async (conn: ConnectionItem) => {
    setClosingId(conn.id);
    try {
      await api.closeConnection(conn.id);
      setConnections((prev) => prev.filter((item) => item.id !== conn.id));
      toast.success("已断开连接", conn.destination || conn.id);
    } catch (err) {
      toast.error("断开连接失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setClosingId(null);
    }
  };

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    const list = q
      ? connections.filter((c) => {
          const hay = [
            c.inbound,
            c.user,
            c.network,
            c.source,
            c.destination,
            c.rule,
            ...(c.chains || []),
          ]
            .join(" ")
            .toLowerCase();
          return hay.includes(q);
        })
      : connections;

    return [...list].sort(
      (a, b) => b.download + b.upload - (a.download + a.upload),
    );
  }, [connections, search]);

  const totals = useMemo(() => {
    let up = 0;
    let down = 0;
    const users = new Set<string>();
    for (const c of filtered) {
      up += c.upload || 0;
      down += c.download || 0;
      if (c.user) users.add(c.user);
    }
    return { up, down, users: users.size };
  }, [filtered]);

  return (
    <div className="space-y-4">
      {/* Compact stats summary bar */}
      <div className="grid divide-y divide-hairline overflow-hidden rounded-lg border border-hairline bg-surface sm:grid-cols-4 sm:divide-x sm:divide-y-0">
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">匹配活跃连接</div>
          <div className="mt-1 font-mono text-lg font-semibold text-ink tabular-nums">
            {filtered.length}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">活跃认证成员</div>
          <div className="mt-1 font-mono text-lg font-semibold text-ink tabular-nums">
            {totals.users}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">连接累计上行</div>
          <div className="mt-1 font-mono text-lg font-semibold text-uplink tabular-nums">
            {formatBytes(totals.up)}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">连接累计下行</div>
          <div className="mt-1 font-mono text-lg font-semibold text-accent tabular-nums">
            {formatBytes(totals.down)}
          </div>
        </div>
      </div>

      <Panel
        title="实时活跃连接列表"
        description="每 2 秒自动轮询 Clash API 连接快照，支持按成员、入站、目标或链路检索与一键踢断"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-56">
              <Search className="pointer-events-none absolute left-2.5 top-2.5 h-3.5 w-3.5 text-ink-muted" />
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="筛选入站 / 成员 / 目标地址..."
                className="h-8 pl-8 text-xs"
              />
            </div>

            <Button
              variant={paused ? "primary" : "secondary"}
              size="sm"
              onClick={() => setPaused((p) => !p)}
            >
              {paused ? <Play className="h-3.5 w-3.5" /> : <Pause className="h-3.5 w-3.5" />}
              {paused ? "恢复轮询" : "暂停轮询"}
            </Button>

            <Button
              variant="outline"
              size="sm"
              loading={loading}
              onClick={() => void fetchConnections(false)}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              刷新
            </Button>
          </div>
        }
        bodyClassName="p-0"
      >
        {error && (
          <div className="border-b border-hairline bg-warn/10 px-4 py-2.5 text-xs text-warn">
            {error}
          </div>
        )}

        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="border-b border-hairline bg-recessed text-ink-secondary">
              <tr>
                <th className="px-4 py-2.5 font-medium">目标地址 / 来源</th>
                <th className="px-3 py-2.5 font-medium">入站与成员</th>
                <th className="px-3 py-2.5 font-medium">出站链路与规则</th>
                <th className="px-3 py-2.5 text-right font-medium">上行 / 下行</th>
                <th className="px-3 py-2.5 text-right font-medium">时长</th>
                <th className="px-4 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {loading && connections.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-12 text-center text-ink-muted">
                    正在加载连接列表...
                  </td>
                </tr>
              ) : filtered.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-12 text-center text-ink-muted">
                    {connections.length === 0
                      ? "当前没有活跃代理连接"
                      : "没有匹配当前筛选条件的连接"}
                  </td>
                </tr>
              ) : (
                filtered.map((conn) => (
                  <tr key={conn.id} className="hover:bg-surface-hover/60">
                    <td className="px-4 py-2.5">
                      <div className="flex items-center gap-2">
                        <Badge tone={conn.network?.toLowerCase() === "udp" ? "uplink" : "accent"}>
                          {(conn.network || "tcp").toUpperCase()}
                        </Badge>
                        <span
                          className="max-w-[260px] truncate font-mono font-medium text-ink"
                          title={conn.destination}
                        >
                          {conn.destination || "-"}
                        </span>
                      </div>
                      {conn.source && (
                        <div className="mt-1 font-mono text-[11px] text-ink-muted">
                          来源: {conn.source}
                        </div>
                      )}
                    </td>

                    <td className="px-3 py-2.5">
                      <div className="font-mono text-ink">{conn.inbound || "-"}</div>
                      <div className="mt-0.5 text-[11px] text-ink-secondary">
                        {conn.user ? `成员: ${conn.user}` : "未绑定成员"}
                      </div>
                    </td>

                    <td className="px-3 py-2.5">
                      <div className="flex flex-wrap items-center gap-1">
                        {conn.chains && conn.chains.length > 0 ? (
                          conn.chains.map((hop, idx) => (
                            <Badge key={`${hop}-${idx}`} tone="neutral">
                              {hop}
                            </Badge>
                          ))
                        ) : (
                          <span className="text-ink-muted">-</span>
                        )}
                      </div>
                      {conn.rule && (
                        <div className="mt-1 truncate font-mono text-[11px] text-ink-muted">
                          {conn.rule}
                        </div>
                      )}
                    </td>

                    <td className="px-3 py-2.5 text-right font-mono tabular-nums">
                      <div className="text-uplink">↑ {formatBytes(conn.upload)}</div>
                      <div className="text-accent">↓ {formatBytes(conn.download)}</div>
                    </td>

                    <td className="px-3 py-2.5 text-right font-mono text-ink-secondary tabular-nums">
                      {formatRelativeAge(conn.start)}
                    </td>

                    <td className="px-4 py-2.5 text-right">
                      <Button
                        variant="danger"
                        size="xs"
                        loading={closingId === conn.id}
                        disabled={closingId !== null}
                        onClick={() => void handleClose(conn)}
                      >
                        <Unplug className="h-3 w-3" />
                        断开
                      </Button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </Panel>
    </div>
  );
}
