export interface AuthStatusResponse {
  initialized: boolean;
  authenticated: boolean;
  username?: string;
  totp_enabled: boolean;
  base_path: string;
}

export interface SetupRequest {
  username: string;
  password: string;
  instance_id?: string;
  web_base_path?: string;
}

export interface SetupResponse {
  token: string;
  base_path: string;
}

export interface LoginRequest {
  username: string;
  password: string;
  totp_code?: string;
}

export interface LoginResponse {
  token?: string;
  requires_totp?: boolean;
}

export interface ChangePasswordRequest {
  current_password: string;
  new_password: string;
}

export interface TOTPSetupResponse {
  secret: string;
  otpauth_url: string;
}

export interface TOTPEnableRequest {
  code: string;
}

export interface TOTPDisableRequest {
  password: string;
  code: string;
}

export interface CoreStatus {
  running: boolean;
  pid: number;
  binary_path: string;
  binary_exists: boolean;
  version: string;
  has_backup: boolean;
  started_at?: string;
  uptime_sec: number;
  clash_api_addr: string;
  last_error?: string;
  upgrading: boolean;
}

export interface MetricsSnapshot {
  up_speed: number;
  down_speed: number;
  memory_inuse: number;
  active_connections: number;
  total_upload: number;
  total_download: number;
  core_connected: boolean;
  updated_at: string;
}

export interface SystemInfo {
  os: string;
  arch: string;
  hostname: string;
  go_version: string;
  panel_version: string;
  panel_uptime_sec: number;
  panel_mem_bytes: number;
  public_ipv4: string;
  public_ipv6: string;
}

export interface DashboardSummary {
  core: CoreStatus;
  metrics: MetricsSnapshot;
  system: SystemInfo;
}

export interface DashboardStreamPayload {
  core: CoreStatus;
  metrics: MetricsSnapshot;
}

export interface ReleaseInfo {
  tag_name: string;
  name: string;
  prerelease: boolean;
  published_at: string;
  asset_name: string;
  download_url: string;
  size: number;
}

export interface ReleasesResponse {
  releases: ReleaseInfo[];
}

export interface UpgradeRequest {
  tag_name: string;
  download_url?: string;
  proxy_url?: string;
}

export interface UpgradeResult {
  success: boolean;
  rolled_back: boolean;
  from_version: string;
  to_version: string;
  message: string;
  check_output?: string;
}

export interface CoreConfigResponse {
  config_json: string;
  override_patch: string;
}

export interface LogEntry {
  time: string;
  level: string;
  message: string;
  source: string;
}

export interface LogsRecentResponse {
  logs: LogEntry[];
}

export interface ConnectionItem {
  id: string;
  inbound: string;
  user: string;
  network: string;
  source: string;
  destination: string;
  rule: string;
  chains: string[];
  upload: number;
  download: number;
  start: string;
}

export interface ConnectionsResponse {
  connections: ConnectionItem[];
}

export interface SettingsData {
  instance_id: string;
  web_base_path: string;
  listen_port: number;
  public_ipv4: string;
  public_ipv6: string;
  github_proxy: string;
  log_level: string;
  totp_enabled: boolean;
  panel_cert_id: number;
}

export interface SettingsUpdateRequest {
  instance_id?: string;
  web_base_path?: string;
  listen_port?: number;
  public_ipv4?: string;
  public_ipv6?: string;
  github_proxy?: string;
  log_level?: string;
  panel_cert_id?: number;
}

// --- Milestone 2: Certificates & Reality Tools ---

export type CertificateType = "acme_http" | "acme_dns" | "self_signed" | "external";
export type ACMEChallenge = "http01" | "dns01";
export type DNSProvider = "cloudflare" | "alidns" | "dnspod";

export interface CertificateItem {
  id: number;
  name: string;
  type: CertificateType;
  domains: string[];
  acme_email?: string;
  dns_provider?: string;
  has_dns_creds?: boolean;
  cert_path: string;
  key_path: string;
  issuer?: string;
  fingerprint?: string;
  not_before?: string;
  not_after?: string;
  auto_renew?: boolean;
  last_error?: string;
  created_at?: string;
  updated_at?: string;
}

export interface CertificatesResponse {
  certificates: CertificateItem[];
}

export interface CreateSelfSignedCertRequest {
  name: string;
  domains: string[];
  valid_days?: number;
}

export interface CreateExternalCertRequest {
  name: string;
  cert_path: string;
  key_path: string;
}

export interface CreateACMECertRequest {
  name: string;
  email: string;
  domains: string[];
  challenge: ACMEChallenge;
  http_port?: string;
  dns_provider?: DNSProvider;
  dns_creds?: Record<string, string>;
  auto_renew?: boolean;
  staging?: boolean;
}

