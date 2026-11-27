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
	SettingBackupRemoteJSON  = "backup_remote_json"
	SettingBackupLastAt      = "backup_last_at"
	SettingBackupLastError   = "backup_last_error"
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

// Node represents a managed VPS node (id=1 is the local master node; id>1 are remote agents).
type Node struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	IsLocal         bool       `json:"is_local"`
	Token           string     `json:"-"`
	PublicIPv4      string     `json:"public_ipv4"`
	PublicIPv6      string     `json:"public_ipv6"`
	OverridePatch   string     `json:"override_patch"`
	DefaultOutbound string     `json:"default_outbound"`
	LogLevel        string     `json:"log_level"`
	GithubProxy     string     `json:"github_proxy"`
	OS              string     `json:"os"`
	Arch            string     `json:"arch"`
	Hostname        string     `json:"hostname"`
	AgentVersion    string     `json:"agent_version"`
	CoreVersion     string     `json:"core_version"`
	DataDir         string     `json:"data_dir"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	Note            string     `json:"note"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// NodeView combines persisted Node configuration with live runtime telemetry from cluster.Hub.
type NodeView struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	IsLocal           bool       `json:"is_local"`
	Online            bool       `json:"online"`
	CoreRunning       bool       `json:"core_running"`
	CoreVersion       string     `json:"core_version"`
	UpSpeed           int64      `json:"up_speed"`
	DownSpeed         int64      `json:"down_speed"`
	ActiveConnections int        `json:"active_connections"`
	MemBytes          uint64     `json:"mem_bytes"`
	UptimeSec         int64      `json:"uptime_sec"`
	InboundCount      int        `json:"inbound_count"`
	OS                string     `json:"os"`
	Arch              string     `json:"arch"`
	Hostname          string     `json:"hostname"`
	AgentVersion      string     `json:"agent_version"`
	DataDir           string     `json:"data_dir"`
	PublicIPv4        string     `json:"public_ipv4"`
	PublicIPv6        string     `json:"public_ipv6"`
	OverridePatch     string     `json:"override_patch"`
	DefaultOutbound   string     `json:"default_outbound"`
	LogLevel          string     `json:"log_level"`
	GithubProxy       string     `json:"github_proxy"`
	LastSeenAt        *time.Time `json:"last_seen_at,omitempty"`
	Note              string     `json:"note"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// ClusterSummary aggregates live node counts and combined throughput across the entire cluster.
type ClusterSummary struct {
	TotalNodes       int   `json:"total_nodes"`
	OnlineNodes      int   `json:"online_nodes"`
	TotalUpSpeed     int64 `json:"total_up_speed"`
	TotalDownSpeed   int64 `json:"total_down_speed"`
	TotalConnections int   `json:"total_connections"`
}

// Certificate stores ACME, self-signed, or external TLS certificate metadata.
type Certificate struct {
	ID           int64      `json:"id"`
	NodeID       int64      `json:"node_id"`
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
	CertPEM      string     `json:"-"`
	KeyPEM       string     `json:"-"`
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
	NodeID        int64           `json:"node_id"`
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

// UpstreamSub represents an external remote subscription source for server-side outbound groups.
type UpstreamSub struct {
	ID              int64      `json:"id"`
	NodeID          int64      `json:"node_id"` // 0 = global shared across all nodes; >0 = specific node
	Name            string     `json:"name"`
	Tag             string     `json:"tag"`
	URL             string     `json:"url"`
	Enabled         bool       `json:"enabled"`
	IntervalMinutes int        `json:"interval_minutes"`
	IncludeRegex    string     `json:"include_regex"`
	ExcludeRegex    string     `json:"exclude_regex"`
	EnableURLTest   bool       `json:"enable_urltest"`
	TestURL         string     `json:"test_url"`
	ProxyURL        string     `json:"proxy_url"`
	CachedFormat    string     `json:"cached_format"`
	CachedNodesJSON string     `json:"-"`
	NodeCount       int        `json:"node_count"`
	LastFetchedAt   *time.Time `json:"last_fetched_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// OutboundExtra represents manual landing outbounds or one-click Cloudflare WARP WireGuard endpoints.
