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

// --- Milestone 3: Multi-Node Cluster Types ---

export interface NodeView {
  id: number;
  name: string;
  is_local: boolean;
  online: boolean;
  core_running: boolean;
  core_version: string;
  up_speed: number;
  down_speed: number;
  active_connections: number;
  mem_bytes: number;
  inbound_count: number;
  os: string;
  arch: string;
  hostname: string;
  agent_version: string;
  data_dir?: string;
  public_ipv4: string;
  public_ipv6: string;
  override_patch: string;
  log_level: string;
  github_proxy: string;
  last_seen_at?: string;
  note: string;
  created_at?: string;
  updated_at?: string;
}

export interface NodesResponse {
  nodes: NodeView[];
}

export interface CreateNodeRequest {
  name: string;
  note?: string;
  public_ipv4?: string;
  public_ipv6?: string;
}

export interface CreateNodeResponse {
  node: NodeView;
  token: string;
  install_command: string;
}

export interface UpdateNodeRequest {
  name?: string;
  note?: string;
  public_ipv4?: string;
  public_ipv6?: string;
  github_proxy?: string;
  log_level?: string;
}

export interface ResetNodeTokenResponse {
  token: string;
  install_command: string;
}

export interface ClusterSummary {
  total_nodes: number;
  online_nodes: number;
  total_up_speed: number;
  total_down_speed: number;
  total_connections: number;
}

export interface NodeUpgradeBatchRequest {
  tag_name: string;
  proxy_url?: string;
}

export interface NodeUpgradeBatchItem {
  node_id: number;
  node_name: string;
  result: UpgradeResult;
}

export interface NodeUpgradeBatchResponse {
  results: NodeUpgradeBatchItem[];
}

export interface DashboardSummary {
  node?: NodeView;
  core: CoreStatus;
  metrics: MetricsSnapshot;
  system: SystemInfo;
  cluster?: ClusterSummary;
}

export interface DashboardStreamPayload {
  node_id?: number;
  online?: boolean;
  core: CoreStatus;
  metrics: MetricsSnapshot;
  cluster?: ClusterSummary;
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

export interface DetectPublicIPResponse {
  public_ipv4: string;
  public_ipv6: string;
}

// --- Milestone 2/3: Certificates & Reality Tools ---

export type CertificateType = "acme_http" | "acme_dns" | "self_signed" | "external";
export type ACMEChallenge = "http01" | "dns01";
export type DNSProvider = "cloudflare" | "alidns" | "dnspod";

export interface CertificateItem {
  id: number;
  node_id?: number;
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
  node_id?: number;
  name: string;
  domains: string[];
  valid_days?: number;
}

export interface CreateExternalCertRequest {
  node_id?: number;
  name: string;
  cert_path: string;
  key_path: string;
}

export interface CreateACMECertRequest {
  node_id?: number;
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

// --- Milestone 2: Members (Global) ---

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

// --- Milestone 2/3: Multi-Protocol Inbounds ---

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
  node_id?: number;
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

export interface InboundLinkItem {
  member_id: number;
  member_name: string;
  name: string;
  uri: string;
  loon: string;
}

export interface InboundLinksResponse {
  inbound_id: number;
  inbound_name: string;
  protocol: string;
  items: InboundLinkItem[];
}

export type InboundPortStatus =
  | "open"
  | "local_only"
  | "local_unverified"
  | "not_listening"
  | "node_offline";

export interface InboundPortCheckResult {
  inbound_id: number;
  node_id: number;
  host: string;
  port: number;
  network: string;
  status: InboundPortStatus;
  local_listening: boolean;
  external_checked: boolean;
  external_reachable: boolean;
  latency_ms: number;
  probed_by_node_id?: number;
  probed_by_node_name?: string;
  message: string;
  hint?: string;
  checked_at: string;
}

export interface InboundPortCheckBatchResponse {
  results: InboundPortCheckResult[];
}

export interface SaveInboundRequest {
  node_id?: number;
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

// --- Milestone 2/3: Traffic Stats ---

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

export interface TrafficNodeRow {
  id: number;
  name: string;
  is_local: boolean;
  upload_bytes: number;
  download_bytes: number;
}

export interface TrafficStatsResponse {
  range: TrafficRange;
  series: TrafficBucket[];
  by_member: TrafficMemberRow[];
  by_inbound: TrafficInboundRow[];
  by_node?: TrafficNodeRow[];
}

export interface ResetTrafficRequest {
  scope: "all" | "member" | "inbound";
  id?: number;
}

// --- Milestone 4: Upstream Outbound Subs, WARP, Live Proxies & Visual Routing ---

export interface UpstreamSubItem {
  id: number;
  node_id: number; // 0 = global shared across all nodes, >0 = specific node
  name: string;
  tag: string;
  url: string;
  enabled: boolean;
  interval_minutes: number;
  include_regex: string;
  exclude_regex: string;
  enable_urltest: boolean;
  test_url: string;
  proxy_url: string;
  cached_format: string; // "singbox" | "clash" | "uri" | ""
  node_count: number;
  last_fetched_at?: string;
  last_error?: string;
  created_at?: string;
  updated_at?: string;
}

export interface UpstreamsResponse {
  upstreams: UpstreamSubItem[];
}

export interface SaveUpstreamRequest {
  node_id?: number;
  name: string;
  tag?: string;
  url: string;
  enabled?: boolean;
  interval_minutes?: number;
  include_regex?: string;
  exclude_regex?: string;
  enable_urltest?: boolean;
  test_url?: string;
  proxy_url?: string;
  fetch_now?: boolean;
}

export type ExtraOutboundType = "warp" | "wireguard" | "socks" | "http" | "raw";

export interface ExtraOutboundSettings {
  // WARP / WireGuard
  account_id?: string;
  private_key?: string;
  public_key?: string;
  peer_public_key?: string;
  endpoint_host?: string;
  endpoint_port?: number;
  local_ipv4?: string;
  local_ipv6?: string;
  reserved?: number[];
  mtu?: number;
  ipv6_only?: boolean;

