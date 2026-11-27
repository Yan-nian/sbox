package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sbox-panel/sbox-panel/internal/backup"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
)

func m5InitAdmin(t *testing.T, srv *Server, basePath string) string {
	t.Helper()
	rec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/auth/setup", map[string]any{
		"username": "admin",
		"password": "password123",
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("setup admin failed (%d): %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Token == "" {
		t.Fatalf("parse setup token failed: %v body=%s", err, rec.Body.String())
	}
	return resp.Token
}

func TestMilestone5ClusterSubscriptionsAndUAFormats(t *testing.T) {
	srv, st, _, _, basePath := newTestServer(t)
	defer srv.StopAutoSync()
	token := m5InitAdmin(t, srv, basePath)
	ctx := context.Background()

	// Configure local master node IPv4 and create a second remote node.
	if err := st.UpdatePanelSettings(ctx, model.PanelSettings{
		InstanceID:  "sg-master",
		WebBasePath: basePath,
		ListenPort:  18080,
		PublicIPv4:  "203.0.113.10",
		LogLevel:    "info",
	}); err != nil {
		t.Fatalf("UpdatePanelSettings: %v", err)
	}

	node2Rec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/nodes", map[string]any{
		"name":        "jp-edge",
		"public_ipv4": "198.51.100.20",
	}, token)
	if node2Rec.Code != http.StatusCreated {
		t.Fatalf("create node2 failed (%d): %s", node2Rec.Code, node2Rec.Body.String())
	}
	var node2Resp struct {
		Node struct {
			ID int64 `json:"id"`
		} `json:"node"`
	}
	_ = json.Unmarshal(node2Rec.Body.Bytes(), &node2Resp)
	node2ID := node2Resp.Node.ID

	// Create enabled member `alice` and disabled member `bob`.
	aliceRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/members", map[string]any{
		"name":    "alice",
		"enabled": true,
	}, token)
	if aliceRec.Code != http.StatusCreated {
		t.Fatalf("create alice failed (%d): %s", aliceRec.Code, aliceRec.Body.String())
	}
	var alice model.Member
	_ = json.Unmarshal(aliceRec.Body.Bytes(), &alice)

	bobRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/members", map[string]any{
		"name":    "bob",
		"enabled": false,
	}, token)
	if bobRec.Code != http.StatusCreated {
		t.Fatalf("create bob failed (%d): %s", bobRec.Code, bobRec.Body.String())
	}
	var bob model.Member
	_ = json.Unmarshal(bobRec.Body.Bytes(), &bob)

	// Record traffic for alice to test Subscription-Userinfo header.
	_ = st.RecordTrafficDeltas(ctx, 1, time.Now().UTC(), []store.TrafficDeltaInput{
		{InboundTag: "vless-sg", MemberName: "alice", Upload: 12345, Download: 67890},
	})

	// Create self-signed cert on node 1.
	certRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/certificates/self-signed", map[string]any{
		"node_id": 1,
		"name":    "SG SelfSigned",
		"domains": []string{"hy2.example.com"},
	}, token)
	if certRec.Code != http.StatusCreated {
		t.Fatalf("create self-signed cert failed (%d): %s", certRec.Code, certRec.Body.String())
	}
	var certObj model.Certificate
	_ = json.Unmarshal(certRec.Body.Bytes(), &certObj)

	// Create VLESS Reality on node 1, Hysteria2 with extra_entries on node 1, SS-2022 on node 2,
	// ShadowTLS v3 on node 1, and a Direct port forwarder (which MUST be skipped in client subs).
	kpRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/tools/reality-keypair", nil, token)
	var kp struct {
		PrivateKey string `json:"private_key"`
		PublicKey  string `json:"public_key"`
		ShortID    string `json:"short_id"`
	}
	_ = json.Unmarshal(kpRec.Body.Bytes(), &kp)

	ssKeyRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/tools/ss2022-key", map[string]any{
		"method": "2022-blake3-aes-128-gcm",
	}, token)
	var ssKeyResp struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(ssKeyRec.Body.Bytes(), &ssKeyResp)

	inbounds := []map[string]any{
		{
			"node_id":     1,
			"name":        "VLESS Reality",
			"tag":         "vless-sg",
			"protocol":    "vless",
			"listen_port": 24443,
			"settings": map[string]any{
				"tls_mode":                 "reality",
				"flow":                     "xtls-rprx-vision",
				"transport":                "tcp",
				"reality_handshake_server": "www.microsoft.com",
				"reality_handshake_port":   443,
				"reality_private_key":      kp.PrivateKey,
				"reality_public_key":       kp.PublicKey,
				"reality_short_ids":        []string{kp.ShortID},
			},
		},
		{
			"node_id":        1,
			"name":           "HY2 Main",
			"tag":            "hy2-sg",
			"protocol":       "hysteria2",
			"listen_port":    28443,
			"certificate_id": certObj.ID,
			"settings": map[string]any{
				"server_ports": "30000:30100",
			},
			"extra_entries": []map[string]any{
				{"name": "CN Relay", "host": "relay.example.com", "port": 443},
			},
		},
		{
			"node_id":     node2ID,
			"name":        "JP SS2022",
			"tag":         "ss-jp",
			"protocol":    "shadowsocks",
			"listen_port": 29000,
			"member_mode": "selected",
			"member_ids":  []int64{alice.ID},
			"settings": map[string]any{
				"method":          "2022-blake3-aes-128-gcm",
				"server_password": ssKeyResp.Key,
			},
		},
		{
			"node_id":     1,
			"name":        "SG ShadowTLS",
			"tag":         "stls-sg",
			"protocol":    "shadowtls",
			"listen_port": 29443,
			"settings": map[string]any{
				"handshake_server":   "www.microsoft.com",
				"handshake_port":     443,
				"strict_mode":        true,
				"detour_protocol":    "shadowsocks",
				"ss_method":          "2022-blake3-aes-128-gcm",
				"ss_server_password": ssKeyResp.Key,
			},
		},
		{
			"node_id":     1,
			"name":        "Direct Forward",
			"tag":         "direct-fwd",
			"protocol":    "direct",
			"listen_port": 25000,
			"settings": map[string]any{
				"override_address": "1.1.1.1",
				"override_port":    53,
			},
		},
	}
	for _, ib := range inbounds {
		r := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/inbounds", ib, token)
		if r.Code != http.StatusCreated {
			t.Fatalf("create inbound %v failed (%d): %s", ib["tag"], r.Code, r.Body.String())
		}
	}

	// 1. Base-path stealth: unprefixed /sub/{token} must return 404.
	unprefixed := httptest.NewRecorder()
	srv.ServeHTTP(unprefixed, httptest.NewRequest(http.MethodGet, "/sub/"+alice.SubToken, nil))
	if unprefixed.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unprefixed /sub/{token}, got %d", unprefixed.Code)
	}

	// 2. Invalid token and disabled member token must return 404.
	for _, tok := range []string{"nonexistent-token", bob.SubToken} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, basePath+"/sub/"+tok, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for token %q, got %d", tok, rec.Code)
		}
	}

	// 3. UA auto-detect: Clash (Mihomo)
	clashReq := httptest.NewRequest(http.MethodGet, basePath+"/sub/"+alice.SubToken, nil)
	clashReq.Header.Set("User-Agent", "clash-verge/2.0.1")
	clashRec := httptest.NewRecorder()
	srv.ServeHTTP(clashRec, clashReq)
	if clashRec.Code != http.StatusOK {
		t.Fatalf("clash sub failed (%d): %s", clashRec.Code, clashRec.Body.String())
	}
	if !strings.Contains(clashRec.Header().Get("Content-Type"), "yaml") {
		t.Fatalf("expected yaml content-type for clash, got %q", clashRec.Header().Get("Content-Type"))
	}
	if ui := clashRec.Header().Get("Subscription-Userinfo"); !strings.Contains(ui, "upload=12345") || !strings.Contains(ui, "download=67890") {
		t.Fatalf("unexpected Subscription-Userinfo header: %q", ui)
	}
	var clashDoc map[string]any
	if err := yaml.Unmarshal(clashRec.Body.Bytes(), &clashDoc); err != nil {
		t.Fatalf("invalid clash yaml: %v\n%s", err, clashRec.Body.String())
	}
	proxies, _ := clashDoc["proxies"].([]any)
	// Expected: VLESS, HY2 primary, HY2 CN Relay extra entry, JP SS2022, ShadowTLS = 5 proxies (direct skipped!)
	if len(proxies) != 5 {
		t.Fatalf("expected 5 clash proxies (4 inbounds + 1 extra_entry, direct skipped), got %d:\n%s", len(proxies), clashRec.Body.String())
	}

	// 4. UA auto-detect: sing-box (SFA/1.12)
	sbReq := httptest.NewRequest(http.MethodGet, basePath+"/sub/"+alice.SubToken, nil)
	sbReq.Header.Set("User-Agent", "SFA/1.12.0")
	sbRec := httptest.NewRecorder()
	srv.ServeHTTP(sbRec, sbReq)
	if sbRec.Code != http.StatusOK {
		t.Fatalf("sing-box sub failed (%d): %s", sbRec.Code, sbRec.Body.String())
	}
	var sbDoc map[string]any
	if err := json.Unmarshal(sbRec.Body.Bytes(), &sbDoc); err != nil {
		t.Fatalf("invalid sing-box json: %v\n%s", err, sbRec.Body.String())
	}
	if !strings.Contains(sbRec.Body.String(), `"shadowtls"`) || !strings.Contains(sbRec.Body.String(), `"sniff"`) || !strings.Contains(sbRec.Body.String(), `relay.example.com`) {
		t.Fatalf("sing-box profile missing expected entries: %s", sbRec.Body.String())
	}

	// 5. UA auto-detect: generic UA -> base64 URI lines
	b64Req := httptest.NewRequest(http.MethodGet, basePath+"/sub/"+alice.SubToken, nil)
	b64Req.Header.Set("User-Agent", "curl/8.5.0")
	b64Rec := httptest.NewRecorder()
	srv.ServeHTTP(b64Rec, b64Req)
	if b64Rec.Code != http.StatusOK {
		t.Fatalf("base64 sub failed (%d): %s", b64Rec.Code, b64Rec.Body.String())
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64Rec.Body.String()))
	if err != nil {
		t.Fatalf("decode base64 sub: %v", err)
	}
	decodedStr := string(decoded)
	if !strings.Contains(decodedStr, "vless://") || !strings.Contains(decodedStr, "hysteria2://") || !strings.Contains(decodedStr, "ss://") || !strings.Contains(decodedStr, "relay.example.com") {
		t.Fatalf("unexpected decoded base64 URI lines:\n%s", decodedStr)
	}

	// 6. Explicit ?format=uri override + admin preview endpoint
	uriRec := httptest.NewRecorder()
	srv.ServeHTTP(uriRec, httptest.NewRequest(http.MethodGet, basePath+"/sub/"+alice.SubToken+"?format=uri", nil))
	if uriRec.Code != http.StatusOK || !strings.Contains(uriRec.Body.String(), "vless://") {
		t.Fatalf("?format=uri failed (%d): %s", uriRec.Code, uriRec.Body.String())
	}

	previewRec := doJSON(t, srv, http.MethodGet, basePath+"/api/v1/subscriptions/preview?member_id="+strconvFormatInt(alice.ID)+"&format=uri", nil, token)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview failed (%d): %s", previewRec.Code, previewRec.Body.String())
	}
	var preview struct {
		Format  string `json:"format"`
		SubURL  string `json:"sub_url"`
		Content string `json:"content"`
		Nodes   []struct {
			Name     string `json:"name"`
			Protocol string `json:"protocol"`
			NodeName string `json:"node_name"`
			URI      string `json:"uri"`
		} `json:"nodes"`
	}
	_ = json.Unmarshal(previewRec.Body.Bytes(), &preview)
	if preview.Format != "uri" || !strings.HasPrefix(preview.SubURL, basePath+"/sub/"+alice.SubToken) || len(preview.Nodes) != 5 {
		t.Fatalf("unexpected preview response: %+v", preview)
	}
	sawMaster, sawEdge := false, false
	for _, n := range preview.Nodes {
		if n.NodeName == "sg-master" {
			sawMaster = true
		}
		if n.NodeName == "jp-edge" {
			sawEdge = true
		}
	}
	if !sawMaster || !sawEdge {
		t.Fatalf("expected nodes from both sg-master and jp-edge, got %+v", preview.Nodes)
	}
}

