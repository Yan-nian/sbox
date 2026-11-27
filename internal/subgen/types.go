package subgen

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// NodeEndpoint describes the public network coordinates of a managed VPS node.
type NodeEndpoint struct {
	NodeID     int64  `json:"node_id"`
	NodeName   string `json:"node_name"`
	PublicIPv4 string `json:"public_ipv4"`
	PublicIPv6 string `json:"public_ipv6"`
}

// ExtraEntry defines an additional relay/entry endpoint derived from an inbound.
type ExtraEntry struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// InboundEntry describes a single enabled inbound with its resolved certificate metadata.
type InboundEntry struct {
	ID              int64           `json:"id"`
	NodeID          int64           `json:"node_id"`
	Name            string          `json:"name"`
	Tag             string          `json:"tag"`
	Protocol        string          `json:"protocol"` // vless|hysteria2|tuic|anytls|shadowsocks|trojan|shadowtls|socks|http|mixed|direct
	ListenPort      int             `json:"listen_port"`
	Settings        json.RawMessage `json:"settings"`
	ExternalHost    string          `json:"external_host"`
	ExternalPort    int             `json:"external_port"`
	ExtraEntries    []ExtraEntry    `json:"extra_entries"`
	CertSelfSigned  bool            `json:"cert_self_signed"`
	CertFingerprint string          `json:"cert_fingerprint"`
	CertDomains     []string        `json:"cert_domains"`
}

// MemberCred holds per-protocol credentials for a single panel member.
type MemberCred struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	UUID        string `json:"uuid"`
	Password    string `json:"password"`
	SS2022Key16 string `json:"ss2022_key16"`
	SS2022Key32 string `json:"ss2022_key32"`
	SubToken    string `json:"sub_token"`
}

// RenderInput aggregates cluster nodes, inbounds, and target member for subscription rendering.
type RenderInput struct {
	InstanceID string         `json:"instance_id"`
	Member     MemberCred     `json:"member"`
	Nodes      []NodeEndpoint `json:"nodes"`
	Inbounds   []InboundEntry `json:"inbounds"`
}

// RenderedNode represents a single rendered client proxy entry.
type RenderedNode struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	NodeName string `json:"node_name"`
	URI      string `json:"uri"`
	Loon     string `json:"loon,omitempty"`
}

// RenderOutput is the complete formatted subscription response.
type RenderOutput struct {
	Format      string         `json:"format"`       // "uri" | "base64" | "singbox" | "clash"
	ContentType string         `json:"content_type"` // "text/plain; charset=utf-8" | "application/json; charset=utf-8" | "application/yaml; charset=utf-8"
	Filename    string         `json:"filename"`
	Content     string         `json:"content"`
	Nodes       []RenderedNode `json:"nodes"`
}

// Protocol settings structs matching internal/configgen/inbounds.go field names.
type vlessSettings struct {
	TLSMode                string   `json:"tls_mode"`
	Flow                   string   `json:"flow"`
	Transport              string   `json:"transport"`
	WSPath                 string   `json:"ws_path"`
	WSHost                 string   `json:"ws_host"`
	GRPCServiceName        string   `json:"grpc_service_name"`
	RealityHandshakeServer string   `json:"reality_handshake_server"`
	RealityHandshakePort   int      `json:"reality_handshake_port"`
	RealityPrivateKey      string   `json:"reality_private_key"`
	RealityPublicKey       string   `json:"reality_public_key"`
	RealityShortIDs        []string `json:"reality_short_ids"`
	ServerName             string   `json:"server_name"`
}

type hysteria2Settings struct {
	UpMbps                int    `json:"up_mbps"`
	DownMbps              int    `json:"down_mbps"`
	ObfsPassword          string `json:"obfs_password"`
	Masquerade            string `json:"masquerade"`
	ServerPorts           string `json:"server_ports"`
	IgnoreClientBandwidth bool   `json:"ignore_client_bandwidth"`
}

