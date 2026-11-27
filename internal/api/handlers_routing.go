package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sbox-panel/sbox-panel/internal/configgen"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
	"github.com/sbox-panel/sbox-panel/internal/syncer"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
)

// warpRegisterFn is a package-level seam defaulting to syncer.RegisterWARP (overridable in tests).
var warpRegisterFn = syncer.RegisterWARP

func sanitizeTagSegment(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' || r == '.' {
			b.WriteByte('-')
		}
		if b.Len() >= 32 {
			break
		}
	}
	return strings.Trim(b.String(), "-_")
}

func (s *Server) recompileScope(ctx context.Context, nodeID int64) {
	if nodeID <= 0 {
		s.RecompileAllOnlineNodes(ctx)
		return
	}
	_ = s.RecompileAndApplyNode(ctx, nodeID)
}

// --- Upstream Outbound Subscriptions ---

func (s *Server) syncUpstreamRecord(ctx context.Context, u *model.UpstreamSub) error {
	tagPrefix := u.Tag
	if tagPrefix == "" {
		tagPrefix = fmt.Sprintf("sub-%d", u.ID)
	}
	res, err := syncer.FetchSubscription(ctx, syncer.FetchOptions{
		URL:          u.URL,
		ProxyURL:     u.ProxyURL,
		IncludeRegex: u.IncludeRegex,
		ExcludeRegex: u.ExcludeRegex,
		TagPrefix:    tagPrefix + "-",
	})
	now := time.Now().UTC()
	u.LastFetchedAt = &now
	if err != nil {
		u.LastError = err.Error()
		_ = s.store.UpdateUpstreamSub(ctx, u)
		return err
	}

	nodesJSON, _ := json.Marshal(res.Nodes)
	u.CachedFormat = res.Format
	u.CachedNodesJSON = string(nodesJSON)
	u.NodeCount = len(res.Nodes)
	u.LastError = ""
	if err := s.store.UpdateUpstreamSub(ctx, u); err != nil {
		return err
	}
	s.recompileScope(ctx, u.NodeID)
	return nil
}

// SyncDueUpstreams checks all enabled upstream subscriptions due for refresh, fetches them,
// persists cached parsed nodes, and recompiles affected online nodes.
func (s *Server) SyncDueUpstreams(ctx context.Context) {
	due, err := s.store.ListDueUpstreamSubs(ctx, time.Now().UTC())
	if err != nil || len(due) == 0 {
		return
	}
	for i := range due {
		u := &due[i]
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.syncUpstreamRecord(fetchCtx, u)
		cancel()
		if err != nil {
			if s.telemetry != nil {
				s.telemetry.PublishLog("error", "panel", fmt.Sprintf("scheduled upstream sync %q failed: %v", u.Name, err))
			}
		} else if s.telemetry != nil {
			s.telemetry.PublishLog("info", "panel", fmt.Sprintf("scheduled upstream sync %q updated (%d nodes, %s)", u.Name, u.NodeCount, u.CachedFormat))
		}
	}
}

func (s *Server) handleListUpstreams(w http.ResponseWriter, r *http.Request) {
	nodeID := parseNodeIDQuery(r, 1, true)
	list, err := s.store.ListUpstreamSubs(r.Context(), nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list upstream subscriptions")
		return
	}
	if list == nil {
		list = []model.UpstreamSub{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"upstreams": list,
	})
}

