import React, { useCallback, useEffect, useState } from "react";
import {
  AlertTriangle,
  Check,
  Edit3,
  Gauge,
  Globe,
  Layers,
  Plus,
  RefreshCw,
  Server,
  Trash2,
  Zap,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
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
  Switch,
} from "@/components/ui/primitives";
import type { ProxyGroupInfo, UpstreamSubItem } from "@/types/api";

const DEFAULT_TEST_URL = "https://www.gstatic.com/generate_204";

const FORMAT_LABELS: Record<string, { label: string; tone: "accent" | "ok" | "warn" | "neutral" }> = {
  singbox: { label: "sing-box JSON", tone: "accent" },
  clash: { label: "Clash YAML", tone: "ok" },
  uri: { label: "URI / Base64", tone: "warn" },
};

function latencyBadge(delay?: number): { label: string; tone: "ok" | "warn" | "uplink" | "danger" | "neutral" } {
  if (delay === undefined || delay === null || delay === 0) {
    return { label: "-", tone: "neutral" };
  }
  if (delay < 0) {
    return { label: "超时", tone: "danger" };
  }
  if (delay < 150) {
    return { label: `${delay} ms`, tone: "ok" };
  }
  if (delay < 400) {
    return { label: `${delay} ms`, tone: "warn" };
  }
  return { label: `${delay} ms`, tone: "uplink" };
}

interface UpstreamsProxiesTabProps {
  onOutboundsChanged?: () => void;
}

