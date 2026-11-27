package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sbox-panel/sbox-panel/internal/cluster"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
)

// InboundPortCheckResult describes the local listener status and cross-node public reachability
// of an inbound port.
type InboundPortCheckResult struct {
	InboundID         int64  `json:"inbound_id"`
	NodeID            int64  `json:"node_id"`
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Network           string `json:"network"` // "tcp" | "udp_quic" | "udp"
	Status            string `json:"status"`  // "open" | "local_only" | "local_unverified" | "not_listening" | "node_offline"
	LocalListening    bool   `json:"local_listening"`
	ExternalChecked   bool   `json:"external_checked"`
	ExternalReachable bool   `json:"external_reachable"`
	LatencyMS         int64  `json:"latency_ms"`
	ProbedByNodeID    int64  `json:"probed_by_node_id,omitempty"`
	ProbedByNodeName  string `json:"probed_by_node_name,omitempty"`
	Message           string `json:"message"`
	Hint              string `json:"hint,omitempty"`
	CheckedAt         string `json:"checked_at"`
}

func resolveInboundProbeNetworks(ib model.Inbound) (probeNetwork, localNetwork, label string) {
	switch strings.ToLower(strings.TrimSpace(ib.Protocol)) {
	case "hysteria2", "tuic":
		return "udp_quic", "udp", "UDP"
	case "direct":
		var cfg struct {
			Network string `json:"network"`
		}
		if len(ib.Settings) > 0 {
			_ = json.Unmarshal(ib.Settings, &cfg)
		}
		if strings.EqualFold(strings.TrimSpace(cfg.Network), "udp") {
			return "udp", "udp", "UDP"
		}
		return "tcp", "tcp", "TCP"
	default:
		return "tcp", "tcp", "TCP"
	}
}

