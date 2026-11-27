package subgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	geositeCNRuleSetURL = "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs"
	geoipCNRuleSetURL   = "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs"
	urlTestEndpoint     = "https://www.gstatic.com/generate_204"
)

func renderSingbox(nodes []resolvedNode) (string, error) {
	proxyTags := make([]string, 0, len(nodes))
	renderedOutbounds := make([]any, 0, len(nodes)+4)

	// Reserve tag names to guarantee helper shadowtls outbound tags never collide.
	usedTags := map[string]struct{}{
		"proxy":  {},
		"auto":   {},
		"direct": {},
	}
	for _, rn := range nodes {
		usedTags[rn.Name] = struct{}{}
	}
	allocHelperTag := func(base string) string {
		cand := base + "-stls"
		if _, exists := usedTags[cand]; !exists {
			usedTags[cand] = struct{}{}
			return cand
		}
		for i := 2; ; i++ {
			c := fmt.Sprintf("%s-stls-%d", base, i)
			if _, exists := usedTags[c]; !exists {
				usedTags[c] = struct{}{}
				return c
			}
		}
	}

	for _, rn := range nodes {
		obs := buildSingboxOutbounds(rn, allocHelperTag)
		if len(obs) == 0 {
			continue
		}
		proxyTags = append(proxyTags, rn.Name)
		for _, ob := range obs {
			renderedOutbounds = append(renderedOutbounds, ob)
		}
	}

	outbounds := make([]any, 0, len(renderedOutbounds)+3)
	if len(proxyTags) > 0 {
		selectorMembers := make([]string, 0, len(proxyTags)+2)
		selectorMembers = append(selectorMembers, "auto")
		selectorMembers = append(selectorMembers, proxyTags...)
		selectorMembers = append(selectorMembers, "direct")

		outbounds = append(outbounds, map[string]any{
			"type":      "selector",
			"tag":       "proxy",
			"outbounds": selectorMembers,
			"default":   "auto",
		})
		outbounds = append(outbounds, map[string]any{
			"type":      "urltest",
			"tag":       "auto",
			"outbounds": proxyTags,
			"url":       urlTestEndpoint,
			"interval":  "5m",
			"tolerance": 50,
		})
	} else {
		outbounds = append(outbounds, map[string]any{
			"type":      "selector",
			"tag":       "proxy",
			"outbounds": []string{"direct"},
			"default":   "direct",
		})
	}

	outbounds = append(outbounds, renderedOutbounds...)
	outbounds = append(outbounds, map[string]any{
		"type": "direct",
		"tag":  "direct",
	})

	doc := map[string]any{
		"log": map[string]any{
			"level":     "info",
			"timestamp": true,
		},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{
					"tag":    "dns-remote",
					"type":   "https",
					"server": "1.1.1.1",
					"detour": "proxy",
				},
				map[string]any{
					"tag":    "dns-direct",
					"type":   "https",
					"server": "223.5.5.5",
				},
				map[string]any{
					"tag":  "dns-local",
					"type": "local",
				},
			},
			"rules": []any{
				map[string]any{
					"rule_set": "geosite-cn",
					"server":   "dns-direct",
				},
			},
			"final": "dns-remote",
		},
		"inbounds": []any{
			map[string]any{
				"type":        "mixed",
				"tag":         "mixed-in",
				"listen":      "127.0.0.1",
				"listen_port": 7890,
			},
		},
		"outbounds": outbounds,
		"route": map[string]any{
			"rules": []any{
				map[string]any{
					"action": "sniff",
				},
				map[string]any{
					"protocol": "dns",
					"action":   "hijack-dns",
				},
				map[string]any{
					"ip_is_private": true,
					"outbound":      "direct",
				},
				map[string]any{
					"rule_set": []string{"geosite-cn", "geoip-cn"},
					"outbound": "direct",
				},
			},
			"rule_set": []any{
				map[string]any{
					"tag":             "geosite-cn",
					"type":            "remote",
					"format":          "binary",
					"url":             geositeCNRuleSetURL,
					"download_detour": "proxy",
				},
				map[string]any{
					"tag":             "geoip-cn",
					"type":            "remote",
					"format":          "binary",
					"url":             geoipCNRuleSetURL,
					"download_detour": "proxy",
				},
			},
			"final":                   "proxy",
			"auto_detect_interface":   true,
			"default_domain_resolver": "dns-direct",
		},
		"experimental": map[string]any{
			"cache_file": map[string]any{
				"enabled": true,
			},
		},
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return "", fmt.Errorf("subgen: marshal sing-box config: %w", err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func buildSingboxOutbounds(rn resolvedNode, allocHelperTag func(string) string) []map[string]any {
	switch rn.Protocol {
	case "vless":
		return []map[string]any{buildSingboxVLESS(rn)}
	case "hysteria2":
		return []map[string]any{buildSingboxHysteria2(rn)}
	case "tuic":
		return []map[string]any{buildSingboxTUIC(rn)}
	case "anytls":
		return []map[string]any{buildSingboxAnyTLS(rn)}
	case "shadowsocks":
		return []map[string]any{buildSingboxShadowsocks(rn)}
	case "trojan":
		return []map[string]any{buildSingboxTrojan(rn)}
	case "shadowtls":
		return buildSingboxShadowTLSPair(rn, allocHelperTag)
	case "snell":
		return []map[string]any{buildSingboxSnell(rn)}
	case "socks", "mixed":
		return []map[string]any{buildSingboxSocks(rn)}
	case "http":
		return []map[string]any{buildSingboxHTTP(rn)}
	default:
		return nil
	}
}

func buildSingboxVLESS(rn resolvedNode) map[string]any {
	s := rn.vless
	ob := map[string]any{
		"type":        "vless",
		"tag":         rn.Name,
		"server":      rn.Host,
		"server_port": rn.Port,
		"uuid":        strings.TrimSpace(rn.Member.UUID),
	}
	if flow := applyVLESSFlow(s); flow != "" {
		ob["flow"] = flow
	}

	switch s.TLSMode {
	case "reality":
		sni := strings.TrimSpace(s.ServerName)
		if sni == "" {
			sni = strings.TrimSpace(s.RealityHandshakeServer)
		}
		realityObj := map[string]any{
			"enabled":    true,
			"public_key": strings.TrimSpace(s.RealityPublicKey),
		}
		if sid := firstShortID(s.RealityShortIDs); sid != "" {
			realityObj["short_id"] = sid
		}
		tlsObj := map[string]any{
			"enabled": true,
			"utls": map[string]any{
				"enabled":     true,
				"fingerprint": "chrome",
			},
			"reality": realityObj,
		}
		if sni != "" {
			tlsObj["server_name"] = sni
		}
		ob["tls"] = tlsObj
	case "tls":
		tlsObj := map[string]any{
			"enabled":     true,
			"server_name": resolveSNI(rn, s.ServerName),
			"utls": map[string]any{
				"enabled":     true,
				"fingerprint": "chrome",
			},
		}
		if rn.CertSelfSigned {
			tlsObj["insecure"] = true
		}
		ob["tls"] = tlsObj
	}

	if tr := buildSingboxTransport(s.Transport, s.WSPath, s.WSHost, s.GRPCServiceName); tr != nil {
		ob["transport"] = tr
	}
	return ob
}

func buildSingboxHysteria2(rn resolvedNode) map[string]any {
	s := rn.hy2
	tlsObj := map[string]any{
		"enabled":     true,
		"server_name": resolveSNI(rn, ""),
	}
	if rn.CertSelfSigned {
		tlsObj["insecure"] = true
	}

	ob := map[string]any{
		"type":        "hysteria2",
		"tag":         rn.Name,
		"server":      rn.Host,
		"server_port": rn.Port,
		"password":    strings.TrimSpace(rn.Member.Password),
		"tls":         tlsObj,
	}
	if ports := normalizePortRanges(s.ServerPorts, ":"); len(ports) > 0 {
		ob["server_ports"] = ports
	}
	if s.UpMbps > 0 {
		ob["up_mbps"] = s.UpMbps
	}
	if s.DownMbps > 0 {
		ob["down_mbps"] = s.DownMbps
	}
	if obfsPw := strings.TrimSpace(s.ObfsPassword); obfsPw != "" {
		ob["obfs"] = map[string]any{
			"type":     "salamander",
			"password": obfsPw,
		}
	}
	return ob
}

func buildSingboxTUIC(rn resolvedNode) map[string]any {
	s := rn.tuic
	tlsObj := map[string]any{
		"enabled":     true,
		"server_name": resolveSNI(rn, ""),
		"alpn":        []string{"h3"},
	}
	if rn.CertSelfSigned {
		tlsObj["insecure"] = true
	}

	ob := map[string]any{
		"type":               "tuic",
		"tag":                rn.Name,
		"server":             rn.Host,
		"server_port":        rn.Port,
		"uuid":               strings.TrimSpace(rn.Member.UUID),
		"password":           strings.TrimSpace(rn.Member.Password),
		"congestion_control": s.CongestionControl,
		"udp_relay_mode":     "native",
		"tls":                tlsObj,
	}
	if s.ZeroRTTHandshake {
		ob["zero_rtt_handshake"] = true
	}
	if hb := strings.TrimSpace(s.Heartbeat); hb != "" {
		ob["heartbeat"] = hb
	}
	return ob
}

func buildSingboxAnyTLS(rn resolvedNode) map[string]any {
	tlsObj := map[string]any{
		"enabled":     true,
		"server_name": resolveSNI(rn, ""),
	}
	if rn.CertSelfSigned {
		tlsObj["insecure"] = true
	}

	return map[string]any{
		"type":        "anytls",
		"tag":         rn.Name,
		"server":      rn.Host,
		"server_port": rn.Port,
		"password":    strings.TrimSpace(rn.Member.Password),
		"tls":         tlsObj,
	}
}

func buildSingboxShadowsocks(rn resolvedNode) map[string]any {
	ob := map[string]any{
		"type":        "shadowsocks",
		"tag":         rn.Name,
		"server":      rn.Host,
		"server_port": rn.Port,
		"method":      rn.ss.Method,
		"password":    resolveSSPassword(rn.ss.Method, rn.ss.ServerPassword, rn.Member),
	}
	if rn.ss.Network == "tcp" || rn.ss.Network == "udp" {
		ob["network"] = rn.ss.Network
	}
	return ob
}

func buildSingboxTrojan(rn resolvedNode) map[string]any {
	s := rn.trojan
	tlsObj := map[string]any{
		"enabled":     true,
		"server_name": resolveSNI(rn, s.ServerName),
	}
	if rn.CertSelfSigned {
		tlsObj["insecure"] = true
	}

	ob := map[string]any{
		"type":        "trojan",
		"tag":         rn.Name,
		"server":      rn.Host,
		"server_port": rn.Port,
		"password":    strings.TrimSpace(rn.Member.Password),
		"tls":         tlsObj,
	}
	if tr := buildSingboxTransport(s.Transport, s.WSPath, s.WSHost, s.GRPCServiceName); tr != nil {
		ob["transport"] = tr
	}
	return ob
}

func buildSingboxShadowTLSPair(rn resolvedNode, allocHelperTag func(string) string) []map[string]any {
	s := rn.shadowtls
	stlsTag := allocHelperTag(rn.Name)
	hsServer := strings.TrimSpace(s.HandshakeServer)
	if hsServer == "" {
		hsServer = rn.Host
	}

	outer := map[string]any{
		"type":        "shadowtls",
		"tag":         stlsTag,
		"server":      rn.Host,
		"server_port": rn.Port,
		"version":     3,
		"password":    strings.TrimSpace(rn.Member.Password),
		"tls": map[string]any{
			"enabled":     true,
			"server_name": hsServer,
			"utls": map[string]any{
				"enabled":     true,
				"fingerprint": "chrome",
			},
		},
	}

	var inner map[string]any
	if s.DetourProtocol == "vless" {
		inner = map[string]any{
			"type":   "vless",
			"tag":    rn.Name,
			"detour": stlsTag,
			"uuid":   strings.TrimSpace(rn.Member.UUID),
		}
	} else {
		inner = map[string]any{
			"type":     "shadowsocks",
			"tag":      rn.Name,
			"detour":   stlsTag,
			"method":   s.SSMethod,
			"password": resolveSSPassword(s.SSMethod, s.SSServerPassword, rn.Member),
		}
	}

	return []map[string]any{inner, outer}
}

func buildSingboxSnell(rn resolvedNode) map[string]any {
	s := rn.snell
	ob := map[string]any{
		"type":        "snell",
		"tag":         rn.Name,
		"server":      rn.Host,
		"server_port": rn.Port,
		"version":     5,
		"psk":         strings.TrimSpace(s.PSK),
	}
	if s.ObfsMode == "http" {
		ob["obfs_mode"] = "http"
	}
	return ob
}

func buildSingboxSocks(rn resolvedNode) map[string]any {
	ob := map[string]any{
		"type":        "socks",
		"tag":         rn.Name,
		"server":      rn.Host,
		"server_port": rn.Port,
		"version":     "5",
	}
	if rn.local.AuthEnabled && strings.TrimSpace(rn.Member.Name) != "" {
		ob["username"] = strings.TrimSpace(rn.Member.Name)
		ob["password"] = strings.TrimSpace(rn.Member.Password)
	}
	return ob
}

func buildSingboxHTTP(rn resolvedNode) map[string]any {
	ob := map[string]any{
		"type":        "http",
		"tag":         rn.Name,
		"server":      rn.Host,
		"server_port": rn.Port,
	}
	if rn.local.AuthEnabled && strings.TrimSpace(rn.Member.Name) != "" {
		ob["username"] = strings.TrimSpace(rn.Member.Name)
		ob["password"] = strings.TrimSpace(rn.Member.Password)
	}
	return ob
}

func buildSingboxTransport(transport, wsPath, wsHost, grpcSvc string) map[string]any {
	switch transport {
	case "ws":
		p := strings.TrimSpace(wsPath)
		if p == "" {
			p = "/"
		}
		tr := map[string]any{
			"type": "ws",
			"path": p,
		}
		if h := strings.TrimSpace(wsHost); h != "" {
			tr["headers"] = map[string]any{"Host": h}
		}
		return tr
	case "httpupgrade":
		p := strings.TrimSpace(wsPath)
		if p == "" {
			p = "/"
		}
		tr := map[string]any{
			"type": "httpupgrade",
			"path": p,
		}
		if h := strings.TrimSpace(wsHost); h != "" {
			tr["host"] = h
		}
		return tr
	case "grpc":
		svc := strings.TrimSpace(grpcSvc)
		if svc == "" {
			svc = "grpc"
		}
		return map[string]any{
			"type":         "grpc",
			"service_name": svc,
		}
	default:
		return nil
	}
}
