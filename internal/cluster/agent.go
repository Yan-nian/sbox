package cluster

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sbox-panel/sbox-panel/internal/cert"
	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
)

const (
	maxConcurrentAgentRPCs = 32
	maxPendingDeltaKeys    = 4096
)

type agentOptions struct {
	clashAPIAddrOverride string
	clashSecretOverride  string
	reconnectSchedule    []time.Duration
	telemetryInterval    time.Duration
}

type deltaKey struct {
	inbound string
	user    string
}

type agentRunner struct {
	cfg               AgentConfig
	wsURL             string
	dataDir           string
	runConfigPath     string
	startedAt         time.Time
	publicIPv4        string
	publicIPv6        string
	coreMgr           *coremgr.Manager
	telHub            *telemetry.Hub
	dialer            *websocket.Dialer
	reconnectSchedule []time.Duration
	telemetryInterval time.Duration

	sessionMu sync.Mutex
	session   *agentSession

	deltaMu       sync.Mutex
	pendingDeltas map[deltaKey]telemetry.TrafficDelta
}

type agentSession struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (s *agentSession) writeJSON(v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return s.conn.WriteJSON(v)
}

func (s *agentSession) writePing() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(wsWriteTimeout))
}

// RunAgent starts a reverse-connecting managed Agent under cfg.DataDir.
//
// Control/Data Plane Decoupling:
//   - Initializes local coremgr.Manager and telemetry.Hub immediately.
//   - If <DataDir>/run/config.json (or <DataDir>/config.json) AND the sing-box binary
//     already exist on disk, sing-box is started immediately without waiting for Master.
//   - Maintains an infinite exponential-backoff reconnect loop (1s -> 2s -> 5s -> 15s)
//     until ctx is canceled. Master disconnects or restarts NEVER stop the running core.
func RunAgent(ctx context.Context, cfg AgentConfig) error {
	return runAgent(ctx, cfg, agentOptions{})
}

