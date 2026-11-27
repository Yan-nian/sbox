package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sbox-panel/sbox-panel/internal/cert"
	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
)

// Compile-time interface assertions.
var (
	_ NodeController = (*LocalDriver)(nil)
	_ NodeController = (*RemoteDriver)(nil)
)

// LocalDriver implements NodeController by directly invoking the Master's local
// coremgr.Manager, telemetry.Hub, and cert helpers.
type LocalDriver struct {
	nodeID       int64
	dataDir      string
	panelVersion string
	startedAt    time.Time
	core         *coremgr.Manager
	tel          *telemetry.Hub
}

// NewLocalDriver constructs a LocalDriver for the Master's embedded local node (typically nodeID=1).
func NewLocalDriver(nodeID int64, dataDir, panelVersion string, core *coremgr.Manager, tel *telemetry.Hub) *LocalDriver {
	if nodeID <= 0 {
		nodeID = 1
	}
	dir := strings.TrimSpace(dataDir)
	if dir == "" {
		dir = "./data"
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &LocalDriver{
		nodeID:       nodeID,
		dataDir:      dir,
		panelVersion: panelVersion,
		startedAt:    time.Now().UTC(),
		core:         core,
		tel:          tel,
	}
}

func (d *LocalDriver) NodeID() int64   { return d.nodeID }
func (d *LocalDriver) IsLocal() bool   { return true }
func (d *LocalDriver) IsOnline() bool  { return true }
func (d *LocalDriver) DataDir() string { return d.dataDir }

func (d *LocalDriver) RuntimeState() NodeRuntimeState {
	hostname, _ := os.Hostname()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	var coreStatus coremgr.Status
	if d.core != nil {
		coreStatus = d.core.Status()
	}
	var snap telemetry.Snapshot
	if d.tel != nil {
		snap = d.tel.Snapshot()
	} else {
		snap.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	uptime := int64(time.Since(d.startedAt).Seconds())
	if uptime < 0 {
		uptime = 0
	}

	return NodeRuntimeState{
		Online:       true,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Hostname:     hostname,
		AgentVersion: d.panelVersion,
		DataDir:      d.dataDir,
		MemBytes:     mem.Alloc,
		UptimeSec:    uptime,
		Core:         coreStatus,
		Metrics:      snap,
		LastSeenAt:   time.Now().UTC(),
	}
}

func (d *LocalDriver) CoreStatus(ctx context.Context) (coremgr.Status, error) {
	if ctx != nil && ctx.Err() != nil {
		return coremgr.Status{}, ctx.Err()
	}
	if d.core == nil {
		return coremgr.Status{}, errors.New("core manager not initialized")
	}
	return d.core.Status(), nil
}

func (d *LocalDriver) StartCore(ctx context.Context) error {
	if d.core == nil {
		return errors.New("core manager not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return d.core.Start(ctx)
}

func (d *LocalDriver) StopCore(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if d.core == nil {
		return errors.New("core manager not initialized")
	}
	return d.core.Stop()
}

func (d *LocalDriver) RestartCore(ctx context.Context) error {
	if d.core == nil {
		return errors.New("core manager not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return d.core.Restart(ctx)
}

func (d *LocalDriver) ApplyConfig(ctx context.Context, cfgJSON []byte, certs []CertFileBundle) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if len(certs) > 0 {
		if err := writeCertBundles(d.dataDir, certs); err != nil {
			return err
		}
	}
	if len(bytes.TrimSpace(cfgJSON)) == 0 {
		return nil
	}
	if d.core == nil {
		return errors.New("core manager not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if d.core.Status().Running {
		return d.core.Reload(ctx, cfgJSON)
	}
	return d.core.EnsureConfig(cfgJSON)
}

func (d *LocalDriver) ListReleases(ctx context.Context, includePrerelease bool, proxyURL string) ([]coremgr.ReleaseInfo, error) {
	if d.core == nil {
		return nil, errors.New("core manager not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return d.core.ListReleases(ctx, includePrerelease, proxyURL)
}

func (d *LocalDriver) UpgradeCore(ctx context.Context, opts coremgr.UpgradeOptions) (coremgr.UpgradeResult, error) {
	if d.core == nil {
		return coremgr.UpgradeResult{}, errors.New("core manager not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return d.core.Upgrade(ctx, opts), nil
}

func (d *LocalDriver) UploadBinary(ctx context.Context, filename string, data []byte) (coremgr.UpgradeResult, error) {
	if d.core == nil {
		return coremgr.UpgradeResult{}, errors.New("core manager not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return d.core.UploadBinary(ctx, bytes.NewReader(data), filename), nil
}

func (d *LocalDriver) RollbackCore(ctx context.Context) error {
	if d.core == nil {
		return errors.New("core manager not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return d.core.Rollback(ctx)
}

func (d *LocalDriver) Snapshot() telemetry.Snapshot {
	if d.tel == nil {
		return telemetry.Snapshot{UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	return d.tel.Snapshot()
}

func (d *LocalDriver) SubscribeMetrics() (<-chan telemetry.Snapshot, func()) {
	if d.tel == nil {
		ch := make(chan telemetry.Snapshot)
		close(ch)
		return ch, func() {}
	}
	return d.tel.SubscribeMetrics()
}

func (d *LocalDriver) RecentLogs(limit int) []telemetry.LogEntry {
	if d.tel == nil {
		return []telemetry.LogEntry{}
	}
	return d.tel.RecentLogs(limit)
}

func (d *LocalDriver) SubscribeLogs() (<-chan telemetry.LogEntry, func()) {
	if d.tel == nil {
		ch := make(chan telemetry.LogEntry)
		close(ch)
		return ch, func() {}
	}
	return d.tel.SubscribeLogs()
}

func (d *LocalDriver) Connections(ctx context.Context) ([]telemetry.ConnectionItem, error) {
	if d.tel == nil {
		return []telemetry.ConnectionItem{}, errors.New("telemetry hub unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return d.tel.Connections(ctx)
}

func (d *LocalDriver) CloseConnection(ctx context.Context, id string) error {
	if d.tel == nil {
		return errors.New("telemetry hub unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return d.tel.CloseConnection(ctx, id)
}

func (d *LocalDriver) ProbeRealityTarget(ctx context.Context, target string) (cert.RealityProbeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return cert.ProbeRealityTarget(ctx, target), nil
}

func (d *LocalDriver) GenerateSelfSigned(ctx context.Context, certID int64, domains []string, validDays int) (*CertFileBundle, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	certDir := certDirForID(d.dataDir, certID)
	info, err := cert.GenerateSelfSigned(cert.SelfSignedOptions{
		CertDir:   certDir,
		Domains:   domains,
		ValidDays: validDays,
	})
	if err != nil {
		return nil, err
	}
	return buildCertBundle(certID, info)
}

func (d *LocalDriver) IssueACME(ctx context.Context, certID int64, opts cert.ACMEOptions) (*CertFileBundle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(opts.CertDir) == "" {
		opts.CertDir = certDirForID(d.dataDir, certID)
	}
	if strings.TrimSpace(opts.AccountDir) == "" {
		opts.AccountDir = acmeAccountDir(d.dataDir)
	}
	info, err := cert.IssueACME(ctx, opts)
	if err != nil {
		return nil, err
	}
	return buildCertBundle(certID, info)
}

func (d *LocalDriver) InspectCertFiles(ctx context.Context, certPath, keyPath string) (*cert.CertInfo, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return cert.InspectCertFiles(certPath, keyPath)
}

// --- RemoteDriver (WebSocket RPC Multiplexer + Telemetry/Log Receiver) ---

type rpcResult struct {
	payload json.RawMessage
	err     error
}

// RemoteDriver implements NodeController over an authenticated reverse WebSocket tunnel.
type RemoteDriver struct {
	nodeID          int64
	conn            *websocket.Conn
	onTrafficDeltas func(nodeID int64, deltas []telemetry.TrafficDelta)

	// writeMu serializes all WebSocket data writes (gorilla/websocket requirement).
	writeMu sync.Mutex

	// stateMu guards runtime metadata, telemetry snapshot, and online state.
	stateMu sync.RWMutex
	online  bool
	state   NodeRuntimeState

	// RPC multiplexing.
	reqSeq    atomic.Uint64
	pendingMu sync.Mutex
	pending   map[string]chan rpcResult
	closed    bool
	closeErr  error
	doneCh    chan struct{}
	closeOnce sync.Once

	// Remote log ring buffer.
	logMu    sync.RWMutex
	logBuf   []telemetry.LogEntry
	logCap   int
	logHead  int
	logCount int

	// Live pub/sub subscribers.
	logSubMu   sync.RWMutex
	logSubs    map[uint64]chan telemetry.LogEntry
	nextLogSub uint64
	logSubDone bool

	metricSubMu   sync.RWMutex
	metricSubs    map[uint64]chan telemetry.Snapshot
	nextMetricSub uint64
	metricSubDone bool
}

func newRemoteDriver(nodeID int64, conn *websocket.Conn, hello NodeHello, onDeltas func(int64, []telemetry.TrafficDelta)) *RemoteDriver {
	now := time.Now().UTC()
	return &RemoteDriver{
		nodeID:          nodeID,
		conn:            conn,
		onTrafficDeltas: onDeltas,
		online:          true,
		state: NodeRuntimeState{
			Online:       true,
			OS:           hello.OS,
			Arch:         hello.Arch,
			Hostname:     hello.Hostname,
			AgentVersion: hello.AgentVersion,
			DataDir:      hello.DataDir,
			Core:         hello.CoreStatus,
			Metrics:      telemetry.Snapshot{UpdatedAt: now.Format(time.RFC3339)},
			LastSeenAt:   now,
		},
		pending:    make(map[string]chan rpcResult),
		doneCh:     make(chan struct{}),
		logBuf:     make([]telemetry.LogEntry, defaultRemoteLogCap),
		logCap:     defaultRemoteLogCap,
		logSubs:    make(map[uint64]chan telemetry.LogEntry),
		metricSubs: make(map[uint64]chan telemetry.Snapshot),
	}
}

func (r *RemoteDriver) NodeID() int64 { return r.nodeID }
func (r *RemoteDriver) IsLocal() bool { return false }

func (r *RemoteDriver) IsOnline() bool {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.online
}

func (r *RemoteDriver) DataDir() string {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.state.DataDir
}

func (r *RemoteDriver) RuntimeState() NodeRuntimeState {
	r.stateMu.RLock()
	st := r.state
	st.Online = r.online
	r.stateMu.RUnlock()
	if st.Metrics.UpdatedAt == "" {
		st.Metrics.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return st
}

func (r *RemoteDriver) Snapshot() telemetry.Snapshot {
	r.stateMu.RLock()
	snap := r.state.Metrics
	r.stateMu.RUnlock()
	if snap.UpdatedAt == "" {
		snap.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return snap
}

func (r *RemoteDriver) SubscribeMetrics() (<-chan telemetry.Snapshot, func()) {
	r.metricSubMu.Lock()
	if r.metricSubDone {
		r.metricSubMu.Unlock()
		ch := make(chan telemetry.Snapshot)
		close(ch)
		return ch, func() {}
	}
	ch := make(chan telemetry.Snapshot, 16)
	id := r.nextMetricSub
	r.nextMetricSub++
	r.metricSubs[id] = ch
	r.metricSubMu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			r.metricSubMu.Lock()
			if existing, ok := r.metricSubs[id]; ok {
				delete(r.metricSubs, id)
				close(existing)
			}
			r.metricSubMu.Unlock()
		})
	}
	return ch, unsub
}

func (r *RemoteDriver) RecentLogs(limit int) []telemetry.LogEntry {
	r.logMu.RLock()
	defer r.logMu.RUnlock()

	if r.logCount == 0 {
		return []telemetry.LogEntry{}
	}
	n := r.logCount
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]telemetry.LogEntry, n)
	start := (r.logHead - n + r.logCap) % r.logCap
	for i := 0; i < n; i++ {
		out[i] = r.logBuf[(start+i)%r.logCap]
	}
	return out
}

func (r *RemoteDriver) SubscribeLogs() (<-chan telemetry.LogEntry, func()) {
	r.logSubMu.Lock()
	if r.logSubDone {
		r.logSubMu.Unlock()
		ch := make(chan telemetry.LogEntry)
		close(ch)
		return ch, func() {}
	}
	ch := make(chan telemetry.LogEntry, 128)
	id := r.nextLogSub
	r.nextLogSub++
	r.logSubs[id] = ch
	r.logSubMu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			r.logSubMu.Lock()
			if existing, ok := r.logSubs[id]; ok {
				delete(r.logSubs, id)
				close(existing)
			}
			r.logSubMu.Unlock()
		})
	}
	return ch, unsub
}

// --- Remote RPC Methods ---

func (r *RemoteDriver) CoreStatus(ctx context.Context) (coremgr.Status, error) {
	var st coremgr.Status
	if err := r.call(ctx, rpcCoreStatus, nil, &st, defaultRPCTimeout); err != nil {
		return coremgr.Status{}, err
	}
	r.updateCoreStatus(st)
	return st, nil
}

func (r *RemoteDriver) StartCore(ctx context.Context) error {
	var st coremgr.Status
	if err := r.call(ctx, rpcCoreStart, nil, &st, defaultRPCTimeout); err != nil {
		return err
	}
	r.updateCoreStatus(st)
	return nil
}

func (r *RemoteDriver) StopCore(ctx context.Context) error {
	var st coremgr.Status
	if err := r.call(ctx, rpcCoreStop, nil, &st, defaultRPCTimeout); err != nil {
		return err
	}
	r.updateCoreStatus(st)
	return nil
}

func (r *RemoteDriver) RestartCore(ctx context.Context) error {
	var st coremgr.Status
	if err := r.call(ctx, rpcCoreRestart, nil, &st, defaultRPCTimeout); err != nil {
		return err
	}
	r.updateCoreStatus(st)
	return nil
}

func (r *RemoteDriver) ApplyConfig(ctx context.Context, cfgJSON []byte, certs []CertFileBundle) error {
	var st coremgr.Status
	req := applyConfigReq{
		ConfigJSON: cfgJSON,
		Certs:      certs,
	}
	if err := r.call(ctx, rpcCoreApplyConfig, req, &st, defaultRPCTimeout); err != nil {
		return err
	}
	r.updateCoreStatus(st)
	return nil
}

func (r *RemoteDriver) ListReleases(ctx context.Context, includePrerelease bool, proxyURL string) ([]coremgr.ReleaseInfo, error) {
	var resp listReleasesResp
	req := listReleasesReq{
		IncludePrerelease: includePrerelease,
		ProxyURL:          proxyURL,
	}
	if err := r.call(ctx, rpcCoreListReleases, req, &resp, releasesRPCTimeout); err != nil {
		return nil, err
	}
	if resp.Releases == nil {
		return []coremgr.ReleaseInfo{}, nil
	}
	return resp.Releases, nil
}

func (r *RemoteDriver) UpgradeCore(ctx context.Context, opts coremgr.UpgradeOptions) (coremgr.UpgradeResult, error) {
	var res coremgr.UpgradeResult
	if err := r.call(ctx, rpcCoreUpgrade, opts, &res, longRPCTimeout); err != nil {
		return coremgr.UpgradeResult{}, err
	}
	return res, nil
}

func (r *RemoteDriver) UploadBinary(ctx context.Context, filename string, data []byte) (coremgr.UpgradeResult, error) {
	if len(data) > maxUploadBinaryBytes {
		return coremgr.UpgradeResult{}, fmt.Errorf("cluster: binary upload size %d bytes exceeds remote WebSocket limit (%d bytes)", len(data), maxUploadBinaryBytes)
	}
	var res coremgr.UpgradeResult
	req := uploadBinaryReq{
		Filename: filename,
		Data:     data,
	}
	if err := r.call(ctx, rpcCoreUploadBinary, req, &res, longRPCTimeout); err != nil {
		return coremgr.UpgradeResult{}, err
	}
	return res, nil
}

func (r *RemoteDriver) RollbackCore(ctx context.Context) error {
	var st coremgr.Status
	if err := r.call(ctx, rpcCoreRollback, nil, &st, defaultRPCTimeout); err != nil {
		return err
	}
	r.updateCoreStatus(st)
	return nil
}

func (r *RemoteDriver) Connections(ctx context.Context) ([]telemetry.ConnectionItem, error) {
	var resp connectionsResp
	if err := r.call(ctx, rpcTelemetryConns, nil, &resp, defaultRPCTimeout); err != nil {
		return nil, err
	}
	if resp.Connections == nil {
		return []telemetry.ConnectionItem{}, nil
	}
	return resp.Connections, nil
}

func (r *RemoteDriver) CloseConnection(ctx context.Context, id string) error {
	return r.call(ctx, rpcTelemetryCloseConn, closeConnectionReq{ID: id}, nil, defaultRPCTimeout)
}

func (r *RemoteDriver) ProbeRealityTarget(ctx context.Context, target string) (cert.RealityProbeResult, error) {
	var res cert.RealityProbeResult
	if err := r.call(ctx, rpcToolsRealityProbe, realityProbeReq{Target: target}, &res, defaultRPCTimeout); err != nil {
		return cert.RealityProbeResult{}, err
	}
	return res, nil
}

func (r *RemoteDriver) GenerateSelfSigned(ctx context.Context, certID int64, domains []string, validDays int) (*CertFileBundle, error) {
	var bundle CertFileBundle
	req := selfSignedCertReq{
		CertID:    certID,
		Domains:   domains,
		ValidDays: validDays,
	}
	if err := r.call(ctx, rpcCertSelfSigned, req, &bundle, defaultRPCTimeout); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (r *RemoteDriver) IssueACME(ctx context.Context, certID int64, opts cert.ACMEOptions) (*CertFileBundle, error) {
	var bundle CertFileBundle
	req := acmeCertReq{
		CertID:  certID,
		Options: opts,
	}
	if err := r.call(ctx, rpcCertACME, req, &bundle, longRPCTimeout); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (r *RemoteDriver) InspectCertFiles(ctx context.Context, certPath, keyPath string) (*cert.CertInfo, error) {
	var info cert.CertInfo
	req := inspectCertReq{
		CertPath: certPath,
		KeyPath:  keyPath,
	}
	if err := r.call(ctx, rpcCertInspect, req, &info, defaultRPCTimeout); err != nil {
		return nil, err
	}
	return &info, nil
}

// --- Internal Transport & Stream Loops ---

func (r *RemoteDriver) call(ctx context.Context, method string, reqPayload any, respOut any, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	rawPayload, err := mustMarshalRaw(reqPayload)
	if err != nil {
		return fmt.Errorf("cluster: marshal rpc %s request: %w", method, err)
	}

	id := strconv.FormatUint(r.reqSeq.Add(1), 10)
	respCh := make(chan rpcResult, 1)

	r.pendingMu.Lock()
	if r.closed {
		err := r.closeErr
		if err == nil {
			err = ErrNodeOffline
		}
		r.pendingMu.Unlock()
		return err
	}
	r.pending[id] = respCh
	r.pendingMu.Unlock()

	cleanupPending := func() {
		r.pendingMu.Lock()
		delete(r.pending, id)
		r.pendingMu.Unlock()
	}

	env := wireEnvelope{
		Type:    msgTypeRPCReq,
		ID:      id,
		Method:  method,
		Payload: rawPayload,
	}
	if err := r.writeJSON(env); err != nil {
		cleanupPending()
		return fmt.Errorf("cluster: send rpc %s to node %d: %w", method, r.nodeID, err)
	}

	select {
	case res := <-respCh:
		if res.err != nil {
			return res.err
		}
		if respOut != nil && len(res.payload) > 0 {
			if err := json.Unmarshal(res.payload, respOut); err != nil {
				return fmt.Errorf("cluster: decode rpc %s response: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		cleanupPending()
		return ctx.Err()
	}
}

func (r *RemoteDriver) writeJSON(v any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	_ = r.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return r.conn.WriteJSON(v)
}

func (r *RemoteDriver) pingLoop() {
	interval := wsPingInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.doneCh:
			return
		case <-ticker.C:
			r.writeMu.Lock()
			err := r.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(wsWriteTimeout))
			r.writeMu.Unlock()
			if err != nil {
				r.close(fmt.Errorf("cluster: ping node %d failed: %w", r.nodeID, err))
				return
			}
		}
	}
}

func (r *RemoteDriver) readLoop() {
	r.conn.SetReadLimit(maxWSMessageBytes)
	_ = r.conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	r.conn.SetPongHandler(func(string) error {
		_ = r.conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
		r.touchLastSeen()
		return nil
	})
	r.conn.SetPingHandler(func(appData string) error {
		_ = r.conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
		r.touchLastSeen()
		return r.conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(wsWriteTimeout))
	})

	for {
		_, data, err := r.conn.ReadMessage()
		if err != nil {
			r.close(fmt.Errorf("%w: %v", ErrNodeOffline, err))
			return
		}
		_ = r.conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
		r.touchLastSeen()

		var env wireEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		r.dispatchInbound(env)
	}
}

func (r *RemoteDriver) dispatchInbound(env wireEnvelope) {
	switch env.Type {
	case msgTypeRPCResp:
		r.pendingMu.Lock()
		ch, ok := r.pending[env.ID]
		if ok {
			delete(r.pending, env.ID)
		}
		r.pendingMu.Unlock()
		if ok {
			var err error
			if env.Error != "" {
				err = errors.New(env.Error)
			}
			ch <- rpcResult{payload: env.Payload, err: err}
		}

	case msgTypeHello:
		var hello NodeHello
		if err := json.Unmarshal(env.Payload, &hello); err != nil {
			return
		}
		r.stateMu.Lock()
		r.state.OS = hello.OS
		r.state.Arch = hello.Arch
		r.state.Hostname = hello.Hostname
		r.state.AgentVersion = hello.AgentVersion
		r.state.DataDir = hello.DataDir
		r.state.Core = hello.CoreStatus
		r.state.LastSeenAt = time.Now().UTC()
		r.stateMu.Unlock()

	case msgTypeTelemetry:
		var frame telemetryFrame
		if err := json.Unmarshal(env.Payload, &frame); err != nil {
			return
		}
		if frame.Snapshot.UpdatedAt == "" {
			frame.Snapshot.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		r.stateMu.Lock()
		r.state.Metrics = frame.Snapshot
		r.state.Core = frame.Core
		r.state.MemBytes = frame.MemBytes
		r.state.UptimeSec = frame.UptimeSec
		r.state.LastSeenAt = time.Now().UTC()
		r.stateMu.Unlock()

		r.metricSubMu.RLock()
		for _, ch := range r.metricSubs {
			select {
			case ch <- frame.Snapshot:
			default:
			}
		}
		r.metricSubMu.RUnlock()

	case msgTypeLog:
		var entry telemetry.LogEntry
		if err := json.Unmarshal(env.Payload, &entry); err != nil {
			return
		}
		if entry.Time.IsZero() {
			entry.Time = time.Now().UTC()
		}
		r.appendLog(entry)

	case msgTypeTrafficDelta:
		if r.onTrafficDeltas == nil {
			return
		}
		var frame trafficDeltaFrame
		if err := json.Unmarshal(env.Payload, &frame); err != nil || len(frame.Deltas) == 0 {
			return
		}
		deltas := make([]telemetry.TrafficDelta, 0, len(frame.Deltas))
		for _, item := range frame.Deltas {
			if item.Upload <= 0 && item.Download <= 0 {
				continue
			}
			deltas = append(deltas, telemetry.TrafficDelta{
				Inbound:  item.Inbound,
				User:     item.User,
				Upload:   item.Upload,
				Download: item.Download,
			})
		}
		if len(deltas) > 0 {
			r.onTrafficDeltas(r.nodeID, deltas)
		}
	}
}

func (r *RemoteDriver) appendLog(entry telemetry.LogEntry) {
	r.logMu.Lock()
	r.logBuf[r.logHead] = entry
	r.logHead = (r.logHead + 1) % r.logCap
	if r.logCount < r.logCap {
		r.logCount++
	}
	r.logMu.Unlock()

	r.logSubMu.RLock()
	for _, ch := range r.logSubs {
		select {
		case ch <- entry:
		default:
		}
	}
	r.logSubMu.RUnlock()
}

func (r *RemoteDriver) updateCoreStatus(st coremgr.Status) {
	r.stateMu.Lock()
	r.state.Core = st
	r.state.LastSeenAt = time.Now().UTC()
	r.stateMu.Unlock()
}

func (r *RemoteDriver) touchLastSeen() {
	r.stateMu.Lock()
	r.state.LastSeenAt = time.Now().UTC()
	r.stateMu.Unlock()
}

func (r *RemoteDriver) close(reason error) {
	r.closeOnce.Do(func() {
		if reason == nil {
			reason = ErrNodeOffline
		}

		r.stateMu.Lock()
		r.online = false
		r.state.Online = false
		r.stateMu.Unlock()

		r.pendingMu.Lock()
		r.closed = true
		r.closeErr = reason
		for id, ch := range r.pending {
			ch <- rpcResult{err: reason}
			delete(r.pending, id)
		}
		r.pendingMu.Unlock()

		r.logSubMu.Lock()
		r.logSubDone = true
		for id, ch := range r.logSubs {
			delete(r.logSubs, id)
			close(ch)
		}
		r.logSubMu.Unlock()

		r.metricSubMu.Lock()
		r.metricSubDone = true
		for id, ch := range r.metricSubs {
			delete(r.metricSubs, id)
			close(ch)
		}
		r.metricSubMu.Unlock()

		close(r.doneCh)
		_ = r.conn.Close()
	})
}
