package api

import (
	"context"
	"io"
	"io/fs"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/store"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
)

// CoreManager is the narrow interface satisfied by *coremgr.Manager.
type CoreManager interface {
	Status() coremgr.Status
	ClashAPIConfig() (addr, secret string)
	EnsureConfig(cfg []byte) error
	CheckConfig(binaryPath, configPath string) (string, error)
	Start(ctx context.Context) error
	Stop() error
	Restart(ctx context.Context) error
	Reload(ctx context.Context, newConfig []byte) error
	ListReleases(ctx context.Context, includePrerelease bool, proxyURL string) ([]coremgr.ReleaseInfo, error)
	Upgrade(ctx context.Context, opts coremgr.UpgradeOptions) coremgr.UpgradeResult
	UploadBinary(ctx context.Context, r io.Reader, filename string) coremgr.UpgradeResult
	Rollback(ctx context.Context) error
	DetectBinaryVersion(binaryPath string) (string, error)
}

// TelemetryHub is the narrow interface satisfied by *telemetry.Hub.
type TelemetryHub interface {
	PublishLog(level, source, msg string)
	RecentLogs(limit int) []telemetry.LogEntry
	SubscribeLogs() (<-chan telemetry.LogEntry, func())
	Snapshot() telemetry.Snapshot
	SubscribeMetrics() (<-chan telemetry.Snapshot, func())
	Connections(ctx context.Context) ([]telemetry.ConnectionItem, error)
	CloseConnection(ctx context.Context, id string) error
}

// Compile-time assertions guaranteeing concrete packages implement our interfaces.
var (
	_ CoreManager  = (*coremgr.Manager)(nil)
	_ TelemetryHub = (*telemetry.Hub)(nil)
)

// ServerConfig holds all dependencies required to construct an API Server.
type ServerConfig struct {
	Store             *store.Store
	Tokens            *auth.TokenManager
	RateLimiter       *auth.LoginRateLimiter
	Core              CoreManager
	Telemetry         TelemetryHub
	StaticFS          fs.FS
	DataDir           string
	PanelVersion      string
	StartedAt         time.Time
	OnPanelCertChange func()
}
