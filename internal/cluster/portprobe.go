package cluster

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// LocalPortCheckResult describes whether a TCP or UDP port is actively listening on a node.
type LocalPortCheckResult struct {
	Listening bool   `json:"listening"`
	Protocol  string `json:"protocol"` // "tcp" | "udp"
	Port      int    `json:"port"`
	Detail    string `json:"detail,omitempty"`
}

// ExternalPortProbeRequest specifies a cross-node external reachability probe target.
type ExternalPortProbeRequest struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Network   string `json:"network"`    // "tcp" | "udp_quic" | "udp"
	TimeoutMS int    `json:"timeout_ms"` // default 3500
}

// ExternalPortProbeResult reports the result of an external TCP/QUIC/UDP reachability probe.
type ExternalPortProbeResult struct {
	Reachable bool   `json:"reachable"`
	LatencyMS int64  `json:"latency_ms"`
	Protocol  string `json:"protocol"`
	Detail    string `json:"detail,omitempty"`
}

type localPortCheckReq struct {
	Network string `json:"network"`
	Port    int    `json:"port"`
}

// CheckLocalPort verifies whether `port` is actively bound/listening on the current host.
func CheckLocalPort(ctx context.Context, network string, port int) (LocalPortCheckResult, error) {
	if port < 1 || port > 65535 {
		return LocalPortCheckResult{Port: port}, fmt.Errorf("invalid port %d", port)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	proto := strings.ToLower(strings.TrimSpace(network))
	if proto == "udp_quic" || proto == "udp" {
		return checkLocalUDPPort(ctx, port), nil
	}
	return checkLocalTCPPort(ctx, port), nil
}

func checkLocalTCPPort(ctx context.Context, port int) LocalPortCheckResult {
	res := LocalPortCheckResult{
		Protocol: "tcp",
		Port:     port,
	}
	dialer := &net.Dialer{Timeout: 1200 * time.Millisecond}
	var lastErr error
	for _, host := range []string{"127.0.0.1", "::1"} {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		dialCtx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
		conn, err := dialer.DialContext(dialCtx, "tcp", addr)
		cancel()
		if err == nil {
			_ = conn.Close()
			res.Listening = true
			res.Detail = "tcp listener reachable on " + addr
			return res
		}
		lastErr = err
	}
	if lastErr != nil {
		res.Detail = lastErr.Error()
	}
	return res
}

func checkLocalUDPPort(_ context.Context, port int) LocalPortCheckResult {
	res := LocalPortCheckResult{
		Protocol: "udp",
		Port:     port,
	}

	// Fast, zero-side-effect check via /proc/net/udp and /proc/net/udp6 on Linux.
	if runtime.GOOS == "linux" {
		for _, procFile := range []string{"/proc/net/udp", "/proc/net/udp6"} {
			if found, err := procNetHasUDPPort(procFile, port); err == nil && found {
				res.Listening = true
				res.Detail = "udp socket active in " + procFile
				return res
			}
		}
	}

	// Fallback (non-Linux or restricted /proc): attempt transient bind.
	// Address-in-use means a UDP listener already owns the port; clean bind means nothing is listening.
	pc, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		if isAddrInUseError(err) {
			res.Listening = true
			res.Detail = "udp port bound by active listener"
			return res
		}
		res.Detail = err.Error()
		return res
	}
	_ = pc.Close()
	res.Listening = false
	res.Detail = "no active udp listener bound on port"
	return res
}

func procNetHasUDPPort(path string, port int) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	targetHex := fmt.Sprintf("%04X", port)
	scanner := bufio.NewScanner(f)
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if first {
			first = false
			continue
		}
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		localAddr := fields[1]
		idx := strings.LastIndexByte(localAddr, ':')
		if idx < 0 || idx+1 >= len(localAddr) {
			continue
		}
		if strings.EqualFold(localAddr[idx+1:], targetHex) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func isAddrInUseError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address") ||
		strings.Contains(msg, "bind: permission denied")
}

