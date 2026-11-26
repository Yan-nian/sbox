package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/configgen"
	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
)

func (s *Server) buildInboundSpec(
	ib model.Inbound,
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
		if c, ok := certByID[*ib.CertificateID]; ok && c.CertPath != "" && c.KeyPath != "" {
			certRef = &configgen.CertRef{
				CertPath: c.CertPath,
				KeyPath:  c.KeyPath,
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

func (s *Server) buildConfig(ctx context.Context, overridePatchOverride *string) ([]byte, string, error) {
	logLevel, _ := s.store.GetSettingDefault(ctx, model.SettingLogLevel, "info")
	overridePatch, _ := s.store.GetSettingDefault(ctx, model.SettingOverridePatch, "")
	if overridePatchOverride != nil {
		overridePatch = *overridePatchOverride
	}

	var clashAddr, clashSecret string
	if s.core != nil {
		clashAddr, clashSecret = s.core.ClashAPIConfig()
	}

	cachePath := ""
	if s.dataDir != "" {
		cachePath = filepath.Join(s.dataDir, "cache.db")
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

	certs, _ := s.store.ListCertificates(ctx)
	certByID := make(map[int64]model.Certificate, len(certs))
	for _, c := range certs {
		certByID[c.ID] = c
	}

	if inbounds, err := s.store.ListInbounds(ctx); err == nil {
		for _, ib := range inbounds {
			if !ib.Enabled {
				continue
			}
			in.Inbounds = append(in.Inbounds, s.buildInboundSpec(ib, enabledMembers, memberByID, certByID))
		}
	}

	if extras, err := s.store.ListOutboundsExtra(ctx); err == nil {
		for _, ex := range extras {
			if !ex.Enabled || strings.TrimSpace(ex.ConfigJSON) == "" || strings.TrimSpace(ex.ConfigJSON) == "{}" {
				continue
			}
			if ex.Kind == "warp_endpoint" {
				in.RawEndpoints = append(in.RawEndpoints, json.RawMessage(ex.ConfigJSON))
			} else {
				in.RawOutbounds = append(in.RawOutbounds, json.RawMessage(ex.ConfigJSON))
			}
		}
	}
	if rules, err := s.store.ListRouteRules(ctx); err == nil {
		for _, rr := range rules {
			if rr.Enabled && strings.TrimSpace(rr.MatchJSON) != "" && strings.TrimSpace(rr.MatchJSON) != "{}" {
				in.RawRouteRules = append(in.RawRouteRules, json.RawMessage(rr.MatchJSON))
			}
		}
	}

	cfgBytes, err := configgen.Build(in)
	if err != nil {
		return nil, overridePatch, err
	}
	return cfgBytes, overridePatch, nil
}

// RecompileAndApply compiles the active sing-box configuration from SQLite state
// and reloads the running sing-box core (or persists config.json when core is stopped).
func (s *Server) RecompileAndApply(ctx context.Context) error {
	cfgBytes, _, err := s.buildConfig(ctx, nil)
	if err != nil {
		return err
	}
	if s.core == nil {
		return nil
	}
	if s.core.Status().Running {
		if err := s.core.Reload(ctx, cfgBytes); err != nil {
			if s.telemetry != nil {
				s.telemetry.PublishLog("error", "panel", "core reload failed after config update: "+err.Error())
			}
			return err
		}
		if s.telemetry != nil {
			s.telemetry.PublishLog("info", "panel", "recompiled sing-box config and reloaded core")
		}
		return nil
	}
	return s.core.EnsureConfig(cfgBytes)
}

func (s *Server) currentCoreStatus() coremgr.Status {
	if s.core == nil {
		return coremgr.Status{}
	}
	return s.core.Status()
}

func (s *Server) currentMetrics() telemetry.Snapshot {
	if s.telemetry == nil {
		return telemetry.Snapshot{UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	return s.telemetry.Snapshot()
}

// --- Dashboard ---

func (s *Server) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hostname, _ := os.Hostname()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	pubV4, _ := s.store.GetSettingDefault(ctx, model.SettingPublicIPv4, "")
	pubV6, _ := s.store.GetSettingDefault(ctx, model.SettingPublicIPv6, "")

	writeJSON(w, http.StatusOK, map[string]any{
		"core":    s.currentCoreStatus(),
		"metrics": s.currentMetrics(),
		"system": map[string]any{
			"os":               runtime.GOOS,
			"arch":             runtime.GOARCH,
			"hostname":         hostname,
			"go_version":       runtime.Version(),
			"panel_version":    s.panelVersion,
			"panel_uptime_sec": int64(time.Since(s.startedAt).Seconds()),
			"panel_mem_bytes":  mem.Alloc,
			"public_ipv4":      pubV4,
			"public_ipv6":      pubV6,
		},
	})
}

func (s *Server) handleDashboardStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func() bool {
		payload, err := json.Marshal(map[string]any{
			"core":    s.currentCoreStatus(),
			"metrics": s.currentMetrics(),
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

// --- Core Lifecycle & Upgrade ---

func (s *Server) handleCoreStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.currentCoreStatus())
}

func (s *Server) handleCoreStart(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		writeError(w, http.StatusServiceUnavailable, "core manager not initialized")
		return
	}
	cfg, _, err := s.buildConfig(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "build config failed: "+err.Error())
		return
	}
	if err := s.core.EnsureConfig(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "write config failed: "+err.Error())
		return
	}
	if err := s.core.Start(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.core.Status())
}

func (s *Server) handleCoreStop(w http.ResponseWriter, _ *http.Request) {
	if s.core == nil {
		writeError(w, http.StatusServiceUnavailable, "core manager not initialized")
		return
	}
	if err := s.core.Stop(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.core.Status())
}

func (s *Server) handleCoreRestart(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		writeError(w, http.StatusServiceUnavailable, "core manager not initialized")
		return
	}
	cfg, _, err := s.buildConfig(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "build config failed: "+err.Error())
		return
	}
	if err := s.core.EnsureConfig(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "write config failed: "+err.Error())
		return
	}
	if err := s.core.Restart(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.core.Status())
}

