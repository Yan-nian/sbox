package api

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sbox-panel/sbox-panel/internal/cert"
	"github.com/sbox-panel/sbox-panel/internal/configgen"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
)

func randomHighPort() int {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return 20000 + int(binary.BigEndian.Uint16(b[:])%29000)
}

func (s *Server) handleListInbounds(w http.ResponseWriter, r *http.Request) {
	inbounds, err := s.store.ListInbounds(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list inbounds")
		return
	}
	if inbounds == nil {
		inbounds = []model.Inbound{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"inbounds": inbounds,
	})
}

func (s *Server) handleInboundDefaults(w http.ResponseWriter, r *http.Request) {
	proto := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "protocol")))
	port := randomHighPort()
	listen := "::"
	var settings map[string]any

	switch proto {
	case "vless":
		kp, err := cert.GenerateRealityKeypair()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "generate reality keypair failed: "+err.Error())
			return
		}
		settings = map[string]any{
			"tls_mode":                 "reality",
			"flow":                     "xtls-rprx-vision",
			"transport":                "tcp",
			"ws_path":                  "/ws",
			"ws_host":                  "",
			"grpc_service_name":        "grpc",
			"reality_handshake_server": "www.microsoft.com",
			"reality_handshake_port":   443,
			"reality_private_key":      kp.PrivateKey,
			"reality_public_key":       kp.PublicKey,
			"reality_short_ids":        []string{kp.ShortID},
			"server_name":              "www.microsoft.com",
		}
	case "hysteria2":
		settings = map[string]any{
			"up_mbps":                 100,
			"down_mbps":               100,
			"obfs_password":           "",
			"masquerade":              "",
			"server_ports":            "",
			"ignore_client_bandwidth": false,
		}
	case "tuic":
		settings = map[string]any{
			"congestion_control": "bbr",
			"auth_timeout":       "3s",
			"zero_rtt_handshake": false,
			"heartbeat":          "10s",
		}
	case "anytls":
		settings = map[string]any{
			"padding_scheme": []string{},
		}
	case "shadowsocks":
		method := "2022-blake3-aes-128-gcm"
		srvKey, err := cert.GenerateSS2022Key(method)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "generate ss2022 key failed: "+err.Error())
			return
		}
		settings = map[string]any{
			"method":          method,
			"server_password": srvKey,
			"network":         "",
		}
	case "trojan":
		settings = map[string]any{
			"transport":         "tcp",
			"ws_path":           "/trojan",
			"ws_host":           "",
			"grpc_service_name": "trojan",
			"server_name":       "",
		}
	case "shadowtls":
		method := "2022-blake3-aes-128-gcm"
		srvKey, err := cert.GenerateSS2022Key(method)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "generate ss2022 key failed: "+err.Error())
			return
		}
		settings = map[string]any{
			"handshake_server":   "www.microsoft.com",
			"handshake_port":     443,
			"strict_mode":        true,
			"detour_protocol":    "shadowsocks",
			"ss_method":          method,
			"ss_server_password": srvKey,
		}
	case "direct":
		settings = map[string]any{
			"override_address": "",
			"override_port":    0,
			"network":          "",
		}
	case "mixed", "socks", "http":
		listen = "127.0.0.1"
		settings = map[string]any{
			"auth_enabled": true,
		}
	default:
		writeError(w, http.StatusBadRequest, "unsupported protocol: "+proto)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tag":         fmt.Sprintf("%s-%d", proto, port),
		"listen":      listen,
		"listen_port": port,
		"settings":    settings,
	})
}

func (s *Server) validateInboundCandidate(ctx context.Context, ib model.Inbound) error {
	members, _ := s.store.ListMembers(ctx)
	enabledMembers := make([]model.Member, 0, len(members))
	memberByID := make(map[int64]model.Member, len(members))
	for _, m := range members {
		memberByID[m.ID] = m
		if m.Enabled {
			enabledMembers = append(enabledMembers, m)
		}
	}

	certByID := make(map[int64]model.Certificate)
	if ib.CertificateID != nil && *ib.CertificateID > 0 {
		c, err := s.store.GetCertificate(ctx, *ib.CertificateID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return errors.New("referenced certificate not found")
			}
			return err
		}
		certByID[c.ID] = *c
	}

	spec := s.buildInboundSpec(ib, enabledMembers, memberByID, certByID)
	_, err := configgen.CompileInbound(spec)
	return err
}

type optionalInt64 struct {
	Set   bool
	Value *int64
}

func (o *optionalInt64) UnmarshalJSON(data []byte) error {
	o.Set = true
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		o.Value = nil
		return nil
	}
	var v int64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

type inboundPayload struct {
	Name          *string             `json:"name"`
	Tag           *string             `json:"tag"`
	Protocol      *string             `json:"protocol"`
	Enabled       *bool               `json:"enabled"`
	Listen        *string             `json:"listen"`
	ListenPort    *int                `json:"listen_port"`
	Settings      json.RawMessage     `json:"settings"`
	CertificateID optionalInt64       `json:"certificate_id"`
	MemberMode    *string             `json:"member_mode"`
	MemberIDs     *[]int64            `json:"member_ids"`
	ExternalHost  *string             `json:"external_host"`
	ExternalPort  *int                `json:"external_port"`
	ExtraEntries  *[]model.ExtraEntry `json:"extra_entries"`
}

