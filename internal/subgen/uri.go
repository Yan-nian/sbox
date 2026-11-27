package subgen

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

func renderURI(rn resolvedNode) string {
	switch rn.Protocol {
	case "vless":
		return renderVLESSURI(rn)
	case "hysteria2":
		return renderHysteria2URI(rn)
	case "tuic":
		return renderTUICURI(rn)
	case "anytls":
		return renderAnyTLSURI(rn)
	case "shadowsocks":
		return renderShadowsocksURI(rn)
	case "trojan":
		return renderTrojanURI(rn)
	case "shadowtls":
		return renderShadowTLSURI(rn)
	case "snell":
		return renderSnellURI(rn)
	case "socks", "mixed":
		return renderSocksURI(rn)
	case "http":
		return renderHTTPURI(rn)
	default:
		return ""
	}
}

func renderVLESSURI(rn resolvedNode) string {
	s := rn.vless
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("type", s.Transport)

	switch s.TLSMode {
	case "reality":
		q.Set("security", "reality")
		sni := strings.TrimSpace(s.ServerName)
		if sni == "" {
			sni = strings.TrimSpace(s.RealityHandshakeServer)
		}
		if sni != "" {
			q.Set("sni", sni)
		}
		if pbk := strings.TrimSpace(s.RealityPublicKey); pbk != "" {
			q.Set("pbk", pbk)
		}
		q.Set("fp", "chrome")
		if sid := firstShortID(s.RealityShortIDs); sid != "" {
			q.Set("sid", sid)
		}
	case "tls":
		q.Set("security", "tls")
		if sni := resolveSNI(rn, s.ServerName); sni != "" {
			q.Set("sni", sni)
		}
		q.Set("fp", "chrome")
		if rn.CertSelfSigned {
			q.Set("allowInsecure", "1")
		}
	default:
		q.Set("security", "none")
	}

	if flow := applyVLESSFlow(s); flow != "" {
		q.Set("flow", flow)
	}

	addTransportQueryParams(q, s.Transport, s.WSPath, s.WSHost, s.GRPCServiceName)

	return fmt.Sprintf("vless://%s@%s?%s#%s",
		url.PathEscape(strings.TrimSpace(rn.Member.UUID)),
		formatHostPort(rn.Host, rn.Port),
		q.Encode(),
		url.PathEscape(rn.Name),
	)
}

func renderHysteria2URI(rn resolvedNode) string {
	s := rn.hy2
	q := url.Values{}
	if sni := resolveSNI(rn, ""); sni != "" {
		q.Set("sni", sni)
	}
	if obfsPw := strings.TrimSpace(s.ObfsPassword); obfsPw != "" {
		q.Set("obfs", "salamander")
		q.Set("obfs-password", obfsPw)
	}
	if ports := normalizePortRanges(s.ServerPorts, "-"); len(ports) > 0 {
		q.Set("mport", strings.Join(ports, ","))
	}
	if rn.CertSelfSigned {
		q.Set("insecure", "1")
	}

	return fmt.Sprintf("hysteria2://%s@%s/?%s#%s",
		url.QueryEscape(strings.TrimSpace(rn.Member.Password)),
		formatHostPort(rn.Host, rn.Port),
		q.Encode(),
		url.PathEscape(rn.Name),
	)
}

func renderTUICURI(rn resolvedNode) string {
	s := rn.tuic
	q := url.Values{}
	q.Set("congestion_control", s.CongestionControl)
	q.Set("alpn", "h3")
	q.Set("udp_relay_mode", "native")
	if sni := resolveSNI(rn, ""); sni != "" {
		q.Set("sni", sni)
	}
	if s.ZeroRTTHandshake {
		q.Set("reduce_rtt", "1")
	}
	if rn.CertSelfSigned {
		q.Set("allow_insecure", "1")
	}

	return fmt.Sprintf("tuic://%s:%s@%s?%s#%s",
		url.PathEscape(strings.TrimSpace(rn.Member.UUID)),
		url.QueryEscape(strings.TrimSpace(rn.Member.Password)),
		formatHostPort(rn.Host, rn.Port),
		q.Encode(),
		url.PathEscape(rn.Name),
	)
}

func renderAnyTLSURI(rn resolvedNode) string {
	q := url.Values{}
	if sni := resolveSNI(rn, ""); sni != "" {
		q.Set("sni", sni)
	}
	if rn.CertSelfSigned {
		q.Set("insecure", "1")
	}

	return fmt.Sprintf("anytls://%s@%s/?%s#%s",
		url.QueryEscape(strings.TrimSpace(rn.Member.Password)),
		formatHostPort(rn.Host, rn.Port),
		q.Encode(),
		url.PathEscape(rn.Name),
	)
}

func renderShadowsocksURI(rn resolvedNode) string {
	clientPw := resolveSSPassword(rn.ss.Method, rn.ss.ServerPassword, rn.Member)
	userInfo := base64.StdEncoding.EncodeToString([]byte(rn.ss.Method + ":" + clientPw))
	return fmt.Sprintf("ss://%s@%s#%s",
		userInfo,
		formatHostPort(rn.Host, rn.Port),
		url.PathEscape(rn.Name),
	)
}

