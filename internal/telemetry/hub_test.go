package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHub_LogRingBufferAndPubSub(t *testing.T) {
	h := NewHub(nil)
	h.logCap = 3
	h.logBuf = make([]LogEntry, 3)

	sub, unsub := h.SubscribeLogs()
	defer unsub()

	h.PublishLog("INFO", "panel", "msg-1")
	h.PublishLog("WARNING", "sing-box", "msg-2")
	h.PublishLog("error", "coremgr", "msg-3")
	h.PublishLog("debug", "panel", "msg-4")

	// Ring buffer capacity 3 keeps msg-2, msg-3, msg-4 in chronological order.
	recent := h.RecentLogs(10)
	if len(recent) != 3 {
		t.Fatalf("expected 3 buffered logs, got %d", len(recent))
	}
	if recent[0].Message != "msg-2" || recent[0].Level != "warn" || recent[2].Message != "msg-4" {
		t.Errorf("unexpected ring buffer entries: %+v", recent)
	}

	last2 := h.RecentLogs(2)
	if len(last2) != 2 || last2[0].Message != "msg-3" || last2[1].Message != "msg-4" {
		t.Errorf("unexpected RecentLogs(2): %+v", last2)
	}

	// Subscriber received all 4 live entries.
	for i := 1; i <= 4; i++ {
		select {
		case e := <-sub:
			if e.Message == "" {
				t.Errorf("empty message at %d", i)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for log %d", i)
		}
	}

	// Unsubscribe is idempotent and closes channel.
	unsub()
	unsub()
	if _, open := <-sub; open {
		t.Errorf("expected log subscription channel to be closed after unsub")
	}
}

func TestHub_DeltaTrafficCalculation(t *testing.T) {
	var mu sync.Mutex
	var recorded [][]TrafficDelta

	h := NewHub(func(deltas []TrafficDelta) {
		mu.Lock()
		cp := make([]TrafficDelta, len(deltas))
		copy(cp, deltas)
		recorded = append(recorded, cp)
		mu.Unlock()
	})

	// Frame 1: two connections for alice on vless-in, one for bob on hy2-in.
	frame1 := `{
		"uploadTotal": 300,
		"downloadTotal": 900,
		"connections": [
			{"id":"c1","upload":100,"download":200,"metadata":{"inboundName":"vless-in","inboundUser":"alice","network":"tcp"}},
			{"id":"c2","upload":50,"download":100,"metadata":{"inboundName":"vless-in","inboundUser":"alice","network":"tcp"}},
			{"id":"c3","upload":150,"download":600,"metadata":{"inboundName":"hy2-in","user":"bob","network":"udp"}}
		]
	}`
	h.handleConnectionsMessage([]byte(frame1))

	// Frame 2: c1 grew by (+40,+80), c2 closed, c3 unchanged, new c4 for bob (+10,+20).
	frame2 := `{
		"uploadTotal": 350,
		"downloadTotal": 1000,
		"connections": [
			{"id":"c1","upload":140,"download":280,"metadata":{"inboundName":"vless-in","inboundUser":"alice","network":"tcp"}},
			{"id":"c3","upload":150,"download":600,"metadata":{"inboundName":"hy2-in","user":"bob","network":"udp"}},
			{"id":"c4","upload":10,"download":20,"metadata":{"inboundName":"hy2-in","user":"bob","network":"udp"}}
		]
	}`
	h.handleConnectionsMessage([]byte(frame2))

	// Frame 3: no changes -> onDelta should NOT be invoked.
	h.handleConnectionsMessage([]byte(frame2))

	mu.Lock()
	defer mu.Unlock()
	if len(recorded) != 2 {
		t.Fatalf("expected 2 delta callbacks, got %d: %+v", len(recorded), recorded)
	}

	// Verify Frame 1 deltas (sorted by inbound, user).
	f1 := recorded[0]
	if len(f1) != 2 {
		t.Fatalf("expected 2 aggregated deltas in frame 1, got %+v", f1)
	}
	if f1[0] != (TrafficDelta{Inbound: "hy2-in", User: "bob", Upload: 150, Download: 600}) {
		t.Errorf("unexpected f1[0]: %+v", f1[0])
	}
	if f1[1] != (TrafficDelta{Inbound: "vless-in", User: "alice", Upload: 150, Download: 300}) {
		t.Errorf("unexpected f1[1]: %+v", f1[1])
	}

	// Verify Frame 2 incremental deltas only.
	f2 := recorded[1]
	if len(f2) != 2 {
		t.Fatalf("expected 2 aggregated deltas in frame 2, got %+v", f2)
	}
	if f2[0] != (TrafficDelta{Inbound: "hy2-in", User: "bob", Upload: 10, Download: 20}) {
		t.Errorf("unexpected f2[0]: %+v", f2[0])
	}
	if f2[1] != (TrafficDelta{Inbound: "vless-in", User: "alice", Upload: 40, Download: 80}) {
		t.Errorf("unexpected f2[1]: %+v", f2[1])
	}

	// Closed c2 should have been pruned from tracker.
	h.deltaMu.Lock()
	_, c2Tracked := h.connTracker["c2"]
	h.deltaMu.Unlock()
	if c2Tracked {
		t.Errorf("expected closed connection c2 to be pruned from connTracker")
	}
}

func TestHub_ClashAPIStreamsAndHTTP(t *testing.T) {
	const testSecret = "secret-token-123"
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	var deletedMu sync.Mutex
	var deletedID string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case r.URL.Path == "/traffic":
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			_ = c.WriteJSON(map[string]int64{"up": 1234, "down": 5678})
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}

		case r.URL.Path == "/memory":
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			_ = c.WriteJSON(map[string]int64{"inuse": 20971520, "oslimit": 0})
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}

		case r.URL.Path == "/logs":
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			_ = c.WriteJSON(map[string]string{"type": "info", "payload": "inbound/vless[vless-in]: accepted connection"})
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}

		case r.URL.Path == "/connections" && websocket.IsWebSocketUpgrade(r):
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			_ = c.WriteJSON(map[string]any{
				"uploadTotal":   1000,
				"downloadTotal": 2000,
				"connections": []map[string]any{
					{
						"id":       "conn-abc",
						"upload":   1000,
						"download": 2000,
						"start":    "2026-11-26T12:00:00Z",
						"chains":   []string{"direct"},
						"rule":     "final",
						"metadata": map[string]string{
							"network":         "tcp",
							"sourceIP":        "10.0.0.2",
							"sourcePort":      "54321",
							"host":            "example.com",
							"destinationPort": "443",
							"inboundName":     "vless-in",
							"inboundUser":     "alice",
						},
					},
				},
			})
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}

		case r.URL.Path == "/connections" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"uploadTotal":   1000,
				"downloadTotal": 2000,
				"connections": []map[string]any{
					{
						"id":       "conn-abc",
						"upload":   1000,
						"download": 2000,
						"start":    "2026-11-26T12:00:00Z",
						"chains":   []string{"direct"},
						"rule":     "final",
						"metadata": map[string]string{
							"network":         "tcp",
							"sourceIP":        "10.0.0.2",
							"sourcePort":      "54321",
							"host":            "example.com",
							"destinationPort": "443",
							"inboundName":     "vless-in",
							"inboundUser":     "alice",
						},
					},
				},
			})

		case strings.HasPrefix(r.URL.Path, "/connections/") && r.Method == http.MethodDelete:
			deletedMu.Lock()
			deletedID = strings.TrimPrefix(r.URL.Path, "/connections/")
			deletedMu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var deltaCh = make(chan []TrafficDelta, 4)
	hub := NewHub(func(deltas []TrafficDelta) {
		select {
		case deltaCh <- deltas:
		default:
		}
	})
	hub.metricsInterval = 50 * time.Millisecond
	hub.initialBackoff = 20 * time.Millisecond
	hub.maxBackoff = 100 * time.Millisecond

	metricsSub, unsubMetrics := hub.SubscribeMetrics()
	defer unsubMetrics()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := strings.TrimPrefix(srv.URL, "http://")
	hub.Start(ctx, addr, testSecret)
	defer hub.Stop()

	// Wait until all 4 streams have populated Snapshot.
	deadline := time.After(3 * time.Second)
	for {
		snap := hub.Snapshot()
		if snap.CoreConnected && snap.UpSpeed == 1234 && snap.DownSpeed == 5678 && snap.MemoryInuse == 20971520 && snap.ActiveConnections == 1 {
			break
		}
		select {
		case <-metricsSub:
		case <-deadline:
			t.Fatalf("timed out waiting for streams to populate snapshot; last=%+v", hub.Snapshot())
		}
	}

	// Verify log arrived from /logs WS.
	deadlineLog := time.After(2 * time.Second)
	for {
		logs := hub.RecentLogs(10)
		found := false
		for _, l := range logs {
			if strings.Contains(l.Message, "accepted connection") && l.Source == "sing-box" {
				found = true
				break
			}
		}
		if found {
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-deadlineLog:
			t.Fatalf("timed out waiting for /logs WS message; logs=%+v", hub.RecentLogs(10))
		}
	}

	// Verify delta callback fired.
	select {
	case ds := <-deltaCh:
		if len(ds) != 1 || ds[0].Inbound != "vless-in" || ds[0].User != "alice" || ds[0].Upload != 1000 || ds[0].Download != 2000 {
			t.Errorf("unexpected WS traffic delta: %+v", ds)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for traffic delta callback")
	}

	// Verify HTTP GET /connections.
	conns, err := hub.Connections(ctx)
	if err != nil {
		t.Fatalf("Connections() error: %v", err)
	}
	if len(conns) != 1 || conns[0].ID != "conn-abc" || conns[0].Destination != "example.com:443" || conns[0].Source != "10.0.0.2:54321" {
		t.Errorf("unexpected Connections() result: %+v", conns)
	}

	// Verify HTTP DELETE /connections/{id}.
	if err := hub.CloseConnection(ctx, "conn-abc"); err != nil {
		t.Fatalf("CloseConnection() error: %v", err)
	}
	deletedMu.Lock()
	gotDeleted := deletedID
	deletedMu.Unlock()
	if gotDeleted != "conn-abc" {
		t.Errorf("expected DELETE /connections/conn-abc, got %q", gotDeleted)
	}

	// Verify Stop resets connected state cleanly.
	hub.Stop()
	if snap := hub.Snapshot(); snap.CoreConnected || snap.UpSpeed != 0 || snap.DownSpeed != 0 {
		t.Errorf("expected disconnected zeroed snapshot after Stop, got %+v", snap)
	}
}

