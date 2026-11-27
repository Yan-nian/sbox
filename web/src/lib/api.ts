import { apiUrl } from "@/lib/basePath";
import type {
  AuthStatusResponse,
  BackupRestoreResponse,
  CertificateItem,
  CertificatesResponse,
  ChangePasswordRequest,
  ConnectionsResponse,
  CoreConfigResponse,
  CoreStatus,
  CreateACMECertRequest,
  CreateExternalCertRequest,
  CreateMemberRequest,
  CreateNodeRequest,
  CreateNodeResponse,
  CreateSelfSignedCertRequest,
  DashboardSummary,
  DetectPublicIPResponse,
  ExtraOutboundItem,
  InboundDefaultsResponse,
  InboundItem,
  InboundProtocol,
  InboundsResponse,
  LoginRequest,
  LoginResponse,
  LogsRecentResponse,
  MemberItem,
  MembersResponse,
  NodesResponse,
  NodeUpgradeBatchRequest,
  NodeUpgradeBatchResponse,
  NodeView,
  OutboundsResponse,
  ProxyDelayResponse,
  ProxyGroupsResponse,
  RealityKeypair,
  RealityProbeResult,
  RegisterWARPRequest,
  ReleasesResponse,
  RemoteBackupConfig,
  RemoteBackupListResponse,
  RemoteBackupPushResponse,
  ResetNodeTokenResponse,
  ResetTrafficRequest,
  RouteRuleItem,
  RoutingPresetsResponse,
  RoutingRulesResponse,
  SaveExtraOutboundRequest,
  SaveInboundRequest,
  SaveRemoteBackupConfigRequest,
  SaveRouteRuleRequest,
  SaveSyncTargetRequest,
  SaveUpstreamRequest,
  SettingsData,
  SettingsUpdateRequest,
  SetupRequest,
  SetupResponse,
  SubFormat,
  SubscriptionPreviewResponse,
  SyncHistoryResponse,
  SyncPushAllResponse,
  SyncPushResponse,
  SyncTargetItem,
  SyncTargetsResponse,
  TOTPDisableRequest,
  TOTPEnableRequest,
  TOTPSetupResponse,
  TrafficRange,
  TrafficStatsResponse,
  UpdateMemberRequest,
  UpdateNodeRequest,
  UpgradeRequest,
  UpgradeResult,
  UpstreamsResponse,
  UpstreamSubItem,
} from "@/types/api";

const TOKEN_STORAGE_KEY = "sbox_panel_token";

export class ApiError extends Error {
  status: number;
  retryAfter?: string | null;

  constructor(status: number, message: string, retryAfter?: string | null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.retryAfter = retryAfter;
  }
}

let unauthorizedHandler: (() => void) | null = null;

export function setUnauthorizedHandler(fn: (() => void) | null): void {
  unauthorizedHandler = fn;
}

export function getAuthToken(): string | null {
  try {
    return sessionStorage.getItem(TOKEN_STORAGE_KEY);
  } catch {
    return null;
  }
}

export function setAuthToken(token: string | null): void {
  try {
    if (token) {
      sessionStorage.setItem(TOKEN_STORAGE_KEY, token);
    } else {
      sessionStorage.removeItem(TOKEN_STORAGE_KEY);
    }
  } catch {
    // Ignore storage errors in restricted contexts
  }
}

