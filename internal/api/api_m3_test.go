package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/cluster"
	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
)

func TestMilestone3NodesOfflineAndClusterAPI(t *testing.T) {
	srv, st, _, bp, tok := setupAuthedTestServer(t)

	// 1. Local node id=1 exists and is online
	rec := doJSON(t, srv, http.MethodGet, bp+"/api/v1/nodes", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /nodes failed: %d %s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Nodes []model.NodeView `json:"nodes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listResp)
	if len(listResp.Nodes) != 1 || !listResp.Nodes[0].IsLocal || !listResp.Nodes[0].Online {
		t.Fatalf("unexpected initial nodes: %+v", listResp.Nodes)
	}

	// 2. Create remote node id=2 -> gets 64-hex token + install_command
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/nodes", map[string]any{
		"name":        "jp-tokyo",
		"note":        "Tokyo VPS",
		"public_ipv4": "203.0.113.42",
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /nodes failed: %d %s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		Node           model.NodeView `json:"node"`
		Token          string         `json:"token"`
		InstallCommand string         `json:"install_command"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &createResp)
	if createResp.Node.ID != 2 || len(createResp.Token) != 64 || !strings.Contains(createResp.InstallCommand, "agent --master") {
		t.Fatalf("unexpected create node response: %+v", createResp)
	}

	// 3. Offline remote node returns 503 ("节点当前离线") on live ops, while dashboard summary returns 200 with online=false
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/core/status?node_id=2", nil, tok)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "节点当前离线") {
		t.Fatalf("expected 503 offline on remote core/status, got %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/logs/recent?node_id=2", nil, tok)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 offline on remote logs/recent, got %d", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/connections?node_id=2", nil, tok)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 offline on remote connections, got %d", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/dashboard/summary?node_id=2", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard/summary for offline node should return 200, got %d %s", rec.Code, rec.Body.String())
	}
	var sumResp struct {
		Node    model.NodeView       `json:"node"`
		Cluster model.ClusterSummary `json:"cluster"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sumResp)
	if sumResp.Node.Online || sumResp.Cluster.TotalNodes != 2 || sumResp.Cluster.OnlineNodes != 1 {
		t.Fatalf("unexpected offline dashboard summary: node=%+v cluster=%+v", sumResp.Node, sumResp.Cluster)
	}

	// 4. Update node + Reset token + Upgrade-all (upgrades online local node)
	rec = doJSON(t, srv, http.MethodPut, bp+"/api/v1/nodes/2", map[string]any{
		"name":        "jp-tokyo-pro",
		"github_proxy": "https://mirror.example.com/",
		"log_level":   "debug",
	}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "jp-tokyo-pro") {
		t.Fatalf("PUT /nodes/2 failed: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/nodes/2/reset-token", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset-token failed: %d %s", rec.Code, rec.Body.String())
	}
	var resetResp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resetResp)
	if resetResp.Token == "" || resetResp.Token == createResp.Token {
		t.Fatalf("expected rotated token, got %q", resetResp.Token)
	}

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/nodes/upgrade-all", map[string]any{
		"tag_name": "v1.12.0",
	}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"node_id":1`) {
		t.Fatalf("upgrade-all failed: %d %s", rec.Code, rec.Body.String())
	}

	// 5. Delete local node id=1 is forbidden (400)
	rec = doJSON(t, srv, http.MethodDelete, bp+"/api/v1/nodes/1", nil, tok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting local node, got %d", rec.Code)
	}

	// 6. Node with inbound triggers 409 on delete unless ?force=true
	kpRec := doJSON(t, srv, http.MethodPost, bp+"/api/v1/tools/reality-keypair", nil, tok)
	var kp struct {
		PrivateKey string `json:"private_key"`
		PublicKey  string `json:"public_key"`
		ShortID    string `json:"short_id"`
	}
	_ = json.Unmarshal(kpRec.Body.Bytes(), &kp)

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/inbounds", map[string]any{
		"node_id":     2,
		"name":        "Tokyo VLESS",
		"tag":         "vless-tokyo",
		"protocol":    "vless",
		"listen_port": 24443,
		"settings": map[string]any{
			"tls_mode":                 "reality",
			"reality_handshake_server": "www.microsoft.com",
			"reality_handshake_port":   443,
			"reality_private_key":      kp.PrivateKey,
			"reality_short_ids":        []string{kp.ShortID},
			"server_name":              "www.microsoft.com",
		},
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create inbound on node 2 failed: %d %s", rec.Code, rec.Body.String())
	}

	// Record traffic on node 2 and verify by_node breakdown
	_ = st.RecordTrafficDeltas(context.Background(), 2, time.Now().UTC(), []store.TrafficDeltaInput{
		{InboundTag: "vless-tokyo", MemberName: "alice", Upload: 300, Download: 700},
	})
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/traffic/stats?range=7d", nil, tok)
	var tr model.TrafficStatsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &tr)
	if len(tr.ByNode) != 2 {
		t.Fatalf("expected 2 entries in by_node, got %+v", tr.ByNode)
	}

	rec = doJSON(t, srv, http.MethodDelete, bp+"/api/v1/nodes/2", nil, tok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 deleting node with inbounds, got %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodDelete, bp+"/api/v1/nodes/2?force=true", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 force deleting node, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestMilestone3MasterAgentLiveWebSocketIntegration(t *testing.T) {
	masterDir := t.TempDir()
	agentDir := t.TempDir()

	st, err := store.Open(filepath.Join(masterDir, "panel.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	bp, _, jwtSecret, err := st.EnsureBootstrapDefaults(context.Background(), 18080)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	localCore := coremgr.NewManager(coremgr.Options{DataDir: masterDir})
	localTel := telemetry.NewHub(nil)
	localDriver := cluster.NewLocalDriver(1, masterDir, "0.3.0-test", localCore, localTel)

	var srv *Server
	agentConnected := make(chan int64, 4)
	hub := cluster.NewHub(localDriver, cluster.HubCallbacks{
		AuthenticateToken: func(token string) (int64, bool) {
			n, err := st.GetNodeByToken(context.Background(), token)
			if err != nil {
				return 0, false
			}
			return n.ID, true
		},
		OnAgentConnected: func(nodeID int64, hello cluster.NodeHello) {
			_ = st.UpdateNodeRuntimeInfo(context.Background(), nodeID, store.NodeRuntimeUpdate{
				OS:           hello.OS,
				Arch:         hello.Arch,
				Hostname:     hello.Hostname,
				AgentVersion: hello.AgentVersion,
				CoreVersion:  hello.CoreStatus.Version,
				DataDir:      hello.DataDir,
				LastSeenAt:   time.Now().UTC(),
			})
			select {
			case agentConnected <- nodeID:
			default:
			}
		},
	})
	defer hub.Close()

	srv, err = NewServer(ServerConfig{
		Store:        st,
		Tokens:       auth.NewTokenManager(jwtSecret, time.Hour),
		Hub:          hub,
		Core:         localCore,
		Telemetry:    localTel,
		DataDir:      masterDir,
		PanelVersion: "0.3.0-test",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	// Setup admin
	rec := doJSON(t, srv, http.MethodPost, bp+"/api/v1/auth/setup", map[string]any{
		"username": "admin",
		"password": "Password123!",
	}, "")
	var setupResp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &setupResp)
	tok := setupResp.Token

	// Create remote node id=2
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/nodes", map[string]any{
		"name": "agent-lab",
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /nodes failed: %d %s", rec.Code, rec.Body.String())
	}
	var createNode struct {
		Node  model.NodeView `json:"node"`
		Token string         `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &createNode)

	// Start real Agent connecting to Master WS over httptest server!
	agentCtx, agentCancel := context.WithCancel(context.Background())
	defer agentCancel()
	go func() {
		_ = cluster.RunAgent(agentCtx, cluster.AgentConfig{
			MasterURL:    httpSrv.URL + bp,
			Token:        createNode.Token,
			DataDir:      agentDir,
			AgentVersion: "0.3.0-agent",
		})
	}()

	select {
	case nid := <-agentConnected:
		if nid != createNode.Node.ID {
			t.Fatalf("expected connected node %d, got %d", createNode.Node.ID, nid)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for agent to connect to Master WS")
	}

	// Verify GET /nodes shows node 2 online
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/nodes", nil, tok)
	var nodesResp struct {
		Nodes []model.NodeView `json:"nodes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &nodesResp)
	if len(nodesResp.Nodes) != 2 || !nodesResp.Nodes[1].Online || nodesResp.Nodes[1].AgentVersion != "0.3.0-agent" {
		t.Fatalf("expected node 2 online with agent_version, got %+v", nodesResp.Nodes)
	}

	// Create self-signed cert on remote node 2 -> PEM files must exist under agentDir/certs!
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/certificates/self-signed", map[string]any{
		"node_id": 2,
		"name":    "Agent Self Cert",
		"domains": []string{"agent.local"},
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("remote self-signed cert failed: %d %s", rec.Code, rec.Body.String())
	}
	var remoteCert model.Certificate
	_ = json.Unmarshal(rec.Body.Bytes(), &remoteCert)
	if remoteCert.NodeID != 2 || remoteCert.CertPath == "" {
		t.Fatalf("unexpected remote cert: %+v", remoteCert)
	}
	if _, err := os.Stat(remoteCert.CertPath); err != nil {
		t.Fatalf("expected remote cert PEM written on agent disk at %s: %v", remoteCert.CertPath, err)
	}

	// Create Hysteria2 inbound on remote node 2 -> config.json must be synced to agentDir/run/config.json!
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/inbounds", map[string]any{
		"node_id":        2,
		"name":           "Agent Hy2",
		"tag":            "hy2-agent",
		"protocol":       "hysteria2",
		"listen_port":    28443,
		"certificate_id": remoteCert.ID,
		"settings":       map[string]any{"up_mbps": 100, "down_mbps": 100},
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("remote inbound create failed: %d %s", rec.Code, rec.Body.String())
	}

	agentCfgPath := filepath.Join(agentDir, "run", "config.json")
	cfgBytes, err := os.ReadFile(agentCfgPath)
	if err != nil {
		t.Fatalf("expected agent config.json written at %s: %v", agentCfgPath, err)
	}
	if !strings.Contains(string(cfgBytes), `"hy2-agent"`) {
		t.Fatalf("agent config.json missing hy2-agent inbound:\n%s", string(cfgBytes))
	}

	// Verify GET /core/status?node_id=2 and remote reality-probe work over WS RPC
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/core/status?node_id=2", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /core/status?node_id=2 failed: %d %s", rec.Code, rec.Body.String())
	}
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer tlsSrv.Close()
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/tools/reality-probe", map[string]any{
		"node_id": 2,
		"target":  strings.TrimPrefix(tlsSrv.URL, "https://"),
	}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reachable":true`) {
		t.Fatalf("remote reality-probe failed: %d %s", rec.Code, rec.Body.String())
	}
}