func runAgent(ctx context.Context, cfg AgentConfig, opts agentOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}

	wsURL, err := deriveAgentWSURL(cfg.MasterURL)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return errors.New("cluster: agent token is required")
	}

	dataDir := strings.TrimSpace(cfg.DataDir)
	if dataDir == "" {
		dataDir = "./data"
	}
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}

	agentVersion := strings.TrimSpace(cfg.AgentVersion)
	if agentVersion == "" {
		agentVersion = "dev"
	}
	cfg.Token = token
	cfg.DataDir = dataDir
	cfg.AgentVersion = agentVersion

	// Ensure standard Agent persistence layout exists.
	for _, sub := range []string{"bin", "run", "certs", "cache"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o755); err != nil {
			return fmt.Errorf("cluster: create agent dir %s: %w", sub, err)
		}
	}

	runConfigPath := filepath.Join(dataDir, "run", "config.json")
	legacyConfigPath := filepath.Join(dataDir, "config.json")

	// Load any persisted config on disk to preserve clash_api when possible and enable offline boot.
	var persistedCfg []byte
	if fileExists(runConfigPath) {
		if data, readErr := os.ReadFile(runConfigPath); readErr == nil && len(bytes.TrimSpace(data)) > 0 {
			persistedCfg = data
		}
	} else if fileExists(legacyConfigPath) {
		if data, readErr := os.ReadFile(legacyConfigPath); readErr == nil && len(bytes.TrimSpace(data)) > 0 {
			persistedCfg = data
		}
	}

	clashAddr := strings.TrimSpace(opts.clashAPIAddrOverride)
	clashSecret := strings.TrimSpace(opts.clashSecretOverride)
	if clashAddr == "" && len(persistedCfg) > 0 {
		if cfgAddr, cfgSecret := extractConfigClashAPI(persistedCfg); cfgAddr != "" && isBindableLoopback(cfgAddr) {
			clashAddr = cfgAddr
			clashSecret = cfgSecret
		}
	}

	schedule := opts.reconnectSchedule
	if len(schedule) == 0 {
		schedule = agentReconnectSchedule
	}
	telInterval := opts.telemetryInterval
	if telInterval <= 0 {
		telInterval = agentTelemetryInterval
	}

	pubV4, pubV6 := detectLocalPublicIPs()

	runner := &agentRunner{
		cfg:               cfg,
		wsURL:             wsURL,
		dataDir:           dataDir,
		runConfigPath:     runConfigPath,
		startedAt:         time.Now().UTC(),
		publicIPv4:        pubV4,
		publicIPv6:        pubV6,
		reconnectSchedule: schedule,
		telemetryInterval: telInterval,
		pendingDeltas:     make(map[deltaKey]telemetry.TrafficDelta),
		dialer: &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: 15 * time.Second,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: cfg.InsecureTLS, //nolint:gosec // Explicitly opt-in via --insecure for self-signed Masters
			},
		},
	}

	telHub := telemetry.NewHub(func(deltas []telemetry.TrafficDelta) {
		runner.handleLocalTrafficDeltas(deltas)
	})
	runner.telHub = telHub

	coreMgr := coremgr.NewManager(coremgr.Options{
		DataDir:      dataDir,
		ClashAPIAddr: clashAddr,
		ClashSecret:  clashSecret,
		OnLogLine: func(level, line string) {
			telHub.PublishLog(level, "sing-box", line)
		},
	})
	runner.coreMgr = coreMgr

	actualClashAddr, actualClashSecret := coreMgr.ClashAPIConfig()
	telHub.Start(ctx, actualClashAddr, actualClashSecret)
	defer telHub.Stop()
	defer func() {
		_ = coreMgr.Stop()
	}()

	// Control/Data Plane Decoupling: start sing-box IMMEDIATELY if config & binary exist on disk!
	if len(persistedCfg) > 0 {
		patched := patchConfigClashAPI(persistedCfg, actualClashAddr, actualClashSecret)
		if err := coreMgr.EnsureConfig(patched); err != nil {
			telHub.PublishLog("warn", "agent", "failed to sync persisted config: "+err.Error())
		} else {
			_ = writeFileAtomic(runConfigPath, patched, 0o600)
			if coreMgr.Status().BinaryExists {
				if err := coreMgr.Start(ctx); err != nil {
					log.Printf("agent: auto-start sing-box failed: %v", err)
					telHub.PublishLog("error", "agent", "auto-start sing-box failed: "+err.Error())
				} else {
					telHub.PublishLog("info", "agent", "sing-box started from persisted local config")
				}
			}
		}
	}

	// Infinite exponential-backoff reconnect loop until ctx.Done().
	backoffIdx := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		connected, sessionErr := runner.runSession(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if connected {
			backoffIdx = 0
		}
		wait := runner.reconnectSchedule[backoffIdx]
		if backoffIdx+1 < len(runner.reconnectSchedule) {
			backoffIdx++
		}

		if sessionErr != nil {
			telHub.PublishLog("warn", "agent", fmt.Sprintf("master connection lost (%v); reconnecting in %s", sessionErr, wait))
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *agentRunner) runSession(ctx context.Context) (connected bool, err error) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+a.cfg.Token)

	conn, resp, err := a.dialer.DialContext(ctx, a.wsURL, headers)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
			return false, fmt.Errorf("dial master HTTP %d: %w", resp.StatusCode, err)
		}
		return false, fmt.Errorf("dial master: %w", err)
	}
	defer conn.Close()

	conn.SetReadLimit(maxWSMessageBytes)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
		return nil
	})
	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(wsWriteTimeout))
	})

	sess := &agentSession{conn: conn}

	// Send initial NodeHello immediately after WebSocket connect.
	if err := a.sendHello(sess); err != nil {
		return false, fmt.Errorf("send hello: %w", err)
	}

	// Register live session so incoming traffic deltas route directly over WS.
	a.sessionMu.Lock()
	a.session = sess
	a.sessionMu.Unlock()
	defer func() {
		a.sessionMu.Lock()
		if a.session == sess {
			a.session = nil
		}
		a.sessionMu.Unlock()
	}()

	// Subscribe to live logs BEFORE flushing initial state so no entries are missed.
	logCh, unsubLogs := a.telHub.SubscribeLogs()
	defer unsubLogs()

	// Replay recent buffered logs so Master immediately sees startup/offline history.
	for _, entry := range a.telHub.RecentLogs(100) {
		if err := a.sendLog(sess, entry); err != nil {
			return true, err
		}
	}

	// Flush any buffered traffic deltas accumulated while disconnected.
	a.flushPendingDeltas(sess)

	// Push an immediate telemetry frame right after connect.
	_ = a.sendTelemetry(sess)

	sessionCtx, sessionCancel := context.WithCancel(ctx)
	defer sessionCancel()

	// Close WS promptly when parent or session context cancels.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-sessionCtx.Done()
		_ = conn.Close()
	}()

	// 1. Telemetry & Heartbeat stream (every 1s-2s).
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(a.telemetryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-ticker.C:
				if err := a.sendTelemetry(sess); err != nil {
					sessionCancel()
					return
				}
			}
		}
	}()

	// 2. Live Log stream.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-sessionCtx.Done():
				return
			case entry, open := <-logCh:
				if !open {
					return
				}
				if err := a.sendLog(sess, entry); err != nil {
					sessionCancel()
					return
				}
			}
		}
	}()

	// 3. Ping keepalive loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		interval := wsPingInterval
		if interval <= 0 {
			interval = 15 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-ticker.C:
				if err := sess.writePing(); err != nil {
					sessionCancel()
					return
				}
			}
		}
	}()

	// 4. Inbound RPC read & concurrent dispatch loop.
	rpcSem := make(chan struct{}, maxConcurrentAgentRPCs)
	var readErr error
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			readErr = err
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(wsPongTimeout))

		var env wireEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		if env.Type != msgTypeRPCReq {
			continue
		}

		reqEnv := env
		select {
		case rpcSem <- struct{}{}:
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-rpcSem }()
				a.handleRPC(ctx, sessionCtx, sess, reqEnv)
			}()
		default:
			_ = a.sendRPCResponse(sess, reqEnv.ID, nil, errors.New("agent busy: too many concurrent RPC requests"))
		}
	}

	sessionCancel()
	wg.Wait()
	return true, readErr
}

