package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultLogCapacity     = 1000
	defaultMetricsInterval = 1 * time.Second
	defaultInitialBackoff  = 500 * time.Millisecond
	defaultMaxBackoff      = 5 * time.Second
)

// LogEntry represents a normalized log line emitted by the panel, supervisor, or sing-box core.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Source  string    `json:"source"`
}

// Snapshot is a point-in-time telemetry summary broadcast every second.
type Snapshot struct {
	UpSpeed           int64  `json:"up_speed"`
	DownSpeed         int64  `json:"down_speed"`
	MemoryInuse       int64  `json:"memory_inuse"`
	ActiveConnections int    `json:"active_connections"`
	TotalUpload       int64  `json:"total_upload"`
	TotalDownload     int64  `json:"total_download"`
	CoreConnected     bool   `json:"core_connected"`
	UpdatedAt         string `json:"updated_at"`
}

// ConnectionItem represents an active connection observed from sing-box's Clash API.
type ConnectionItem struct {
	ID          string   `json:"id"`
	Inbound     string   `json:"inbound"`
	User        string   `json:"user"`
	Network     string   `json:"network"`
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
	Rule        string   `json:"rule"`
	Chains      []string `json:"chains"`
	Upload      int64    `json:"upload"`
	Download    int64    `json:"download"`
	Start       string   `json:"start"`
}

// TrafficDelta represents incremental bytes transferred for an (Inbound, User) pair.
type TrafficDelta struct {
	Inbound  string
	User     string
	Upload   int64
	Download int64
}

type connCounter struct {
	inbound      string
	user         string
	lastUpload   int64
	lastDownload int64
}

type deltaKey struct {
	inbound string
	user    string
}

type clashTrafficPayload struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

type clashMemoryPayload struct {
	Inuse   int64 `json:"inuse"`
	OSLimit int64 `json:"oslimit"`
}

type clashLogPayload struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

type clashConnectionsPayload struct {
	DownloadTotal int64                 `json:"downloadTotal"`
	UploadTotal   int64                 `json:"uploadTotal"`
	Memory        int64                 `json:"memory"`
	Connections   []clashConnectionWire `json:"connections"`
}

type clashConnectionWire struct {
	ID          string   `json:"id"`
	Upload      int64    `json:"upload"`
	Download    int64    `json:"download"`
	Start       string   `json:"start"`
	Chains      []string `json:"chains"`
	Rule        string   `json:"rule"`
	RulePayload string   `json:"rulePayload"`
	Metadata    struct {
		Network         string `json:"network"`
		Type            string `json:"type"`
		SourceIP        string `json:"sourceIP"`
		SourcePort      string `json:"sourcePort"`
		DestinationIP   string `json:"destinationIP"`
		DestinationPort string `json:"destinationPort"`
		Host            string `json:"host"`
		Inbound         string `json:"inbound"`
		InboundName     string `json:"inboundName"`
		InboundTag      string `json:"inboundTag"`
		InboundUser     string `json:"inboundUser"`
		AuthUser        string `json:"authUser"`
		User            string `json:"user"`
	} `json:"metadata"`
}

// Hub maintains Clash API HTTP/WebSocket telemetry streams, a ring-buffer log store,
// a 1-second metrics broadcaster, and per-inbound/per-user traffic delta accounting.
type Hub struct {
	onDelta func(deltas []TrafficDelta)

	// Configurable timings (overridable in unit tests).
	metricsInterval time.Duration
	initialBackoff  time.Duration
	maxBackoff      time.Duration
	httpClient      *http.Client
	dialer          *websocket.Dialer

	// Lifecycle management.
	lifeMu    sync.Mutex
	clashAddr string
	secret    string
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	// Metrics & connection status.
	metricsMu     sync.RWMutex
	snapshot      Snapshot
	streamUp      map[string]bool
	latestConns   []ConnectionItem

	// Connection-ID delta tracker.
	deltaMu     sync.Mutex
	connTracker map[string]connCounter

	// Ring-buffer log store.
	logMu      sync.RWMutex
	logBuf     []LogEntry
	logCap     int
	logHead    int
	logCount   int

	// Pub/sub subscribers.
	logSubMu    sync.RWMutex
	logSubs     map[uint64]chan LogEntry
	nextLogSub  uint64

	metricSubMu   sync.RWMutex
	metricSubs    map[uint64]chan Snapshot
	nextMetricSub uint64
}

