package cert

import (
	"context"
	"crypto/ecdh"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
)

func TestGenerateSelfSignedAndInspect(t *testing.T) {
	dir := t.TempDir()

	info, err := GenerateSelfSigned(SelfSignedOptions{
		CertDir:   dir,
		Domains:   []string{"example.com", "sub.example.com", "127.0.0.1", "::1", "example.com"},
		ValidDays: 90,
	})
	if err != nil {
		t.Fatalf("GenerateSelfSigned failed: %v", err)
	}

	if info.CertPath != filepath.Join(dir, "fullchain.pem") {
		t.Fatalf("unexpected cert_path: %q", info.CertPath)
	}
	if info.KeyPath != filepath.Join(dir, "privkey.pem") {
		t.Fatalf("unexpected key_path: %q", info.KeyPath)
	}
	if len(info.Fingerprint) != 64 {
		t.Fatalf("expected 64-char sha256 hex fingerprint, got %q (len=%d)", info.Fingerprint, len(info.Fingerprint))
	}
	if _, err := hex.DecodeString(info.Fingerprint); err != nil {
		t.Fatalf("fingerprint is not valid hex: %v", err)
	}
	if info.Issuer == "" {
		t.Fatal("expected non-empty issuer")
	}
	for _, want := range []string{"example.com", "sub.example.com", "127.0.0.1", "::1"} {
		if !slices.Contains(info.Domains, want) {
			t.Fatalf("expected domain/IP %q in %v", want, info.Domains)
		}
	}
	if len(info.Domains) != 4 {
		t.Fatalf("expected deduplicated 4 SANs, got %v", info.Domains)
	}

	validSpan := info.NotAfter.Sub(info.NotBefore)
	if validSpan < 89*24*time.Hour || validSpan > 91*24*time.Hour {
		t.Fatalf("unexpected validity span: %v", validSpan)
	}

	// Verify x509 key usage and ext key usage directly.
	pair, err := tls.LoadX509KeyPair(info.CertPath, info.KeyPath)
	if err != nil {
		t.Fatalf("tls.LoadX509KeyPair failed: %v", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("x509.ParseCertificate failed: %v", err)
	}
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || leaf.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Fatalf("unexpected KeyUsage: %v", leaf.KeyUsage)
	}
	if !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		t.Fatalf("missing ExtKeyUsageServerAuth: %v", leaf.ExtKeyUsage)
	}

	// Re-inspect from disk and confirm identical metadata.
	inspected, err := InspectCertFiles(info.CertPath, info.KeyPath)
	if err != nil {
		t.Fatalf("InspectCertFiles failed: %v", err)
	}
	if inspected.Fingerprint != info.Fingerprint || inspected.Issuer != info.Issuer {
		t.Fatalf("InspectCertFiles mismatch: got %+v, want %+v", inspected, info)
	}
}

func TestGenerateSelfSignedDefaultsAndRegenerate(t *testing.T) {
	dir := t.TempDir()

	first, err := GenerateSelfSigned(SelfSignedOptions{
		CertDir: dir,
	})
	if err != nil {
		t.Fatalf("GenerateSelfSigned default failed: %v", err)
	}
	if !slices.Equal(first.Domains, []string{"www.bing.com"}) {
		t.Fatalf("expected fallback domain [www.bing.com], got %v", first.Domains)
	}
	if first.NotAfter.Sub(time.Now().UTC()) < 3648*24*time.Hour {
		t.Fatalf("expected ~3650 day default validity, got NotAfter=%v", first.NotAfter)
	}

	// Overwrite/regenerate in the same directory to verify atomic replacement works on Windows & Unix.
	second, err := GenerateSelfSigned(SelfSignedOptions{
		CertDir:   dir,
		Domains:   []string{"renewed.local"},
		ValidDays: 30,
	})
	if err != nil {
		t.Fatalf("GenerateSelfSigned overwrite failed: %v", err)
	}
	if second.Fingerprint == first.Fingerprint {
		t.Fatal("expected regenerated certificate to have a new fingerprint")
	}
	if !slices.Equal(second.Domains, []string{"renewed.local"}) {
		t.Fatalf("expected regenerated domain [renewed.local], got %v", second.Domains)
	}
}