func (s *Server) handleCreateUpstream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		NodeID          *int64 `json:"node_id"`
		Name            string `json:"name"`
		Tag             string `json:"tag"`
		URL             string `json:"url"`
		Enabled         *bool  `json:"enabled"`
		IntervalMinutes *int   `json:"interval_minutes"`
		IncludeRegex    string `json:"include_regex"`
		ExcludeRegex    string `json:"exclude_regex"`
		EnableURLTest   *bool  `json:"enable_urltest"`
		TestURL         string `json:"test_url"`
		ProxyURL        string `json:"proxy_url"`
		FetchNow        *bool  `json:"fetch_now"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" || req.URL == "" {
		writeError(w, http.StatusBadRequest, "name and url are required")
		return
	}

	nodeID := int64(0) // default 0 (global shared) unless explicitly scoped
	if req.NodeID != nil {
		nodeID = *req.NodeID
		if nodeID < 0 {
			nodeID = 0
		}
	}
	if nodeID > 0 {
		if _, err := s.store.GetNode(ctx, nodeID); err != nil {
			writeError(w, http.StatusBadRequest, "target node not found")
			return
		}
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	enableURLTest := true
	if req.EnableURLTest != nil {
		enableURLTest = *req.EnableURLTest
	}
	interval := 360
	if req.IntervalMinutes != nil && *req.IntervalMinutes >= 0 {
		interval = *req.IntervalMinutes
	}
	fetchNow := true
	if req.FetchNow != nil {
		fetchNow = *req.FetchNow
	}

	u := &model.UpstreamSub{
		NodeID:          nodeID,
		Name:            req.Name,
		Tag:             sanitizeTagSegment(req.Tag),
		URL:             req.URL,
		Enabled:         enabled,
		IntervalMinutes: interval,
		IncludeRegex:    req.IncludeRegex,
		ExcludeRegex:    req.ExcludeRegex,
		EnableURLTest:   enableURLTest,
		TestURL:         strings.TrimSpace(req.TestURL),
		ProxyURL:        strings.TrimSpace(req.ProxyURL),
	}

	if err := s.store.CreateUpstreamSub(ctx, u); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "an upstream subscription with this tag already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create upstream subscription: "+err.Error())
		return
	}

	if fetchNow {
		fetchCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		_ = s.syncUpstreamRecord(fetchCtx, u) // saves last_error cleanly even if remote fetch fails
		cancel()
	}

	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("created upstream subscription %q (tag=%s, nodes=%d)", u.Name, u.Tag, u.NodeCount))
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleUpdateUpstream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid upstream id")
		return
	}

	u, err := s.store.GetUpstreamSub(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "upstream subscription not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load upstream subscription")
		return
	}

	var req struct {
		NodeID          *int64  `json:"node_id"`
		Name            *string `json:"name"`
		Tag             *string `json:"tag"`
		URL             *string `json:"url"`
		Enabled         *bool   `json:"enabled"`
		IntervalMinutes *int    `json:"interval_minutes"`
		IncludeRegex    *string `json:"include_regex"`
		ExcludeRegex    *string `json:"exclude_regex"`
		EnableURLTest   *bool   `json:"enable_urltest"`
		TestURL         *string `json:"test_url"`
		ProxyURL        *string `json:"proxy_url"`
		FetchNow        *bool   `json:"fetch_now"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	oldNodeID := u.NodeID
	if req.NodeID != nil && *req.NodeID >= 0 {
		if *req.NodeID > 0 {
			if _, err := s.store.GetNode(ctx, *req.NodeID); err != nil {
				writeError(w, http.StatusBadRequest, "target node not found")
				return
			}
		}
		u.NodeID = *req.NodeID
	}
	if req.Name != nil {
		if n := strings.TrimSpace(*req.Name); n != "" {
			u.Name = n
		}
	}
	if req.Tag != nil {
		if t := sanitizeTagSegment(*req.Tag); t != "" {
			u.Tag = t
		}
	}
	if req.URL != nil {
		if urlStr := strings.TrimSpace(*req.URL); urlStr != "" {
			u.URL = urlStr
		}
	}
	if req.Enabled != nil {
		u.Enabled = *req.Enabled
	}
	if req.IntervalMinutes != nil && *req.IntervalMinutes >= 0 {
		u.IntervalMinutes = *req.IntervalMinutes
	}
	if req.IncludeRegex != nil {
		u.IncludeRegex = *req.IncludeRegex
	}
	if req.ExcludeRegex != nil {
		u.ExcludeRegex = *req.ExcludeRegex
	}
	if req.EnableURLTest != nil {
		u.EnableURLTest = *req.EnableURLTest
	}
	if req.TestURL != nil {
		u.TestURL = strings.TrimSpace(*req.TestURL)
	}
	if req.ProxyURL != nil {
		u.ProxyURL = strings.TrimSpace(*req.ProxyURL)
	}

	if err := s.store.UpdateUpstreamSub(ctx, u); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "an upstream subscription with this tag already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update upstream subscription")
		return
	}

	if req.FetchNow != nil && *req.FetchNow {
		fetchCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		_ = s.syncUpstreamRecord(fetchCtx, u)
		cancel()
	} else {
		if oldNodeID != u.NodeID {
			s.recompileScope(ctx, oldNodeID)
		}
		s.recompileScope(ctx, u.NodeID)
	}

	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleSyncUpstream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid upstream id")
		return
	}

	u, err := s.store.GetUpstreamSub(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "upstream subscription not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load upstream subscription")
		return
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	syncErr := s.syncUpstreamRecord(fetchCtx, u)
	cancel()

	if syncErr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":    "sync upstream failed: " + syncErr.Error(),
			"upstream": u,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"upstream": u,
	})
}

