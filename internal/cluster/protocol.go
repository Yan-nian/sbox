package cluster

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/cert"
	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
)

const (
	// maxWSMessageBytes is the maximum WebSocket message size (64 MiB) on both
	// Master and Agent so base64-encoded core.upload_binary payloads pass safely.
	maxWSMessageBytes = 64 << 20

	// maxUploadBinaryBytes caps raw binary upload bytes so base64 JSON fits
	// comfortably below maxWSMessageBytes without severing the WebSocket.
	maxUploadBinaryBytes = 47 << 20

	defaultRemoteLogCap = 1000
	defaultRPCTimeout   = 30 * time.Second
	releasesRPCTimeout  = 60 * time.Second
	longRPCTimeout      = 5 * time.Minute
)

// Package-private timing defaults (overridable in unit tests).
var (
	wsPingInterval         = 15 * time.Second
	wsPongTimeout          = 40 * time.Second
	wsWriteTimeout         = 15 * time.Second
	wsHelloTimeout         = 10 * time.Second
	agentTelemetryInterval = 1 * time.Second
	agentReconnectSchedule = []time.Duration{
		1 * time.Second,
		2 * time.Second,
		5 * time.Second,
		15 * time.Second,
	}
)

// Sentinel errors returned by cluster operations.
var (
	ErrNodeOffline = errors.New("cluster: remote node is offline")
	ErrHubClosed   = errors.New("cluster: hub is closed")
)

// CertFileBundle carries managed certificate PEM files synced between Master & Agent
// (or returned after remote ACME/SelfSigned).
type CertFileBundle struct {
	CertID  int64          `json:"cert_id"`
	CertPEM []byte         `json:"cert_pem,omitempty"`
	KeyPEM  []byte         `json:"key_pem,omitempty"`
	Info    *cert.CertInfo `json:"info,omitempty"`
}

// NodeHello is sent by an Agent immediately after WebSocket connect (and periodically updated).
type NodeHello struct {
	OS           string         `json:"os"`
	Arch         string         `json:"arch"`
	Hostname     string         `json:"hostname"`
	AgentVersion string         `json:"agent_version"`
	DataDir      string         `json:"data_dir"`
	PublicIPv4   string         `json:"public_ipv4,omitempty"`
	PublicIPv6   string         `json:"public_ipv6,omitempty"`
	CoreStatus   coremgr.Status `json:"core_status"`
}

// NodeRuntimeState captures the live runtime status and telemetry of a local or remote node.
type NodeRuntimeState struct {
	Online       bool               `json:"online"`
	OS           string             `json:"os"`
	Arch         string             `json:"arch"`
	Hostname     string             `json:"hostname"`
	AgentVersion string             `json:"agent_version"`
	DataDir      string             `json:"data_dir"`
	MemBytes     uint64             `json:"mem_bytes"`
	UptimeSec    int64              `json:"uptime_sec"`
	Core         coremgr.Status     `json:"core"`
	Metrics      telemetry.Snapshot `json:"metrics"`
	LastSeenAt   time.Time          `json:"last_seen_at"`
}

// NodeController abstracts both Local node (id=1) and Remote Agent nodes seamlessly.
type NodeController interface {
	NodeID() int64
	IsLocal() bool
	IsOnline() bool
	RuntimeState() NodeRuntimeState
	DataDir() string
	CoreStatus(ctx context.Context) (coremgr.Status, error)
	StartCore(ctx context.Context) error
	StopCore(ctx context.Context) error
	RestartCore(ctx context.Context) error
	ApplyConfig(ctx context.Context, cfgJSON []byte, certs []CertFileBundle) error
	ListReleases(ctx context.Context, includePrerelease bool, proxyURL string) ([]coremgr.ReleaseInfo, error)
	UpgradeCore(ctx context.Context, opts coremgr.UpgradeOptions) (coremgr.UpgradeResult, error)
	UploadBinary(ctx context.Context, filename string, data []byte) (coremgr.UpgradeResult, error)
	RollbackCore(ctx context.Context) error
	Snapshot() telemetry.Snapshot
	SubscribeMetrics() (<-chan telemetry.Snapshot, func())
	RecentLogs(limit int) []telemetry.LogEntry
	SubscribeLogs() (<-chan telemetry.LogEntry, func())
	Connections(ctx context.Context) ([]telemetry.ConnectionItem, error)
	CloseConnection(ctx context.Context, id string) error
	ListProxyGroups(ctx context.Context) ([]telemetry.ProxyGroupInfo, error)
	TestProxyDelay(ctx context.Context, proxyName, testURL string, timeoutMS int) (int, error)
	SelectProxy(ctx context.Context, groupName, proxyName string) error
	ProbeRealityTarget(ctx context.Context, target string) (cert.RealityProbeResult, error)
	GenerateSelfSigned(ctx context.Context, certID int64, domains []string, validDays int) (*CertFileBundle, error)
	IssueACME(ctx context.Context, certID int64, opts cert.ACMEOptions) (*CertFileBundle, error)
	InspectCertFiles(ctx context.Context, certPath, keyPath string) (*cert.CertInfo, error)
}