func (s *Server) handleCoreReload(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		writeError(w, http.StatusServiceUnavailable, "core manager not initialized")
		return
	}
	cfg, _, err := s.buildConfig(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "build config failed: "+err.Error())
		return
	}
	if err := s.core.Reload(r.Context(), cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.core.Status())
}

func (s *Server) handleCoreRollback(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		writeError(w, http.StatusServiceUnavailable, "core manager not initialized")
		return
	}
	if err := s.core.Rollback(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.core.Status())
}

func (s *Server) handleCoreReleases(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		writeError(w, http.StatusServiceUnavailable, "core manager not initialized")
		return
	}
	includePre := strings.EqualFold(r.URL.Query().Get("prerelease"), "true")
	proxyURL, _ := s.store.GetSettingDefault(r.Context(), model.SettingGithubProxy, "")

	releases, err := s.core.ListReleases(r.Context(), includePre, proxyURL)
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
	if s.core == nil {
		writeError(w, http.StatusServiceUnavailable, "core manager not initialized")
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
		req.ProxyURL, _ = s.store.GetSettingDefault(r.Context(), model.SettingGithubProxy, "")
	}

	if cfg, _, err := s.buildConfig(r.Context(), nil); err == nil {
		_ = s.core.EnsureConfig(cfg)
	}

	res := s.core.Upgrade(r.Context(), req)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCoreUpload(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		writeError(w, http.StatusServiceUnavailable, "core manager not initialized")
		return
	}
	// Limit upload size to 256 MiB
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
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

	if cfg, _, err := s.buildConfig(r.Context(), nil); err == nil {
		_ = s.core.EnsureConfig(cfg)
	}

	res := s.core.UploadBinary(r.Context(), file, header.Filename)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCoreConfig(w http.ResponseWriter, r *http.Request) {
	cfgBytes, overridePatch, err := s.buildConfig(r.Context(), nil)
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

	cfgBytes, _, err := s.buildConfig(ctx, &patch)
	if err != nil {
		writeError(w, http.StatusBadRequest, "merge override_patch failed: "+err.Error())
		return
	}

	// Pre-check candidate config with sing-box binary if installed
	if s.core != nil {
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

	if err := s.store.SetSetting(ctx, model.SettingOverridePatch, patch); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save override_patch")
		return
	}

	if s.core != nil {
		st := s.core.Status()
		if st.Running {
			if err := s.core.Reload(ctx, cfgBytes); err != nil {
				writeError(w, http.StatusInternalServerError, "saved override but failed to reload core: "+err.Error())
				return
			}
		} else {
			_ = s.core.EnsureConfig(cfgBytes)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"config_json":    string(cfgBytes),
		"override_patch": patch,
	})
}

// --- Logs ---

func (s *Server) handleLogsRecent(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 2000 {
				n = 2000
			}
			limit = n
		}
	}
	var logs []telemetry.LogEntry
	if s.telemetry != nil {
		logs = s.telemetry.RecentLogs(limit)
	}
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
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	if s.telemetry == nil {
		<-r.Context().Done()
		return
	}

	ch, unsub := s.telemetry.SubscribeLogs()
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

// --- Connections ---

func (s *Server) handleConnectionsList(w http.ResponseWriter, r *http.Request) {
	var conns []telemetry.ConnectionItem
	if s.telemetry != nil {
		list, err := s.telemetry.Connections(r.Context())
		if err != nil {
			// If core is stopped or unreachable, return empty list cleanly rather than breaking the page.
			conns = []telemetry.ConnectionItem{}
		} else {
			conns = list
		}
	}
	if conns == nil {
		conns = []telemetry.ConnectionItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connections": conns,
	})
}

func (s *Server) handleConnectionClose(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		writeError(w, http.StatusBadRequest, "connection id is required")
		return
	}
	if s.telemetry == nil {
		writeError(w, http.StatusServiceUnavailable, "telemetry hub unavailable")
		return
	}
	if err := s.telemetry.CloseConnection(r.Context(), id); err != nil {
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
		// Re-issue auth cookie scoped to the new base path so the browser stays logged in after redirect.
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

	updated, err := s.store.GetPanelSettings(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, current)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
