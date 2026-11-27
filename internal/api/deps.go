package api

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/cert"
	"github.com/sbox-panel/sbox-panel/internal/cluster"
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
	ListProxyGroups(ctx context.Context) ([]telemetry.ProxyGroupInfo, error)
	TestProxyDelay(ctx context.Context, proxyName, testURL string, timeoutMS int) (int, error)
	SelectProxy(ctx context.Context, groupName, proxyName string) error
}

// ClusterHub is the narrow interface satisfied by *cluster.Hub.
type ClusterHub interface {
	Get(nodeID int64) (cluster.NodeController, bool)
	IsOnline(nodeID int64) bool
	OnlineNodeIDs() []int64
	RuntimeState(nodeID int64) (cluster.NodeRuntimeState, bool)
	DisconnectNode(nodeID int64)
	HandleAgentWS(w http.ResponseWriter, r *http.Request)
}

// Compile-time assertions guaranteeing concrete packages implement our interfaces.
var (
	_ CoreManager  = (*coremgr.Manager)(nil)
	_ TelemetryHub = (*telemetry.Hub)(nil)
	_ ClusterHub   = (*cluster.Hub)(nil)
)

// ServerConfig holds all dependencies required to construct an API Server.
type ServerConfig struct {
	Store             *store.Store
	Tokens            *auth.TokenManager
	RateLimiter       *auth.LoginRateLimiter
	Hub               ClusterHub
	Core              CoreManager
	Telemetry         TelemetryHub
	StaticFS          fs.FS
	DataDir           string
	PanelVersion      string
	StartedAt         time.Time
	OnPanelCertChange func()
}

// fallbackHub wraps standalone CoreManager + TelemetryHub as local node id=1 when Hub is nil (used in unit tests).
type fallbackHub struct {
	local *fallbackLocalController
}

func newFallbackHub(dataDir, version string, startedAt time.Time, core CoreManager, tel TelemetryHub) *fallbackHub {
	return &fallbackHub{
		local: &fallbackLocalController{
			dataDir:   dataDir,
			version:   version,
			startedAt: startedAt,
			core:      core,
			tel:       tel,
		},
	}
}

func (f *fallbackHub) Get(nodeID int64) (cluster.NodeController, bool) {
	if nodeID == 1 {
		return f.local, true
	}
	return nil, false
}
func (f *fallbackHub) IsOnline(nodeID int64) bool { return nodeID == 1 }
func (f *fallbackHub) OnlineNodeIDs() []int64     { return []int64{1} }
func (f *fallbackHub) RuntimeState(nodeID int64) (cluster.NodeRuntimeState, bool) {
	if nodeID == 1 {
		return f.local.RuntimeState(), true
	}
	return cluster.NodeRuntimeState{}, false
}
func (f *fallbackHub) DisconnectNode(int64) {}
func (f *fallbackHub) HandleAgentWS(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusServiceUnavailable, "cluster hub not configured")
}

type fallbackLocalController struct {
	dataDir   string
	version   string
	startedAt time.Time
	core      CoreManager
	tel       TelemetryHub
}

