import { useCallback, useEffect, useMemo, useState } from "react";
import { BarChart3, Radio, RefreshCw, RotateCcw, Server, Users } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import { formatBytes } from "@/lib/format";
import { useNode } from "@/contexts/NodeContext";
import { useToast } from "@/components/ui/Toast";
import { Badge, Button, Panel } from "@/components/ui/primitives";
import type { TrafficRange, TrafficStatsResponse } from "@/types/api";

const RANGES: { id: TrafficRange; label: string }[] = [
  { id: "24h", label: "24 小时" },
  { id: "7d", label: "7 天" },
  { id: "30d", label: "30 天" },
];

function formatBucketLabel(iso: string, range: TrafficRange): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  if (range === "24h") {
    return d.toLocaleTimeString("zh-CN", { hour12: false, hour: "2-digit", minute: "2-digit" });
  }
  return `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

interface TrafficHistoryPanelProps {
  mode?: "cluster" | "node";
}

export function TrafficHistoryPanel({ mode }: TrafficHistoryPanelProps = {}) {
  const { activeNodeId, activeNode, nodes } = useNode();
  const toast = useToast();
  const [range, setRange] = useState<TrafficRange>("24h");
  const [scopeAllNodes, setScopeAllNodes] = useState(mode !== "node");
  const [stats, setStats] = useState<TrafficStatsResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [resetting, setResetting] = useState(false);
  const [confirmReset, setConfirmReset] = useState(false);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  const effectiveScopeAll =
    mode === "cluster" ? true : mode === "node" ? false : scopeAllNodes;
  const queryNodeId = effectiveScopeAll ? undefined : activeNodeId;

  const loadStats = useCallback(
    async (r: TrafficRange, nodeId?: number) => {
      setLoading(true);
      try {
        const res = await api.getTrafficStats({ range: r, node_id: nodeId });
        setStats(res);
      } catch (err) {
        toast.error("加载历史流量统计失败", err instanceof ApiError ? err.message : undefined);
      } finally {
        setLoading(false);
      }
    },
    [toast],
  );

  useEffect(() => {
    void loadStats(range, queryNodeId);
  }, [range, queryNodeId, loadStats]);

  const handleResetAll = async () => {
    setResetting(true);
    try {
      await api.resetTraffic({ scope: "all" });
      setConfirmReset(false);
      toast.success("已重置全部流量统计");
      await loadStats(range, queryNodeId);
    } catch (err) {
      toast.error("重置流量失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setResetting(false);
    }
  };

  const series = stats?.series ?? [];
  const byMember = stats?.by_member ?? [];
  const byInbound = stats?.by_inbound ?? [];
  const byNode = stats?.by_node ?? [];

  const { maxBucket, sumUp, sumDown } = useMemo(() => {
    let maxVal = 1;
    let up = 0;
    let down = 0;
    for (const b of series) {
      const u = Math.max(0, b.upload_bytes || 0);
      const d = Math.max(0, b.download_bytes || 0);
      up += u;
      down += d;
      if (u + d > maxVal) maxVal = u + d;
    }
    return { maxBucket: maxVal, sumUp: up, sumDown: down };
  }, [series]);

  const topMemberMax = useMemo(
    () =>
      Math.max(
        1,
        ...byMember.map((m) => (m.upload_bytes || 0) + (m.download_bytes || 0)),
      ),
    [byMember],
  );

  const topInboundMax = useMemo(
    () =>
      Math.max(
        1,
        ...byInbound.map((ib) => (ib.upload_bytes || 0) + (ib.download_bytes || 0)),
      ),
    [byInbound],
  );

  const topNodeMax = useMemo(
    () =>
      Math.max(
        1,
        ...byNode.map((n) => (n.upload_bytes || 0) + (n.download_bytes || 0)),
      ),
    [byNode],
  );

  const activeBucket =
    hoverIdx !== null && series[hoverIdx] ? series[hoverIdx] : null;

  const showNodeBreakdown = mode !== "node" && byNode.length > 0;

  return (
    <Panel
      title={
        mode === "node" && activeNode
          ? `节点历史流量 · ${activeNode.name}`
          : "集群历史流量归档与排行"
      }
      description={
        mode === "node"
          ? "当前节点 Clash API 上报并归档的历史流量趋势与成员/入站排行"
          : "由各节点 Clash API 连接追踪器聚合上报并落盘主控 SQLite，支持按节点、成员与入站多维统计"
      }
      actions={
        <div className="flex flex-wrap items-center gap-2">
          {!mode && nodes.length > 1 && (
            <div className="inline-flex rounded-md border border-hairline bg-recessed p-0.5 text-xs">
              <button
                type="button"
                onClick={() => setScopeAllNodes(true)}
                className={cn(
                  "rounded px-2 py-1 transition-colors",
                  scopeAllNodes
                    ? "bg-surface font-medium text-accent"
                    : "text-ink-muted hover:text-ink",
                )}
              >
                全集群
              </button>
              <button
                type="button"
                onClick={() => setScopeAllNodes(false)}
                className={cn(
                  "max-w-[120px] truncate rounded px-2 py-1 font-mono transition-colors",
                  !scopeAllNodes
                    ? "bg-surface font-medium text-accent"
                    : "text-ink-muted hover:text-ink",
                )}
              >
                {activeNode?.name || "当前节点"}
              </button>
            </div>
          )}

          <div className="inline-flex rounded-md border border-hairline bg-recessed p-0.5 text-xs">
            {RANGES.map((item) => (
              <button
                key={item.id}
                type="button"
                onClick={() => setRange(item.id)}
                className={cn(
                  "rounded px-2.5 py-1 transition-colors",
                  range === item.id
                    ? "bg-surface font-medium text-accent"
                    : "text-ink-muted hover:text-ink",
                )}
              >
                {item.label}
              </button>
            ))}
          </div>

          <Button
            variant="ghost"
            size="xs"
            loading={loading}
            onClick={() => void loadStats(range, queryNodeId)}
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>

          {confirmReset ? (
            <div className="inline-flex items-center gap-1 rounded border border-danger/45 bg-danger/10 px-2 py-0.5">
              <span className="text-[11px] text-danger">清空所有历史流量？</span>
              <Button
                variant="danger"
                size="xs"
                loading={resetting}
                onClick={() => void handleResetAll()}
              >
                确认重置
              </Button>
              <Button
                variant="ghost"
                size="xs"
                onClick={() => setConfirmReset(false)}
              >
                取消
              </Button>
            </div>
          ) : (
            <Button
              variant="outline"
              size="xs"
              onClick={() => setConfirmReset(true)}
            >
              <RotateCcw className="h-3 w-3" />
              重置统计
            </Button>
          )}
        </div>
      }
    >
      <div className="space-y-5">
        {/* Stacked bar trend chart */}
        <div>
          <div className="mb-2.5 flex flex-wrap items-center justify-between gap-2 text-xs">
            <div className="flex items-center gap-4 font-mono tabular-nums">
              <span className="flex items-center gap-1.5 text-ink">
                <span className="h-2 w-2 rounded-sm bg-accent" />
                区间下行: {formatBytes(sumDown)}
              </span>
              <span className="flex items-center gap-1.5 text-ink">
                <span className="h-2 w-2 rounded-sm bg-uplink" />
                区间上行: {formatBytes(sumUp)}
              </span>
            </div>

            {activeBucket ? (
              <div className="rounded border border-hairline bg-recessed px-2.5 py-0.5 font-mono text-[11px] text-ink tabular-nums">
                {formatBucketLabel(activeBucket.bucket_time, range)} · ↓{" "}
                {formatBytes(activeBucket.download_bytes)} / ↑{" "}
                {formatBytes(activeBucket.upload_bytes)}
              </div>
            ) : (
              <span className="text-[11px] text-ink-muted">
                悬停柱条查看单时段明细
              </span>
            )}
          </div>

          <div className="rounded-md border border-hairline bg-recessed px-3 pb-2 pt-3">
            {series.length === 0 ? (
              <div className="flex h-36 flex-col items-center justify-center text-xs text-ink-muted">
                <BarChart3 className="mb-1.5 h-5 w-5 opacity-60" />
                <span>当前时间范围内暂无归档流量数据</span>
              </div>
            ) : (
              <>
                <div className="flex h-36 items-end gap-1">
                  {series.map((bucket, idx) => {
                    const down = Math.max(0, bucket.download_bytes || 0);
                    const up = Math.max(0, bucket.upload_bytes || 0);
                    const total = down + up;
                    const heightPct = total > 0 ? Math.max(3, (total / maxBucket) * 100) : 0;
                    const upRatio = total > 0 ? (up / total) * 100 : 0;
                    const downRatio = 100 - upRatio;

                    return (
                      <div
                        key={`${bucket.bucket_time}-${idx}`}
                        onMouseEnter={() => setHoverIdx(idx)}
                        onMouseLeave={() => setHoverIdx(null)}
                        className="group relative flex h-full flex-1 flex-col justify-end"
                      >
                        <div
                          className={cn(
                            "flex w-full flex-col overflow-hidden rounded-t-[2px] transition-opacity",
                            hoverIdx !== null && hoverIdx !== idx && "opacity-45",
                          )}
                          style={{ height: `${heightPct}%` }}
                        >
                          {up > 0 && (
                            <div
                              className="w-full bg-uplink"
                              style={{ height: `${upRatio}%` }}
                            />
                          )}
                          {down > 0 && (
                            <div
                              className="w-full bg-accent"
                              style={{ height: `${downRatio}%` }}
                            />
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
                <div className="mt-1.5 flex items-center justify-between border-t border-hairline pt-1 font-mono text-[10px] text-ink-muted">
                  <span>{formatBucketLabel(series[0]?.bucket_time, range)}</span>
                  {series.length > 2 && (
                    <span>
                      {formatBucketLabel(
                        series[Math.floor(series.length / 2)]?.bucket_time,
                        range,
                      )}
                    </span>
                  )}
                  <span>
                    {formatBucketLabel(series[series.length - 1]?.bucket_time, range)}
                  </span>
                </div>
              </>
            )}
          </div>
        </div>

        {/* Breakdown: By Node + Top Members + Top Inbounds */}
        <div
          className={cn(
            "grid gap-4 border-t border-hairline pt-4",
            showNodeBreakdown ? "lg:grid-cols-3" : "lg:grid-cols-2",
          )}
        >
          {showNodeBreakdown && (
            <div className="space-y-2.5">
              <div className="flex items-center gap-1.5 text-xs font-medium text-ink">
                <Server className="h-3.5 w-3.5 text-ok" />
                <span>节点流量分布 (By Node)</span>
              </div>

              <div className="space-y-2">
                {byNode.slice(0, 6).map((row) => {
                  const total = (row.upload_bytes || 0) + (row.download_bytes || 0);
                  const pct = Math.min(100, Math.round((total / topNodeMax) * 100));
                  return (
                    <div
                      key={row.id || row.name}
                      className="rounded border border-hairline bg-recessed/60 px-3 py-2 text-xs"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex min-w-0 items-center gap-1.5">
                          <span className="truncate font-mono font-medium text-ink">
                            {row.name}
                          </span>
                          <Badge tone={row.is_local ? "accent" : "neutral"}>
                            {row.is_local ? "本机" : "远端"}
                          </Badge>
                        </div>
                        <span className="shrink-0 font-mono text-[11px] tabular-nums">
                          <span className="text-uplink">↑ {formatBytes(row.upload_bytes)}</span>
                          <span className="mx-1 text-ink-muted">/</span>
                          <span className="text-accent">↓ {formatBytes(row.download_bytes)}</span>
                        </span>
                      </div>
                      <div className="mt-1.5 h-1 w-full overflow-hidden rounded-full bg-hairline">
                        <div
                          className="h-full rounded-full bg-ok"
                          style={{ width: `${total > 0 ? Math.max(3, pct) : 0}%` }}
                        />
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {/* Top Members */}
          <div className="space-y-2.5">
            <div className="flex items-center gap-1.5 text-xs font-medium text-ink">
              <Users className="h-3.5 w-3.5 text-accent" />
              <span>成员流量排行 (Top Members)</span>
            </div>

            {byMember.length === 0 ? (
              <div className="rounded-md border border-hairline bg-recessed px-3 py-5 text-center text-xs text-ink-muted">
                暂无成员流量记录
              </div>
            ) : (
              <div className="space-y-2">
                {byMember.slice(0, 6).map((row) => {
                  const total = (row.upload_bytes || 0) + (row.download_bytes || 0);
                  const pct = Math.min(100, Math.round((total / topMemberMax) * 100));
                  return (
                    <div
                      key={row.id || row.name}
                      className="rounded border border-hairline bg-recessed/60 px-3 py-2 text-xs"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="truncate font-mono font-medium text-ink">
                          {row.name}
                        </span>
                        <span className="shrink-0 font-mono text-[11px] tabular-nums">
                          <span className="text-uplink">↑ {formatBytes(row.upload_bytes)}</span>
                          <span className="mx-1 text-ink-muted">/</span>
                          <span className="text-accent">↓ {formatBytes(row.download_bytes)}</span>
                        </span>
                      </div>
                      <div className="mt-1.5 h-1 w-full overflow-hidden rounded-full bg-hairline">
                        <div
                          className="h-full rounded-full bg-accent"
                          style={{ width: `${total > 0 ? Math.max(3, pct) : 0}%` }}
                        />
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>

          {/* Top Inbounds */}
          <div className="space-y-2.5">
            <div className="flex items-center gap-1.5 text-xs font-medium text-ink">
              <Radio className="h-3.5 w-3.5 text-uplink" />
              <span>入站节点流量排行 (Top Inbounds)</span>
            </div>

            {byInbound.length === 0 ? (
              <div className="rounded-md border border-hairline bg-recessed px-3 py-5 text-center text-xs text-ink-muted">
                暂无入站流量记录
              </div>
            ) : (
              <div className="space-y-2">
                {byInbound.slice(0, 6).map((row) => {
                  const total = (row.upload_bytes || 0) + (row.download_bytes || 0);
                  const pct = Math.min(100, Math.round((total / topInboundMax) * 100));
                  return (
                    <div
                      key={row.id || row.tag}
                      className="rounded border border-hairline bg-recessed/60 px-3 py-2 text-xs"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex min-w-0 items-center gap-1.5">
                          <span className="truncate font-medium text-ink">
                            {row.name || row.tag}
                          </span>
                          {row.protocol && (
                            <Badge tone="neutral">{row.protocol.toUpperCase()}</Badge>
                          )}
                        </div>
                        <span className="shrink-0 font-mono text-[11px] tabular-nums">
                          <span className="text-uplink">↑ {formatBytes(row.upload_bytes)}</span>
                          <span className="mx-1 text-ink-muted">/</span>
                          <span className="text-accent">↓ {formatBytes(row.download_bytes)}</span>
                        </span>
                      </div>
                      <div className="mt-1.5 h-1 w-full overflow-hidden rounded-full bg-hairline">
                        <div
                          className="h-full rounded-full bg-uplink"
                          style={{ width: `${total > 0 ? Math.max(3, pct) : 0}%` }}
                        />
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      </div>
    </Panel>
  );
}