export interface RealityKeypair {
  private_key: string;
  public_key: string;
  short_id: string;
}

export interface RealityProbeResult {
  target: string;
  reachable: boolean;
  tls13: boolean;
  h2: boolean;
  latency_ms: number;
  server_name?: string;
  issuer?: string;
  not_after?: string;
  error?: string;
}

// --- Milestone 2: Members ---

export interface MemberItem {
  id: number;
  name: string;
  enabled: boolean;
  uuid: string;
  password: string;
  ss2022_key16: string;
  ss2022_key32: string;
  sub_token: string;
  upload_bytes: number;
  download_bytes: number;
  note: string;
  created_at?: string;
  updated_at?: string;
}

export interface MembersResponse {
  members: MemberItem[];
}

export interface CreateMemberRequest {
  name: string;
  note?: string;
  enabled?: boolean;
}

export interface UpdateMemberRequest {
  name?: string;
  note?: string;
  enabled?: boolean;
}

// --- Milestone 2: Multi-Protocol Inbounds ---

export type InboundProtocol =
  | "vless"
  | "hysteria2"
  | "tuic"
  | "anytls"
  | "shadowsocks"
  | "trojan"
  | "shadowtls"
  | "direct"
  | "mixed"
  | "socks"
  | "http";

export type MemberBindMode = "all" | "selected";

export interface ExtraEntry {
  name: string;
  host: string;
  port: number;
}

export interface InboundSettings {
  // VLESS
  tls_mode?: "reality" | "tls" | "none";
  flow?: string;
  transport?: "tcp" | "ws" | "httpupgrade" | "grpc";
  ws_path?: string;
  ws_host?: string;
  grpc_service_name?: string;
  reality_handshake_server?: string;
  reality_handshake_port?: number;
  reality_private_key?: string;
  reality_public_key?: string;
  reality_short_ids?: string[];
  server_name?: string;

  // Hysteria2
  up_mbps?: number;
  down_mbps?: number;
  obfs_password?: string;
  masquerade?: string;
  server_ports?: string;
  ignore_client_bandwidth?: boolean;

  // TUIC
  congestion_control?: "bbr" | "cubic" | "new_reno";
  auth_timeout?: string;
  zero_rtt_handshake?: boolean;
  heartbeat?: string;

  // AnyTLS
  padding_scheme?: string[];

  // Shadowsocks
  method?: string;
  server_password?: string;
  network?: string;

  // ShadowTLS
  handshake_server?: string;
  handshake_port?: number;
  strict_mode?: boolean;
  detour_protocol?: "shadowsocks" | "vless";
  detour_port?: number;
  ss_method?: string;
  ss_server_password?: string;

  // Direct
  override_address?: string;
  override_port?: number;

  // Mixed / SOCKS / HTTP
  auth_enabled?: boolean;

  [key: string]: unknown;
}

export interface InboundItem {
  id: number;
  name: string;
  tag: string;
  protocol: InboundProtocol;
  enabled: boolean;
  listen: string;
  listen_port: number;
  settings: InboundSettings;
  certificate_id?: number | null;
  member_mode: MemberBindMode;
  member_ids: number[];
  external_host: string;
  external_port: number;
  extra_entries: ExtraEntry[];
  upload_bytes: number;
  download_bytes: number;
  created_at?: string;
  updated_at?: string;
}

export interface InboundsResponse {
  inbounds: InboundItem[];
}

export interface InboundDefaultsResponse {
  tag: string;
  listen: string;
  listen_port: number;
  settings: InboundSettings;
}

export interface SaveInboundRequest {
  name: string;
  tag?: string;
  protocol: InboundProtocol;
  enabled?: boolean;
  listen?: string;
  listen_port: number;
  settings: InboundSettings;
  certificate_id?: number | null;
  member_mode?: MemberBindMode;
  member_ids?: number[];
  external_host?: string;
  external_port?: number;
  extra_entries?: ExtraEntry[];
}

// --- Milestone 2: Traffic Stats ---

export type TrafficRange = "24h" | "7d" | "30d";

export interface TrafficBucket {
  bucket_time: string;
  upload_bytes: number;
  download_bytes: number;
}

export interface TrafficMemberRow {
  id: number;
  name: string;
  upload_bytes: number;
  download_bytes: number;
}

export interface TrafficInboundRow {
  id: number;
  name: string;
  tag: string;
  protocol: string;
  upload_bytes: number;
  download_bytes: number;
}

export interface TrafficStatsResponse {
  range: TrafficRange;
  series: TrafficBucket[];
  by_member: TrafficMemberRow[];
  by_inbound: TrafficInboundRow[];
}

export interface ResetTrafficRequest {
  scope: "all" | "member" | "inbound";
  id?: number;
}