// NewHub constructs a telemetry Hub invoking onDelta whenever connection byte counters advance.
func NewHub(onDelta func(deltas []TrafficDelta)) *Hub {
	return &Hub{
		onDelta:         onDelta,
		metricsInterval: defaultMetricsInterval,
		initialBackoff:  defaultInitialBackoff,
		maxBackoff:      defaultMaxBackoff,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				Proxy: nil, // Local loopback Clash API must never traverse external HTTP_PROXY.
			},
		},
		dialer: &websocket.Dialer{
			Proxy:            nil,
			HandshakeTimeout: 5 * time.Second,
		},
		streamUp:    make(map[string]bool, 4),
		connTracker: make(map[string]connCounter),
		logBuf:      make([]LogEntry, defaultLogCapacity),
		logCap:      defaultLogCapacity,
		logSubs:     make(map[uint64]chan LogEntry),
		metricSubs:  make(map[uint64]chan Snapshot),
	}
}

// Start connects WebSocket streams (/traffic, /memory, /logs, /connections) to clashAddr
// with exponential-backoff reconnection and begins the 1-second metrics snapshot broadcast.
// Calling Start while already running cleanly stops the previous loops first.
func (h *Hub) Start(ctx context.Context, clashAddr, secret string) {
	h.Stop()

	h.lifeMu.Lock()
	h.clashAddr = strings.TrimSpace(clashAddr)
	h.secret = strings.TrimSpace(secret)
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	h.cancel = cancel

	addr := h.clashAddr
	sec := h.secret

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.runMetricsBroadcaster(runCtx)
	}()

	if addr != "" {
		streams := []struct {
			name    string
			path    string
			handler func([]byte)
		}{
			{name: "traffic", path: "/traffic", handler: h.handleTrafficMessage},
			{name: "memory", path: "/memory", handler: h.handleMemoryMessage},
			{name: "logs", path: "/logs?level=info", handler: h.handleLogMessage},
			{name: "connections", path: "/connections", handler: h.handleConnectionsMessage},
		}

		for _, s := range streams {
			stream := s
			h.wg.Add(1)
			go func() {
				defer h.wg.Done()
				h.runWSStream(runCtx, addr, sec, stream.name, stream.path, stream.handler)
			}()
		}
	}
	h.lifeMu.Unlock()
}

// Stop halts all WebSocket loops and the metrics broadcaster, waiting for clean shutdown.
func (h *Hub) Stop() {
	h.lifeMu.Lock()
	cancel := h.cancel
	h.cancel = nil
	h.lifeMu.Unlock()

	if cancel != nil {
		cancel()
	}
	h.wg.Wait()

	h.metricsMu.Lock()
	for k := range h.streamUp {
		h.streamUp[k] = false
	}
	h.snapshot.CoreConnected = false
	h.snapshot.UpSpeed = 0
	h.snapshot.DownSpeed = 0
	h.snapshot.MemoryInuse = 0
	h.snapshot.ActiveConnections = 0
	h.metricsMu.Unlock()
}

// PublishLog records a log entry in the ring buffer and fans it out to active log subscribers.
func (h *Hub) PublishLog(level, source, msg string) {
	trimmed := strings.TrimSpace(msg)
	if trimmed == "" {
		return
	}
	entry := LogEntry{
		Time:    time.Now().UTC(),
		Level:   normalizeLogLevel(level),
		Message: trimmed,
		Source:  strings.TrimSpace(source),
	}
	if entry.Source == "" {
		entry.Source = "panel"
	}

	h.logMu.Lock()
	h.logBuf[h.logHead] = entry
	h.logHead = (h.logHead + 1) % h.logCap
	if h.logCount < h.logCap {
		h.logCount++
	}
	h.logMu.Unlock()

	h.logSubMu.RLock()
	for _, ch := range h.logSubs {
		select {
		case ch <- entry:
		default:
			// Drop for slow consumers rather than stalling log producers.
		}
	}
	h.logSubMu.RUnlock()
}