func (s *Server) handleCreateInbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req inboundPayload
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	proto := ""
	if req.Protocol != nil {
		proto = strings.ToLower(strings.TrimSpace(*req.Protocol))
	}
	listenPort := 0
	if req.ListenPort != nil {
		listenPort = *req.ListenPort
	}

	if name == "" || proto == "" || listenPort < 1 || listenPort > 65535 {
		writeError(w, http.StatusBadRequest, "name, protocol, and valid listen_port (1..65535) are required")
		return
	}

	tag := ""
	if req.Tag != nil {
		tag = strings.TrimSpace(*req.Tag)
	}
	if tag == "" {
		suffix, _ := store.RandomHex(2)
		tag = fmt.Sprintf("%s-%d-%s", proto, listenPort, suffix)
	}

	listen := "::"
	if req.Listen != nil && strings.TrimSpace(*req.Listen) != "" {
		listen = strings.TrimSpace(*req.Listen)
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	memberMode := "all"
	if req.MemberMode != nil && *req.MemberMode == "selected" {
		memberMode = "selected"
	}

	var memberIDs []int64
	if req.MemberIDs != nil {
		memberIDs = *req.MemberIDs
	}

	var extraEntries []model.ExtraEntry
	if req.ExtraEntries != nil {
		extraEntries = *req.ExtraEntries
	}

	extHost := ""
	if req.ExternalHost != nil {
		extHost = strings.TrimSpace(*req.ExternalHost)
	}
	extPort := 0
	if req.ExternalPort != nil && *req.ExternalPort > 0 {
		extPort = *req.ExternalPort
	}

	var certID *int64
	if req.CertificateID.Set && req.CertificateID.Value != nil && *req.CertificateID.Value > 0 {
		v := *req.CertificateID.Value
		certID = &v
	}

	settingsRaw := req.Settings
	if len(settingsRaw) == 0 {
		settingsRaw = json.RawMessage("{}")
	}

	ib := &model.Inbound{
		Name:          name,
		Tag:           tag,
		Protocol:      proto,
		Enabled:       enabled,
		Listen:        listen,
		ListenPort:    listenPort,
		Settings:      settingsRaw,
		CertificateID: certID,
		MemberMode:    memberMode,
		MemberIDs:     memberIDs,
		ExternalHost:  extHost,
		ExternalPort:  extPort,
		ExtraEntries:  extraEntries,
	}

	if err := s.validateInboundCandidate(ctx, *ib); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.CreateInbound(ctx, ib); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "an inbound with this tag already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create inbound: "+err.Error())
		return
	}

	_ = s.RecompileAndApply(ctx)
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("created inbound %q (%s:%d)", ib.Tag, ib.Protocol, ib.ListenPort))
	}
	writeJSON(w, http.StatusCreated, ib)
}

func (s *Server) handleUpdateInbound(w http.ResponseWriter, r *http.Request) {
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

	var req inboundPayload
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != nil {
		if n := strings.TrimSpace(*req.Name); n != "" {
			ib.Name = n
		}
	}
	if req.Tag != nil {
		if t := strings.TrimSpace(*req.Tag); t != "" {
			ib.Tag = t
		}
	}
	if req.Protocol != nil {
		if p := strings.ToLower(strings.TrimSpace(*req.Protocol)); p != "" {
			ib.Protocol = p
		}
	}
	if req.Enabled != nil {
		ib.Enabled = *req.Enabled
	}
	if req.Listen != nil {
		if l := strings.TrimSpace(*req.Listen); l != "" {
			ib.Listen = l
		}
	}
	if req.ListenPort != nil {
		ib.ListenPort = *req.ListenPort
	}
	if len(req.Settings) > 0 {
		ib.Settings = req.Settings
	}
	if req.CertificateID.Set {
		if req.CertificateID.Value == nil || *req.CertificateID.Value <= 0 {
			ib.CertificateID = nil
		} else {
			v := *req.CertificateID.Value
			ib.CertificateID = &v
		}
	}
	if req.MemberMode != nil {
		ib.MemberMode = *req.MemberMode
	}
	if req.MemberIDs != nil {
		ib.MemberIDs = *req.MemberIDs
	}
	if req.ExternalHost != nil {
		ib.ExternalHost = strings.TrimSpace(*req.ExternalHost)
	}
	if req.ExternalPort != nil {
		ib.ExternalPort = *req.ExternalPort
	}
	if req.ExtraEntries != nil {
		ib.ExtraEntries = *req.ExtraEntries
	}

	if err := s.validateInboundCandidate(ctx, *ib); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.UpdateInbound(ctx, ib); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "an inbound with this tag already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update inbound: "+err.Error())
		return
	}

	_ = s.RecompileAndApply(ctx)
	writeJSON(w, http.StatusOK, ib)
}

func (s *Server) handleToggleInbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid inbound id")
		return
	}

	ib, err := s.store.ToggleInbound(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "inbound not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to toggle inbound")
		return
	}

	_ = s.RecompileAndApply(ctx)
	writeJSON(w, http.StatusOK, ib)
}

func (s *Server) handleDeleteInbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid inbound id")
		return
	}

	if err := s.store.DeleteInbound(ctx, id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "inbound not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete inbound")
		return
	}

	_ = s.RecompileAndApply(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
