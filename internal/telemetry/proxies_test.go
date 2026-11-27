package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestHub_ClashAPIProxyControls(t *testing.T) {
	const testSecret = "proxy-secret-xyz"

	var mu sync.Mutex
	selectedGroup := ""
	selectedProxy := ""
	lastDelayQuery := url.Values{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"proxies": map[string]any{
					"sub-airport": map[string]any{
						"name":    "sub-airport",
						"type":    "Selector",
						"now":     "sub-airport-auto",
						"all":     []string{"sub-airport-auto", "hk-01", "jp-01", "dead-01"},
						"history": []any{},
					},
					"sub-airport-auto": map[string]any{
						"name": "sub-airport-auto",
						"type": "URLTest",
						"now":  "hk-01",
						"all":  []string{"hk-01", "jp-01", "dead-01"},
						"history": []map[string]any{
							{"time": "2026-11-27T10:00:00Z", "delay": 45},
						},
					},
					"hk-01": map[string]any{
						"name": "hk-01",
						"type": "VLESS",
						"history": []map[string]any{
							{"time": "2026-11-27T09:59:00Z", "delay": 120},
							{"time": "2026-11-27T10:00:00Z", "delay": 45},
						},
					},
					"jp-01": map[string]any{
						"name": "jp-01",
						"type": "Trojan",
						"history": []map[string]any{
							{"time": "2026-11-27T10:00:00Z", "delay": 88},
						},
					},
					"dead-01": map[string]any{
						"name": "dead-01",
						"type": "Shadowsocks",
						"history": []map[string]any{
							{"time": "2026-11-27T10:00:00Z", "delay": 0},
						},
					},
					"direct": map[string]any{
						"name": "direct",
						"type": "Direct",
					},
				},
			})

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/proxies/") && strings.HasSuffix(r.URL.Path, "/delay"):
			rawName := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/proxies/"), "/delay")
			name, _ := url.PathUnescape(rawName)
			mu.Lock()
			lastDelayQuery = r.URL.Query()
			mu.Unlock()

			if name == "dead-01" {
				w.WriteHeader(http.StatusGatewayTimeout)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "An error occurred in the delay test"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]int{"delay": 63})

		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/proxies/"):
			groupName, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/proxies/"))
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body.Name == "invalid-node" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"Proxy does not exist"}`))
				return
			}
			mu.Lock()
			selectedGroup = groupName
			selectedProxy = body.Name
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	hub := NewHub(nil)
	hub.lifeMu.Lock()
	hub.clashAddr = strings.TrimPrefix(srv.URL, "http://")
	hub.secret = testSecret
	hub.lifeMu.Unlock()

	ctx := context.Background()

	// 1. ListProxyGroups extracts groups only (Selector & URLTest), sorted, with latest positive delays.
	groups, err := hub.ListProxyGroups(ctx)
	if err != nil {
		t.Fatalf("ListProxyGroups failed: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 proxy groups, got %d: %+v", len(groups), groups)
	}
	g0 := groups[0] // sub-airport
	if g0.Name != "sub-airport" || g0.Type != "Selector" || g0.Now != "sub-airport-auto" || len(g0.All) != 4 {
		t.Errorf("unexpected group[0]: %+v", g0)
	}
	if g0.Delays["hk-01"] != 45 || g0.Delays["jp-01"] != 88 || g0.Delays["sub-airport-auto"] != 45 {
		t.Errorf("unexpected group[0] delays: %+v", g0.Delays)
	}
	if _, hasZero := g0.Delays["dead-01"]; hasZero {
		t.Errorf("zero delay must not be populated in Delays map: %+v", g0.Delays)
	}

	// 2. TestProxyDelay uses default testURL/timeoutMS when omitted and handles URL-escaped proxy names.
	delay, err := hub.TestProxyDelay(ctx, "hk-01", "", 0)
	if err != nil {
		t.Fatalf("TestProxyDelay failed: %v", err)
	}
	if delay != 63 {
		t.Errorf("expected delay 63ms, got %d", delay)
	}
	mu.Lock()
	gotURL := lastDelayQuery.Get("url")
	gotTimeout := lastDelayQuery.Get("timeout")
	mu.Unlock()
	if gotURL != defaultProxyDelayTestURL || gotTimeout != "5000" {
		t.Errorf("expected default delay query url=%q timeout=5000, got url=%q timeout=%q", defaultProxyDelayTestURL, gotURL, gotTimeout)
	}

	// 3. TestProxyDelay surfaces Clash API failure cleanly.
	if _, err := hub.TestProxyDelay(ctx, "dead-01", "https://example.com", 2000); err == nil {
		t.Errorf("expected error for dead-01 timeout")
	}

	// 4. SelectProxy sends PUT /proxies/{group} with JSON {"name": proxy}.
	if err := hub.SelectProxy(ctx, "sub-airport", "jp-01"); err != nil {
		t.Fatalf("SelectProxy failed: %v", err)
	}
	mu.Lock()
	gotGroup, gotProxy := selectedGroup, selectedProxy
	mu.Unlock()
	if gotGroup != "sub-airport" || gotProxy != "jp-01" {
		t.Errorf("expected SelectProxy(sub-airport, jp-01), got (%q, %q)", gotGroup, gotProxy)
	}

	// 5. SelectProxy returns error on HTTP 400 and on missing parameters.
	if err := hub.SelectProxy(ctx, "sub-airport", "invalid-node"); err == nil {
		t.Errorf("expected error for invalid-node")
	}
	if err := hub.SelectProxy(ctx, "", "jp-01"); err == nil {
		t.Errorf("expected error for empty group name")
	}
	if _, err := hub.TestProxyDelay(ctx, "", "", 0); err == nil {
		t.Errorf("expected error for empty proxy name")
	}
}