func TestInspectCertFilesErrors(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	c1, err := GenerateSelfSigned(SelfSignedOptions{CertDir: dir1, Domains: []string{"a.example"}})
	if err != nil {
		t.Fatalf("gen c1: %v", err)
	}
	c2, err := GenerateSelfSigned(SelfSignedOptions{CertDir: dir2, Domains: []string{"b.example"}})
	if err != nil {
		t.Fatalf("gen c2: %v", err)
	}

	// Mismatched cert and key must fail.
	if _, err := InspectCertFiles(c1.CertPath, c2.KeyPath); err == nil {
		t.Fatal("expected error for mismatched cert and key")
	}
	// Missing paths must fail.
	if _, err := InspectCertFiles("", c1.KeyPath); err == nil {
		t.Fatal("expected error for empty cert path")
	}
	if _, err := InspectCertFiles(filepath.Join(dir1, "missing.pem"), c1.KeyPath); err == nil {
		t.Fatal("expected error for missing cert file")
	}
	// Empty CertDir must fail.
	if _, err := GenerateSelfSigned(SelfSignedOptions{}); err == nil {
		t.Fatal("expected error for empty CertDir")
	}
}

func TestGenerateRealityKeypairX25519(t *testing.T) {
	kp, err := GenerateRealityKeypair()
	if err != nil {
		t.Fatalf("GenerateRealityKeypair failed: %v", err)
	}

	if strings.Contains(kp.PrivateKey, "=") || strings.Contains(kp.PublicKey, "=") {
		t.Fatalf("expected RawURLEncoding without padding: priv=%q pub=%q", kp.PrivateKey, kp.PublicKey)
	}

	privRaw, err := base64.RawURLEncoding.DecodeString(kp.PrivateKey)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	pubRaw, err := base64.RawURLEncoding.DecodeString(kp.PublicKey)
	if err != nil {
		t.Fatalf("decode public key: %v", err)
	}
	if len(privRaw) != 32 || len(pubRaw) != 32 {
		t.Fatalf("expected 32-byte keys, got priv=%d pub=%d", len(privRaw), len(pubRaw))
	}

	// Verify RFC 7748 clamping matches `sing-box generate reality-keypair`.
	if privRaw[0]&7 != 0 || privRaw[31]&128 != 0 || privRaw[31]&64 == 0 {
		t.Fatalf("private key is not properly RFC 7748 clamped: b0=%08b b31=%08b", privRaw[0], privRaw[31])
	}

	// Cross-verify scalar multiplication with golang.org/x/crypto/curve25519 AND crypto/ecdh.
	derivedPub, err := curve25519.X25519(privRaw, curve25519.Basepoint)
	if err != nil {
		t.Fatalf("curve25519.X25519 failed: %v", err)
	}
	if !slices.Equal(derivedPub, pubRaw) {
		t.Fatalf("curve25519 derived pubkey mismatch: got %x, want %x", derivedPub, pubRaw)
	}

	ecdhPriv, err := ecdh.X25519().NewPrivateKey(privRaw)
	if err != nil {
		t.Fatalf("ecdh.X25519().NewPrivateKey failed: %v", err)
	}
	if !slices.Equal(ecdhPriv.PublicKey().Bytes(), pubRaw) {
		t.Fatal("crypto/ecdh public key mismatch")
	}

	// Verify ECDH shared secret symmetry with a second peer keypair.
	peer, err := GenerateRealityKeypair()
	if err != nil {
		t.Fatalf("generate peer keypair: %v", err)
	}
	peerPrivRaw, _ := base64.RawURLEncoding.DecodeString(peer.PrivateKey)
	peerPubRaw, _ := base64.RawURLEncoding.DecodeString(peer.PublicKey)
	secret1, err := curve25519.X25519(privRaw, peerPubRaw)
	if err != nil {
		t.Fatalf("shared secret 1 failed: %v", err)
	}
	secret2, err := curve25519.X25519(peerPrivRaw, pubRaw)
	if err != nil {
		t.Fatalf("shared secret 2 failed: %v", err)
	}
	if !slices.Equal(secret1, secret2) {
		t.Fatal("ECDH shared secret mismatch between two generated keypairs")
	}

	// Verify ShortID is 16 lowercase hex chars (8 bytes).
	if len(kp.ShortID) != 16 {
		t.Fatalf("expected 16-char ShortID, got %q", kp.ShortID)
	}
	if _, err := hex.DecodeString(kp.ShortID); err != nil {
		t.Fatalf("ShortID is not valid hex: %v", err)
	}
}