func (a *agentRunner) buildHello() NodeHello {
	hostname, _ := os.Hostname()
	return NodeHello{
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Hostname:     hostname,
		AgentVersion: a.cfg.AgentVersion,
		DataDir:      a.dataDir,
		PublicIPv4:   a.publicIPv4,
		PublicIPv6:   a.publicIPv6,
		CoreStatus:   a.coreMgr.Status(),
	}
}

func (a *agentRunner) sendHello(sess *agentSession) error {
	raw, err := mustMarshalRaw(a.buildHello())
	if err != nil {
		return err
	}
	return sess.writeJSON(wireEnvelope{
		Type:    msgTypeHello,
		Payload: raw,
	})
}

func (a *agentRunner) sendTelemetry(sess *agentSession) error {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	uptime := int64(time.Since(a.startedAt).Seconds())
	if uptime < 0 {
		uptime = 0
	}

	frame := telemetryFrame{
		Snapshot:  a.telHub.Snapshot(),
		Core:      a.coreMgr.Status(),
		MemBytes:  mem.Alloc,
		UptimeSec: uptime,
	}
	raw, err := mustMarshalRaw(frame)
	if err != nil {
		return err
	}
	return sess.writeJSON(wireEnvelope{
		Type:    msgTypeTelemetry,
		Payload: raw,
	})
}