func (c *fallbackLocalController) NodeID() int64  { return 1 }
func (c *fallbackLocalController) IsLocal() bool  { return true }
func (c *fallbackLocalController) IsOnline() bool { return true }
func (c *fallbackLocalController) DataDir() string {
	return c.dataDir
}
func (c *fallbackLocalController) RuntimeState() cluster.NodeRuntimeState {
	host, _ := os.Hostname()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	var cs coremgr.Status
	if c.core != nil {
		cs = c.core.Status()
	}
	var snap telemetry.Snapshot
	if c.tel != nil {
		snap = c.tel.Snapshot()
	}
	return cluster.NodeRuntimeState{
		Online:       true,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Hostname:     host,
		AgentVersion: c.version,
		DataDir:      c.dataDir,
		MemBytes:     mem.Alloc,
		UptimeSec:    int64(time.Since(c.startedAt).Seconds()),
		Core:         cs,
		Metrics:      snap,
		LastSeenAt:   time.Now().UTC(),
	}
}
func (c *fallbackLocalController) CoreStatus(_ context.Context) (coremgr.Status, error) {
	if c.core == nil {
		return coremgr.Status{}, nil
	}
	return c.core.Status(), nil
}
func (c *fallbackLocalController) StartCore(ctx context.Context) error {
	if c.core == nil {
		return nil
	}
	return c.core.Start(ctx)
}
func (c *fallbackLocalController) StopCore(_ context.Context) error {
	if c.core == nil {
		return nil
	}
	return c.core.Stop()
}
func (c *fallbackLocalController) RestartCore(ctx context.Context) error {
	if c.core == nil {
		return nil
	}
	return c.core.Restart(ctx)
}
func (c *fallbackLocalController) ApplyConfig(ctx context.Context, cfgJSON []byte, certs []cluster.CertFileBundle) error {
	for _, b := range certs {
		if len(b.CertPEM) == 0 || len(b.KeyPEM) == 0 || c.dataDir == "" {
			continue
		}
		dir := filepath.Join(c.dataDir, "certs", "cert-"+strconvFormatInt(b.CertID))
		if err := os.MkdirAll(dir, 0o750); err == nil {
			_ = os.WriteFile(filepath.Join(dir, "fullchain.pem"), b.CertPEM, 0o644)
			_ = os.WriteFile(filepath.Join(dir, "privkey.pem"), b.KeyPEM, 0o600)
		}
	}
	if c.core == nil {
		return nil
	}
	if c.core.Status().Running {
		return c.core.Reload(ctx, cfgJSON)
	}
	return c.core.EnsureConfig(cfgJSON)
}
func (c *fallbackLocalController) ListReleases(ctx context.Context, includePrerelease bool, proxyURL string) ([]coremgr.ReleaseInfo, error) {
	if c.core == nil {
		return []coremgr.ReleaseInfo{}, nil
	}
	return c.core.ListReleases(ctx, includePrerelease, proxyURL)
}
func (c *fallbackLocalController) UpgradeCore(ctx context.Context, opts coremgr.UpgradeOptions) (coremgr.UpgradeResult, error) {
	if c.core == nil {
		return coremgr.UpgradeResult{Success: false, Message: "core unavailable"}, nil
	}
	return c.core.Upgrade(ctx, opts), nil
}
func (c *fallbackLocalController) UploadBinary(ctx context.Context, filename string, data []byte) (coremgr.UpgradeResult, error) {
	if c.core == nil {
		return coremgr.UpgradeResult{Success: false, Message: "core unavailable"}, nil
	}
	return c.core.UploadBinary(ctx, newBytesReader(data), filename), nil
}
func (c *fallbackLocalController) RollbackCore(ctx context.Context) error {
	if c.core == nil {
		return nil
	}
	return c.core.Rollback(ctx)
}
func (c *fallbackLocalController) Snapshot() telemetry.Snapshot {
	if c.tel == nil {
		return telemetry.Snapshot{UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	return c.tel.Snapshot()
}
func (c *fallbackLocalController) SubscribeMetrics() (<-chan telemetry.Snapshot, func()) {
	if c.tel == nil {
		ch := make(chan telemetry.Snapshot)
		return ch, func() { close(ch) }
	}
	return c.tel.SubscribeMetrics()
}
func (c *fallbackLocalController) RecentLogs(limit int) []telemetry.LogEntry {
	if c.tel == nil {
		return []telemetry.LogEntry{}
	}
	return c.tel.RecentLogs(limit)
}
func (c *fallbackLocalController) SubscribeLogs() (<-chan telemetry.LogEntry, func()) {
	if c.tel == nil {
		ch := make(chan telemetry.LogEntry)
		return ch, func() { close(ch) }
	}
	return c.tel.SubscribeLogs()
}
func (c *fallbackLocalController) Connections(ctx context.Context) ([]telemetry.ConnectionItem, error) {
	if c.tel == nil {
		return []telemetry.ConnectionItem{}, nil
	}
	return c.tel.Connections(ctx)
}
func (c *fallbackLocalController) CloseConnection(ctx context.Context, id string) error {
	if c.tel == nil {
		return nil
	}
	return c.tel.CloseConnection(ctx, id)
}
func (c *fallbackLocalController) ListProxyGroups(ctx context.Context) ([]telemetry.ProxyGroupInfo, error) {
	if c.tel == nil {
		return []telemetry.ProxyGroupInfo{}, nil
	}
	return c.tel.ListProxyGroups(ctx)
}
func (c *fallbackLocalController) TestProxyDelay(ctx context.Context, proxyName, testURL string, timeoutMS int) (int, error) {
	if c.tel == nil {
		return 0, nil
	}
	return c.tel.TestProxyDelay(ctx, proxyName, testURL, timeoutMS)
}
func (c *fallbackLocalController) SelectProxy(ctx context.Context, groupName, proxyName string) error {
	if c.tel == nil {
		return nil
	}
	return c.tel.SelectProxy(ctx, groupName, proxyName)
}
func (c *fallbackLocalController) ProbeRealityTarget(ctx context.Context, target string) (cert.RealityProbeResult, error) {
	return cert.ProbeRealityTarget(ctx, target), nil
}
func (c *fallbackLocalController) GenerateSelfSigned(_ context.Context, certID int64, domains []string, validDays int) (*cluster.CertFileBundle, error) {
	base := c.dataDir
	if base == "" {
		base = "./data"
	}
	certDir := filepath.Join(base, "certs", "cert-"+strconvFormatInt(certID))
	info, err := cert.GenerateSelfSigned(cert.SelfSignedOptions{
		CertDir:   certDir,
		Domains:   domains,
		ValidDays: validDays,
	})
	if err != nil {
		return nil, err
	}
	certPEM, _ := os.ReadFile(info.CertPath)
	keyPEM, _ := os.ReadFile(info.KeyPath)
	return &cluster.CertFileBundle{
		CertID:  certID,
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
		Info:    info,
	}, nil
}
func (c *fallbackLocalController) IssueACME(ctx context.Context, certID int64, opts cert.ACMEOptions) (*cluster.CertFileBundle, error) {
	base := c.dataDir
	if base == "" {
		base = "./data"
	}
	if opts.CertDir == "" {
		opts.CertDir = filepath.Join(base, "certs", "cert-"+strconvFormatInt(certID))
	}
	if opts.AccountDir == "" {
		opts.AccountDir = filepath.Join(base, "certs", "acme-account")
	}
	info, err := cert.IssueACME(ctx, opts)
	if err != nil {
		return nil, err
	}
	certPEM, _ := os.ReadFile(info.CertPath)
	keyPEM, _ := os.ReadFile(info.KeyPath)
	return &cluster.CertFileBundle{
		CertID:  certID,
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
		Info:    info,
	}, nil
}
func (c *fallbackLocalController) InspectCertFiles(_ context.Context, certPath, keyPath string) (*cert.CertInfo, error) {
	return cert.InspectCertFiles(certPath, keyPath)
}
func (c *fallbackLocalController) CheckLocalPort(ctx context.Context, network string, port int) (cluster.LocalPortCheckResult, error) {
	return cluster.CheckLocalPort(ctx, network, port)
}
func (c *fallbackLocalController) ProbeExternalPort(ctx context.Context, req cluster.ExternalPortProbeRequest) (cluster.ExternalPortProbeResult, error) {
	return cluster.ProbeExternalPort(ctx, req)
}