func TestGenerateShortID(t *testing.T) {
	idDefault, err := GenerateShortID(0)
	if err != nil {
		t.Fatalf("GenerateShortID(0) failed: %v", err)
	}
	if len(idDefault) != 16 {
		t.Fatalf("expected 16 hex chars for default, got %q", idDefault)
	}

	for n := 1; n <= 8; n++ {
		id, err := GenerateShortID(n)
		if err != nil {
			t.Fatalf("GenerateShortID(%d) failed: %v", n, err)
		}
		if len(id) != n*2 {
			t.Fatalf("GenerateShortID(%d) len=%d, want %d", n, len(id), n*2)
		}
		if _, err := hex.DecodeString(id); err != nil {
			t.Fatalf("GenerateShortID(%d) invalid hex: %v", n, err)
		}
	}

	for _, bad := range []int{-1, 9, 16} {
		if _, err := GenerateShortID(bad); err == nil {
			t.Fatalf("expected error for GenerateShortID(%d)", bad)
		}
	}
}

func TestGenerateSS2022Key(t *testing.T) {
	cases := []struct {
		method  string
		wantLen int
	}{
		{"2022-blake3-aes-128-gcm", 16},
		{"aes-128-gcm", 16},
		{"2022-blake3-aes-256-gcm", 32},
		{"aes-256-gcm", 32},
		{"2022-blake3-chacha20-poly1305", 32},
	}

	for _, tc := range cases {
		key, err := GenerateSS2022Key(tc.method)
		if err != nil {
			t.Fatalf("GenerateSS2022Key(%q) failed: %v", tc.method, err)
		}
		raw, err := base64.StdEncoding.DecodeString(key)
		if err != nil {
			t.Fatalf("GenerateSS2022Key(%q) invalid base64: %v", tc.method, err)
		}
		if len(raw) != tc.wantLen {
			t.Fatalf("GenerateSS2022Key(%q) decoded len=%d, want %d", tc.method, len(raw), tc.wantLen)
		}
	}

	if _, err := GenerateSS2022Key("unknown-cipher"); err == nil {
		t.Fatal("expected error for unsupported SS2022 method")
	}
	if _, err := GenerateSS2022Key(""); err == nil {
		t.Fatal("expected error for empty SS2022 method")
	}
}

func TestProbeRealityTargetTLS13AndH2(t *testing.T) {
	dir := t.TempDir()
	info, err := GenerateSelfSigned(SelfSignedOptions{
		CertDir:   dir,
		Domains:   []string{"probe.test.local", "127.0.0.1"},
		ValidDays: 30,
	})
	if err != nil {
		t.Fatalf("GenerateSelfSigned failed: %v", err)
	}
	tlsCert, err := tls.LoadX509KeyPair(info.CertPath, info.KeyPath)
	if err != nil {
		t.Fatalf("LoadX509KeyPair failed: %v", err)
	}

	// Custom local TLS 1.3 + H2 listener.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2", "http/1.1"},
	})
	if err != nil {
		t.Fatalf("tls.Listen failed: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if tc, ok := c.(*tls.Conn); ok {
					_ = tc.Handshake()
				}
			}(conn)
		}
	}()

	res := ProbeRealityTarget(context.Background(), ln.Addr().String())
	if !res.Reachable {
		t.Fatalf("expected reachable=true, got error=%q", res.Error)
	}
	if !res.TLS13 {
		t.Fatalf("expected TLS13=true, got %+v", res)
	}
	if !res.H2 {
		t.Fatalf("expected H2=true, got %+v", res)
	}
	if res.ServerName != "probe.test.local" {
		t.Fatalf("expected ServerName=probe.test.local, got %q", res.ServerName)
	}
	if res.Issuer != "probe.test.local" {
		t.Fatalf("expected Issuer=probe.test.local, got %q", res.Issuer)
	}
	if _, err := time.Parse(time.RFC3339, res.NotAfter); err != nil {
		t.Fatalf("expected RFC3339 NotAfter, got %q: %v", res.NotAfter, err)
	}
}