type OutboundExtra struct {
	ID        int64           `json:"id"`
	NodeID    int64           `json:"node_id"`
	Name      string          `json:"name"`
	Tag       string          `json:"tag"`
	Type      string          `json:"type"` // warp | wireguard | socks | http | raw
	Enabled   bool            `json:"enabled"`
	Settings  json.RawMessage `json:"settings"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// RouteRule represents a node-scoped sing-box routing rule entry ordered by priority.
type RouteRule struct {
	ID            int64     `json:"id"`
	NodeID        int64     `json:"node_id"`
	Name          string    `json:"name"`
	PresetKey     string    `json:"preset_key"`
	Priority      int       `json:"priority"`
	Enabled       bool      `json:"enabled"`
	Action        string    `json:"action"` // route | reject | hijack-dns | sniff
	OutboundTag   string    `json:"outbound_tag"`
	RuleSets      []string  `json:"rule_sets"`
	Domains       []string  `json:"domains"`
	DomainSuffix  []string  `json:"domain_suffix"`
	DomainKeyword []string  `json:"domain_keyword"`
	IPCIDR        []string  `json:"ip_cidr"`
	Protocols     []string  `json:"protocol"`
	Ports         []int     `json:"port"`
	AuthUsers     []string  `json:"auth_user"`
	Inbounds      []string  `json:"inbound"`
	IPIsPrivate   bool      `json:"ip_is_private"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SyncTargetConfig holds full persisted push configuration for a sync target (including secret token).
type SyncTargetConfig struct {
	Token        string `json:"token,omitempty"`
	GistID       string `json:"gist_id,omitempty"`
	Repo         string `json:"repo,omitempty"`
	Branch       string `json:"branch,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
	SubStoreURL  string `json:"sub_store_url,omitempty"`
	SubStoreName string `json:"sub_store_name,omitempty"`
	ProxyURL     string `json:"proxy_url,omitempty"`
	APIBaseURL   string `json:"api_base_url,omitempty"`
}

// SyncTargetPublicConfig is the API-safe configuration view with secret token omitted.
type SyncTargetPublicConfig struct {
	GistID       string `json:"gist_id,omitempty"`
	Repo         string `json:"repo,omitempty"`
	Branch       string `json:"branch,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
	SubStoreURL  string `json:"sub_store_url,omitempty"`
	SubStoreName string `json:"sub_store_name,omitempty"`
	ProxyURL     string `json:"proxy_url,omitempty"`
	APIBaseURL   string `json:"api_base_url,omitempty"`
}

// SyncTarget represents an outbound push destination (GitHub Gist, GitHub Repo, or Sub-Store).
type SyncTarget struct {
	ID            int64            `json:"id"`
	Name          string           `json:"name"`
	Enabled       bool             `json:"enabled"`
	Type          string           `json:"type"`       // github_gist | github_repo | sub_store
	MemberID      int64            `json:"member_id"`
	Format        string           `json:"format"`     // uri | base64 | singbox | clash
	MergeMode     string           `json:"merge_mode"` // replace | merge_by_instance
	ConfigJSON    string           `json:"-"`
	Config        SyncTargetConfig `json:"-"`
	AutoPush      bool             `json:"auto_push"`
	LastPushedAt  *time.Time       `json:"last_pushed_at,omitempty"`
	LastStatus    string           `json:"last_status"`
	LastError     string           `json:"last_error,omitempty"`
	LastRemoteURL string           `json:"last_remote_url,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

// SyncTargetView is the sanitized API response for a sync target (never exposes raw token).
type SyncTargetView struct {
	ID            int64                  `json:"id"`
	Name          string                 `json:"name"`
	Enabled       bool                   `json:"enabled"`
	Type          string                 `json:"type"`
	MemberID      int64                  `json:"member_id"`
	Format        string                 `json:"format"`
	MergeMode     string                 `json:"merge_mode"`
	AutoPush      bool                   `json:"auto_push"`
	HasToken      bool                   `json:"has_token"`
	Config        SyncTargetPublicConfig `json:"config"`
	LastPushedAt  *time.Time             `json:"last_pushed_at,omitempty"`
	LastStatus    string                 `json:"last_status"`
	LastError     string                 `json:"last_error,omitempty"`
	LastRemoteURL string                 `json:"last_remote_url,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

// ToView converts SyncTarget into an API-safe SyncTargetView with secret token masked.
func (t SyncTarget) ToView() SyncTargetView {
	return SyncTargetView{
		ID:        t.ID,
		Name:      t.Name,
		Enabled:   t.Enabled,
		Type:      t.Type,
		MemberID:  t.MemberID,
		Format:    t.Format,
		MergeMode: t.MergeMode,
		AutoPush:  t.AutoPush,
		HasToken:  t.Config.Token != "",
		Config: SyncTargetPublicConfig{
			GistID:       t.Config.GistID,
			Repo:         t.Config.Repo,
			Branch:       t.Config.Branch,
			FilePath:     t.Config.FilePath,
			SubStoreURL:  t.Config.SubStoreURL,
			SubStoreName: t.Config.SubStoreName,
			ProxyURL:     t.Config.ProxyURL,
			APIBaseURL:   t.Config.APIBaseURL,
		},
		LastPushedAt:  t.LastPushedAt,
		LastStatus:    t.LastStatus,
		LastError:     t.LastError,
		LastRemoteURL: t.LastRemoteURL,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
	}
}

// SyncHistory records one outbound push execution attempt.
type SyncHistory struct {
	ID         int64     `json:"id"`
	TargetID   int64     `json:"target_id"`
	TargetName string    `json:"target_name"`
	Success    bool      `json:"success"`
	HTTPStatus int       `json:"http_status"`
	NodeCount  int       `json:"node_count"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

// RemoteBackupConfig persists remote encrypted GitHub backup settings (stored in settings table).
type RemoteBackupConfig struct {
	Enabled       bool   `json:"enabled"`
	Type          string `json:"type"` // github_repo | github_gist
	Token         string `json:"token,omitempty"`
	Repo          string `json:"repo"`
	Branch        string `json:"branch"`
	PathPrefix    string `json:"path_prefix"`
	GistID        string `json:"gist_id"`
	KeepCount     int    `json:"keep_count"`
	IntervalHours int    `json:"interval_hours"`
	Passphrase    string `json:"passphrase,omitempty"`
	ProxyURL      string `json:"proxy_url"`
	APIBaseURL    string `json:"api_base_url,omitempty"`
}

// RemoteBackupConfigView is the sanitized API view of RemoteBackupConfig with secrets omitted.
type RemoteBackupConfigView struct {
	Enabled       bool   `json:"enabled"`
	Type          string `json:"type"`
	Repo          string `json:"repo"`
	Branch        string `json:"branch"`
	PathPrefix    string `json:"path_prefix"`
	GistID        string `json:"gist_id"`
	KeepCount     int    `json:"keep_count"`
	IntervalHours int    `json:"interval_hours"`
	HasToken      bool   `json:"has_token"`
	HasPassphrase bool   `json:"has_passphrase"`
	ProxyURL      string `json:"proxy_url"`
	APIBaseURL    string `json:"api_base_url,omitempty"`
	LastBackupAt  string `json:"last_backup_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

// TrafficStat stores aggregated hourly or daily traffic counters per node, member, and inbound.
type TrafficStat struct {
	ID            int64     `json:"id"`
	NodeID        int64     `json:"node_id"`
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
	NodeID        int64  `json:"node_id"`
	Name          string `json:"name"`
	Tag           string `json:"tag"`
	Protocol      string `json:"protocol"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

// NodeTrafficItem summarizes cumulative traffic for a single node.
type NodeTrafficItem struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	IsLocal       bool   `json:"is_local"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

// TrafficStatsResponse is returned by GET /traffic/stats.
type TrafficStatsResponse struct {
	Range     string               `json:"range"`
	Series    []TrafficSeriesPoint `json:"series"`
	ByMember  []MemberTrafficItem  `json:"by_member"`
	ByInbound []InboundTrafficItem `json:"by_inbound"`
	ByNode    []NodeTrafficItem    `json:"by_node"`
}
