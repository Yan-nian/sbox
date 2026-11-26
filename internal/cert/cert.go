package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	fullchainFileName = "fullchain.pem"
	privkeyFileName   = "privkey.pem"
	defaultValidDays  = 3650
)

// CertInfo describes a loaded or generated TLS certificate and key pair on disk.
type CertInfo struct {
	CertPath    string    `json:"cert_path"`
	KeyPath     string    `json:"key_path"`
	Domains     []string  `json:"domains"`
	Issuer      string    `json:"issuer"`
	Fingerprint string    `json:"fingerprint"` // SHA-256 hex of leaf DER cert
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
}

// SelfSignedOptions configures self-signed certificate generation.
type SelfSignedOptions struct {
	CertDir   string
	Domains   []string // DNS names or IPs (fallback "localhost" if empty)
	ValidDays int      // default 3650
}

// GenerateSelfSigned creates an ECDSA P-256 self-signed TLS server certificate,
// writes fullchain.pem and privkey.pem (0600) into opts.CertDir, and returns CertInfo.
func GenerateSelfSigned(opts SelfSignedOptions) (*CertInfo, error) {
	certDir := strings.TrimSpace(opts.CertDir)
	if certDir == "" {
		return nil, errors.New("cert: cert_dir is required")
	}

	domains := normalizeDomains(opts.Domains)
	if len(domains) == 0 {
		domains = []string{"localhost"}
	}

	validDays := opts.ValidDays
	if validDays <= 0 {
		validDays = defaultValidDays
	}

	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return nil, fmt.Errorf("cert: create cert dir: %w", err)
	}

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("cert: generate ECDSA P-256 key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, fmt.Errorf("cert: generate serial number: %w", err)
	}
	if serialNumber.Sign() == 0 {
		serialNumber.SetInt64(1)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   domains[0],
			Organization: []string{"sbox-panel self-signed"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(time.Duration(validDays) * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	for _, d := range domains {
		cleanedIP := strings.Trim(d, "[]")
		if ip := net.ParseIP(cleanedIP); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, d)
		}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &privKey.PublicKey, privKey)
	if err != nil {
		return nil, fmt.Errorf("cert: create self-signed certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	})

	keyDER, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("cert: marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyDER,
	})

	certPath := filepath.Join(certDir, fullchainFileName)
	keyPath := filepath.Join(certDir, privkeyFileName)

	if err := writeFileAtomic(certPath, certPEM, 0o644); err != nil {
		return nil, fmt.Errorf("cert: write %s: %w", fullchainFileName, err)
	}
	if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("cert: write %s: %w", privkeyFileName, err)
	}

	return InspectCertFiles(certPath, keyPath)
}

// InspectCertFiles loads and verifies a PEM certificate and key pair from disk
// and parses metadata from the leaf X.509 certificate.
func InspectCertFiles(certPath, keyPath string) (*CertInfo, error) {
	certPath = strings.TrimSpace(certPath)
	keyPath = strings.TrimSpace(keyPath)
	if certPath == "" || keyPath == "" {
		return nil, errors.New("cert: cert_path and key_path are required")
	}

	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("cert: load x509 keypair: %w", err)
	}
	if len(tlsCert.Certificate) == 0 {
		return nil, errors.New("cert: certificate file contains no certificates")
	}

	leaf := tlsCert.Leaf
	if leaf == nil {
		leaf, err = x509.ParseCertificate(tlsCert.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("cert: parse leaf certificate: %w", err)
		}
	}

	domains := extractCertDomains(leaf)
	sum := sha256.Sum256(leaf.Raw)

	return &CertInfo{
		CertPath:    certPath,
		KeyPath:     keyPath,
		Domains:     domains,
		Issuer:      formatIssuer(leaf),
		Fingerprint: hex.EncodeToString(sum[:]),
		NotBefore:   leaf.NotBefore.UTC(),
		NotAfter:    leaf.NotAfter.UTC(),
	}, nil
}

func extractCertDomains(leaf *x509.Certificate) []string {
	seen := make(map[string]struct{}, len(leaf.DNSNames)+len(leaf.IPAddresses)+1)
	out := make([]string, 0, len(leaf.DNSNames)+len(leaf.IPAddresses)+1)
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}

	for _, dns := range leaf.DNSNames {
		add(dns)
	}
	for _, ip := range leaf.IPAddresses {
		add(ip.String())
	}
	if len(out) == 0 && leaf.Subject.CommonName != "" {
		add(leaf.Subject.CommonName)
	}
	return out
}

func formatIssuer(leaf *x509.Certificate) string {
	if cn := strings.TrimSpace(leaf.Issuer.CommonName); cn != "" {
		return cn
	}
	if len(leaf.Issuer.Organization) > 0 {
		orgs := make([]string, 0, len(leaf.Issuer.Organization))
		for _, o := range leaf.Issuer.Organization {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				orgs = append(orgs, trimmed)
			}
		}
		if len(orgs) > 0 {
			return strings.Join(orgs, ", ")
		}
	}
	return strings.TrimSpace(leaf.Issuer.String())
}

func normalizeDomains(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		d := strings.TrimSpace(item)
		if d == "" {
			continue
		}
		key := strings.ToLower(d)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, d)
	}
	return out
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-cert-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	_ = os.Chmod(path, perm)
	return nil
}
