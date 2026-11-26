package model

import (
	"encoding/json"
	"time"
)

// Setting keys stored in the SQLite `settings` table.
const (
	SettingInitialized       = "initialized"
	SettingInstanceID        = "instance_id"
	SettingAdminUsername     = "admin_username"
	SettingAdminPasswordHash = "admin_password_hash"
	SettingJWTSecret         = "jwt_secret"
	SettingTOTPSecret        = "totp_secret"
	SettingTOTPPendingSecret = "totp_pending_secret"
	SettingTOTPEnabled       = "totp_enabled"
	SettingWebBasePath       = "web_base_path"
	SettingListenPort        = "listen_port"
	SettingPanelTLSCertID    = "panel_tls_cert_id"
	SettingPublicIPv4        = "public_ipv4"
	SettingPublicIPv6        = "public_ipv6"
	SettingGithubProxy       = "github_proxy"
	SettingLogLevel          = "log_level"
	SettingOverridePatch     = "override_patch"
	SettingClashAPIAddr      = "clash_api_addr"
	SettingClashAPISecret    = "clash_api_secret"
	SettingDecoyHTML         = "decoy_html"
)

// Setting represents a single key-value entry in the settings table.
type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PanelSettings is the aggregated public-facing settings view used by REST APIs.
type PanelSettings struct {
	InstanceID  string `json:"instance_id"`
	WebBasePath string `json:"web_base_path"`
	ListenPort  int    `json:"listen_port"`
	PublicIPv4  string `json:"public_ipv4"`
	PublicIPv6  string `json:"public_ipv6"`
	GithubProxy string `json:"github_proxy"`
	LogLevel    string `json:"log_level"`
	TOTPEnabled bool   `json:"totp_enabled"`
	PanelCertID int64  `json:"panel_cert_id"`
}

// Certificate stores ACME, self-signed, or external TLS certificate metadata.
type Certificate struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Type         string     `json:"type"` // acme_http | acme_dns | self_signed | external
	Domains      []string   `json:"domains"`
	ACMEEmail    string     `json:"acme_email,omitempty"`
	ACMEHTTPPort string     `json:"acme_http_port,omitempty"`
	ACMEStaging  bool       `json:"acme_staging,omitempty"`
	DNSProvider  string     `json:"dns_provider,omitempty"`
	DNSCredsJSON string     `json:"-"`
	HasDNSCreds  bool       `json:"has_dns_creds"`
	CertPath     string     `json:"cert_path"`
	KeyPath      string     `json:"key_path"`
	Issuer       string     `json:"issuer,omitempty"`
	Fingerprint  string     `json:"fingerprint,omitempty"`
	NotBefore    *time.Time `json:"not_before,omitempty"`
	NotAfter     *time.Time `json:"not_after,omitempty"`
	AutoRenew    bool       `json:"auto_renew"`
	LastError    string     `json:"last_error,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Member represents a shared-panel user with unique credentials and subscription token.
type Member struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Enabled       bool      `json:"enabled"`
	UUID          string    `json:"uuid"`
	Password      string    `json:"password"`
	SS2022Key16   string    `json:"ss2022_key16"`
	SS2022Key32   string    `json:"ss2022_key32"`
	SubToken      string    `json:"sub_token"`
	UploadBytes   int64     `json:"upload_bytes"`
	DownloadBytes int64     `json:"download_bytes"`
	Note          string    `json:"note"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ExtraEntry represents an additional relay/transit external endpoint for client subscriptions.