  // SOCKS / HTTP
  server?: string;
  server_port?: number;
  username?: string;
  password?: string;
  tls?: boolean;
  server_name?: string;
  insecure?: boolean;

  // Raw sing-box JSON (arbitrary fields)
  [key: string]: unknown;
}

export interface ExtraOutboundItem {
  id: number;
  node_id: number;
  name: string;
  tag: string;
  type: ExtraOutboundType;
  enabled: boolean;
  settings: ExtraOutboundSettings;
  created_at?: string;
  updated_at?: string;
}

export interface OutboundsResponse {
  outbounds: ExtraOutboundItem[];
}

export interface SaveExtraOutboundRequest {
  node_id?: number;
  name: string;
  tag?: string;
  type: ExtraOutboundType;
  enabled?: boolean;
  settings: ExtraOutboundSettings;
}

export interface RegisterWARPRequest {
  node_id?: number;
  name?: string;
  tag?: string;
  ipv6_only?: boolean;
  mtu?: number;
  proxy_url?: string;
}

export interface ProxyGroupInfo {
  name: string;
  type: string;
  now: string;
  all: string[];
  delays: Record<string, number>;
}

export interface ProxyGroupsResponse {
  groups: ProxyGroupInfo[];
}

export interface ProxyDelayResponse {
  proxy: string;
  delay_ms: number;
}

export type RouteRuleAction = "route" | "reject" | "hijack-dns" | "sniff";

export interface RouteRuleItem {
  id: number;
  node_id: number;
  name: string;
  preset_key?: string;
  priority: number;
  enabled: boolean;
  action: RouteRuleAction;
  outbound_tag: string;
  rule_sets: string[];
  domains: string[];
  domain_suffix: string[];
  domain_keyword: string[];
  ip_cidr: string[];
  protocol: string[];
  port: number[];
  auth_user: string[];
  inbound: string[];
  ip_is_private: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface SaveRouteRuleRequest {
  node_id?: number;
  name: string;
  preset_key?: string;
  priority?: number;
  enabled?: boolean;
  action: RouteRuleAction;
  outbound_tag?: string;
  rule_sets?: string[];
  domains?: string[];
  domain_suffix?: string[];
  domain_keyword?: string[];
  ip_cidr?: string[];
  protocol?: string[];
  port?: number[];
  auth_user?: string[];
  inbound?: string[];
  ip_is_private?: boolean;
}

export interface RoutingPresetItem {
  key: string;
  name: string;
  description: string;
  category: "unlock" | "block";
  needs_outbound: boolean;
  default_action: "route" | "reject";
  rule_sets?: {
    tag: string;
    url: string;
    download_detour?: string;
    update_interval?: string;
  }[];
  rule_template?: Record<string, unknown>;
}

export interface RoutingPresetsResponse {
  presets: RoutingPresetItem[];
}

export interface RoutingRulesResponse {
  rules: RouteRuleItem[];
  default_outbound: string;
  available_outbounds: string[];
}

// --- Milestone 5: Cluster Subscriptions, Remote Sync & Encrypted Backup ---

export type SubFormat = "base64" | "uri" | "singbox" | "clash";

export interface SubscriptionNodeItem {
  name: string;
  protocol: string;
  node_name: string;
  uri: string;
}

export interface SubscriptionPreviewResponse {
  format: SubFormat;
  sub_url: string;
  content: string;
  nodes: SubscriptionNodeItem[];
}

export type SyncTargetType = "github_gist" | "github_repo" | "sub_store";
export type SyncMergeMode = "replace" | "merge_by_instance";

export interface SyncTargetConfig {
  token?: string;
  gist_id?: string;
  repo?: string;
  branch?: string;
  file_path?: string;
  sub_store_url?: string;
  sub_store_name?: string;
  proxy_url?: string;
}

export interface SyncTargetItem {
  id: number;
  name: string;
  enabled: boolean;
  type: SyncTargetType;
  member_id: number;
  format: SubFormat;
  merge_mode: SyncMergeMode;
  auto_push: boolean;
  has_token: boolean;
  config: SyncTargetConfig;
  last_pushed_at?: string;
  last_status?: string;
  last_error?: string;
  last_remote_url?: string;
  created_at?: string;
  updated_at?: string;
}

export interface SyncTargetsResponse {
  targets: SyncTargetItem[];
}

export interface SaveSyncTargetRequest {
  name: string;
  enabled?: boolean;
  type: SyncTargetType;
  member_id: number;
  format: SubFormat;
  merge_mode?: SyncMergeMode;
  auto_push?: boolean;
  config: SyncTargetConfig;
}

export interface SyncPushResult {
  success: boolean;
  http_status: number;
  remote_url?: string;
  node_count: number;
  message: string;
}

export interface SyncPushResponse {
  target: SyncTargetItem;
  result: SyncPushResult;
}

export interface SyncPushAllItem {
  target_id: number;
  target_name: string;
  result: SyncPushResult;
}

export interface SyncPushAllResponse {
  results: SyncPushAllItem[];
}

export interface SyncHistoryItem {
  id: number;
  target_id: number;
  target_name: string;
  success: boolean;
  http_status: number;
  node_count: number;
  message: string;
  created_at: string;
}

export interface SyncHistoryResponse {
  history: SyncHistoryItem[];
}

export interface BackupInspectResult {
  encrypted: boolean;
  file_count: number;
  has_db: boolean;
  cert_files: number;
}

export interface BackupRestoreResponse {
  restored: BackupInspectResult;
  base_path: string;
}

export type RemoteBackupType = "github_repo" | "github_gist";

export interface RemoteBackupConfig {
  enabled: boolean;
  type: RemoteBackupType;
  repo: string;
  branch: string;
  path_prefix: string;
  gist_id: string;
  keep_count: number;
  interval_hours: number;
  has_token: boolean;
  has_passphrase: boolean;
  proxy_url: string;
  last_backup_at?: string;
  last_error?: string;
}

export interface SaveRemoteBackupConfigRequest {
  enabled: boolean;
  type: RemoteBackupType;
  repo?: string;
  branch?: string;
  path_prefix?: string;
  gist_id?: string;
  token?: string;
  passphrase?: string;
  keep_count?: number;
  interval_hours?: number;
  proxy_url?: string;
}

export interface RemoteBackupEntry {
  name: string;
  path: string;
  sha?: string;
  size: number;
  created_at?: string;
}

export interface RemoteBackupListResponse {
  entries: RemoteBackupEntry[];
}

export interface RemoteBackupPushResponse {
  entry: RemoteBackupEntry;
}
