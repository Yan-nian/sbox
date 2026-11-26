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
	certs, err := s.store.ListCertificates(r.Context())
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

func (s *Server) handleCreateSelfSignedCert(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
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

	suffix, err := store.RandomHex(4)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate directory suffix")
		return
	}
	certDir := filepath.Join(s.certsRootDir(), "self-"+suffix)

	info, err := cert.GenerateSelfSigned(cert.SelfSignedOptions{
		CertDir:   certDir,
		Domains:   req.Domains,
		ValidDays: req.ValidDays,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "generate self-signed certificate failed: "+err.Error())
		return
	}

	nb := info.NotBefore.UTC()
	na := info.NotAfter.UTC()
	rec := &model.Certificate{
		Name:        req.Name,
		Type:        "self_signed",
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
		_ = os.RemoveAll(certDir)
		writeError(w, http.StatusInternalServerError, "failed to save certificate: "+err.Error())
		return
	}

	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("generated self-signed certificate %q (%s)", rec.Name, strings.Join(rec.Domains, ",")))
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleCreateExternalCert(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
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

	info, err := cert.InspectCertFiles(req.CertPath, req.KeyPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid external certificate files: "+err.Error())
		return
	}

	nb := info.NotBefore.UTC()
	na := info.NotAfter.UTC()
	rec := &model.Certificate{
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
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("imported external certificate %q from %s", rec.Name, rec.CertPath))
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleCreateACMECert(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
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

	suffix, err := store.RandomHex(4)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate directory suffix")
		return
	}
	certDir := filepath.Join(s.certsRootDir(), "acme-"+suffix)
	accountDir := filepath.Join(s.certsRootDir(), "acme-accounts")

	info, err := cert.IssueACME(ctx, cert.ACMEOptions{
		CertDir:     certDir,
		AccountDir:  accountDir,
		Email:       req.Email,
		Domains:     req.Domains,
		Challenge:   req.Challenge,
		HTTPPort:    req.HTTPPort,
		DNSProvider: req.DNSProvider,
		DNSCreds:    req.DNSCreds,
		Staging:     req.Staging,
	})
	if err != nil {
		_ = os.RemoveAll(certDir)
		writeError(w, http.StatusBadGateway, "ACME certificate issuance failed: "+err.Error())
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

	nb := info.NotBefore.UTC()
	na := info.NotAfter.UTC()
	rec := &model.Certificate{
		Name:         req.Name,
		Type:         certType,
		Domains:      info.Domains,
		ACMEEmail:    req.Email,
		ACMEHTTPPort: req.HTTPPort,
		ACMEStaging:  req.Staging,
		DNSProvider:  req.DNSProvider,
		DNSCredsJSON: credsJSON,
		CertPath:     info.CertPath,
		KeyPath:      info.KeyPath,
		Issuer:       info.Issuer,
		Fingerprint:  info.Fingerprint,
		NotBefore:    &nb,
		NotAfter:     &na,
		AutoRenew:    autoRenew,
	}
	if err := s.store.CreateCertificate(ctx, rec); err != nil {
		_ = os.RemoveAll(certDir)
		writeError(w, http.StatusInternalServerError, "failed to save ACME certificate: "+err.Error())
		return
	}

	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", fmt.Sprintf("issued ACME (%s) certificate %q for %s", req.Challenge, rec.Name, strings.Join(rec.Domains, ",")))
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) renewCertificateRecord(ctx context.Context, c *model.Certificate) error {
	var (
		info *cert.CertInfo
		err  error
	)

	switch c.Type {
	case "self_signed":
		certDir := filepath.Dir(c.CertPath)
		if certDir == "" || certDir == "." {
			suffix, _ := store.RandomHex(4)
			certDir = filepath.Join(s.certsRootDir(), "self-"+suffix)
		}
		info, err = cert.GenerateSelfSigned(cert.SelfSignedOptions{
			CertDir:   certDir,
			Domains:   c.Domains,
			ValidDays: 3650,
		})
	case "external":
		info, err = cert.InspectCertFiles(c.CertPath, c.KeyPath)
	case "acme_http", "acme_dns":
		challenge := "http01"
		if c.Type == "acme_dns" {
			challenge = "dns01"
		}
		var creds map[string]string
		if strings.TrimSpace(c.DNSCredsJSON) != "" {
			_ = json.Unmarshal([]byte(c.DNSCredsJSON), &creds)
		}
		certDir := filepath.Dir(c.CertPath)
		if certDir == "" || certDir == "." {
			suffix, _ := store.RandomHex(4)
			certDir = filepath.Join(s.certsRootDir(), "acme-"+suffix)
		}
		info, err = cert.IssueACME(ctx, cert.ACMEOptions{
			CertDir:     certDir,
			AccountDir:  filepath.Join(s.certsRootDir(), "acme-accounts"),
			Email:       c.ACMEEmail,
			Domains:     c.Domains,
			Challenge:   challenge,
			HTTPPort:    c.ACMEHTTPPort,
			DNSProvider: c.DNSProvider,
			DNSCreds:    creds,
			Staging:     c.ACMEStaging,
		})
	default:
		return fmt.Errorf("unsupported certificate type %q", c.Type)
	}

	if err != nil {
		c.LastError = err.Error()
		_ = s.store.UpdateCertificate(ctx, c)
		return err
	}

	nb := info.NotBefore.UTC()
	na := info.NotAfter.UTC()
	c.Domains = info.Domains
	c.CertPath = info.CertPath
	c.KeyPath = info.KeyPath
	c.Issuer = info.Issuer
	c.Fingerprint = info.Fingerprint
	c.NotBefore = &nb
	c.NotAfter = &na
	c.LastError = ""

	if err := s.store.UpdateCertificate(ctx, c); err != nil {
		return err
	}

	_ = s.RecompileAndApply(ctx)
	if ps, err := s.store.GetPanelSettings(ctx); err == nil && ps.PanelCertID == c.ID && s.onPanelCertChange != nil {
		s.onPanelCertChange()
	}
	return nil
}

// RenewDueCertificates checks all auto-renewing ACME certificates expiring within 30 days and renews them.
func (s *Server) RenewDueCertificates(ctx context.Context) {
	certs, err := s.store.ListCertificates(ctx)
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
		if s.telemetry != nil {
			s.telemetry.PublishLog("info", "panel", fmt.Sprintf("auto-renewing expiring certificate %q (id=%d)", c.Name, c.ID))
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
	usedByPanel := ps.PanelCertID == id

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

	// Only clean up managed certificate directories inside <dataDir>/certs/
	if c.Type != "external" && c.CertPath != "" {
		absRoot, err1 := filepath.Abs(s.certsRootDir())
		absCertDir, err2 := filepath.Abs(filepath.Dir(c.CertPath))
		if err1 == nil && err2 == nil && strings.HasPrefix(absCertDir, absRoot+string(filepath.Separator)) {
			_ = os.RemoveAll(absCertDir)
		}
	}

	if inRefCount > 0 {
		_ = s.RecompileAndApply(ctx)
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
	var req struct {
		Target string `json:"target"`
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
	res := cert.ProbeRealityTarget(r.Context(), req.Target)
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
