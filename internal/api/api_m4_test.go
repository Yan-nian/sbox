package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/syncer"
)

func TestMilestone4UpstreamsWARPProxiesAndVisualRouting(t *testing.T) {
	srv, _, _, bp, tok := setupAuthedTestServer(t)

	// 1. Mock Clash YAML upstream subscription server
	clashYAML := `proxies:
  - name: "Tokyo-01"
    type: vless
    server: jp.example.com
    port: 443
    uuid: 11111111-2222-3333-4444-555555555555
    tls: true
    servername: jp.example.com
    udp: true
  - name: "Singapore-01"
    type: hysteria2
    server: sg.example.com
    port: 8443
    password: hy2-password
    sni: sg.example.com
`
	subSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(clashYAML))
	}))
	defer subSrv.Close()

	// Create upstream sub with fetch_now: true
	rec := doJSON(t, srv, http.MethodPost, bp+"/api/v1/upstreams", map[string]any{
		"node_id":        0,
		"name":           "Main Airport",
		"tag":            "airport",
		"url":            subSrv.URL,
		"enable_urltest": true,
		"fetch_now":      true,
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /upstreams failed: %d %s", rec.Code, rec.Body.String())
	}
	var up model.UpstreamSub
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatalf("decode upstream: %v", err)
	}
	if up.Tag != "airport" || up.CachedFormat != "clash" || up.NodeCount != 2 || up.LastError != "" {
		t.Fatalf("unexpected fetched upstream: %+v", up)
	}

	// Sync manually
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/upstreams/1/sync", nil, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"node_count":2`) {
		t.Fatalf("POST /upstreams/1/sync failed: %d %s", rec.Code, rec.Body.String())
	}

	// Regression check: interval_minutes: 0 (manual-only) preserved on PUT and POST
	rec = doJSON(t, srv, http.MethodPut, bp+"/api/v1/upstreams/1", map[string]any{
		"interval_minutes": 0,
	}, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /upstreams/1 interval_minutes=0 failed: %d %s", rec.Code, rec.Body.String())
	}
	var upUpdated model.UpstreamSub
	_ = json.Unmarshal(rec.Body.Bytes(), &upUpdated)
	if upUpdated.IntervalMinutes != 0 {
		t.Fatalf("expected interval_minutes=0 preserved on PUT, got %d", upUpdated.IntervalMinutes)
	}

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/upstreams", map[string]any{
		"node_id":          1,
		"name":             "Manual Sub",
		"tag":              "manual-sub",
		"url":              subSrv.URL,
		"interval_minutes": 0,
		"fetch_now":        false,
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /upstreams manual-only failed: %d %s", rec.Code, rec.Body.String())
	}
	var upManual model.UpstreamSub
	_ = json.Unmarshal(rec.Body.Bytes(), &upManual)
	if upManual.IntervalMinutes != 0 {
		t.Fatalf("expected interval_minutes=0 preserved on POST, got %d", upManual.IntervalMinutes)
	}
	_ = doJSON(t, srv, http.MethodDelete, bp+"/api/v1/upstreams/2", nil, tok)

	// 2. Mock one-click Cloudflare WARP registration
	origRegister := warpRegisterFn
	warpRegisterFn = func(_ context.Context, _ string) (*syncer.WARPAccount, error) {
		return &syncer.WARPAccount{
			AccountID:    "acct-test-1",
			AccessToken:  "tok-test-1",
			PrivateKey:   "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=",
			PublicKey:    "YmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmI=",
			PeerPubKey:   "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
			EndpointHost: "engage.cloudflareclient.com",
			EndpointPort: 2408,
			LocalIPv4:    "172.16.0.2/32",
			LocalIPv6:    "2606:4700:110:8a36::2/128",
			Reserved:     []int{1, 2, 3},
			MTU:          1280,
		}, nil
	}
	defer func() { warpRegisterFn = origRegister }()

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/outbounds/warp/register", map[string]any{
		"node_id":   1,
		"name":      "Cloudflare WARP",
		"tag":       "warp",
		"ipv6_only": false,
		"mtu":       1280,
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /outbounds/warp/register failed: %d %s", rec.Code, rec.Body.String())
	}
	var warpResp struct {
		Outbound model.OutboundExtra `json:"outbound"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &warpResp)
	if warpResp.Outbound.Tag != "warp" || warpResp.Outbound.Type != "warp" {
		t.Fatalf("unexpected WARP outbound: %+v", warpResp.Outbound)
	}

	// Manual SOCKS5 extra outbound
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/outbounds", map[string]any{
		"node_id": 1,
		"name":    "Landing SOCKS",
		"tag":     "socks-exit",
		"type":    "socks",
		"settings": map[string]any{
			"server":      "127.0.0.1",
			"server_port": 1080,
		},
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /outbounds socks failed: %d %s", rec.Code, rec.Body.String())
	}

	// 3. Live Proxy Group Inspection, Delay Test & Manual Switch
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/proxies?node_id=1", nil, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"airport"`) {
		t.Fatalf("GET /proxies failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/proxies/delay?node_id=1", map[string]any{
		"proxy": "airport-sg-1",
	}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"delay_ms":77`) {
		t.Fatalf("POST /proxies/delay failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodPut, bp+"/api/v1/proxies/select?node_id=1", map[string]any{
		"group": "airport",
		"proxy": "airport-sg-1",
	}, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /proxies/select failed: %d %s", rec.Code, rec.Body.String())
	}

	// 4. Routing Presets, Custom Rule, Reorder & Default Outbound
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/routing/presets", nil, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ai_services") {
		t.Fatalf("GET /routing/presets failed: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/routing/rules/apply-preset", map[string]any{
		"node_id":      1,
		"preset_key":   "ai_services",
		"outbound_tag": "warp",
	}, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply-preset ai_services failed: %d %s", rec.Code, rec.Body.String())
	}
	var aiRule model.RouteRule
	_ = json.Unmarshal(rec.Body.Bytes(), &aiRule)

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/routing/rules/apply-preset", map[string]any{
		"node_id":    1,
		"preset_key": "block_bt",
	}, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply-preset block_bt failed: %d %s", rec.Code, rec.Body.String())
	}
	var btRule model.RouteRule
	_ = json.Unmarshal(rec.Body.Bytes(), &btRule)

	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/routing/rules", map[string]any{
		"node_id":       1,
		"name":          "Custom Example",
		"action":        "route",
		"outbound_tag":  "airport",
		"domain_suffix": []string{"example.org"},
	}, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /routing/rules failed: %d %s", rec.Code, rec.Body.String())
	}
	var customRule model.RouteRule
	_ = json.Unmarshal(rec.Body.Bytes(), &customRule)

	// Reorder rules and set default outbound
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/routing/rules/reorder", map[string]any{
		"node_id":  1,
		"rule_ids": []int64{btRule.ID, customRule.ID, aiRule.ID},
	}, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /routing/rules/reorder failed: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, srv, http.MethodPut, bp+"/api/v1/routing/default-outbound", map[string]any{
		"node_id":          1,
		"default_outbound": "airport",
	}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"default_outbound":"airport"`) {
		t.Fatalf("PUT /routing/default-outbound failed: %d %s", rec.Code, rec.Body.String())
	}

	// Verify GET /routing/rules exposes available_outbounds (direct, warp, socks-exit, airport, airport-auto)
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/routing/rules?node_id=1", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /routing/rules failed: %d %s", rec.Code, rec.Body.String())
	}
	var rulesResp struct {
		Rules              []model.RouteRule `json:"rules"`
		DefaultOutbound    string            `json:"default_outbound"`
		AvailableOutbounds []string          `json:"available_outbounds"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &rulesResp)
	if rulesResp.DefaultOutbound != "airport" || len(rulesResp.Rules) != 3 || rulesResp.Rules[0].ID != btRule.ID {
		t.Fatalf("unexpected routing/rules response: %+v", rulesResp)
	}
	availStr := strings.Join(rulesResp.AvailableOutbounds, ",")
	for _, want := range []string{"direct", "warp", "socks-exit", "airport", "airport-auto"} {
		if !strings.Contains(availStr, want) {
			t.Fatalf("missing %q in available_outbounds: %v", want, rulesResp.AvailableOutbounds)
		}
	}

	// 5. Inspect GET /core/config -> verify compiled modern sing-box 1.12+ JSON!
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/core/config?node_id=1", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /core/config failed: %d %s", rec.Code, rec.Body.String())
	}
	var cfgResp struct {
		ConfigJSON string `json:"config_json"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cfgResp)
	cfg := cfgResp.ConfigJSON
	for _, needle := range []string{
		`"endpoints"`,
		`"type": "wireguard"`,
		`"tag": "warp"`,
		`"type": "selector"`,
		`"tag": "airport"`,
		`"type": "urltest"`,
		`"tag": "airport-auto"`,
		`"tag": "socks-exit"`,
		`"rule_set"`,
		`geosite-openai.srs`,
		`"action": "sniff"`,
		`"action": "reject"`,
		`"example.org"`,
		`"final": "airport"`,
	} {
		if !strings.Contains(cfg, needle) {
			t.Fatalf("compiled sing-box config missing %s:\n%s", needle, cfg)
		}
	}
	if strings.Contains(cfg, `"type": "block"`) || strings.Contains(cfg, `"outbound": "block"`) {
		t.Fatalf("compiled config must not emit legacy block outbound:\n%s", cfg)
	}
}
