import { apiUrl } from "@/lib/basePath";
import type {
  AuthStatusResponse,
  CertificateItem,
  CertificatesResponse,
  ChangePasswordRequest,
  ConnectionsResponse,
  CoreConfigResponse,
  CoreStatus,
  CreateACMECertRequest,
  CreateExternalCertRequest,
  CreateMemberRequest,
  CreateSelfSignedCertRequest,
  DashboardSummary,
  InboundDefaultsResponse,
  InboundItem,
  InboundProtocol,
  InboundsResponse,
  LoginRequest,
  LoginResponse,
  LogsRecentResponse,
  MemberItem,
  MembersResponse,
  RealityKeypair,
  RealityProbeResult,
  ReleasesResponse,
  ResetTrafficRequest,
  SaveInboundRequest,
  SettingsData,
  SettingsUpdateRequest,
  SetupRequest,
  SetupResponse,
  TOTPDisableRequest,
  TOTPEnableRequest,
  TOTPSetupResponse,
  TrafficRange,
  TrafficStatsResponse,
  UpdateMemberRequest,
  UpgradeRequest,
  UpgradeResult,
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

export function buildSSEUrl(path: string): string {
  const url = apiUrl(path);
  const token = getAuthToken();
  if (!token) return url;
  const sep = url.includes("?") ? "&" : "?";
  return `${url}${sep}token=${encodeURIComponent(token)}`;
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

  // Dashboard
  getDashboardSummary: () => request<DashboardSummary>("/dashboard/summary"),

  // Core lifecycle & upgrades
  getCoreStatus: () => request<CoreStatus>("/core/status"),
  startCore: () => request<CoreStatus>("/core/start", { method: "POST" }),
  stopCore: () => request<CoreStatus>("/core/stop", { method: "POST" }),
  restartCore: () => request<CoreStatus>("/core/restart", { method: "POST" }),
  reloadCore: () => request<CoreStatus>("/core/reload", { method: "POST" }),
  rollbackCore: () => request<CoreStatus>("/core/rollback", { method: "POST" }),

  listReleases: (prerelease: boolean) =>
    request<ReleasesResponse>(`/core/releases?prerelease=${prerelease ? "true" : "false"}`),

  upgradeCore: (payload: UpgradeRequest) =>
    request<UpgradeResult>("/core/upgrade", {
      method: "POST",
      body: payload,
    }),

  uploadCoreBinary: (file: File) => {
    const form = new FormData();
    form.append("binary", file);
    return request<UpgradeResult>("/core/upload", {
      method: "POST",
      body: form,
    });
  },

  getCoreConfig: () => request<CoreConfigResponse>("/core/config"),

  updateCoreOverride: (override_patch: string) =>
    request<CoreConfigResponse>("/core/config/override", {
      method: "PUT",
      body: { override_patch },
    }),

  // Logs
  getRecentLogs: (limit = 200) =>
    request<LogsRecentResponse>(`/logs/recent?limit=${limit}`),

  // Connections
  getConnections: () => request<ConnectionsResponse>("/connections"),

  closeConnection: (id: string) =>
    request<{ ok?: boolean }>(`/connections/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),

  // Settings
  getSettings: () => request<SettingsData>("/settings"),

  updateSettings: (payload: SettingsUpdateRequest) =>
    request<SettingsData>("/settings", {
      method: "PUT",
      body: payload,
    }),

  // Milestone 2: Certificates & Tools
  listCertificates: () => request<CertificatesResponse>("/certificates"),

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

  probeRealityTarget: (target: string) =>
    request<RealityProbeResult>("/tools/reality-probe", {
      method: "POST",
      body: { target },
    }),

  generateSS2022Key: (method: string) =>
    request<{ key: string }>("/tools/ss2022-key", {
      method: "POST",
      body: { method },
    }),

  // Milestone 2: Members
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

  // Milestone 2: Inbounds
  listInbounds: () => request<InboundsResponse>("/inbounds"),

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

  // Milestone 2: Traffic Stats
  getTrafficStats: (params: {
    range: TrafficRange;
    member_id?: number;
    inbound_id?: number;
  }) => {
    const qs = new URLSearchParams({ range: params.range });
    if (params.member_id) qs.set("member_id", String(params.member_id));
    if (params.inbound_id) qs.set("inbound_id", String(params.inbound_id));
    return request<TrafficStatsResponse>(`/traffic/stats?${qs.toString()}`);
  },

  resetTraffic: (payload: ResetTrafficRequest) =>
    request<{ ok?: boolean }>("/traffic/reset", {
      method: "POST",
      body: payload,
    }),
};