func (s *Server) handleDeleteUpstream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid upstream id")
		return
	}

	u, err := s.store.GetUpstreamSub(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "upstream subscription not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load upstream subscription")
		return
	}

	if err := s.store.DeleteUpstreamSub(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete upstream subscription")
		return
	}

	s.recompileScope(ctx, u.NodeID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Extra Outbounds & One-Click Cloudflare WARP ---

func validateExtraOutboundCandidate(ob model.OutboundExtra) error {
	_, err := configgen.Build(configgen.BuildInput{
		ExtraOutbounds: []configgen.ExtraOutboundSpec{
			{
				Tag:      ob.Tag,
				Type:     ob.Type,
				Settings: ob.Settings,
			},
		},
	})
	return err
}

func (s *Server) handleListOutbounds(w http.ResponseWriter, r *http.Request) {
	nodeID := parseNodeIDQuery(r, 1, true)
	list, err := s.store.ListOutboundsExtra(r.Context(), nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list extra outbounds")
		return
	}
	if list == nil {
		list = []model.OutboundExtra{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"outbounds": list,
	})
}

func (s *Server) handleCreateOutbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		NodeID   *int64          `json:"node_id"`
		Name     string          `json:"name"`
		Tag      string          `json:"tag"`
		Type     string          `json:"type"`
		Enabled  *bool           `json:"enabled"`
		Settings json.RawMessage `json:"settings"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	if req.Name == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, "name and type are required")
		return
	}

	nodeID := int64(0)
	if req.NodeID != nil && *req.NodeID > 0 {
		nodeID = *req.NodeID
	}
	if nodeID <= 0 {
		nodeID = parseNodeIDQuery(r, 1, false)
	}
	if _, err := s.store.GetNode(ctx, nodeID); err != nil {
		writeError(w, http.StatusBadRequest, "target node not found")
		return
	}

	tag := sanitizeTagSegment(req.Tag)
	if tag == "" {
		suffix, _ := store.RandomHex(2)
		tag = fmt.Sprintf("%s-%s", req.Type, suffix)
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	settingsRaw := req.Settings
	if len(settingsRaw) == 0 {
		settingsRaw = json.RawMessage("{}")
	}

	ob := &model.OutboundExtra{
		NodeID:   nodeID,
		Name:     req.Name,
		Tag:      tag,
		Type:     req.Type,
		Enabled:  enabled,
		Settings: settingsRaw,
	}

	if err := validateExtraOutboundCandidate(*ob); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.CreateOutboundExtra(ctx, ob); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "an outbound with this tag already exists on this node")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create outbound: "+err.Error())
		return
	}

	_ = s.RecompileAndApplyNode(ctx, ob.NodeID)
	writeJSON(w, http.StatusCreated, ob)
}

func (s *Server) handleRegisterWARP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		NodeID   *int64 `json:"node_id"`
		Name     string `json:"name"`
		Tag      string `json:"tag"`
		IPv6Only bool   `json:"ipv6_only"`
		MTU      int    `json:"mtu"`
		ProxyURL string `json:"proxy_url"`
	}
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, http.ErrBodyNotAllowed) {
		// Allow empty body gracefully for true one-click WARP registration
		if err.Error() != "EOF" {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	nodeID := int64(0)
	if req.NodeID != nil && *req.NodeID > 0 {
		nodeID = *req.NodeID
	}
	if nodeID <= 0 {
		nodeID = parseNodeIDQuery(r, 1, false)
	}
	if _, err := s.store.GetNode(ctx, nodeID); err != nil {
		writeError(w, http.StatusBadRequest, "target node not found")
		return
	}

	acct, err := warpRegisterFn(ctx, strings.TrimSpace(req.ProxyURL))
	if err != nil {
		writeError(w, http.StatusBadGateway, "Cloudflare WARP registration failed: "+err.Error())
		return
	}

	mtu := req.MTU
	if mtu <= 0 {
		mtu = acct.MTU
	}
	if mtu <= 0 {
		mtu = 1280
	}

	settingsMap := map[string]any{
		"account_id":      acct.AccountID,
		"access_token":    acct.AccessToken,
		"private_key":     acct.PrivateKey,
		"public_key":      acct.PublicKey,
		"peer_public_key": acct.PeerPubKey,
		"endpoint_host":   acct.EndpointHost,
		"endpoint_port":   acct.EndpointPort,
		"local_ipv4":      acct.LocalIPv4,
		"local_ipv6":      acct.LocalIPv6,
		"reserved":        acct.Reserved,
		"mtu":             mtu,
		"ipv6_only":       req.IPv6Only,
	}
	settingsBytes, _ := json.Marshal(settingsMap)

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Cloudflare WARP"
	}
	tag := sanitizeTagSegment(req.Tag)
	if tag == "" {
		tag = "warp"
		// Avoid colliding with an existing "warp" tag on the same node
		existing, _ := s.store.ListOutboundsExtra(ctx, nodeID)
		used := make(map[string]bool, len(existing))
		for _, e := range existing {
			used[e.Tag] = true
		}
		if used[tag] {
			suffix, _ := store.RandomHex(2)
			tag = "warp-" + suffix
		}
	}

	ob := &model.OutboundExtra{
		NodeID:   nodeID,
		Name:     name,
		Tag:      tag,
		Type:     "warp",
		Enabled:  true,
		Settings: settingsBytes,
	}
	if err := validateExtraOutboundCandidate(*ob); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.CreateOutboundExtra(ctx, ob); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "an outbound with this tag already exists on this node")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save WARP endpoint: "+err.Error())
		return
	}

	_ = s.RecompileAndApplyNode(ctx, ob.NodeID)
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("registered Cloudflare WARP endpoint %q on node %d (%s)", ob.Tag, ob.NodeID, acct.LocalIPv4))
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"outbound": ob,
	})
}

func (s *Server) handleUpdateOutbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid outbound id")
		return
	}

	ob, err := s.store.GetOutboundExtra(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "outbound not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load outbound")
		return
	}

	var req struct {
		Name     *string         `json:"name"`
		Tag      *string         `json:"tag"`
		Type     *string         `json:"type"`
		Enabled  *bool           `json:"enabled"`
		Settings json.RawMessage `json:"settings"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != nil {
		if n := strings.TrimSpace(*req.Name); n != "" {
			ob.Name = n
		}
	}
	if req.Tag != nil {
		if t := sanitizeTagSegment(*req.Tag); t != "" {
			ob.Tag = t
		}
	}
	if req.Type != nil {
		if tp := strings.ToLower(strings.TrimSpace(*req.Type)); tp != "" {
			ob.Type = tp
		}
	}
	if req.Enabled != nil {
		ob.Enabled = *req.Enabled
	}
	if len(req.Settings) > 0 {
		ob.Settings = req.Settings
	}

	if err := validateExtraOutboundCandidate(*ob); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.UpdateOutboundExtra(ctx, ob); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "an outbound with this tag already exists on this node")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update outbound")
		return
	}

	_ = s.RecompileAndApplyNode(ctx, ob.NodeID)
	writeJSON(w, http.StatusOK, ob)
}

