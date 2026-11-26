package cert

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const defaultProbeTimeout = 5 * time.Second

// RealityKeypair holds an X25519 key pair in sing-box base64 RawURLEncoding format
// along with a default 8-byte hex short ID.
type RealityKeypair struct {
	PrivateKey string `json:"private_key"` // base64 RawURLEncoding 32B X25519 scalar (sing-box format!)
	PublicKey  string `json:"public_key"`  // base64 RawURLEncoding 32B X25519 pubkey
	ShortID    string `json:"short_id"`    // 8-byte hex (16 chars)
}

// RealityProbeResult reports TLS 1.3, HTTP/2 ALPN, latency, and leaf certificate metadata
// gathered by probing a candidate Reality camouflage target.
type RealityProbeResult struct {
	Target     string `json:"target"`
	Reachable  bool   `json:"reachable"`
	TLS13      bool   `json:"tls13"`
	H2         bool   `json:"h2"`
	LatencyMS  int64  `json:"latency_ms"`
	ServerName string `json:"server_name,omitempty"`
	Issuer     string `json:"issuer,omitempty"`
	NotAfter   string `json:"not_after,omitempty"`
	Error      string `json:"error,omitempty"`
}

// GenerateRealityKeypair generates a valid X25519 key pair matching `sing-box generate reality-keypair`
// (RFC 7748 clamped 32-byte scalar and public key encoded with base64.RawURLEncoding) plus an 8-byte hex short ID.
func GenerateRealityKeypair() (RealityKeypair, error) {
	var scalar [32]byte
	if _, err := rand.Read(scalar[:]); err != nil {
		return RealityKeypair{}, fmt.Errorf("cert: read random bytes for x25519 key: %w", err)
	}

	// Apply RFC 7748 / Curve25519 clamping identical to `sing-box generate reality-keypair`.
	scalar[0] &= 248
	scalar[31] &= 127
	scalar[31] |= 64

	privKey, err := ecdh.X25519().NewPrivateKey(scalar[:])
	if err != nil {
		return RealityKeypair{}, fmt.Errorf("cert: construct x25519 private key: %w", err)
	}
	pubBytes := privKey.PublicKey().Bytes()

	shortID, err := GenerateShortID(8)
	if err != nil {
		return RealityKeypair{}, err
	}

	return RealityKeypair{
		PrivateKey: base64.RawURLEncoding.EncodeToString(privKey.Bytes()),
		PublicKey:  base64.RawURLEncoding.EncodeToString(pubBytes),
		ShortID:    shortID,
	}, nil
}

// GenerateShortID generates a random lowercase hex Reality short ID of byteLen bytes (1..8; 0 defaults to 8).
func GenerateShortID(byteLen int) (string, error) {
	if byteLen == 0 {
		byteLen = 8
	}
	if byteLen < 1 || byteLen > 8 {
		return "", fmt.Errorf("cert: reality short_id byte length must be between 1 and 8 (got %d)", byteLen)
	}
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("cert: generate short id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// ProbeRealityTarget dials target (e.g. "example.com" or "example.com:443") over TLS
// with a 5-second timeout, MinVersion TLS 1.2, and ALPN ["h2", "http/1.1"], reporting
// TLS 1.3 and HTTP/2 support, handshake latency, and leaf certificate details.
func ProbeRealityTarget(ctx context.Context, target string) RealityProbeResult {
	if ctx == nil {
		ctx = context.Background()
	}

	host, addr, err := parseProbeTarget(target)
	if err != nil {
		return RealityProbeResult{
			Target: strings.TrimSpace(target),
			Error:  err.Error(),
		}
	}

	res := RealityProbeResult{
		Target: addr,
	}

	probeCtx, cancel := context.WithTimeout(ctx, defaultProbeTimeout)
	defer cancel()

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
		// Diagnostic probe inspects peer TLS parameters/certificate without transmitting sensitive data.
		InsecureSkipVerify: true, //nolint:gosec
	}
	if net.ParseIP(host) == nil {
		tlsCfg.ServerName = host
	}

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{
			Timeout: defaultProbeTimeout,
		},
		Config: tlsCfg,
	}

	start := time.Now()
	conn, err := dialer.DialContext(probeCtx, "tcp", addr)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		res.LatencyMS = latency
		res.Error = err.Error()
		return res
	}
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		res.LatencyMS = latency
		res.Error = "unexpected non-TLS connection"
		return res
	}

	state := tlsConn.ConnectionState()
	res.Reachable = true
	res.LatencyMS = latency
	res.TLS13 = state.Version == tls.VersionTLS13
	res.H2 = state.NegotiatedProtocol == "h2"

	if len(state.PeerCertificates) > 0 {
		leaf := state.PeerCertificates[0]
		if cn := strings.TrimSpace(leaf.Subject.CommonName); cn != "" {
			res.ServerName = cn
		} else if len(leaf.DNSNames) > 0 {
			res.ServerName = leaf.DNSNames[0]
		}
		res.Issuer = formatIssuer(leaf)
		res.NotAfter = leaf.NotAfter.UTC().Format(time.RFC3339)
	}

	return res
}

func parseProbeTarget(target string) (host, addr string, err error) {
	cleaned := strings.TrimSpace(target)
	if cleaned == "" {
		return "", "", errors.New("target is required")
	}

	if idx := strings.Index(cleaned, "://"); idx != -1 {
		cleaned = cleaned[idx+3:]
	}
	if idx := strings.IndexByte(cleaned, '/'); idx != -1 {
		cleaned = cleaned[:idx]
	}
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "", "", errors.New("target host is empty")
	}

	if h, p, splitErr := net.SplitHostPort(cleaned); splitErr == nil {
		h = strings.TrimSpace(h)
		p = strings.TrimSpace(p)
		if h == "" {
			return "", "", errors.New("target host is empty")
		}
		if p == "" {
			p = "443"
		}
		return h, net.JoinHostPort(h, p), nil
	}

	unbracketed := strings.Trim(cleaned, "[]")
	if unbracketed == "" {
		return "", "", errors.New("target host is empty")
	}
	// Reject malformed host:port (multiple colons that are not a valid IPv6 literal).
	if strings.Contains(unbracketed, ":") && net.ParseIP(unbracketed) == nil {
		return "", "", fmt.Errorf("invalid target address %q", target)
	}

	return unbracketed, net.JoinHostPort(unbracketed, "443"), nil
}

// GenerateSS2022Key generates a random standard-base64 key sized for Shadowsocks 2022 methods:
// 16 bytes for aes-128-gcm methods, 32 bytes for aes-256-gcm and chacha20-poly1305 methods.
func GenerateSS2022Key(method string) (string, error) {
	var keyLen int
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "2022-blake3-aes-128-gcm", "aes-128-gcm":
		keyLen = 16
	case "2022-blake3-aes-256-gcm", "aes-256-gcm",
		"2022-blake3-chacha20-poly1305", "chacha20-ietf-poly1305", "chacha20-poly1305":
		keyLen = 32
	default:
		return "", fmt.Errorf("cert: unsupported shadowsocks method %q", method)
	}

	buf := make([]byte, keyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("cert: generate shadowsocks key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}