// RecentLogs returns up to limit most recent log entries in chronological order (oldest first).
// If limit <= 0, all buffered entries are returned.
func (h *Hub) RecentLogs(limit int) []LogEntry {
	h.logMu.RLock()
	defer h.logMu.RUnlock()

	if h.logCount == 0 {
		return []LogEntry{}
	}

	n := h.logCount
	if limit > 0 && limit < n {
		n = limit
	}

	out := make([]LogEntry, n)
	// Start index of the oldest entry among the last n entries.
	start := (h.logHead - n + h.logCap) % h.logCap
	for i := 0; i < n; i++ {
		out[i] = h.logBuf[(start+i)%h.logCap]
	}
	return out
}

// SubscribeLogs registers a live log subscriber and returns its channel and an idempotent unsubscribe function.
func (h *Hub) SubscribeLogs() (<-chan LogEntry, func()) {
	ch := make(chan LogEntry, 128)
	h.logSubMu.Lock()
	id := h.nextLogSub
	h.nextLogSub++
	h.logSubs[id] = ch
	h.logSubMu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.logSubMu.Lock()
			if existing, ok := h.logSubs[id]; ok {
				delete(h.logSubs, id)
				close(existing)
			}
			h.logSubMu.Unlock()
		})
	}
	return ch, cancel
}

// Snapshot returns the latest aggregated telemetry metrics snapshot.
func (h *Hub) Snapshot() Snapshot {
	h.metricsMu.RLock()
	snap := h.snapshot
	h.metricsMu.RUnlock()

	if snap.UpdatedAt == "" {
		snap.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return snap
}

// SubscribeMetrics registers a 1-second snapshot subscriber and returns its channel and unsubscribe function.
func (h *Hub) SubscribeMetrics() (<-chan Snapshot, func()) {
	ch := make(chan Snapshot, 16)
	h.metricSubMu.Lock()
	id := h.nextMetricSub
	h.nextMetricSub++
	h.metricSubs[id] = ch
	h.metricSubMu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.metricSubMu.Lock()
			if existing, ok := h.metricSubs[id]; ok {
				delete(h.metricSubs, id)
				close(existing)
			}
			h.metricSubMu.Unlock()
		})
	}
	return ch, cancel
}

// Connections queries Clash API GET /connections and returns normalized active connections.
func (h *Hub) Connections(ctx context.Context) ([]ConnectionItem, error) {
	addr, secret := h.getTarget()
	if addr == "" {
		return []ConnectionItem{}, errors.New("clash API address is not configured")
	}

	reqURL := toHTTPBaseURL(addr) + "/connections"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build connections request: %w", err)
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch connections: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("clash API GET /connections returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload clashConnectionsPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode connections response: %w", err)
	}

	items := convertConnections(payload.Connections)
	h.recordConnectionsSnapshot(payload, items)
	return items, nil
}

// CloseConnection terminates an active connection via Clash API DELETE /connections/{id}.
func (h *Hub) CloseConnection(ctx context.Context, id string) error {
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return errors.New("connection id is required")
	}
	addr, secret := h.getTarget()
	if addr == "" {
		return errors.New("clash API address is not configured")
	}

	reqURL := toHTTPBaseURL(addr) + "/connections/" + url.PathEscape(trimmedID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("build close connection request: %w", err)
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("close connection %s: %w", trimmedID, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("clash API DELETE /connections/%s returned HTTP %d", trimmedID, resp.StatusCode)
	}
	return nil
}

func (h *Hub) getTarget() (string, string) {
	h.lifeMu.Lock()
	defer h.lifeMu.Unlock()
	return h.clashAddr, h.secret
}

func (h *Hub) runMetricsBroadcaster(ctx context.Context) {
	interval := h.metricsInterval
	if interval <= 0 {
		interval = defaultMetricsInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap := h.Snapshot()
			snap.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

			h.metricSubMu.RLock()
			for _, ch := range h.metricSubs {
				select {
				case ch <- snap:
				default:
				}
			}
			h.metricSubMu.RUnlock()
		}
	}
}

