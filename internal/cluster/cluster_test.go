package cluster

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
)

func TestDeriveAgentWSURL(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "https://master.example.com:18080/sbox-abcd", want: "wss://master.example.com:18080/sbox-abcd/api/v1/agent/ws"},
		{in: "https://master.example.com:18080/sbox-abcd/", want: "wss://master.example.com:18080/sbox-abcd/api/v1/agent/ws"},
		{in: "http://127.0.0.1:18080", want: "ws://127.0.0.1:18080/api/v1/agent/ws"},
		{in: "http://127.0.0.1:18080/", want: "ws://127.0.0.1:18080/api/v1/agent/ws"},
		{in: "wss://master.example.com:18080/sbox-abcd/api/v1/agent/ws", want: "wss://master.example.com:18080/sbox-abcd/api/v1/agent/ws"},
		{in: "ws://127.0.0.1:18080/api/v1/agent/ws", want: "ws://127.0.0.1:18080/api/v1/agent/ws"},
		{in: "master.example.com:18080/sbox-1234", want: "wss://master.example.com:18080/sbox-1234/api/v1/agent/ws"},
		{in: "", wantErr: true},
		{in: "ftp://master.example.com", wantErr: true},
	}

	for _, tc := range cases {
		got, err := deriveAgentWSURL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("deriveAgentWSURL(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("deriveAgentWSURL(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("deriveAgentWSURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLocalDriverContract(t *testing.T) {
	dir := t.TempDir()
	tel := telemetry.NewHub(nil)
	core := coremgr.NewManager(coremgr.Options{DataDir: dir})
	local := NewLocalDriver(1, dir, "0.3.0-test", core, tel)

	if local.NodeID() != 1 || !local.IsLocal() || !local.IsOnline() {
		t.Fatalf("unexpected local identity: id=%d local=%v online=%v", local.NodeID(), local.IsLocal(), local.IsOnline())
	}
	if local.DataDir() != dir {
		t.Fatalf("DataDir = %q, want %q", local.DataDir(), dir)
	}

	st := local.RuntimeState()
	if !st.Online || st.OS != runtime.GOOS || st.Arch != runtime.GOARCH || st.AgentVersion != "0.3.0-test" {
		t.Fatalf("unexpected RuntimeState: %+v", st)
	}

	ctx := context.Background()
	bundle, err := local.GenerateSelfSigned(ctx, 10, []string{"local.test", "127.0.0.1"}, 30)
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}
	if bundle.CertID != 10 || len(bundle.CertPEM) == 0 || len(bundle.KeyPEM) == 0 || bundle.Info == nil {
		t.Fatalf("invalid bundle: %+v", bundle)
	}

	info, err := local.InspectCertFiles(ctx, bundle.Info.CertPath, bundle.Info.KeyPath)
	if err != nil {
		t.Fatalf("InspectCertFiles: %v", err)
	}
	if info.Fingerprint != bundle.Info.Fingerprint {
		t.Fatalf("fingerprint mismatch: %s vs %s", info.Fingerprint, bundle.Info.Fingerprint)
	}

	cfg := []byte(`{"log":{"level":"info"},"inbounds":[]}`)
	if err := local.ApplyConfig(ctx, cfg, []CertFileBundle{
		{CertID: 11, CertPEM: bundle.CertPEM, KeyPEM: bundle.KeyPEM},
	}); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("expected config.json written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "certs", "cert-11", "fullchain.pem")); err != nil {
		t.Fatalf("expected cert-11 fullchain.pem written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "certs", "cert-11", "privkey.pem")); err != nil {
		t.Fatalf("expected cert-11 privkey.pem written: %v", err)
	}
}

func TestClusterAuthHelloAndLiveRPCs(t *testing.T) {
	masterDir := t.TempDir()
	agentDir := t.TempDir()
	const validToken = "tok-node2-secret-abcdef123456"

	localCore := coremgr.NewManager(coremgr.Options{DataDir: masterDir})
	localTel := telemetry.NewHub(nil)
	local := NewLocalDriver(1, masterDir, "0.3.0-master", localCore, localTel)

	connectedCh := make(chan struct {
		nodeID int64
		hello  NodeHello
	}, 4)
	disconnectedCh := make(chan int64, 4)

	hub := NewHub(local, HubCallbacks{
		AuthenticateToken: func(token string) (int64, bool) {
			if token == validToken {
				return 2, true
			}
			return 0, false
		},
		OnAgentConnected: func(nodeID int64, hello NodeHello) {
			connectedCh <- struct {
				nodeID int64
				hello  NodeHello
			}{nodeID: nodeID, hello: hello}
		},
		OnAgentDisconnected: func(nodeID int64) {
			disconnectedCh <- nodeID
		},
	})
	defer hub.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/sbox-secret/api/v1/agent/ws", hub.HandleAgentWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1. Verify bad & missing token rejected with 401.
	badResp, err := http.Get(srv.URL + "/sbox-secret/api/v1/agent/ws")
	if err != nil {
		t.Fatalf("GET missing token: %v", err)
	}
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", badResp.StatusCode)
	}

	reqBad, _ := http.NewRequest(http.MethodGet, srv.URL+"/sbox-secret/api/v1/agent/ws", nil)
	reqBad.Header.Set("Authorization", "Bearer wrong-token")
	badResp2, err := http.DefaultClient.Do(reqBad)
	if err != nil {
		t.Fatalf("GET bad token: %v", err)
	}
	_ = badResp2.Body.Close()
	if badResp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", badResp2.StatusCode)
	}

	// Verify offline state before agent connects.
	if ctrl, ok := hub.Get(2); ok || ctrl != nil {
		t.Fatalf("expected node 2 offline before connect")
	}
	if hub.IsOnline(2) {
		t.Fatalf("expected IsOnline(2) == false before connect")
	}
	if !hub.IsOnline(1) {
		t.Fatalf("expected local node 1 always online")
	}

	// 2. Start real RunAgent in background goroutine.
	agentCtx, agentCancel := context.WithCancel(context.Background())
	agentDone := make(chan error, 1)
	go func() {
		agentDone <- RunAgent(agentCtx, AgentConfig{
			MasterURL:    srv.URL + "/sbox-secret",
			Token:        validToken,
			DataDir:      agentDir,
			AgentVersion: "0.3.0-agent",
		})
	}()
	defer func() {
		agentCancel()
		select {
		case <-agentDone:
		case <-time.After(5 * time.Second):
			t.Errorf("timed out waiting for RunAgent to exit")
		}
	}()

	// Wait for OnAgentConnected.
	var hello NodeHello
	select {
	case ev := <-connectedCh:
		if ev.nodeID != 2 {
			t.Fatalf("connected nodeID = %d, want 2", ev.nodeID)
		}
		hello = ev.hello
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for OnAgentConnected")
	}

	if hello.OS != runtime.GOOS || hello.Arch != runtime.GOARCH {
		t.Fatalf("hello OS/Arch = %s/%s, want %s/%s", hello.OS, hello.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if hello.DataDir != agentDir {
		t.Fatalf("hello DataDir = %q, want %q", hello.DataDir, agentDir)
	}
	if hello.AgentVersion != "0.3.0-agent" {
		t.Fatalf("hello AgentVersion = %q, want 0.3.0-agent", hello.AgentVersion)
	}

	// Verify Hub lookup & OnlineNodeIDs.
	ids := hub.OnlineNodeIDs()
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("OnlineNodeIDs = %v, want [1 2]", ids)
	}
	ctrl, ok := hub.Get(2)
	if !ok || ctrl == nil {
		t.Fatalf("hub.Get(2) returned offline")
	}
	if ctrl.IsLocal() || !ctrl.IsOnline() || ctrl.NodeID() != 2 || ctrl.DataDir() != agentDir {
		t.Fatalf("unexpected remote controller metadata: local=%v online=%v id=%d dir=%q",
			ctrl.IsLocal(), ctrl.IsOnline(), ctrl.NodeID(), ctrl.DataDir())
	}

	// Subscribe to live logs BEFORE invoking RPCs so we capture agent log streaming.
	logCh, unsubLogs := ctrl.SubscribeLogs()
	defer unsubLogs()

	// 3. Live RPC: CoreStatus.
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer rpcCancel()

	status, err := ctrl.CoreStatus(rpcCtx)
	if err != nil {
		t.Fatalf("CoreStatus RPC: %v", err)
	}
	if status.Running || status.BinaryExists {
		t.Fatalf("unexpected initial CoreStatus: %+v", status)
	}

	// 4. Live RPC: GenerateSelfSigned on Agent.
	ssBundle, err := ctrl.GenerateSelfSigned(rpcCtx, 7, []string{"edge.example.com", "127.0.0.1"}, 365)
	if err != nil {
		t.Fatalf("GenerateSelfSigned RPC: %v", err)
	}
	wantCertPath := filepath.Join(agentDir, "certs", "cert-7", "fullchain.pem")
	wantKeyPath := filepath.Join(agentDir, "certs", "cert-7", "privkey.pem")
	if ssBundle.CertID != 7 || ssBundle.Info == nil {
		t.Fatalf("invalid self-signed bundle: %+v", ssBundle)
	}
	if ssBundle.Info.CertPath != wantCertPath || ssBundle.Info.KeyPath != wantKeyPath {
		t.Fatalf("cert paths = (%q, %q), want (%q, %q)", ssBundle.Info.CertPath, ssBundle.Info.KeyPath, wantCertPath, wantKeyPath)
	}
	if len(ssBundle.CertPEM) == 0 || len(ssBundle.KeyPEM) == 0 || ssBundle.Info.Fingerprint == "" {
		t.Fatalf("missing PEM or fingerprint in self-signed bundle")
	}
	if !fileExists(wantCertPath) || !fileExists(wantKeyPath) {
		t.Fatalf("expected generated cert files on agent disk")
	}

	// 5. Live RPC: InspectCertFiles on Agent.
	inspected, err := ctrl.InspectCertFiles(rpcCtx, wantCertPath, wantKeyPath)
	if err != nil {
		t.Fatalf("InspectCertFiles RPC: %v", err)
	}
	if inspected.Fingerprint != ssBundle.Info.Fingerprint || len(inspected.Domains) < 2 {
		t.Fatalf("unexpected inspected cert: %+v", inspected)
	}

	// 6. Live RPC: ApplyConfig with CertFileBundle synced to Agent disk.
	rawCfg := []byte(`{"log":{"level":"info"},"inbounds":[{"type":"vless","tag":"vless-remote","listen":"::","listen_port":443}]}`)
	err = ctrl.ApplyConfig(rpcCtx, rawCfg, []CertFileBundle{
		{
			CertID:  42,
			CertPEM: ssBundle.CertPEM,
			KeyPEM:  ssBundle.KeyPEM,
		},
	})
	if err != nil {
		t.Fatalf("ApplyConfig RPC: %v", err)
	}

	// Verify synced cert PEMs on agent disk.
	gotCertPEM, err := os.ReadFile(filepath.Join(agentDir, "certs", "cert-42", "fullchain.pem"))
	if err != nil {
		t.Fatalf("read synced fullchain.pem: %v", err)
	}
	if !bytes.Equal(gotCertPEM, ssBundle.CertPEM) {
		t.Fatalf("synced fullchain.pem bytes mismatch")
	}
	gotKeyPEM, err := os.ReadFile(filepath.Join(agentDir, "certs", "cert-42", "privkey.pem"))
	if err != nil {
		t.Fatalf("read synced privkey.pem: %v", err)
	}
	if !bytes.Equal(gotKeyPEM, ssBundle.KeyPEM) {
		t.Fatalf("synced privkey.pem bytes mismatch")
	}

	// Verify config persisted in BOTH <agentDir>/run/config.json and <agentDir>/config.json.
	runCfgBytes, err := os.ReadFile(filepath.Join(agentDir, "run", "config.json"))
	if err != nil {
		t.Fatalf("read run/config.json: %v", err)
	}
	if !strings.Contains(string(runCfgBytes), "vless-remote") {
		t.Fatalf("run/config.json missing inbound tag: %s", string(runCfgBytes))
	}
	if addr, sec := extractConfigClashAPI(runCfgBytes); addr == "" || sec == "" {
		t.Fatalf("expected agent clash_api injected into run/config.json, got addr=%q sec=%q", addr, sec)
	}
	coreCfgBytes, err := os.ReadFile(filepath.Join(agentDir, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	if !bytes.Equal(runCfgBytes, coreCfgBytes) {
		t.Fatalf("run/config.json and config.json should match")
	}

	// 7. Live RPC: ProbeRealityTarget against a local TLS 1.3 + H2 test server.
	tlsCert, err := tls.X509KeyPair(ssBundle.CertPEM, ssBundle.KeyPEM)
	if err != nil {
		t.Fatalf("load tls keypair: %v", err)
	}
	tlsLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2", "http/1.1"},
		Certificates: []tls.Certificate{tlsCert},
	})
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer tlsLn.Close()
	go func() {
		for {
			c, acceptErr := tlsLn.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				if tc, ok := conn.(*tls.Conn); ok {
					_ = tc.Handshake()
				}
			}(c)
		}
	}()

	probeRes, err := ctrl.ProbeRealityTarget(rpcCtx, tlsLn.Addr().String())
	if err != nil {
		t.Fatalf("ProbeRealityTarget RPC: %v", err)
	}
	if !probeRes.Reachable || !probeRes.TLS13 || !probeRes.H2 {
		t.Fatalf("unexpected ProbeRealityTarget result: %+v", probeRes)
	}

	// 8. Verify live log streaming arrived from Agent -> Master.
	gotAppliedLog := false
	logDeadline := time.After(3 * time.Second)
	for !gotAppliedLog {
		select {
		case entry := <-logCh:
			if strings.Contains(entry.Message, "applied config") {
				gotAppliedLog = true
			}
		case <-logDeadline:
			t.Fatalf("timed out waiting for streamed agent log; recent=%+v", ctrl.RecentLogs(20))
		}
	}
	if len(ctrl.RecentLogs(10)) == 0 {
		t.Fatalf("expected non-empty RecentLogs on RemoteDriver")
	}

	// 9. Verify DisconnectNode & automatic Agent reconnect.
	hub.DisconnectNode(2)
	select {
	case id := <-disconnectedCh:
		if id != 2 {
			t.Fatalf("disconnected nodeID = %d, want 2", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for OnAgentDisconnected")
	}

	if hub.IsOnline(2) {
		t.Fatalf("expected node 2 offline immediately after DisconnectNode")
	}
	if _, ok := hub.Get(2); ok {
		t.Fatalf("expected hub.Get(2) false immediately after DisconnectNode")
	}

	// Agent should automatically reconnect within ~1-3s.
	select {
	case ev := <-connectedCh:
		if ev.nodeID != 2 {
			t.Fatalf("reconnected nodeID = %d, want 2", ev.nodeID)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for Agent automatic reconnect")
	}

	ctrl2, ok := hub.Get(2)
	if !ok || !ctrl2.IsOnline() {
		t.Fatalf("expected node 2 back online after reconnect")
	}
	if _, err := ctrl2.CoreStatus(rpcCtx); err != nil {
		t.Fatalf("CoreStatus after reconnect failed: %v", err)
	}
}

func TestClusterTelemetryLogsAndTrafficDeltas(t *testing.T) {
	const (
		validToken  = "tok-telemetry-node3"
		clashSecret = "secret-clash-test"
	)

	// Spin up mock Clash API server for the Agent's local telemetry.Hub.
	var closedConnID atomic.Value
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	clashMux := http.NewServeMux()
	clashMux.HandleFunc("/traffic", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"up":1234,"down":5678}`))
		<-r.Context().Done()
	})
	clashMux.HandleFunc("/memory", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"inuse":4194304,"oslimit":0}`))
		<-r.Context().Done()
	})
	clashMux.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"info","payload":"mock sing-box inbound accepted"}`))
		<-r.Context().Done()
	})
	clashMux.HandleFunc("/connections", func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			payload1 := `{"downloadTotal":500,"uploadTotal":200,"memory":4194304,"connections":[{"id":"conn-1","upload":200,"download":500,"start":"2026-01-01T00:00:00Z","chains":["direct"],"rule":"final","metadata":{"network":"tcp","sourceIP":"1.2.3.4","sourcePort":"12345","host":"example.com","destinationPort":"443","inboundTag":"vless-reality","authUser":"alice"}}]}`
			_ = c.WriteMessage(websocket.TextMessage, []byte(payload1))
			time.Sleep(150 * time.Millisecond)
			payload2 := `{"downloadTotal":1500,"uploadTotal":600,"memory":4194304,"connections":[{"id":"conn-1","upload":600,"download":1500,"start":"2026-01-01T00:00:00Z","chains":["direct"],"rule":"final","metadata":{"network":"tcp","sourceIP":"1.2.3.4","sourcePort":"12345","host":"example.com","destinationPort":"443","inboundTag":"vless-reality","authUser":"alice"}}]}`
			_ = c.WriteMessage(websocket.TextMessage, []byte(payload2))
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"downloadTotal":1500,"uploadTotal":600,"memory":4194304,"connections":[{"id":"conn-1","upload":600,"download":1500,"start":"2026-01-01T00:00:00Z","chains":["direct"],"rule":"final","metadata":{"network":"tcp","sourceIP":"1.2.3.4","sourcePort":"12345","host":"example.com","destinationPort":"443","inboundTag":"vless-reality","authUser":"alice"}}]}`))
	})
	clashMux.HandleFunc("/connections/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			closedConnID.Store(strings.TrimPrefix(r.URL.Path, "/connections/"))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	clashSrv := httptest.NewServer(clashMux)
	defer clashSrv.Close()

	// Spin up Master Hub with OnTrafficDeltas callback.
	var (
		deltaMu      sync.Mutex
		totalUp      int64
		totalDown    int64
		deltaNodeID  int64
		deltaUpdated = make(chan struct{}, 16)
	)
	connectedCh := make(chan int64, 4)

	hub := NewHub(NewLocalDriver(1, t.TempDir(), "0.3.0", nil, nil), HubCallbacks{
		AuthenticateToken: func(token string) (int64, bool) {
			if token == validToken {
				return 3, true
			}
			return 0, false
		},
		OnAgentConnected: func(nodeID int64, _ NodeHello) {
			connectedCh <- nodeID
		},
		OnTrafficDeltas: func(nodeID int64, deltas []telemetry.TrafficDelta) {
			deltaMu.Lock()
			deltaNodeID = nodeID
			for _, d := range deltas {
				if d.Inbound == "vless-reality" && d.User == "alice" {
					totalUp += d.Upload
					totalDown += d.Download
				}
			}
			deltaMu.Unlock()
			select {
			case deltaUpdated <- struct{}{}:
			default:
			}
		},
	})
	defer hub.Close()

	masterMux := http.NewServeMux()
	masterMux.HandleFunc("/api/v1/agent/ws", hub.HandleAgentWS)
	masterSrv := httptest.NewServer(masterMux)
	defer masterSrv.Close()

	agentCtx, agentCancel := context.WithCancel(context.Background())
	agentDone := make(chan error, 1)
	go func() {
		agentDone <- runAgent(agentCtx, AgentConfig{
			MasterURL:    masterSrv.URL,
			Token:        validToken,
			DataDir:      t.TempDir(),
			AgentVersion: "0.3.0-agent",
		}, agentOptions{
			clashAPIAddrOverride: strings.TrimPrefix(clashSrv.URL, "http://"),
			clashSecretOverride:  clashSecret,
			reconnectSchedule:    []time.Duration{100 * time.Millisecond, 200 * time.Millisecond},
			telemetryInterval:    100 * time.Millisecond,
		})
	}()
	defer func() {
		agentCancel()
		<-agentDone
	}()

	select {
	case id := <-connectedCh:
		if id != 3 {
			t.Fatalf("connected id = %d, want 3", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for node 3 to connect")
	}

	ctrl, ok := hub.Get(3)
	if !ok {
		t.Fatalf("hub.Get(3) offline")
	}

	// Verify traffic deltas arrive at Master callback (200+400=600 up, 500+1000=1500 down).
	deadline := time.After(5 * time.Second)
	for {
		deltaMu.Lock()
		u, d, nid := totalUp, totalDown, deltaNodeID
		deltaMu.Unlock()
		if nid == 3 && u >= 600 && d >= 1500 {
			break
		}
		select {
		case <-deltaUpdated:
		case <-deadline:
			t.Fatalf("timed out waiting for traffic deltas: got node=%d up=%d down=%d", nid, u, d)
		}
	}

	// Verify live telemetry snapshot stream reaches Master.
	metricCh, unsubMetrics := ctrl.SubscribeMetrics()
	defer unsubMetrics()
	metricDeadline := time.After(3 * time.Second)
	for {
		select {
		case snap := <-metricCh:
			if snap.CoreConnected && snap.UpSpeed == 1234 && snap.DownSpeed == 5678 && snap.MemoryInuse == 4194304 {
				goto metricsOK
			}
		case <-metricDeadline:
			t.Fatalf("timed out waiting for telemetry snapshot, latest=%+v", ctrl.Snapshot())
		}
	}
metricsOK:

	// Verify live sing-box log reached Master's RemoteDriver buffer.
	logDeadline := time.After(3 * time.Second)
	for {
		found := false
		for _, entry := range ctrl.RecentLogs(50) {
			if strings.Contains(entry.Message, "mock sing-box inbound accepted") {
				found = true
				break
			}
		}
		if found {
			break
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-logDeadline:
			t.Fatalf("timed out waiting for clash log on Master; logs=%+v", ctrl.RecentLogs(50))
		}
	}

	// Verify Connections & CloseConnection RPCs.
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rpcCancel()
	conns, err := ctrl.Connections(rpcCtx)
	if err != nil {
		t.Fatalf("Connections RPC: %v", err)
	}
	if len(conns) != 1 || conns[0].ID != "conn-1" || conns[0].User != "alice" || conns[0].Inbound != "vless-reality" {
		t.Fatalf("unexpected Connections result: %+v", conns)
	}
	if err := ctrl.CloseConnection(rpcCtx, "conn-1"); err != nil {
		t.Fatalf("CloseConnection RPC: %v", err)
	}
	if got, _ := closedConnID.Load().(string); got != "conn-1" {
		t.Fatalf("CloseConnection closed id = %q, want conn-1", got)
	}
}

const mockAgentSingBoxSource = `package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

type cfgDoc struct {
	Experimental struct {
		ClashAPI struct {
			ExternalController string ` + "`json:\"external_controller\"`" + `
			Secret             string ` + "`json:\"secret\"`" + `
		} ` + "`json:\"clash_api\"`" + `
	} ` + "`json:\"experimental\"`" + `
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println("sing-box version 1.12.9-mock")
	case "check":
		fmt.Println("INFO check passed")
	case "run":
		var cfgPath string
		for i := 2; i < len(os.Args)-1; i++ {
			if os.Args[i] == "-c" {
				cfgPath = os.Args[i+1]
				break
			}
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			os.Exit(1)
		}
		var doc cfgDoc
		_ = json.Unmarshal(data, &doc)
		addr := doc.Experimental.ClashAPI.ExternalController
		secret := doc.Experimental.ClashAPI.Secret

		mux := http.NewServeMux()
		mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
			if secret != "" && r.Header.Get("Authorization") != "Bearer "+secret {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"version\":\"1.12.9-mock\"}"))
		})
		if addr != "" {
			go func() { _ = http.ListenAndServe(addr, mux) }()
		}
		fmt.Printf("INFO mock sing-box started pid=%d\n", os.Getpid())
		sig := make(chan os.Signal, 2)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
	default:
		os.Exit(2)
	}
}
`

func buildMockSingBoxBinary(t *testing.T, outPath string) {
	t.Helper()
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(srcFile, []byte(mockAgentSingBoxSource), 0o600); err != nil {
		t.Fatalf("write mock source: %v", err)
	}
	goBin := "go"
	if runtime.GOOS == "windows" {
		goBin = "go.exe"
	}
	goPath := filepath.Join(runtime.GOROOT(), "bin", goBin)
	if !fileExists(goPath) {
		var err error
		goPath, err = exec.LookPath("go")
		if err != nil {
			t.Skipf("go compiler not available to build mock sing-box: %v", err)
		}
	}
	cmd := exec.Command(goPath, "build", "-o", outPath, srcFile)
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock sing-box failed: %v\n%s", err, string(out))
	}
}

func TestAgentControlDataPlaneDecouplingAndMasterRestart(t *testing.T) {
	const validToken = "tok-decoupled-node4"

	agentDir := t.TempDir()
	binDir := filepath.Join(agentDir, "bin")
	runDir := filepath.Join(agentDir, "run")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run: %v", err)
	}

	binName := "sing-box"
	if runtime.GOOS == "windows" {
		binName = "sing-box.exe"
	}
	mockBinPath := filepath.Join(binDir, binName)
	buildMockSingBoxBinary(t, mockBinPath)

	// Pre-populate <agentDir>/run/config.json with a known loopback Clash API port.
	clashLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick clash port: %v", err)
	}
	clashAddr := clashLn.Addr().String()
	_ = clashLn.Close()

	initialCfg := fmt.Sprintf(`{"log":{"level":"info"},"experimental":{"clash_api":{"external_controller":%q,"secret":"offline-boot-secret"}}}`, clashAddr)
	if err := os.WriteFile(filepath.Join(runDir, "config.json"), []byte(initialCfg), 0o600); err != nil {
		t.Fatalf("write persisted config: %v", err)
	}

	// Reserve a fixed port for Master, close it so Master is INITIALLY UNREACHABLE!
	masterLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve master port: %v", err)
	}
	masterAddr := masterLn.Addr().String()
	_ = masterLn.Close()

	// Start Agent while Master is completely DOWN.
	agentCtx, agentCancel := context.WithCancel(context.Background())
	agentDone := make(chan error, 1)
	go func() {
		agentDone <- runAgent(agentCtx, AgentConfig{
			MasterURL:    "http://" + masterAddr,
			Token:        validToken,
			DataDir:      agentDir,
			AgentVersion: "0.3.0-decoupled",
		}, agentOptions{
			reconnectSchedule: []time.Duration{150 * time.Millisecond, 250 * time.Millisecond},
			telemetryInterval: 150 * time.Millisecond,
		})
	}()
	defer func() {
		agentCancel()
		select {
		case <-agentDone:
		case <-time.After(5 * time.Second):
			t.Errorf("timed out waiting for decoupled agent shutdown")
		}
	}()

	// 1. Verify sing-box started IMMEDIATELY on Agent even though Master is unreachable!
	clashClient := &http.Client{Timeout: 500 * time.Millisecond}
	bootDeadline := time.After(5 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, "http://"+clashAddr+"/version", nil)
		req.Header.Set("Authorization", "Bearer offline-boot-secret")
		resp, reqErr := clashClient.Do(req)
		if reqErr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-bootDeadline:
			t.Fatalf("expected sing-box to start immediately from persisted run/config.json while Master is offline")
		}
	}

	// 2. Now boot Master #1 on masterAddr and verify Agent connects & reports running core + PID!
	startMaster := func() (*Hub, *http.Server, chan NodeHello) {
		connCh := make(chan NodeHello, 4)
		hub := NewHub(NewLocalDriver(1, t.TempDir(), "0.3.0", nil, nil), HubCallbacks{
			AuthenticateToken: func(token string) (int64, bool) {
				if token == validToken {
					return 4, true
				}
				return 0, false
			},
			OnAgentConnected: func(nodeID int64, hello NodeHello) {
				if nodeID == 4 {
					connCh <- hello
				}
			},
		})
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v1/agent/ws", hub.HandleAgentWS)
		ln, listenErr := net.Listen("tcp", masterAddr)
		if listenErr != nil {
			t.Fatalf("listen master %s: %v", masterAddr, listenErr)
		}
		srv := &http.Server{Handler: mux}
		go func() { _ = srv.Serve(ln) }()
		return hub, srv, connCh
	}

	hub1, srv1, connCh1 := startMaster()
	var firstPID int
	select {
	case hello := <-connCh1:
		if !hello.CoreStatus.Running || hello.CoreStatus.PID <= 0 {
			t.Fatalf("expected running core in NodeHello, got %+v", hello.CoreStatus)
		}
		firstPID = hello.CoreStatus.PID
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for Agent to connect to Master #1")
	}

	// 3. Shut down Master #1 completely (simulating Master restart / network outage).
	hub1.Close()
	_ = srv1.Close()
	time.Sleep(300 * time.Millisecond)

	// Verify sing-box Clash API is STILL responding uninterrupted while Master is down!
	req, _ := http.NewRequest(http.MethodGet, "http://"+clashAddr+"/version", nil)
	req.Header.Set("Authorization", "Bearer offline-boot-secret")
	resp, err := clashClient.Do(req)
	if err != nil {
		t.Fatalf("sing-box stopped during Master outage: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected Clash API status during Master outage: %d", resp.StatusCode)
	}

	// 4. Boot Master #2 on the same address -> verify Agent reconnects with the EXACT SAME PID!
	hub2, srv2, connCh2 := startMaster()
	defer func() {
		hub2.Close()
		_ = srv2.Close()
	}()

	select {
	case hello2 := <-connCh2:
		if !hello2.CoreStatus.Running || hello2.CoreStatus.PID != firstPID {
			t.Fatalf("core PID changed across Master restart: got %+v, want running PID %d", hello2.CoreStatus, firstPID)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for Agent to reconnect to Master #2")
	}

	// 5. Also exercise UploadBinary RPC over WebSocket with the mock sing-box binary!
	ctrl, ok := hub2.Get(4)
	if !ok {
		t.Fatalf("expected node 4 online on Master #2")
	}
	binBytes, err := os.ReadFile(mockBinPath)
	if err != nil {
		t.Fatalf("read mock binary: %v", err)
	}
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer rpcCancel()
	upRes, err := ctrl.UploadBinary(rpcCtx, binName, binBytes)
	if err != nil {
		t.Fatalf("UploadBinary RPC failed: %v", err)
	}
	if !upRes.Success || upRes.ToVersion != "1.12.9-mock" {
		t.Fatalf("unexpected UploadBinary result: %+v", upRes)
	}
}

