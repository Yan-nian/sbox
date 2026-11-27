package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/sbox-panel/sbox-panel/internal/cluster"
	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
)

const installScriptURL = "https://raw.githubusercontent.com/Yan-nian/sbox/main/deploy/install.sh"

// parseNodeIDQuery reads `?node_id=` returning defaultID when empty/invalid.
// If allowAll is true, `"0"` or `"all"` returns 0.
func parseNodeIDQuery(r *http.Request, defaultID int64, allowAll bool) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get("node_id"))
	if raw == "" {
		return defaultID
	}
	if allowAll && (raw == "0" || strings.EqualFold(raw, "all")) {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return defaultID
	}
	return v
}

func (s *Server) buildMasterURL(r *http.Request) string {
	scheme := "http"
	if isRequestSecure(r) {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		ps, _ := s.store.GetPanelSettings(r.Context())
		h := ps.PublicIPv4
		if h == "" {
			h = "127.0.0.1"
		}
		host = fmt.Sprintf("%s:%d", h, ps.ListenPort)
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, s.BasePath())
}

func (s *Server) buildAgentInstallCommand(r *http.Request, token string) string {
	masterURL := s.buildMasterURL(r)
	return fmt.Sprintf("curl -fsSL %s | sudo bash -s -- agent --master %s --token %s", installScriptURL, masterURL, token)
}

func (s *Server) buildNodeView(n model.Node, inboundCount int) model.NodeView {
	v := model.NodeView{
		ID:            n.ID,
		Name:          n.Name,
		IsLocal:       n.IsLocal,
		Online:        false,
		CoreRunning:   false,
		CoreVersion:   n.CoreVersion,
		InboundCount:  inboundCount,
		OS:            n.OS,
		Arch:          n.Arch,
		Hostname:      n.Hostname,
		AgentVersion:  n.AgentVersion,
		DataDir:       n.DataDir,
		PublicIPv4:    n.PublicIPv4,
		PublicIPv6:    n.PublicIPv6,
		OverridePatch: n.OverridePatch,
		LogLevel:      n.LogLevel,
		GithubProxy:   n.GithubProxy,
		LastSeenAt:    n.LastSeenAt,
		Note:          n.Note,
		CreatedAt:     n.CreatedAt,
		UpdatedAt:     n.UpdatedAt,
	}

	if s.hub != nil {
		if rt, ok := s.hub.RuntimeState(n.ID); ok {
			v.Online = rt.Online
			v.CoreRunning = rt.Core.Running
			if rt.Core.Version != "" {
				v.CoreVersion = rt.Core.Version
			}
			v.UpSpeed = rt.Metrics.UpSpeed
			v.DownSpeed = rt.Metrics.DownSpeed
			v.ActiveConnections = rt.Metrics.ActiveConnections
			v.MemBytes = rt.MemBytes
			v.UptimeSec = rt.UptimeSec
			if rt.OS != "" {
				v.OS = rt.OS
			}
			if rt.Arch != "" {
				v.Arch = rt.Arch
			}
			if rt.Hostname != "" {
				v.Hostname = rt.Hostname
			}
			if rt.AgentVersion != "" {
				v.AgentVersion = rt.AgentVersion
			}
			if rt.DataDir != "" {
				v.DataDir = rt.DataDir
			}
			if !rt.LastSeenAt.IsZero() {
				ts := rt.LastSeenAt.UTC()
				v.LastSeenAt = &ts
			}
		}
	}
	return v
}

func (s *Server) computeClusterSummary(ctx context.Context) model.ClusterSummary {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return model.ClusterSummary{}
	}
	summary := model.ClusterSummary{
		TotalNodes: len(nodes),
	}
	for _, n := range nodes {
		if s.hub == nil {
			continue
		}
		if rt, ok := s.hub.RuntimeState(n.ID); ok && rt.Online {
			summary.OnlineNodes++
			summary.TotalUpSpeed += rt.Metrics.UpSpeed
			summary.TotalDownSpeed += rt.Metrics.DownSpeed
			summary.TotalConnections += rt.Metrics.ActiveConnections
		}
	}
	return summary
}