func TestProbeRealityTargetTLS12AndHTTP11(t *testing.T) {
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	ts.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
	}
	ts.StartTLS()
	defer ts.Close()

	res := ProbeRealityTarget(context.Background(), ts.Listener.Addr().String())
	if !res.Reachable {
		t.Fatalf("expected reachable=true, got error=%q", res.Error)
	}
	if res.TLS13 {
		t.Fatalf("expected TLS13=false for TLS 1.2 server, got %+v", res)
	}
	if res.H2 {
		t.Fatalf("expected H2=false for http/1.1 server, got %+v", res)
	}
}

func TestProbeRealityTargetErrors(t *testing.T) {
	// Empty target.
	empty := ProbeRealityTarget(context.Background(), "   ")
	if empty.Reachable || empty.Error == "" {
		t.Fatalf("expected error for empty target, got %+v", empty)
	}

	// Closed local port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	closedAddr := ln.Addr().String()
	_ = ln.Close()

	closed := ProbeRealityTarget(context.Background(), closedAddr)
	if closed.Reachable || closed.Error == "" {
		t.Fatalf("expected unreachable for closed port, got %+v", closed)
	}

	// Default port normalization check.
	host, addr, err := parseProbeTarget("example.com")
	if err != nil || host != "example.com" || addr != "example.com:443" {
		t.Fatalf("parseProbeTarget(example.com) = (%q, %q, %v)", host, addr, err)
	}
	host, addr, err = parseProbeTarget("https://example.com:8443/path")
	if err != nil || host != "example.com" || addr != "example.com:8443" {
		t.Fatalf("parseProbeTarget(url) = (%q, %q, %v)", host, addr, err)
	}
}