func TestMilestone5SyncTargetsPushMergeHistoryAndDebouncedAutoSync(t *testing.T) {
	srv, st, _, _, basePath := newTestServer(t)
	defer srv.StopAutoSync()
	token := m5InitAdmin(t, srv, basePath)
	ctx := context.Background()

	_ = st.UpdatePanelSettings(ctx, model.PanelSettings{
		InstanceID:  "sg-master",
		WebBasePath: basePath,
		ListenPort:  18080,
		PublicIPv4:  "203.0.113.10",
		LogLevel:    "info",
	})

	aliceRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/members", map[string]any{"name": "alice"}, token)
	var alice model.Member
	_ = json.Unmarshal(aliceRec.Body.Bytes(), &alice)

	ssKeyRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/tools/ss2022-key", map[string]any{"method": "2022-blake3-aes-128-gcm"}, token)
	var ssKey struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(ssKeyRec.Body.Bytes(), &ssKey)

	doJSON(t, srv, http.MethodPost, basePath+"/api/v1/inbounds", map[string]any{
		"node_id":     1,
		"name":        "SS Main",
		"tag":         "ss-main",
		"protocol":    "shadowsocks",
		"listen_port": 29100,
		"settings": map[string]any{
			"method":          "2022-blake3-aes-128-gcm",
			"server_password": ssKey.Key,
		},
	}, token)

	// Mock local server acting as BOTH GitHub Gist API and Sub-Store API.
	var mockMu sync.Mutex
	gistContent := "# >>> sbox-panel:other-vps >>>\nvless://other-node@1.2.3.4:443#OtherVPS\n# <<< sbox-panel:other-vps <<<"
	var gistPatchCount atomic.Int32
	subStoreExists := false
	subStoreMethod := ""
	subStoreContent := ""

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/gists/gist-abc" && r.Method == http.MethodGet:
			mockMu.Lock()
			c := gistContent
			mockMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"html_url": "https://gist.github.com/user/gist-abc",
				"files": map[string]any{
					"nodes.txt": map[string]any{
						"content": c,
						"raw_url": "https://gist.githubusercontent.com/user/gist-abc/raw/nodes.txt",
					},
				},
			})
		case r.URL.Path == "/gists/gist-abc" && r.Method == http.MethodPatch:
			if r.Header.Get("Authorization") != "Bearer ghp_secret_gist_token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body struct {
				Files map[string]struct {
					Content string `json:"content"`
				} `json:"files"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mockMu.Lock()
			gistContent = body.Files["nodes.txt"].Content
			mockMu.Unlock()
			gistPatchCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"html_url": "https://gist.github.com/user/gist-abc",
				"files": map[string]any{
					"nodes.txt": map[string]any{
						"raw_url": "https://gist.githubusercontent.com/user/gist-abc/raw/nodes.txt",
					},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/substore/api/sub/") && r.Method == http.MethodGet:
			mockMu.Lock()
			exists := subStoreExists
			mockMu.Unlock()
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"name": "sbox-sg"}})
		case r.URL.Path == "/substore/api/subs" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mockMu.Lock()
			subStoreExists = true
			subStoreMethod = http.MethodPost
			subStoreContent, _ = body["content"].(string)
			mockMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
		case strings.HasPrefix(r.URL.Path, "/substore/api/sub/") && r.Method == http.MethodPatch:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mockMu.Lock()
			subStoreMethod = http.MethodPatch
			subStoreContent, _ = body["content"].(string)
			mockMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockSrv.Close()

	// Create Gist sync target (merge_by_instance, auto_push=true) and Sub-Store target.
	gistCreate := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/sync-targets", map[string]any{
		"name":       "My Gist",
		"enabled":    true,
		"type":       "github_gist",
		"member_id":  alice.ID,
		"format":     "uri",
		"merge_mode": "merge_by_instance",
		"auto_push":  true,
		"config": map[string]any{
			"token":        "ghp_secret_gist_token",
			"gist_id":      "gist-abc",
			"file_path":    "nodes.txt",
			"api_base_url": mockSrv.URL,
		},
	}, token)
	if gistCreate.Code != http.StatusCreated {
		t.Fatalf("create gist target failed (%d): %s", gistCreate.Code, gistCreate.Body.String())
	}
	if strings.Contains(gistCreate.Body.String(), "ghp_secret_gist_token") {
		t.Fatalf("create response leaked raw token: %s", gistCreate.Body.String())
	}
	var gistTarget model.SyncTargetView
	_ = json.Unmarshal(gistCreate.Body.Bytes(), &gistTarget)
	if !gistTarget.HasToken {
		t.Fatalf("expected has_token=true, got %+v", gistTarget)
	}

	subCreate := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/sync-targets", map[string]any{
		"name":       "My Sub-Store",
		"enabled":    true,
		"type":       "sub_store",
		"member_id":  alice.ID,
		"format":     "base64",
		"merge_mode": "replace",
		"auto_push":  false,
		"config": map[string]any{
			"sub_store_url":  mockSrv.URL + "/substore",
			"sub_store_name": "sbox-sg",
		},
	}, token)
	if subCreate.Code != http.StatusCreated {
		t.Fatalf("create substore target failed (%d): %s", subCreate.Code, subCreate.Body.String())
	}

	// Verify GET /sync-targets masks secret token.
	listRec := doJSON(t, srv, http.MethodGet, basePath+"/api/v1/sync-targets", nil, token)
	if listRec.Code != http.StatusOK || strings.Contains(listRec.Body.String(), "ghp_secret_gist_token") {
		t.Fatalf("list sync-targets failed or leaked token (%d): %s", listRec.Code, listRec.Body.String())
	}

	// Update Gist target with empty token -> must preserve stored token!
	updRec := doJSON(t, srv, http.MethodPut, basePath+"/api/v1/sync-targets/"+strconvFormatInt(gistTarget.ID), map[string]any{
		"name": "My Gist Renamed",
		"config": map[string]any{
			"token": "",
		},
	}, token)
	if updRec.Code != http.StatusOK {
		t.Fatalf("update sync-target failed (%d): %s", updRec.Code, updRec.Body.String())
	}

	// Manual push on Gist target -> verify merge_by_instance preserved `other-vps` block AND added `sg-master` block!
	pushRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/sync-targets/"+strconvFormatInt(gistTarget.ID)+"/push", nil, token)
	if pushRec.Code != http.StatusOK {
		t.Fatalf("push gist failed (%d): %s", pushRec.Code, pushRec.Body.String())
	}
	var pushResp struct {
		Target model.SyncTargetView `json:"target"`
		Result struct {
			Success    bool   `json:"success"`
			HTTPStatus int    `json:"http_status"`
			NodeCount  int    `json:"node_count"`
			RemoteURL  string `json:"remote_url"`
		} `json:"result"`
	}
	_ = json.Unmarshal(pushRec.Body.Bytes(), &pushResp)
	if !pushResp.Result.Success || pushResp.Result.NodeCount != 1 || pushResp.Target.LastStatus != "success" {
		t.Fatalf("unexpected gist push response: %+v", pushResp)
	}
	mockMu.Lock()
	merged := gistContent
	mockMu.Unlock()
	if !strings.Contains(merged, "other-node@1.2.3.4:443") || !strings.Contains(merged, "# >>> sbox-panel:sg-master >>>") || !strings.Contains(merged, "ss://") {
		t.Fatalf("expected non-destructive merged gist content, got:\n%s", merged)
	}

	// Push-all -> pushes both Gist and Sub-Store (Sub-Store POST on 404).
	pushAllRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/sync-targets/push-all", nil, token)
	if pushAllRec.Code != http.StatusOK {
		t.Fatalf("push-all failed (%d): %s", pushAllRec.Code, pushAllRec.Body.String())
	}
	mockMu.Lock()
	ssMethod1, ssContent := subStoreMethod, subStoreContent
	mockMu.Unlock()
	if ssMethod1 != http.MethodPost || !strings.Contains(ssContent, "ss://") {
		t.Fatalf("expected Sub-Store POST with plain URI content, got method=%q content=%q", ssMethod1, ssContent)
	}

	// Check history endpoint.
	histRec := doJSON(t, srv, http.MethodGet, basePath+"/api/v1/sync-targets/history?limit=50", nil, token)
	if histRec.Code != http.StatusOK {
		t.Fatalf("history failed (%d): %s", histRec.Code, histRec.Body.String())
	}
	var histResp struct {
		History []model.SyncHistory `json:"history"`
	}
	_ = json.Unmarshal(histRec.Body.Bytes(), &histResp)
	if len(histResp.History) != 3 {
		t.Fatalf("expected 3 sync history entries (1 gist + 2 push-all), got %d", len(histResp.History))
	}

	// Verify debounced auto-sync coalesces rapid mutations into a single push!
	srv.autoSyncMu.Lock()
	srv.autoSyncDelay = 80 * time.Millisecond
	srv.autoSyncMu.Unlock()
	beforeAuto := gistPatchCount.Load()

	// Trigger 3 rapid mutations within <20ms.
	doJSON(t, srv, http.MethodPost, basePath+"/api/v1/members", map[string]any{"name": "m1"}, token)
	doJSON(t, srv, http.MethodPost, basePath+"/api/v1/members", map[string]any{"name": "m2"}, token)
	doJSON(t, srv, http.MethodPost, basePath+"/api/v1/members", map[string]any{"name": "m3"}, token)

	time.Sleep(350 * time.Millisecond)
	afterAuto := gistPatchCount.Load()
	if afterAuto-beforeAuto != 1 {
		t.Fatalf("expected 3 rapid mutations to coalesce into exactly 1 auto-push, got delta=%d", afterAuto-beforeAuto)
	}
}

func TestMilestone5LocalAndRemoteEncryptedBackupRestore(t *testing.T) {
	srv, st, _, _, basePath := newTestServer(t)
	defer srv.StopAutoSync()
	token := m5InitAdmin(t, srv, basePath)

	// Seed member `alice` and a self-signed cert (with actual PEM files on disk under certs/).
	aliceRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/members", map[string]any{"name": "alice"}, token)
	if aliceRec.Code != http.StatusCreated {
		t.Fatalf("create alice failed: %d %s", aliceRec.Code, aliceRec.Body.String())
	}
	var alice model.Member
	_ = json.Unmarshal(aliceRec.Body.Bytes(), &alice)

	certRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/certificates/self-signed", map[string]any{
		"node_id": 1,
		"name":    "Backup Cert",
		"domains": []string{"backup.example.com"},
	}, token)
	if certRec.Code != http.StatusCreated {
		t.Fatalf("create cert failed: %d %s", certRec.Code, certRec.Body.String())
	}
	var certObj model.Certificate
	_ = json.Unmarshal(certRec.Body.Bytes(), &certObj)

	// 1. Export AES-256-GCM encrypted backup.
	expRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/backup/export", map[string]any{
		"passphrase": "my-strong-master-passphrase",
	}, token)
	if expRec.Code != http.StatusOK {
		t.Fatalf("backup export failed (%d): %s", expRec.Code, expRec.Body.String())
	}
	encryptedArchive := expRec.Body.Bytes()
	if !backup.IsEncrypted(encryptedArchive) {
		t.Fatalf("expected SBOXENC1 encrypted backup header")
	}
	if !strings.Contains(expRec.Header().Get("Content-Disposition"), ".sboxbak") {
		t.Fatalf("expected .sboxbak filename, got %q", expRec.Header().Get("Content-Disposition"))
	}

	// 2. Export plaintext backup (sanity check).
	plainRec := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/backup/export", map[string]any{"passphrase": ""}, token)
	if plainRec.Code != http.StatusOK || backup.IsEncrypted(plainRec.Body.Bytes()) || !strings.Contains(plainRec.Header().Get("Content-Disposition"), ".tar.gz") {
		t.Fatalf("plaintext export unexpected: code=%d disp=%q", plainRec.Code, plainRec.Header().Get("Content-Disposition"))
	}

	// 3. Mutate DB and delete cert file on disk!
	delRec := doJSON(t, srv, http.MethodDelete, basePath+"/api/v1/members/"+strconvFormatInt(alice.ID), nil, token)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete alice failed (%d): %s", delRec.Code, delRec.Body.String())
	}
	doJSON(t, srv, http.MethodPost, basePath+"/api/v1/members", map[string]any{"name": "temp-post-backup"}, token)
	_ = os.Remove(certObj.CertPath)

	// Helper to send multipart POST /backup/restore.
	uploadRestore := func(data []byte, passphrase string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", "backup.sboxbak")
		_, _ = io.Copy(fw, bytes.NewReader(data))
		if passphrase != "" {
			_ = mw.WriteField("passphrase", passphrase)
		}
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, basePath+"/api/v1/backup/restore", &body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}

	// 4. Wrong passphrase must fail cleanly with 400 and leave DB intact.
	badRec := uploadRestore(encryptedArchive, "wrong-passphrase")
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on wrong passphrase, got %d: %s", badRec.Code, badRec.Body.String())
	}
	if _, err := st.GetMemberByName(context.Background(), "temp-post-backup"); err != nil {
		t.Fatalf("DB should remain operational after failed restore: %v", err)
	}

	// 5. Correct passphrase restores `alice` and cert files and removes `temp-post-backup`!
	okRec := uploadRestore(encryptedArchive, "my-strong-master-passphrase")
	if okRec.Code != http.StatusOK {
		t.Fatalf("restore failed (%d): %s", okRec.Code, okRec.Body.String())
	}
	var restoreResp struct {
		Restored backup.InspectResult `json:"restored"`
		BasePath string               `json:"base_path"`
	}
	_ = json.Unmarshal(okRec.Body.Bytes(), &restoreResp)
	if !restoreResp.Restored.Encrypted || !restoreResp.Restored.HasDB || restoreResp.Restored.CertFiles < 2 || restoreResp.BasePath != basePath {
		t.Fatalf("unexpected restore response: %+v", restoreResp)
	}
	if _, err := st.GetMemberByName(context.Background(), "alice"); err != nil {
		t.Fatalf("expected alice to be restored: %v", err)
	}
	if _, err := st.GetMemberByName(context.Background(), "temp-post-backup"); err == nil {
		t.Fatalf("expected temp-post-backup to be gone after restore")
	}
	if _, err := os.Stat(certObj.CertPath); err != nil {
		t.Fatalf("expected cert file %s to be restored on disk: %v", certObj.CertPath, err)
	}

	// 6. Remote GitHub Repo Encrypted Backup Push / List / Restore against mock GitHub Contents API!
	var ghMu sync.Mutex
	repoFiles := make(map[string][]byte) // full path -> raw file bytes

	mockGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghp_remote_backup_tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		prefix := "/repos/owner/sbox-backups/contents/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, prefix)

		switch r.Method {
		case http.MethodPut:
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			raw, _ := base64.StdEncoding.DecodeString(body.Content)
			ghMu.Lock()
			repoFiles[rel] = raw
			ghMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]any{
					"name": filepath.Base(rel),
					"path": rel,
					"sha":  "sha-" + filepath.Base(rel),
					"size": len(raw),
				},
			})
		case http.MethodGet:
			ghMu.Lock()
			defer ghMu.Unlock()
			if raw, ok := repoFiles[rel]; ok {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"type":     "file",
					"name":     filepath.Base(rel),
					"path":     rel,
					"sha":      "sha-" + filepath.Base(rel),
					"size":     len(raw),
					"encoding": "base64",
					"content":  base64.StdEncoding.EncodeToString(raw),
				})
				return
			}
			// Directory listing
			dirPrefix := strings.TrimSuffix(rel, "/") + "/"
			items := make([]map[string]any, 0)
			for p, raw := range repoFiles {
				if strings.HasPrefix(p, dirPrefix) {
					items = append(items, map[string]any{
						"type": "file",
						"name": filepath.Base(p),
						"path": p,
						"sha":  "sha-" + filepath.Base(p),
						"size": len(raw),
					})
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
		case http.MethodDelete:
			ghMu.Lock()
			delete(repoFiles, rel)
			ghMu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer mockGH.Close()

	// Configure remote backup and verify secret masking + preservation on empty update.
	cfgPut := doJSON(t, srv, http.MethodPut, basePath+"/api/v1/backup/remote/config", map[string]any{
		"enabled":        true,
		"type":           "github_repo",
		"repo":           "owner/sbox-backups",
		"branch":         "main",
		"path_prefix":    "backups/test",
		"token":          "ghp_remote_backup_tok",
		"passphrase":     "remote-aes-passphrase",
		"keep_count":     5,
		"interval_hours": 12,
		"api_base_url":   mockGH.URL,
	}, token)
	if cfgPut.Code != http.StatusOK {
		t.Fatalf("put remote config failed (%d): %s", cfgPut.Code, cfgPut.Body.String())
	}
	if strings.Contains(cfgPut.Body.String(), "ghp_remote_backup_tok") || strings.Contains(cfgPut.Body.String(), "remote-aes-passphrase") {
		t.Fatalf("remote config response leaked secrets: %s", cfgPut.Body.String())
	}
	var cfgView model.RemoteBackupConfigView
	_ = json.Unmarshal(cfgPut.Body.Bytes(), &cfgView)
	if !cfgView.HasToken || !cfgView.HasPassphrase {
		t.Fatalf("expected has_token & has_passphrase true: %+v", cfgView)
	}

	// Second PUT with blank secrets must preserve stored secrets.
	cfgPut2 := doJSON(t, srv, http.MethodPut, basePath+"/api/v1/backup/remote/config", map[string]any{
		"keep_count": 3,
		"token":      "",
		"passphrase": "",
	}, token)
	if cfgPut2.Code != http.StatusOK {
		t.Fatalf("second put remote config failed (%d): %s", cfgPut2.Code, cfgPut2.Body.String())
	}

	// Push remote encrypted backup.
	rPush := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/backup/remote/push", nil, token)
	if rPush.Code != http.StatusOK {
		t.Fatalf("remote backup push failed (%d): %s", rPush.Code, rPush.Body.String())
	}
	var pushOut struct {
		Entry backup.RemoteBackupEntry `json:"entry"`
	}
	_ = json.Unmarshal(rPush.Body.Bytes(), &pushOut)
	if pushOut.Entry.Path == "" || pushOut.Entry.Size == 0 {
		t.Fatalf("unexpected remote push entry: %+v", pushOut.Entry)
	}

	// Verify list returns the pushed entry.
	rList := doJSON(t, srv, http.MethodGet, basePath+"/api/v1/backup/remote/list", nil, token)
	if rList.Code != http.StatusOK {
		t.Fatalf("remote backup list failed (%d): %s", rList.Code, rList.Body.String())
	}
	var listOut struct {
		Entries []backup.RemoteBackupEntry `json:"entries"`
	}
	_ = json.Unmarshal(rList.Body.Bytes(), &listOut)
	if len(listOut.Entries) != 1 || listOut.Entries[0].Path != pushOut.Entry.Path {
		t.Fatalf("unexpected remote backup list: %+v", listOut.Entries)
	}

	// Delete alice, then restore directly from remote snapshot (omitting passphrase -> uses stored passphrase!).
	doJSON(t, srv, http.MethodDelete, basePath+"/api/v1/members/"+strconvFormatInt(alice.ID), nil, token)
	if _, err := st.GetMemberByName(context.Background(), "alice"); err == nil {
		t.Fatalf("expected alice deleted before remote restore")
	}

	rRestore := doJSON(t, srv, http.MethodPost, basePath+"/api/v1/backup/remote/restore", map[string]any{
		"path": pushOut.Entry.Path,
	}, token)
	if rRestore.Code != http.StatusOK {
		t.Fatalf("remote backup restore failed (%d): %s", rRestore.Code, rRestore.Body.String())
	}
	if _, err := st.GetMemberByName(context.Background(), "alice"); err != nil {
		t.Fatalf("expected alice restored from remote GitHub backup: %v", err)
	}
}