func (s *Server) handleToggleOutbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid outbound id")
		return
	}

	ob, err := s.store.ToggleOutboundExtra(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "outbound not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to toggle outbound")
		return
	}

	_ = s.RecompileAndApplyNode(ctx, ob.NodeID)
	writeJSON(w, http.StatusOK, ob)
}

func (s *Server) handleDeleteOutbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid outbound id")
		return
	}

	ob, err := s.store.GetOutboundExtra(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "outbound not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load outbound")
		return
	}

	if err := s.store.DeleteOutboundExtra(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete outbound")
		return
	}

	_ = s.RecompileAndApplyNode(ctx, ob.NodeID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Live Clash API Proxy Groups (node-scoped via NodeController) ---

func (s *Server) handleListProxies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	groups, err := ctrl.ListProxyGroups(ctx)
	if err != nil || groups == nil {
		// When core is stopped or Clash API hasn't started yet, return empty list cleanly
		groups = []telemetry.ProxyGroupInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"groups": groups,
	})
}

func (s *Server) handleProxyDelay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}

	var req struct {
		Proxy     string `json:"proxy"`
		TestURL   string `json:"test_url"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Proxy = strings.TrimSpace(req.Proxy)
	if req.Proxy == "" {
		writeError(w, http.StatusBadRequest, "proxy name is required")
		return
	}

	delayMS, err := ctrl.TestProxyDelay(ctx, req.Proxy, strings.TrimSpace(req.TestURL), req.TimeoutMS)
	if err != nil {
		writeError(w, http.StatusBadGateway, "proxy delay test failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proxy":    req.Proxy,
		"delay_ms": delayMS,
	})
}

func (s *Server) handleSelectProxy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}

	var req struct {
		Group string `json:"group"`
		Proxy string `json:"proxy"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Group = strings.TrimSpace(req.Group)
	req.Proxy = strings.TrimSpace(req.Proxy)
	if req.Group == "" || req.Proxy == "" {
		writeError(w, http.StatusBadRequest, "group and proxy are required")
		return
	}

	if err := ctrl.SelectProxy(ctx, req.Group, req.Proxy); err != nil {
		writeError(w, http.StatusBadGateway, "select proxy failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Routing Rules & Presets ---

func (s *Server) computeAvailableOutbounds(ctx context.Context, nodeID int64) []string {
	out := []string{"direct"}
	seen := map[string]bool{"direct": true}
	add := func(tag string) {
		tag = strings.TrimSpace(tag)
		if tag != "" && !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}

	if extras, err := s.store.ListOutboundsExtra(ctx, nodeID); err == nil {
		for _, ex := range extras {
			if ex.Enabled {
				add(ex.Tag)
			}
		}
	}
	if upstreams, err := s.store.ListUpstreamSubs(ctx, nodeID); err == nil {
		for _, u := range upstreams {
			if !u.Enabled || u.NodeCount <= 0 {
				continue
			}
			add(u.Tag)
			if u.EnableURLTest {
				add(u.Tag + "-auto")
			}
		}
	}
	return out
}

func (s *Server) handleListRoutingPresets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"presets": configgen.PresetRuleDefinitions(),
	})
}

