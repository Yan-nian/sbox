package subgen

import (
	"fmt"
	"strconv"
	"strings"
)

// LoonRenderedNode represents a single proxy node formatted in native Loon INI syntax.
type LoonRenderedNode struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	NodeName string `json:"node_name"`
	Line     string `json:"line"`
}

// RenderLoon expands all eligible inbounds across nodes for the target member and formats
// each node as a native Loon INI proxy line.
func RenderLoon(in RenderInput) ([]LoonRenderedNode, error) {
	expanded, err := expandInbounds(in)
	if err != nil {
		return nil, err
	}
	out := make([]LoonRenderedNode, 0, len(expanded))
	for _, rn := range expanded {
		line := renderLoonLine(rn)
		if line == "" {
			continue
		}
		out = append(out, LoonRenderedNode{
			Name:     rn.Name,
			Protocol: rn.Protocol,
			NodeName: rn.NodeName,
			Line:     line,
		})
	}
	return out, nil
}

func sanitizeLoonName(name string) string {
	s := strings.TrimSpace(name)
	s = strings.NewReplacer("=", "-", ",", "-", "\r", "", "\n", "").Replace(s)
	if s == "" {
		return "sbox-node"
	}
	return s
}

func quoteLoon(v string) string {
	escaped := strings.ReplaceAll(strings.TrimSpace(v), `"`, `\"`)
	return `"` + escaped + `"`
}

func loonBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func renderLoonLine(rn resolvedNode) string {
	switch rn.Protocol {
	case "vless":
		return renderLoonVLESS(rn)
	case "hysteria2":
		return renderLoonHysteria2(rn)
	case "tuic":
		return renderLoonTUIC(rn)
	case "anytls":
		return renderLoonAnyTLS(rn)
	case "shadowsocks":
		return renderLoonShadowsocks(rn)
	case "shadowtls":
		return renderLoonShadowTLS(rn)
	case "snell":
		return renderLoonSnell(rn)
	case "trojan":
		return renderLoonTrojan(rn)
	case "socks", "mixed":
		return renderLoonSocks(rn)
	case "http":
		return renderLoonHTTP(rn)
	default:
		return ""
	}
}

func renderLoonVLESS(rn resolvedNode) string {
	s := rn.vless
	parts := []string{
		"VLESS",
		rn.Host,
		strconv.Itoa(rn.Port),
		quoteLoon(rn.Member.UUID),
	}

	switch s.Transport {
	case "ws", "httpupgrade":
		parts = append(parts, "transport=ws")
		p := strings.TrimSpace(s.WSPath)
		if p == "" {
			p = "/"
		}
		parts = append(parts, "path="+p)
		if h := strings.TrimSpace(s.WSHost); h != "" {
			parts = append(parts, "host="+h)
		}
	case "grpc":
		parts = append(parts, "transport=grpc")
		if svc := strings.TrimSpace(s.GRPCServiceName); svc != "" {
			parts = append(parts, "service-name="+svc)
		}
	default:
		parts = append(parts, "transport=tcp")
	}

	if flow := applyVLESSFlow(s); flow != "" {
		parts = append(parts, "flow="+flow)
	}

	switch s.TLSMode {
	case "reality":
		if pbk := strings.TrimSpace(s.RealityPublicKey); pbk != "" {
			parts = append(parts, "public-key="+quoteLoon(pbk))
		}
		if sid := firstShortID(s.RealityShortIDs); sid != "" {
			parts = append(parts, "short-id="+sid)
		}
		parts = append(parts, "over-tls=true")
		sni := strings.TrimSpace(s.ServerName)
		if sni == "" {
			sni = strings.TrimSpace(s.RealityHandshakeServer)
		}
		if sni != "" {
			parts = append(parts, "sni="+sni)
		}
	case "tls":
		parts = append(parts, "over-tls=true")
		if sni := resolveSNI(rn, s.ServerName); sni != "" {
			parts = append(parts, "sni="+sni)
		}
		if rn.CertSelfSigned {
			parts = append(parts, "skip-cert-verify=true")
		}
	}

	parts = append(parts, "udp=true")
	return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
}

func renderLoonHysteria2(rn resolvedNode) string {
	s := rn.hy2
	parts := []string{
		"Hysteria2",
		rn.Host,
		strconv.Itoa(rn.Port),
		quoteLoon(rn.Member.Password),
	}
	if obfs := strings.TrimSpace(s.ObfsPassword); obfs != "" {
		parts = append(parts, "salamander-password="+quoteLoon(obfs))
	}
	if ports := normalizePortRanges(s.ServerPorts, ":"); len(ports) > 0 {
		parts = append(parts, "server-ports="+quoteLoon(strings.Join(ports, ",")))
	}
	if sni := resolveSNI(rn, ""); sni != "" {
		parts = append(parts, "sni="+sni)
	}
	parts = append(parts, "skip-cert-verify="+loonBool(rn.CertSelfSigned))
	parts = append(parts, "udp=true")
	return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
}

