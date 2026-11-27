package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sbox-panel/sbox-panel/internal/cert"
	"github.com/sbox-panel/sbox-panel/internal/cluster"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
)

func (s *Server) certsRootDir() string {
	base := s.dataDir
	if base == "" {
		base = "./data"
	}
	return filepath.Join(base, "certs")
}

func (s *Server) handleListCertificates(w http.ResponseWriter, r *http.Request) {
	nodeID := parseNodeIDQuery(r, 1, true)
	certs, err := s.store.ListCertificates(r.Context(), nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list certificates")
		return
	}
	if certs == nil {
		certs = []model.Certificate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"certificates": certs,
	})
}

func applyCertBundleToRecord(rec *model.Certificate, bundle *cluster.CertFileBundle) {
	if bundle == nil {
		return
	}
	if len(bundle.CertPEM) > 0 {
		rec.CertPEM = string(bundle.CertPEM)
	}
	if len(bundle.KeyPEM) > 0 {
		rec.KeyPEM = string(bundle.KeyPEM)
	}
	if bundle.Info != nil {
		info := bundle.Info
		if len(info.Domains) > 0 {
			rec.Domains = info.Domains
		}
		rec.CertPath = info.CertPath
		rec.KeyPath = info.KeyPath
		rec.Issuer = info.Issuer
		rec.Fingerprint = info.Fingerprint
		nb := info.NotBefore.UTC()
		na := info.NotAfter.UTC()
		rec.NotBefore = &nb
		rec.NotAfter = &na
	}
}

