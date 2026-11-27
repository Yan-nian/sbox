package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/backup"
	"github.com/sbox-panel/sbox-panel/internal/model"
)

func sanitizeBackupFileSegment(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
		if b.Len() >= 24 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "panel"
	}
	return out
}

func toRemoteBackupConfigView(cfg model.RemoteBackupConfig, lastAt, lastErr string) model.RemoteBackupConfigView {
	return model.RemoteBackupConfigView{
		Enabled:       cfg.Enabled,
		Type:          cfg.Type,
		Repo:          cfg.Repo,
		Branch:        cfg.Branch,
		PathPrefix:    cfg.PathPrefix,
		GistID:        cfg.GistID,
		KeepCount:     cfg.KeepCount,
		IntervalHours: cfg.IntervalHours,
		HasToken:      strings.TrimSpace(cfg.Token) != "",
		HasPassphrase: strings.TrimSpace(cfg.Passphrase) != "",
		ProxyURL:      cfg.ProxyURL,
		APIBaseURL:    cfg.APIBaseURL,
		LastBackupAt:  lastAt,
		LastError:     lastErr,
	}
}

func (s *Server) toGitHubBackupConfig(ctx context.Context, cfg model.RemoteBackupConfig) backup.RemoteGitHubConfig {
	proxyURL := strings.TrimSpace(cfg.ProxyURL)
	if proxyURL == "" {
		proxyURL, _ = s.store.GetSettingDefault(ctx, model.SettingGithubProxy, "")
	}
	return backup.RemoteGitHubConfig{
		Type:       cfg.Type,
		Token:      cfg.Token,
		Repo:       cfg.Repo,
		Branch:     cfg.Branch,
		PathPrefix: cfg.PathPrefix,
		GistID:     cfg.GistID,
		KeepCount:  cfg.KeepCount,
		Passphrase: cfg.Passphrase,
		ProxyURL:   strings.TrimSpace(proxyURL),
		APIBaseURL: strings.TrimSpace(cfg.APIBaseURL),
	}
}

func (s *Server) performSafeRestore(ctx context.Context, archive []byte, passphrase string) (*backup.InspectResult, string, error) {
	var inspectRes *backup.InspectResult
	err := s.store.WithClosedDB(ctx, func(dbPath string) error {
		res, restoreErr := backup.RestoreArchive(ctx, archive, passphrase, dbPath, s.certsRootDir())
		if restoreErr != nil {
			return restoreErr
		}
		inspectRes = res
		return nil
	})
	if err != nil {
		return nil, s.BasePath(), err
	}

	// Refresh active runtime state from the restored database.
	if bp, ok, err := s.store.GetSetting(ctx, model.SettingWebBasePath); err == nil && ok && bp != "" {
		s.SetBasePath(bp)
	}
	if decoy, ok, err := s.store.GetSetting(ctx, model.SettingDecoyHTML); err == nil && ok {
		s.SetDecoyHTML(decoy)
	}
	if secret, ok, err := s.store.GetSetting(ctx, model.SettingJWTSecret); err == nil && ok && strings.TrimSpace(secret) != "" {
		s.tokens.SetSecret(strings.TrimSpace(secret))
	}
	if s.onPanelCertChange != nil {
		s.onPanelCertChange()
	}

	s.RecompileAllOnlineNodes(ctx)
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "backup", fmt.Sprintf("restored panel backup (encrypted=%v, files=%d, certs=%d)", inspectRes.Encrypted, inspectRes.FileCount, inspectRes.CertFiles))
	}
	return inspectRes, s.BasePath(), nil
}

// handleBackupExport serves POST /api/v1/backup/export ({ passphrase?: string }).
func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Passphrase string `json:"passphrase"`
	}
	if r.Body != nil {
		if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	_ = s.store.CheckpointWAL(ctx)
	data, err := backup.CreateArchive(ctx, backup.ExportOptions{
		DBPath:     s.store.Path(),
		CertsDir:   s.certsRootDir(),
		Passphrase: req.Passphrase,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create backup archive: "+err.Error())
		return
	}

	ts := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("sbox-panel-backup-%s.tar.gz", ts)
	encryptedHeader := "false"
	if req.Passphrase != "" {
		filename = fmt.Sprintf("sbox-panel-backup-%s.sboxbak", ts)
		encryptedHeader = "true"
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Backup-Encrypted", encryptedHeader)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleBackupRestore serves POST /api/v1/backup/restore (multipart `file` + optional `passphrase`).
func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, 128<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart backup upload: "+err.Error())
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing multipart form field 'file'")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read uploaded backup file: "+err.Error())
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "uploaded backup file is empty")
		return
	}

	passphrase := r.FormValue("passphrase")
	oldBasePath := s.BasePath()

	restored, newBasePath, err := s.performSafeRestore(ctx, data, passphrase)
	if err != nil {
		writeError(w, http.StatusBadRequest, "backup restore failed: "+err.Error())
		return
	}

	if newBasePath != oldBasePath {
		if token := auth.ExtractToken(r); token != "" {
			if _, verifyErr := s.tokens.VerifyToken(token); verifyErr == nil {
				auth.ClearAuthCookie(w, oldBasePath, isRequestSecure(r))
				auth.SetAuthCookie(w, token, newBasePath, isRequestSecure(r), s.tokens.TTL())
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"restored":  restored,
		"base_path": newBasePath,
	})
}

