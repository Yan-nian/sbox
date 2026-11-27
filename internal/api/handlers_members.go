package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sbox-panel/sbox-panel/internal/cert"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
)

func generateUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func generateRandomPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func populateMemberCredentials(m *model.Member, rotateSubToken bool) error {
	u, err := generateUUIDv4()
	if err != nil {
		return err
	}
	pw, err := generateRandomPassword()
	if err != nil {
		return err
	}
	k16, err := cert.GenerateSS2022Key("2022-blake3-aes-128-gcm")
	if err != nil {
		return err
	}
	k32, err := cert.GenerateSS2022Key("2022-blake3-aes-256-gcm")
	if err != nil {
		return err
	}
	m.UUID = u
	m.Password = pw
	m.SS2022Key16 = k16
	m.SS2022Key32 = k32
	if rotateSubToken || m.SubToken == "" {
		tok, err := store.RandomHex(16)
		if err != nil {
			return err
		}
		m.SubToken = tok
	}
	return nil
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	members, err := s.store.ListMembers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list members")
		return
	}
	if members == nil {
		members = []model.Member{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"members": members,
	})
}

func (s *Server) handleCreateMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Name    string `json:"name"`
		Note    string `json:"note"`
		Enabled *bool  `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 64 {
		writeError(w, http.StatusBadRequest, "member name must be 1 to 64 characters")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	m := &model.Member{
		Name:    req.Name,
		Enabled: enabled,
		Note:    strings.TrimSpace(req.Note),
	}
	if err := populateMemberCredentials(m, true); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate credentials: "+err.Error())
		return
	}

	if err := s.store.CreateMember(ctx, m); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "a member with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create member: "+err.Error())
		return
	}

	s.RecompileAllOnlineNodes(ctx)
	s.TriggerAutoSync()
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("created global member %q (synced to all online nodes)", m.Name))
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleUpdateMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid member id")
		return
	}

	m, err := s.store.GetMember(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load member")
		return
	}

	var req struct {
		Name    *string `json:"name"`
		Note    *string `json:"note"`
		Enabled *bool   `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 64 {
			writeError(w, http.StatusBadRequest, "member name must be 1 to 64 characters")
			return
		}
		m.Name = name
	}
	if req.Note != nil {
		m.Note = strings.TrimSpace(*req.Note)
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}

	if err := s.store.UpdateMember(ctx, m); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "a member with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update member")
		return
	}

	s.RecompileAllOnlineNodes(ctx)
	s.TriggerAutoSync()
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleToggleMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid member id")
		return
	}

	m, err := s.store.ToggleMember(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to toggle member")
		return
	}

	s.RecompileAllOnlineNodes(ctx)
	s.TriggerAutoSync()
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleResetMemberCredentials(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid member id")
		return
	}

	m, err := s.store.GetMember(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load member")
		return
	}

	var req struct {
		ResetSubToken bool `json:"reset_sub_token"`
	}
	_ = decodeJSON(r, &req)

	if err := populateMemberCredentials(m, req.ResetSubToken); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to rotate credentials")
		return
	}
	if err := s.store.UpdateMember(ctx, m); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist rotated credentials")
		return
	}

	s.RecompileAllOnlineNodes(ctx)
	s.TriggerAutoSync()
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("reset credentials for global member %q", m.Name))
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleDeleteMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid member id")
		return
	}

	if err := s.store.DeleteMember(ctx, id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "member not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete member")
		return
	}

	s.RecompileAllOnlineNodes(ctx)
	s.TriggerAutoSync()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