func (s *Server) handleListRoutingRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	node, err := s.store.GetNode(ctx, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load node")
		return
	}

	rules, err := s.store.ListRouteRules(ctx, nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list routing rules")
		return
	}
	if rules == nil {
		rules = []model.RouteRule{}
	}

	defaultOut := node.DefaultOutbound
	if defaultOut == "" {
		defaultOut = "direct"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"rules":               rules,
		"default_outbound":    defaultOut,
		"available_outbounds": s.computeAvailableOutbounds(ctx, nodeID),
	})
}

type routeRulePayload struct {
	NodeID        *int64    `json:"node_id"`
	Name          *string   `json:"name"`
	PresetKey     *string   `json:"preset_key"`
	Priority      *int      `json:"priority"`
	Enabled       *bool     `json:"enabled"`
	Action        *string   `json:"action"`
	OutboundTag   *string   `json:"outbound_tag"`
	RuleSets      *[]string `json:"rule_sets"`
	Domains       *[]string `json:"domains"`
	DomainSuffix  *[]string `json:"domain_suffix"`
	DomainKeyword *[]string `json:"domain_keyword"`
	IPCIDR        *[]string `json:"ip_cidr"`
	Protocols     *[]string `json:"protocols"`
	ProtocolAlt   *[]string `json:"protocol"`
	Ports         *[]int    `json:"ports"`
	PortAlt       *[]int    `json:"port"`
	AuthUsers     *[]string `json:"auth_users"`
	AuthUserAlt   *[]string `json:"auth_user"`
	Inbounds      *[]string `json:"inbounds"`
	InboundAlt    *[]string `json:"inbound"`
	IPIsPrivate   *bool     `json:"ip_is_private"`
}

func validateRouteAction(action string) bool {
	switch action {
	case "route", "reject", "hijack-dns", "sniff":
		return true
	default:
		return false
	}
}