type ExtraEntry struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Inbound represents a sing-box inbound protocol listener definition.
type Inbound struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	Tag           string          `json:"tag"`
	Protocol      string          `json:"protocol"`
	Enabled       bool            `json:"enabled"`
	Listen        string          `json:"listen"`
	ListenPort    int             `json:"listen_port"`
	Settings      json.RawMessage `json:"settings"`
	CertificateID *int64          `json:"certificate_id"`
	MemberMode    string          `json:"member_mode"` // all | selected
	MemberIDs     []int64         `json:"member_ids"`
	ExternalHost  string          `json:"external_host"`
	ExternalPort  int             `json:"external_port"`
	ExtraEntries  []ExtraEntry    `json:"extra_entries"`
	UploadBytes   int64           `json:"upload_bytes"`
	DownloadBytes int64           `json:"download_bytes"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// UpstreamSub represents an external remote subscription source for server-side outbounds.
type UpstreamSub struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	Enabled         bool       `json:"enabled"`
	UpdateCron      string     `json:"update_cron"`
	IncludeRegex    string     `json:"include_regex"`
	ExcludeRegex    string     `json:"exclude_regex"`
	SelectorTag     string     `json:"selector_tag"`
	URLTestTag      string     `json:"urltest_tag"`
	CachedNodesJSON string     `json:"cached_nodes_json"`
	LastFetchStatus string     `json:"last_fetch_status"`
	LastFetchError  string     `json:"last_fetch_error,omitempty"`
	LastFetchedAt   *time.Time `json:"last_fetched_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// OutboundExtra represents manual landing outbounds or Cloudflare WARP WireGuard endpoints.
type OutboundExtra struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Tag        string    `json:"tag"`
	Kind       string    `json:"kind"` // manual | warp_endpoint
	Enabled    bool      `json:"enabled"`
	ConfigJSON string    `json:"config_json"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// RouteRule represents a sing-box routing rule entry ordered by priority.
type RouteRule struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Priority    int       `json:"priority"`
	PresetType  string    `json:"preset_type"` // custom | ai | streaming | security_block
	MatchJSON   string    `json:"match_json"`
	Action      string    `json:"action"` // route | reject | sniff | hijack-dns
	OutboundTag string    `json:"outbound_tag"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SyncTarget represents an outbound push destination (GitHub Gist, GitHub Repo, or Sub-Store).
type SyncTarget struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	TargetType       string     `json:"target_type"` // github_gist | github_repo | sub_store
	Enabled          bool       `json:"enabled"`
	EndpointURL      string     `json:"endpoint_url"`
	AuthTokenEnc     string     `json:"-"`
	TargetRef        string     `json:"target_ref"`
	SyncMode         string     `json:"sync_mode"`  // independent | merge_by_instance
	SubFormat        string     `json:"sub_format"` // base64 | singbox | clash
	LastSyncStatus   string     `json:"last_sync_status"`
	LastSyncHTTPCode int        `json:"last_sync_http_code"`
	LastSyncError    string     `json:"last_sync_error,omitempty"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// TrafficStat stores aggregated hourly or daily traffic counters per member and inbound.
type TrafficStat struct {
	ID            int64     `json:"id"`
	MemberID      *int64    `json:"member_id,omitempty"`
	InboundID     *int64    `json:"inbound_id,omitempty"`
	MemberName    string    `json:"member_name"`
	InboundTag    string    `json:"inbound_tag"`
	Granularity   string    `json:"granularity"` // hour | day
	BucketTime    time.Time `json:"bucket_time"`
	UploadBytes   int64     `json:"upload_bytes"`
	DownloadBytes int64     `json:"download_bytes"`
}

// TrafficSeriesPoint represents one aggregated time bucket for traffic trend charts.
type TrafficSeriesPoint struct {
	BucketTime    string `json:"bucket_time"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

// MemberTrafficItem summarizes cumulative traffic for a single member.
type MemberTrafficItem struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

// InboundTrafficItem summarizes cumulative traffic for a single inbound.
type InboundTrafficItem struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Tag           string `json:"tag"`
	Protocol      string `json:"protocol"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

// TrafficStatsResponse is returned by GET /traffic/stats.
type TrafficStatsResponse struct {
	Range     string               `json:"range"`
	Series    []TrafficSeriesPoint `json:"series"`
	ByMember  []MemberTrafficItem  `json:"by_member"`
	ByInbound []InboundTrafficItem `json:"by_inbound"`
}