func TestStaleConnectionReplacementDoesNotMarkNodeOffline(t *testing.T) {
	const validToken = "tok-replace-node5"
	var disconnectedCount atomic.Int32
	connectedCh := make(chan string, 4)

	hub := NewHub(NewLocalDriver(1, t.TempDir(), "0.3.0", nil, nil), HubCallbacks{
		AuthenticateToken: func(token string) (int64, bool) {
			if token == validToken {
				return 5, true
			}
			return 0, false
		},
		OnAgentConnected: func(nodeID int64, hello NodeHello) {
			if nodeID == 5 {
				connectedCh <- hello.Hostname
			}
		},
		OnAgentDisconnected: func(nodeID int64) {
			if nodeID == 5 {
				disconnectedCount.Add(1)
			}
		},
	})
	defer hub.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/ws", hub.HandleAgentWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL, _ := deriveAgentWSURL(srv.URL)
	headers := http.Header{"Authorization": []string{"Bearer " + validToken}}

	dialAndHello := func(host string) *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
		if err != nil {
			t.Fatalf("dial %s: %v", host, err)
		}
		raw, _ := mustMarshalRaw(NodeHello{
			OS:           "linux",
			Arch:         "amd64",
			Hostname:     host,
			AgentVersion: "0.3.0",
			DataDir:      "/var/lib/sbox-agent",
		})
		if err := c.WriteJSON(wireEnvelope{Type: msgTypeHello, Payload: raw}); err != nil {
			t.Fatalf("send hello %s: %v", host, err)
		}
		return c
	}

	c1 := dialAndHello("old-session")
	defer c1.Close()
	select {
	case h := <-connectedCh:
		if h != "old-session" {
			t.Fatalf("first hostname = %q, want old-session", h)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for first connection")
	}

	// Connect replacement session for the SAME nodeID while c1 is still open.
	c2 := dialAndHello("new-session")
	defer c2.Close()
	select {
	case h := <-connectedCh:
		if h != "new-session" {
			t.Fatalf("second hostname = %q, want new-session", h)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for replacement connection")
	}

	// Give stale connection teardown a moment to complete.
	time.Sleep(150 * time.Millisecond)

	if !hub.IsOnline(5) {
		t.Fatalf("expected node 5 to stay online after stale replacement")
	}
	if disconnectedCount.Load() != 0 {
		t.Fatalf("expected 0 OnAgentDisconnected callbacks on stale replacement, got %d", disconnectedCount.Load())
	}
	st, ok := hub.RuntimeState(5)
	if !ok || st.Hostname != "new-session" {
		t.Fatalf("expected RuntimeState hostname=new-session, got ok=%v st=%+v", ok, st)
	}
}

func TestClusterProxyControlRPCs(t *testing.T) {
	const (
		validToken  = "tok-proxy-control-node6"
		clashSecret = "secret-proxy-test"
	)

	var (
		mu             sync.Mutex
		selectedNow    = "node-jp"
		lastDelayURL   string
		lastDelayTO    string
	)

	clashMux := http.NewServeMux()
	clashMux.HandleFunc("/proxies", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+clashSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		cur := selectedNow
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"proxies": map[string]any{
				"sub-airport": map[string]any{
					"name": "sub-airport",
					"type": "Selector",
					"now":  cur,
					"all":  []string{"sub-airport-auto", "node-jp", "node-sg"},
				},
				"sub-airport-auto": map[string]any{
					"name": "sub-airport-auto",
					"type": "URLTest",
					"now":  "node-sg",
					"all":  []string{"node-jp", "node-sg"},
				},
				"node-jp": map[string]any{
					"name":    "node-jp",
					"type":    "VLESS",
					"history": []map[string]any{{"delay": 42}},
				},
				"node-sg": map[string]any{
					"name":    "node-sg",
					"type":    "VLESS",
					"history": []map[string]any{{"delay": 88}},
				},
			},
		})
	})
	clashMux.HandleFunc("/proxies/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+clashSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/proxies/")
		if r.Method == http.MethodGet && strings.HasSuffix(rest, "/delay") {
			rawName := strings.TrimSuffix(rest, "/delay")
			name, _ := url.PathUnescape(rawName)
			mu.Lock()
			lastDelayURL = r.URL.Query().Get("url")
			lastDelayTO = r.URL.Query().Get("timeout")
			mu.Unlock()

			if name == "node-dead" {
				w.WriteHeader(http.StatusGatewayTimeout)
				_, _ = w.Write([]byte(`{"message":"timeout"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"delay":73}`))
			return
		}
		if r.Method == http.MethodPut {
			group, _ := url.PathUnescape(rest)
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if group != "sub-airport" || (body.Name != "node-jp" && body.Name != "node-sg" && body.Name != "sub-airport-auto") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"invalid group or proxy"}`))
				return
			}
			mu.Lock()
			selectedNow = body.Name
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	clashSrv := httptest.NewServer(clashMux)
	defer clashSrv.Close()
	clashAddr := strings.TrimPrefix(clashSrv.URL, "http://")

	// Also verify LocalDriver delegation directly against the same mock Clash API.
	localTel := telemetry.NewHub(nil)
	localCtx, localCancel := context.WithCancel(context.Background())
	localTel.Start(localCtx, clashAddr, clashSecret)
	defer func() {
		localCancel()
		localTel.Stop()
	}()
	local := NewLocalDriver(1, t.TempDir(), "0.4.0-master", nil, localTel)

	connectedCh := make(chan int64, 4)
	hub := NewHub(local, HubCallbacks{
		AuthenticateToken: func(token string) (int64, bool) {
			if token == validToken {
				return 6, true
			}
			return 0, false
		},
		OnAgentConnected: func(nodeID int64, _ NodeHello) {
			connectedCh <- nodeID
		},
	})
	defer hub.Close()

	masterMux := http.NewServeMux()
	masterMux.HandleFunc("/api/v1/agent/ws", hub.HandleAgentWS)
	masterSrv := httptest.NewServer(masterMux)
	defer masterSrv.Close()

	agentCtx, agentCancel := context.WithCancel(context.Background())
	agentDone := make(chan error, 1)
	go func() {
		agentDone <- runAgent(agentCtx, AgentConfig{
			MasterURL:    masterSrv.URL,
			Token:        validToken,
			DataDir:      t.TempDir(),
			AgentVersion: "0.4.0-agent",
		}, agentOptions{
			clashAPIAddrOverride: clashAddr,
			clashSecretOverride:  clashSecret,
			reconnectSchedule:    []time.Duration{100 * time.Millisecond},
			telemetryInterval:    150 * time.Millisecond,
		})
	}()
	defer func() {
		agentCancel()
		<-agentDone
	}()

	select {
	case id := <-connectedCh:
		if id != 6 {
			t.Fatalf("connected id = %d, want 6", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for node 6 to connect")
	}

	remoteCtrl, ok := hub.Get(6)
	if !ok {
		t.Fatalf("hub.Get(6) returned offline")
	}

	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rpcCancel()

	// 1. Verify LocalDriver ListProxyGroups & TestProxyDelay.
	localGroups, err := local.ListProxyGroups(rpcCtx)
	if err != nil {
		t.Fatalf("local.ListProxyGroups: %v", err)
	}
	if len(localGroups) != 2 {
		t.Fatalf("local.ListProxyGroups count = %d, want 2: %+v", len(localGroups), localGroups)
	}

	// 2. Verify RemoteDriver ListProxyGroups over WebSocket RPC.
	groups, err := remoteCtrl.ListProxyGroups(rpcCtx)
	if err != nil {
		t.Fatalf("remote.ListProxyGroups: %v", err)
	}
	var airport *telemetry.ProxyGroupInfo
	for i := range groups {
		if groups[i].Name == "sub-airport" {
			airport = &groups[i]
			break
		}
	}
	if airport == nil {
		t.Fatalf("sub-airport group not found in %+v", groups)
	}
	if airport.Type != "Selector" || airport.Now != "node-jp" || len(airport.All) != 3 {
		t.Fatalf("unexpected sub-airport group: %+v", *airport)
	}
	if airport.Delays["node-jp"] != 42 || airport.Delays["node-sg"] != 88 {
		t.Fatalf("unexpected group delays: %+v", airport.Delays)
	}

	// 3. Verify RemoteDriver TestProxyDelay success and error propagation.
	delayMS, err := remoteCtrl.TestProxyDelay(rpcCtx, "node-jp", "https://cp.cloudflare.com", 3200)
	if err != nil {
		t.Fatalf("remote.TestProxyDelay: %v", err)
	}
	if delayMS != 73 {
		t.Fatalf("remote.TestProxyDelay = %d, want 73", delayMS)
	}
	mu.Lock()
	gotURL, gotTO := lastDelayURL, lastDelayTO
	mu.Unlock()
	if gotURL != "https://cp.cloudflare.com" || gotTO != "3200" {
		t.Fatalf("delay query params = (url=%q, timeout=%q), want (https://cp.cloudflare.com, 3200)", gotURL, gotTO)
	}

	if _, err := remoteCtrl.TestProxyDelay(rpcCtx, "node-dead", "", 2000); err == nil {
		t.Fatalf("expected error for dead proxy delay test, got nil")
	}

	// 4. Verify RemoteDriver SelectProxy updates state and propagates errors cleanly.
	if err := remoteCtrl.SelectProxy(rpcCtx, "sub-airport", "node-sg"); err != nil {
		t.Fatalf("remote.SelectProxy: %v", err)
	}
	groupsAfter, err := remoteCtrl.ListProxyGroups(rpcCtx)
	if err != nil {
		t.Fatalf("remote.ListProxyGroups after select: %v", err)
	}
	var switchedNow string
	for _, g := range groupsAfter {
		if g.Name == "sub-airport" {
			switchedNow = g.Now
			break
		}
	}
	if switchedNow != "node-sg" {
		t.Fatalf("sub-airport now after SelectProxy = %q, want node-sg", switchedNow)
	}

	if err := remoteCtrl.SelectProxy(rpcCtx, "missing-group", "node-sg"); err == nil {
		t.Fatalf("expected error selecting on missing group, got nil")
	}
}
