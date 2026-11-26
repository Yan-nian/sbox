import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Check,
  Copy,
  Edit3,
  Eye,
  EyeOff,
  KeyRound,
  Plus,
  RefreshCw,
  RotateCcw,
  Trash2,
  Users,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { cn } from "@/lib/cn";
import { formatBytes, formatDateTime } from "@/lib/format";
import { useToast } from "@/components/ui/Toast";
import {
  Badge,
  Button,
  Field,
  Input,
  Modal,
  Panel,
  StatusDot,
  Switch,
} from "@/components/ui/primitives";
import type { MemberItem } from "@/types/api";

function maskSecret(val: string): string {
  if (!val) return "-";
  if (val.length <= 10) return "••••••••";
  return `${val.slice(0, 6)}••••••${val.slice(-4)}`;
}

export function MembersPage() {
  const toast = useToast();
  const [members, setMembers] = useState<MemberItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [revealedIds, setRevealedIds] = useState<Record<number, boolean>>({});
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  // Inline confirm states
  const [confirmResetId, setConfirmResetId] = useState<number | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);

  // Create / Edit Modal
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<MemberItem | null>(null);
  const [name, setName] = useState("");
  const [note, setNote] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [saving, setSaving] = useState(false);

  const loadMembers = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.listMembers();
      setMembers(Array.isArray(res.members) ? res.members : []);
    } catch (err) {
      toast.error("加载成员列表失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setLoading(false);
    }
  }, [toast]);

  useEffect(() => {
    void loadMembers();
  }, [loadMembers]);

  const openCreate = () => {
    setEditing(null);
    setName("");
    setNote("");
    setEnabled(true);
    setModalOpen(true);
  };

  const openEdit = (m: MemberItem) => {
    setEditing(m);
    setName(m.name);
    setNote(m.note || "");
    setEnabled(m.enabled);
    setModalOpen(true);
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmedName = name.trim();
    if (!trimmedName) {
      toast.error("请输入成员名称");
      return;
    }
    setSaving(true);
    try {
      if (editing) {
        await api.updateMember(editing.id, {
          name: trimmedName,
          note: note.trim(),
          enabled,
        });
        toast.success("已更新成员信息", trimmedName);
      } else {
        await api.createMember({
          name: trimmedName,
          note: note.trim(),
          enabled,
        });
        toast.success("已创建成员并自动生成全协议凭据", trimmedName);
      }
      setModalOpen(false);
      await loadMembers();
    } catch (err) {
      toast.error(
        editing ? "更新成员失败" : "创建成员失败",
        err instanceof ApiError ? err.message : undefined,
      );
    } finally {
      setSaving(false);
    }
  };

  const handleToggle = async (m: MemberItem) => {
    setBusyId(m.id);
    try {
      const updated = await api.toggleMember(m.id);
      setMembers((prev) => prev.map((item) => (item.id === m.id ? updated : item)));
      toast.success(
        updated.enabled ? `已启用成员 ${m.name}` : `已停用成员 ${m.name}`,
        "核心配置已同步热重载",
      );
    } catch (err) {
      toast.error("切换状态失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyId(null);
    }
  };

  const handleResetCredentials = async (m: MemberItem) => {
    setBusyId(m.id);
    try {
      const updated = await api.resetMemberCredentials(m.id);
      setMembers((prev) => prev.map((item) => (item.id === m.id ? updated : item)));
      setConfirmResetId(null);
      toast.success("已轮换成员全部凭据", `${m.name} 需更新客户端配置`);
    } catch (err) {
      toast.error("重置凭据失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyId(null);
    }
  };

  const handleDelete = async (m: MemberItem) => {
    setBusyId(m.id);
    try {
      await api.deleteMember(m.id);
      setMembers((prev) => prev.filter((item) => item.id !== m.id));
      setConfirmDeleteId(null);
      toast.success("已删除成员", m.name);
    } catch (err) {
      toast.error("删除成员失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setBusyId(null);
    }
  };

  const copyValue = async (key: string, val: string, label: string) => {
    try {
      await navigator.clipboard.writeText(val);
      setCopiedKey(key);
      toast.info(`已复制 ${label}`);
      window.setTimeout(() => setCopiedKey(null), 1600);
    } catch {
      toast.error("复制失败");
    }
  };

  const summary = useMemo(() => {
    let active = 0;
    let up = 0;
    let down = 0;
    let maxTraffic = 1;
    for (const m of members) {
      if (m.enabled) active += 1;
      up += m.upload_bytes || 0;
      down += m.download_bytes || 0;
      const tot = (m.upload_bytes || 0) + (m.download_bytes || 0);
      if (tot > maxTraffic) maxTraffic = tot;
    }
    return { active, up, down, maxTraffic };
  }, [members]);

  return (
    <div className="space-y-5">
      {/* Unified summary strip */}
      <div className="grid divide-y divide-hairline overflow-hidden rounded-lg border border-hairline bg-surface sm:grid-cols-4 sm:divide-x sm:divide-y-0">
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">成员总数</div>
          <div className="mt-1 font-mono text-lg font-semibold text-ink tabular-nums">
            {members.length}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">已启用成员</div>
          <div className="mt-1 font-mono text-lg font-semibold text-ok tabular-nums">
            {summary.active}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">成员累计上行</div>
          <div className="mt-1 font-mono text-lg font-semibold text-uplink tabular-nums">
            {formatBytes(summary.up)}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-[11px] text-ink-muted">成员累计下行</div>
          <div className="mt-1 font-mono text-lg font-semibold text-accent tabular-nums">
            {formatBytes(summary.down)}
          </div>
        </div>
      </div>

      <Panel
        title="成员凭据与访问控制"
        description="每位成员拥有独立 UUID、密码、SS-2022 密钥与订阅 Token；启停或轮换凭据自动热重载核心"
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              loading={loading}
              onClick={() => void loadMembers()}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              刷新
            </Button>
            <Button variant="primary" size="sm" onClick={openCreate}>
              <Plus className="h-3.5 w-3.5" />
              新增成员
            </Button>
          </div>
        }
      >
        {loading && members.length === 0 ? (
          <div className="py-12 text-center text-xs text-ink-muted">正在加载成员列表...</div>
        ) : members.length === 0 ? (
          <div className="rounded-md border border-dashed border-hairline-strong bg-recessed py-12 text-center">
            <Users className="mx-auto h-7 w-7 text-ink-muted" />
            <p className="mt-2 text-xs text-ink-secondary">尚未创建任何成员</p>
            <p className="mt-1 text-[11px] text-ink-muted">
              入站节点将自动绑定已启用成员的凭据，并按成员独立统计上下行流量。
            </p>
            <Button variant="secondary" size="sm" className="mt-3" onClick={openCreate}>
              <Plus className="h-3.5 w-3.5" />
              创建首位成员
            </Button>
          </div>
        ) : (
          <div className="space-y-3">
            {members.map((m) => {
              const revealed = Boolean(revealedIds[m.id]);
              const total = (m.upload_bytes || 0) + (m.download_bytes || 0);
              const pct = Math.min(100, Math.round((total / summary.maxTraffic) * 100));
              const isBusy = busyId === m.id;

              return (
                <article
                  key={m.id}
                  className={cn(
                    "rounded-md border bg-recessed/50 p-4 transition-colors",
                    m.enabled ? "border-hairline" : "border-hairline/60 opacity-75",
                  )}
                >
                  {/* Header row */}
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="flex items-center gap-3">
                      <Switch
                        checked={m.enabled}
                        disabled={isBusy}
                        onChange={() => void handleToggle(m)}
                        label={`切换成员 ${m.name} 状态`}
                      />
                      <div>
                        <div className="flex flex-wrap items-center gap-2">
                          <StatusDot state={m.enabled ? "ok" : "idle"} />
                          <span className="font-mono text-sm font-semibold text-ink">
                            {m.name}
                          </span>
                          <Badge tone={m.enabled ? "ok" : "neutral"}>
                            {m.enabled ? "已启用" : "已停用"}
                          </Badge>
                          {m.note && (
                            <span className="text-xs text-ink-secondary">{m.note}</span>
                          )}
                        </div>
                        {m.created_at && (
                          <div className="mt-0.5 font-mono text-[11px] text-ink-muted">
                            创建于 {formatDateTime(m.created_at)}
                          </div>
                        )}
                      </div>
                    </div>

                    {/* Traffic + action buttons */}
                    <div className="flex flex-wrap items-center gap-2">
                      <div className="mr-2 text-right font-mono text-xs tabular-nums">
                        <div className="flex items-center justify-end gap-2.5">
                          <span className="text-uplink">↑ {formatBytes(m.upload_bytes)}</span>
                          <span className="text-accent">↓ {formatBytes(m.download_bytes)}</span>
                        </div>
                        <div className="mt-1 h-1.5 w-36 overflow-hidden rounded-full bg-hairline">
                          <div
                            className="h-full rounded-full bg-accent"
                            style={{ width: `${total > 0 ? Math.max(4, pct) : 0}%` }}
                          />
                        </div>
                      </div>

                      <Button
                        variant="ghost"
                        size="xs"
                        onClick={() =>
                          setRevealedIds((prev) => ({ ...prev, [m.id]: !prev[m.id] }))
                        }
                      >
                        {revealed ? (
                          <>
                            <EyeOff className="h-3.5 w-3.5" />
                            隐藏
                          </>
                        ) : (
                          <>
                            <Eye className="h-3.5 w-3.5" />
                            明文
                          </>
                        )}
                      </Button>

                      <Button variant="outline" size="xs" onClick={() => openEdit(m)}>
                        <Edit3 className="h-3 w-3" />
                        编辑
                      </Button>

                      {confirmResetId === m.id ? (
                        <div className="inline-flex items-center gap-1 rounded border border-warn/45 bg-warn/10 px-2 py-0.5">
                          <span className="text-[11px] text-warn">确认轮换凭据？</span>
                          <Button
                            variant="danger"
                            size="xs"
                            loading={isBusy}
                            onClick={() => void handleResetCredentials(m)}
                          >
                            确认
                          </Button>
                          <Button
                            variant="ghost"
                            size="xs"
                            onClick={() => setConfirmResetId(null)}
                          >
                            取消
                          </Button>
                        </div>
                      ) : (
                        <Button
                          variant="outline"
                          size="xs"
                          disabled={isBusy}
                          onClick={() => {
                            setConfirmDeleteId(null);
                            setConfirmResetId(m.id);
                          }}
                        >
                          <RotateCcw className="h-3 w-3 text-warn" />
                          轮换凭据
                        </Button>
                      )}

                      {confirmDeleteId === m.id ? (
                        <div className="inline-flex items-center gap-1 rounded border border-danger/45 bg-danger/10 px-2 py-0.5">
                          <span className="text-[11px] text-danger">确认删除？</span>
                          <Button
                            variant="danger"
                            size="xs"
                            loading={isBusy}
                            onClick={() => void handleDelete(m)}
                          >
                            删除
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
                          onClick={() => {
                            setConfirmResetId(null);
                            setConfirmDeleteId(m.id);
                          }}
                        >
                          <Trash2 className="h-3 w-3" />
                          删除
                        </Button>
                      )}
                    </div>
                  </div>

                  {/* Credentials Grid */}
                  <div className="mt-3.5 grid gap-2 border-t border-hairline pt-3 sm:grid-cols-2 lg:grid-cols-5">
                    {[
                      { k: "uuid", label: "UUID", val: m.uuid },
                      { k: "pwd", label: "通用密码", val: m.password },
                      { k: "ss16", label: "SS-2022 (128)", val: m.ss2022_key16 },
                      { k: "ss32", label: "SS-2022 (256)", val: m.ss2022_key32 },
                      { k: "sub", label: "订阅 Token", val: m.sub_token },
                    ].map((cred) => {
                      const copyId = `${m.id}-${cred.k}`;
                      return (
                        <div
                          key={cred.k}
                          className="rounded border border-hairline bg-surface px-2.5 py-1.5"
                        >
                          <div className="flex items-center justify-between text-[10px] text-ink-muted">
                            <span className="flex items-center gap-1">
                              <KeyRound className="h-2.5 w-2.5" />
                              {cred.label}
                            </span>
                            {cred.val && (
                              <button
                                type="button"
                                onClick={() =>
                                  void copyValue(copyId, cred.val, `${m.name} ${cred.label}`)
                                }
                                className="text-ink-muted hover:text-accent"
                                title={`复制 ${cred.label}`}
                              >
                                {copiedKey === copyId ? (
                                  <Check className="h-3 w-3 text-ok" />
                                ) : (
                                  <Copy className="h-3 w-3" />
                                )}
                              </button>
                            )}
                          </div>
                          <div
                            className="mt-0.5 truncate font-mono text-[11px] text-ink"
                            title={revealed ? cred.val : undefined}
                          >
                            {revealed ? cred.val || "-" : maskSecret(cred.val)}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </article>
              );
            })}
          </div>
        )}
      </Panel>

      {/* Add / Edit Member Modal */}
      <Modal
        open={modalOpen}
        onClose={() => !saving && setModalOpen(false)}
        title={editing ? `编辑成员：${editing.name}` : "新增成员"}
        description={
          editing
            ? "修改成员名称、备注或启用状态；凭据保持不变"
            : "创建后自动生成 UUID、强密码、SS-2022 16/32B 密钥与独立订阅 Token"
        }
        size="md"
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
              form="member-form"
              variant="primary"
              size="sm"
              loading={saving}
            >
              {editing ? "保存修改" : "创建成员"}
            </Button>
          </>
        }
      >
        <form id="member-form" onSubmit={handleSave} className="space-y-4">
          <Field
            label="成员标识 (Name)"
            htmlFor="member-name"
            hint="建议使用英文字母或数字（如 alice、phone-1），将作为 Clash API 流量统计的 user 标签"
          >
            <Input
              id="member-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="alice"
              className="font-mono"
              required
            />
          </Field>

          <Field label="备注（可选）" htmlFor="member-note">
            <Input
              id="member-note"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="家用手机与笔记本"
            />
          </Field>

          <div className="flex items-center justify-between rounded-md border border-hairline bg-recessed px-3.5 py-2.5">
            <div>
              <div className="text-xs font-medium text-ink">启用该成员</div>
              <div className="text-[11px] text-ink-muted">
                停用后将从所有入站配置中移除并立即热重载
              </div>
            </div>
            <Switch checked={enabled} onChange={setEnabled} label="启用该成员" />
          </div>
        </form>
      </Modal>
    </div>
  );
}
