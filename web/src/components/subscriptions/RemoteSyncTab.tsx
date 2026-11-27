import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  CloudUpload,
  Edit3,
  ExternalLink,
  History,
  Plus,
  RefreshCw,
  Send,
  Trash2,
  XCircle,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import { formatDateTime } from "@/lib/format";
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
} from "@/components/ui/primitives";
import type {
  MemberItem,
  SubFormat,
  SyncHistoryItem,
  SyncMergeMode,
  SyncTargetConfig,
  SyncTargetItem,
  SyncTargetType,
} from "@/types/api";

const TARGET_TYPE_META: Record<
  SyncTargetType,
  { label: string; tone: "accent" | "ok" | "uplink" }
> = {
  github_gist: { label: "GitHub Gist", tone: "accent" },
  github_repo: { label: "GitHub Repo", tone: "ok" },
  sub_store: { label: "Sub-Store API", tone: "uplink" },
};

export function RemoteSyncTab() {
  const toast = useToast();
  const [targets, setTargets] = useState<SyncTargetItem[]>([]);
  const [history, setHistory] = useState<SyncHistoryItem[]>([]);
  const [members, setMembers] = useState<MemberItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [pushingId, setPushingId] = useState<number | null>(null);
  const [pushingAll, setPushingAll] = useState(false);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);

  // Add/Edit Modal
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<SyncTargetItem | null>(null);
  const [name, setName] = useState("");
  const [type, setType] = useState<SyncTargetType>("github_gist");
  const [memberId, setMemberId] = useState<string>("");
  const [format, setFormat] = useState<SubFormat>("base64");
  const [mergeMode, setMergeMode] = useState<SyncMergeMode>("merge_by_instance");
  const [autoPush, setAutoPush] = useState(true);
  const [enabled, setEnabled] = useState(true);
  const [token, setToken] = useState("");
  const [gistId, setGistId] = useState("");
  const [repo, setRepo] = useState("");
  const [branch, setBranch] = useState("main");
  const [filePath, setFilePath] = useState("sbox-nodes.txt");
  const [subStoreUrl, setSubStoreUrl] = useState("");
  const [subStoreName, setSubStoreName] = useState("");
  const [proxyUrl, setProxyUrl] = useState("");
  const [saving, setSaving] = useState(false);

  const loadAll = useCallback(async () => {
    setLoading(true);
    try {
      const [tRes, hRes, mRes] = await Promise.all([
        api.listSyncTargets(),
        api.listSyncHistory(50).catch(() => ({ history: [] })),
        api.listMembers().catch(() => ({ members: [] })),
      ]);
      setTargets(Array.isArray(tRes.targets) ? tRes.targets : []);
      setHistory(Array.isArray(hRes.history) ? hRes.history : []);
      setMembers(Array.isArray(mRes.members) ? mRes.members : []);
    } catch (err) {
      toast.error("加载远端同步目标失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setLoading(false);
    }
  }, [toast]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  const memberMap = useMemo(() => {
    const map = new Map<number, MemberItem>();
    for (const m of members) map.set(m.id, m);
    return map;
  }, [members]);

  const openCreateModal = () => {
    setEditing(null);
    setName("");
    setType("github_gist");
    setMemberId(members[0] ? String(members[0].id) : "");
    setFormat("base64");
    setMergeMode("merge_by_instance");
    setAutoPush(true);
    setEnabled(true);
    setToken("");
    setGistId("");
    setRepo("");
    setBranch("main");
    setFilePath("sbox-nodes.txt");
    setSubStoreUrl("");
    setSubStoreName("");
    setProxyUrl("");
    setModalOpen(true);
  };

  const openEditModal = (t: SyncTargetItem) => {
    setEditing(t);
    setName(t.name);
    setType(t.type);
    setMemberId(String(t.member_id || ""));
    setFormat(t.format || (t.type === "sub_store" ? "clash" : "base64"));
    setMergeMode(t.merge_mode || "replace");
    setAutoPush(t.auto_push !== false);
    setEnabled(t.enabled);
    setToken(""); // Blank preserves stored secret on backend
    setGistId(t.config?.gist_id || "");
    setRepo(t.config?.repo || "");
    setBranch(t.config?.branch || "main");
    setFilePath(t.config?.file_path || "sbox-nodes.txt");
    setSubStoreUrl(t.config?.sub_store_url || "");
    setSubStoreName(t.config?.sub_store_name || "");
    setProxyUrl(t.config?.proxy_url || "");
    setModalOpen(true);
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    const parsedMemberId = Number.parseInt(memberId, 10);
    if (!name.trim() || !parsedMemberId) {
      toast.error("请填写目标名称并选择要同步的成员");
      return;
    }

    const config: SyncTargetConfig = {
      proxy_url: proxyUrl.trim() || undefined,
    };
    if (token.trim()) {
      config.token = token.trim();
    }
    if (type === "github_gist") {
      if (!gistId.trim()) {
        toast.error("请输入 GitHub Gist ID");
        return;
      }
      config.gist_id = gistId.trim();
      config.file_path = filePath.trim() || "sbox-nodes.txt";
    } else if (type === "github_repo") {
      if (!repo.trim()) {
        toast.error("请输入 GitHub 仓库名（如 owner/repo）");
        return;
      }
      config.repo = repo.trim();
      config.branch = branch.trim() || "main";
      config.file_path = filePath.trim() || "sbox-nodes.txt";
    } else if (type === "sub_store") {
      if (!subStoreUrl.trim()) {
        toast.error("请输入 Sub-Store 后端地址");
        return;
      }
      config.sub_store_url = subStoreUrl.trim();
      config.sub_store_name = subStoreName.trim() || undefined;
    }

    const effectiveMerge: SyncMergeMode =
      type !== "sub_store" && (format === "uri" || format === "base64")
        ? mergeMode
        : "replace";

    setSaving(true);
    try {
      const payload = {
        name: name.trim(),
        enabled,
        type,
        member_id: parsedMemberId,
        format,
        merge_mode: effectiveMerge,
        auto_push: autoPush,
        config,
      };
      if (editing) {
        await api.updateSyncTarget(editing.id, payload);
        toast.success("同步目标已更新", name.trim());
      } else {
        await api.createSyncTarget(payload);
        toast.success("已添加远端同步目标", name.trim());
      }
      setModalOpen(false);
      await loadAll();
    } catch (err) {
      toast.error(
        editing ? "更新同步目标失败" : "创建同步目标失败",
        err instanceof ApiError ? err.message : undefined,
      );
    } finally {
      setSaving(false);
    }
  };

  const handleToggle = async (t: SyncTargetItem) => {
    setBusyId(t.id);
    try {
      await api.updateSyncTarget(t.id, {
        name: t.name,
        enabled: !t.enabled,
        type: t.type,
        member_id: t.member_id,
        format: t.format,
        merge_mode: t.merge_mode,
        auto_push: t.auto_push,
        config: t.config || {},
      });
      toast.success(!t.enabled ? `已启用同步目标 ${t.name}` : `已停用同步目标 ${t.name}`);
      await loadAll();
    } catch (err) {
      toast.error("切换状态失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyId(null);
    }
  };

  const handlePushOne = async (t: SyncTargetItem) => {
    setPushingId(t.id);
    try {
      const res = await api.pushSyncTarget(t.id);
      if (res.result?.success) {
        toast.success(
          `推送成功：${t.name}`,
          `共同步 ${res.result.node_count} 条节点 (HTTP ${res.result.http_status})`,
        );
      } else {
        toast.error(`推送失败：${t.name}`, res.result?.message);
      }
      await loadAll();
    } catch (err) {
      toast.error("推送失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setPushingId(null);
    }
  };

  const handlePushAll = async () => {
    setPushingAll(true);
    try {
      const res = await api.pushAllSyncTargets();
      const results = Array.isArray(res.results) ? res.results : [];
      const ok = results.filter((r) => r.result?.success).length;
      if (results.length === 0) {
        toast.info("没有已启用的同步目标");
      } else if (ok === results.length) {
        toast.success(`全部 ${ok} 个远端目标推送成功`);
      } else {
        toast.info(`批量推送完成：${ok}/${results.length} 成功`);
      }
      await loadAll();
    } catch (err) {
      toast.error("批量推送失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setPushingAll(false);
    }
  };

  const handleDelete = async (t: SyncTargetItem) => {
    setBusyId(t.id);
    try {
      await api.deleteSyncTarget(t.id);
      setConfirmDeleteId(null);
      toast.success("已删除同步目标", t.name);
      await loadAll();
    } catch (err) {
      toast.error("删除同步目标失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyId(null);
    }
  };

  const canMergeByInstance = type !== "sub_store" && (format === "uri" || format === "base64");

  return (
    <div className="space-y-5">
      {/* Sync Targets Panel */}
      <Panel
        title="远端主动推送目标 (GitHub Gist / Repo & Sub-Store)"
        description="支持按 instance_id 无损增量合并（多面板共用私有 Gist 不互盖）及变更后 3 秒防抖自动推送"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              loading={loading}
              onClick={() => void loadAll()}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              刷新
            </Button>
            <Button
              variant="secondary"
              size="sm"
              loading={pushingAll}
              disabled={targets.filter((t) => t.enabled).length === 0 || pushingAll}
              onClick={() => void handlePushAll()}
            >
              <CloudUpload className="h-3.5 w-3.5" />
              全部立即推送
            </Button>
            <Button
              variant="primary"
              size="sm"
              disabled={members.length === 0}
              onClick={openCreateModal}
            >
              <Plus className="h-3.5 w-3.5" />
              新增同步目标
            </Button>
          </div>
        }
        bodyClassName="p-0"
      >
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="border-b border-hairline bg-recessed text-ink-secondary">
              <tr>
                <th className="px-4 py-2.5 font-medium">启停与目标名称</th>
                <th className="px-3 py-2.5 font-medium">类型 / 绑定成员 / 格式</th>
                <th className="px-3 py-2.5 font-medium">同步策略与远端地址</th>
                <th className="px-3 py-2.5 font-medium">最近推送状态</th>
                <th className="px-4 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {loading && targets.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-10 text-center text-ink-muted">
                    正在加载同步目标...
                  </td>
                </tr>
              ) : targets.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-10 text-center text-ink-muted">
                    尚未配置远端推送目标。添加私有 GitHub Gist / 仓库或自建 Sub-Store 后，节点变更将在 3 秒防抖后自动同步。
                  </td>
                </tr>
              ) : (
                targets.map((t) => {
                  const meta = TARGET_TYPE_META[t.type] ?? {
                    label: t.type,
                    tone: "accent" as const,
                  };
                  const boundMember = memberMap.get(t.member_id);
                  const isPushing = pushingId === t.id;
                  const isBusy = busyId === t.id;

                  return (
                    <tr
                      key={t.id}
                      className={cn(
                        "hover:bg-surface-hover/60",
                        !t.enabled && "opacity-65",
                      )}
                    >
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-3">
                          <Switch
                            checked={t.enabled}
                            disabled={isBusy || isPushing}
                            onChange={() => void handleToggle(t)}
                            label={`切换同步目标 ${t.name}`}
                          />
                          <div>
                            <div className="flex items-center gap-2">
                              <StatusDot state={t.enabled ? "ok" : "idle"} />
                              <span className="font-medium text-ink">{t.name}</span>
                            </div>
                            <div className="mt-0.5 font-mono text-[11px] text-ink-muted">
                              {t.type === "github_gist"
                                ? `Gist: ${t.config?.gist_id || "-"} / ${t.config?.file_path || "sbox-nodes.txt"}`
                                : t.type === "github_repo"
                                  ? `${t.config?.repo || "-"}@${t.config?.branch || "main"}:${t.config?.file_path || "sbox-nodes.txt"}`
                                  : `${t.config?.sub_store_url || "-"} (${t.config?.sub_store_name || "auto"})`}
                            </div>
                          </div>
                        </div>
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <Badge tone={meta.tone}>{meta.label}</Badge>
                          <Badge tone="neutral" className="font-mono">
                            {boundMember?.name || `成员 #${t.member_id}`}
                          </Badge>
                          <Badge tone="neutral" className="font-mono uppercase">
                            {t.format}
                          </Badge>
                        </div>
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <Badge
                            tone={t.merge_mode === "merge_by_instance" ? "ok" : "neutral"}
                          >
                            {t.merge_mode === "merge_by_instance"
                              ? "按实例无损合并"
                              : "整文件覆盖"}
                          </Badge>
                          {t.auto_push && <Badge tone="accent">变更自动推送</Badge>}
                        </div>
                        {t.last_remote_url && (
                          <a
                            href={t.last_remote_url}
                            target="_blank"
                            rel="noreferrer"
                            className="mt-1 inline-flex max-w-[240px] items-center gap-1 truncate font-mono text-[11px] text-accent hover:underline"
                            title={t.last_remote_url}
                          >
                            <ExternalLink className="h-3 w-3 shrink-0" />
                            <span className="truncate">{t.last_remote_url}</span>
                          </a>
                        )}
                      </td>

                      <td className="px-3 py-3">
                        <div className="flex items-center gap-1.5">
                          {t.last_status === "ok" || t.last_status === "success" ? (
                            <Badge tone="ok">
                              <CheckCircle2 className="h-3 w-3" />
                              成功
                            </Badge>
                          ) : t.last_error || t.last_status === "error" || t.last_status === "failed" ? (
                            <Badge tone="danger">
                              <XCircle className="h-3 w-3" />
                              失败
                            </Badge>
                          ) : (
                            <Badge tone="neutral">未推送</Badge>
                          )}
                          {t.last_pushed_at && (
                            <span className="font-mono text-[10px] text-ink-muted tabular-nums">
                              {formatDateTime(t.last_pushed_at)}
                            </span>
                          )}
                        </div>
                        {t.last_error && (
                          <div
                            className="mt-1 flex max-w-[220px] items-center gap-1 text-[11px] text-danger"
                            title={t.last_error}
                          >
                            <AlertTriangle className="h-3 w-3 shrink-0" />
                            <span className="truncate">{t.last_error}</span>
                          </div>
                        )}
                      </td>

                      <td className="px-4 py-3 text-right">
                        <div className="inline-flex flex-wrap items-center justify-end gap-1.5">
                          <Button
                            variant="secondary"
                            size="xs"
                            loading={isPushing}
                            disabled={isBusy || isPushing}
                            onClick={() => void handlePushOne(t)}
                          >
                            <Send className="h-3 w-3" />
                            立即推送
                          </Button>
                          <Button
                            variant="outline"
                            size="xs"
                            disabled={isBusy || isPushing}
                            onClick={() => openEditModal(t)}
                          >
                            <Edit3 className="h-3 w-3" />
                            编辑
                          </Button>
                          {confirmDeleteId === t.id ? (
                            <div className="inline-flex items-center gap-1 rounded border border-danger/45 bg-danger/10 px-2 py-0.5">
                              <span className="text-[11px] text-danger">删除？</span>
                              <Button
                                variant="danger"
                                size="xs"
                                loading={isBusy}
                                onClick={() => void handleDelete(t)}
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
                              disabled={isBusy || isPushing}
                              onClick={() => setConfirmDeleteId(t.id)}
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

      {/* Push History Table */}
      <Panel
        title={
          <span className="flex items-center gap-2">
            <History className="h-4 w-4 text-ink-muted" />
            <span>最近推送历史记录</span>
          </span>
        }
        description="自动保留最近 200 条主动推送与防抖自动同步日志"
        bodyClassName="p-0"
      >
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="border-b border-hairline bg-recessed text-ink-secondary">
              <tr>
                <th className="px-4 py-2 font-medium">时间</th>
                <th className="px-3 py-2 font-medium">目标名称</th>
                <th className="px-3 py-2 font-medium">状态 / HTTP</th>
                <th className="px-3 py-2 text-right font-medium">节点数</th>
                <th className="px-4 py-2 font-medium">消息详情</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {history.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-8 text-center text-ink-muted">
                    暂无推送历史记录
                  </td>
                </tr>
              ) : (
                history.map((h) => (
                  <tr key={h.id} className="hover:bg-surface-hover/50">
                    <td className="px-4 py-2 font-mono text-[11px] text-ink-muted tabular-nums">
                      {formatDateTime(h.created_at)}
                    </td>
                    <td className="px-3 py-2 font-medium text-ink">{h.target_name}</td>
                    <td className="px-3 py-2">
                      <div className="flex items-center gap-1.5">
                        <Badge tone={h.success ? "ok" : "danger"}>
                          {h.success ? "成功" : "失败"}
                        </Badge>
                        {h.http_status > 0 && (
                          <span className="font-mono text-[11px] text-ink-muted">
                            HTTP {h.http_status}
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-3 py-2 text-right font-mono tabular-nums text-ink">
                      {h.node_count}
                    </td>
                    <td className="px-4 py-2 text-ink-secondary">{h.message || "-"}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </Panel>

      {/* Add/Edit Sync Target Modal */}
      <Modal
        open={modalOpen}
        onClose={() => !saving && setModalOpen(false)}
        title={editing ? `编辑同步目标：${editing.name}` : "新增远端推送同步目标"}
        description="配置私有 GitHub Gist、GitHub 仓库或自建 Sub-Store 后端以主动同步订阅内容"
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
              form="sync-target-form"
              variant="primary"
              size="sm"
              loading={saving}
            >
              {editing ? "保存修改" : "创建目标"}
            </Button>
          </>
        }
      >
        <form id="sync-target-form" onSubmit={handleSave} className="space-y-4">
          <div className="grid gap-3.5 sm:grid-cols-2">
            <Field label="目标名称" htmlFor="st-name">
              <Input
                id="st-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="例如：私有 Gist 节点汇总"
                required
              />
            </Field>
            <Field label="同步目标类型" htmlFor="st-type">
              <Select
                id="st-type"
                value={type}
                onChange={(e) => {
                  const nextType = e.target.value as SyncTargetType;
                  setType(nextType);
                  if (nextType === "sub_store") {
                    setFormat("clash");
                  }
                }}
              >
                <option value="github_gist">GitHub Gist</option>
                <option value="github_repo">GitHub 私有仓库</option>
                <option value="sub_store">Sub-Store 后端 API</option>
              </Select>
            </Field>
          </div>

          <div className="grid gap-3.5 sm:grid-cols-3">
            <Field label="绑定订阅成员" htmlFor="st-member">
              <Select
                id="st-member"
                value={memberId}
                onChange={(e) => setMemberId(e.target.value)}
                required
              >
                <option value="">请选择成员...</option>
                {members.map((m) => (
                  <option key={m.id} value={String(m.id)}>
                    {m.name}
                  </option>
                ))}
              </Select>
            </Field>

            <Field label="推送订阅格式" htmlFor="st-format">
              <Select
                id="st-format"
                value={format}
                onChange={(e) => setFormat(e.target.value as SubFormat)}
              >
                <option value="base64">Base64 URI</option>
                <option value="uri">明文 URI 列表</option>
                <option value="singbox">sing-box JSON</option>
                <option value="clash">Clash Mihomo YAML</option>
              </Select>
            </Field>

            <Field
              label="文件写入策略"
              htmlFor="st-merge"
              hint={canMergeByInstance ? "多机共用文件推荐无损合并" : "仅 URI/Base64 支持按实例合并"}
            >
              <Select
                id="st-merge"
                disabled={!canMergeByInstance}
                value={canMergeByInstance ? mergeMode : "replace"}
                onChange={(e) => setMergeMode(e.target.value as SyncMergeMode)}
              >
                <option value="merge_by_instance">按 instance_id 无损合并</option>
                <option value="replace">整文件覆盖</option>
              </Select>
            </Field>
          </div>

          {/* Dynamic Config per Target Type */}
          <div className="space-y-3.5 rounded-md border border-hairline bg-recessed p-4">
            {type === "github_gist" && (
              <>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label="GitHub Gist ID" htmlFor="st-gist-id">
                    <Input
                      id="st-gist-id"
                      value={gistId}
                      onChange={(e) => setGistId(e.target.value)}
                      placeholder="例如：a1b2c3d4e5f6..."
                      className="font-mono text-xs"
                      required
                    />
                  </Field>
                  <Field label="Gist 文件名 (file_path)" htmlFor="st-gist-file">
                    <Input
                      id="st-gist-file"
                      value={filePath}
                      onChange={(e) => setFilePath(e.target.value)}
                      placeholder="sbox-nodes.txt"
                      className="font-mono text-xs"
                    />
                  </Field>
                </div>
                <Field
                  label="GitHub Personal Access Token（需 gist 权限）"
                  htmlFor="st-gh-token"
                  hint={editing?.has_token ? "已保存密钥，留空保持原 Token 不变" : undefined}
                >
                  <Input
                    id="st-gh-token"
                    type="password"
                    value={token}
                    onChange={(e) => setToken(e.target.value)}
                    placeholder={editing?.has_token ? "••••••••（留空保持不变）" : "ghp_..."}
                    className="font-mono text-xs"
                    required={!editing?.has_token}
                  />
                </Field>
              </>
            )}

            {type === "github_repo" && (
              <>
                <div className="grid gap-3 sm:grid-cols-3">
                  <Field label="仓库 (owner/repo)" htmlFor="st-repo">
                    <Input
                      id="st-repo"
                      value={repo}
                      onChange={(e) => setRepo(e.target.value)}
                      placeholder="username/my-nodes"
                      className="font-mono text-xs"
                      required
                    />
                  </Field>
                  <Field label="分支 (branch)" htmlFor="st-branch">
                    <Input
                      id="st-branch"
                      value={branch}
                      onChange={(e) => setBranch(e.target.value)}
                      placeholder="main"
                      className="font-mono text-xs"
                    />
                  </Field>
                  <Field label="文件路径 (file_path)" htmlFor="st-repo-file">
                    <Input
                      id="st-repo-file"
                      value={filePath}
                      onChange={(e) => setFilePath(e.target.value)}
                      placeholder="nodes/sbox-nodes.txt"
                      className="font-mono text-xs"
                    />
                  </Field>
                </div>
                <Field
                  label="GitHub Personal Access Token（需 Contents 读写权限）"
                  htmlFor="st-repo-token"
                  hint={editing?.has_token ? "已保存密钥，留空保持原 Token 不变" : undefined}
                >
                  <Input
                    id="st-repo-token"
                    type="password"
                    value={token}
                    onChange={(e) => setToken(e.target.value)}
                    placeholder={editing?.has_token ? "••••••••（留空保持不变）" : "ghp_..."}
                    className="font-mono text-xs"
                    required={!editing?.has_token}
                  />
                </Field>
              </>
            )}

            {type === "sub_store" && (
              <div className="space-y-3">
                <div className="rounded-md border border-accent/35 bg-accent/10 px-3 py-2 text-[11px] leading-relaxed text-ink-secondary">
                  Sub-Store 推荐「Clash Mihomo YAML」，可 100% 无损解析 VLESS / Hy2 / TUIC / AnyTLS / SS-2022 / ShadowTLS / Snell 全协议并导出给 Loon / Surge / Clash
                </div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field
                    label="Sub-Store 后端地址（含路径前缀）"
                    htmlFor="st-ss-url"
                    hint="例如 https://sub.example.com/your_secret_path"
                  >
                    <Input
                      id="st-ss-url"
                      type="url"
                      value={subStoreUrl}
                      onChange={(e) => setSubStoreUrl(e.target.value)}
                      placeholder="https://sub.example.com/secret"
                      className="font-mono text-xs"
                      required
                    />
                  </Field>
                  <Field
                    label="Sub-Store 订阅名称（可选）"
                    htmlFor="st-ss-name"
                    hint="留空默认 sbox-<instance_id>，幂等创建/更新"
                  >
                    <Input
                      id="st-ss-name"
                      value={subStoreName}
                      onChange={(e) => setSubStoreName(e.target.value)}
                      placeholder="sbox-cluster"
                      className="font-mono text-xs"
                    />
                  </Field>
                </div>
              </div>
            )}

            <Field label="推送代理 URL（可选）" htmlFor="st-proxy">
              <Input
                id="st-proxy"
                value={proxyUrl}
                onChange={(e) => setProxyUrl(e.target.value)}
                placeholder="http://127.0.0.1:7890"
                className="font-mono text-xs"
              />
            </Field>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-4 rounded-md border border-hairline bg-recessed px-3.5 py-2.5 text-xs">
            <label className="flex items-center gap-2">
              <Switch checked={autoPush} onChange={setAutoPush} label="入站/成员变更自动防抖推送" />
              <span className="text-ink">入站、成员或证书变更后 3 秒防抖自动推送</span>
            </label>
            <label className="flex items-center gap-2">
              <Switch checked={enabled} onChange={setEnabled} label="启用同步目标" />
              <span className="text-ink">启用目标</span>
            </label>
          </div>
        </form>
      </Modal>
    </div>
  );
}