// HubCallbacks wires authentication, lifecycle notifications, and remote traffic accounting into Hub.
type HubCallbacks struct {
	AuthenticateToken   func(token string) (nodeID int64, ok bool)
	OnAgentConnected    func(nodeID int64, hello NodeHello)
	OnAgentDisconnected func(nodeID int64)
	OnTrafficDeltas     func(nodeID int64, deltas []telemetry.TrafficDelta)
}

// AgentConfig configures a reverse-connecting sbox-panel Agent.
type AgentConfig struct {
	MasterURL    string // e.g. "https://master:18080/sbox-xxxx" (auto-derives ws(s)://.../api/v1/agent/ws)
	Token        string
	DataDir      string
	AgentVersion string
	InsecureTLS  bool
}

// Wire protocol message types.
const (
	msgTypeHello        = "hello"
	msgTypeTelemetry    = "telemetry"
	msgTypeLog          = "log"
	msgTypeTrafficDelta = "traffic_delta"
	msgTypeRPCReq       = "rpc_req"
	msgTypeRPCResp      = "rpc_resp"
)

// RPC method names exchanged between Master (RemoteDriver) and Agent.
const (
	rpcCoreStatus          = "core.status"
	rpcCoreStart           = "core.start"
	rpcCoreStop            = "core.stop"
	rpcCoreRestart         = "core.restart"
	rpcCoreApplyConfig     = "core.apply_config"
	rpcCoreListReleases    = "core.list_releases"
	rpcCoreUpgrade         = "core.upgrade"
	rpcCoreUploadBinary    = "core.upload_binary"
	rpcCoreRollback        = "core.rollback"
	MethodCoreProxies      = "core.proxies"
	MethodCoreProxyDelay   = "core.proxy_delay"
	MethodCoreProxySelect  = "core.proxy_select"
	rpcTelemetryConns      = "telemetry.connections"
	rpcTelemetryCloseConn  = "telemetry.close_connection"
	rpcToolsRealityProbe   = "tools.reality_probe"
	rpcCertSelfSigned      = "cert.self_signed"
	rpcCertACME            = "cert.acme"
	rpcCertInspect         = "cert.inspect"
)

// wireEnvelope is the top-level JSON frame sent over the agent WebSocket tunnel.
type wireEnvelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type telemetryFrame struct {
	Snapshot  telemetry.Snapshot `json:"snapshot"`
	Core      coremgr.Status     `json:"core"`
	MemBytes  uint64             `json:"mem_bytes"`
	UptimeSec int64              `json:"uptime_sec"`
}