func (a *agentRunner) sendLog(sess *agentSession, entry telemetry.LogEntry) error {
	raw, err := mustMarshalRaw(entry)
	if err != nil {
		return err
	}
	return sess.writeJSON(wireEnvelope{
		Type:    msgTypeLog,
		Payload: raw,
	})
}

func (a *agentRunner) handleLocalTrafficDeltas(deltas []telemetry.TrafficDelta) {
	if len(deltas) == 0 {
		return
	}

	a.sessionMu.Lock()
	sess := a.session
	a.sessionMu.Unlock()

	if sess != nil {
		if err := a.sendTrafficDeltas(sess, deltas); err == nil {
			return
		}
	}

	// Master currently unreachable: buffer deltas in memory until reconnect.
	a.deltaMu.Lock()
	for _, d := range deltas {
		if d.Upload <= 0 && d.Download <= 0 {
			continue
		}
		k := deltaKey{inbound: d.Inbound, user: d.User}
		cur, exists := a.pendingDeltas[k]
		if !exists && len(a.pendingDeltas) >= maxPendingDeltaKeys {
			continue
		}
		cur.Inbound = d.Inbound
		cur.User = d.User
		cur.Upload += d.Upload
		cur.Download += d.Download
		a.pendingDeltas[k] = cur
	}
	a.deltaMu.Unlock()
}

func (a *agentRunner) flushPendingDeltas(sess *agentSession) {
	a.deltaMu.Lock()
	if len(a.pendingDeltas) == 0 {
		a.deltaMu.Unlock()
		return
	}
	batch := make([]telemetry.TrafficDelta, 0, len(a.pendingDeltas))
	for _, d := range a.pendingDeltas {
		batch = append(batch, d)
	}
	a.pendingDeltas = make(map[deltaKey]telemetry.TrafficDelta)
	a.deltaMu.Unlock()

	sort.Slice(batch, func(i, j int) bool {
		if batch[i].Inbound != batch[j].Inbound {
			return batch[i].Inbound < batch[j].Inbound
		}
		return batch[i].User < batch[j].User
	})

	if err := a.sendTrafficDeltas(sess, batch); err != nil {
		a.handleLocalTrafficDeltas(batch)
	}
}

func (a *agentRunner) sendTrafficDeltas(sess *agentSession, deltas []telemetry.TrafficDelta) error {
	items := make([]trafficDeltaWire, 0, len(deltas))
	for _, d := range deltas {
		if d.Upload <= 0 && d.Download <= 0 {
			continue
		}
		items = append(items, trafficDeltaWire{
			Inbound:  d.Inbound,
			User:     d.User,
			Upload:   d.Upload,
			Download: d.Download,
		})
	}
	if len(items) == 0 {
		return nil
	}
	raw, err := mustMarshalRaw(trafficDeltaFrame{Deltas: items})
	if err != nil {
		return err
	}
	return sess.writeJSON(wireEnvelope{
		Type:    msgTypeTrafficDelta,
		Payload: raw,
	})
}

func (a *agentRunner) sendRPCResponse(sess *agentSession, id string, respPayload any, callErr error) error {
	env := wireEnvelope{
		Type: msgTypeRPCResp,
		ID:   id,
	}
	if callErr != nil {
		env.Error = callErr.Error()
	} else if respPayload != nil {
		raw, err := mustMarshalRaw(respPayload)
		if err != nil {
			env.Error = err.Error()
		} else {
			env.Payload = raw
		}
	}
	return sess.writeJSON(env)
}

