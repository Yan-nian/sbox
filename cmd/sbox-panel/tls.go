package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/store"
)

// panelTLSHolder manages hot-reloadable TLS certificates for the panel HTTPS listener.
type panelTLSHolder struct {
	store *store.Store

	mu     sync.RWMutex
	certID int64
	cert   *tls.Certificate
}

func newPanelTLSHolder(st *store.Store) *panelTLSHolder {
	return &panelTLSHolder{store: st}
}

// Reload loads or clears the configured panel TLS certificate from SQLite and disk.
func (h *panelTLSHolder) Reload() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ps, err := h.store.GetPanelSettings(ctx)
	if err != nil {
		return err
	}
	if ps.PanelCertID <= 0 {
		h.mu.Lock()
		h.certID = 0
		h.cert = nil
		h.mu.Unlock()
		return nil
	}

	rec, err := h.store.GetCertificate(ctx, ps.PanelCertID)
	if err != nil {
		return fmt.Errorf("load panel cert record id=%d: %w", ps.PanelCertID, err)
	}
	pair, err := tls.LoadX509KeyPair(rec.CertPath, rec.KeyPath)
	if err != nil {
		return fmt.Errorf("load panel TLS keypair (%s, %s): %w", rec.CertPath, rec.KeyPath, err)
	}

	h.mu.Lock()
	h.certID = rec.ID
	h.cert = &pair
	h.mu.Unlock()
	return nil
}

// HasCert reports whether a valid TLS certificate is currently loaded.
func (h *panelTLSHolder) HasCert() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cert != nil
}

// GetCertificate satisfies tls.Config.GetCertificate for zero-downtime certificate rotation.
func (h *panelTLSHolder) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	h.mu.RLock()
	c := h.cert
	h.mu.RUnlock()
	if c == nil {
		return nil, errors.New("no panel TLS certificate loaded")
	}
	return c, nil
}

// HandleCertChange reloads the in-memory certificate when settings or ACME renewals update it.
func (h *panelTLSHolder) HandleCertChange(httpsActive bool) {
	if err := h.Reload(); err != nil {
		log.Printf("warn: panel TLS reload failed: %v", err)
		return
	}
	if h.HasCert() && !httpsActive {
		log.Printf("info: panel HTTPS certificate configured; restart sbox-panel to switch listener to HTTPS")
	} else if !h.HasCert() && httpsActive {
		log.Printf("info: panel HTTPS disabled in settings; restart sbox-panel to switch listener back to HTTP")
	} else if h.HasCert() && httpsActive {
		log.Printf("info: reloaded live panel HTTPS certificate")
	}
}