type tuicSettings struct {
	CongestionControl string `json:"congestion_control"`
	AuthTimeout       string `json:"auth_timeout"`
	ZeroRTTHandshake  bool   `json:"zero_rtt_handshake"`
	Heartbeat         string `json:"heartbeat"`
}

type anytlsSettings struct {
	PaddingScheme []string `json:"padding_scheme"`
}

type shadowsocksSettings struct {
	Method         string `json:"method"`
	ServerPassword string `json:"server_password"`
	Network        string `json:"network"`
}

type trojanSettings struct {
	Transport       string `json:"transport"`
	WSPath          string `json:"ws_path"`
	WSHost          string `json:"ws_host"`
	GRPCServiceName string `json:"grpc_service_name"`
	ServerName      string `json:"server_name"`
}

type shadowtlsSettings struct {
	HandshakeServer  string `json:"handshake_server"`
	HandshakePort    int    `json:"handshake_port"`
	StrictMode       *bool  `json:"strict_mode"`
	DetourProtocol   string `json:"detour_protocol"`
	DetourPort       int    `json:"detour_port"`
	SSMethod         string `json:"ss_method"`
	SSServerPassword string `json:"ss_server_password"`
}

type localProxySettings struct {
	AuthEnabled bool `json:"auth_enabled"`
}

// resolvedNode is the normalized, expanded internal representation of a single proxy node.
type resolvedNode struct {
	Name            string
	NodeName        string
	Protocol        string
	Host            string
	Port            int
	CertSelfSigned  bool
	CertFingerprint string
	CertDomains     []string
	Member          MemberCred

	// Parsed protocol settings (only the relevant one is populated)
	vless     vlessSettings
	hy2       hysteria2Settings
	tuic      tuicSettings
	anytls    anytlsSettings
	ss        shadowsocksSettings
	trojan    trojanSettings
	shadowtls shadowtlsSettings
	local     localProxySettings
}

// DetectFormatFromUA inspects the client HTTP User-Agent header and picks the best subscription format.
// Returns "singbox", "clash", or "base64".
func DetectFormatFromUA(userAgent string) string {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	if ua == "" {
		return "base64"
	}

	// sing-box ecosystem (official SFA/SFI/SFM/SFT, sing-box CLI, Hiddify, NekoBox, Karing)
	if strings.Contains(ua, "sing-box") ||
		strings.Contains(ua, "singbox") ||
		strings.Contains(ua, "hiddify") ||
		hasUAToken(ua, "sfa") ||
		hasUAToken(ua, "sfi") ||
		hasUAToken(ua, "sfm") ||
		hasUAToken(ua, "sft") {
		return "singbox"
	}

	// Clash / Mihomo ecosystem
	if strings.Contains(ua, "clash") ||
		strings.Contains(ua, "mihomo") ||
		strings.Contains(ua, "stash") ||
		strings.Contains(ua, "flclash") {
		return "clash"
	}

	return "base64"
}

func hasUAToken(ua, token string) bool {
	idx := 0
	for {
		pos := strings.Index(ua[idx:], token)
		if pos < 0 {
			return false
		}
		start := idx + pos
		end := start + len(token)
		leftOK := start == 0 || !isAlphaNum(ua[start-1])
		rightOK := end == len(ua) || !isAlphaNum(ua[end])
		if leftOK && rightOK {
			return true
		}
		idx = start + 1
	}
}

func isAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// Render compiles all eligible inbounds across nodes for the given member into the requested format.
func Render(in RenderInput, format string) (*RenderOutput, error) {
	normFmt, err := normalizeFormat(format)
	if err != nil {
		return nil, err
	}

	expanded, err := expandInbounds(in)
	if err != nil {
		return nil, err
	}

	renderedNodes := make([]RenderedNode, 0, len(expanded))
	uriLines := make([]string, 0, len(expanded))
	for _, rn := range expanded {
		u := renderURI(rn)
		renderedNodes = append(renderedNodes, RenderedNode{
			Name:     rn.Name,
			Protocol: rn.Protocol,
			NodeName: rn.NodeName,
			URI:      u,
			Loon:     renderLoonLine(rn),
		})
		if u != "" {
			uriLines = append(uriLines, u)
		}
	}

	baseName := buildFilenameBase(in.InstanceID, in.Member.Name)
	out := &RenderOutput{
		Format: normFmt,
		Nodes:  renderedNodes,
	}

	switch normFmt {
	case "uri":
		out.ContentType = "text/plain; charset=utf-8"
		out.Filename = baseName + ".txt"
		out.Content = strings.Join(uriLines, "\n")
	case "base64":
		out.ContentType = "text/plain; charset=utf-8"
		out.Filename = baseName + ".txt"
		out.Content = base64.StdEncoding.EncodeToString([]byte(strings.Join(uriLines, "\n")))
	case "singbox":
		content, err := renderSingbox(expanded)
		if err != nil {
			return nil, err
		}
		out.ContentType = "application/json; charset=utf-8"
		out.Filename = baseName + ".json"
		out.Content = content
	case "clash":
		content, err := renderClash(expanded)
		if err != nil {
			return nil, err
		}
		out.ContentType = "application/yaml; charset=utf-8"
		out.Filename = baseName + ".yaml"
		out.Content = content
	}

	return out, nil
}

func normalizeFormat(format string) (string, error) {
	f := strings.ToLower(strings.TrimSpace(format))
	switch f {
	case "", "base64":
		return "base64", nil
	case "uri", "raw", "plain":
		return "uri", nil
	case "singbox", "sing-box":
		return "singbox", nil
	case "clash", "mihomo":
		return "clash", nil
	default:
		return "", fmt.Errorf("subgen: unsupported format %q", format)
	}
}

func expandInbounds(in RenderInput) ([]resolvedNode, error) {
	nodeMap := make(map[int64]NodeEndpoint, len(in.Nodes))
	for _, n := range in.Nodes {
		nodeMap[n.NodeID] = n
	}

	usedNames := make(map[string]int)
	uniqueName := func(base string) string {
		if _, exists := usedNames[base]; !exists {
			usedNames[base] = 1
			return base
		}
		for n := usedNames[base] + 1; ; n++ {
			cand := fmt.Sprintf("%s #%d", base, n)
			if _, exists := usedNames[cand]; !exists {
				usedNames[base] = n
				usedNames[cand] = 1
				return cand
			}
		}
	}

	var result []resolvedNode
	for _, ib := range in.Inbounds {
		proto := strings.ToLower(strings.TrimSpace(ib.Protocol))
		if proto == "" || proto == "direct" {
			continue
		}

		node := nodeMap[ib.NodeID]
		nodeName := strings.TrimSpace(node.NodeName)
		if nodeName == "" {
			if id := strings.TrimSpace(in.InstanceID); id != "" {
				nodeName = id
			} else {
				nodeName = "local"
			}
		}

		baseNode := resolvedNode{
			NodeName:        nodeName,
			Protocol:        proto,
			CertSelfSigned:  ib.CertSelfSigned,
			CertFingerprint: strings.TrimSpace(ib.CertFingerprint),
			CertDomains:     ib.CertDomains,
			Member:          in.Member,
		}

		if err := parseProtocolSettings(&baseNode, ib); err != nil {
			return nil, err
		}

		primaryHost := resolveHost(ib, node, baseNode)
		primaryPort := ib.ExternalPort
		if primaryPort <= 0 {
			primaryPort = ib.ListenPort
		}

		label := strings.TrimSpace(ib.Name)
		if label == "" {
			label = strings.TrimSpace(ib.Tag)
		}
		if label == "" {
			label = strings.ToUpper(proto)
		}

		primary := baseNode
		primary.Host = primaryHost
		primary.Port = primaryPort
		primary.Name = uniqueName(fmt.Sprintf("[%s] %s", nodeName, label))
		result = append(result, primary)

		for _, extra := range ib.ExtraEntries {
			eh := strings.TrimSpace(extra.Host)
			ep := extra.Port
			if eh == "" && ep <= 0 {
				continue
			}
			if eh == "" {
				eh = primaryHost
			}
			if ep <= 0 {
				ep = primaryPort
			}
			en := strings.TrimSpace(extra.Name)
			if en == "" {
				en = net.JoinHostPort(eh, strconv.Itoa(ep))
			}
			derived := baseNode
			derived.Host = eh
			derived.Port = ep
			derived.Name = uniqueName(fmt.Sprintf("[%s] %s - %s", nodeName, label, en))
			result = append(result, derived)
		}
	}

	return result, nil
}

