package cluster

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Hub manages the Master's local NodeController and all connected remote Agent WebSocket tunnels.
type Hub struct {
	local    *LocalDriver
	cb       HubCallbacks
	upgrader websocket.Upgrader

	mu      sync.RWMutex
	remotes map[int64]*RemoteDriver
	closed  bool
}

// NewHub constructs a cluster Hub wrapping the local node driver and callbacks.
func NewHub(local *LocalDriver, cb HubCallbacks) *Hub {
	return &Hub{
		local: local,
		cb:    cb,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  64 * 1024,
			WriteBufferSize: 64 * 1024,
			CheckOrigin: func(r *http.Request) bool {
				// Agents connect with Bearer node tokens rather than browser cookies.
				return true
			},
		},
		remotes: make(map[int64]*RemoteDriver),
	}
}

// Get returns the active NodeController for nodeID:
//   - local driver when nodeID matches the local node;
//   - live RemoteDriver when that remote node is online;
//   - (nil, false) when the remote node is offline or unknown.
func (h *Hub) Get(nodeID int64) (NodeController, bool) {
	if h.local != nil && nodeID == h.local.NodeID() {
		return h.local, true
	}
	h.mu.RLock()
	rd, ok := h.remotes[nodeID]
	h.mu.RUnlock()
	if !ok || rd == nil || !rd.IsOnline() {
		return nil, false
	}
	return rd, true
}

// IsOnline reports whether nodeID is currently reachable (local is always online).
func (h *Hub) IsOnline(nodeID int64) bool {
	if h.local != nil && nodeID == h.local.NodeID() {
		return true
	}
	h.mu.RLock()
	rd, ok := h.remotes[nodeID]
	h.mu.RUnlock()
	return ok && rd != nil && rd.IsOnline()
}

// OnlineNodeIDs returns a sorted list of all currently online node IDs (including local).
func (h *Hub) OnlineNodeIDs() []int64 {
	h.mu.RLock()
	ids := make([]int64, 0, len(h.remotes)+1)
	if h.local != nil {
		ids = append(ids, h.local.NodeID())
	}
	for id, rd := range h.remotes {
		if rd != nil && rd.IsOnline() {
			ids = append(ids, id)
		}
	}
	h.mu.RUnlock()

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// RuntimeState returns the live runtime state for nodeID if online.
func (h *Hub) RuntimeState(nodeID int64) (NodeRuntimeState, bool) {
	if h.local != nil && nodeID == h.local.NodeID() {
		return h.local.RuntimeState(), true
	}
	h.mu.RLock()
	rd, ok := h.remotes[nodeID]
	h.mu.RUnlock()
	if !ok || rd == nil || !rd.IsOnline() {
		return NodeRuntimeState{}, false
	}
	return rd.RuntimeState(), true
}

// DisconnectNode forcibly closes the active WebSocket connection for a remote node (if connected).
// Local nodes cannot be disconnected.
func (h *Hub) DisconnectNode(nodeID int64) {
	if h.local != nil && nodeID == h.local.NodeID() {
		return
	}
	h.mu.Lock()
	rd, ok := h.remotes[nodeID]
	if ok {
		delete(h.remotes, nodeID)
	}
	h.mu.Unlock()

	if ok && rd != nil {
		rd.close(errors.New("cluster: disconnected by master"))
		if h.cb.OnAgentDisconnected != nil {
			h.cb.OnAgentDisconnected(nodeID)
		}
	}
}

// Close shuts down the hub and closes all active remote agent connections.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	active := make([]*RemoteDriver, 0, len(h.remotes))
	for id, rd := range h.remotes {
		active = append(active, rd)
		delete(h.remotes, id)
	}
	h.mu.Unlock()

	for _, rd := range active {
		if rd != nil {
			rd.close(ErrHubClosed)
		}
	}
}

// HandleAgentWS authenticates an incoming reverse-connect Agent WebSocket, completes
// the initial NodeHello handshake, registers the live RemoteDriver, and serves until disconnect.
func (h *Hub) HandleAgentWS(w http.ResponseWriter, r *http.Request) {
	token := extractAgentToken(r)
	if token == "" || h.cb.AuthenticateToken == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	nodeID, ok := h.cb.AuthenticateToken(token)
	if !ok || nodeID <= 0 || (h.local != nil && nodeID == h.local.NodeID()) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	h.mu.RLock()
	closed := h.closed
	h.mu.RUnlock()
	if closed {
		http.Error(w, "cluster hub closed", http.StatusServiceUnavailable)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(maxWSMessageBytes)

	// Wait for the Agent's immediate NodeHello frame before marking it online.
	_ = conn.SetReadDeadline(time.Now().Add(wsHelloTimeout))
	_, rawHello, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return
	}

	var env wireEnvelope
	if err := json.Unmarshal(rawHello, &env); err != nil || env.Type != msgTypeHello {
		_ = conn.Close()
		return
	}
	var hello NodeHello
	if err := json.Unmarshal(env.Payload, &hello); err != nil {
		_ = conn.Close()
		return
	}

	rd := newRemoteDriver(nodeID, conn, hello, h.cb.OnTrafficDeltas)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		rd.close(ErrHubClosed)
		return
	}
	stale := h.remotes[nodeID]
	h.remotes[nodeID] = rd
	h.mu.Unlock()

	// Cleanly tear down any stale connection for the same nodeID without firing
	// a spurious disconnect for the newly registered live connection.
	if stale != nil {
		stale.close(errors.New("cluster: replaced by newer agent connection"))
	}

	go rd.pingLoop()

	if h.cb.OnAgentConnected != nil {
		go h.cb.OnAgentConnected(nodeID, hello)
	}

	// Block in readLoop for the lifetime of this WebSocket session.
	rd.readLoop()

	h.mu.Lock()
	wasCurrent := h.remotes[nodeID] == rd
	if wasCurrent {
		delete(h.remotes, nodeID)
	}
	hubClosed := h.closed
	h.mu.Unlock()

	if wasCurrent && !hubClosed && h.cb.OnAgentDisconnected != nil {
		h.cb.OnAgentDisconnected(nodeID)
	}
}

func extractAgentToken(r *http.Request) string {
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "" {
		if len(authz) > 7 && strings.EqualFold(authz[:7], "Bearer ") {
			if tok := strings.TrimSpace(authz[7:]); tok != "" {
				return tok
			}
		}
	}
	if tok := strings.TrimSpace(r.Header.Get("X-Agent-Token")); tok != "" {
		return tok
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}
