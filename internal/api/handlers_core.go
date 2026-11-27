package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/cluster"
	"github.com/sbox-panel/sbox-panel/internal/configgen"
	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
	"github.com/sbox-panel/sbox-panel/internal/syncer"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
)

func joinNodePath(nodeOS string, parts ...string) string {
	if strings.EqualFold(nodeOS, "windows") {
		return filepath.Join(parts...)
	}
	// Ensure forward slashes for Linux/macOS agents even when Master runs on Windows.
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			cleaned = append(cleaned, strings.ReplaceAll(p, `\`, "/"))
		}
	}
	return path.Join(cleaned...)
}

func (s *Server) buildInboundSpec(
	ib model.Inbound,
	nodeOS string,
	nodeDataDir string,
	enabledMembers []model.Member,
	memberByID map[int64]model.Member,
	certByID map[int64]model.Certificate,
) configgen.InboundSpec {
	var bound []configgen.MemberCredential
	if ib.MemberMode == "selected" {
		for _, mid := range ib.MemberIDs {
			if m, ok := memberByID[mid]; ok && m.Enabled {
				bound = append(bound, toMemberCredential(m))
			}
		}
	} else {
		for _, m := range enabledMembers {
			bound = append(bound, toMemberCredential(m))
		}
	}

	var certRef *configgen.CertRef
	if ib.CertificateID != nil && *ib.CertificateID > 0 {
		if c, ok := certByID[*ib.CertificateID]; ok {
			certPath := c.CertPath
			keyPath := c.KeyPath
			if (certPath == "" || keyPath == "") && nodeDataDir != "" {
				certDir := joinNodePath(nodeOS, nodeDataDir, "certs", fmt.Sprintf("cert-%d", c.ID))
				certPath = joinNodePath(nodeOS, certDir, "fullchain.pem")
				keyPath = joinNodePath(nodeOS, certDir, "privkey.pem")
			}
			if certPath != "" && keyPath != "" {
				certRef = &configgen.CertRef{
					CertPath: certPath,
					KeyPath:  keyPath,
				}
			}
		}
	}

	return configgen.InboundSpec{
		Tag:        ib.Tag,
		Protocol:   ib.Protocol,
		Listen:     ib.Listen,
		ListenPort: ib.ListenPort,
		Settings:   ib.Settings,
		Cert:       certRef,
		Members:    bound,
	}
}

func toMemberCredential(m model.Member) configgen.MemberCredential {
	return configgen.MemberCredential{
		Name:        m.Name,
		UUID:        m.UUID,
		Password:    m.Password,
		SS2022Key16: m.SS2022Key16,
		SS2022Key32: m.SS2022Key32,
	}
}

func (s *Server) buildConfigForNode(
	ctx context.Context,
	nodeID int64,
	overridePatchOverride *string,
) ([]byte, string, []cluster.CertFileBundle, error) {
	if nodeID <= 0 {
		nodeID = 1
	}

	node, err := s.store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, "", nil, err
	}

	logLevel := node.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}
	overridePatch := node.OverridePatch
	if node.IsLocal {
		// Backward-compatibility if settings table was edited directly
		if ll, ok, _ := s.store.GetSetting(ctx, model.SettingLogLevel); ok && ll != "" {
			logLevel = ll
		}
		if op, ok, _ := s.store.GetSetting(ctx, model.SettingOverridePatch); ok && overridePatch == "" && op != "" {
			overridePatch = op
		}
	}
	if overridePatchOverride != nil {
		overridePatch = *overridePatchOverride
	}

	nodeDataDir := node.DataDir
	nodeOS := node.OS
	var clashAddr, clashSecret string

	if node.IsLocal {
		if nodeDataDir == "" {
			nodeDataDir = s.dataDir
		}
		if nodeOS == "" {
			nodeOS = runtime.GOOS
		}
		if s.core != nil {
			clashAddr, clashSecret = s.core.ClashAPIConfig()
		}
	} else {
		if s.hub != nil {
			if ctrl, ok := s.hub.Get(nodeID); ok {
				if d := ctrl.DataDir(); d != "" {
					nodeDataDir = d
				}
			}
			if rt, ok := s.hub.RuntimeState(nodeID); ok {
				if rt.DataDir != "" {
					nodeDataDir = rt.DataDir
				}
				if rt.OS != "" {
					nodeOS = rt.OS
				}
				if rt.Core.ClashAPIAddr != "" {
					clashAddr = rt.Core.ClashAPIAddr
				}
			}
		}
		if nodeOS == "" {
			nodeOS = "linux"
		}
	}

	cachePath := ""
	if nodeDataDir != "" {
		if node.IsLocal {
			cachePath = filepath.Join(nodeDataDir, "cache.db")
		} else {
			cachePath = joinNodePath(nodeOS, nodeDataDir, "cache", "cache.db")
		}
	}

	in := configgen.BuildInput{
		LogLevel:      logLevel,
		CacheFilePath: cachePath,
		ClashAPI: configgen.ClashAPIConfig{
			ListenAddr: clashAddr,
			Secret:     clashSecret,
		},
		OverridePatch: overridePatch,
	}

	members, _ := s.store.ListMembers(ctx)
	enabledMembers := make([]model.Member, 0, len(members))
	memberByID := make(map[int64]model.Member, len(members))
	for _, m := range members {
		memberByID[m.ID] = m
		if m.Enabled {
			enabledMembers = append(enabledMembers, m)
		}
	}

	certs, _ := s.store.ListCertificates(ctx, nodeID)
	certByID := make(map[int64]model.Certificate, len(certs))
	bundles := make([]cluster.CertFileBundle, 0)
	for _, c := range certs {
		certByID[c.ID] = c
		if c.Type != "external" && len(c.CertPEM) > 0 && len(c.KeyPEM) > 0 {
			bundles = append(bundles, cluster.CertFileBundle{
				CertID:  c.ID,
				CertPEM: []byte(c.CertPEM),
				KeyPEM:  []byte(c.KeyPEM),
			})
		}
	}

	if inbounds, err := s.store.ListInbounds(ctx, nodeID); err == nil {
		for _, ib := range inbounds {
			if !ib.Enabled {
				continue
			}
			in.Inbounds = append(in.Inbounds, s.buildInboundSpec(ib, nodeOS, nodeDataDir, enabledMembers, memberByID, certByID))
		}
	}

	if upstreams, err := s.store.ListUpstreamSubs(ctx, nodeID); err == nil {
		for _, u := range upstreams {
			if !u.Enabled {
				continue
			}
			var parsed []syncer.ParsedOutbound
			if err := json.Unmarshal([]byte(u.CachedNodesJSON), &parsed); err != nil || len(parsed) == 0 {
				continue
			}
			rawNodes := make([]json.RawMessage, 0, len(parsed))
			nodeTags := make([]string, 0, len(parsed))
			for _, p := range parsed {
				if len(p.Raw) == 0 || strings.TrimSpace(p.Tag) == "" {
					continue
				}
				rawNodes = append(rawNodes, p.Raw)
				nodeTags = append(nodeTags, p.Tag)
			}
			if len(nodeTags) == 0 {
				continue
			}
			in.UpstreamGroups = append(in.UpstreamGroups, configgen.UpstreamGroupSpec{
				GroupTag:      u.Tag,
				URLTestTag:    u.Tag + "-auto",
				EnableURLTest: u.EnableURLTest,
				TestURL:       u.TestURL,
				Nodes:         rawNodes,
				NodeTags:      nodeTags,
			})
		}
	}

	if extras, err := s.store.ListOutboundsExtra(ctx, nodeID); err == nil {
		for _, ex := range extras {
			if !ex.Enabled || strings.TrimSpace(ex.Tag) == "" {
				continue
			}
			in.ExtraOutbounds = append(in.ExtraOutbounds, configgen.ExtraOutboundSpec{
				Tag:      ex.Tag,
				Type:     ex.Type,
				Settings: ex.Settings,
			})
		}
	}

	if rules, err := s.store.ListRouteRules(ctx, nodeID); err == nil {
		for _, rr := range rules {
			if !rr.Enabled {
				continue
			}
			in.RouteRules = append(in.RouteRules, configgen.RouteRuleSpec{
				Priority:      rr.Priority,
				Action:        rr.Action,
				OutboundTag:   rr.OutboundTag,
				RuleSets:      rr.RuleSets,
				Domains:       rr.Domains,
				DomainSuffix:  rr.DomainSuffix,
				DomainKeyword: rr.DomainKeyword,
				IPCIDR:        rr.IPCIDR,
				Protocols:     rr.Protocols,
				Ports:         rr.Ports,
				AuthUsers:     rr.AuthUsers,
				Inbounds:      rr.Inbounds,
				IPIsPrivate:   rr.IPIsPrivate,
			})
		}
	}

	in.DefaultOutbound = node.DefaultOutbound

	cfgBytes, err := configgen.Build(in)
	if err != nil {
		return nil, overridePatch, nil, err
	}
	return cfgBytes, overridePatch, bundles, nil
}

// RecompileAndApply compiles and applies configuration for the local master node (id=1).
func (s *Server) RecompileAndApply(ctx context.Context) error {
	return s.RecompileAndApplyNode(ctx, 1)
}

// RecompileAndApplyNode compiles configuration for nodeID and pushes/reloads it if the node is online.
func (s *Server) RecompileAndApplyNode(ctx context.Context, nodeID int64) error {
	if nodeID <= 0 {
		nodeID = 1
	}
	cfgBytes, _, bundles, err := s.buildConfigForNode(ctx, nodeID, nil)
	if err != nil {
		return err
	}
	if s.hub == nil {
		return nil
	}
	ctrl, ok := s.hub.Get(nodeID)
	if !ok || !ctrl.IsOnline() {
		return nil
	}
	if err := ctrl.ApplyConfig(ctx, cfgBytes, bundles); err != nil {
		if s.telemetry != nil {
			s.telemetry.PublishLog("error", "panel", fmt.Sprintf("apply config to node %d failed: %v", nodeID, err))
		}
		return err
	}
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("recompiled and applied sing-box config for node %d", nodeID))
	}
	return nil
}

// RecompileAllOnlineNodes propagates global changes (such as member edits) to every online node concurrently.
func (s *Server) RecompileAllOnlineNodes(ctx context.Context) {
	if s.hub == nil {
		return
	}
	ids := s.hub.OnlineNodeIDs()
	var wg sync.WaitGroup
	for _, nid := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			_ = s.RecompileAndApplyNode(ctx, id)
		}(nid)
	}
	wg.Wait()
}

func (s *Server) nodeSnapshotAndCore(ctx context.Context, nodeID int64, node *model.Node) (coremgr.Status, telemetry.Snapshot, bool) {
	if s.hub != nil {
		if rt, ok := s.hub.RuntimeState(nodeID); ok && rt.Online {
			return rt.Core, rt.Metrics, true
		}
		if ctrl, ok := s.hub.Get(nodeID); ok && ctrl.IsOnline() {
			cs, _ := ctrl.CoreStatus(ctx)
			return cs, ctrl.Snapshot(), true
		}
	}
	version := ""
	if node != nil {
		version = node.CoreVersion
	}
	return coremgr.Status{
			Running: false,
			Version: version,
		}, telemetry.Snapshot{
			CoreConnected: false,
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		}, false
}

// --- Dashboard ---

func (s *Server) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
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

	counts, _ := s.store.InboundCountsByNode(ctx)
	nodeView := s.buildNodeView(*node, counts[node.ID])
	coreStatus, metrics, _ := s.nodeSnapshotAndCore(ctx, nodeID, node)
	clusterSummary := s.computeClusterSummary(ctx)

	var sysInfo map[string]any
	if node.IsLocal {
		hostname, _ := os.Hostname()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		pubV4, _ := s.store.GetSettingDefault(ctx, model.SettingPublicIPv4, node.PublicIPv4)
		pubV6, _ := s.store.GetSettingDefault(ctx, model.SettingPublicIPv6, node.PublicIPv6)
		sysInfo = map[string]any{
			"os":               runtime.GOOS,
			"arch":             runtime.GOARCH,
			"hostname":         hostname,
			"go_version":       runtime.Version(),
			"panel_version":    s.panelVersion,
			"panel_uptime_sec": int64(time.Since(s.startedAt).Seconds()),
			"panel_mem_bytes":  mem.Alloc,
			"public_ipv4":      pubV4,
			"public_ipv6":      pubV6,
		}
	} else {
		sysInfo = map[string]any{
			"os":               nodeView.OS,
			"arch":             nodeView.Arch,
			"hostname":         nodeView.Hostname,
			"go_version":       "",
			"panel_version":    nodeView.AgentVersion,
			"panel_uptime_sec": nodeView.UptimeSec,
			"panel_mem_bytes":  nodeView.MemBytes,
			"public_ipv4":      nodeView.PublicIPv4,
			"public_ipv6":      nodeView.PublicIPv6,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"node":    nodeView,
		"core":    coreStatus,
		"metrics": metrics,
		"system":  sysInfo,
		"cluster": clusterSummary,
	})
}

func (s *Server) handleDashboardStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	nodeID := parseNodeIDQuery(r, 1, false)
	node, err := s.store.GetNode(r.Context(), nodeID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load node")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func() bool {
		coreStatus, metrics, online := s.nodeSnapshotAndCore(r.Context(), nodeID, node)
		payload, err := json.Marshal(map[string]any{
			"node_id": nodeID,
			"online":  online,
			"core":    coreStatus,
			"metrics": metrics,
			"cluster": s.computeClusterSummary(r.Context()),
		})
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !send() {
		return
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

// --- Core Lifecycle & Upgrade (node-scoped) ---

func (s *Server) handleCoreStatus(w http.ResponseWriter, r *http.Request) {
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, r.Context(), nodeID)
	if !ok {
		return
	}
	status, err := ctrl.CoreStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCoreStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	cfg, _, bundles, err := s.buildConfigForNode(ctx, nodeID, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "build config failed: "+err.Error())
		return
	}
	if err := ctrl.ApplyConfig(ctx, cfg, bundles); err != nil {
		writeError(w, http.StatusInternalServerError, "write config failed: "+err.Error())
		return
	}
	if err := ctrl.StartCore(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, _ := ctrl.CoreStatus(ctx)
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCoreStop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	if err := ctrl.StopCore(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, _ := ctrl.CoreStatus(ctx)
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCoreRestart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	cfg, _, bundles, err := s.buildConfigForNode(ctx, nodeID, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "build config failed: "+err.Error())
		return
	}
	if err := ctrl.ApplyConfig(ctx, cfg, bundles); err != nil {
		writeError(w, http.StatusInternalServerError, "write config failed: "+err.Error())
		return
	}
	if err := ctrl.RestartCore(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, _ := ctrl.CoreStatus(ctx)
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCoreReload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	cfg, _, bundles, err := s.buildConfigForNode(ctx, nodeID, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "build config failed: "+err.Error())
		return
	}
	if err := ctrl.ApplyConfig(ctx, cfg, bundles); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, _ := ctrl.CoreStatus(ctx)
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCoreRollback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	if err := ctrl.RollbackCore(ctx); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, _ := ctrl.CoreStatus(ctx)
	if status.Version != "" {
		_ = s.store.UpdateNodeRuntimeInfo(ctx, nodeID, store.NodeRuntimeUpdate{CoreVersion: status.Version})
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCoreReleases(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, node, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	includePre := strings.EqualFold(r.URL.Query().Get("prerelease"), "true")
	proxyURL := node.GithubProxy
	if proxyURL == "" && node.IsLocal {
		proxyURL, _ = s.store.GetSettingDefault(ctx, model.SettingGithubProxy, "")
	}

	releases, err := ctrl.ListReleases(ctx, includePre, proxyURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch sing-box releases: "+err.Error())
		return
	}
	if releases == nil {
		releases = []coremgr.ReleaseInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"releases": releases,
	})
}

func (s *Server) handleCoreUpgrade(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, node, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	var req coremgr.UpgradeOptions
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.TagName) == "" && strings.TrimSpace(req.DownloadURL) == "" {
		writeError(w, http.StatusBadRequest, "tag_name or download_url is required")
		return
	}
	if strings.TrimSpace(req.ProxyURL) == "" {
		req.ProxyURL = node.GithubProxy
		if req.ProxyURL == "" && node.IsLocal {
			req.ProxyURL, _ = s.store.GetSettingDefault(ctx, model.SettingGithubProxy, "")
		}
	}

	if cfg, _, bundles, err := s.buildConfigForNode(ctx, nodeID, nil); err == nil {
		_ = ctrl.ApplyConfig(ctx, cfg, bundles)
	}

	res, err := ctrl.UpgradeCore(ctx, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if res.Success && res.ToVersion != "" {
		_ = s.store.UpdateNodeRuntimeInfo(ctx, nodeID, store.NodeRuntimeUpdate{CoreVersion: res.ToVersion})
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCoreUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	// Limit upload size to 48 MiB for remote WS safety, 256 MiB for local
	maxSize := int64(256 << 20)
	if !ctrl.IsLocal() {
		maxSize = 48 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart upload: "+err.Error())
		return
	}
	file, header, err := r.FormFile("binary")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing multipart form field 'binary'")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read uploaded binary: "+err.Error())
		return
	}

	if cfg, _, bundles, err := s.buildConfigForNode(ctx, nodeID, nil); err == nil {
		_ = ctrl.ApplyConfig(ctx, cfg, bundles)
	}

	res, err := ctrl.UploadBinary(ctx, header.Filename, data)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if res.Success && res.ToVersion != "" {
		_ = s.store.UpdateNodeRuntimeInfo(ctx, nodeID, store.NodeRuntimeUpdate{CoreVersion: res.ToVersion})
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCoreConfig(w http.ResponseWriter, r *http.Request) {
	nodeID := parseNodeIDQuery(r, 1, false)
	cfgBytes, overridePatch, _, err := s.buildConfigForNode(r.Context(), nodeID, nil)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "build config failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config_json":    string(cfgBytes),
		"override_patch": overridePatch,
	})
}

func (s *Server) handleCoreConfigOverride(w http.ResponseWriter, r *http.Request) {
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

	var req struct {
		OverridePatch string `json:"override_patch"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	patch := strings.TrimSpace(req.OverridePatch)
	if patch != "" {
		var js any
		if err := json.Unmarshal([]byte(patch), &js); err != nil {
			writeError(w, http.StatusBadRequest, "override_patch must be valid JSON: "+err.Error())
			return
		}
	}

	cfgBytes, _, bundles, err := s.buildConfigForNode(ctx, nodeID, &patch)
	if err != nil {
		writeError(w, http.StatusBadRequest, "merge override_patch failed: "+err.Error())
		return
	}

	// Pre-check candidate config with local sing-box binary if installed for local node
	if node.IsLocal && s.core != nil {
		st := s.core.Status()
		if st.BinaryExists && st.BinaryPath != "" {
			tmpFile, err := os.CreateTemp("", "sbox-override-check-*.json")
			if err == nil {
				tmpPath := tmpFile.Name()
				_, writeErr := tmpFile.Write(cfgBytes)
				_ = tmpFile.Close()
				defer os.Remove(tmpPath)
				if writeErr == nil {
					if out, checkErr := s.core.CheckConfig(st.BinaryPath, tmpPath); checkErr != nil {
						msg := "sing-box config check failed: " + checkErr.Error()
						if strings.TrimSpace(out) != "" {
							msg = "sing-box config check failed: " + strings.TrimSpace(out)
						}
						writeError(w, http.StatusBadRequest, msg)
						return
					}
				}
			}
		}
	}

	node.OverridePatch = patch
	if err := s.store.UpdateNode(ctx, node); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save node override_patch")
		return
	}
	if node.IsLocal {
		_ = s.store.SetSetting(ctx, model.SettingOverridePatch, patch)
	}

	if s.hub != nil {
		if ctrl, ok := s.hub.Get(nodeID); ok && ctrl.IsOnline() {
			if err := ctrl.ApplyConfig(ctx, cfgBytes, bundles); err != nil {
				writeError(w, http.StatusInternalServerError, "saved override but failed to apply config: "+err.Error())
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"config_json":    string(cfgBytes),
		"override_patch": patch,
	})
}

// --- Logs (node-scoped) ---

func (s *Server) handleLogsRecent(w http.ResponseWriter, r *http.Request) {
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, r.Context(), nodeID)
	if !ok {
		return
	}
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 2000 {
				n = 2000
			}
			limit = n
		}
	}
	logs := ctrl.RecentLogs(limit)
	if logs == nil {
		logs = []telemetry.LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs": logs,
	})
}