func parseProtocolSettings(rn *resolvedNode, ib InboundEntry) error {
	switch rn.Protocol {
	case "vless":
		if err := decodeJSON(ib.Settings, &rn.vless); err != nil {
			return fmt.Errorf("subgen: inbound %q vless settings: %w", ib.Tag, err)
		}
		rn.vless.TLSMode = resolveVLESSTLSMode(rn.vless, ib)
		rn.vless.Transport = strings.ToLower(strings.TrimSpace(rn.vless.Transport))
		if rn.vless.Transport == "" {
			rn.vless.Transport = "tcp"
		}
	case "hysteria2":
		if err := decodeJSON(ib.Settings, &rn.hy2); err != nil {
			return fmt.Errorf("subgen: inbound %q hysteria2 settings: %w", ib.Tag, err)
		}
	case "tuic":
		if err := decodeJSON(ib.Settings, &rn.tuic); err != nil {
			return fmt.Errorf("subgen: inbound %q tuic settings: %w", ib.Tag, err)
		}
		rn.tuic.CongestionControl = strings.ToLower(strings.TrimSpace(rn.tuic.CongestionControl))
		if rn.tuic.CongestionControl == "" {
			rn.tuic.CongestionControl = "bbr"
		}
	case "anytls":
		if err := decodeJSON(ib.Settings, &rn.anytls); err != nil {
			return fmt.Errorf("subgen: inbound %q anytls settings: %w", ib.Tag, err)
		}
	case "shadowsocks":
		if err := decodeJSON(ib.Settings, &rn.ss); err != nil {
			return fmt.Errorf("subgen: inbound %q shadowsocks settings: %w", ib.Tag, err)
		}
		rn.ss.Method = strings.ToLower(strings.TrimSpace(rn.ss.Method))
		if rn.ss.Method == "" {
			rn.ss.Method = "2022-blake3-aes-128-gcm"
		}
		rn.ss.Network = strings.ToLower(strings.TrimSpace(rn.ss.Network))
	case "trojan":
		if err := decodeJSON(ib.Settings, &rn.trojan); err != nil {
			return fmt.Errorf("subgen: inbound %q trojan settings: %w", ib.Tag, err)
		}
		rn.trojan.Transport = strings.ToLower(strings.TrimSpace(rn.trojan.Transport))
		if rn.trojan.Transport == "" {
			rn.trojan.Transport = "tcp"
		}
	case "shadowtls":
		if err := decodeJSON(ib.Settings, &rn.shadowtls); err != nil {
			return fmt.Errorf("subgen: inbound %q shadowtls settings: %w", ib.Tag, err)
		}
		rn.shadowtls.DetourProtocol = strings.ToLower(strings.TrimSpace(rn.shadowtls.DetourProtocol))
		if rn.shadowtls.DetourProtocol == "" {
			rn.shadowtls.DetourProtocol = "shadowsocks"
		}
		rn.shadowtls.SSMethod = strings.ToLower(strings.TrimSpace(rn.shadowtls.SSMethod))
		if rn.shadowtls.SSMethod == "" {
			rn.shadowtls.SSMethod = "2022-blake3-aes-128-gcm"
		}
	case "socks", "http", "mixed":
		if err := decodeJSON(ib.Settings, &rn.local); err != nil {
			return fmt.Errorf("subgen: inbound %q %s settings: %w", ib.Tag, rn.Protocol, err)
		}
	default:
		return fmt.Errorf("subgen: inbound %q unsupported protocol %q", ib.Tag, ib.Protocol)
	}
	return nil
}