func renderLoonTUIC(rn resolvedNode) string {
	parts := []string{
		"TUIC",
		rn.Host,
		strconv.Itoa(rn.Port),
		quoteLoon(rn.Member.UUID),
		quoteLoon(rn.Member.Password),
		"version=5",
	}
	if sni := resolveSNI(rn, ""); sni != "" {
		parts = append(parts, "sni="+sni)
	}
	parts = append(parts, "skip-cert-verify="+loonBool(rn.CertSelfSigned))
	parts = append(parts, "udp=true")
	return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
}

func renderLoonAnyTLS(rn resolvedNode) string {
	parts := []string{
		"AnyTLS",
		rn.Host,
		strconv.Itoa(rn.Port),
		quoteLoon(rn.Member.Password),
	}
	if sni := resolveSNI(rn, ""); sni != "" {
		parts = append(parts, "sni="+sni)
	}
	parts = append(parts, "skip-cert-verify="+loonBool(rn.CertSelfSigned))
	parts = append(parts, "udp=true")
	return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
}

func renderLoonShadowsocks(rn resolvedNode) string {
	clientPw := resolveSSPassword(rn.ss.Method, rn.ss.ServerPassword, rn.Member)
	parts := []string{
		"Shadowsocks",
		rn.Host,
		strconv.Itoa(rn.Port),
		rn.ss.Method,
		quoteLoon(clientPw),
		"udp=true",
	}
	return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
}

func renderLoonShadowTLS(rn resolvedNode) string {
	s := rn.shadowtls
	hsServer := strings.TrimSpace(s.HandshakeServer)
	if hsServer == "" {
		hsServer = rn.Host
	}
	outerPw := strings.TrimSpace(rn.Member.Password)

	if s.DetourProtocol == "vless" {
		parts := []string{
			"VLESS",
			rn.Host,
			strconv.Itoa(rn.Port),
			quoteLoon(rn.Member.UUID),
			"transport=tcp",
			"shadow-tls-password=" + quoteLoon(outerPw),
			"shadow-tls-sni=" + hsServer,
			"shadow-tls-version=3",
			"udp=true",
		}
		return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
	}

	clientPw := resolveSSPassword(s.SSMethod, s.SSServerPassword, rn.Member)
	parts := []string{
		"Shadowsocks",
		rn.Host,
		strconv.Itoa(rn.Port),
		s.SSMethod,
		quoteLoon(clientPw),
		"shadow-tls-password=" + quoteLoon(outerPw),
		"shadow-tls-sni=" + hsServer,
		"shadow-tls-version=3",
		"udp=true",
	}
	return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
}

func renderLoonSnell(rn resolvedNode) string {
	s := rn.snell
	ver := s.Version
	if ver != 4 && ver != 5 {
		ver = 5
	}
	udpEnabled := true
	if s.UDP != nil {
		udpEnabled = *s.UDP
	}
	parts := []string{
		"Snell",
		rn.Host,
		strconv.Itoa(rn.Port),
		quoteLoon(s.PSK),
		"version=" + strconv.Itoa(ver),
		"udp=" + loonBool(udpEnabled),
	}
	if s.ObfsMode == "http" {
		parts = append(parts, "obfs=http")
		if h := strings.TrimSpace(s.ObfsHost); h != "" {
			parts = append(parts, "obfs-host="+h)
		}
	}
	return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
}

func renderLoonTrojan(rn resolvedNode) string {
	s := rn.trojan
	parts := []string{
		"Trojan",
		rn.Host,
		strconv.Itoa(rn.Port),
		quoteLoon(rn.Member.Password),
	}
	if s.Transport == "ws" {
		parts = append(parts, "transport=ws")
		p := strings.TrimSpace(s.WSPath)
		if p == "" {
			p = "/"
		}
		parts = append(parts, "path="+p)
		if h := strings.TrimSpace(s.WSHost); h != "" {
			parts = append(parts, "host="+h)
		}
	}
	if sni := resolveSNI(rn, s.ServerName); sni != "" {
		parts = append(parts, "sni="+sni)
	}
	parts = append(parts, "skip-cert-verify="+loonBool(rn.CertSelfSigned))
	parts = append(parts, "udp=true")
	return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
}

func renderLoonSocks(rn resolvedNode) string {
	parts := []string{
		"SOCKS5",
		rn.Host,
		strconv.Itoa(rn.Port),
	}
	if rn.local.AuthEnabled && strings.TrimSpace(rn.Member.Name) != "" {
		parts = append(parts, strings.TrimSpace(rn.Member.Name), strings.TrimSpace(rn.Member.Password))
	}
	return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
}

func renderLoonHTTP(rn resolvedNode) string {
	parts := []string{
		"HTTP",
		rn.Host,
		strconv.Itoa(rn.Port),
	}
	if rn.local.AuthEnabled && strings.TrimSpace(rn.Member.Name) != "" {
		parts = append(parts, strings.TrimSpace(rn.Member.Name), strings.TrimSpace(rn.Member.Password))
	}
	return fmt.Sprintf("%s = %s", sanitizeLoonName(rn.Name), strings.Join(parts, ", "))
}