// handleGetRemoteBackupConfig serves GET /api/v1/backup/remote/config.
func (s *Server) handleGetRemoteBackupConfig(w http.ResponseWriter, r *http.Request) {
	cfg, lastAt, lastErr, err := s.store.GetRemoteBackupConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load remote backup configuration")
		return
	}
	writeJSON(w, http.StatusOK, toRemoteBackupConfigView(cfg, lastAt, lastErr))
}

// handleUpdateRemoteBackupConfig serves PUT /api/v1/backup/remote/config.
func (s *Server) handleUpdateRemoteBackupConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, lastAt, lastErr, err := s.store.GetRemoteBackupConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load remote backup configuration")
		return
	}

	var req struct {
		Enabled       *bool   `json:"enabled"`
		Type          *string `json:"type"`
		Token         *string `json:"token"`
		Repo          *string `json:"repo"`
		Branch        *string `json:"branch"`
		PathPrefix    *string `json:"path_prefix"`
		GistID        *string `json:"gist_id"`
		KeepCount     *int    `json:"keep_count"`
		IntervalHours *int    `json:"interval_hours"`
		Passphrase    *string `json:"passphrase"`
		ProxyURL      *string `json:"proxy_url"`
		APIBaseURL    *string `json:"api_base_url"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.Type != nil {
		t := strings.ToLower(strings.TrimSpace(*req.Type))
		if t != "github_repo" && t != "github_gist" {
			writeError(w, http.StatusBadRequest, "type must be 'github_repo' or 'github_gist'")
			return
		}
		cfg.Type = t
	}
	// Preserve stored token/passphrase when omitted or empty.
	if req.Token != nil {
		if tok := strings.TrimSpace(*req.Token); tok != "" {
			cfg.Token = tok
		}
	}
	if req.Passphrase != nil {
		if pw := *req.Passphrase; pw != "" {
			cfg.Passphrase = pw
		}
	}
	if req.Repo != nil {
		cfg.Repo = strings.TrimSpace(*req.Repo)
	}
	if req.Branch != nil {
		cfg.Branch = strings.TrimSpace(*req.Branch)
	}
	if req.PathPrefix != nil {
		cfg.PathPrefix = strings.TrimSpace(*req.PathPrefix)
	}
	if req.GistID != nil {
		cfg.GistID = strings.TrimSpace(*req.GistID)
	}
	if req.KeepCount != nil {
		if *req.KeepCount < 1 || *req.KeepCount > 100 {
			writeError(w, http.StatusBadRequest, "keep_count must be between 1 and 100")
			return
		}
		cfg.KeepCount = *req.KeepCount
	}
	if req.IntervalHours != nil {
		if *req.IntervalHours < 0 {
			writeError(w, http.StatusBadRequest, "interval_hours must be >= 0")
			return
		}
		cfg.IntervalHours = *req.IntervalHours
	}
	if req.ProxyURL != nil {
		cfg.ProxyURL = strings.TrimSpace(*req.ProxyURL)
	}
	if req.APIBaseURL != nil {
		cfg.APIBaseURL = strings.TrimSpace(*req.APIBaseURL)
	}

	if err := s.store.SaveRemoteBackupConfig(ctx, cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save remote backup configuration")
		return
	}
	updatedCfg, lastAt, lastErr, _ := s.store.GetRemoteBackupConfig(ctx)
	writeJSON(w, http.StatusOK, toRemoteBackupConfigView(updatedCfg, lastAt, lastErr))
}

func validateRemoteBackupForAccess(cfg model.RemoteBackupConfig, requirePassphrase bool) error {
	if strings.TrimSpace(cfg.Token) == "" {
		return errors.New("GitHub token is required for remote backup")
	}
	if requirePassphrase && cfg.Passphrase == "" {
		return errors.New("encryption passphrase is required for remote backup push")
	}
	switch cfg.Type {
	case "github_repo":
		if strings.TrimSpace(cfg.Repo) == "" || !strings.Contains(cfg.Repo, "/") {
			return errors.New("valid GitHub repo (owner/repo) is required")
		}
	case "github_gist":
		if strings.TrimSpace(cfg.GistID) == "" {
			return errors.New("GitHub gist_id is required")
		}
	default:
		return errors.New("type must be 'github_repo' or 'github_gist'")
	}
	return nil
}

// RunRemoteBackup creates an AES-256-GCM encrypted backup and pushes it to the configured GitHub target.
func (s *Server) RunRemoteBackup(ctx context.Context) (*backup.RemoteBackupEntry, error) {
	cfg, _, _, err := s.store.GetRemoteBackupConfig(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateRemoteBackupForAccess(cfg, true); err != nil {
		return nil, err
	}

	_ = s.store.CheckpointWAL(ctx)
	archive, err := backup.CreateArchive(ctx, backup.ExportOptions{
		DBPath:     s.store.Path(),
		CertsDir:   s.certsRootDir(),
		Passphrase: cfg.Passphrase,
	})
	if err != nil {
		_ = s.store.RecordRemoteBackupStatus(ctx, "", err.Error())
		return nil, fmt.Errorf("create encrypted archive: %w", err)
	}

	instanceID, _ := s.store.GetSettingDefault(ctx, model.SettingInstanceID, "panel")
	filename := fmt.Sprintf("sbox-panel-%s-%s.sboxbak", sanitizeBackupFileSegment(instanceID), time.Now().UTC().Format("20060102-150405"))

	entry, err := backup.PushGitHubBackup(ctx, archive, filename, s.toGitHubBackupConfig(ctx, cfg))
	if err != nil {
		_ = s.store.RecordRemoteBackupStatus(ctx, "", err.Error())
		if s.telemetry != nil {
			s.telemetry.PublishLog("error", "backup", "remote encrypted backup failed: "+err.Error())
		}
		return nil, err
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	_ = s.store.RecordRemoteBackupStatus(ctx, nowStr, "")
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "backup", fmt.Sprintf("pushed encrypted remote backup %s (%d bytes)", entry.Name, entry.Size))
	}
	return entry, nil
}

// RunScheduledRemoteBackupIfDue checks remote backup config and runs an encrypted backup if interval_hours has elapsed.
func (s *Server) RunScheduledRemoteBackupIfDue(ctx context.Context) {
	if s.store == nil || s.store.DB() == nil {
		return
	}
	cfg, lastAtStr, _, err := s.store.GetRemoteBackupConfig(ctx)
	if err != nil || !cfg.Enabled || cfg.IntervalHours <= 0 {
		return
	}
	if err := validateRemoteBackupForAccess(cfg, true); err != nil {
		return
	}
	if strings.TrimSpace(lastAtStr) != "" {
		if lastAt, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(lastAtStr)); parseErr == nil {
			if time.Since(lastAt) < time.Duration(cfg.IntervalHours)*time.Hour {
				return
			}
		}
	}
	_, _ = s.RunRemoteBackup(ctx)
}

// handleRemoteBackupPush serves POST /api/v1/backup/remote/push.
func (s *Server) handleRemoteBackupPush(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, _, _, err := s.store.GetRemoteBackupConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load remote backup config")
		return
	}
	if err := validateRemoteBackupForAccess(cfg, true); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entry, err := s.RunRemoteBackup(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "remote backup push failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entry": entry,
	})
}

// handleRemoteBackupList serves GET /api/v1/backup/remote/list.
func (s *Server) handleRemoteBackupList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, _, _, err := s.store.GetRemoteBackupConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load remote backup config")
		return
	}
	if err := validateRemoteBackupForAccess(cfg, false); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, err := backup.ListGitHubBackups(ctx, s.toGitHubBackupConfig(ctx, cfg))
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list remote backups: "+err.Error())
		return
	}
	if entries == nil {
		entries = []backup.RemoteBackupEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
	})
}

// handleRemoteBackupRestore serves POST /api/v1/backup/remote/restore ({ path, passphrase? }).
func (s *Server) handleRemoteBackupRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Path       string `json:"path"`
		Passphrase string `json:"passphrase"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "backup path is required")
		return
	}

	cfg, _, _, err := s.store.GetRemoteBackupConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load remote backup config")
		return
	}
	if err := validateRemoteBackupForAccess(cfg, false); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	data, err := backup.FetchGitHubBackup(ctx, req.Path, s.toGitHubBackupConfig(ctx, cfg))
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch remote backup: "+err.Error())
		return
	}

	passphrase := req.Passphrase
	if passphrase == "" {
		passphrase = cfg.Passphrase
	}
	oldBasePath := s.BasePath()

	restored, newBasePath, err := s.performSafeRestore(ctx, data, passphrase)
	if err != nil {
		writeError(w, http.StatusBadRequest, "remote backup restore failed: "+err.Error())
		return
	}

	if newBasePath != oldBasePath {
		if token := auth.ExtractToken(r); token != "" {
			if _, verifyErr := s.tokens.VerifyToken(token); verifyErr == nil {
				auth.ClearAuthCookie(w, oldBasePath, isRequestSecure(r))
				auth.SetAuthCookie(w, token, newBasePath, isRequestSecure(r), s.tokens.TTL())
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"restored":  restored,
		"base_path": newBasePath,
	})
}