func resolveVLESSTLSMode(s vlessSettings, ib InboundEntry) string {
	mode := strings.ToLower(strings.TrimSpace(s.TLSMode))
	if mode != "" {
		return mode
	}
	if strings.TrimSpace(s.RealityPublicKey) != "" ||
		strings.TrimSpace(s.RealityPrivateKey) != "" ||
		strings.TrimSpace(s.RealityHandshakeServer) != "" {
		return "reality"
	}
	if len(ib.CertDomains) > 0 || ib.CertSelfSigned || strings.TrimSpace(ib.CertFingerprint) != "" {
		return "tls"
	}
	return "none"
}

func isTLSProtocol(rn resolvedNode) bool {
	switch rn.Protocol {
	case "hysteria2", "tuic", "anytls", "trojan":
		return true
	case "vless":
		return rn.vless.TLSMode == "tls"
	default:
		return false
	}
}

func firstNonWildcardDomain(domains []string) string {
	for _, d := range domains {
		d = strings.TrimSpace(d)
		if d == "" || strings.HasPrefix(d, "*.") {
			continue
		}
		return d
	}
	return ""
}

func resolveHost(ib InboundEntry, node NodeEndpoint, rn resolvedNode) string {
	if h := strings.TrimSpace(ib.ExternalHost); h != "" {
		return h
	}
	if isTLSProtocol(rn) {
		if d := firstNonWildcardDomain(ib.CertDomains); d != "" {
			return d
		}
	}
	if ip4 := strings.TrimSpace(node.PublicIPv4); ip4 != "" {
		return ip4
	}
	if ip6 := strings.TrimSpace(node.PublicIPv6); ip6 != "" {
		return ip6
	}
	return "127.0.0.1"
}

func resolveSNI(rn resolvedNode, explicitSNI string) string {
	if s := strings.TrimSpace(explicitSNI); s != "" {
		return s
	}
	if d := firstNonWildcardDomain(rn.CertDomains); d != "" {
		return d
	}
	return rn.Host
}

func resolveSSPassword(method, serverPassword string, m MemberCred) string {
	serverPw := strings.TrimSpace(serverPassword)
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "2022-blake3-aes-128-gcm":
		userKey := strings.TrimSpace(m.SS2022Key16)
		if serverPw != "" && userKey != "" {
			return serverPw + ":" + userKey
		}
		if serverPw != "" {
			return serverPw
		}
		return userKey
	case "2022-blake3-aes-256-gcm":
		userKey := strings.TrimSpace(m.SS2022Key32)
		if serverPw != "" && userKey != "" {
			return serverPw + ":" + userKey
		}
		if serverPw != "" {
			return serverPw
		}
		return userKey
	case "2022-blake3-chacha20-poly1305":
		if serverPw != "" {
			return serverPw
		}
		return strings.TrimSpace(m.SS2022Key32)
	default:
		if pw := strings.TrimSpace(m.Password); pw != "" {
			return pw
		}
		return serverPw
	}
}

func firstShortID(ids []string) string {
	for _, id := range ids {
		if t := strings.TrimSpace(id); t != "" {
			return t
		}
	}
	return ""
}

func applyVLESSFlow(s vlessSettings) string {
	flow := strings.TrimSpace(s.Flow)
	if flow != "" && s.Transport == "tcp" && s.TLSMode != "none" {
		return flow
	}
	return ""
}

func normalizePortRanges(raw string, sep string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Normalize delimiter between "-" and ":"
		norm := strings.ReplaceAll(p, "-", sep)
		norm = strings.ReplaceAll(norm, ":", sep)
		if !strings.Contains(norm, sep) && sep == ":" {
			// sing-box server_ports expects "start:end"
			norm = norm + ":" + norm
		}
		out = append(out, norm)
	}
	return out
}

func formatHostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func decodeJSON(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	return json.Unmarshal(trimmed, dst)
}

func buildFilenameBase(instanceID, memberName string) string {
	parts := make([]string, 0, 2)
	if s := sanitizeFilename(instanceID); s != "" {
		parts = append(parts, s)
	}
	if s := sanitizeFilename(memberName); s != "" {
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "sbox-panel"
	}
	return strings.Join(parts, "-")
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' || r == '.' {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-_")
}