func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, r.Context(), nodeID)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ch, unsub := ctrl.SubscribeLogs()
	defer unsub()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case entry, open := <-ch:
			if !open {
				return
			}
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: log\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// --- Connections (node-scoped) ---

func (s *Server) handleConnectionsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	conns, err := ctrl.Connections(ctx)
	if err != nil || conns == nil {
		conns = []telemetry.ConnectionItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connections": conns,
	})
}

func (s *Server) handleConnectionClose(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := parseNodeIDQuery(r, 1, false)
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		writeError(w, http.StatusBadRequest, "connection id is required")
		return
	}
	if err := ctrl.CloseConnection(ctx, id); err != nil {
		writeError(w, http.StatusBadGateway, "failed to close connection: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Settings ---

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	ps, err := s.store.GetPanelSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	current, err := s.store.GetPanelSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}

	var req struct {
		InstanceID  *string `json:"instance_id"`
		WebBasePath *string `json:"web_base_path"`
		ListenPort  *int    `json:"listen_port"`
		PublicIPv4  *string `json:"public_ipv4"`
		PublicIPv6  *string `json:"public_ipv6"`
		GithubProxy *string `json:"github_proxy"`
		LogLevel    *string `json:"log_level"`
		PanelCertID *int64  `json:"panel_cert_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.InstanceID != nil {
		id := strings.TrimSpace(*req.InstanceID)
		if id == "" {
			writeError(w, http.StatusBadRequest, "instance_id cannot be empty")
			return
		}
		current.InstanceID = id
	}

	basePathChanged := false
	oldBasePath := s.BasePath()
	if req.WebBasePath != nil {
		norm := store.NormalizeBasePath(*req.WebBasePath)
		if !isValidBasePath(norm) {
			writeError(w, http.StatusBadRequest, "invalid web_base_path: must start with '/', contain only [a-zA-Z0-9/_-], and not be '/' or '/api'")
			return
		}
		if norm != current.WebBasePath {
			basePathChanged = true
		}
		current.WebBasePath = norm
	}

	if req.ListenPort != nil {
		if *req.ListenPort < 1 || *req.ListenPort > 65535 {
			writeError(w, http.StatusBadRequest, "listen_port must be between 1 and 65535")
			return
		}
		current.ListenPort = *req.ListenPort
	}

	if req.PublicIPv4 != nil {
		current.PublicIPv4 = strings.TrimSpace(*req.PublicIPv4)
	}
	if req.PublicIPv6 != nil {
		current.PublicIPv6 = strings.TrimSpace(*req.PublicIPv6)
	}
	if req.GithubProxy != nil {
		current.GithubProxy = strings.TrimSpace(*req.GithubProxy)
	}
	if req.LogLevel != nil {
		ll := strings.ToLower(strings.TrimSpace(*req.LogLevel))
		switch ll {
		case "trace", "debug", "info", "warn", "error", "fatal":
			current.LogLevel = ll
		default:
			writeError(w, http.StatusBadRequest, "invalid log_level")
			return
		}
	}

	panelCertChanged := false
	if req.PanelCertID != nil {
		cid := *req.PanelCertID
		if cid < 0 {
			writeError(w, http.StatusBadRequest, "panel_cert_id must be >= 0")
			return
		}
		if cid > 0 {
			if _, err := s.store.GetCertificate(ctx, cid); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					writeError(w, http.StatusBadRequest, "referenced panel certificate not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to verify panel certificate")
				return
			}
		}
		if cid != current.PanelCertID {
			panelCertChanged = true
		}
		current.PanelCertID = cid
	}

	if err := s.store.UpdatePanelSettings(ctx, current); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist settings")
		return
	}

	if basePathChanged {
		s.SetBasePath(current.WebBasePath)
		if token := auth.ExtractToken(r); token != "" {
			auth.ClearAuthCookie(w, oldBasePath, isRequestSecure(r))
			auth.SetAuthCookie(w, token, current.WebBasePath, isRequestSecure(r), s.tokens.TTL())
		}
		if s.telemetry != nil {
			s.telemetry.PublishLog("info", "panel", "web_base_path updated dynamically to "+current.WebBasePath)
		}
	}

	if panelCertChanged && s.onPanelCertChange != nil {
		s.onPanelCertChange()
	}
	if req.InstanceID != nil || req.PublicIPv4 != nil || req.PublicIPv6 != nil {
		s.TriggerAutoSync()
	}

	updated, err := s.store.GetPanelSettings(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, current)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
