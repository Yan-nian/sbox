package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
	"github.com/sbox-panel/sbox-panel/internal/syncer"
)

type syncTargetConfigPayload struct {
	Token        *string `json:"token"`
	GistID       *string `json:"gist_id"`
	Repo         *string `json:"repo"`
	Branch       *string `json:"branch"`
	FilePath     *string `json:"file_path"`
	SubStoreURL  *string `json:"sub_store_url"`
	SubStoreName *string `json:"sub_store_name"`
	ProxyURL     *string `json:"proxy_url"`
	APIBaseURL   *string `json:"api_base_url"`
}

type syncTargetPayload struct {
	Name      *string                  `json:"name"`
	Enabled   *bool                    `json:"enabled"`
	Type      *string                  `json:"type"`
	MemberID  *int64                   `json:"member_id"`
	Format    *string                  `json:"format"`
	MergeMode *string                  `json:"merge_mode"`
	AutoPush  *bool                    `json:"auto_push"`
	Config    *syncTargetConfigPayload `json:"config"`
}

func applySyncConfigPatch(dst *model.SyncTargetConfig, patch *syncTargetConfigPayload) {
	if patch == nil {
		return
	}
	if patch.Token != nil {
		if tok := strings.TrimSpace(*patch.Token); tok != "" {
			dst.Token = tok
		}
	}
	if patch.GistID != nil {
		dst.GistID = strings.TrimSpace(*patch.GistID)
	}
	if patch.Repo != nil {
		dst.Repo = strings.TrimSpace(*patch.Repo)
	}
	if patch.Branch != nil {
		dst.Branch = strings.TrimSpace(*patch.Branch)
	}
	if patch.FilePath != nil {
		dst.FilePath = strings.TrimSpace(*patch.FilePath)
	}
	if patch.SubStoreURL != nil {
		dst.SubStoreURL = strings.TrimSpace(*patch.SubStoreURL)
	}
	if patch.SubStoreName != nil {
		dst.SubStoreName = strings.TrimSpace(*patch.SubStoreName)
	}
	if patch.ProxyURL != nil {
		dst.ProxyURL = strings.TrimSpace(*patch.ProxyURL)
	}
	if patch.APIBaseURL != nil {
		dst.APIBaseURL = strings.TrimSpace(*patch.APIBaseURL)
	}
}

func validateSyncTarget(t *model.SyncTarget) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return errors.New("sync target name is required")
	}
	t.Type = strings.ToLower(strings.TrimSpace(t.Type))
	switch t.Type {
	case "github_gist", "github_repo", "sub_store":
	default:
		return errors.New("type must be 'github_gist', 'github_repo', or 'sub_store'")
	}
	if t.MemberID <= 0 {
		return errors.New("member_id is required")
	}
	format, ok := normalizeSubFormat(t.Format, false)
	if !ok {
		return errors.New("format must be 'uri', 'base64', 'singbox', or 'clash'")
	}
	t.Format = format

	t.MergeMode = strings.ToLower(strings.TrimSpace(t.MergeMode))
	if t.MergeMode == "" {
		if t.Type == "sub_store" {
			t.MergeMode = "replace"
		} else {
			t.MergeMode = "merge_by_instance"
		}
	}
	if t.MergeMode != "replace" && t.MergeMode != "merge_by_instance" {
		return errors.New("merge_mode must be 'replace' or 'merge_by_instance'")
	}

	switch t.Type {
	case "github_gist":
		if strings.TrimSpace(t.Config.GistID) == "" {
			return errors.New("config.gist_id is required for github_gist target")
		}
		if strings.TrimSpace(t.Config.Token) == "" {
			return errors.New("config.token is required for github_gist target")
		}
		if strings.TrimSpace(t.Config.FilePath) == "" {
			t.Config.FilePath = "sbox-nodes.txt"
		}
	case "github_repo":
		if strings.TrimSpace(t.Config.Repo) == "" || !strings.Contains(t.Config.Repo, "/") {
			return errors.New("config.repo (owner/repo) is required for github_repo target")
		}
		if strings.TrimSpace(t.Config.Token) == "" {
			return errors.New("config.token is required for github_repo target")
		}
		if strings.TrimSpace(t.Config.Branch) == "" {
			t.Config.Branch = "main"
		}
		if strings.TrimSpace(t.Config.FilePath) == "" {
			t.Config.FilePath = "sbox-nodes.txt"
		}
	case "sub_store":
		if strings.TrimSpace(t.Config.SubStoreURL) == "" {
			return errors.New("config.sub_store_url is required for sub_store target")
		}
	}
	return nil
}