export function UpstreamsProxiesTab({ onOutboundsChanged }: UpstreamsProxiesTabProps) {
  const { activeNodeId, activeNode } = useNode();
  const toast = useToast();
  const nodeOffline = activeNode ? !activeNode.online : false;

  // Upstreams state
  const [upstreams, setUpstreams] = useState<UpstreamSubItem[]>([]);
  const [loadingSubs, setLoadingSubs] = useState(true);
  const [syncingId, setSyncingId] = useState<number | null>(null);
  const [busySubId, setBusySubId] = useState<number | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);

  // Add/Edit Upstream modal
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<UpstreamSubItem | null>(null);
  const [name, setName] = useState("");
  const [tag, setTag] = useState("");
  const [url, setUrl] = useState("");
  const [isGlobal, setIsGlobal] = useState(true);
  const [intervalMin, setIntervalMin] = useState("360");
  const [includeRegex, setIncludeRegex] = useState("");
  const [excludeRegex, setExcludeRegex] = useState("");
  const [enableUrltest, setEnableUrltest] = useState(true);
  const [testUrl, setTestUrl] = useState(DEFAULT_TEST_URL);
  const [proxyUrl, setProxyUrl] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [fetchNow, setFetchNow] = useState(true);
  const [saving, setSaving] = useState(false);

  // Live Proxy Groups state
  const [groups, setGroups] = useState<ProxyGroupInfo[]>([]);
  const [loadingProxies, setLoadingProxies] = useState(false);
  const [proxyError, setProxyError] = useState<string | null>(null);
  const [delayTestUrl, setDelayTestUrl] = useState(DEFAULT_TEST_URL);
  const [delays, setDelays] = useState<Record<string, number>>({});
  const [testingProxies, setTestingProxies] = useState<Record<string, boolean>>({});
  const [batchTestingGroup, setBatchTestingGroup] = useState<string | null>(null);
  const [selectingKey, setSelectingKey] = useState<string | null>(null);

  const loadUpstreams = useCallback(async () => {
    setLoadingSubs(true);
    try {
      const res = await api.listUpstreams(activeNodeId);
      setUpstreams(Array.isArray(res.upstreams) ? res.upstreams : []);
    } catch (err) {
      toast.error("加载上游订阅失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setLoadingSubs(false);
    }
  }, [activeNodeId, toast]);

  const loadProxyGroups = useCallback(
    async (silent = false) => {
      if (nodeOffline) {
        setGroups([]);
        setProxyError(null);
        return;
      }
      if (!silent) setLoadingProxies(true);
      try {
        const res = await api.listProxyGroups(activeNodeId);
        const list = Array.isArray(res.groups) ? res.groups : [];
        setGroups(list);
        setProxyError(null);
        setDelays((prev) => {
          const next = { ...prev };
          for (const g of list) {
            if (g.delays) {
              for (const [k, v] of Object.entries(g.delays)) {
                if (v > 0) next[k] = v;
              }
            }
          }
          return next;
        });
      } catch (err) {
        setGroups([]);
        setProxyError(
          err instanceof ApiError
            ? err.message
            : "无法读取 Clash API 策略组（请确认目标节点 sing-box 正在运行）",
        );
      } finally {
        setLoadingProxies(false);
      }
    },
    [activeNodeId, nodeOffline],
  );

  useEffect(() => {
    void loadUpstreams();
    setDelays({});
    void loadProxyGroups(false);
  }, [loadUpstreams, loadProxyGroups]);

  const openCreateModal = () => {
    setEditing(null);
    setName("");
    setTag("");
    setUrl("");
    setIsGlobal(true);
    setIntervalMin("360");
    setIncludeRegex("");
    setExcludeRegex("");
    setEnableUrltest(true);
    setTestUrl(DEFAULT_TEST_URL);
    setProxyUrl("");
    setEnabled(true);
    setFetchNow(true);
    setModalOpen(true);
  };

  const openEditModal = (sub: UpstreamSubItem) => {
    setEditing(sub);
    setName(sub.name);
    setTag(sub.tag);
    setUrl(sub.url);
    setIsGlobal(sub.node_id === 0);
    setIntervalMin(String(sub.interval_minutes ?? 360));
    setIncludeRegex(sub.include_regex || "");
    setExcludeRegex(sub.exclude_regex || "");
    setEnableUrltest(sub.enable_urltest !== false);
    setTestUrl(sub.test_url || DEFAULT_TEST_URL);
    setProxyUrl(sub.proxy_url || "");
    setEnabled(sub.enabled);
    setFetchNow(false);
    setModalOpen(true);
  };

  const handleSaveSub = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || !url.trim()) {
      toast.error("请填写订阅名称和订阅 URL");
      return;
    }
    const parsedInterval = Number.parseInt(intervalMin, 10);
    const payload = {
      node_id: isGlobal ? 0 : activeNodeId,
      name: name.trim(),
      tag: tag.trim() || undefined,
      url: url.trim(),
      enabled,
      interval_minutes: Number.isFinite(parsedInterval) && parsedInterval >= 0 ? parsedInterval : 360,
      include_regex: includeRegex.trim(),
      exclude_regex: excludeRegex.trim(),
      enable_urltest: enableUrltest,
      test_url: testUrl.trim() || DEFAULT_TEST_URL,
      proxy_url: proxyUrl.trim(),
      fetch_now: editing ? undefined : fetchNow,
    };

    setSaving(true);
    try {
      if (editing) {
        await api.updateUpstream(editing.id, payload);
        toast.success("上游订阅已更新", name.trim());
      } else {
        const created = await api.createUpstream(payload);
        if (created.last_error) {
          toast.info("订阅已保存，但首次拉取遇到错误", created.last_error);
        } else {
          toast.success(
            "上游订阅已添加",
            created.node_count ? `解析出 ${created.node_count} 个出站节点` : name.trim(),
          );
        }
      }
      setModalOpen(false);
      await loadUpstreams();
      onOutboundsChanged?.();
      window.setTimeout(() => void loadProxyGroups(true), 800);
    } catch (err) {
      toast.error(
        editing ? "更新上游订阅失败" : "添加上游订阅失败",
        err instanceof ApiError ? err.message : undefined,
      );
    } finally {
      setSaving(false);
    }
  };

  const handleToggleSub = async (sub: UpstreamSubItem) => {
    setBusySubId(sub.id);
    try {
      await api.updateUpstream(sub.id, {
        node_id: sub.node_id,
        name: sub.name,
        tag: sub.tag,
        url: sub.url,
        enabled: !sub.enabled,
        interval_minutes: sub.interval_minutes,
        include_regex: sub.include_regex,
        exclude_regex: sub.exclude_regex,
        enable_urltest: sub.enable_urltest,
        test_url: sub.test_url,
        proxy_url: sub.proxy_url,
      });
      toast.success(!sub.enabled ? `已启用订阅 ${sub.name}` : `已停用订阅 ${sub.name}`);
      await loadUpstreams();
      onOutboundsChanged?.();
      window.setTimeout(() => void loadProxyGroups(true), 700);
    } catch (err) {
      toast.error("切换订阅状态失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusySubId(null);
    }
  };

  const handleSyncSub = async (sub: UpstreamSubItem) => {
    setSyncingId(sub.id);
    try {
      const res = await api.syncUpstream(sub.id);
      const updated = res.upstream;
      if (updated?.last_error) {
        toast.error("同步订阅失败", updated.last_error);
      } else {
        toast.success(
          `同步完成：${sub.name}`,
          `共解析 ${updated?.node_count ?? 0} 个节点（${updated?.cached_format || "auto"}）`,
        );
      }
      await loadUpstreams();
      onOutboundsChanged?.();
      window.setTimeout(() => void loadProxyGroups(true), 800);
    } catch (err) {
      toast.error("同步订阅失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setSyncingId(null);
    }
  };

  const handleDeleteSub = async (sub: UpstreamSubItem) => {
    setBusySubId(sub.id);
    try {
      await api.deleteUpstream(sub.id);
      setConfirmDeleteId(null);
      toast.success("已删除上游订阅", sub.name);
      await loadUpstreams();
      onOutboundsChanged?.();
      window.setTimeout(() => void loadProxyGroups(true), 700);
    } catch (err) {
      toast.error("删除订阅失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusySubId(null);
    }
  };

  const testSingleProxy = async (proxyName: string) => {
    setTestingProxies((prev) => ({ ...prev, [proxyName]: true }));
    try {
      const res = await api.testProxyDelay(
        { proxy: proxyName, test_url: delayTestUrl.trim() || DEFAULT_TEST_URL, timeout_ms: 5000 },
        activeNodeId,
      );
      setDelays((prev) => ({
        ...prev,
        [proxyName]: res.delay_ms > 0 ? res.delay_ms : -1,
      }));
    } catch {
      setDelays((prev) => ({ ...prev, [proxyName]: -1 }));
    } finally {
      setTestingProxies((prev) => ({ ...prev, [proxyName]: false }));
    }
  };

  const testAllInGroup = async (group: ProxyGroupInfo) => {
    if (!group.all?.length) return;
    setBatchTestingGroup(group.name);
    const queue = [...group.all];
    const concurrency = 4;

    const worker = async () => {
      while (queue.length > 0) {
        const next = queue.shift();
        if (!next) break;
        await testSingleProxy(next);
      }
    };

    try {
      await Promise.all(
        Array.from({ length: Math.min(concurrency, group.all.length) }, () => worker()),
      );
      toast.info(`已完成策略组 ${group.name} 测速`);
    } finally {
      setBatchTestingGroup(null);
    }
  };

  const handleSelectProxy = async (groupName: string, proxyName: string) => {
    const key = `${groupName}::${proxyName}`;
    setSelectingKey(key);
    try {
      await api.selectProxy({ group: groupName, proxy: proxyName }, activeNodeId);
      setGroups((prev) =>
        prev.map((g) => (g.name === groupName ? { ...g, now: proxyName } : g)),
      );
      toast.success(`已切换 ${groupName}`, `当前出口：${proxyName}`);
    } catch (err) {
      toast.error("切换节点失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setSelectingKey(null);
    }
  };

  return (
    <div className="space-y-5">
      {/* Upstream Subscriptions Panel */}
      <Panel
        title="外部上游订阅出站 (Upstream Subscriptions)"
        description="自适应解析 sing-box JSON / Clash YAML / Base64 URI 订阅，自动生成 Selector 与 URLTest 出站组"
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              loading={loadingSubs}
              onClick={() => void loadUpstreams()}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              刷新
            </Button>
            <Button variant="primary" size="sm" onClick={openCreateModal}>
              <Plus className="h-3.5 w-3.5" />
              添加上游订阅
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
                <th className="px-3 py-2.5 font-medium">作用范围与出站标签</th>
                <th className="px-3 py-2.5 font-medium">解析格式与节点数</th>
                <th className="px-3 py-2.5 font-medium">同步周期与状态</th>
                <th className="px-4 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {loadingSubs && upstreams.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-10 text-center text-ink-muted">
                    正在加载上游订阅列表...
                  </td>
                </tr>
              ) : upstreams.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-10 text-center">
                    <Layers className="mx-auto h-7 w-7 text-ink-muted" />
                    <p className="mt-2 text-xs text-ink-secondary">尚未添加外部上游订阅</p>
                    <p className="mt-1 text-[11px] text-ink-muted">
                      可添加机场或落地 VPS 订阅作为服务端落地出站，并在「可视化分流规则」中按规则分流。
                    </p>
                    <Button
                      variant="secondary"
                      size="sm"
                      className="mt-3"
                      onClick={openCreateModal}
                    >
                      <Plus className="h-3.5 w-3.5" />
                      添加首个上游订阅
                    </Button>
                  </td>
                </tr>
              ) : (
                upstreams.map((sub) => {
                  const fmt = FORMAT_LABELS[sub.cached_format] ?? null;
                  const isSyncing = syncingId === sub.id;
                  const isBusy = busySubId === sub.id;

                  return (
                    <tr
                      key={sub.id}
                      className={cn(
                        "hover:bg-surface-hover/60",
                        !sub.enabled && "opacity-65",
                      )}
                    >
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-3">
                          <Switch
                            checked={sub.enabled}
                            disabled={isBusy || isSyncing}
                            onChange={() => void handleToggleSub(sub)}
                            label={`切换订阅 ${sub.name}`}
                          />
                          <div className="min-w-0">
                            <div className="font-medium text-ink">{sub.name}</div>
                            <div
                              className="mt-0.5 max-w-[260px] truncate font-mono text-[11px] text-ink-muted"
                              title={sub.url}
                            >
                              {sub.url}
                            </div>
                          </div>
                        </div>
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <Badge tone={sub.node_id === 0 ? "accent" : "neutral"}>
                            {sub.node_id === 0 ? (
                              <>
                                <Globe className="h-3 w-3" />
                                全局共享
                              </>
                            ) : (
                              <>
                                <Server className="h-3 w-3" />
                                仅当前节点
                              </>
                            )}
                          </Badge>
                          <code className="rounded border border-hairline bg-recessed px-1.5 py-0.5 font-mono text-[11px] text-ink">
                            {sub.tag}
                          </code>
                          {sub.enable_urltest && (
                            <span
                              className="font-mono text-[10px] text-ink-muted"
                              title={`自动测速组: ${sub.tag}-auto`}
                            >
                              +{sub.tag}-auto
                            </span>
                          )}
                        </div>
                        {(sub.include_regex || sub.exclude_regex) && (
                          <div className="mt-1 flex flex-wrap gap-2 font-mono text-[10px] text-ink-muted">
                            {sub.include_regex && <span>含: /{sub.include_regex}/</span>}
                            {sub.exclude_regex && <span>排: /{sub.exclude_regex}/</span>}
                          </div>
                        )}
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex flex-wrap items-center gap-1.5">
                          {fmt ? (
                            <Badge tone={fmt.tone}>{fmt.label}</Badge>
                          ) : (
                            <Badge tone="neutral">待同步</Badge>
                          )}
                          <span className="font-mono font-semibold text-ink tabular-nums">
                            {sub.node_count} 节点
                          </span>
                        </div>
                      </td>

                      <td className="px-3 py-3">
                        <div className="font-mono text-[11px] text-ink-secondary tabular-nums">
                          {sub.interval_minutes > 0
                            ? `每 ${sub.interval_minutes} 分钟`
                            : "仅手动同步"}
                        </div>
                        <div className="mt-0.5 font-mono text-[10px] text-ink-muted tabular-nums">
                          {sub.last_fetched_at
                            ? `上次: ${formatDateTime(sub.last_fetched_at)}`
                            : "尚未同步"}
                        </div>
                        {sub.last_error && (
                          <div
                            className="mt-1 flex max-w-[240px] items-center gap-1 text-[11px] text-danger"
                            title={sub.last_error}
                          >
                            <AlertTriangle className="h-3 w-3 shrink-0" />
                            <span className="truncate">{sub.last_error}</span>
                          </div>
                        )}
                      </td>

                      <td className="px-4 py-3 text-right">
                        <div className="inline-flex flex-wrap items-center justify-end gap-1.5">
                          <Button
                            variant="secondary"
                            size="xs"
                            loading={isSyncing}
                            disabled={isBusy || isSyncing}
                            onClick={() => void handleSyncSub(sub)}
                          >
                            <RefreshCw className="h-3 w-3" />
                            立即同步
                          </Button>

                          <Button
                            variant="outline"
                            size="xs"
                            disabled={isBusy || isSyncing}
                            onClick={() => openEditModal(sub)}
                          >
                            <Edit3 className="h-3 w-3" />
                            编辑
                          </Button>

                          {confirmDeleteId === sub.id ? (
                            <div className="inline-flex items-center gap-1 rounded border border-danger/45 bg-danger/10 px-2 py-0.5">
                              <span className="text-[11px] text-danger">删除？</span>
                              <Button
                                variant="danger"
                                size="xs"
                                loading={isBusy}
                                onClick={() => void handleDeleteSub(sub)}
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
                              disabled={isBusy || isSyncing}
                              onClick={() => setConfirmDeleteId(sub.id)}
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

      {/* Live Proxy Groups (Clash API Selector & Delay Test) */}
      <Panel
        title="实时策略组测速与手动切换"
        description="直连目标节点 Clash API，实时测试每个落地节点延迟并在 Selector 策略组内一键切换出口"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Input
              value={delayTestUrl}
              onChange={(e) => setDelayTestUrl(e.target.value)}
              disabled={nodeOffline}
              placeholder="测速 URL"
              className="h-8 w-56 font-mono text-xs"
            />
            <Button
              variant="outline"
              size="sm"
              disabled={nodeOffline}
              loading={loadingProxies}
              onClick={() => void loadProxyGroups(false)}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              刷新策略组
            </Button>
          </div>
        }
      >
        {nodeOffline ? (
          <div className="rounded-md border border-hairline bg-recessed py-10 text-center text-xs text-ink-muted">
            当前远程节点离线，无法访问 Clash API 实时策略组
          </div>
        ) : proxyError ? (
          <div className="rounded-md border border-warn/40 bg-warn/10 px-4 py-3 text-xs text-warn">
            {proxyError}
          </div>
        ) : groups.length === 0 ? (
          <div className="rounded-md border border-dashed border-hairline-strong bg-recessed py-10 text-center text-xs text-ink-muted">
            当前节点运行配置中暂无 Selector / URLTest 策略组。添加并同步上游订阅后即可在此实时测速与切节点。
          </div>
        ) : (
          <div className="space-y-4">
            {groups.map((group) => {
              const isSelector = group.type.toLowerCase() === "selector";
              const isBatching = batchTestingGroup === group.name;

              return (
                <div
                  key={group.name}
                  className="rounded-md border border-hairline bg-recessed/50 p-4"
                >
                  <div className="mb-3 flex flex-wrap items-center justify-between gap-2 border-b border-hairline pb-3">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-sm font-semibold text-ink">
                        {group.name}
                      </span>
                      <Badge tone={isSelector ? "accent" : "ok"}>{group.type}</Badge>
                      {group.now && (
                        <span className="inline-flex items-center gap-1 rounded border border-ok/35 bg-ok/10 px-2 py-0.5 font-mono text-[11px] text-ok">
                          <Check className="h-3 w-3" />
                          当前: {group.now}
                        </span>
                      )}
                      <span className="font-mono text-[11px] text-ink-muted tabular-nums">
                        ({group.all?.length ?? 0} 节点)
                      </span>
                    </div>

                    <Button
                      variant="secondary"
                      size="xs"
                      loading={isBatching}
                      disabled={isBatching || !group.all?.length}
                      onClick={() => void testAllInGroup(group)}
                    >
                      <Gauge className="h-3.5 w-3.5" />
                      测速全部
                    </Button>
                  </div>

                  <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                    {(group.all || []).map((proxyName) => {
                      const active = group.now === proxyName;
                      const delayVal =
                        delays[proxyName] !== undefined
                          ? delays[proxyName]
                          : group.delays?.[proxyName];
                      const badge = latencyBadge(delayVal);
                      const isPinging = Boolean(testingProxies[proxyName]);
                      const isSelecting = selectingKey === `${group.name}::${proxyName}`;

                      return (
                        <div
                          key={proxyName}
                          className={cn(
                            "flex flex-col justify-between gap-2 rounded-md border p-2.5 text-xs transition-colors",
                            active
                              ? "border-ok/60 bg-ok/10"
                              : "border-hairline bg-surface hover:border-hairline-strong",
                          )}
                        >
                          <div className="flex items-start justify-between gap-2">
                            <span
                              className="truncate font-mono font-medium text-ink"
                              title={proxyName}
                            >
                              {proxyName}
                            </span>
                            <Badge tone={badge.tone} className="shrink-0 font-mono text-[10px] tabular-nums">
                              {badge.label}
                            </Badge>
                          </div>

                          <div className="flex items-center justify-between gap-1.5 pt-1">
                            <Button
                              variant="ghost"
                              size="xs"
                              loading={isPinging}
                              onClick={() => void testSingleProxy(proxyName)}
                            >
                              <Zap className="h-3 w-3 text-accent" />
                              测速
                            </Button>

                            {isSelector ? (
                              active ? (
                                <span className="inline-flex items-center gap-1 px-2 text-[11px] font-medium text-ok">
                                  <Check className="h-3 w-3" />
                                  已选中
                                </span>
                              ) : (
                                <Button
                                  variant="secondary"
                                  size="xs"
                                  loading={isSelecting}
                                  disabled={selectingKey !== null}
                                  onClick={() => void handleSelectProxy(group.name, proxyName)}
                                >
                                  切换
                                </Button>
                              )
                            ) : (
                              <span className="text-[10px] text-ink-muted">
                                {active ? "自动优选当前" : "自动测速"}
                              </span>
                            )}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Panel>

      {/* Add/Edit Upstream Subscription Modal */}
      <Modal
        open={modalOpen}
        onClose={() => !saving && setModalOpen(false)}
        title={editing ? `编辑上游订阅：${editing.name}` : "添加外部上游订阅"}
        description="支持自动识别 Base64 URI、Clash (Mihomo) YAML 与 sing-box JSON 订阅链接"
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
              form="upstream-sub-form"
              variant="primary"
              size="sm"
              loading={saving}
            >
              {editing ? "保存修改" : "添加订阅"}
            </Button>
          </>
        }
      >
        <form id="upstream-sub-form" onSubmit={handleSaveSub} className="space-y-4">
          <div className="grid gap-3.5 sm:grid-cols-2">
            <Field label="订阅名称" htmlFor="up-name">
              <Input
                id="up-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="例如：日本落地机场"
                required
              />
            </Field>
            <Field
              label="出站策略组标签 (tag)"
              htmlFor="up-tag"
              hint="留空自动生成（如 sub-1）；路由规则将引用此标签"
            >
              <Input
                id="up-tag"
                value={tag}
                onChange={(e) => setTag(e.target.value)}
                placeholder="sub-jp"
                className="font-mono"
              />
            </Field>
          </div>

          <Field label="远程订阅地址 (URL)" htmlFor="up-url">
            <Input
              id="up-url"
              type="url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://example.com/subscribe?token=..."
              className="font-mono text-xs"
              required
            />
          </Field>

          {/* Scope selector */}
          <div className="rounded-md border border-hairline bg-recessed p-3">
            <div className="mb-2 text-xs font-medium text-ink">应用范围</div>
            <div className="grid gap-2 sm:grid-cols-2">
              <button
                type="button"
                onClick={() => setIsGlobal(true)}
                className={cn(
                  "rounded-md border px-3 py-2 text-left text-xs transition-colors",
                  isGlobal
                    ? "border-accent bg-accent-muted text-ink"
                    : "border-hairline bg-surface text-ink-secondary hover:text-ink",
                )}
              >
                <div className="font-medium">全局共享 (node_id = 0)</div>
                <div className="mt-0.5 text-[11px] text-ink-muted">
                  集群内全部节点均可引用此上游出站组
                </div>
              </button>
              <button
                type="button"
                onClick={() => setIsGlobal(false)}
                className={cn(
                  "rounded-md border px-3 py-2 text-left text-xs transition-colors",
                  !isGlobal
                    ? "border-accent bg-accent-muted text-ink"
                    : "border-hairline bg-surface text-ink-secondary hover:text-ink",
                )}
              >
                <div className="font-medium">
                  仅当前节点 ({activeNode?.name || `#${activeNodeId}`})
                </div>
                <div className="mt-0.5 text-[11px] text-ink-muted">
                  仅编译下发到当前选中的 VPS 节点
                </div>
              </button>
            </div>
          </div>

          <div className="grid gap-3.5 sm:grid-cols-2">
            <Field label="包含节点正则 (Include Regex)" htmlFor="up-inc" hint="可选，按节点名称过滤">
              <Input
                id="up-inc"
                value={includeRegex}
                onChange={(e) => setIncludeRegex(e.target.value)}
                placeholder="例如：日本|JP|新加坡|SG"
                className="font-mono text-xs"
              />
            </Field>
            <Field label="排除节点正则 (Exclude Regex)" htmlFor="up-exc" hint="可选，排除倍率/到期提示等">
              <Input
                id="up-exc"
                value={excludeRegex}
                onChange={(e) => setExcludeRegex(e.target.value)}
                placeholder="例如：剩余流量|套餐到期|官网"
                className="font-mono text-xs"
              />
            </Field>
          </div>

          <div className="grid gap-3.5 sm:grid-cols-3">
            <Field label="自动更新周期（分钟）" htmlFor="up-int" hint="0 表示仅手动同步">
              <Input
                id="up-int"
                type="number"
                min={0}
                value={intervalMin}
                onChange={(e) => setIntervalMin(e.target.value)}
                className="font-mono"
              />
            </Field>
            <Field label="URLTest 测速地址" htmlFor="up-testurl">
              <Input
                id="up-testurl"
                value={testUrl}
                onChange={(e) => setTestUrl(e.target.value)}
                className="font-mono text-xs"
              />
            </Field>
            <Field label="拉取订阅代理（可选）" htmlFor="up-proxy">
              <Input
                id="up-proxy"
                value={proxyUrl}
                onChange={(e) => setProxyUrl(e.target.value)}
                placeholder="http://127.0.0.1:7890"
                className="font-mono text-xs"
              />
            </Field>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-4 rounded-md border border-hairline bg-recessed px-3.5 py-2.5 text-xs">
            <label className="flex items-center gap-2">
              <Switch checked={enableUrltest} onChange={setEnableUrltest} label="启用自动测速择优组" />
              <span className="text-ink">同时生成 URLTest 自动测速择优组</span>
            </label>

            <label className="flex items-center gap-2">
              <Switch checked={enabled} onChange={setEnabled} label="启用该订阅" />
              <span className="text-ink">启用订阅</span>
            </label>

            {!editing && (
              <label className="flex items-center gap-2">
                <Switch checked={fetchNow} onChange={setFetchNow} label="保存后立即同步" />
                <span className="text-ink-secondary">立即拉取解析</span>
              </label>
            )}
          </div>
        </form>
      </Modal>
    </div>
  );
}