func (h *Hub) runWSStream(ctx context.Context, addr, secret, name, path string, handler func([]byte)) {
	wsURL := toWSBaseURL(addr) + path
	headers := http.Header{}
	if secret != "" {
		headers.Set("Authorization", "Bearer "+secret)
	}

	backoff := h.initialBackoff
	if backoff <= 0 {
		backoff = defaultInitialBackoff
	}
	maxBackoff := h.maxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}

	for {
		if ctx.Err() != nil {
			h.setStreamConnected(name, false)
			return
		}

		conn, _, err := h.dialer.DialContext(ctx, wsURL, headers)
		if err != nil {
			h.setStreamConnected(name, false)
			if !sleepWithContext(ctx, backoff) {
				return
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		h.setStreamConnected(name, true)

		// Ensure blocking ReadMessage unblocks immediately when ctx is canceled.
		readDone := make(chan struct{})
		go func(c *websocket.Conn) {
			select {
			case <-ctx.Done():
				_ = c.Close()
			case <-readDone:
			}
		}(conn)

		gotMsg := false
		for {
			_, data, readErr := conn.ReadMessage()
			if readErr != nil {
				break
			}
			if !gotMsg {
				gotMsg = true
				backoff = h.initialBackoff
				if backoff <= 0 {
					backoff = defaultInitialBackoff
				}
			}
			handler(data)
		}

		close(readDone)
		_ = conn.Close()
		h.setStreamConnected(name, false)

		if ctx.Err() != nil {
			return
		}

		if !sleepWithContext(ctx, backoff) {
			return
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (h *Hub) setStreamConnected(name string, connected bool) {
	h.metricsMu.Lock()
	h.streamUp[name] = connected
	anyConnected := false
	for _, up := range h.streamUp {
		if up {
			anyConnected = true
			break
		}
	}
	h.snapshot.CoreConnected = anyConnected
	if !anyConnected {
		h.snapshot.UpSpeed = 0
		h.snapshot.DownSpeed = 0
		h.snapshot.MemoryInuse = 0
		h.snapshot.ActiveConnections = 0
	}
	h.snapshot.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	h.metricsMu.Unlock()
}

func (h *Hub) handleTrafficMessage(data []byte) {
	var p clashTrafficPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return
	}
	h.metricsMu.Lock()
	h.snapshot.UpSpeed = p.Up
	h.snapshot.DownSpeed = p.Down
	h.snapshot.CoreConnected = true
	h.snapshot.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	h.metricsMu.Unlock()
}

func (h *Hub) handleMemoryMessage(data []byte) {
	var p clashMemoryPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return
	}
	h.metricsMu.Lock()
	h.snapshot.MemoryInuse = p.Inuse
	h.snapshot.CoreConnected = true
	h.snapshot.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	h.metricsMu.Unlock()
}

func (h *Hub) handleLogMessage(data []byte) {
	var p clashLogPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return
	}
	h.PublishLog(p.Type, "sing-box", p.Payload)
}

func (h *Hub) handleConnectionsMessage(data []byte) {
	var p clashConnectionsPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return
	}
	items := convertConnections(p.Connections)
	h.recordConnectionsSnapshot(p, items)
}

func (h *Hub) recordConnectionsSnapshot(p clashConnectionsPayload, items []ConnectionItem) {
	h.metricsMu.Lock()
	h.snapshot.ActiveConnections = len(items)
	h.snapshot.TotalUpload = p.UploadTotal
	h.snapshot.TotalDownload = p.DownloadTotal
	if p.Memory > 0 {
		h.snapshot.MemoryInuse = p.Memory
	}
	h.snapshot.CoreConnected = true
	h.snapshot.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	h.latestConns = items
	h.metricsMu.Unlock()

	deltas := h.computeTrafficDeltas(items)
	if len(deltas) > 0 && h.onDelta != nil {
		h.onDelta(deltas)
	}
}

