package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/cluster"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
)

type fakeProbeController struct {
	*fallbackLocalController
	nodeID   int64
	isLocal  bool
	online   bool
	localRes cluster.LocalPortCheckResult
	extRes   cluster.ExternalPortProbeResult

	mu            sync.Mutex
	localCalls    int
	externalCalls []cluster.ExternalPortProbeRequest
}

func (f *fakeProbeController) NodeID() int64  { return f.nodeID }
func (f *fakeProbeController) IsLocal() bool  { return f.isLocal }
func (f *fakeProbeController) IsOnline() bool { return f.online }
func (f *fakeProbeController) CheckLocalPort(_ context.Context, network string, port int) (cluster.LocalPortCheckResult, error) {
	f.mu.Lock()
	f.localCalls++
	f.mu.Unlock()
	res := f.localRes
	res.Protocol = network
	res.Port = port
	return res, nil
}
func (f *fakeProbeController) ProbeExternalPort(_ context.Context, req cluster.ExternalPortProbeRequest) (cluster.ExternalPortProbeResult, error) {
	f.mu.Lock()
	f.externalCalls = append(f.externalCalls, req)
	f.mu.Unlock()
	res := f.extRes
	res.Protocol = req.Network
	return res, nil
}

type fakeProbeHub struct {
	mu    sync.RWMutex
	nodes map[int64]*fakeProbeController
	order []int64
}

func (h *fakeProbeHub) Get(nodeID int64) (cluster.NodeController, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.nodes[nodeID]
	if !ok {
		return nil, false
	}
	return c, true
}
func (h *fakeProbeHub) IsOnline(nodeID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.nodes[nodeID]
	return ok && c.online
}
func (h *fakeProbeHub) OnlineNodeIDs() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]int64, 0, len(h.order))
	for _, id := range h.order {
		if c, ok := h.nodes[id]; ok && c.online {
			out = append(out, id)
		}
	}
	return out
}
func (h *fakeProbeHub) RuntimeState(nodeID int64) (cluster.NodeRuntimeState, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.nodes[nodeID]
	if !ok {
		return cluster.NodeRuntimeState{}, false
	}
	return cluster.NodeRuntimeState{Online: c.online}, true
}
func (h *fakeProbeHub) DisconnectNode(int64) {}
func (h *fakeProbeHub) HandleAgentWS(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusServiceUnavailable, "fake hub")
}