func TestHub_ReconnectsAfterDisconnect(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	var mu sync.Mutex
	trafficConnCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/traffic" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		mu.Lock()
		trafficConnCount++
		attempt := trafficConnCount
		mu.Unlock()

		if attempt == 1 {
			_ = c.WriteJSON(map[string]int64{"up": 100, "down": 200})
			// Simulate abrupt sing-box restart by closing the WS immediately.
			time.Sleep(30 * time.Millisecond)
			return
		}

		_ = c.WriteJSON(map[string]int64{"up": 999, "down": 888})
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	hub := NewHub(nil)
	hub.metricsInterval = 30 * time.Millisecond
	hub.initialBackoff = 20 * time.Millisecond
	hub.maxBackoff = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub.Start(ctx, strings.TrimPrefix(srv.URL, "http://"), "")
	defer hub.Stop()

	deadline := time.After(3 * time.Second)
	for {
		snap := hub.Snapshot()
		if snap.CoreConnected && snap.UpSpeed == 999 && snap.DownSpeed == 888 {
			break
		}
		select {
		case <-time.After(25 * time.Millisecond):
		case <-deadline:
			mu.Lock()
			cnt := trafficConnCount
			mu.Unlock()
			t.Fatalf("expected reconnect with updated traffic; attempts=%d snap=%+v", cnt, hub.Snapshot())
		}
	}
}