type trafficDeltaWire struct {
	Inbound  string `json:"inbound"`
	User     string `json:"user"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}

type trafficDeltaFrame struct {
	Deltas []trafficDeltaWire `json:"deltas"`
}

type applyConfigReq struct {
	ConfigJSON []byte           `json:"config_json"`
	Certs      []CertFileBundle `json:"certs,omitempty"`
}

type listReleasesReq struct {
	IncludePrerelease bool   `json:"include_prerelease"`
	ProxyURL          string `json:"proxy_url,omitempty"`
}

type listReleasesResp struct {
	Releases []coremgr.ReleaseInfo `json:"releases"`
}

type uploadBinaryReq struct {
	Filename string `json:"filename"`
	Data     []byte `json:"data"`
}

type connectionsResp struct {
	Connections []telemetry.ConnectionItem `json:"connections"`
}

type closeConnectionReq struct {
	ID string `json:"id"`
}

type coreProxiesResp struct {
	Groups []telemetry.ProxyGroupInfo `json:"groups"`
}

type coreProxyDelayReq struct {
	Proxy     string `json:"proxy"`
	TestURL   string `json:"test_url,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type coreProxyDelayResp struct {
	DelayMS int `json:"delay_ms"`
}

type coreProxySelectReq struct {
	Group string `json:"group"`
	Proxy string `json:"proxy"`
}

type realityProbeReq struct {
	Target string `json:"target"`
}

type selfSignedCertReq struct {
	CertID    int64    `json:"cert_id"`
	Domains   []string `json:"domains"`
	ValidDays int      `json:"valid_days"`
}

type acmeCertReq struct {
	CertID  int64            `json:"cert_id"`
	Options cert.ACMEOptions `json:"options"`
}

type inspectCertReq struct {
	CertPath string `json:"cert_path"`
	KeyPath  string `json:"key_path"`
}

// deriveAgentWSURL converts a Master base URL (e.g. "https://master:18080/sbox-xxxx")
// into the WebSocket endpoint URL ("wss://master:18080/sbox-xxxx/api/v1/agent/ws").
func deriveAgentWSURL(masterURL string) (string, error) {
	raw := strings.TrimSpace(masterURL)
	if raw == "" {
		return "", errors.New("cluster: master_url is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("cluster: invalid master_url %q: %w", masterURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("cluster: master_url %q missing host", masterURL)
	}

	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		u.Scheme = strings.ToLower(u.Scheme)
	default:
		return "", fmt.Errorf("cluster: unsupported master_url scheme %q", u.Scheme)
	}

	cleanPath := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(cleanPath, "/api/v1/agent/ws") {
		cleanPath += "/api/v1/agent/ws"
	}
	u.Path = cleanPath
	u.RawPath = ""
	u.Fragment = ""
	return u.String(), nil
}

