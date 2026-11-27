import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  ArrowUpRight,
  Ban,
  CheckCircle2,
  Edit3,
  GitBranch,
  Plus,
  RefreshCw,
  ShieldAlert,
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
  Switch,
} from "@/components/ui/primitives";
import type { AppRoute } from "@/components/AppShell";
import type {
  InboundItem,
  MemberItem,
  RouteRuleAction,
  RouteRuleItem,
  RoutingPresetItem,
} from "@/types/api";

const BUILTIN_SRS_SUGGESTIONS = [
  "geosite-openai",
  "geosite-anthropic",
  "geosite-google-gemini",
  "geosite-netflix",
  "geosite-disney",
  "geosite-youtube",
  "geosite-telegram",
  "geoip-telegram",
  "geosite-category-ads-all",
  "geosite-geolocation-!cn",
  "geosite-cn",
  "geoip-cn",
];

function splitCommaOrLines(raw: string): string[] {
  return raw
    .split(/[\n,;]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

function splitPorts(raw: string): number[] {
  return raw
    .split(/[\n,;\s]+/)
    .map((s) => Number.parseInt(s.trim(), 10))
    .filter((n) => Number.isFinite(n) && n >= 1 && n <= 65535);
}

function buildMatcherChips(rule: RouteRuleItem): { label: string; tone: "accent" | "warn" | "uplink" | "neutral" }[] {
  const chips: { label: string; tone: "accent" | "warn" | "uplink" | "neutral" }[] = [];
  for (const rs of rule.rule_sets || []) chips.push({ label: `srs:${rs}`, tone: "accent" });
  for (const d of (rule.domains || []).slice(0, 3)) chips.push({ label: `domain:${d}`, tone: "neutral" });
  if ((rule.domains || []).length > 3) chips.push({ label: `+${rule.domains.length - 3} domain`, tone: "neutral" });
  for (const d of (rule.domain_suffix || []).slice(0, 3)) chips.push({ label: `*.${d}`, tone: "neutral" });
  if ((rule.domain_suffix || []).length > 3) chips.push({ label: `+${rule.domain_suffix.length - 3} suffix`, tone: "neutral" });
  for (const kw of (rule.domain_keyword || []).slice(0, 2)) chips.push({ label: `kw:${kw}`, tone: "neutral" });
  for (const ip of (rule.ip_cidr || []).slice(0, 2)) chips.push({ label: `ip:${ip}`, tone: "neutral" });
  if ((rule.ip_cidr || []).length > 2) chips.push({ label: `+${rule.ip_cidr.length - 2} cidr`, tone: "neutral" });
  for (const p of rule.protocol || []) chips.push({ label: `proto:${p}`, tone: "warn" });
  if ((rule.port || []).length > 0) chips.push({ label: `port:${rule.port.join(",")}`, tone: "warn" });
  for (const u of rule.auth_user || []) chips.push({ label: `user:${u}`, tone: "uplink" });
  for (const ib of rule.inbound || []) chips.push({ label: `in:${ib}`, tone: "uplink" });
  if (rule.ip_is_private) chips.push({ label: "ip_is_private", tone: "warn" });
  return chips;
}

interface VisualRoutingTabProps {
  refreshSignal?: number;
  onNavigate?: (route: AppRoute) => void;
}

export function VisualRoutingTab({ refreshSignal = 0, onNavigate }: VisualRoutingTabProps) {
  const { activeNodeId, activeNode } = useNode();
  const toast = useToast();

  const [presets, setPresets] = useState<RoutingPresetItem[]>([]);
  const [rules, setRules] = useState<RouteRuleItem[]>([]);
  const [defaultOutbound, setDefaultOutbound] = useState("direct");
  const [availableOutbounds, setAvailableOutbounds] = useState<string[]>(["direct"]);
  const [members, setMembers] = useState<MemberItem[]>([]);
  const [inbounds, setInbounds] = useState<InboundItem[]>([]);

  const [loading, setLoading] = useState(true);
  const [savingDefault, setSavingDefault] = useState(false);
  const [presetTargetMap, setPresetTargetMap] = useState<Record<string, string>>({});
  const [applyingPresetKey, setApplyingPresetKey] = useState<string | null>(null);
  const [busyRuleId, setBusyRuleId] = useState<number | null>(null);
  const [reordering, setReordering] = useState(false);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);

  // Add/Edit Custom Rule Modal
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<RouteRuleItem | null>(null);
  const [ruleName, setRuleName] = useState("");
  const [action, setAction] = useState<RouteRuleAction>("route");
  const [outboundTag, setOutboundTag] = useState("direct");
  const [ruleEnabled, setRuleEnabled] = useState(true);
  const [ruleSets, setRuleSets] = useState<string[]>([]);
  const [customRuleSetInput, setCustomRuleSetInput] = useState("");
  const [domainsText, setDomainsText] = useState("");
  const [suffixText, setSuffixText] = useState("");
  const [keywordText, setKeywordText] = useState("");
  const [ipCidrText, setIpCidrText] = useState("");
  const [protocolsText, setProtocolsText] = useState("");
  const [portsText, setPortsText] = useState("");
  const [authUsers, setAuthUsers] = useState<string[]>([]);
  const [inboundTags, setInboundTags] = useState<string[]>([]);
  const [ipIsPrivate, setIpIsPrivate] = useState(false);
  const [savingRule, setSavingRule] = useState(false);

  const loadAll = useCallback(async () => {
    setLoading(true);
    try {
      const [presetsRes, rulesRes, memRes, ibRes] = await Promise.all([
        api.listRoutingPresets(),
        api.listRoutingRules(activeNodeId),
        api.listMembers().catch(() => ({ members: [] })),
        api.listInbounds(activeNodeId).catch(() => ({ inbounds: [] })),
      ]);

      const presetList = Array.isArray(presetsRes.presets) ? presetsRes.presets : [];
      const ruleList = Array.isArray(rulesRes.rules) ? rulesRes.rules : [];
      const avail =
        Array.isArray(rulesRes.available_outbounds) && rulesRes.available_outbounds.length > 0
          ? rulesRes.available_outbounds
          : ["direct"];

      setPresets(presetList);
      setRules(ruleList);
      setDefaultOutbound(rulesRes.default_outbound || "direct");
      setAvailableOutbounds(avail);
      setMembers(Array.isArray(memRes.members) ? memRes.members : []);
      setInbounds(Array.isArray(ibRes.inbounds) ? ibRes.inbounds : []);

      // Smart default selection for unlock presets
      const preferredUnlockTarget =
        avail.find((t) => t === "warp") ||
        avail.find((t) => t !== "direct") ||
        "direct";

      setPresetTargetMap((prev) => {
        const next = { ...prev };
        for (const p of presetList) {
          if (!p.needs_outbound) continue;
          const existingRule = ruleList.find((r) => r.preset_key === p.key);
          if (existingRule?.outbound_tag && avail.includes(existingRule.outbound_tag)) {
            next[p.key] = existingRule.outbound_tag;
          } else if (!next[p.key] || !avail.includes(next[p.key])) {
            next[p.key] = preferredUnlockTarget;
          }
        }
        return next;
      });
    } catch (err) {
      toast.error("加载分流规则失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setLoading(false);
    }
  }, [activeNodeId, toast]);

  useEffect(() => {
    void loadAll();
  }, [loadAll, refreshSignal]);

  const activePresetByKey = useMemo(() => {
    const map = new Map<string, RouteRuleItem>();
    for (const r of rules) {
      if (r.preset_key) map.set(r.preset_key, r);
    }
    return map;
  }, [rules]);

  const handleSaveDefaultOutbound = async (nextTag: string) => {
    setDefaultOutbound(nextTag);
    setSavingDefault(true);
    try {
      const res = await api.setDefaultOutbound({
        node_id: activeNodeId,
        default_outbound: nextTag,
      });
      setDefaultOutbound(res.default_outbound || nextTag);
      toast.success("已更新默认出站 (route.final)", `未命中规则的流量将走 ${nextTag}`);
    } catch (err) {
      toast.error("更新默认出站失败", err instanceof ApiError ? err.message : undefined);
      await loadAll();
    } finally {
      setSavingDefault(false);
    }
  };

  const handleApplyPreset = async (preset: RoutingPresetItem) => {
    setApplyingPresetKey(preset.key);
    try {
      const target = preset.needs_outbound
        ? presetTargetMap[preset.key] || availableOutbounds[0] || "direct"
        : undefined;
      await api.applyRoutingPreset({
        node_id: activeNodeId,
        preset_key: preset.key,
        outbound_tag: target,
      });
      toast.success(
        `已应用预设：${preset.name}`,
        target ? `目标出站：${target}` : "动作：REJECT 拦截",
      );
      await loadAll();
    } catch (err) {
      toast.error("应用分流预设失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setApplyingPresetKey(null);
    }
  };

  const handleMoveRule = async (idx: number, delta: -1 | 1) => {
    const targetIdx = idx + delta;
    if (targetIdx < 0 || targetIdx >= rules.length) return;
    const reordered = [...rules];
    const [moved] = reordered.splice(idx, 1);
    reordered.splice(targetIdx, 0, moved);
    setRules(reordered);

    setReordering(true);
    try {
      await api.reorderRoutingRules({
        node_id: activeNodeId,
        rule_ids: reordered.map((r) => r.id),
      });
      toast.info("已更新分流规则优先级");
      await loadAll();
    } catch (err) {
      toast.error("排序保存失败", err instanceof ApiError ? err.message : undefined);
      await loadAll();
    } finally {
      setReordering(false);
    }
  };

  const handleToggleRule = async (rule: RouteRuleItem) => {
    setBusyRuleId(rule.id);
    try {
      await api.updateRoutingRule(rule.id, {
        node_id: rule.node_id,
        name: rule.name,
        preset_key: rule.preset_key,
        priority: rule.priority,
        enabled: !rule.enabled,
        action: rule.action,
        outbound_tag: rule.outbound_tag,
        rule_sets: rule.rule_sets,
        domains: rule.domains,
        domain_suffix: rule.domain_suffix,
        domain_keyword: rule.domain_keyword,
        ip_cidr: rule.ip_cidr,
        protocol: rule.protocol,
        port: rule.port,
        auth_user: rule.auth_user,
        inbound: rule.inbound,
        ip_is_private: rule.ip_is_private,
      });
      toast.success(!rule.enabled ? `已启用规则 ${rule.name}` : `已停用规则 ${rule.name}`);
      await loadAll();
    } catch (err) {
      toast.error("切换规则状态失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyRuleId(null);
    }
  };

  const handleDeleteRule = async (rule: RouteRuleItem) => {
    setBusyRuleId(rule.id);
    try {
      await api.deleteRoutingRule(rule.id);
      setConfirmDeleteId(null);
      toast.success("已删除分流规则", rule.name);
      await loadAll();
    } catch (err) {
      toast.error("删除规则失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyRuleId(null);
    }
  };

  const openCreateModal = () => {
    setEditing(null);
    setRuleName("");
    setAction("route");
    setOutboundTag(availableOutbounds.find((o) => o !== "direct") || "direct");
    setRuleEnabled(true);
    setRuleSets([]);
    setCustomRuleSetInput("");
    setDomainsText("");
    setSuffixText("");
    setKeywordText("");
    setIpCidrText("");
    setProtocolsText("");
    setPortsText("");
    setAuthUsers([]);
    setInboundTags([]);
    setIpIsPrivate(false);
    setModalOpen(true);
  };

  const openEditModal = (r: RouteRuleItem) => {
    setEditing(r);
    setRuleName(r.name);
    setAction(r.action);
    setOutboundTag(r.outbound_tag || "direct");
    setRuleEnabled(r.enabled);
    setRuleSets(Array.isArray(r.rule_sets) ? r.rule_sets : []);
    setCustomRuleSetInput("");
    setDomainsText((r.domains || []).join(", "));
    setSuffixText((r.domain_suffix || []).join(", "));
    setKeywordText((r.domain_keyword || []).join(", "));
    setIpCidrText((r.ip_cidr || []).join(", "));
    setProtocolsText((r.protocol || []).join(", "));
    setPortsText((r.port || []).join(", "));
    setAuthUsers(Array.isArray(r.auth_user) ? r.auth_user : []);
    setInboundTags(Array.isArray(r.inbound) ? r.inbound : []);
    setIpIsPrivate(Boolean(r.ip_is_private));
    setModalOpen(true);
  };

  const toggleInArray = (list: string[], val: string): string[] =>
    list.includes(val) ? list.filter((x) => x !== val) : [...list, val];

  const handleSaveRule = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ruleName.trim()) {
      toast.error("请输入分流规则名称");
      return;
    }
    const extraSrs = splitCommaOrLines(customRuleSetInput);
    const mergedRuleSets = Array.from(new Set([...ruleSets, ...extraSrs]));

    const payload = {
      node_id: editing?.node_id ?? activeNodeId,
      name: ruleName.trim(),
      preset_key: editing?.preset_key,
      priority: editing?.priority,
      enabled: ruleEnabled,
      action,
      outbound_tag: action === "route" ? outboundTag : "",
      rule_sets: mergedRuleSets,
      domains: splitCommaOrLines(domainsText),
      domain_suffix: splitCommaOrLines(suffixText),
      domain_keyword: splitCommaOrLines(keywordText),
      ip_cidr: splitCommaOrLines(ipCidrText),
      protocol: splitCommaOrLines(protocolsText),
      port: splitPorts(portsText),
      auth_user: authUsers,
      inbound: inboundTags,
      ip_is_private: ipIsPrivate,
    };

    setSavingRule(true);
    try {
      if (editing) {
        await api.updateRoutingRule(editing.id, payload);
        toast.success("分流规则已更新", ruleName.trim());
      } else {
        await api.createRoutingRule(payload);
        toast.success("分流规则已创建", ruleName.trim());
      }
      setModalOpen(false);
      await loadAll();
    } catch (err) {
      toast.error(
        editing ? "更新规则失败" : "创建规则失败",
        err instanceof ApiError ? err.message : undefined,
      );
    } finally {
      setSavingRule(false);
    }
  };

  return (
    <div className="space-y-5">
      {/* Default Final Outbound Control Bar */}
      <Panel bodyClassName="p-4">
        <div className="flex flex-col gap-3.5 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-sm font-semibold text-ink">
              <GitBranch className="h-4 w-4 text-accent" />
              <span>默认兜底出站 (route.final) · {activeNode?.name || `#${activeNodeId}`}</span>
            </div>
            <p className="mt-0.5 text-xs text-ink-secondary">
              未命中下方任何分流规则的流量将默认转发至此出站（如 <code className="font-mono">direct</code>、WARP 或上游策略组）
            </p>
          </div>

          <div className="flex flex-wrap items-center gap-2.5">
            <Select
              value={defaultOutbound}
              disabled={savingDefault || loading}
              onChange={(e) => void handleSaveDefaultOutbound(e.target.value)}
              className="w-52 font-mono text-xs"
              aria-label="默认出站"
            >
              {availableOutbounds.map((tag) => (
                <option key={tag} value={tag}>
                  {tag === "direct" ? "direct（直连）" : tag}
                </option>
              ))}
            </Select>

            {onNavigate && (
              <Button variant="outline" size="sm" onClick={() => onNavigate("/core")}>
                查看 config.json
                <ArrowUpRight className="h-3.5 w-3.5" />
              </Button>
            )}
          </div>
        </div>
      </Panel>

      {/* High-Frequency One-Click Routing Presets */}
      <Panel
        title="高频场景一键分流预设"
        description="自动挂载 SagerNet 官方远程 .srs 二进制规则集并按 sing-box 1.12+ 规范编译（拦截采用 action: reject）"
        actions={
          <Button variant="ghost" size="xs" loading={loading} onClick={() => void loadAll()}>
            <RefreshCw className="h-3.5 w-3.5" />
            刷新
          </Button>
        }
      >
        {presets.length === 0 ? (
          <div className="py-6 text-center text-xs text-ink-muted">
            {loading ? "正在加载预设模板..." : "暂无可用预设模板"}
          </div>
        ) : (
          <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
            {presets.map((preset) => {
              const existing = activePresetByKey.get(preset.key);
              const isActive = Boolean(existing?.enabled);
              const isBlock = preset.category === "block" || !preset.needs_outbound;
              const applying = applyingPresetKey === preset.key;

              return (
                <div
                  key={preset.key}
                  className={cn(
                    "flex flex-col justify-between gap-3 rounded-md border p-3.5 transition-colors",
                    isActive
                      ? "border-ok/45 bg-ok/5"
                      : "border-hairline bg-recessed/50",
                  )}
                >
                  <div>
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex items-center gap-1.5 text-xs font-semibold text-ink">
                        {isBlock ? (
                          <ShieldAlert className="h-3.5 w-3.5 shrink-0 text-danger" />
                        ) : (
                          <Sparkles className="h-3.5 w-3.5 shrink-0 text-accent" />
                        )}
                        <span>{preset.name}</span>
                      </div>

                      {isActive ? (
                        <Badge tone="ok">
                          <CheckCircle2 className="h-3 w-3" />
                          已启用
                        </Badge>
                      ) : (
                        <Badge tone={isBlock ? "danger" : "accent"}>
                          {isBlock ? "安全拦截" : "流媒体/AI"}
                        </Badge>
                      )}
                    </div>

                    <p className="mt-1.5 text-[11px] leading-relaxed text-ink-secondary">
                      {preset.description}
                    </p>

                    {preset.rule_sets && preset.rule_sets.length > 0 && (
                      <div className="mt-2 flex flex-wrap gap-1">
                        {preset.rule_sets.map((rs) => (
                          <span
                            key={rs.tag}
                            className="rounded border border-hairline bg-surface px-1.5 py-0.5 font-mono text-[10px] text-ink-muted"
                          >
                            {rs.tag}.srs
                          </span>
                        ))}
                      </div>
                    )}
                  </div>

                  <div className="flex items-center gap-2 border-t border-hairline pt-2.5">
                    {preset.needs_outbound ? (
                      <Select
                        value={presetTargetMap[preset.key] || "direct"}
                        onChange={(e) =>
                          setPresetTargetMap((prev) => ({
                            ...prev,
                            [preset.key]: e.target.value,
                          }))
                        }
                        className="h-7 flex-1 font-mono text-[11px]"
                        aria-label={`${preset.name} 目标出站`}
                      >
                        {availableOutbounds.map((tag) => (
                          <option key={tag} value={tag}>
                            → {tag}
                          </option>
                        ))}
                      </Select>
                    ) : (
                      <span className="flex-1 font-mono text-[11px] text-danger">
                        action: reject
                      </span>
                    )}

                    <Button
                      variant={isActive ? "secondary" : "primary"}
                      size="xs"
                      loading={applying}
                      disabled={applyingPresetKey !== null}
                      onClick={() => void handleApplyPreset(preset)}
                    >
                      {isActive ? "更新" : "一键启用"}
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Panel>

      {/* Ordered Custom & Active Routing Rules Table */}
      <Panel
        title="分流规则列表（自上而下按优先级匹配）"
        description="系统自动在规则顶部注入 action: sniff 协议嗅探；支持上下调整顺序与按成员/入站精细分流"
        actions={
          <Button variant="primary" size="sm" onClick={openCreateModal}>
            <Plus className="h-3.5 w-3.5" />
            新建自定义规则
          </Button>
        }
        bodyClassName="p-0"
      >
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="border-b border-hairline bg-recessed text-ink-secondary">
              <tr>
                <th className="px-4 py-2.5 font-medium">顺序与启停</th>
                <th className="px-3 py-2.5 font-medium">规则名称</th>
                <th className="px-3 py-2.5 font-medium">匹配条件 (Matchers)</th>
                <th className="px-3 py-2.5 font-medium">动作与目标出站</th>
                <th className="px-4 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {loading && rules.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-10 text-center text-ink-muted">
                    正在加载分流规则...
                  </td>
                </tr>
              ) : rules.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-10 text-center text-ink-muted">
                    当前节点暂无自定义分流规则，所有流量将走默认出站{" "}
                    <code className="font-mono text-ink">{defaultOutbound}</code>。
                  </td>
                </tr>
              ) : (
                rules.map((rule, idx) => {
                  const chips = buildMatcherChips(rule);
                  const isBusy = busyRuleId === rule.id;

                  return (
                    <tr
                      key={rule.id}
                      className={cn(
                        "hover:bg-surface-hover/60",
                        !rule.enabled && "opacity-60",
                      )}
                    >
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2">
                          <span className="w-5 font-mono text-[11px] text-ink-muted tabular-nums">
                            #{idx + 1}
                          </span>
                          <div className="inline-flex flex-col">
                            <button
                              type="button"
                              disabled={idx === 0 || reordering}
                              onClick={() => void handleMoveRule(idx, -1)}
                              className="rounded p-0.5 text-ink-muted hover:bg-surface-hover hover:text-ink disabled:opacity-30"
                              title="上移优先级"
                              aria-label="上移规则"
                            >
                              <ArrowUp className="h-3 w-3" />
                            </button>
                            <button
                              type="button"
                              disabled={idx === rules.length - 1 || reordering}
                              onClick={() => void handleMoveRule(idx, 1)}
                              className="rounded p-0.5 text-ink-muted hover:bg-surface-hover hover:text-ink disabled:opacity-30"
                              title="下移优先级"
                              aria-label="下移规则"
                            >
                              <ArrowDown className="h-3 w-3" />
                            </button>
                          </div>
                          <Switch
                            checked={rule.enabled}
                            disabled={isBusy}
                            onChange={() => void handleToggleRule(rule)}
                            label={`切换规则 ${rule.name}`}
                          />
                        </div>
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <span className="font-medium text-ink">{rule.name}</span>
                          {rule.preset_key && (
                            <Badge tone="neutral">预设</Badge>
                          )}
                        </div>
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex max-w-[360px] flex-wrap gap-1">
                          {chips.length > 0 ? (
                            chips.map((c, i) => (
                              <Badge key={`${c.label}-${i}`} tone={c.tone} className="font-mono text-[10px]">
                                {c.label}
                              </Badge>
                            ))
                          ) : (
                            <span className="text-ink-muted">全部匹配</span>
                          )}
                        </div>
                      </td>

                      <td className="px-3 py-3">
                        {rule.action === "reject" ? (
                          <Badge tone="danger">
                            <Ban className="h-3 w-3" />
                            拦截 (reject)
                          </Badge>
                        ) : rule.action === "route" ? (
                          <Badge tone="accent" className="font-mono">
                            → {rule.outbound_tag || "direct"}
                          </Badge>
                        ) : (
                          <Badge tone="warn" className="font-mono">
                            {rule.action}
                          </Badge>
                        )}
                      </td>

                      <td className="px-4 py-3 text-right">
                        <div className="inline-flex items-center gap-1.5">
                          <Button
                            variant="outline"
                            size="xs"
                            disabled={isBusy}
                            onClick={() => openEditModal(rule)}
                          >
                            <Edit3 className="h-3 w-3" />
                            编辑
                          </Button>

                          {confirmDeleteId === rule.id ? (
                            <div className="inline-flex items-center gap-1 rounded border border-danger/45 bg-danger/10 px-2 py-0.5">
                              <span className="text-[11px] text-danger">删除？</span>
                              <Button
                                variant="danger"
                                size="xs"
                                loading={isBusy}
                                onClick={() => void handleDeleteRule(rule)}
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
                              onClick={() => setConfirmDeleteId(rule.id)}
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

      {/* Add/Edit Custom Route Rule Modal */}
      <Modal
        open={modalOpen}
        onClose={() => !savingRule && setModalOpen(false)}
        title={editing ? `编辑分流规则：${editing.name}` : "新建自定义分流规则"}
        description="按 sing-box 1.12+ 路由规则编译，支持远程 .srs 规则集、域名/IP、端口、协议及按成员/入站匹配"
        size="xl"
        footer={
          <>
            <Button
              variant="ghost"
              size="sm"
              disabled={savingRule}
              onClick={() => setModalOpen(false)}
            >
              取消
            </Button>
            <Button
              type="submit"
              form="route-rule-form"
              variant="primary"
              size="sm"
              loading={savingRule}
            >
              {editing ? "保存修改" : "创建规则"}
            </Button>
          </>
        }
      >
        <form id="route-rule-form" onSubmit={handleSaveRule} className="space-y-4">
          <div className="grid gap-3.5 sm:grid-cols-3">
            <Field label="规则名称" htmlFor="rr-name">
              <Input
                id="rr-name"
                value={ruleName}
                onChange={(e) => setRuleName(e.target.value)}
                placeholder="例如：OpenAI 走 WARP"
                required
              />
            </Field>
            <Field label="执行动作 (action)" htmlFor="rr-action">
              <Select
                id="rr-action"
                value={action}
                onChange={(e) => setAction(e.target.value as RouteRuleAction)}
              >
                <option value="route">转发至指定出站 (route)</option>
                <option value="reject">拦截丢弃 (reject)</option>
                <option value="hijack-dns">劫持 DNS (hijack-dns)</option>
              </Select>
            </Field>
            <Field label="目标出站 (outbound_tag)" htmlFor="rr-outbound">
              <Select
                id="rr-outbound"
                disabled={action !== "route"}
                value={outboundTag}
                onChange={(e) => setOutboundTag(e.target.value)}
                className="font-mono"
              >
                {availableOutbounds.map((tag) => (
                  <option key={tag} value={tag}>
                    {tag}
                  </option>
                ))}
              </Select>
            </Field>
          </div>

          {/* Builtin SRS rule sets picker */}
          <div className="space-y-2 rounded-md border border-hairline bg-recessed p-3.5">
            <div className="text-xs font-medium text-ink">
              内置官方远程规则集 (.srs Rule Sets)
            </div>
            <div className="flex flex-wrap gap-1.5">
              {BUILTIN_SRS_SUGGESTIONS.map((tag) => {
                const selected = ruleSets.includes(tag);
                return (
                  <button
                    key={tag}
                    type="button"
                    onClick={() => setRuleSets((prev) => toggleInArray(prev, tag))}
                    className={cn(
                      "rounded border px-2 py-1 font-mono text-[11px] transition-colors",
                      selected
                        ? "border-accent bg-accent-muted text-accent"
                        : "border-hairline bg-surface text-ink-secondary hover:text-ink",
                    )}
                  >
                    {tag}
                  </button>
                );
              })}
            </div>
            <Input
              value={customRuleSetInput}
              onChange={(e) => setCustomRuleSetInput(e.target.value)}
              placeholder="可选：附加其他规则集标签（逗号分隔）"
              className="h-8 font-mono text-xs"
            />
          </div>

          {/* Domain & IP matchers */}
          <div className="grid gap-3.5 sm:grid-cols-2">
            <Field label="精确域名 (domains，逗号分隔)" htmlFor="rr-domains">
              <Input
                id="rr-domains"
                value={domainsText}
                onChange={(e) => setDomainsText(e.target.value)}
                placeholder="api.openai.com, claude.ai"
                className="font-mono text-xs"
              />
            </Field>
            <Field label="域名后缀 (domain_suffix，逗号分隔)" htmlFor="rr-suffix">
              <Input
                id="rr-suffix"
                value={suffixText}
                onChange={(e) => setSuffixText(e.target.value)}
                placeholder="openai.com, anthropic.com, netflix.com"
                className="font-mono text-xs"
              />
            </Field>
            <Field label="域名关键词 (domain_keyword)" htmlFor="rr-kw">
              <Input
                id="rr-kw"
                value={keywordText}
                onChange={(e) => setKeywordText(e.target.value)}
                placeholder="google, youtube"
                className="font-mono text-xs"
              />
            </Field>
            <Field label="目标 IP / CIDR (ip_cidr)" htmlFor="rr-cidr">
              <Input
                id="rr-cidr"
                value={ipCidrText}
                onChange={(e) => setIpCidrText(e.target.value)}
                placeholder="1.1.1.1/32, 10.0.0.0/8"
                className="font-mono text-xs"
              />
            </Field>
            <Field label="探测协议 (protocols，如 bittorrent, quic)" htmlFor="rr-proto">
              <Input
                id="rr-proto"
                value={protocolsText}
                onChange={(e) => setProtocolsText(e.target.value)}
                placeholder="bittorrent"
                className="font-mono text-xs"
              />
            </Field>
            <Field label="目标端口 (ports，逗号分隔)" htmlFor="rr-ports">
              <Input
                id="rr-ports"
                value={portsText}
                onChange={(e) => setPortsText(e.target.value)}
                placeholder="25, 465, 587"
                className="font-mono text-xs"
              />
            </Field>
          </div>

          {/* Member & Inbound selectors */}
          <div className="grid gap-3.5 sm:grid-cols-2">
            <div className="rounded-md border border-hairline bg-recessed p-3">
              <div className="mb-1.5 text-xs font-medium text-ink">
                限定认证成员 (auth_users，留空匹配全部)
              </div>
              <div className="flex flex-wrap gap-1.5">
                {members.length === 0 ? (
                  <span className="text-[11px] text-ink-muted">暂无成员</span>
                ) : (
                  members.map((m) => {
                    const selected = authUsers.includes(m.name);
                    return (
                      <button
                        key={m.id}
                        type="button"
                        onClick={() => setAuthUsers((prev) => toggleInArray(prev, m.name))}
                        className={cn(
                          "rounded border px-2 py-0.5 font-mono text-[11px] transition-colors",
                          selected
                            ? "border-uplink bg-uplink/15 text-uplink"
                            : "border-hairline bg-surface text-ink-secondary hover:text-ink",
                        )}
                      >
                        {m.name}
                      </button>
                    );
                  })
                )}
              </div>
            </div>

            <div className="rounded-md border border-hairline bg-recessed p-3">
              <div className="mb-1.5 text-xs font-medium text-ink">
                限定入站标签 (inbounds，留空匹配全部)
              </div>
              <div className="flex flex-wrap gap-1.5">
                {inbounds.length === 0 ? (
                  <span className="text-[11px] text-ink-muted">当前节点暂无入站</span>
                ) : (
                  inbounds.map((ib) => {
                    const selected = inboundTags.includes(ib.tag);
                    return (
                      <button
                        key={ib.id}
                        type="button"
                        onClick={() => setInboundTags((prev) => toggleInArray(prev, ib.tag))}
                        className={cn(
                          "rounded border px-2 py-0.5 font-mono text-[11px] transition-colors",
                          selected
                            ? "border-accent bg-accent-muted text-accent"
                            : "border-hairline bg-surface text-ink-secondary hover:text-ink",
                        )}
                      >
                        {ib.tag}
                      </button>
                    );
                  })
                )}
              </div>
            </div>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-4 rounded-md border border-hairline bg-recessed px-3.5 py-2.5 text-xs">
            <label className="flex items-center gap-2">
              <Switch
                checked={ipIsPrivate}
                onChange={setIpIsPrivate}
                label="匹配私有内网 IP"
              />
              <span className="text-ink">匹配私有/局域网 IP (ip_is_private)</span>
            </label>

            <label className="flex items-center gap-2">
              <Switch checked={ruleEnabled} onChange={setRuleEnabled} label="启用该规则" />
              <span className="text-ink">启用规则</span>
            </label>
          </div>
        </form>
      </Modal>
    </div>
  );
}
