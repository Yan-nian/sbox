package syncer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultHTTPTimeout = 20 * time.Second

// newHTTPClient builds an HTTP client with optional HTTP/HTTPS/SOCKS5 proxy support.
func newHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	rawProxy := strings.TrimSpace(proxyURL)
	if rawProxy != "" {
		u, err := url.Parse(rawProxy)
		if err != nil {
			return nil, fmt.Errorf("syncer: invalid proxy URL %q: %w", rawProxy, err)
		}
		if u.Host == "" {
			return nil, fmt.Errorf("syncer: proxy URL %q missing host", rawProxy)
		}

		scheme := strings.ToLower(u.Scheme)
		switch scheme {
		case "http", "https":
			tr.Proxy = http.ProxyURL(u)
		case "socks", "socks5", "socks5h":
			socksDialer, err := newSOCKS5Dialer(u, dialer)
			if err != nil {
				return nil, err
			}
			tr.Proxy = nil
			tr.DialContext = socksDialer.DialContext
		default:
			return nil, fmt.Errorf("syncer: unsupported proxy scheme %q", u.Scheme)
		}
	}

	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}, nil
}

type socks5Dialer struct {
	proxyAddr string
	username  string
	password  string
	hasAuth   bool
	base      *net.Dialer
}

func newSOCKS5Dialer(u *url.URL, base *net.Dialer) (*socks5Dialer, error) {
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("syncer: socks5 proxy missing host")
	}
	port := u.Port()
	if port == "" {
		port = "1080"
	}
	d := &socks5Dialer{
		proxyAddr: net.JoinHostPort(host, port),
		base:      base,
	}
	if u.User != nil {
		d.username = u.User.Username()
		d.password, _ = u.User.Password()
		d.hasAuth = true
		if len(d.username) > 255 || len(d.password) > 255 {
			return nil, errors.New("syncer: socks5 credentials exceed 255 bytes")
		}
	}
	return d, nil
}

// DialContext establishes a TCP connection to addr through the configured SOCKS5 proxy (RFC 1928 / RFC 1929).
func (d *socks5Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if !strings.HasPrefix(network, "tcp") {
		return nil, fmt.Errorf("syncer: socks5 dialer only supports tcp, got %q", network)
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("syncer: invalid target address %q: %w", addr, err)
	}
	portNum, err := strconv.Atoi(portStr)
	if err != nil || portNum < 1 || portNum > 65535 {
		return nil, fmt.Errorf("syncer: invalid target port %q", portStr)
	}

	conn, err := d.base.DialContext(ctx, "tcp", d.proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("syncer: connect to socks5 proxy %s: %w", d.proxyAddr, err)
	}

	if err := d.handshake(ctx, conn, host, uint16(portNum)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (d *socks5Dialer) handshake(ctx context.Context, conn net.Conn, host string, port uint16) error {
	deadline := time.Now().Add(10 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}

	// Abort blocked I/O promptly if context is cancelled.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
	}()

	// 1. Method negotiation.
	var greeting []byte
	if d.hasAuth {
		greeting = []byte{0x05, 0x02, 0x00, 0x02}
	} else {
		greeting = []byte{0x05, 0x01, 0x00}
	}
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("syncer: socks5 greeting write: %w", err)
	}

	var methodResp [2]byte
	if _, err := io.ReadFull(conn, methodResp[:]); err != nil {
		return fmt.Errorf("syncer: socks5 greeting read: %w", err)
	}
	if methodResp[0] != 0x05 {
		return fmt.Errorf("syncer: unexpected socks version %d", methodResp[0])
	}

	switch methodResp[1] {
	case 0x00:
		// No authentication required.
	case 0x02:
		if !d.hasAuth {
			return errors.New("syncer: socks5 proxy requires authentication")
		}
		authBuf := make([]byte, 0, 3+len(d.username)+len(d.password))
		authBuf = append(authBuf, 0x01, byte(len(d.username)))
		authBuf = append(authBuf, d.username...)
		authBuf = append(authBuf, byte(len(d.password)))
		authBuf = append(authBuf, d.password...)
		if _, err := conn.Write(authBuf); err != nil {
			return fmt.Errorf("syncer: socks5 auth write: %w", err)
		}
		var authResp [2]byte
		if _, err := io.ReadFull(conn, authResp[:]); err != nil {
			return fmt.Errorf("syncer: socks5 auth read: %w", err)
		}
		if authResp[1] != 0x00 {
			return errors.New("syncer: socks5 authentication failed")
		}
	case 0xFF:
		return errors.New("syncer: socks5 proxy rejected authentication methods")
	default:
		return fmt.Errorf("syncer: unsupported socks5 auth method 0x%02x", methodResp[1])
	}

	// 2. CONNECT request.
	req := make([]byte, 0, 7+len(host))
	req = append(req, 0x05, 0x01, 0x00)
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, 0x01)
			req = append(req, ip4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("syncer: socks5 target domain too long (%d bytes)", len(host))
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], port)
	req = append(req, portBytes[:]...)

	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("syncer: socks5 connect write: %w", err)
	}

	var replyHdr [4]byte
	if _, err := io.ReadFull(conn, replyHdr[:]); err != nil {
		return fmt.Errorf("syncer: socks5 connect read: %w", err)
	}
	if replyHdr[0] != 0x05 {
		return fmt.Errorf("syncer: unexpected socks5 reply version %d", replyHdr[0])
	}
	if replyHdr[1] != 0x00 {
		return fmt.Errorf("syncer: socks5 connect failed with reply code 0x%02x", replyHdr[1])
	}

	var tailLen int
	switch replyHdr[3] {
	case 0x01:
		tailLen = 4 + 2
	case 0x04:
		tailLen = 16 + 2
	case 0x03:
		var domainLen [1]byte
		if _, err := io.ReadFull(conn, domainLen[:]); err != nil {
			return fmt.Errorf("syncer: socks5 reply domain len read: %w", err)
		}
		tailLen = int(domainLen[0]) + 2
	default:
		return fmt.Errorf("syncer: unsupported socks5 reply address type 0x%02x", replyHdr[3])
	}

	discard := make([]byte, tailLen)
	if _, err := io.ReadFull(conn, discard); err != nil {
		return fmt.Errorf("syncer: socks5 reply tail read: %w", err)
	}

	// Clear handshake deadline for subsequent HTTP traffic.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return err
	}
	return nil
}