func randomHex(numBytes int) string {
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func certDirForID(dataDir string, certID int64) string {
	if certID > 0 {
		return filepath.Join(dataDir, "certs", fmt.Sprintf("cert-%d", certID))
	}
	return filepath.Join(dataDir, "certs", "cert-"+randomHex(4))
}

func acmeAccountDir(dataDir string) string {
	return filepath.Join(dataDir, "certs", "acme-account")
}

// writeCertBundles atomically writes each bundle's certificate and private key PEMs
// under <dataDir>/certs/cert-<CertID>/{fullchain.pem,privkey.pem}.
func writeCertBundles(dataDir string, bundles []CertFileBundle) error {
	for _, b := range bundles {
		if len(b.CertPEM) == 0 && len(b.KeyPEM) == 0 {
			continue
		}
		if b.CertID <= 0 {
			return fmt.Errorf("cluster: invalid cert_id %d in cert bundle", b.CertID)
		}
		dir := certDirForID(dataDir, b.CertID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("cluster: create cert dir for cert-%d: %w", b.CertID, err)
		}
		if len(b.CertPEM) > 0 {
			certPath := filepath.Join(dir, "fullchain.pem")
			if err := writeFileAtomic(certPath, b.CertPEM, 0o644); err != nil {
				return fmt.Errorf("cluster: write fullchain.pem for cert-%d: %w", b.CertID, err)
			}
		}
		if len(b.KeyPEM) > 0 {
			keyPath := filepath.Join(dir, "privkey.pem")
			if err := writeFileAtomic(keyPath, b.KeyPEM, 0o600); err != nil {
				return fmt.Errorf("cluster: write privkey.pem for cert-%d: %w", b.CertID, err)
			}
		}
	}
	return nil
}

// buildCertBundle reads the certificate and key PEM bytes referenced by info.
func buildCertBundle(certID int64, info *cert.CertInfo) (*CertFileBundle, error) {
	if info == nil {
		return nil, errors.New("cluster: cert info is nil")
	}
	certPEM, err := os.ReadFile(info.CertPath)
	if err != nil {
		return nil, fmt.Errorf("cluster: read generated cert %s: %w", info.CertPath, err)
	}
	keyPEM, err := os.ReadFile(info.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("cluster: read generated key %s: %w", info.KeyPath, err)
	}
	return &CertFileBundle{
		CertID:  certID,
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
		Info:    info,
	}, nil
}

// patchConfigClashAPI ensures experimental.clash_api matches the agent's local loopback
// external_controller and secret so telemetry and upgrade health probes always align.
// Non-object or malformed JSON is returned untouched so sing-box check surfaces any syntax error.
func patchConfigClashAPI(cfgJSON []byte, clashAddr, clashSecret string) []byte {
	trimmed := bytes.TrimSpace(cfgJSON)
	if len(trimmed) == 0 || clashAddr == "" {
		return cfgJSON
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return cfgJSON
	}

	exp, ok := doc["experimental"].(map[string]any)
	if !ok || exp == nil {
		exp = make(map[string]any)
		doc["experimental"] = exp
	}

	clashAPI, ok := exp["clash_api"].(map[string]any)
	if !ok || clashAPI == nil {
		clashAPI = make(map[string]any)
		exp["clash_api"] = clashAPI
	}
	clashAPI["external_controller"] = clashAddr
	if clashSecret != "" {
		clashAPI["secret"] = clashSecret
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return cfgJSON
	}
	return buf.Bytes()
}

// extractConfigClashAPI reads experimental.clash_api.{external_controller,secret} if present.
func extractConfigClashAPI(cfgJSON []byte) (addr, secret string) {
	var parsed struct {
		Experimental struct {
			ClashAPI struct {
				ExternalController string `json:"external_controller"`
				Secret             string `json:"secret"`
			} `json:"clash_api"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(cfgJSON, &parsed); err != nil {
		return "", ""
	}
	return strings.TrimSpace(parsed.Experimental.ClashAPI.ExternalController),
		strings.TrimSpace(parsed.Experimental.ClashAPI.Secret)
}

// isBindableLoopback tests whether addr is a loopback host:port that can currently be bound.
func isBindableLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// detectLocalPublicIPs inspects local network interfaces (zero network I/O) to find
// directly assigned global unicast public IPv4/IPv6 addresses if present.
func detectLocalPublicIPs() (ipv4, ipv6 string) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", ""
	}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || !isPublicUnicastIP(ip) {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			if ipv4 == "" {
				ipv4 = v4.String()
			}
		} else if ipv6 == "" {
			ipv6 = ip.String()
		}
	}
	return ipv4, ipv6
}

var (
	_, cgnatNet, _ = net.ParseCIDR("100.64.0.0/10")
	_, doc1Net, _  = net.ParseCIDR("192.0.2.0/24")
	_, doc2Net, _  = net.ParseCIDR("198.51.100.0/24")
	_, doc3Net, _  = net.ParseCIDR("203.0.113.0/24")
	_, doc6Net, _  = net.ParseCIDR("2001:db8::/32")
)

func isPublicUnicastIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || !ip.IsGlobalUnicast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		if cgnatNet.Contains(v4) || doc1Net.Contains(v4) || doc2Net.Contains(v4) || doc3Net.Contains(v4) {
			return false
		}
		return true
	}
	if doc6Net.Contains(ip) {
		return false
	}
	return true
}

func writeFileAtomic(dst string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := replaceFile(tmpName, dst); err != nil {
		return err
	}
	cleanup = false
	_ = os.Chmod(dst, perm)
	return nil
}

func replaceFile(src, dst string) error {
	var lastErr error
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 10
	}
	for i := 0; i < attempts; i++ {
		if err := os.Rename(src, dst); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if runtime.GOOS == "windows" {
			_ = os.Remove(dst)
			if err := os.Rename(src, dst); err == nil {
				return nil
			} else {
				lastErr = err
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	return lastErr
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func mustMarshalRaw(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
