package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/model"
)

var (
	ipv4EchoEndpoints = []string{
		"https://api.ipify.org",
		"https://ipv4.icanhazip.com",
		"https://ifconfig.me/ip",
	}
	ipv6EchoEndpoints = []string{
		"https://api6.ipify.org",
		"https://ipv6.icanhazip.com",
	}
)

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		// Reject RFC 6598 CGNAT (100.64.0.0/10)
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return false
		}
	}
	return true
}

func newIPDetectClient(network, proxyURL string, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 0,
	}
	tr := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
	}
	if p := strings.TrimSpace(proxyURL); p != "" {
		if pu, err := url.Parse(p); err == nil && (pu.Scheme == "http" || pu.Scheme == "https") {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}
}

func probeEchoEndpoints(ctx context.Context, endpoints []string, wantV6 bool, proxyURL string) string {
	network := "tcp4"
	if wantV6 {
		network = "tcp6"
	}
	client := newIPDetectClient(network, proxyURL, 4*time.Second)

	for _, ep := range endpoints {
		if ctx.Err() != nil {
			return ""
		}
		reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ep, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("User-Agent", "sbox-panel/1.0")
		req.Header.Set("Accept", "text/plain")

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 128))
		_ = resp.Body.Close()
		cancel()
		if readErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}

		candidate := strings.TrimSpace(string(raw))
		ip := net.ParseIP(candidate)
		if !isPublicIP(ip) {
			continue
		}
		if wantV6 {
			if ip.To4() == nil {
				return ip.String()
			}
		} else {
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return ""
}

// DetectPublicIPs concurrently probes public IPv4 and IPv6 echo services with a 4-second per-request timeout,
// rejecting loopback, private, link-local, and CGNAT addresses.
func DetectPublicIPs(ctx context.Context, proxyURL string) (ipv4, ipv6 string) {
	if ctx == nil {
		ctx = context.Background()
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ipv4 = probeEchoEndpoints(ctx, ipv4EchoEndpoints, false, proxyURL)
	}()
	go func() {
		defer wg.Done()
		ipv6 = probeEchoEndpoints(ctx, ipv6EchoEndpoints, true, proxyURL)
	}()
	wg.Wait()
	return ipv4, ipv6
}

// AutoPopulateLocalPublicIP checks whether the local master node (id=1) is missing public_ipv4;
// if empty, it detects public IPv4/IPv6 and persists them into both `nodes` (id=1) and `settings`.
func (s *Server) AutoPopulateLocalPublicIP(ctx context.Context) {
	if s.store == nil || s.store.DB() == nil {
		return
	}
	local, err := s.store.GetNode(ctx, 1)
	if err != nil {
		return
	}
	ps, _ := s.store.GetPanelSettings(ctx)

	curV4 := strings.TrimSpace(local.PublicIPv4)
	if curV4 == "" {
		curV4 = strings.TrimSpace(ps.PublicIPv4)
	}
	curV6 := strings.TrimSpace(local.PublicIPv6)
	if curV6 == "" {
		curV6 = strings.TrimSpace(ps.PublicIPv6)
	}
	if curV4 != "" {
		// Ensure node id=1 and settings stay aligned if one had it and the other was empty.
		if strings.TrimSpace(local.PublicIPv4) == "" {
			local.PublicIPv4 = curV4
			if local.PublicIPv6 == "" && curV6 != "" {
				local.PublicIPv6 = curV6
			}
			_ = s.store.UpdateNode(ctx, local)
		}
		return
	}

	detectCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	detectedV4, detectedV6 := DetectPublicIPs(detectCtx, ps.GithubProxy)
	if detectedV4 == "" && (curV6 != "" || detectedV6 == "") {
		return
	}

	updates := map[string]string{}
	changed := false
	if detectedV4 != "" && local.PublicIPv4 == "" {
		local.PublicIPv4 = detectedV4
		updates[model.SettingPublicIPv4] = detectedV4
		changed = true
	}
	if detectedV6 != "" && local.PublicIPv6 == "" && curV6 == "" {
		local.PublicIPv6 = detectedV6
		updates[model.SettingPublicIPv6] = detectedV6
		changed = true
	}
	if !changed {
		return
	}

	if len(updates) > 0 {
		_ = s.store.SetSettings(ctx, updates)
	}
	_ = s.store.UpdateNode(ctx, local)
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", "auto-detected local public IP: ipv4="+local.PublicIPv4+" ipv6="+local.PublicIPv6)
	}
	s.TriggerAutoSync()
}

// handleToolDetectIP serves POST /api/v1/tools/detect-ip ({ save?: bool, proxy_url?: string }).
func (s *Server) handleToolDetectIP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Save     bool   `json:"save"`
		ProxyURL string `json:"proxy_url"`
	}
	if r.Body != nil {
		if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	proxyURL := strings.TrimSpace(req.ProxyURL)
	if proxyURL == "" {
		proxyURL, _ = s.store.GetSettingDefault(ctx, model.SettingGithubProxy, "")
	}

	detectCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ipv4, ipv6 := DetectPublicIPs(detectCtx, proxyURL)

	if req.Save && (ipv4 != "" || ipv6 != "") {
		updates := map[string]string{}
		if ipv4 != "" {
			updates[model.SettingPublicIPv4] = ipv4
		}
		if ipv6 != "" {
			updates[model.SettingPublicIPv6] = ipv6
		}
		if len(updates) > 0 {
			_ = s.store.SetSettings(ctx, updates)
		}
		if local, err := s.store.GetNode(ctx, 1); err == nil {
			if ipv4 != "" {
				local.PublicIPv4 = ipv4
			}
			if ipv6 != "" {
				local.PublicIPv6 = ipv6
			}
			_ = s.store.UpdateNode(ctx, local)
		}
		s.TriggerAutoSync()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"public_ipv4": ipv4,
		"public_ipv6": ipv6,
	})
}