func TestHub_MetadataVariationsAndRapidConnectionChurn(t *testing.T) {
	var mu sync.Mutex
	totals := make(map[string][2]int64)

	h := NewHub(func(deltas []TrafficDelta) {
		mu.Lock()
		for _, d := range deltas {
			k := d.Inbound + "|" + d.User
			cur := totals[k]
			cur[0] += d.Upload
			cur[1] += d.Download
			totals[k] = cur
		}
		mu.Unlock()
	})

	// Poll 1: test all metadata variations:
	// - metadata.inbound + metadata.authUser
	// - metadata.inboundName + metadata.user
	// - metadata.type ("vless/vless-reality") + metadata.inboundUser
	// - metadata.type ("inbound/trojan/trojan-edge") + metadata.authUser
	poll1 := `{
		"uploadTotal": 1000,
		"downloadTotal": 2000,
		"connections": [
			{"id":"r1","upload":100,"download":200,"metadata":{"inbound":"hy2-in","authUser":"alice","network":"udp"}},
			{"id":"r2","upload":200,"download":400,"metadata":{"inboundName":"tuic-in","user":"bob","network":"udp"}},
			{"id":"r3","upload":300,"download":600,"metadata":{"type":"vless/vless-reality","inboundUser":"carol","network":"tcp"}},
			{"id":"r4","upload":400,"download":800,"metadata":{"type":"inbound/trojan/trojan-edge","authUser":"dave","network":"tcp"}}
		]
	}`
	h.handleConnectionsMessage([]byte(poll1))

	// Poll 2: rapid churn — r1, r2, r3, r4 ALL closed; short-lived r5 & r6 appear for alice & carol!
	poll2 := `{
		"uploadTotal": 1350,
		"downloadTotal": 2700,
		"connections": [
			{"id":"r5","upload":150,"download":300,"metadata":{"inbound":"hy2-in","authUser":"alice","network":"udp"}},
			{"id":"r6","upload":200,"download":400,"metadata":{"type":"vless/vless-reality","inboundUser":"carol","network":"tcp"}}
		]
	}`
	h.handleConnectionsMessage([]byte(poll2))

	// Poll 3: r5 grows by +50/+100, r6 closed, brand-new r7 for alice opens and carries +25/+50.
	poll3 := `{
		"uploadTotal": 1425,
		"downloadTotal": 2850,
		"connections": [
			{"id":"r5","upload":200,"download":400,"metadata":{"inbound":"hy2-in","authUser":"alice","network":"udp"}},
			{"id":"r7","upload":25,"download":50,"metadata":{"inbound":"hy2-in","user":"alice","network":"udp"}}
		]
	}`
	h.handleConnectionsMessage([]byte(poll3))

	mu.Lock()
	defer mu.Unlock()

	want := map[string][2]int64{
		"hy2-in|alice":        {100 + 150 + 50 + 25, 200 + 300 + 100 + 50},
		"tuic-in|bob":         {200, 400},
		"vless-reality|carol": {300 + 200, 600 + 400},
		"trojan-edge|dave":    {400, 800},
	}

	for k, w := range want {
		if got := totals[k]; got != w {
			t.Errorf("totals[%q] = %v, want %v", k, got, w)
		}
	}

	// Ensure tracker only retains live connections (r5, r7).
	h.deltaMu.Lock()
	trackedCount := len(h.connTracker)
	h.deltaMu.Unlock()
	if trackedCount != 2 {
		t.Errorf("expected 2 active connections in tracker after churn, got %d", trackedCount)
	}
}