func (s *Server) handleCreateSelfSignedCert(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		NodeID    int64    `json:"node_id"`
		Name      string   `json:"name"`
		Domains   []string `json:"domains"`
		ValidDays int      `json:"valid_days"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "certificate name is required")
		return
	}
	if req.ValidDays <= 0 {
		req.ValidDays = 3650
	}
	nodeID := req.NodeID
	if nodeID <= 0 {
		nodeID = parseNodeIDQuery(r, 1, false)
	}

	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}

	rec := &model.Certificate{
		NodeID:    nodeID,
		Name:      req.Name,
		Type:      "self_signed",
		Domains:   req.Domains,
		AutoRenew: false,
	}
	if err := s.store.CreateCertificate(ctx, rec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate certificate record: "+err.Error())
		return
	}

	bundle, err := ctrl.GenerateSelfSigned(ctx, rec.ID, req.Domains, req.ValidDays)
	if err != nil {
		_ = s.store.DeleteCertificate(ctx, rec.ID)
		writeError(w, http.StatusBadRequest, "generate self-signed certificate failed: "+err.Error())
		return
	}

	applyCertBundleToRecord(rec, bundle)
	if err := s.store.UpdateCertificate(ctx, rec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save certificate metadata: "+err.Error())
		return
	}

	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("generated self-signed certificate %q on node %d (%s)", rec.Name, nodeID, strings.Join(rec.Domains, ",")))
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleCreateExternalCert(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		NodeID   int64  `json:"node_id"`
		Name     string `json:"name"`
		CertPath string `json:"cert_path"`
		KeyPath  string `json:"key_path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.CertPath = strings.TrimSpace(req.CertPath)
	req.KeyPath = strings.TrimSpace(req.KeyPath)
	if req.Name == "" || req.CertPath == "" || req.KeyPath == "" {
		writeError(w, http.StatusBadRequest, "name, cert_path, and key_path are required")
		return
	}
	nodeID := req.NodeID
	if nodeID <= 0 {
		nodeID = parseNodeIDQuery(r, 1, false)
	}

	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}

	info, err := ctrl.InspectCertFiles(ctx, req.CertPath, req.KeyPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid external certificate files: "+err.Error())
		return
	}

	nb := info.NotBefore.UTC()
	na := info.NotAfter.UTC()
	rec := &model.Certificate{
		NodeID:      nodeID,
		Name:        req.Name,
		Type:        "external",
		Domains:     info.Domains,
		CertPath:    info.CertPath,
		KeyPath:     info.KeyPath,
		Issuer:      info.Issuer,
		Fingerprint: info.Fingerprint,
		NotBefore:   &nb,
		NotAfter:    &na,
		AutoRenew:   false,
	}
	if err := s.store.CreateCertificate(ctx, rec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save certificate: "+err.Error())
		return
	}

	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("imported external certificate %q on node %d from %s", rec.Name, nodeID, rec.CertPath))
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleCreateACMECert(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		NodeID      int64             `json:"node_id"`
		Name        string            `json:"name"`
		Email       string            `json:"email"`
		Domains     []string          `json:"domains"`
		Challenge   string            `json:"challenge"` // "http01" | "dns01"
		HTTPPort    string            `json:"http_port"`
		DNSProvider string            `json:"dns_provider"`
		DNSCreds    map[string]string `json:"dns_creds"`
		AutoRenew   *bool             `json:"auto_renew"`
		Staging     bool              `json:"staging"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(req.Email)
	req.Challenge = strings.ToLower(strings.TrimSpace(req.Challenge))
	req.DNSProvider = strings.ToLower(strings.TrimSpace(req.DNSProvider))
	if req.HTTPPort == "" {
		req.HTTPPort = "80"
	}
	if req.Name == "" || req.Email == "" || len(req.Domains) == 0 {
		writeError(w, http.StatusBadRequest, "name, email, and at least one domain are required")
		return
	}

	certType := "acme_http"
	if req.Challenge == "dns01" {
		certType = "acme_dns"
		if req.DNSProvider == "" {
			writeError(w, http.StatusBadRequest, "dns_provider is required for dns01 challenge")
			return
		}
	} else if req.Challenge != "http01" {
		writeError(w, http.StatusBadRequest, "challenge must be 'http01' or 'dns01'")
		return
	}

	nodeID := req.NodeID
	if nodeID <= 0 {
		nodeID = parseNodeIDQuery(r, 1, false)
	}
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}

	autoRenew := true
	if req.AutoRenew != nil {
		autoRenew = *req.AutoRenew
	}
	credsJSON := ""
	if len(req.DNSCreds) > 0 {
		b, _ := json.Marshal(req.DNSCreds)
		credsJSON = string(b)
	}

	rec := &model.Certificate{
		NodeID:       nodeID,
		Name:         req.Name,
		Type:         certType,
		Domains:      req.Domains,
		ACMEEmail:    req.Email,
		ACMEHTTPPort: req.HTTPPort,
		ACMEStaging:  req.Staging,
		DNSProvider:  req.DNSProvider,
		DNSCredsJSON: credsJSON,
		AutoRenew:    autoRenew,
	}
	if err := s.store.CreateCertificate(ctx, rec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate ACME certificate record: "+err.Error())
		return
	}

	bundle, err := ctrl.IssueACME(ctx, rec.ID, cert.ACMEOptions{
		Email:       req.Email,
		Domains:     req.Domains,
		Challenge:   req.Challenge,
		HTTPPort:    req.HTTPPort,
		DNSProvider: req.DNSProvider,
		DNSCreds:    req.DNSCreds,
		Staging:     req.Staging,
	})
	if err != nil {
		_ = s.store.DeleteCertificate(ctx, rec.ID)
		writeError(w, http.StatusBadGateway, "ACME certificate issuance failed: "+err.Error())
		return
	}

	applyCertBundleToRecord(rec, bundle)
	if err := s.store.UpdateCertificate(ctx, rec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save ACME certificate metadata: "+err.Error())
		return
	}

	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("issued ACME (%s) certificate %q on node %d for %s", req.Challenge, rec.Name, nodeID, strings.Join(rec.Domains, ",")))
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) renewCertificateRecord(ctx context.Context, c *model.Certificate) error {
	if s.hub == nil {
		return errors.New("节点当前离线")
	}
	ctrl, ok := s.hub.Get(c.NodeID)
	if !ok || !ctrl.IsOnline() {
		return errors.New("节点当前离线")
	}

	var err error
	switch c.Type {
	case "self_signed":
		var bundle *cluster.CertFileBundle
		bundle, err = ctrl.GenerateSelfSigned(ctx, c.ID, c.Domains, 3650)
		if err == nil {
			applyCertBundleToRecord(c, bundle)
		}
	case "external":
		var info *cert.CertInfo
		info, err = ctrl.InspectCertFiles(ctx, c.CertPath, c.KeyPath)
		if err == nil && info != nil {
			nb := info.NotBefore.UTC()
			na := info.NotAfter.UTC()
			c.Domains = info.Domains
			c.CertPath = info.CertPath
			c.KeyPath = info.KeyPath
			c.Issuer = info.Issuer
			c.Fingerprint = info.Fingerprint
			c.NotBefore = &nb
			c.NotAfter = &na
		}
	case "acme_http", "acme_dns":
		challenge := "http01"
		if c.Type == "acme_dns" {
			challenge = "dns01"
		}
		var creds map[string]string
		if strings.TrimSpace(c.DNSCredsJSON) != "" {
			_ = json.Unmarshal([]byte(c.DNSCredsJSON), &creds)
		}
		var bundle *cluster.CertFileBundle
		bundle, err = ctrl.IssueACME(ctx, c.ID, cert.ACMEOptions{
			Email:       c.ACMEEmail,
			Domains:     c.Domains,
			Challenge:   challenge,
			HTTPPort:    c.ACMEHTTPPort,
			DNSProvider: c.DNSProvider,
			DNSCreds:    creds,
			Staging:     c.ACMEStaging,
		})
		if err == nil {
			applyCertBundleToRecord(c, bundle)
		}
	default:
		return fmt.Errorf("unsupported certificate type %q", c.Type)
	}

	if err != nil {
		c.LastError = err.Error()
		_ = s.store.UpdateCertificate(ctx, c)
		return err
	}

	c.LastError = ""
	if err := s.store.UpdateCertificate(ctx, c); err != nil {
		return err
	}

	_ = s.RecompileAndApplyNode(ctx, c.NodeID)
	if c.NodeID == 1 {
		if ps, err := s.store.GetPanelSettings(ctx); err == nil && ps.PanelCertID == c.ID && s.onPanelCertChange != nil {
			s.onPanelCertChange()
		}
	}
	return nil
}

// RenewDueCertificates checks all auto-renewing ACME certificates across online nodes expiring within 30 days.
func (s *Server) RenewDueCertificates(ctx context.Context) {
	certs, err := s.store.ListCertificates(ctx, 0)
	if err != nil {
		return
	}
	threshold := time.Now().UTC().Add(30 * 24 * time.Hour)
	for i := range certs {
		c := &certs[i]
		if !c.AutoRenew || (c.Type != "acme_http" && c.Type != "acme_dns") {
			continue
		}
		if c.NotAfter != nil && c.NotAfter.After(threshold) {
			continue
		}
		if s.hub != nil && !s.hub.IsOnline(c.NodeID) {
			continue
		}
		if s.telemetry != nil {
			s.telemetry.PublishLog("info", "panel", fmt.Sprintf("auto-renewing expiring certificate %q (id=%d, node=%d)", c.Name, c.ID, c.NodeID))
		}
		if err := s.renewCertificateRecord(ctx, c); err != nil {
			if s.telemetry != nil {
				s.telemetry.PublishLog("error", "panel", fmt.Sprintf("auto-renew certificate %q failed: %v", c.Name, err))
			}
		} else if s.telemetry != nil {
			s.telemetry.PublishLog("info", "panel", fmt.Sprintf("auto-renewed certificate %q successfully", c.Name))
		}
	}
}

func (s *Server) handleRenewCertificate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid certificate id")
		return
	}
	c, err := s.store.GetCertificate(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "certificate not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load certificate")
		return
	}

	if _, _, ok := s.resolveOnlineController(w, ctx, c.NodeID); !ok {
		return
	}

	if err := s.renewCertificateRecord(ctx, c); err != nil {
		writeError(w, http.StatusBadGateway, "certificate renewal failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleDeleteCertificate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid certificate id")
		return
	}

	c, err := s.store.GetCertificate(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "certificate not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load certificate")
		return
	}

	force := strings.EqualFold(r.URL.Query().Get("force"), "true")
	inRefCount, err := s.store.CountInboundsByCertificate(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check certificate usage")
		return
	}
	ps, _ := s.store.GetPanelSettings(ctx)
	usedByPanel := c.NodeID == 1 && ps.PanelCertID == id

	if (inRefCount > 0 || usedByPanel) && !force {
		writeError(w, http.StatusConflict, "certificate is currently referenced by inbound(s) or panel HTTPS; pass ?force=true to delete")
		return
	}

	if inRefCount > 0 {
		_ = s.store.ClearInboundCertificateRefs(ctx, id)
	}
	if usedByPanel {
		_ = s.store.SetSetting(ctx, model.SettingPanelTLSCertID, "0")
		if s.onPanelCertChange != nil {
			s.onPanelCertChange()
		}
	}

	if err := s.store.DeleteCertificate(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete certificate")
		return
	}

	// Only clean up local managed certificate directories inside <dataDir>/certs/
	if c.NodeID == 1 && c.Type != "external" && c.CertPath != "" {
		absRoot, err1 := filepath.Abs(s.certsRootDir())
		absCertDir, err2 := filepath.Abs(filepath.Dir(c.CertPath))
		if err1 == nil && err2 == nil && strings.HasPrefix(absCertDir, absRoot+string(filepath.Separator)) {
			_ = os.RemoveAll(absCertDir)
		}
	}

	if inRefCount > 0 {
		_ = s.RecompileAndApplyNode(ctx, c.NodeID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Crypto & Reality Tools ---

func (s *Server) handleToolRealityKeypair(w http.ResponseWriter, _ *http.Request) {
	kp, err := cert.GenerateRealityKeypair()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate reality keypair failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, kp)
}

func (s *Server) handleToolRealityProbe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Target string `json:"target"`
		NodeID int64  `json:"node_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Target = strings.TrimSpace(req.Target)
	if req.Target == "" {
		writeError(w, http.StatusBadRequest, "target domain or host:port is required")
		return
	}

	nodeID := req.NodeID
	if nodeID <= 0 {
		nodeID = parseNodeIDQuery(r, 1, false)
	}
	ctrl, _, ok := s.resolveOnlineController(w, ctx, nodeID)
	if !ok {
		return
	}

	res, err := ctrl.ProbeRealityTarget(ctx, req.Target)
	if err != nil {
		writeError(w, http.StatusBadGateway, "reality probe failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleToolSS2022Key(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string `json:"method"`
	}
	_ = decodeJSON(r, &req)
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = "2022-blake3-aes-128-gcm"
	}
	key, err := cert.GenerateSS2022Key(method)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"key":    key,
		"method": method,
	})
}