// resolveOnlineController verifies nodeID exists and returns its live NodeController,
// writing 404 or 503 ("节点当前离线") on error.
func (s *Server) resolveOnlineController(w http.ResponseWriter, ctx context.Context, nodeID int64) (cluster.NodeController, *model.Node, bool) {
	if nodeID <= 0 {
		nodeID = 1
	}
	node, err := s.store.GetNode(ctx, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return nil, nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load node")
		return nil, nil, false
	}
	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "节点当前离线")
		return nil, node, false
	}
	ctrl, ok := s.hub.Get(nodeID)
	if !ok || !ctrl.IsOnline() {
		writeError(w, http.StatusServiceUnavailable, "节点当前离线")
		return nil, node, false
	}
	return ctrl, node, true
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list nodes")
		return
	}
	counts, _ := s.store.InboundCountsByNode(ctx)
	views := make([]model.NodeView, 0, len(nodes))
	for _, n := range nodes {
		views = append(views, s.buildNodeView(n, counts[n.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": views,
	})
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Name       string `json:"name"`
		Note       string `json:"note"`
		PublicIPv4 string `json:"public_ipv4"`
		PublicIPv6 string `json:"public_ipv6"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 64 {
		writeError(w, http.StatusBadRequest, "node name must be 1 to 64 characters")
		return
	}

	n := &model.Node{
		Name:       req.Name,
		IsLocal:    false,
		Note:       strings.TrimSpace(req.Note),
		PublicIPv4: strings.TrimSpace(req.PublicIPv4),
		PublicIPv6: strings.TrimSpace(req.PublicIPv6),
		LogLevel:   "info",
	}
	if err := s.store.CreateNode(ctx, n); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "a node with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create node: "+err.Error())
		return
	}

	view := s.buildNodeView(*n, 0)
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("created remote node %q (id=%d)", n.Name, n.ID))
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"node":            view,
		"token":           n.Token,
		"install_command": s.buildAgentInstallCommand(r, n.Token),
	})
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}

	n, err := s.store.GetNode(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load node")
		return
	}

	var req struct {
		Name        *string `json:"name"`
		Note        *string `json:"note"`
		PublicIPv4  *string `json:"public_ipv4"`
		PublicIPv6  *string `json:"public_ipv6"`
		GithubProxy *string `json:"github_proxy"`
		LogLevel    *string `json:"log_level"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	logLevelChanged := false
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 64 {
			writeError(w, http.StatusBadRequest, "node name must be 1 to 64 characters")
			return
		}
		n.Name = name
	}
	if req.Note != nil {
		n.Note = strings.TrimSpace(*req.Note)
	}
	if req.PublicIPv4 != nil {
		n.PublicIPv4 = strings.TrimSpace(*req.PublicIPv4)
	}
	if req.PublicIPv6 != nil {
		n.PublicIPv6 = strings.TrimSpace(*req.PublicIPv6)
	}
	if req.GithubProxy != nil {
		n.GithubProxy = strings.TrimSpace(*req.GithubProxy)
	}
	if req.LogLevel != nil {
		ll := strings.ToLower(strings.TrimSpace(*req.LogLevel))
		switch ll {
		case "trace", "debug", "info", "warn", "error", "fatal":
			if n.LogLevel != ll {
				logLevelChanged = true
			}
			n.LogLevel = ll
		default:
			writeError(w, http.StatusBadRequest, "invalid log_level")
			return
		}
	}

	if err := s.store.UpdateNode(ctx, n); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "a node with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update node")
		return
	}

	// Keep settings table aligned when editing local master node (id=1)
	if n.IsLocal {
		_ = s.store.SetSettings(ctx, map[string]string{
			model.SettingInstanceID:  n.Name,
			model.SettingPublicIPv4:  n.PublicIPv4,
			model.SettingPublicIPv6:  n.PublicIPv6,
			model.SettingGithubProxy: n.GithubProxy,
			model.SettingLogLevel:    n.LogLevel,
		})
	}

	if logLevelChanged {
		_ = s.RecompileAndApplyNode(ctx, n.ID)
	}

	counts, _ := s.store.InboundCountsByNode(ctx)
	writeJSON(w, http.StatusOK, s.buildNodeView(*n, counts[n.ID]))
}

func (s *Server) handleResetNodeToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	if id == 1 {
		writeError(w, http.StatusBadRequest, "cannot reset token for local master node")
		return
	}

	token, err := s.store.ResetNodeToken(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset node token: "+err.Error())
		return
	}

	if s.hub != nil {
		s.hub.DisconnectNode(id)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":           token,
		"install_command": s.buildAgentInstallCommand(r, token),
	})
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	if id == 1 {
		writeError(w, http.StatusBadRequest, "cannot delete local master node")
		return
	}

	if _, err := s.store.GetNode(ctx, id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load node")
		return
	}

	force := strings.EqualFold(r.URL.Query().Get("force"), "true")
	inCount, certCount, err := s.store.CountNodeResources(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check node resources")
		return
	}
	if (inCount > 0 || certCount > 0) && !force {
		writeError(w, http.StatusConflict, "node still has inbounds or certificates; pass ?force=true to delete")
		return
	}

	if s.hub != nil {
		s.hub.DisconnectNode(id)
	}
	if err := s.store.DeleteNode(ctx, id, force); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete node: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type nodeUpgradeBatchItem struct {
	NodeID   int64                 `json:"node_id"`
	NodeName string                `json:"node_name"`
	Result   coremgr.UpgradeResult `json:"result"`
}

func (s *Server) handleUpgradeAllNodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		TagName  string `json:"tag_name"`
		ProxyURL string `json:"proxy_url"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.TagName = strings.TrimSpace(req.TagName)
	if req.TagName == "" {
		writeError(w, http.StatusBadRequest, "tag_name is required")
		return
	}

	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list nodes")
		return
	}

	type target struct {
		node model.Node
		ctrl cluster.NodeController
	}
	var targets []target
	for _, n := range nodes {
		if s.hub == nil {
			continue
		}
		if ctrl, ok := s.hub.Get(n.ID); ok && ctrl.IsOnline() {
			targets = append(targets, target{node: n, ctrl: ctrl})
		}
	}

	results := make([]nodeUpgradeBatchItem, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(idx int, t target) {
			defer wg.Done()
			proxy := strings.TrimSpace(req.ProxyURL)
			if proxy == "" {
				proxy = t.node.GithubProxy
			}
			// Ensure config exists on target node before upgrading so `sing-box check` has a config
			if cfg, _, bundles, err := s.buildConfigForNode(ctx, t.node.ID, nil); err == nil {
				_ = t.ctrl.ApplyConfig(ctx, cfg, bundles)
			}
			res, err := t.ctrl.UpgradeCore(ctx, coremgr.UpgradeOptions{
				TagName:  req.TagName,
				ProxyURL: proxy,
			})
			if err != nil {
				res = coremgr.UpgradeResult{
					Success: false,
					Message: err.Error(),
				}
			} else if res.Success && res.ToVersion != "" {
				_ = s.store.UpdateNodeRuntimeInfo(ctx, t.node.ID, store.NodeRuntimeUpdate{
					CoreVersion: res.ToVersion,
				})
			}
			results[idx] = nodeUpgradeBatchItem{
				NodeID:   t.node.ID,
				NodeName: t.node.Name,
				Result:   res,
			}
		}(i, t)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
	})
}