func (h *Hub) computeTrafficDeltas(items []ConnectionItem) []TrafficDelta {
	h.deltaMu.Lock()
	defer h.deltaMu.Unlock()

	seen := make(map[string]struct{}, len(items))
	agg := make(map[deltaKey]*TrafficDelta)

	for _, item := range items {
		if item.ID == "" {
			continue
		}
		seen[item.ID] = struct{}{}

		prev, exists := h.connTracker[item.ID]
		var du, dd int64
		if !exists {
			du = item.Upload
			dd = item.Download
		} else {
			if item.Upload >= prev.lastUpload {
				du = item.Upload - prev.lastUpload
			} else {
				du = item.Upload
			}
			if item.Download >= prev.lastDownload {
				dd = item.Download - prev.lastDownload
			} else {
				dd = item.Download
			}
		}

		h.connTracker[item.ID] = connCounter{
			inbound:      item.Inbound,
			user:         item.User,
			lastUpload:   item.Upload,
			lastDownload: item.Download,
		}

		if du > 0 || dd > 0 {
			key := deltaKey{inbound: item.Inbound, user: item.User}
			entry, ok := agg[key]
			if !ok {
				entry = &TrafficDelta{Inbound: item.Inbound, User: item.User}
				agg[key] = entry
			}
			entry.Upload += du
			entry.Download += dd
		}
	}

	// Prune closed connections so memory stays bounded.
	for id := range h.connTracker {
		if _, alive := seen[id]; !alive {
			delete(h.connTracker, id)
		}
	}

	if len(agg) == 0 {
		return nil
	}

	deltas := make([]TrafficDelta, 0, len(agg))
	for _, d := range agg {
		deltas = append(deltas, *d)
	}
	sort.Slice(deltas, func(i, j int) bool {
		if deltas[i].Inbound != deltas[j].Inbound {
			return deltas[i].Inbound < deltas[j].Inbound
		}
		return deltas[i].User < deltas[j].User
	})
	return deltas
}

func convertConnections(raw []clashConnectionWire) []ConnectionItem {
	items := make([]ConnectionItem, 0, len(raw))
	for _, c := range raw {
		inbound := firstNonEmpty(c.Metadata.InboundName, c.Metadata.InboundTag, c.Metadata.Inbound)
		if inbound == "" && c.Metadata.Type != "" {
			t := strings.TrimSpace(c.Metadata.Type)
			if idx := strings.LastIndex(t, "/"); idx >= 0 && idx+1 < len(t) {
				inbound = strings.TrimSpace(t[idx+1:])
			} else {
				inbound = t
			}
		}
		user := firstNonEmpty(c.Metadata.InboundUser, c.Metadata.AuthUser, c.Metadata.User)

		source := formatHostPort(c.Metadata.SourceIP, c.Metadata.SourcePort)
		destHost := firstNonEmpty(c.Metadata.Host, c.Metadata.DestinationIP)
		dest := formatHostPort(destHost, c.Metadata.DestinationPort)

		rule := c.Rule
		if c.RulePayload != "" {
			if rule != "" {
				rule = rule + "(" + c.RulePayload + ")"
			} else {
				rule = c.RulePayload
			}
		}

		chains := make([]string, len(c.Chains))
		copy(chains, c.Chains)

		items = append(items, ConnectionItem{
			ID:          c.ID,
			Inbound:     inbound,
			User:        user,
			Network:     c.Metadata.Network,
			Source:      source,
			Destination: dest,
			Rule:        rule,
			Chains:      chains,
			Upload:      c.Upload,
			Download:    c.Download,
			Start:       c.Start,
		})
	}
	return items
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func formatHostPort(host, port string) string {
	h := strings.TrimSpace(host)
	p := strings.TrimSpace(port)
	if h == "" {
		return p
	}
	if p == "" {
		return h
	}
	return net.JoinHostPort(h, p)
}

func normalizeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace":
		return "trace"
	case "debug":
		return "debug"
	case "info":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error", "err":
		return "error"
	case "fatal", "panic":
		return "fatal"
	default:
		return "info"
	}
}

func toHTTPBaseURL(addr string) string {
	a := strings.TrimSpace(addr)
	if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
		return strings.TrimRight(a, "/")
	}
	if strings.HasPrefix(a, "ws://") {
		return "http://" + strings.TrimRight(strings.TrimPrefix(a, "ws://"), "/")
	}
	if strings.HasPrefix(a, "wss://") {
		return "https://" + strings.TrimRight(strings.TrimPrefix(a, "wss://"), "/")
	}
	return "http://" + strings.TrimRight(a, "/")
}

func toWSBaseURL(addr string) string {
	a := strings.TrimSpace(addr)
	if strings.HasPrefix(a, "ws://") || strings.HasPrefix(a, "wss://") {
		return strings.TrimRight(a, "/")
	}
	if strings.HasPrefix(a, "https://") {
		return "wss://" + strings.TrimRight(strings.TrimPrefix(a, "https://"), "/")
	}
	if strings.HasPrefix(a, "http://") {
		return "ws://" + strings.TrimRight(strings.TrimPrefix(a, "http://"), "/")
	}
	return "ws://" + strings.TrimRight(a, "/")
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