func applyRouteRulePayload(rr *model.RouteRule, req routeRulePayload) {
	if req.Name != nil {
		rr.Name = strings.TrimSpace(*req.Name)
	}
	if req.PresetKey != nil {
		rr.PresetKey = strings.TrimSpace(*req.PresetKey)
	}
	if req.Priority != nil {
		rr.Priority = *req.Priority
	}
	if req.Enabled != nil {
		rr.Enabled = *req.Enabled
	}
	if req.Action != nil {
		rr.Action = strings.ToLower(strings.TrimSpace(*req.Action))
	}
	if req.OutboundTag != nil {
		rr.OutboundTag = strings.TrimSpace(*req.OutboundTag)
	}
	if req.RuleSets != nil {
		rr.RuleSets = *req.RuleSets
	}
	if req.Domains != nil {
		rr.Domains = *req.Domains
	}
	if req.DomainSuffix != nil {
		rr.DomainSuffix = *req.DomainSuffix
	}
	if req.DomainKeyword != nil {
		rr.DomainKeyword = *req.DomainKeyword
	}
	if req.IPCIDR != nil {
		rr.IPCIDR = *req.IPCIDR
	}
	if req.Protocols != nil {
		rr.Protocols = *req.Protocols
	} else if req.ProtocolAlt != nil {
		rr.Protocols = *req.ProtocolAlt
	}
	if req.Ports != nil {
		rr.Ports = *req.Ports
	} else if req.PortAlt != nil {
		rr.Ports = *req.PortAlt
	}
	if req.AuthUsers != nil {
		rr.AuthUsers = *req.AuthUsers
	} else if req.AuthUserAlt != nil {
		rr.AuthUsers = *req.AuthUserAlt
	}
	if req.Inbounds != nil {
		rr.Inbounds = *req.Inbounds
	} else if req.InboundAlt != nil {
		rr.Inbounds = *req.InboundAlt
	}
	if req.IPIsPrivate != nil {
		rr.IPIsPrivate = *req.IPIsPrivate
	}
}

func (s *Server) handleCreateRoutingRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req routeRulePayload
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	nodeID := int64(0)
	if req.NodeID != nil && *req.NodeID > 0 {
		nodeID = *req.NodeID
	}
	if nodeID <= 0 {
		nodeID = parseNodeIDQuery(r, 1, false)
	}
	if _, err := s.store.GetNode(ctx, nodeID); err != nil {
		writeError(w, http.StatusBadRequest, "target node not found")
		return
	}

	rr := &model.RouteRule{
		NodeID:  nodeID,
		Enabled: true,
		Action:  "route",
	}
	applyRouteRulePayload(rr, req)
	if rr.Name == "" {
		writeError(w, http.StatusBadRequest, "rule name is required")
		return
	}
	if !validateRouteAction(rr.Action) {
		writeError(w, http.StatusBadRequest, "action must be route, reject, hijack-dns, or sniff")
		return
	}
	if rr.Action == "route" && rr.OutboundTag == "" {
		rr.OutboundTag = "direct"
	}

	if err := s.store.CreateRouteRule(ctx, rr); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create routing rule: "+err.Error())
		return
	}

	_ = s.RecompileAndApplyNode(ctx, rr.NodeID)
	writeJSON(w, http.StatusCreated, rr)
}

func (s *Server) handleUpdateRoutingRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid rule id")
		return
	}

	rr, err := s.store.GetRouteRule(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "routing rule not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load routing rule")
		return
	}

	var req routeRulePayload
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	applyRouteRulePayload(rr, req)
	if !validateRouteAction(rr.Action) {
		writeError(w, http.StatusBadRequest, "action must be route, reject, hijack-dns, or sniff")
		return
	}
	if rr.Action == "route" && rr.OutboundTag == "" {
		rr.OutboundTag = "direct"
	}

	if err := s.store.UpdateRouteRule(ctx, rr); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update routing rule")
		return
	}

	_ = s.RecompileAndApplyNode(ctx, rr.NodeID)
	writeJSON(w, http.StatusOK, rr)
}