func (a *agentRunner) handleRPC(agentCtx, sessionCtx context.Context, sess *agentSession, req wireEnvelope) {
	var (
		resp          any
		err           error
		pushTelemetry bool
	)

	switch req.Method {
	case rpcCoreStatus:
		resp = a.coreMgr.Status()

	case rpcCoreStart:
		opCtx, cancel := context.WithTimeout(agentCtx, defaultRPCTimeout)
		err = a.coreMgr.Start(opCtx)
		cancel()
		if err == nil {
			resp = a.coreMgr.Status()
			pushTelemetry = true
		}

	case rpcCoreStop:
		err = a.coreMgr.Stop()
		if err == nil {
			resp = a.coreMgr.Status()
			pushTelemetry = true
		}

	case rpcCoreRestart:
		opCtx, cancel := context.WithTimeout(agentCtx, defaultRPCTimeout)
		err = a.coreMgr.Restart(opCtx)
		cancel()
		if err == nil {
			resp = a.coreMgr.Status()
			pushTelemetry = true
		}

	case rpcCoreApplyConfig:
		var p applyConfigReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			if len(p.Certs) > 0 {
				err = writeCertBundles(a.dataDir, p.Certs)
			}
			if err == nil && len(bytes.TrimSpace(p.ConfigJSON)) > 0 {
				clashAddr, clashSecret := a.coreMgr.ClashAPIConfig()
				patched := patchConfigClashAPI(p.ConfigJSON, clashAddr, clashSecret)

				opCtx, cancel := context.WithTimeout(agentCtx, defaultRPCTimeout)
				if a.coreMgr.Status().Running {
					err = a.coreMgr.Reload(opCtx, patched)
				} else {
					err = a.coreMgr.EnsureConfig(patched)
				}
				cancel()

				if err == nil {
					if writeErr := writeFileAtomic(a.runConfigPath, patched, 0o600); writeErr != nil {
						err = fmt.Errorf("persist run/config.json: %w", writeErr)
					} else {
						a.telHub.PublishLog("info", "agent", fmt.Sprintf("applied config (%d bytes, %d cert bundles)", len(patched), len(p.Certs)))
					}
				}
			}
			if err == nil {
				resp = a.coreMgr.Status()
				pushTelemetry = true
			}
		}

	case rpcCoreListReleases:
		var p listReleasesReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			opCtx, cancel := context.WithTimeout(sessionCtx, releasesRPCTimeout)
			var list []coremgr.ReleaseInfo
			list, err = a.coreMgr.ListReleases(opCtx, p.IncludePrerelease, p.ProxyURL)
			cancel()
			if err == nil {
				resp = listReleasesResp{Releases: list}
			}
		}

	case rpcCoreUpgrade:
		var opts coremgr.UpgradeOptions
		if err = json.Unmarshal(req.Payload, &opts); err == nil {
			opCtx, cancel := context.WithTimeout(agentCtx, longRPCTimeout)
			resp = a.coreMgr.Upgrade(opCtx, opts)
			cancel()
			pushTelemetry = true
		}

	case rpcCoreUploadBinary:
		var p uploadBinaryReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			opCtx, cancel := context.WithTimeout(agentCtx, longRPCTimeout)
			resp = a.coreMgr.UploadBinary(opCtx, bytes.NewReader(p.Data), p.Filename)
			cancel()
			pushTelemetry = true
		}

	case rpcCoreRollback:
		opCtx, cancel := context.WithTimeout(agentCtx, defaultRPCTimeout)
		err = a.coreMgr.Rollback(opCtx)
		cancel()
		if err == nil {
			resp = a.coreMgr.Status()
			pushTelemetry = true
		}

	case rpcTelemetryConns:
		opCtx, cancel := context.WithTimeout(sessionCtx, defaultRPCTimeout)
		var list []telemetry.ConnectionItem
		list, err = a.telHub.Connections(opCtx)
		cancel()
		if err == nil {
			resp = connectionsResp{Connections: list}
		}

	case rpcTelemetryCloseConn:
		var p closeConnectionReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			opCtx, cancel := context.WithTimeout(sessionCtx, defaultRPCTimeout)
			err = a.telHub.CloseConnection(opCtx, p.ID)
			cancel()
		}

	case MethodCoreProxies:
		opCtx, cancel := context.WithTimeout(sessionCtx, defaultRPCTimeout)
		var groups []telemetry.ProxyGroupInfo
		groups, err = a.telHub.ListProxyGroups(opCtx)
		cancel()
		if err == nil {
			resp = coreProxiesResp{Groups: groups}
		}

	case MethodCoreProxyDelay:
		var p coreProxyDelayReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			timeout := defaultRPCTimeout
			if p.TimeoutMS > 0 {
				if want := time.Duration(p.TimeoutMS)*time.Millisecond + 5*time.Second; want > timeout {
					timeout = want
				}
			}
			opCtx, cancel := context.WithTimeout(sessionCtx, timeout)
			var delayMS int
			delayMS, err = a.telHub.TestProxyDelay(opCtx, p.Proxy, p.TestURL, p.TimeoutMS)
			cancel()
			if err == nil {
				resp = coreProxyDelayResp{DelayMS: delayMS}
			}
		}

	case MethodCoreProxySelect:
		var p coreProxySelectReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			opCtx, cancel := context.WithTimeout(sessionCtx, defaultRPCTimeout)
			err = a.telHub.SelectProxy(opCtx, p.Group, p.Proxy)
			cancel()
		}

	case MethodNetCheckLocalPort:
		var p localPortCheckReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			opCtx, cancel := context.WithTimeout(sessionCtx, 8*time.Second)
			resp, err = CheckLocalPort(opCtx, p.Network, p.Port)
			cancel()
		}

	case MethodNetProbeExternalPort:
		var p ExternalPortProbeRequest
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			timeout := 10 * time.Second
			if p.TimeoutMS > 0 {
				if want := time.Duration(p.TimeoutMS)*time.Millisecond + 3*time.Second; want > timeout {
					timeout = want
				}
			}
			opCtx, cancel := context.WithTimeout(sessionCtx, timeout)
			resp, err = ProbeExternalPort(opCtx, p)
			cancel()
		}

	case rpcToolsRealityProbe:
		var p realityProbeReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			opCtx, cancel := context.WithTimeout(sessionCtx, defaultRPCTimeout)
			resp = cert.ProbeRealityTarget(opCtx, p.Target)
			cancel()
		}

	case rpcCertSelfSigned:
		var p selfSignedCertReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			certDir := certDirForID(a.dataDir, p.CertID)
			var info *cert.CertInfo
			info, err = cert.GenerateSelfSigned(cert.SelfSignedOptions{
				CertDir:   certDir,
				Domains:   p.Domains,
				ValidDays: p.ValidDays,
			})
			if err == nil {
				resp, err = buildCertBundle(p.CertID, info)
				if err == nil {
					a.telHub.PublishLog("info", "agent", fmt.Sprintf("generated self-signed cert-%d (%s)", p.CertID, strings.Join(info.Domains, ",")))
				}
			}
		}

	case rpcCertACME:
		var p acmeCertReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			opts := p.Options
			opts.CertDir = certDirForID(a.dataDir, p.CertID)
			opts.AccountDir = acmeAccountDir(a.dataDir)
			opCtx, cancel := context.WithTimeout(agentCtx, longRPCTimeout)
			var info *cert.CertInfo
			info, err = cert.IssueACME(opCtx, opts)
			cancel()
			if err == nil {
				resp, err = buildCertBundle(p.CertID, info)
				if err == nil {
					a.telHub.PublishLog("info", "agent", fmt.Sprintf("issued ACME cert-%d (%s)", p.CertID, strings.Join(info.Domains, ",")))
				}
			}
		}

	case rpcCertInspect:
		var p inspectCertReq
		if err = json.Unmarshal(req.Payload, &p); err == nil {
			resp, err = cert.InspectCertFiles(p.CertPath, p.KeyPath)
		}

	default:
		err = fmt.Errorf("cluster: unsupported rpc method %q", req.Method)
	}

	_ = a.sendRPCResponse(sess, req.ID, resp, err)
	if pushTelemetry {
		_ = a.sendTelemetry(sess)
	}
}