func (s *Server) handleListSyncTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := s.store.ListSyncTargets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sync targets")
		return
	}
	views := make([]model.SyncTargetView, 0, len(targets))
	for _, t := range targets {
		views = append(views, t.ToView())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"targets": views,
	})
}

func (s *Server) handleCreateSyncTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req syncTargetPayload
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	t := &model.SyncTarget{
		Enabled:  true,
		AutoPush: false,
	}
	if req.Name != nil {
		t.Name = *req.Name
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.Type != nil {
		t.Type = *req.Type
	}
	if req.MemberID != nil {
		t.MemberID = *req.MemberID
	}
	if req.Format != nil && strings.TrimSpace(*req.Format) != "" {
		t.Format = *req.Format
	} else if strings.EqualFold(strings.TrimSpace(t.Type), "sub_store") {
		t.Format = "uri"
	} else {
		t.Format = "base64"
	}
	if req.MergeMode != nil {
		t.MergeMode = *req.MergeMode
	}
	if req.AutoPush != nil {
		t.AutoPush = *req.AutoPush
	}
	applySyncConfigPatch(&t.Config, req.Config)

	if err := validateSyncTarget(t); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.store.GetMember(ctx, t.MemberID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusBadRequest, "referenced member not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify member")
		return
	}

	if err := s.store.CreateSyncTarget(ctx, t); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create sync target: "+err.Error())
		return
	}
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("created sync target %q (%s)", t.Name, t.Type))
	}
	writeJSON(w, http.StatusCreated, t.ToView())
}

func (s *Server) handleUpdateSyncTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid sync target id")
		return
	}

	t, err := s.store.GetSyncTarget(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "sync target not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load sync target")
		return
	}

	var req syncTargetPayload
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != nil {
		t.Name = *req.Name
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.Type != nil {
		t.Type = *req.Type
	}
	if req.MemberID != nil {
		t.MemberID = *req.MemberID
	}
	if req.Format != nil {
		t.Format = *req.Format
	}
	if req.MergeMode != nil {
		t.MergeMode = *req.MergeMode
	}
	if req.AutoPush != nil {
		t.AutoPush = *req.AutoPush
	}
	// Preserves existing t.Config.Token when req.Config.Token is omitted or blank!
	applySyncConfigPatch(&t.Config, req.Config)

	if err := validateSyncTarget(t); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.store.GetMember(ctx, t.MemberID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusBadRequest, "referenced member not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify member")
		return
	}

	if err := s.store.UpdateSyncTarget(ctx, t); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update sync target: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t.ToView())
}