func renderTrojanURI(rn resolvedNode) string {
	s := rn.trojan
	q := url.Values{}
	q.Set("security", "tls")
	q.Set("type", s.Transport)
	if sni := resolveSNI(rn, s.ServerName); sni != "" {
		q.Set("sni", sni)
	}
	if rn.CertSelfSigned {
		q.Set("allowInsecure", "1")
	}
	addTransportQueryParams(q, s.Transport, s.WSPath, s.WSHost, s.GRPCServiceName)

	return fmt.Sprintf("trojan://%s@%s?%s#%s",
		url.QueryEscape(strings.TrimSpace(rn.Member.Password)),
		formatHostPort(rn.Host, rn.Port),
		q.Encode(),
		url.PathEscape(rn.Name),
	)
}

func renderShadowTLSURI(rn resolvedNode) string {
	s := rn.shadowtls
	hsServer := strings.TrimSpace(s.HandshakeServer)
	if hsServer == "" {
		hsServer = rn.Host
	}
	outerPw := strings.TrimSpace(rn.Member.Password)

	if s.DetourProtocol == "vless" {
		q := url.Values{}
		q.Set("encryption", "none")
		q.Set("security", "none")
		q.Set("type", "tcp")
		q.Set("plugin", fmt.Sprintf("shadow-tls;host=%s;password=%s;version=3", hsServer, outerPw))
		return fmt.Sprintf("vless://%s@%s?%s#%s",
			url.PathEscape(strings.TrimSpace(rn.Member.UUID)),
			formatHostPort(rn.Host, rn.Port),
			q.Encode(),
			url.PathEscape(rn.Name),
		)
	}

	clientPw := resolveSSPassword(s.SSMethod, s.SSServerPassword, rn.Member)
	userInfo := base64.StdEncoding.EncodeToString([]byte(s.SSMethod + ":" + clientPw))
	plugin := fmt.Sprintf("shadow-tls;host=%s;password=%s;version=3", hsServer, outerPw)
	q := url.Values{}
	q.Set("plugin", plugin)

	return fmt.Sprintf("ss://%s@%s/?%s#%s",
		userInfo,
		formatHostPort(rn.Host, rn.Port),
		q.Encode(),
		url.PathEscape(rn.Name),
	)
}

func renderSnellURI(rn resolvedNode) string {
	s := rn.snell
	ver := s.Version
	if ver != 4 && ver != 5 {
		ver = 5
	}
	udpStr := "true"
	if s.UDP != nil && !*s.UDP {
		udpStr = "false"
	}
	q := url.Values{}
	q.Set("version", fmt.Sprintf("%d", ver))
	q.Set("udp", udpStr)
	if s.ObfsMode == "http" {
		q.Set("obfs", "http")
		if h := strings.TrimSpace(s.ObfsHost); h != "" {
			q.Set("obfs-host", h)
		}
	}
	return fmt.Sprintf("snell://%s@%s?%s#%s",
		url.QueryEscape(strings.TrimSpace(s.PSK)),
		formatHostPort(rn.Host, rn.Port),
		q.Encode(),
		url.PathEscape(rn.Name),
	)
}

func renderSocksURI(rn resolvedNode) string {
	auth := ""
	if rn.local.AuthEnabled && strings.TrimSpace(rn.Member.Name) != "" {
		auth = url.QueryEscape(strings.TrimSpace(rn.Member.Name)) + ":" + url.QueryEscape(strings.TrimSpace(rn.Member.Password)) + "@"
	}
	return fmt.Sprintf("socks5://%s%s#%s",
		auth,
		formatHostPort(rn.Host, rn.Port),
		url.PathEscape(rn.Name),
	)
}

func renderHTTPURI(rn resolvedNode) string {
	auth := ""
	if rn.local.AuthEnabled && strings.TrimSpace(rn.Member.Name) != "" {
		auth = url.QueryEscape(strings.TrimSpace(rn.Member.Name)) + ":" + url.QueryEscape(strings.TrimSpace(rn.Member.Password)) + "@"
	}
	return fmt.Sprintf("http://%s%s#%s",
		auth,
		formatHostPort(rn.Host, rn.Port),
		url.PathEscape(rn.Name),
	)
}

func addTransportQueryParams(q url.Values, transport, wsPath, wsHost, grpcSvc string) {
	switch transport {
	case "ws", "httpupgrade":
		p := strings.TrimSpace(wsPath)
		if p == "" {
			p = "/"
		}
		q.Set("path", p)
		if h := strings.TrimSpace(wsHost); h != "" {
			q.Set("host", h)
		}
	case "grpc":
		svc := strings.TrimSpace(grpcSvc)
		if svc == "" {
			svc = "grpc"
		}
		q.Set("serviceName", svc)
	}
}
