package subgen

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type clashConfig struct {
	MixedPort   int                `yaml:"mixed-port"`
	AllowLan    bool               `yaml:"allow-lan"`
	Mode        string             `yaml:"mode"`
	LogLevel    string             `yaml:"log-level"`
	IPv6        bool               `yaml:"ipv6"`
	Proxies     []map[string]any   `yaml:"proxies"`
	ProxyGroups []clashProxyGroup  `yaml:"proxy-groups"`
	Rules       []string           `yaml:"rules"`
}

type clashProxyGroup struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Proxies   []string `yaml:"proxies"`
	URL       string   `yaml:"url,omitempty"`
	Interval  int      `yaml:"interval,omitempty"`
	Tolerance int      `yaml:"tolerance,omitempty"`
}

func renderClash(nodes []resolvedNode) (string, error) {
	proxies := make([]map[string]any, 0, len(nodes))
	proxyNames := make([]string, 0, len(nodes))

	for _, rn := range nodes {
		p := buildClashProxy(rn)
		if p == nil {
			continue
		}
		proxies = append(proxies, p)
		proxyNames = append(proxyNames, rn.Name)
	}

	var selectProxies []string
	var autoProxies []string
	if len(proxyNames) > 0 {
		selectProxies = make([]string, 0, len(proxyNames)+2)
		selectProxies = append(selectProxies, "♻️ 自动选择")
		selectProxies = append(selectProxies, proxyNames...)
		selectProxies = append(selectProxies, "DIRECT")

		autoProxies = append([]string(nil), proxyNames...)
	} else {
		selectProxies = []string{"DIRECT"}
		autoProxies = []string{"DIRECT"}
	}

	cfg := clashConfig{
		MixedPort: 7890,
		AllowLan:  false,
		Mode:      "rule",
		LogLevel:  "info",
		IPv6:      true,
		Proxies:   proxies,
		ProxyGroups: []clashProxyGroup{
			{
				Name:    "🚀 节点选择",
				Type:    "select",
				Proxies: selectProxies,
			},
			{
				Name:      "♻️ 自动选择",
				Type:      "url-test",
				Proxies:   autoProxies,
				URL:       urlTestEndpoint,
				Interval:  300,
				Tolerance: 50,
			},
		},
		Rules: []string{
			"GEOIP,LAN,DIRECT,no-resolve",
			"GEOIP,CN,DIRECT,no-resolve",
			"MATCH,🚀 节点选择",
		},
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return "", fmt.Errorf("subgen: marshal clash yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("subgen: close clash yaml encoder: %w", err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func buildClashProxy(rn resolvedNode) map[string]any {
	switch rn.Protocol {
	case "vless":
		return buildClashVLESS(rn)
	case "hysteria2":
		return buildClashHysteria2(rn)
	case "tuic":
		return buildClashTUIC(rn)
	case "anytls":
		return buildClashAnyTLS(rn)
	case "shadowsocks":
		return buildClashShadowsocks(rn)
	case "trojan":
		return buildClashTrojan(rn)
	case "shadowtls":
		return buildClashShadowTLS(rn)
	case "socks", "mixed":
		return buildClashSocks(rn)
	case "http":
		return buildClashHTTP(rn)
	default:
		return nil
	}
}

func buildClashVLESS(rn resolvedNode) map[string]any {
	s := rn.vless
	p := map[string]any{
		"name":   rn.Name,
		"type":   "vless",
		"server": rn.Host,
		"port":   rn.Port,
		"uuid":   strings.TrimSpace(rn.Member.UUID),
		"udp":    true,
	}

	switch s.TLSMode {
	case "reality":
		p["tls"] = true
		p["client-fingerprint"] = "chrome"
		sni := strings.TrimSpace(s.ServerName)
		if sni == "" {
			sni = strings.TrimSpace(s.RealityHandshakeServer)
		}
		if sni != "" {
			p["servername"] = sni
		}
		ro := map[string]any{
			"public-key": strings.TrimSpace(s.RealityPublicKey),
		}
		if sid := firstShortID(s.RealityShortIDs); sid != "" {
			ro["short-id"] = sid
		}
		p["reality-opts"] = ro
	case "tls":
		p["tls"] = true
		p["client-fingerprint"] = "chrome"
		p["servername"] = resolveSNI(rn, s.ServerName)
		if rn.CertSelfSigned {
			p["skip-cert-verify"] = true
		}
	default:
		p["tls"] = false
	}

	if flow := applyVLESSFlow(s); flow != "" {
		p["flow"] = flow
	}

	addClashTransport(p, s.Transport, s.WSPath, s.WSHost, s.GRPCServiceName)
	return p
}

func buildClashHysteria2(rn resolvedNode) map[string]any {
	s := rn.hy2
	p := map[string]any{
		"name":     rn.Name,
		"type":     "hysteria2",
		"server":   rn.Host,
		"port":     rn.Port,
		"password": strings.TrimSpace(rn.Member.Password),
		"sni":      resolveSNI(rn, ""),
	}
	if ports := normalizePortRanges(s.ServerPorts, "-"); len(ports) > 0 {
		p["ports"] = strings.Join(ports, ",")
	}
	if s.UpMbps > 0 {
		p["up"] = fmt.Sprintf("%d Mbps", s.UpMbps)
	}
	if s.DownMbps > 0 {
		p["down"] = fmt.Sprintf("%d Mbps", s.DownMbps)
	}
	if obfsPw := strings.TrimSpace(s.ObfsPassword); obfsPw != "" {
		p["obfs"] = "salamander"
		p["obfs-password"] = obfsPw
	}
	if rn.CertSelfSigned {
		p["skip-cert-verify"] = true
	}
	if rn.CertFingerprint != "" {
		p["fingerprint"] = rn.CertFingerprint
	}
	return p
}

func buildClashTUIC(rn resolvedNode) map[string]any {
	s := rn.tuic
	p := map[string]any{
		"name":                  rn.Name,
		"type":                  "tuic",
		"server":                rn.Host,
		"port":                  rn.Port,
		"uuid":                  strings.TrimSpace(rn.Member.UUID),
		"password":              strings.TrimSpace(rn.Member.Password),
		"congestion-controller": s.CongestionControl,
		"udp-relay-mode":        "native",
		"alpn":                  []string{"h3"},
		"sni":                   resolveSNI(rn, ""),
	}
	if s.ZeroRTTHandshake {
		p["reduce-rtt"] = true
	}
	if rn.CertSelfSigned {
		p["skip-cert-verify"] = true
	}
	return p
}

func buildClashAnyTLS(rn resolvedNode) map[string]any {
	p := map[string]any{
		"name":     rn.Name,
		"type":     "anytls",
		"server":   rn.Host,
		"port":     rn.Port,
		"password": strings.TrimSpace(rn.Member.Password),
		"sni":      resolveSNI(rn, ""),
		"udp":      true,
	}
	if rn.CertSelfSigned {
		p["skip-cert-verify"] = true
	}
	return p
}

func buildClashShadowsocks(rn resolvedNode) map[string]any {
	p := map[string]any{
		"name":     rn.Name,
		"type":     "ss",
		"server":   rn.Host,
		"port":     rn.Port,
		"cipher":   rn.ss.Method,
		"password": resolveSSPassword(rn.ss.Method, rn.ss.ServerPassword, rn.Member),
		"udp":      rn.ss.Network != "tcp",
	}
	return p
}

func buildClashTrojan(rn resolvedNode) map[string]any {
	s := rn.trojan
	p := map[string]any{
		"name":     rn.Name,
		"type":     "trojan",
		"server":   rn.Host,
		"port":     rn.Port,
		"password": strings.TrimSpace(rn.Member.Password),
		"sni":      resolveSNI(rn, s.ServerName),
		"udp":      true,
	}
	if rn.CertSelfSigned {
		p["skip-cert-verify"] = true
	}
	addClashTransport(p, s.Transport, s.WSPath, s.WSHost, s.GRPCServiceName)
	return p
}

func buildClashShadowTLS(rn resolvedNode) map[string]any {
	s := rn.shadowtls
	hsServer := strings.TrimSpace(s.HandshakeServer)
	if hsServer == "" {
		hsServer = rn.Host
	}

	if s.DetourProtocol == "vless" {
		return map[string]any{
			"name":       rn.Name,
			"type":       "vless",
			"server":     rn.Host,
			"port":       rn.Port,
			"uuid":       strings.TrimSpace(rn.Member.UUID),
			"udp":        true,
			"tls":        true,
			"servername": hsServer,
			"plugin":     "shadow-tls",
			"plugin-opts": map[string]any{
				"host":     hsServer,
				"password": strings.TrimSpace(rn.Member.Password),
				"version":  3,
			},
		}
	}

	return map[string]any{
		"name":     rn.Name,
		"type":     "ss",
		"server":   rn.Host,
		"port":     rn.Port,
		"cipher":   s.SSMethod,
		"password": resolveSSPassword(s.SSMethod, s.SSServerPassword, rn.Member),
		"udp":      true,
		"plugin":   "shadow-tls",
		"plugin-opts": map[string]any{
			"host":     hsServer,
			"password": strings.TrimSpace(rn.Member.Password),
			"version":  3,
		},
	}
}

func buildClashSocks(rn resolvedNode) map[string]any {
	p := map[string]any{
		"name":   rn.Name,
		"type":   "socks5",
		"server": rn.Host,
		"port":   rn.Port,
		"udp":    true,
	}
	if rn.local.AuthEnabled && strings.TrimSpace(rn.Member.Name) != "" {
		p["username"] = strings.TrimSpace(rn.Member.Name)
		p["password"] = strings.TrimSpace(rn.Member.Password)
	}
	return p
}

func buildClashHTTP(rn resolvedNode) map[string]any {
	p := map[string]any{
		"name":   rn.Name,
		"type":   "http",
		"server": rn.Host,
		"port":   rn.Port,
	}
	if rn.local.AuthEnabled && strings.TrimSpace(rn.Member.Name) != "" {
		p["username"] = strings.TrimSpace(rn.Member.Name)
		p["password"] = strings.TrimSpace(rn.Member.Password)
	}
	return p
}

func addClashTransport(p map[string]any, transport, wsPath, wsHost, grpcSvc string) {
	switch transport {
	case "ws":
		p["network"] = "ws"
		path := strings.TrimSpace(wsPath)
		if path == "" {
			path = "/"
		}
		opts := map[string]any{"path": path}
		if h := strings.TrimSpace(wsHost); h != "" {
			opts["headers"] = map[string]any{"Host": h}
		}
		p["ws-opts"] = opts
	case "httpupgrade":
		p["network"] = "ws"
		path := strings.TrimSpace(wsPath)
		if path == "" {
			path = "/"
		}
		opts := map[string]any{
			"path":               path,
			"v2ray-http-upgrade": true,
		}
		if h := strings.TrimSpace(wsHost); h != "" {
			opts["headers"] = map[string]any{"Host": h}
		}
		p["ws-opts"] = opts
	case "grpc":
		p["network"] = "grpc"
		svc := strings.TrimSpace(grpcSvc)
		if svc == "" {
			svc = "grpc"
		}
		p["grpc-opts"] = map[string]any{
			"grpc-service-name": svc,
		}
	}
}
