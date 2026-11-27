package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
)

func setupAuthedTestServer(t *testing.T) (*Server, *store.Store, *mockCore, string, string) {
	t.Helper()
	srv, st, mc, _, bp := newTestServer(t)
	rec := doJSON(t, srv, http.MethodPost, bp+"/api/v1/auth/setup", map[string]any{
		"username": "admin",
		"password": "Password123!",
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return srv, st, mc, bp, resp.Token
}

func TestMilestone2CertificatesToolsMembersInboundsAndTraffic(t *testing.T) {
	srv, st, mc, bp, tok := setupAuthedTestServer(t)

	// 1. Self-signed certificate + External import + Renew
	rec := doJSON(t, srv, http.MethodPost, bp+"/api/v1/certificates/self-signed", map[string]any{
		"name":       "Self Edge Cert",
		"domains":    []string{"edge.local", "127.0.0.1"},
		"valid_days": 365,
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create self-signed failed: %d %s", rec.Code, rec.Body.String())
	}
	var selfCert model.Certificate
	if err := json.Unmarshal(rec.Body.Bytes(), &selfCert); err != nil {
		t.Fatalf("decode selfCert: %v", err)
	}
	if selfCert.ID == 0 || selfCert.Type != "self_signed" || selfCert.Fingerprint == "" {
		t.Fatalf("unexpected selfCert: %+v", selfCert)
	}

	// External cert import reusing the generated PEM files
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/certificates/external", map[string]any{
		"name":      "Imported Cert",
		"cert_path": selfCert.CertPath,
		"key_path":  selfCert.KeyPath,
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create external cert failed: %d %s", rec.Code, rec.Body.String())
	}
	var extCert model.Certificate
	_ = json.Unmarshal(rec.Body.Bytes(), &extCert)

	// Renew self-signed cert
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/certificates/1/renew", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("renew cert failed: %d %s", rec.Code, rec.Body.String())
	}

	// List certificates
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/certificates", nil, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Self Edge Cert") {
		t.Fatalf("list certificates unexpected: %d %s", rec.Code, rec.Body.String())
	}

	// 2. Tools: Reality keypair, SS2022 key, local TLS probe
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/tools/reality-keypair", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("reality-keypair failed: %d %s", rec.Code, rec.Body.String())
	}
	var kp struct {
		PrivateKey string `json:"private_key"`
		PublicKey  string `json:"public_key"`
		ShortID    string `json:"short_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &kp)
	if kp.PrivateKey == "" || kp.PublicKey == "" || len(kp.ShortID) != 16 {
		t.Fatalf("unexpected reality keypair: %+v", kp)
	}

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/tools/ss2022-key", map[string]any{
		"method": "2022-blake3-aes-256-gcm",
	}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"key"`) {
		t.Fatalf("ss2022-key failed: %d %s", rec.Code, rec.Body.String())
	}

	// Local TLS test server for reality-probe (100% offline!)
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer tlsSrv.Close()
	targetHostPort := strings.TrimPrefix(tlsSrv.URL, "https://")
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/tools/reality-probe", map[string]any{
		"target": targetHostPort,
	}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reachable":true`) {
		t.Fatalf("reality-probe failed: %d %s", rec.Code, rec.Body.String())
	}

	// 3. Members: create 2, toggle, reset credentials
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/members", map[string]any{
		"name": "alice",
		"note": "Admin friend",
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create member alice failed: %d %s", rec.Code, rec.Body.String())
	}
	var alice model.Member
	_ = json.Unmarshal(rec.Body.Bytes(), &alice)
	if alice.UUID == "" || alice.Password == "" || alice.SS2022Key16 == "" || alice.SS2022Key32 == "" || alice.SubToken == "" {
		t.Fatalf("incomplete generated member credentials: %+v", alice)
	}

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/members", map[string]any{
		"name": "bob",
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create member bob failed: %d %s", rec.Code, rec.Body.String())
	}
	var bob model.Member
	_ = json.Unmarshal(rec.Body.Bytes(), &bob)

	oldUUID := alice.UUID
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/members/1/reset-credentials", map[string]any{"reset_sub_token": true}, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset-credentials failed: %d %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &alice)
	if alice.UUID == oldUUID {
		t.Fatalf("expected UUID to rotate after reset-credentials")
	}

	// 4. Inbounds: defaults + create VLESS-Reality, Hysteria2 (with cert), ShadowTLS v3, SS-2022
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/inbounds/defaults/vless", nil, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reality_private_key"`) {
		t.Fatalf("inbounds/defaults/vless failed: %d %s", rec.Code, rec.Body.String())
	}

	// Reject Hysteria2 without certificate
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/inbounds", map[string]any{
		"name":        "Bad Hy2",
		"tag":         "bad-hy2",
		"protocol":    "hysteria2",
		"listen_port": 28443,
		"settings":    map[string]any{"up_mbps": 100, "down_mbps": 100},
	}, tok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for hysteria2 without cert, got %d %s", rec.Code, rec.Body.String())
	}

	// Create VLESS Reality inbound
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/inbounds", map[string]any{
		"name":        "VLESS Reality",
		"tag":         "vless-reality",
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
			"server_name":              "www.microsoft.com",
		},
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create vless inbound failed: %d %s", rec.Code, rec.Body.String())
	}

	// Create Hysteria2 inbound referencing selfCert
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/inbounds", map[string]any{
		"name":           "Hysteria2 Main",
		"tag":            "hy2-main",
		"protocol":       "hysteria2",
		"listen_port":    28443,
		"certificate_id": selfCert.ID,
		"member_mode":    "selected",
		"member_ids":     []int64{alice.ID},
		"settings": map[string]any{
			"up_mbps":      200,
			"down_mbps":    200,
			"server_ports": "28443-28500",
		},
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create hy2 inbound failed: %d %s", rec.Code, rec.Body.String())
	}

	// Verify GET /core/config compiles BOTH inbounds with correct member credentials
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/core/config", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /core/config failed: %d %s", rec.Code, rec.Body.String())
	}
	var cfgResp struct {
		ConfigJSON string `json:"config_json"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cfgResp)
	if !strings.Contains(cfgResp.ConfigJSON, `"vless-reality"`) ||
		!strings.Contains(cfgResp.ConfigJSON, `"hy2-main"`) ||
		!strings.Contains(cfgResp.ConfigJSON, alice.UUID) ||
		!strings.Contains(cfgResp.ConfigJSON, bob.UUID) {
		t.Fatalf("compiled config missing expected inbounds or member UUIDs:\n%s", cfgResp.ConfigJSON)
	}
	// Ensure subscription-only server_ports is not leaked into sing-box JSON
	if strings.Contains(cfgResp.ConfigJSON, "28443-28500") {
		t.Fatalf("server_ports should not appear in compiled sing-box JSON")
	}

	// Toggle bob off and confirm bob's UUID disappears from compiled config immediately
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/members/2/toggle", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle bob failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/core/config", nil, tok)
	_ = json.Unmarshal(rec.Body.Bytes(), &cfgResp)
	if strings.Contains(cfgResp.ConfigJSON, bob.UUID) {
		t.Fatalf("disabled member bob UUID should not appear in compiled config")
	}
	// Core EnsureConfig should have received updated bytes
	mc.mu.Lock()
	hasWrittenCfg := len(mc.lastConfig) > 0
	mc.mu.Unlock()
	if !hasWrittenCfg {
		t.Fatalf("expected RecompileAndApply to write config to core manager")
	}

	// 5. Certificate delete guard (409 when referenced, 200 with ?force=true)
	rec = doJSON(t, srv, http.MethodDelete, bp+"/api/v1/certificates/1", nil, tok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 deleting referenced cert, got %d %s", rec.Code, rec.Body.String())
	}

	// Panel HTTPS cert binding in settings + 409 check on extCert
	rec = doJSON(t, srv, http.MethodPut, bp+"/api/v1/settings", map[string]any{
		"panel_cert_id": extCert.ID,
	}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"panel_cert_id":2`) {
		t.Fatalf("PUT /settings panel_cert_id failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodDelete, bp+"/api/v1/certificates/2", nil, tok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 deleting panel_cert_id cert, got %d", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodDelete, bp+"/api/v1/certificates/2?force=true", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 force deleting cert, got %d %s", rec.Code, rec.Body.String())
	}

	// 6. Traffic stats & reset endpoints
	if err := st.RecordTrafficDeltas(context.Background(), 1, time.Now().UTC(), []store.TrafficDeltaInput{
		{InboundTag: "vless-reality", MemberName: "alice", Upload: 2048, Download: 8192},
	}); err != nil {
		t.Fatalf("RecordTrafficDeltas: %v", err)
	}
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/traffic/stats?range=24h", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /traffic/stats failed: %d %s", rec.Code, rec.Body.String())
	}
	var trafficResp model.TrafficStatsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &trafficResp)
	if trafficResp.Range != "24h" || len(trafficResp.Series) != 1 || trafficResp.Series[0].DownloadBytes != 8192 {
		t.Fatalf("unexpected traffic stats: %+v", trafficResp)
	}

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/traffic/reset", map[string]any{"scope": "all"}, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /traffic/reset failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/traffic/stats?range=24h", nil, tok)
	_ = json.Unmarshal(rec.Body.Bytes(), &trafficResp)
	if len(trafficResp.Series) != 0 {
		t.Fatalf("expected empty series after reset, got %+v", trafficResp.Series)
	}
}