func isLoopbackOrEmptyHost(host string) bool {
	h := strings.Trim(strings.TrimSpace(host), "[]")
	if h == "" || strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

func (s *Server) selectExternalProber(ctx context.Context, targetNodeID int64) (cluster.NodeController, int64, string, bool) {
	if s.hub == nil {
		return nil, 0, "", false
	}

	// If target is a remote Agent, probe from Master (id=1) first.
	if targetNodeID != 1 {
		if ctrl, ok := s.hub.Get(1); ok && ctrl.IsOnline() {
			name := "master"
			if n, err := s.store.GetNode(ctx, 1); err == nil && strings.TrimSpace(n.Name) != "" {
				name = n.Name
			}
			return ctrl, 1, name, true
		}
	}

	// Otherwise pick the first online node different from targetNodeID.
	for _, nid := range s.hub.OnlineNodeIDs() {
		if nid == targetNodeID {
			continue
		}
		if ctrl, ok := s.hub.Get(nid); ok && ctrl.IsOnline() {
			name := fmt.Sprintf("node-%d", nid)
			if n, err := s.store.GetNode(ctx, nid); err == nil && strings.TrimSpace(n.Name) != "" {
				name = n.Name
			}
			return ctrl, nid, name, true
		}
	}
	return nil, 0, "", false
}

func (s *Server) checkSingleInboundPort(ctx context.Context, ib model.Inbound) InboundPortCheckResult {
	probeNet, localNet, protoLabel := resolveInboundProbeNetworks(ib)

	extPort := ib.ExternalPort
	if extPort <= 0 {
		extPort = ib.ListenPort
	}

	host := strings.TrimSpace(ib.ExternalHost)
	node, nodeErr := s.store.GetNode(ctx, ib.NodeID)
	if host == "" && nodeErr == nil && node != nil {
		if strings.TrimSpace(node.PublicIPv4) != "" {
			host = strings.TrimSpace(node.PublicIPv4)
		} else if strings.TrimSpace(node.PublicIPv6) != "" {
			host = strings.TrimSpace(node.PublicIPv6)
		} else if node.IsLocal {
			ps, _ := s.store.GetPanelSettings(ctx)
			if strings.TrimSpace(ps.PublicIPv4) != "" {
				host = strings.TrimSpace(ps.PublicIPv4)
			} else if strings.TrimSpace(ps.PublicIPv6) != "" {
				host = strings.TrimSpace(ps.PublicIPv6)
			}
		}
	}

	res := InboundPortCheckResult{
		InboundID: ib.ID,
		NodeID:    ib.NodeID,
		Host:      host,
		Port:      extPort,
		Network:   probeNet,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if s.hub == nil {
		res.Status = "node_offline"
		res.Message = "目标节点当前离线"
		return res
	}
	targetCtrl, ok := s.hub.Get(ib.NodeID)
	if !ok || !targetCtrl.IsOnline() {
		res.Status = "node_offline"
		res.Message = "目标节点当前离线"
		return res
	}

	// Step 1: Verify the inbound port is actively listening on the target node itself.
	localRes, localErr := targetCtrl.CheckLocalPort(ctx, localNet, ib.ListenPort)
	if localErr != nil || !localRes.Listening {
		res.Status = "not_listening"
		res.LocalListening = false
		res.Message = "本机未监听该端口"
		res.Hint = "请确认 sing-box 核心已启动且该端口未被其他程序占用"
		return res
	}
	res.LocalListening = true

	// Step 2: Pick an independent online prober node (Master <-> Agent) to verify real public reachability.
	proberCtrl, proberID, proberName, hasProber := s.selectExternalProber(ctx, ib.NodeID)
	if !hasProber || isLoopbackOrEmptyHost(host) {
		res.Status = "local_unverified"
		res.Message = "本机已监听（单机无法跨机验证安全组）"
		res.Hint = "接入其他节点后可自动跨机验证公网放行状态"
		return res
	}

	// Step 3: Execute cross-node external probe from the independent prober node.
	extRes, extErr := proberCtrl.ProbeExternalPort(ctx, cluster.ExternalPortProbeRequest{
		Host:      host,
		Port:      extPort,
		Network:   probeNet,
		TimeoutMS: 3500,
	})
	res.ExternalChecked = true
	res.ProbedByNodeID = proberID
	res.ProbedByNodeName = proberName
	res.LatencyMS = extRes.LatencyMS
	res.CheckedAt = time.Now().UTC().Format(time.RFC3339)

	if extErr == nil && extRes.Reachable {
		res.Status = "open"
		res.ExternalReachable = true
		res.Message = fmt.Sprintf("公网通（由 %s 实测 %dms）", proberName, extRes.LatencyMS)
		return res
	}

	res.Status = "local_only"
	res.ExternalReachable = false
	res.Message = "仅本机监听，外部探测超时"
	res.Hint = fmt.Sprintf("请检查云服务商安全组/VPC 防火墙是否放行 %s %d", protoLabel, extPort)
	return res
}

// handleCheckInboundPort serves POST /api/v1/inbounds/{id}/check-port.
func (s *Server) handleCheckInboundPort(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid inbound id")
		return
	}

	ib, err := s.store.GetInbound(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "inbound not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load inbound")
		return
	}

	result := s.checkSingleInboundPort(ctx, *ib)
	writeJSON(w, http.StatusOK, result)
}

// handleCheckAllInboundPorts serves POST /api/v1/inbounds/check-ports?node_id=<id>.
func (s *Server) handleCheckAllInboundPorts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	if _, err := s.store.GetNode(ctx, nodeID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load node")
		return
	}

	inbounds, err := s.store.ListInbounds(ctx, nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list inbounds")
		return
	}

	enabled := make([]model.Inbound, 0, len(inbounds))
	for _, ib := range inbounds {
		if ib.Enabled {
			enabled = append(enabled, ib)
		}
	}

	results := make([]InboundPortCheckResult, len(enabled))
	if len(enabled) > 0 {
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for i, ib := range enabled {
			wg.Add(1)
			go func(idx int, item model.Inbound) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				results[idx] = s.checkSingleInboundPort(ctx, item)
			}(i, ib)
		}
		wg.Wait()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
	})
}