func (s *Server) handleReorderRoutingRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		NodeID  *int64  `json:"node_id"`
		RuleIDs []int64 `json:"rule_ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	nodeID := int64(0)
	if req.NodeID != nil && *req.NodeID > 0 {
		nodeID = *req.NodeID
	}
	if nodeID <= 0 {
		nodeID = parseNodeIDQuery(r, 1, false)
	}

	if err := s.store.ReorderRouteRules(ctx, nodeID, req.RuleIDs); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reorder routing rules: "+err.Error())
		return
	}

	_ = s.RecompileAndApplyNode(ctx, nodeID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleApplyRoutingPreset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		NodeID      *int64 `json:"node_id"`
		PresetKey   string `json:"preset_key"`
		OutboundTag string `json:"outbound_tag"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	nodeID := int64(0)
	if req.NodeID != nil && *req.NodeID > 0 {
		nodeID = *req.NodeID
	}
	if nodeID <= 0 {
		nodeID = parseNodeIDQuery(r, 1, false)
	}
	if _, err := s.store.GetNode(ctx, nodeID); err != nil {
		writeError(w, http.StatusBadRequest, "target node not found")
		return
	}

	var preset *configgen.RoutingPreset
	for _, p := range configgen.PresetRuleDefinitions() {
		if p.Key == strings.TrimSpace(req.PresetKey) {
			cp := p
			preset = &cp
			break
		}
	}
	if preset == nil {
		writeError(w, http.StatusBadRequest, "unknown preset_key: "+req.PresetKey)
		return
	}

	outboundTag := strings.TrimSpace(req.OutboundTag)
	if preset.NeedsOutbound {
		if outboundTag == "" {
			outboundTag = "direct"
		}
	} else {
		outboundTag = ""
	}

	tpl := preset.RuleTemplate
	existing, err := s.store.FindRouteRuleByPresetKey(ctx, nodeID, preset.Key)
	if err == nil && existing != nil {
		existing.Name = preset.Name
		existing.Enabled = true
		existing.Action = preset.DefaultAction
		existing.OutboundTag = outboundTag
		existing.RuleSets = tpl.RuleSets
		existing.Domains = tpl.Domains
		existing.DomainSuffix = tpl.DomainSuffix
		existing.DomainKeyword = tpl.DomainKeyword
		existing.IPCIDR = tpl.IPCIDR
		existing.Protocols = tpl.Protocols
		existing.Ports = tpl.Ports
		existing.AuthUsers = tpl.AuthUsers
		existing.Inbounds = tpl.Inbounds
		existing.IPIsPrivate = tpl.IPIsPrivate
		if err := s.store.UpdateRouteRule(ctx, existing); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update preset rule")
			return
		}
		_ = s.RecompileAndApplyNode(ctx, nodeID)
		writeJSON(w, http.StatusOK, existing)
		return
	}

	rr := &model.RouteRule{
		NodeID:        nodeID,
		Name:          preset.Name,
		PresetKey:     preset.Key,
		Priority:      tpl.Priority,
		Enabled:       true,
		Action:        preset.DefaultAction,
		OutboundTag:   outboundTag,
		RuleSets:      tpl.RuleSets,
		Domains:       tpl.Domains,
		DomainSuffix:  tpl.DomainSuffix,
		DomainKeyword: tpl.DomainKeyword,
		IPCIDR:        tpl.IPCIDR,
		Protocols:     tpl.Protocols,
		Ports:         tpl.Ports,
		AuthUsers:     tpl.AuthUsers,
		Inbounds:      tpl.Inbounds,
		IPIsPrivate:   tpl.IPIsPrivate,
	}
	if err := s.store.CreateRouteRule(ctx, rr); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create preset rule: "+err.Error())
		return
	}

	_ = s.RecompileAndApplyNode(ctx, nodeID)
	writeJSON(w, http.StatusOK, rr)
}

func (s *Server) handleDeleteRoutingRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid rule id")
		return
	}

	rr, err := s.store.GetRouteRule(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "routing rule not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load routing rule")
		return
	}

	if err := s.store.DeleteRouteRule(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete routing rule")
		return
	}

	_ = s.RecompileAndApplyNode(ctx, rr.NodeID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSetDefaultOutbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		NodeID          *int64 `json:"node_id"`
		DefaultOutbound string `json:"default_outbound"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	nodeID := int64(0)
	if req.NodeID != nil && *req.NodeID > 0 {
		nodeID = *req.NodeID
	}
	if nodeID <= 0 {
		nodeID = parseNodeIDQuery(r, 1, false)
	}
	tag := strings.TrimSpace(req.DefaultOutbound)
	if tag == "" {
		tag = "direct"
	}

	if err := s.store.SetNodeDefaultOutbound(ctx, nodeID, tag); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update default outbound")
		return
	}

	_ = s.RecompileAndApplyNode(ctx, nodeID)
	writeJSON(w, http.StatusOK, map[string]any{
		"default_outbound": tag,
	})
}