// ProbeExternalPort probes a remote host:port from the current node over TCP, QUIC UDP, or raw UDP.
func ProbeExternalPort(ctx context.Context, req ExternalPortProbeRequest) (ExternalPortProbeResult, error) {
	host := strings.TrimSpace(req.Host)
	if host == "" {
		return ExternalPortProbeResult{}, errors.New("host is required")
	}
	if req.Port < 1 || req.Port > 65535 {
		return ExternalPortProbeResult{}, fmt.Errorf("invalid port %d", req.Port)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	timeoutMS := req.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 3500
	}
	if timeoutMS > 10000 {
		timeoutMS = 10000
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond

	network := strings.ToLower(strings.TrimSpace(req.Network))
	if network == "" {
		network = "tcp"
	}

	switch network {
	case "udp_quic":
		return probeExternalQUIC(ctx, host, req.Port, timeout), nil
	case "udp":
		return probeExternalRawUDP(ctx, host, req.Port, timeout), nil
	default:
		return probeExternalTCP(ctx, host, req.Port, timeout), nil
	}
}

func probeExternalTCP(ctx context.Context, host string, port int, timeout time.Duration) ExternalPortProbeResult {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(dialCtx, "tcp", addr)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return ExternalPortProbeResult{
			Reachable: false,
			LatencyMS: lat,
			Protocol:  "tcp",
			Detail:    err.Error(),
		}
	}
	_ = conn.Close()
	if lat < 1 {
		lat = 1
	}
	return ExternalPortProbeResult{
		Reachable: true,
		LatencyMS: lat,
		Protocol:  "tcp",
		Detail:    "tcp handshake succeeded",
	}
}

// buildQUICVersionProbe constructs a 1200-byte RFC 9000 Long Header packet using reserved
// version 0x0a0a0a0a so any compliant QUIC server (Hysteria2, TUIC v5, etc.) immediately
// responds with a Version Negotiation packet, confirming UDP firewall traversal end-to-end.
func buildQUICVersionProbe() []byte {
	pkt := make([]byte, 1200)
	pkt[0] = 0xc0 // Long Header (0x80) | Fixed Bit (0x40) | Initial type (0x00)
	binary.BigEndian.PutUint32(pkt[1:5], 0x0a0a0a0a)
	pkt[5] = 8 // DCID length
	_, _ = rand.Read(pkt[6:14])
	pkt[14] = 8 // SCID length
	_, _ = rand.Read(pkt[15:23])
	return pkt
}

func probeExternalQUIC(ctx context.Context, host string, port int, timeout time.Duration) ExternalPortProbeResult {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(dialCtx, "udp", addr)
	if err != nil {
		return ExternalPortProbeResult{
			Reachable: false,
			LatencyMS: time.Since(start).Milliseconds(),
			Protocol:  "udp_quic",
			Detail:    err.Error(),
		}
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)

	pkt := buildQUICVersionProbe()
	if _, err := conn.Write(pkt); err != nil {
		return ExternalPortProbeResult{
			Reachable: false,
			LatencyMS: time.Since(start).Milliseconds(),
			Protocol:  "udp_quic",
			Detail:    err.Error(),
		}
	}

	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	lat := time.Since(start).Milliseconds()
	if err != nil || n == 0 {
		detail := "timeout waiting for QUIC UDP response"
		if err != nil {
			detail = err.Error()
		}
		return ExternalPortProbeResult{
			Reachable: false,
			LatencyMS: lat,
			Protocol:  "udp_quic",
			Detail:    detail,
		}
	}
	if lat < 1 {
		lat = 1
	}
	return ExternalPortProbeResult{
		Reachable: true,
		LatencyMS: lat,
		Protocol:  "udp_quic",
		Detail:    fmt.Sprintf("received %d-byte QUIC UDP reply", n),
	}
}

func probeExternalRawUDP(ctx context.Context, host string, port int, timeout time.Duration) ExternalPortProbeResult {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(dialCtx, "udp", addr)
	if err != nil {
		return ExternalPortProbeResult{
			Reachable: false,
			LatencyMS: time.Since(start).Milliseconds(),
			Protocol:  "udp",
			Detail:    err.Error(),
		}
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write([]byte("SBOXPRB1")); err != nil {
		return ExternalPortProbeResult{
			Reachable: false,
			LatencyMS: time.Since(start).Milliseconds(),
			Protocol:  "udp",
			Detail:    err.Error(),
		}
	}

	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	lat := time.Since(start).Milliseconds()
	if err != nil || n == 0 {
		return ExternalPortProbeResult{
			Reachable: false,
			LatencyMS: lat,
			Protocol:  "udp",
			Detail:    "no UDP reply (raw UDP listeners may silently drop unknown datagrams)",
		}
	}
	if lat < 1 {
		lat = 1
	}
	return ExternalPortProbeResult{
		Reachable: true,
		LatencyMS: lat,
		Protocol:  "udp",
		Detail:    fmt.Sprintf("received %d-byte UDP reply", n),
	}
}