function withNodeId(path: string, nodeId?: number): string {
  if (nodeId === undefined || nodeId === null) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}node_id=${encodeURIComponent(String(nodeId))}`;
}

interface RequestOptions extends Omit<RequestInit, "body"> {
  body?: unknown;
  skipAuthRedirect?: boolean;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { body, headers: customHeaders, skipAuthRedirect, ...rest } = options;
  const headers = new Headers(customHeaders);

  const token = getAuthToken();
  if (token && !headers.has("Authorization")) {
    headers.set("Authorization", `Bearer ${token}`);
  }

  let payload: BodyInit | undefined;
  if (body instanceof FormData) {
    payload = body;
  } else if (body !== undefined) {
    headers.set("Content-Type", "application/json");
    payload = JSON.stringify(body);
  }

  const res = await fetch(apiUrl(path), {
    ...rest,
    headers,
    body: payload,
    credentials: "same-origin",
  });

  const text = await res.text();
  let parsed: Record<string, unknown> | null = null;
  if (text) {
    try {
      parsed = JSON.parse(text) as Record<string, unknown>;
    } catch {
      parsed = null;
    }
  }

  if (!res.ok) {
    if (res.status === 401 && !skipAuthRedirect) {
      setAuthToken(null);
      unauthorizedHandler?.();
    }
    const msg =
      (parsed && typeof parsed.error === "string" && parsed.error) ||
      (parsed && typeof parsed.message === "string" && parsed.message) ||
      (text && text.length < 240 ? text : "") ||
      `请求失败 (${res.status})`;
    const retryHeader =
      res.headers.get("Retry-After") ||
      (parsed && typeof parsed.retry_after_sec === "number"
        ? String(parsed.retry_after_sec)
        : null);
    throw new ApiError(res.status, msg, retryHeader);
  }

  return (parsed ?? {}) as T;
}

export function buildSSEUrl(path: string, nodeId?: number): string {
  let url = apiUrl(withNodeId(path, nodeId));
  const token = getAuthToken();
  if (!token) return url;
  const sep = url.includes("?") ? "&" : "?";
  url = `${url}${sep}token=${encodeURIComponent(token)}`;
  return url;
}

export const api = {
  // Auth
  getAuthStatus: () =>
    request<AuthStatusResponse>("/auth/status", { skipAuthRedirect: true }),

  setup: (payload: SetupRequest) =>
    request<SetupResponse>("/auth/setup", {
      method: "POST",
      body: payload,
      skipAuthRedirect: true,
    }),

  login: (payload: LoginRequest) =>
    request<LoginResponse>("/auth/login", {
      method: "POST",
      body: payload,
      skipAuthRedirect: true,
    }),

  logout: () =>
    request<{ ok?: boolean }>("/auth/logout", {
      method: "POST",
      skipAuthRedirect: true,
    }),

  changePassword: (payload: ChangePasswordRequest) =>
    request<{ ok?: boolean }>("/auth/password", {
      method: "POST",
      body: payload,
      skipAuthRedirect: true,
    }),

  setupTOTP: () =>
    request<TOTPSetupResponse>("/auth/totp/setup", {
      method: "POST",
    }),

  enableTOTP: (payload: TOTPEnableRequest) =>
    request<{ ok?: boolean }>("/auth/totp/enable", {
      method: "POST",
      body: payload,
    }),

  disableTOTP: (payload: TOTPDisableRequest) =>
    request<{ ok?: boolean }>("/auth/totp/disable", {
      method: "POST",
      body: payload,
      skipAuthRedirect: true,
    }),

  // Milestone 3: Cluster Nodes
  listNodes: () => request<NodesResponse>("/nodes"),

  createNode: (payload: CreateNodeRequest) =>
    request<CreateNodeResponse>("/nodes", {
      method: "POST",
      body: payload,
    }),

  updateNode: (id: number, payload: UpdateNodeRequest) =>
    request<NodeView>(`/nodes/${id}`, {
      method: "PUT",
      body: payload,
    }),

  resetNodeToken: (id: number) =>
    request<ResetNodeTokenResponse>(`/nodes/${id}/reset-token`, {
      method: "POST",
    }),

  deleteNode: (id: number, force = false) =>
    request<{ ok?: boolean }>(`/nodes/${id}${force ? "?force=true" : ""}`, {
      method: "DELETE",
    }),

  upgradeAllNodes: (payload: NodeUpgradeBatchRequest) =>
    request<NodeUpgradeBatchResponse>("/nodes/upgrade-all", {
      method: "POST",
      body: payload,
    }),

  // Dashboard (node-scoped)
  getDashboardSummary: (nodeId?: number) =>
    request<DashboardSummary>(withNodeId("/dashboard/summary", nodeId)),

  // Core lifecycle & upgrades (node-scoped)
  getCoreStatus: (nodeId?: number) =>
    request<CoreStatus>(withNodeId("/core/status", nodeId)),
  startCore: (nodeId?: number) =>
    request<CoreStatus>(withNodeId("/core/start", nodeId), { method: "POST" }),
  stopCore: (nodeId?: number) =>
    request<CoreStatus>(withNodeId("/core/stop", nodeId), { method: "POST" }),
  restartCore: (nodeId?: number) =>
    request<CoreStatus>(withNodeId("/core/restart", nodeId), { method: "POST" }),
  reloadCore: (nodeId?: number) =>
    request<CoreStatus>(withNodeId("/core/reload", nodeId), { method: "POST" }),
  rollbackCore: (nodeId?: number) =>
    request<CoreStatus>(withNodeId("/core/rollback", nodeId), { method: "POST" }),

  listReleases: (prerelease: boolean, nodeId?: number) =>
    request<ReleasesResponse>(
      withNodeId(`/core/releases?prerelease=${prerelease ? "true" : "false"}`, nodeId),
    ),

  upgradeCore: (payload: UpgradeRequest, nodeId?: number) =>
    request<UpgradeResult>(withNodeId("/core/upgrade", nodeId), {
      method: "POST",
      body: payload,
    }),

  uploadCoreBinary: (file: File, nodeId?: number) => {
    const form = new FormData();
    form.append("binary", file);
    return request<UpgradeResult>(withNodeId("/core/upload", nodeId), {
      method: "POST",
      body: form,
    });
  },

  getCoreConfig: (nodeId?: number) =>
    request<CoreConfigResponse>(withNodeId("/core/config", nodeId)),

  updateCoreOverride: (override_patch: string, nodeId?: number) =>
    request<CoreConfigResponse>(withNodeId("/core/config/override", nodeId), {
      method: "PUT",
      body: { override_patch },
    }),

  // Logs (node-scoped)
  getRecentLogs: (limit = 200, nodeId?: number) =>
    request<LogsRecentResponse>(withNodeId(`/logs/recent?limit=${limit}`, nodeId)),

  // Connections (node-scoped)
  getConnections: (nodeId?: number) =>
    request<ConnectionsResponse>(withNodeId("/connections", nodeId)),

  closeConnection: (id: string, nodeId?: number) =>
    request<{ ok?: boolean }>(
      withNodeId(`/connections/${encodeURIComponent(id)}`, nodeId),
      { method: "DELETE" },
    ),

  // Settings
  getSettings: () => request<SettingsData>("/settings"),

  updateSettings: (payload: SettingsUpdateRequest) =>
    request<SettingsData>("/settings", {
      method: "PUT",
      body: payload,
    }),

  // Certificates & Tools (node-scoped)
  listCertificates: (nodeId?: number) =>
    request<CertificatesResponse>(withNodeId("/certificates", nodeId)),

  createSelfSignedCert: (payload: CreateSelfSignedCertRequest) =>
    request<CertificateItem>("/certificates/self-signed", {
      method: "POST",
      body: payload,
    }),

  createExternalCert: (payload: CreateExternalCertRequest) =>
    request<CertificateItem>("/certificates/external", {
      method: "POST",
      body: payload,
    }),

  createACMECert: (payload: CreateACMECertRequest) =>
    request<CertificateItem>("/certificates/acme", {
      method: "POST",
      body: payload,
    }),

  renewCertificate: (id: number) =>
    request<CertificateItem>(`/certificates/${id}/renew`, {
      method: "POST",
    }),

  deleteCertificate: (id: number, force = false) =>
    request<{ ok?: boolean }>(`/certificates/${id}${force ? "?force=true" : ""}`, {
      method: "DELETE",
    }),

  generateRealityKeypair: () =>
    request<RealityKeypair>("/tools/reality-keypair", {
      method: "POST",
    }),

  probeRealityTarget: (target: string, nodeId?: number) =>
    request<RealityProbeResult>("/tools/reality-probe", {
      method: "POST",
      body: nodeId ? { target, node_id: nodeId } : { target },
    }),

  generateSS2022Key: (method: string) =>
    request<{ key: string }>("/tools/ss2022-key", {
      method: "POST",
      body: { method },
    }),

  detectPublicIP: (save = false) =>
    request<DetectPublicIPResponse>("/tools/detect-ip", {
      method: "POST",
      body: { save },
    }),

  // Members (Global)
  listMembers: () => request<MembersResponse>("/members"),

  createMember: (payload: CreateMemberRequest) =>
    request<MemberItem>("/members", {
      method: "POST",
      body: payload,
    }),

  updateMember: (id: number, payload: UpdateMemberRequest) =>
    request<MemberItem>(`/members/${id}`, {
      method: "PUT",
      body: payload,
    }),

  toggleMember: (id: number) =>
    request<MemberItem>(`/members/${id}/toggle`, {
      method: "POST",
    }),

  resetMemberCredentials: (id: number) =>
    request<MemberItem>(`/members/${id}/reset-credentials`, {
      method: "POST",
    }),

  deleteMember: (id: number) =>
    request<{ ok?: boolean }>(`/members/${id}`, {
      method: "DELETE",
    }),

  // Inbounds (node-scoped)
  listInbounds: (nodeId?: number) =>
    request<InboundsResponse>(withNodeId("/inbounds", nodeId)),

  getInboundDefaults: (protocol: InboundProtocol) =>
    request<InboundDefaultsResponse>(`/inbounds/defaults/${encodeURIComponent(protocol)}`),

  createInbound: (payload: SaveInboundRequest) =>
    request<InboundItem>("/inbounds", {
      method: "POST",
      body: payload,
    }),

  updateInbound: (id: number, payload: SaveInboundRequest) =>
    request<InboundItem>(`/inbounds/${id}`, {
      method: "PUT",
      body: payload,
    }),

  toggleInbound: (id: number) =>
    request<InboundItem>(`/inbounds/${id}/toggle`, {
      method: "POST",
    }),

  deleteInbound: (id: number) =>
    request<{ ok?: boolean }>(`/inbounds/${id}`, {
      method: "DELETE",
    }),

  // Traffic Stats (supports node_id filter; omit or 0 for cluster-wide)
  getTrafficStats: (params: {
    range: TrafficRange;
    node_id?: number;
    member_id?: number;
    inbound_id?: number;
  }) => {
    const qs = new URLSearchParams({ range: params.range });
    if (params.node_id && params.node_id > 0) qs.set("node_id", String(params.node_id));
    if (params.member_id) qs.set("member_id", String(params.member_id));
    if (params.inbound_id) qs.set("inbound_id", String(params.inbound_id));
    return request<TrafficStatsResponse>(`/traffic/stats?${qs.toString()}`);
  },

  resetTraffic: (payload: ResetTrafficRequest) =>
    request<{ ok?: boolean }>("/traffic/reset", {
      method: "POST",
      body: payload,
    }),

  // Milestone 4: Upstream Outbound Subscriptions
  listUpstreams: (nodeId?: number) =>
    request<UpstreamsResponse>(withNodeId("/upstreams", nodeId)),

  createUpstream: (payload: SaveUpstreamRequest) =>
    request<UpstreamSubItem>("/upstreams", {
      method: "POST",
      body: payload,
    }),

  updateUpstream: (id: number, payload: SaveUpstreamRequest) =>
    request<UpstreamSubItem>(`/upstreams/${id}`, {
      method: "PUT",
      body: payload,
    }),

  syncUpstream: (id: number) =>
    request<{ upstream: UpstreamSubItem }>(`/upstreams/${id}/sync`, {
      method: "POST",
    }),

  deleteUpstream: (id: number) =>
    request<{ ok?: boolean }>(`/upstreams/${id}`, {
      method: "DELETE",
    }),

  // Milestone 4: Extra Outbounds & One-Click Cloudflare WARP
  listOutbounds: (nodeId?: number) =>
    request<OutboundsResponse>(withNodeId("/outbounds", nodeId)),

  createOutbound: (payload: SaveExtraOutboundRequest) =>
    request<ExtraOutboundItem>("/outbounds", {
      method: "POST",
      body: payload,
    }),

  registerWARP: (payload: RegisterWARPRequest) =>
    request<{ outbound: ExtraOutboundItem }>("/outbounds/warp/register", {
      method: "POST",
      body: payload,
    }),

  updateOutbound: (id: number, payload: SaveExtraOutboundRequest) =>
    request<ExtraOutboundItem>(`/outbounds/${id}`, {
      method: "PUT",
      body: payload,
    }),

  toggleOutbound: (id: number) =>
    request<ExtraOutboundItem>(`/outbounds/${id}/toggle`, {
      method: "POST",
    }),

  deleteOutbound: (id: number) =>
    request<{ ok?: boolean }>(`/outbounds/${id}`, {
      method: "DELETE",
    }),

  // Milestone 4: Live Clash API Proxy Groups
  listProxyGroups: (nodeId?: number) =>
    request<ProxyGroupsResponse>(withNodeId("/proxies", nodeId)),

  testProxyDelay: (
    payload: { proxy: string; test_url?: string; timeout_ms?: number },
    nodeId?: number,
  ) =>
    request<ProxyDelayResponse>(withNodeId("/proxies/delay", nodeId), {
      method: "POST",
      body: payload,
    }),

  selectProxy: (payload: { group: string; proxy: string }, nodeId?: number) =>
    request<{ ok?: boolean }>(withNodeId("/proxies/select", nodeId), {
      method: "PUT",
      body: payload,
    }),

  // Milestone 4: Visual Routing Rules & Presets
  listRoutingPresets: () => request<RoutingPresetsResponse>("/routing/presets"),

  listRoutingRules: (nodeId?: number) =>
    request<RoutingRulesResponse>(withNodeId("/routing/rules", nodeId)),

  createRoutingRule: (payload: SaveRouteRuleRequest) =>
    request<RouteRuleItem>("/routing/rules", {
      method: "POST",
      body: payload,
    }),

  updateRoutingRule: (id: number, payload: SaveRouteRuleRequest) =>
    request<RouteRuleItem>(`/routing/rules/${id}`, {
      method: "PUT",
      body: payload,
    }),

  reorderRoutingRules: (payload: { node_id?: number; rule_ids: number[] }) =>
    request<{ ok?: boolean }>("/routing/rules/reorder", {
      method: "POST",
      body: payload,
    }),

  applyRoutingPreset: (payload: {
    node_id?: number;
    preset_key: string;
    outbound_tag?: string;
  }) =>
    request<RouteRuleItem>("/routing/rules/apply-preset", {
      method: "POST",
      body: payload,
    }),

  deleteRoutingRule: (id: number) =>
    request<{ ok?: boolean }>(`/routing/rules/${id}`, {
      method: "DELETE",
    }),

  setDefaultOutbound: (payload: { node_id?: number; default_outbound: string }) =>
    request<{ default_outbound: string }>("/routing/default-outbound", {
      method: "PUT",
      body: payload,
    }),

  // Milestone 5: Cluster Subscriptions & Preview
  previewSubscription: (memberId: number, format: SubFormat) =>
    request<SubscriptionPreviewResponse>(
      `/subscriptions/preview?member_id=${encodeURIComponent(String(memberId))}&format=${encodeURIComponent(format)}`,
    ),

  // Milestone 5: Remote Sync Targets (GitHub Gist / Repo & Sub-Store)
  listSyncTargets: () => request<SyncTargetsResponse>("/sync-targets"),

  createSyncTarget: (payload: SaveSyncTargetRequest) =>
    request<SyncTargetItem>("/sync-targets", {
      method: "POST",
      body: payload,
    }),

  updateSyncTarget: (id: number, payload: SaveSyncTargetRequest) =>
    request<SyncTargetItem>(`/sync-targets/${id}`, {
      method: "PUT",
      body: payload,
    }),

  deleteSyncTarget: (id: number) =>
    request<{ ok?: boolean }>(`/sync-targets/${id}`, {
      method: "DELETE",
    }),

  pushSyncTarget: (id: number) =>
    request<SyncPushResponse>(`/sync-targets/${id}/push`, {
      method: "POST",
    }),

  pushAllSyncTargets: () =>
    request<SyncPushAllResponse>("/sync-targets/push-all", {
      method: "POST",
    }),

  listSyncHistory: (limit = 50) =>
    request<SyncHistoryResponse>(`/sync-targets/history?limit=${limit}`),

  // Milestone 5: Local & Remote AES-256-GCM Encrypted Backup & Restore
  exportBackup: async (passphrase?: string): Promise<{ blob: Blob; filename: string }> => {
    const headers = new Headers({ "Content-Type": "application/json" });
    const token = getAuthToken();
    if (token) headers.set("Authorization", `Bearer ${token}`);

    const res = await fetch(apiUrl("/backup/export"), {
      method: "POST",
      headers,
      body: JSON.stringify({ passphrase: passphrase || "" }),
      credentials: "same-origin",
    });

    if (!res.ok) {
      if (res.status === 401) {
        setAuthToken(null);
        unauthorizedHandler?.();
      }
      const text = await res.text();
      let msg = `导出备份失败 (${res.status})`;
      try {
        const parsed = JSON.parse(text) as Record<string, unknown>;
        if (typeof parsed.error === "string" && parsed.error) msg = parsed.error;
      } catch {
        if (text && text.length < 200) msg = text;
      }
      throw new ApiError(res.status, msg);
    }

    const disp = res.headers.get("Content-Disposition") || "";
    const match = /filename="?([^";]+)"?/i.exec(disp);
    const fallbackExt = passphrase ? "sboxbak" : "tar.gz";
    const filename = match?.[1] || `sbox-panel-backup.${fallbackExt}`;
    const blob = await res.blob();
    return { blob, filename };
  },

  restoreBackup: (file: File, passphrase?: string) => {
    const form = new FormData();
    form.append("file", file);
    if (passphrase) {
      form.append("passphrase", passphrase);
    }
    return request<BackupRestoreResponse>("/backup/restore", {
      method: "POST",
      body: form,
    });
  },

  getRemoteBackupConfig: () => request<RemoteBackupConfig>("/backup/remote/config"),

  updateRemoteBackupConfig: (payload: SaveRemoteBackupConfigRequest) =>
    request<RemoteBackupConfig>("/backup/remote/config", {
      method: "PUT",
      body: payload,
    }),

  pushRemoteBackup: () =>
    request<RemoteBackupPushResponse>("/backup/remote/push", {
      method: "POST",
    }),

  listRemoteBackups: () => request<RemoteBackupListResponse>("/backup/remote/list"),

  restoreRemoteBackup: (payload: { path: string; passphrase?: string }) =>
    request<BackupRestoreResponse>("/backup/remote/restore", {
      method: "POST",
      body: payload,
    }),
};