func TestInboundPortCheckCrossNodeProberSelectionAndStatuses(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	basePath, _, jwtSecret, err := st.EnsureBootstrapDefaults(ctx, 18080)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	_ = st.UpdatePanelSettings(ctx, model.PanelSettings{
		InstanceID:  "hw-sg",
		WebBasePath: basePath,
		ListenPort:  18080,
		PublicIPv4:  "188.239.22.220",
		LogLevel:    "info",
	})

	remoteNode := &model.Node{Name: "gcp-us", PublicIPv4: "35.212.210.56", LogLevel: "info"}
	if err := st.CreateNode(ctx, remoteNode); err != nil {
		t.Fatalf("create remote node: %v", err)
	}

	masterCtrl := &fakeProbeController{
		fallbackLocalController: &fallbackLocalController{dataDir: dir},
		nodeID:                  1,
		isLocal:                 true,
		online:                  true,
		localRes:                cluster.LocalPortCheckResult{Listening: true},
		extRes:                  cluster.ExternalPortProbeResult{Reachable: true, LatencyMS: 42},
	}
	agentCtrl := &fakeProbeController{
		fallbackLocalController: &fallbackLocalController{dataDir: dir},
		nodeID:                  remoteNode.ID,
		isLocal:                 false,
		online:                  true,
		localRes:                cluster.LocalPortCheckResult{Listening: true},
		extRes:                  cluster.ExternalPortProbeResult{Reachable: false, LatencyMS: 3500},
	}
	hub := &fakeProbeHub{
		nodes: map[int64]*fakeProbeController{
			1:             masterCtrl,
			remoteNode.ID: agentCtrl,
		},
		order: []int64{1, remoteNode.ID},
	}

	srv, err := NewServer(ServerConfig{
		Store:        st,
		Tokens:       auth.NewTokenManager(jwtSecret, time.Hour),
		Hub:          hub,
		DataDir:      dir,
		PanelVersion: "1.1.0-test",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.StopAutoSync()
	token := m5InitAdmin(t, srv, basePath)

	// Create 1 TCP inbound on Master (id=1) and 1 Hysteria2 QUIC UDP inbound on Agent (id=2).
	certRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/certificates/self-signed", map[string]any{
		"node_id": remoteNode.ID,
		"name":    "GCP Cert",
		"domains": []string{"gcp.example.com"},
	}, token)
	var certObj model.Certificate
	_ = json.Unmarshal(certRec.Body.Bytes(), &certObj)

	ssKeyRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/tools/ss2022-key", map[string]any{"method": "2022-blake3-aes-128-gcm"}, token)
	var ssKey struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(ssKeyRec.Body.Bytes(), &ssKey)

	ibMasterRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/inbounds", map[string]any{
		"node_id":     1,
		"name":        "SG SS",
		"tag":         "ss-sg",
		"protocol":    "shadowsocks",
		"listen_port": 26150,
		"settings": map[string]any{
			"method":          "2022-blake3-aes-128-gcm",
			"server_password": ssKey.Key,
		},
	}, token)
	var ibMaster model.Inbound
	_ = json.Unmarshal(ibMasterRec.Body.Bytes(), &ibMaster)

	ibAgentRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/inbounds", map[string]any{
		"node_id":        remoteNode.ID,
		"name":           "US HY2",
		"tag":            "hy2-us",
		"protocol":       "hysteria2",
		"listen_port":    28443,
		"certificate_id": certObj.ID,
		"settings":       map[string]any{},
	}, token)
	var ibAgent model.Inbound
	_ = json.Unmarshal(ibAgentRec.Body.Bytes(), &ibAgent)

	// 1. Agent inbound checked by Master (id=1): listening locally + external reachable (udp_quic) -> status="open"!
	rec1 := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/inbounds/"+strconvFormatInt(ibAgent.ID)+"/check-port", nil, token)
	if rec1.Code != http.StatusOK {
		t.Fatalf("check-port agent failed (%d): %s", rec1.Code, rec1.Body.String())
	}
	var r1 InboundPortCheckResult
	_ = json.Unmarshal(rec1.Body.Bytes(), &r1)
	if r1.Status != "open" || !r1.LocalListening || !r1.ExternalChecked || !r1.ExternalReachable ||
		r1.ProbedByNodeID != 1 || r1.ProbedByNodeName != "hw-sg" || r1.Network != "udp_quic" || r1.LatencyMS != 42 {
		t.Fatalf("unexpected Agent check-port result: %+v", r1)
	}
	if len(masterCtrl.externalCalls) != 1 || masterCtrl.externalCalls[0].Host != "35.212.210.56" || masterCtrl.externalCalls[0].Network != "udp_quic" {
		t.Fatalf("expected Master to probe Agent 35.212.210.56 via udp_quic, got %+v", masterCtrl.externalCalls)
	}

	// 2. Master inbound checked by Agent (id=2): listening locally + external blocked -> status="local_only" with firewall hint!
	rec2 := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/inbounds/"+strconvFormatInt(ibMaster.ID)+"/check-port", nil, token)
	var r2 InboundPortCheckResult
	_ = json.Unmarshal(rec2.Body.Bytes(), &r2)
	if r2.Status != "local_only" || !r2.LocalListening || !r2.ExternalChecked || r2.ExternalReachable ||
		r2.ProbedByNodeID != remoteNode.ID || r2.ProbedByNodeName != "gcp-us" || !strings.Contains(r2.Hint, "TCP 26150") {
		t.Fatalf("unexpected Master local_only result: %+v", r2)
	}

	// 3. Batch check-ports on node 1 while Agent is offline -> status="local_unverified" (single-node mode)!
	agentCtrl.online = false
	batchRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/inbounds/check-ports?node_id=1", nil, token)
	if batchRec.Code != http.StatusOK {
		t.Fatalf("check-ports failed (%d): %s", batchRec.Code, batchRec.Body.String())
	}
	var batchResp struct {
		Results []InboundPortCheckResult `json:"results"`
	}
	_ = json.Unmarshal(batchRec.Body.Bytes(), &batchResp)
	if len(batchResp.Results) != 1 || batchResp.Results[0].Status != "local_unverified" || batchResp.Results[0].ExternalChecked {
		t.Fatalf("expected single-node local_unverified, got %+v", batchResp.Results)
	}

	// 4. Checking inbound on offline Agent -> status="node_offline"!
	offRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/inbounds/"+strconvFormatInt(ibAgent.ID)+"/check-port", nil, token)
	var rOff InboundPortCheckResult
	_ = json.Unmarshal(offRec.Body.Bytes(), &rOff)
	if rOff.Status != "node_offline" {
		t.Fatalf("expected node_offline, got %+v", rOff)
	}

	// 5. Not listening locally -> status="not_listening"!
	masterCtrl.localRes.Listening = false
	notLnRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/inbounds/"+strconvFormatInt(ibMaster.ID)+"/check-port", nil, token)
	var rNotLn InboundPortCheckResult
	_ = json.Unmarshal(notLnRec.Body.Bytes(), &rNotLn)
	if rNotLn.Status != "not_listening" || rNotLn.LocalListening {
		t.Fatalf("expected not_listening, got %+v", rNotLn)
	}
}