func (s *Server) handleDeleteSyncTarget(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid sync target id")
		return
	}
	if err := s.store.DeleteSyncTarget(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "sync target not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete sync target")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) executeSyncTargetPush(ctx context.Context, t *model.SyncTarget) (*model.SyncTarget, syncer.PushResult) {
	now := time.Now().UTC()
	var res syncer.PushResult

	member, err := s.store.GetMember(ctx, t.MemberID)
	if err != nil {
		res = syncer.PushResult{
			Success: false,
			Message: fmt.Sprintf("bound member (id=%d) not found: %v", t.MemberID, err),
		}
	} else {
		// Real Sub-Store `source: "local"` parses plain URI lines or Clash YAML, NOT full sing-box client JSON.
		renderFormat := t.Format
		if t.Type == "sub_store" && renderFormat != "clash" {
			renderFormat = "uri"
		}
		out, renderErr := s.renderMemberSubscription(ctx, member, renderFormat, "")
		if renderErr != nil {
			res = syncer.PushResult{
				Success: false,
				Message: "render subscription failed: " + renderErr.Error(),
			}
		} else {
			ps, _ := s.store.GetPanelSettings(ctx)
			instanceID := strings.TrimSpace(ps.InstanceID)
			if instanceID == "" {
				instanceID = "sbox-panel"
			}
			proxyURL := strings.TrimSpace(t.Config.ProxyURL)
			if proxyURL == "" {
				proxyURL = strings.TrimSpace(ps.GithubProxy)
			}

			pushCfg := syncer.PushTargetConfig{
				ID:           t.ID,
				Name:         t.Name,
				Type:         t.Type,
				Format:       renderFormat,
				MergeMode:    t.MergeMode,
				InstanceID:   instanceID,
				Token:        t.Config.Token,
				GistID:       t.Config.GistID,
				Repo:         t.Config.Repo,
				Branch:       t.Config.Branch,
				FilePath:     t.Config.FilePath,
				SubStoreURL:  t.Config.SubStoreURL,
				SubStoreName: t.Config.SubStoreName,
				ProxyURL:     proxyURL,
				APIBaseURL:   t.Config.APIBaseURL,
			}
			res = syncer.PushToTarget(ctx, pushCfg, out.Content, len(out.Nodes))
		}
	}

	status := "error"
	errMsg := res.Message
	if res.Success {
		status = "success"
		errMsg = ""
	}
	_ = s.store.UpdateSyncTargetStatus(ctx, t.ID, now, status, errMsg, res.RemoteURL)
	_ = s.store.RecordSyncHistory(ctx, &model.SyncHistory{
		TargetID:   t.ID,
		TargetName: t.Name,
		Success:    res.Success,
		HTTPStatus: res.HTTPStatus,
		NodeCount:  res.NodeCount,
		Message:    res.Message,
	})

	if s.telemetry != nil {
		if res.Success {
			s.telemetry.PublishLog("info", "sync", fmt.Sprintf("pushed sync target %q (%s, %d nodes, HTTP %d)", t.Name, t.Type, res.NodeCount, res.HTTPStatus))
		} else {
			s.telemetry.PublishLog("error", "sync", fmt.Sprintf("push sync target %q (%s) failed: %s", t.Name, t.Type, res.Message))
		}
	}

	if updated, err := s.store.GetSyncTarget(ctx, t.ID); err == nil {
		return updated, res
	}
	return t, res
}

func (s *Server) handlePushSyncTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid sync target id")
		return
	}

	t, err := s.store.GetSyncTarget(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "sync target not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load sync target")
		return
	}

	updated, res := s.executeSyncTargetPush(ctx, t)
	writeJSON(w, http.StatusOK, map[string]any{
		"target": updated.ToView(),
		"result": res,
	})
}

type pushAllResultItem struct {
	TargetID   int64             `json:"target_id"`
	TargetName string            `json:"target_name"`
	Result     syncer.PushResult `json:"result"`
}

func (s *Server) handlePushAllSyncTargets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	targets, err := s.store.ListSyncTargets(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sync targets")
		return
	}

	results := make([]pushAllResultItem, 0)
	for i := range targets {
		t := &targets[i]
		if !t.Enabled {
			continue
		}
		_, res := s.executeSyncTargetPush(ctx, t)
		results = append(results, pushAllResultItem{
			TargetID:   t.ID,
			TargetName: t.Name,
			Result:     res,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
	})
}

func (s *Server) handleListSyncHistory(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	history, err := s.store.ListSyncHistory(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sync history")
		return
	}
	if history == nil {
		history = []model.SyncHistory{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"history": history,
	})
}

// TriggerAutoSync coalesces rapid inbound/member/node/certificate mutations and asynchronously
// pushes all enabled `auto_push` sync targets after a 3-second debounce window.
func (s *Server) TriggerAutoSync() {
	s.autoSyncMu.Lock()
	defer s.autoSyncMu.Unlock()
	if s.autoSyncClosed {
		return
	}
	delay := s.autoSyncDelay
	if delay <= 0 {
		delay = 3 * time.Second
	}
	if s.autoSyncTimer != nil {
		s.autoSyncTimer.Stop()
	}
	s.autoSyncTimer = time.AfterFunc(delay, func() {
		s.runAutoSync()
	})
}

// StopAutoSync cancels any pending debounced auto-sync timer (used during shutdown/tests).
func (s *Server) StopAutoSync() {
	s.autoSyncMu.Lock()
	s.autoSyncClosed = true
	if s.autoSyncTimer != nil {
		s.autoSyncTimer.Stop()
		s.autoSyncTimer = nil
	}
	s.autoSyncMu.Unlock()
}

func (s *Server) runAutoSync() {
	if s.store == nil || s.store.DB() == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	targets, err := s.store.ListSyncTargets(ctx)
	if err != nil {
		return
	}
	for i := range targets {
		t := &targets[i]
		if !t.Enabled || !t.AutoPush {
			continue
		}
		s.executeSyncTargetPush(ctx, t)
	}
}
