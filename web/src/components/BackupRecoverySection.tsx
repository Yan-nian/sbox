import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  AlertTriangle,
  Archive,
  Cloud,
  CloudUpload,
  Download,
  History,
  Lock,
  RefreshCw,
  RotateCcw,
  Save,
  ShieldCheck,
  Upload,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { buildAppUrl, getBasePath, normalizeBasePath } from "@/lib/basePath";
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
  Select,
  Switch,
} from "@/components/ui/primitives";
import type {
  BackupRestoreResponse,
  RemoteBackupConfig,
  RemoteBackupEntry,
  RemoteBackupType,
} from "@/types/api";

export function BackupRecoverySection() {
  const toast = useToast();

  // Local Export & Restore state
  const [exportPassphrase, setExportPassphrase] = useState("");
  const [exporting, setExporting] = useState(false);

  const [restoreFile, setRestoreFile] = useState<File | null>(null);
  const [restorePassphrase, setRestorePassphrase] = useState("");
  const [confirmLocalRestore, setConfirmLocalRestore] = useState(false);
  const [restoringLocal, setRestoringLocal] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  // Remote GitHub Encrypted Backup state
  const [loadingRemote, setLoadingRemote] = useState(true);
  const [remoteCfg, setRemoteCfg] = useState<RemoteBackupConfig | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [remoteType, setRemoteType] = useState<RemoteBackupType>("github_repo");
  const [repo, setRepo] = useState("");
  const [branch, setBranch] = useState("main");
  const [pathPrefix, setPathPrefix] = useState("sbox-backups");
  const [gistId, setGistId] = useState("");
  const [token, setToken] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [keepCount, setKeepCount] = useState("10");
  const [intervalHours, setIntervalHours] = useState("24");
  const [proxyUrl, setProxyUrl] = useState("");

  const [savingRemote, setSavingRemote] = useState(false);
  const [pushingRemote, setPushingRemote] = useState(false);
  const [listingRemote, setListingRemote] = useState(false);
  const [remoteEntries, setRemoteEntries] = useState<RemoteBackupEntry[] | null>(null);

  // Remote snapshot restore modal
  const [restoreEntry, setRestoreEntry] = useState<RemoteBackupEntry | null>(null);
  const [remoteRestorePass, setRemoteRestorePass] = useState("");
  const [restoringRemote, setRestoringRemote] = useState(false);

  const loadRemoteConfig = useCallback(async () => {
    setLoadingRemote(true);
    try {
      const cfg = await api.getRemoteBackupConfig();
      setRemoteCfg(cfg);
      setEnabled(Boolean(cfg.enabled));
      setRemoteType(cfg.type === "github_gist" ? "github_gist" : "github_repo");
      setRepo(cfg.repo || "");
      setBranch(cfg.branch || "main");
      setPathPrefix(cfg.path_prefix || "sbox-backups");
      setGistId(cfg.gist_id || "");
      setKeepCount(String(cfg.keep_count || 10));
      setIntervalHours(String(cfg.interval_hours || 24));
      setProxyUrl(cfg.proxy_url || "");
      setToken("");
      setPassphrase("");
    } catch (err) {
      toast.error("加载远端备份配置失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setLoadingRemote(false);
    }
  }, [toast]);

  useEffect(() => {
    void loadRemoteConfig();
  }, [loadRemoteConfig]);

  const handlePostRestore = (res: BackupRestoreResponse) => {
    const info = res.restored;
    toast.success(
      "备份恢复成功",
      `已恢复 ${info?.file_count ?? 0} 个文件（含 ${info?.cert_files ?? 0} 个证书文件），全集群配置正在重新下发`,
    );
    const activeBase = getBasePath();
    const targetBase = normalizeBasePath(res.base_path);
    if (targetBase && targetBase !== activeBase) {
      toast.info("恢复的备份入口路径已变更", `即将跳转至 ${targetBase}`);
      window.setTimeout(() => {
        window.location.assign(buildAppUrl("/settings", targetBase));
      }, 900);
    } else {
      window.setTimeout(() => {
        window.location.reload();
      }, 900);
    }
  };

  const handleExport = async (e: React.FormEvent) => {
    e.preventDefault();
    setExporting(true);
    try {
      const { blob, filename } = await api.exportBackup(exportPassphrase.trim() || undefined);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      toast.success(
        exportPassphrase.trim() ? "已导出 AES-256-GCM 加密备份包" : "已导出本地备份快照",
        filename,
      );
    } catch (err) {
      toast.error("导出备份失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setExporting(false);
    }
  };

  const handleLocalRestore = async () => {
    if (!restoreFile) return;
    setRestoringLocal(true);
    try {
      const res = await api.restoreBackup(restoreFile, restorePassphrase.trim() || undefined);
      setConfirmLocalRestore(false);
      setRestoreFile(null);
      setRestorePassphrase("");
      handlePostRestore(res);
    } catch (err) {
      toast.error("恢复本地备份失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setRestoringLocal(false);
    }
  };

  const handleSaveRemote = async (e: React.FormEvent) => {
    e.preventDefault();
    const keep = Number.parseInt(keepCount, 10);
    const hours = Number.parseInt(intervalHours, 10);
    setSavingRemote(true);
    try {
      const updated = await api.updateRemoteBackupConfig({
        enabled,
        type: remoteType,
        repo: repo.trim(),
        branch: branch.trim() || "main",
        path_prefix: pathPrefix.trim() || "sbox-backups",
        gist_id: gistId.trim(),
        token: token.trim() || undefined,
        passphrase: passphrase.trim() || undefined,
        keep_count: Number.isFinite(keep) && keep > 0 ? keep : 10,
        interval_hours: Number.isFinite(hours) && hours >= 0 ? hours : 24,
        proxy_url: proxyUrl.trim(),
      });
      setRemoteCfg(updated);
      setToken("");
      setPassphrase("");
      toast.success("远端加密备份配置已保存");
    } catch (err) {
      toast.error("保存远端备份配置失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setSavingRemote(false);
    }
  };

  const handlePushRemote = async () => {
    setPushingRemote(true);
    try {
      const res = await api.pushRemoteBackup();
      toast.success(
        "远端 AES-256-GCM 加密备份已上传",
        `${res.entry?.name || res.entry?.path || "snapshot"} (${formatBytes(res.entry?.size)})`,
      );
      await loadRemoteConfig();
      if (remoteEntries !== null) {
        void handleListRemote(true);
      }
    } catch (err) {
      toast.error("远端加密备份失败", err instanceof ApiError ? err.message : undefined);
      await loadRemoteConfig();
    } finally {
      setPushingRemote(false);
    }
  };

  const handleListRemote = async (silent = false) => {
    setListingRemote(true);
    try {
      const res = await api.listRemoteBackups();
      setRemoteEntries(Array.isArray(res.entries) ? res.entries : []);
      if (!silent) {
        toast.info(`已拉取远端快照 (${(res.entries || []).length} 份)`);
      }
    } catch (err) {
      toast.error("拉取远端快照列表失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setListingRemote(false);
    }
  };

  const handleConfirmRemoteRestore = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!restoreEntry) return;
    setRestoringRemote(true);
    try {
      const res = await api.restoreRemoteBackup({
        path: restoreEntry.path,
        passphrase: remoteRestorePass.trim() || undefined,
      });
      setRestoreEntry(null);
      setRemoteRestorePass("");
      handlePostRestore(res);
    } catch (err) {
      toast.error("从远端快照恢复失败", err instanceof ApiError ? err.message : undefined);
    } finally {
      setRestoringRemote(false);
    }
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    const f = e.dataTransfer.files?.[0];
    if (f) {
      setRestoreFile(f);
      setConfirmLocalRestore(false);
    }
  };

  return (
    <div className="space-y-5">
      {/* Local Snapshot Export & Restore */}
      <Panel
        title={
          <span className="flex items-center gap-2">
            <Archive className="h-4 w-4 text-accent" />
            <span>备份与灾难恢复 · 本地快照导出 / 导入</span>
          </span>
        }
        description="打包 SQLite 数据库一致性快照（含所有节点、成员、入站、路由规则）及托管 TLS 证书私钥，支持客户端 AES-256-GCM 加密"
      >
        <div className="grid gap-5 lg:grid-cols-12">
          {/* Export */}
          <form
            onSubmit={handleExport}
            className="flex flex-col justify-between space-y-4 rounded-md border border-hairline bg-recessed/60 p-4 lg:col-span-6"
          >
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-xs font-semibold text-ink">一键导出完整状态包</span>
                <Badge tone={exportPassphrase.trim() ? "ok" : "neutral"}>
                  {exportPassphrase.trim() ? (
                    <>
                      <Lock className="h-3 w-3" />
                      AES-256-GCM (.sboxbak)
                    </>
                  ) : (
                    "明文 (.tar.gz)"
                  )}
                </Badge>
              </div>

              <Field
                label="加密口令（可选，推荐设置）"
                htmlFor="bak-export-pass"
                hint="填写后使用 Argon2id + AES-256-GCM 加密打包为 .sboxbak；留空导出标准 .tar.gz"
              >
                <Input
                  id="bak-export-pass"
                  type="password"
                  value={exportPassphrase}
                  onChange={(e) => setExportPassphrase(e.target.value)}
                  placeholder="可选：输入备份加密主口令"
                  className="font-mono text-xs"
                />
              </Field>
            </div>

            <div className="flex justify-end">
              <Button type="submit" variant="primary" size="sm" loading={exporting}>
                <Download className="h-3.5 w-3.5" />
                一键导出备份包
              </Button>
            </div>
          </form>

          {/* Local Upload & Restore */}
          <div className="space-y-3 rounded-md border border-hairline bg-recessed/60 p-4 lg:col-span-6">
            <div className="text-xs font-semibold text-ink">上传备份包并恢复</div>

            <div
              onDragOver={(e) => {
                e.preventDefault();
                setDragOver(true);
              }}
              onDragLeave={() => setDragOver(false)}
              onDrop={handleDrop}
              onClick={() => fileInputRef.current?.click()}
              className={cn(
                "flex cursor-pointer flex-col items-center justify-center rounded-md border border-dashed px-4 py-5 text-center transition-colors",
                dragOver
                  ? "border-accent bg-accent-muted"
                  : "border-hairline-strong bg-surface hover:border-accent/60",
              )}
            >
              <Upload className="h-5 w-5 text-ink-muted" />
              <p className="mt-1.5 text-xs font-medium text-ink">
                {restoreFile ? restoreFile.name : "拖拽 .sboxbak 或 .tar.gz 备份包到此，或点击选择"}
              </p>
              <p className="mt-0.5 font-mono text-[11px] text-ink-muted">
                {restoreFile
                  ? `${formatBytes(restoreFile.size)} · 准备恢复`
                  : "若备份内的安全路径 (Web Base Path) 不同，恢复后将自动跳转"}
              </p>
              <input
                ref={fileInputRef}
                type="file"
                accept=".sboxbak,.tar.gz,.tgz,.gz"
                className="hidden"
                onChange={(e) => {
                  setRestoreFile(e.target.files?.[0] ?? null);
                  setConfirmLocalRestore(false);
                }}
              />
            </div>

            <div className="flex flex-wrap items-end gap-2">
              <div className="min-w-[180px] flex-1">
                <Field label="解密口令（若为加密 .sboxbak 必填）" htmlFor="bak-restore-pass">
                  <Input
                    id="bak-restore-pass"
                    type="password"
                    value={restorePassphrase}
                    onChange={(e) => setRestorePassphrase(e.target.value)}
                    placeholder="明文 .tar.gz 可留空"
                    className="h-8 font-mono text-xs"
                  />
                </Field>
              </div>

              {confirmLocalRestore ? (
                <div className="flex items-center gap-1.5 rounded-md border border-danger/45 bg-danger/10 px-2.5 py-1.5">
                  <span className="text-[11px] text-danger">覆盖当前库并重载？</span>
                  <Button
                    variant="danger"
                    size="xs"
                    loading={restoringLocal}
                    onClick={() => void handleLocalRestore()}
                  >
                    确认恢复
                  </Button>
                  <Button
                    variant="ghost"
                    size="xs"
                    disabled={restoringLocal}
                    onClick={() => setConfirmLocalRestore(false)}
                  >
                    取消
                  </Button>
                </div>
              ) : (
                <Button
                  variant="danger"
                  size="sm"
                  disabled={!restoreFile}
                  onClick={() => setConfirmLocalRestore(true)}
                >
                  <RotateCcw className="h-3.5 w-3.5" />
                  上传并恢复备份
                </Button>
              )}
            </div>
          </div>
        </div>
      </Panel>

      {/* Remote GitHub Scheduled AES-256-GCM Encrypted Backup */}
      <Panel
        title={
          <span className="flex items-center gap-2">
            <Cloud className="h-4 w-4 text-ok" />
            <span>GitHub 远端定时加密备份</span>
            <Badge tone={remoteCfg?.enabled ? "ok" : "neutral"}>
              {remoteCfg?.enabled ? "定时任务已开启" : "未开启定时"}
            </Badge>
          </span>
        }
        description="强制使用 AES-256-GCM 在本地加密后上传至私有 GitHub 仓库或私有 Gist，自动按保留份数清理旧快照"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              loading={listingRemote}
              disabled={!remoteCfg?.has_token}
              onClick={() => void handleListRemote(false)}
            >
              <History className="h-3.5 w-3.5" />
              拉取远端快照列表
            </Button>
            <Button
              variant="secondary"
              size="sm"
              loading={pushingRemote}
              disabled={!remoteCfg?.has_token || !remoteCfg?.has_passphrase}
              onClick={() => void handlePushRemote()}
            >
              <CloudUpload className="h-3.5 w-3.5" />
              立即加密备份
            </Button>
          </div>
        }
      >
        {loadingRemote ? (
          <div className="py-8 text-center text-xs text-ink-muted">正在加载远端备份配置...</div>
        ) : (
          <div className="space-y-5">
            {remoteCfg?.last_error && (
              <div className="flex items-start gap-2 rounded-md border border-danger/45 bg-danger/10 p-3 text-xs text-danger">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                <div>
                  <span className="font-medium">最近一次远端备份失败：</span>
                  <span className="font-mono">{remoteCfg.last_error}</span>
                </div>
              </div>
            )}

            <form onSubmit={handleSaveRemote} className="space-y-4">
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-hairline bg-recessed px-3.5 py-2.5 text-xs">
                <label className="flex items-center gap-2.5">
                  <Switch
                    checked={enabled}
                    onChange={setEnabled}
                    label="启用定时远端加密备份"
                  />
                  <span className="font-medium text-ink">启用后台定时 AES-256-GCM 远端加密备份</span>
                </label>

                {remoteCfg?.last_backup_at && (
                  <span className="font-mono text-[11px] text-ink-muted tabular-nums">
                    上次成功备份: {formatDateTime(remoteCfg.last_backup_at)}
                  </span>
                )}
              </div>

              <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
                <Field label="远端存储类型" htmlFor="rb-type">
                  <Select
                    id="rb-type"
                    value={remoteType}
                    onChange={(e) => setRemoteType(e.target.value as RemoteBackupType)}
                  >
                    <option value="github_repo">私有 GitHub 仓库（推荐）</option>
                    <option value="github_gist">私有 GitHub Gist</option>
                  </Select>
                </Field>

                {remoteType === "github_repo" ? (
                  <>
                    <Field label="私有仓库 (owner/repo)" htmlFor="rb-repo">
                      <Input
                        id="rb-repo"
                        value={repo}
                        onChange={(e) => setRepo(e.target.value)}
                        placeholder="username/sbox-backup"
                        className="font-mono text-xs"
                      />
                    </Field>
                    <Field label="分支 (branch)" htmlFor="rb-branch">
                      <Input
                        id="rb-branch"
                        value={branch}
                        onChange={(e) => setBranch(e.target.value)}
                        placeholder="main"
                        className="font-mono text-xs"
                      />
                    </Field>
                    <Field label="存储目录前缀 (path_prefix)" htmlFor="rb-prefix">
                      <Input
                        id="rb-prefix"
                        value={pathPrefix}
                        onChange={(e) => setPathPrefix(e.target.value)}
                        placeholder="sbox-backups"
                        className="font-mono text-xs"
                      />
                    </Field>
                  </>
                ) : (
                  <div className="md:col-span-3">
                    <Field label="私有 GitHub Gist ID" htmlFor="rb-gist">
                      <Input
                        id="rb-gist"
                        value={gistId}
                        onChange={(e) => setGistId(e.target.value)}
                        placeholder="输入已有私有 Gist ID"
                        className="font-mono text-xs"
                      />
                    </Field>
                  </div>
                )}
              </div>

              <div className="grid gap-4 md:grid-cols-2">
                <Field
                  label="GitHub Personal Access Token"
                  htmlFor="rb-token"
                  hint={
                    remoteCfg?.has_token
                      ? "已加密存储 Token；留空保持原 Token 不变"
                      : "需具备私有仓库 Contents 或 Gist 读写权限"
                  }
                >
                  <Input
                    id="rb-token"
                    type="password"
                    value={token}
                    onChange={(e) => setToken(e.target.value)}
                    placeholder={remoteCfg?.has_token ? "••••••••（已保存，留空不变）" : "ghp_..."}
                    className="font-mono text-xs"
                  />
                </Field>

                <Field
                  label="AES-256-GCM 加密主口令（远端备份必填）"
                  htmlFor="rb-pass"
                  hint={
                    remoteCfg?.has_passphrase
                      ? "已保存加密主口令；请务必离线牢记以便灾难恢复"
                      : "远端备份强制端到端加密，未设置主口令将拒绝上传"
                  }
                >
                  <Input
                    id="rb-pass"
                    type="password"
                    value={passphrase}
                    onChange={(e) => setPassphrase(e.target.value)}
                    placeholder={
                      remoteCfg?.has_passphrase
                        ? "••••••••（已保存，留空不变）"
                        : "设置高强度 AES-256-GCM 主口令"
                    }
                    className="font-mono text-xs"
                  />
                </Field>
              </div>

              <div className="grid gap-4 md:grid-cols-3">
                <Field label="最大保留历史份数 (keep_count)" htmlFor="rb-keep">
                  <Input
                    id="rb-keep"
                    type="number"
                    min={1}
                    max={100}
                    value={keepCount}
                    onChange={(e) => setKeepCount(e.target.value)}
                    className="font-mono"
                  />
                </Field>
                <Field label="自动备份间隔（小时）" htmlFor="rb-interval">
                  <Input
                    id="rb-interval"
                    type="number"
                    min={1}
                    max={720}
                    value={intervalHours}
                    onChange={(e) => setIntervalHours(e.target.value)}
                    className="font-mono"
                  />
                </Field>
                <Field label="代理 URL（可选）" htmlFor="rb-proxy">
                  <Input
                    id="rb-proxy"
                    value={proxyUrl}
                    onChange={(e) => setProxyUrl(e.target.value)}
                    placeholder="http://127.0.0.1:7890"
                    className="font-mono text-xs"
                  />
                </Field>
              </div>

              <div className="flex flex-wrap items-center justify-between gap-3 border-t border-hairline pt-4">
                <div className="flex flex-wrap items-center gap-2 text-xs">
                  <Badge tone={remoteCfg?.has_token ? "ok" : "warn"}>
                    <ShieldCheck className="h-3 w-3" />
                    Token: {remoteCfg?.has_token ? "已配置" : "未配置"}
                  </Badge>
                  <Badge tone={remoteCfg?.has_passphrase ? "ok" : "warn"}>
                    <Lock className="h-3 w-3" />
                    主口令: {remoteCfg?.has_passphrase ? "已配置" : "未配置"}
                  </Badge>
                </div>

                <Button type="submit" variant="primary" size="sm" loading={savingRemote}>
                  <Save className="h-3.5 w-3.5" />
                  保存远端备份配置
                </Button>
              </div>
            </form>

            {/* Remote Snapshots List */}
            {remoteEntries !== null && (
              <div className="space-y-2 border-t border-hairline pt-4">
                <div className="flex items-center justify-between text-xs">
                  <span className="font-semibold text-ink">
                    远端历史加密快照 ({remoteEntries.length})
                  </span>
                  <Button
                    variant="ghost"
                    size="xs"
                    loading={listingRemote}
                    onClick={() => void handleListRemote(false)}
                  >
                    <RefreshCw className="h-3 w-3" />
                    刷新
                  </Button>
                </div>

                <div className="overflow-x-auto rounded-md border border-hairline">
                  <table className="w-full text-left text-xs">
                    <thead className="border-b border-hairline bg-recessed text-ink-secondary">
                      <tr>
                        <th className="px-3 py-2 font-medium">快照文件名</th>
                        <th className="px-3 py-2 font-medium">路径</th>
                        <th className="px-3 py-2 text-right font-medium">体积</th>
                        <th className="px-3 py-2 font-medium">时间</th>
                        <th className="px-3 py-2 text-right font-medium">操作</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-hairline">
                      {remoteEntries.length === 0 ? (
                        <tr>
                          <td colSpan={5} className="px-3 py-6 text-center text-ink-muted">
                            远端仓库/Gist 中暂无历史备份快照
                          </td>
                        </tr>
                      ) : (
                        remoteEntries.map((entry) => (
                          <tr key={entry.path} className="hover:bg-surface-hover/50">
                            <td className="px-3 py-2 font-mono font-medium text-ink">
                              {entry.name}
                            </td>
                            <td className="px-3 py-2 font-mono text-[11px] text-ink-muted">
                              {entry.path}
                            </td>
                            <td className="px-3 py-2 text-right font-mono tabular-nums text-ink-secondary">
                              {formatBytes(entry.size)}
                            </td>
                            <td className="px-3 py-2 font-mono text-[11px] text-ink-muted tabular-nums">
                              {entry.created_at ? formatDateTime(entry.created_at) : "-"}
                            </td>
                            <td className="px-3 py-2 text-right">
                              <Button
                                variant="danger"
                                size="xs"
                                onClick={() => {
                                  setRestoreEntry(entry);
                                  setRemoteRestorePass("");
                                }}
                              >
                                <RotateCcw className="h-3 w-3" />
                                从此快照恢复
                              </Button>
                            </td>
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
          </div>
        )}
      </Panel>

      {/* Remote Snapshot Restore Confirmation Modal */}
      <Modal
        open={restoreEntry !== null}
        onClose={() => !restoringRemote && setRestoreEntry(null)}
        title="确认从远端加密快照恢复"
        description={restoreEntry?.name}
        size="md"
        footer={
          <>
            <Button
              variant="ghost"
              size="sm"
              disabled={restoringRemote}
              onClick={() => setRestoreEntry(null)}
            >
              取消
            </Button>
            <Button
              type="submit"
              form="remote-restore-form"
              variant="danger"
              size="sm"
              loading={restoringRemote}
            >
              确认解密并覆盖恢复
            </Button>
          </>
        }
      >
        <form id="remote-restore-form" onSubmit={handleConfirmRemoteRestore} className="space-y-4">
          <div className="rounded-md border border-warn/45 bg-warn/10 p-3 text-xs text-ink-secondary leading-relaxed">
            将拉取远端文件{" "}
            <code className="font-mono text-ink">{restoreEntry?.path}</code>{" "}
            并覆盖当前 SQLite 数据库及证书目录，随后自动重新编译并推送至全部在线节点。
          </div>

          <Field
            label="解密口令（可选，留空默认使用已保存的远端主口令）"
            htmlFor="rb-restore-pass"
          >
            <Input
              id="rb-restore-pass"
              type="password"
              value={remoteRestorePass}
              onChange={(e) => setRemoteRestorePass(e.target.value)}
              placeholder="若与当前保存的主口令一致请留空"
              className="font-mono text-xs"
            />
          </Field>
        </form>
      </Modal>
    </div>
  );
}