func TestACMEOptionValidationAndProviders(t *testing.T) {
	dir := t.TempDir()

	// Valid HTTP-01 option normalization.
	norm, prov, err := validateACMEOptions(ACMEOptions{
		CertDir:   dir,
		Email:     "admin@example.com",
		Domains:   []string{"example.com", "www.example.com", "example.com"},
		Challenge: "http01",
	})
	if err != nil {
		t.Fatalf("validateACMEOptions http01 failed: %v", err)
	}
	if prov != nil || norm.HTTPPort != "80" || len(norm.Domains) != 2 {
		t.Fatalf("unexpected normalized http01 options: %+v", norm)
	}

	// Valid DNS-01 providers (no os.Setenv mutation).
	dnsCases := []struct {
		name     string
		provider string
		creds    map[string]string
	}{
		{"cloudflare token", "cloudflare", map[string]string{"api_token": "cf-test-token"}},
		{"cloudflare email+key", "cloudflare", map[string]string{"email": "a@example.com", "api_key": "cf-global-key"}},
		{"alidns keys", "alidns", map[string]string{"access_key": "ak-test", "secret_key": "sk-test"}},
		{"dnspod api_key", "dnspod", map[string]string{"api_key": "12345,abcdef"}},
		{"dnspod id+token", "dnspod", map[string]string{"id": "12345", "token": "abcdef"}},
	}

	for _, tc := range dnsCases {
		t.Run(tc.name, func(t *testing.T) {
			_, p, err := validateACMEOptions(ACMEOptions{
				CertDir:     dir,
				Email:       "admin@example.com",
				Domains:     []string{"*.example.com"},
				Challenge:   "dns01",
				DNSProvider: tc.provider,
				DNSCreds:    tc.creds,
			})
			if err != nil {
				t.Fatalf("validateACMEOptions(%s) failed: %v", tc.name, err)
			}
			if p == nil {
				t.Fatalf("expected non-nil DNS provider for %s", tc.name)
			}
		})
	}

	// Invalid cases.
	invalidCases := []struct {
		name string
		opts ACMEOptions
	}{
		{"missing cert_dir", ACMEOptions{Email: "a@b.com", Domains: []string{"a.com"}, Challenge: "http01"}},
		{"missing email", ACMEOptions{CertDir: dir, Domains: []string{"a.com"}, Challenge: "http01"}},
		{"invalid email", ACMEOptions{CertDir: dir, Email: "not-an-email", Domains: []string{"a.com"}, Challenge: "http01"}},
		{"missing domains", ACMEOptions{CertDir: dir, Email: "a@b.com", Challenge: "http01"}},
		{"bad challenge", ACMEOptions{CertDir: dir, Email: "a@b.com", Domains: []string{"a.com"}, Challenge: "tlsalpn01"}},
		{"bad http port", ACMEOptions{CertDir: dir, Email: "a@b.com", Domains: []string{"a.com"}, Challenge: "http01", HTTPPort: "99999"}},
		{"bad dns provider", ACMEOptions{CertDir: dir, Email: "a@b.com", Domains: []string{"a.com"}, Challenge: "dns01", DNSProvider: "route53"}},
		{"cloudflare missing creds", ACMEOptions{CertDir: dir, Email: "a@b.com", Domains: []string{"a.com"}, Challenge: "dns01", DNSProvider: "cloudflare"}},
		{"alidns missing secret", ACMEOptions{CertDir: dir, Email: "a@b.com", Domains: []string{"a.com"}, Challenge: "dns01", DNSProvider: "alidns", DNSCreds: map[string]string{"access_key": "ak"}}},
		{"dnspod missing creds", ACMEOptions{CertDir: dir, Email: "a@b.com", Domains: []string{"a.com"}, Challenge: "dns01", DNSProvider: "dnspod"}},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := validateACMEOptions(tc.opts); err == nil {
				t.Fatalf("expected error for case %q", tc.name)
			}
		})
	}

	// Cancelled context must abort IssueACME immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := IssueACME(ctx, ACMEOptions{
		CertDir:   dir,
		Email:     "admin@example.com",
		Domains:   []string{"example.com"},
		Challenge: "http01",
	}); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestACMEAccountPersistence(t *testing.T) {
	acctDir := t.TempDir()
	prodScope := acmeAccountScopeDir(acctDir, "Admin@Example.com", false)
	stagingScope := acmeAccountScopeDir(acctDir, "admin@example.com", true)
	if prodScope == stagingScope {
		t.Fatal("expected distinct account scopes for staging vs production")
	}

	u1, err := loadOrCreateACMEAccount(prodScope, "admin@example.com")
	if err != nil {
		t.Fatalf("first loadOrCreateACMEAccount failed: %v", err)
	}
	u2, err := loadOrCreateACMEAccount(prodScope, "admin@example.com")
	if err != nil {
		t.Fatalf("second loadOrCreateACMEAccount failed: %v", err)
	}

	// Ensure the same private key is reused across invocations.
	k1, err := x509.MarshalPKCS8PrivateKey(u1.GetPrivateKey())
	if err != nil {
		t.Fatalf("marshal k1: %v", err)
	}
	k2, err := x509.MarshalPKCS8PrivateKey(u2.GetPrivateKey())
	if err != nil {
		t.Fatalf("marshal k2: %v", err)
	}
	if !slices.Equal(k1, k2) {
		t.Fatal("expected ACME account private key to be persisted and reused")
	}

	if _, err := os.Stat(filepath.Join(prodScope, "account.key")); err != nil {
		t.Fatalf("expected account.key on disk: %v", err)
	}
}
