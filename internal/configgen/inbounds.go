package configgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// CertRef identifies PEM certificate and private key file paths for TLS-enabled inbounds.
type CertRef struct {
	CertPath string `json:"cert_path"`
	KeyPath  string `json:"key_path"`
}

// MemberCredential carries a single member's per-protocol authentication credentials.
type MemberCredential struct {
	Name        string `json:"name"`
	UUID        string `json:"uuid"`
	Password    string `json:"password"`
	SS2022Key16 string `json:"ss2022_key16"`
	SS2022Key32 string `json:"ss2022_key32"`
}

// InboundSpec is the structured high-level description of a panel inbound compiled into
// one or more modern sing-box 1.12+ inbound JSON objects.
type InboundSpec struct {
	Tag        string             `json:"tag"`
	Protocol   string             `json:"protocol"` // "vless"|"hysteria2"|"tuic"|"anytls"|"shadowsocks"|"trojan"|"shadowtls"|"direct"|"mixed"|"socks"|"http"
	Listen     string             `json:"listen"`   // default "::"
	ListenPort int                `json:"listen_port"`
	Settings   json.RawMessage    `json:"settings"`
	Cert       *CertRef           `json:"cert,omitempty"`
	Members    []MemberCredential `json:"members,omitempty"`
}

type vlessSettings struct {
	TLSMode                string   `json:"tls_mode"` // "reality" | "tls" | "none"
	Flow                   string   `json:"flow"`     // "xtls-rprx-vision" | ""
	Transport              string   `json:"transport"`
	WSPath                 string   `json:"ws_path"`
	WSHost                 string   `json:"ws_host"`
	GRPCServiceName        string   `json:"grpc_service_name"`
	RealityHandshakeServer string   `json:"reality_handshake_server"`
	RealityHandshakePort   int      `json:"reality_handshake_port"`
	RealityPrivateKey      string   `json:"reality_private_key"`
	RealityPublicKey       string   `json:"reality_public_key"` // subscription metadata only
	RealityShortIDs        []string `json:"reality_short_ids"`
	ServerName             string   `json:"server_name"`
}

type hysteria2Settings struct {
	UpMbps                int    `json:"up_mbps"`
	DownMbps              int    `json:"down_mbps"`
	ObfsPassword          string `json:"obfs_password"`
	Masquerade            string `json:"masquerade"`
	ServerPorts           string `json:"server_ports"` // subscription metadata only
	IgnoreClientBandwidth bool   `json:"ignore_client_bandwidth"`
}

type tuicSettings struct {
	CongestionControl string `json:"congestion_control"` // "bbr" | "cubic" | "new_reno"
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
	Transport       string `json:"transport"` // "tcp" | "ws" | "grpc" | "httpupgrade"
	WSPath          string `json:"ws_path"`
	WSHost          string `json:"ws_host"`
	GRPCServiceName string `json:"grpc_service_name"`
	ServerName      string `json:"server_name"`
}

type shadowtlsSettings struct {
	HandshakeServer  string `json:"handshake_server"`
	HandshakePort    int    `json:"handshake_port"`
	StrictMode       *bool  `json:"strict_mode"`
	DetourProtocol   string `json:"detour_protocol"` // "shadowsocks" | "vless"
	DetourPort       int    `json:"detour_port"`
	SSMethod         string `json:"ss_method"`
	SSServerPassword string `json:"ss_server_password"`
}

type directSettings struct {
	OverrideAddress string `json:"override_address"`
	OverridePort    int    `json:"override_port"`
	Network         string `json:"network"`
}

type localProxySettings struct {
	AuthEnabled bool `json:"auth_enabled"`
}

// CompileInbound validates spec and compiles it into one (or two for shadowtls)
// modern sing-box 1.12+ inbound JSON objects.
func CompileInbound(spec InboundSpec) ([]json.RawMessage, error) {
	tag := strings.TrimSpace(spec.Tag)
	if tag == "" {
		return nil, fmt.Errorf("configgen: inbound tag is required")
	}
	if spec.ListenPort < 1 || spec.ListenPort > 65535 {
		return nil, fmt.Errorf("configgen: inbound %q invalid listen_port %d (must be 1..65535)", tag, spec.ListenPort)
	}
	listen := strings.TrimSpace(spec.Listen)
	if listen == "" {
		listen = "::"
	}

	proto := strings.ToLower(strings.TrimSpace(spec.Protocol))
	spec.Tag = tag
	spec.Listen = listen
	spec.Protocol = proto

	var objs []map[string]any
	var err error

	switch proto {
	case "vless":
		objs, err = compileVLESS(spec)
	case "hysteria2":
		objs, err = compileHysteria2(spec)
	case "tuic":
		objs, err = compileTUIC(spec)
	case "anytls":
		objs, err = compileAnyTLS(spec)
	case "shadowsocks":
		objs, err = compileShadowsocks(spec)
	case "trojan":
		objs, err = compileTrojan(spec)
	case "shadowtls":
		objs, err = compileShadowTLS(spec)
	case "direct":
		objs, err = compileDirect(spec)
	case "mixed", "socks", "http":
		objs, err = compileLocalProxy(spec)
	default:
		return nil, fmt.Errorf("configgen: inbound %q unsupported protocol %q", tag, spec.Protocol)
	}
	if err != nil {
		return nil, err
	}

	out := make([]json.RawMessage, 0, len(objs))
	for _, obj := range objs {
		b, err := marshalCompactNoEscape(obj)
		if err != nil {
			return nil, fmt.Errorf("configgen: marshal inbound %q: %w", tag, err)
		}
		out = append(out, json.RawMessage(b))
	}
	return out, nil
}

func compileVLESS(spec InboundSpec) ([]map[string]any, error) {
	var s vlessSettings
	if err := decodeSettings(spec.Tag, spec.Settings, &s); err != nil {
		return nil, err
	}

	transport := strings.ToLower(strings.TrimSpace(s.Transport))
	if transport == "" {
		transport = "tcp"
	}
	if transport != "tcp" && transport != "ws" && transport != "httpupgrade" && transport != "grpc" {
		return nil, fmt.Errorf("configgen: inbound %q unsupported vless transport %q", spec.Tag, s.Transport)
	}

	tlsMode := strings.ToLower(strings.TrimSpace(s.TLSMode))
	if tlsMode == "" {
		switch {
		case strings.TrimSpace(s.RealityPrivateKey) != "":
			tlsMode = "reality"
		case spec.Cert != nil:
			tlsMode = "tls"
		default:
			tlsMode = "none"
		}
	}

	var tlsObj map[string]any
	switch tlsMode {
	case "reality":
		hsServer := strings.TrimSpace(s.RealityHandshakeServer)
		if hsServer == "" {
			return nil, fmt.Errorf("configgen: inbound %q vless reality requires reality_handshake_server", spec.Tag)
		}
		hsPort := s.RealityHandshakePort
		if hsPort == 0 {
			hsPort = 443
		}
		if hsPort < 1 || hsPort > 65535 {
			return nil, fmt.Errorf("configgen: inbound %q invalid reality_handshake_port %d", spec.Tag, hsPort)
		}
		privKey := strings.TrimSpace(s.RealityPrivateKey)
		if privKey == "" {
			return nil, fmt.Errorf("configgen: inbound %q vless reality requires reality_private_key", spec.Tag)
		}
		serverName := strings.TrimSpace(s.ServerName)
		if serverName == "" {
			serverName = hsServer
		}
		shortIDs := normalizeShortIDs(s.RealityShortIDs)
		tlsObj = map[string]any{
			"enabled":     true,
			"server_name": serverName,
			"reality": map[string]any{
				"enabled": true,
				"handshake": map[string]any{
					"server":      hsServer,
					"server_port": hsPort,
				},
				"private_key": privKey,
				"short_id":    shortIDs,
			},
		}
	case "tls":
		if err := requireCert(spec.Tag, "vless(tls)", spec.Cert); err != nil {
			return nil, err
		}
		tlsObj = map[string]any{
			"enabled":          true,
			"certificate_path": strings.TrimSpace(spec.Cert.CertPath),
			"key_path":         strings.TrimSpace(spec.Cert.KeyPath),
		}
		if sn := strings.TrimSpace(s.ServerName); sn != "" {
			tlsObj["server_name"] = sn
		}
	case "none":
		// no TLS
	default:
		return nil, fmt.Errorf("configgen: inbound %q unsupported vless tls_mode %q", spec.Tag, s.TLSMode)
	}

	flow := strings.TrimSpace(s.Flow)
	applyFlow := flow != "" && transport == "tcp" && tlsMode != "none"

	users := make([]map[string]any, 0, len(spec.Members))
	for _, m := range spec.Members {
		uuid := strings.TrimSpace(m.UUID)
		if uuid == "" {
			return nil, fmt.Errorf("configgen: inbound %q member %q missing uuid", spec.Tag, m.Name)
		}
		u := map[string]any{
			"name": m.Name,
			"uuid": uuid,
		}
		if applyFlow {
			u["flow"] = flow
		}
		users = append(users, u)
	}

	ib := map[string]any{
		"type":        "vless",
		"tag":         spec.Tag,
		"listen":      spec.Listen,
		"listen_port": spec.ListenPort,
		"users":       users,
	}
	if tlsObj != nil {
		ib["tls"] = tlsObj
	}
	if tr, err := buildTransport(spec.Tag, transport, s.WSPath, s.WSHost, s.GRPCServiceName); err != nil {
		return nil, err
	} else if tr != nil {
		ib["transport"] = tr
	}

	return []map[string]any{ib}, nil
}

func compileHysteria2(spec InboundSpec) ([]map[string]any, error) {
	if err := requireCert(spec.Tag, "hysteria2", spec.Cert); err != nil {
		return nil, err
	}
	var s hysteria2Settings
	if err := decodeSettings(spec.Tag, spec.Settings, &s); err != nil {
		return nil, err
	}
	if s.UpMbps < 0 || s.DownMbps < 0 {
		return nil, fmt.Errorf("configgen: inbound %q hysteria2 bandwidth values must be non-negative", spec.Tag)
	}

	users := make([]map[string]any, 0, len(spec.Members))
	for _, m := range spec.Members {
		pw := strings.TrimSpace(m.Password)
		if pw == "" {
			return nil, fmt.Errorf("configgen: inbound %q member %q missing password", spec.Tag, m.Name)
		}
		users = append(users, map[string]any{
			"name":     m.Name,
			"password": pw,
		})
	}

	ib := map[string]any{
		"type":        "hysteria2",
		"tag":         spec.Tag,
		"listen":      spec.Listen,
		"listen_port": spec.ListenPort,
		"users":       users,
		"tls": map[string]any{
			"enabled":          true,
			"certificate_path": strings.TrimSpace(spec.Cert.CertPath),
			"key_path":         strings.TrimSpace(spec.Cert.KeyPath),
		},
	}
	if s.UpMbps > 0 {
		ib["up_mbps"] = s.UpMbps
	}
	if s.DownMbps > 0 {
		ib["down_mbps"] = s.DownMbps
	}
	if s.IgnoreClientBandwidth {
		ib["ignore_client_bandwidth"] = true
	}
	if obfsPw := strings.TrimSpace(s.ObfsPassword); obfsPw != "" {
		ib["obfs"] = map[string]any{
			"type":     "salamander",
			"password": obfsPw,
		}
	}
	if masq := strings.TrimSpace(s.Masquerade); masq != "" {
		ib["masquerade"] = masq
	}

	return []map[string]any{ib}, nil
}

func compileTUIC(spec InboundSpec) ([]map[string]any, error) {
	if err := requireCert(spec.Tag, "tuic", spec.Cert); err != nil {
		return nil, err
	}
	var s tuicSettings
	if err := decodeSettings(spec.Tag, spec.Settings, &s); err != nil {
		return nil, err
	}

	cc := strings.ToLower(strings.TrimSpace(s.CongestionControl))
	if cc == "" {
		cc = "bbr"
	}
	if cc != "bbr" && cc != "cubic" && cc != "new_reno" {
		return nil, fmt.Errorf("configgen: inbound %q unsupported tuic congestion_control %q", spec.Tag, s.CongestionControl)
	}

	users := make([]map[string]any, 0, len(spec.Members))
	for _, m := range spec.Members {
		uuid := strings.TrimSpace(m.UUID)
		if uuid == "" {
			return nil, fmt.Errorf("configgen: inbound %q member %q missing uuid", spec.Tag, m.Name)
		}
		users = append(users, map[string]any{
			"name":     m.Name,
			"uuid":     uuid,
			"password": m.Password,
		})
	}

	ib := map[string]any{
		"type":               "tuic",
		"tag":                spec.Tag,
		"listen":             spec.Listen,
		"listen_port":        spec.ListenPort,
		"users":              users,
		"congestion_control": cc,
		"tls": map[string]any{
			"enabled":          true,
			"alpn":             []string{"h3"},
			"certificate_path": strings.TrimSpace(spec.Cert.CertPath),
			"key_path":         strings.TrimSpace(spec.Cert.KeyPath),
		},
	}
	if v := strings.TrimSpace(s.AuthTimeout); v != "" {
		ib["auth_timeout"] = v
	}
	if s.ZeroRTTHandshake {
		ib["zero_rtt_handshake"] = true
	}
	if v := strings.TrimSpace(s.Heartbeat); v != "" {
		ib["heartbeat"] = v
	}

	return []map[string]any{ib}, nil
}

func compileAnyTLS(spec InboundSpec) ([]map[string]any, error) {
	if err := requireCert(spec.Tag, "anytls", spec.Cert); err != nil {
		return nil, err
	}
	var s anytlsSettings
	if err := decodeSettings(spec.Tag, spec.Settings, &s); err != nil {
		return nil, err
	}

	users := make([]map[string]any, 0, len(spec.Members))
	for _, m := range spec.Members {
		pw := strings.TrimSpace(m.Password)
		if pw == "" {
			return nil, fmt.Errorf("configgen: inbound %q member %q missing password", spec.Tag, m.Name)
		}
		users = append(users, map[string]any{
			"name":     m.Name,
			"password": pw,
		})
	}

	ib := map[string]any{
		"type":        "anytls",
		"tag":         spec.Tag,
		"listen":      spec.Listen,
		"listen_port": spec.ListenPort,
		"users":       users,
		"tls": map[string]any{
			"enabled":          true,
			"certificate_path": strings.TrimSpace(spec.Cert.CertPath),
			"key_path":         strings.TrimSpace(spec.Cert.KeyPath),
		},
	}
	if len(s.PaddingScheme) > 0 {
		scheme := make([]string, 0, len(s.PaddingScheme))
		for _, line := range s.PaddingScheme {
			if t := strings.TrimSpace(line); t != "" {
				scheme = append(scheme, t)
			}
		}
		if len(scheme) > 0 {
			ib["padding_scheme"] = scheme
		}
	}

	return []map[string]any{ib}, nil
}

func compileShadowsocks(spec InboundSpec) ([]map[string]any, error) {
	var s shadowsocksSettings
	if err := decodeSettings(spec.Tag, spec.Settings, &s); err != nil {
		return nil, err
	}
	ib, err := buildShadowsocksObject(spec.Tag, spec.Listen, spec.ListenPort, s, spec.Members)
	if err != nil {
		return nil, err
	}
	return []map[string]any{ib}, nil
}

func buildShadowsocksObject(tag, listen string, listenPort int, s shadowsocksSettings, members []MemberCredential) (map[string]any, error) {
	method := strings.ToLower(strings.TrimSpace(s.Method))
	if method == "" {
		method = "2022-blake3-aes-128-gcm"
	}

	switch method {
	case "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305",
		"aes-256-gcm", "aes-128-gcm", "chacha20-ietf-poly1305", "none":
	default:
		return nil, fmt.Errorf("configgen: inbound %q unsupported shadowsocks method %q", tag, s.Method)
	}

	ib := map[string]any{
		"type":   "shadowsocks",
		"tag":    tag,
		"listen": listen,
		"method": method,
	}
	if listenPort > 0 {
		ib["listen_port"] = listenPort
	}

	if netVal, err := validateNetwork(tag, s.Network); err != nil {
		return nil, err
	} else if netVal != "" {
		ib["network"] = netVal
	}

	serverPw := strings.TrimSpace(s.ServerPassword)
	switch method {
	case "2022-blake3-aes-128-gcm":
		if serverPw == "" {
			return nil, fmt.Errorf("configgen: inbound %q shadowsocks %s requires server_password", tag, method)
		}
		ib["password"] = serverPw
		if len(members) > 0 {
			users := make([]map[string]any, 0, len(members))
			for _, m := range members {
				key := strings.TrimSpace(m.SS2022Key16)
				if key == "" {
					return nil, fmt.Errorf("configgen: inbound %q member %q missing ss2022_key16", tag, m.Name)
				}
				users = append(users, map[string]any{
					"name":     m.Name,
					"password": key,
				})
			}
			ib["users"] = users
		}
	case "2022-blake3-aes-256-gcm":
		if serverPw == "" {
			return nil, fmt.Errorf("configgen: inbound %q shadowsocks %s requires server_password", tag, method)
		}
		ib["password"] = serverPw
		if len(members) > 0 {
			users := make([]map[string]any, 0, len(members))
			for _, m := range members {
				key := strings.TrimSpace(m.SS2022Key32)
				if key == "" {
					return nil, fmt.Errorf("configgen: inbound %q member %q missing ss2022_key32", tag, m.Name)
				}
				users = append(users, map[string]any{
					"name":     m.Name,
					"password": key,
				})
			}
			ib["users"] = users
		}
	case "2022-blake3-chacha20-poly1305":
		if serverPw == "" {
			return nil, fmt.Errorf("configgen: inbound %q shadowsocks %s requires server_password", tag, method)
		}
		ib["password"] = serverPw
	default:
		// Classic AEAD: multi-user `users` when members bound; single `password` fallback when none.
		if len(members) > 0 {
			users := make([]map[string]any, 0, len(members))
			for _, m := range members {
				pw := strings.TrimSpace(m.Password)
				if pw == "" {
					return nil, fmt.Errorf("configgen: inbound %q member %q missing password", tag, m.Name)
				}
				users = append(users, map[string]any{
					"name":     m.Name,
					"password": pw,
				})
			}
			ib["users"] = users
		} else {
			if serverPw == "" && method != "none" {
				return nil, fmt.Errorf("configgen: inbound %q shadowsocks %s requires members or server_password", tag, method)
			}
			ib["password"] = serverPw
		}
	}

	return ib, nil
}

func compileTrojan(spec InboundSpec) ([]map[string]any, error) {
	if err := requireCert(spec.Tag, "trojan", spec.Cert); err != nil {
		return nil, err
	}
	var s trojanSettings
	if err := decodeSettings(spec.Tag, spec.Settings, &s); err != nil {
		return nil, err
	}

	transport := strings.ToLower(strings.TrimSpace(s.Transport))
	if transport == "" {
		transport = "tcp"
	}
	if transport != "tcp" && transport != "ws" && transport != "grpc" && transport != "httpupgrade" {
		return nil, fmt.Errorf("configgen: inbound %q unsupported trojan transport %q", spec.Tag, s.Transport)
	}

	users := make([]map[string]any, 0, len(spec.Members))
	for _, m := range spec.Members {
		pw := strings.TrimSpace(m.Password)
		if pw == "" {
			return nil, fmt.Errorf("configgen: inbound %q member %q missing password", spec.Tag, m.Name)
		}
		users = append(users, map[string]any{
			"name":     m.Name,
			"password": pw,
		})
	}

	tlsObj := map[string]any{
		"enabled":          true,
		"certificate_path": strings.TrimSpace(spec.Cert.CertPath),
		"key_path":         strings.TrimSpace(spec.Cert.KeyPath),
	}
	if sn := strings.TrimSpace(s.ServerName); sn != "" {
		tlsObj["server_name"] = sn
	}

	ib := map[string]any{
		"type":        "trojan",
		"tag":         spec.Tag,
		"listen":      spec.Listen,
		"listen_port": spec.ListenPort,
		"users":       users,
		"tls":         tlsObj,
	}
	if tr, err := buildTransport(spec.Tag, transport, s.WSPath, s.WSHost, s.GRPCServiceName); err != nil {
		return nil, err
	} else if tr != nil {
		ib["transport"] = tr
	}

	return []map[string]any{ib}, nil
}

func compileShadowTLS(spec InboundSpec) ([]map[string]any, error) {
	var s shadowtlsSettings
	if err := decodeSettings(spec.Tag, spec.Settings, &s); err != nil {
		return nil, err
	}

	hsServer := strings.TrimSpace(s.HandshakeServer)
	if hsServer == "" {
		return nil, fmt.Errorf("configgen: inbound %q shadowtls requires handshake_server", spec.Tag)
	}
	hsPort := s.HandshakePort
	if hsPort == 0 {
		hsPort = 443
	}
	if hsPort < 1 || hsPort > 65535 {
		return nil, fmt.Errorf("configgen: inbound %q invalid shadowtls handshake_port %d", spec.Tag, hsPort)
	}
	if s.DetourPort < 0 || s.DetourPort > 65535 {
		return nil, fmt.Errorf("configgen: inbound %q invalid shadowtls detour_port %d", spec.Tag, s.DetourPort)
	}

	strictMode := true
	if s.StrictMode != nil {
		strictMode = *s.StrictMode
	}

	outerUsers := make([]map[string]any, 0, len(spec.Members))
	for _, m := range spec.Members {
		pw := strings.TrimSpace(m.Password)
		if pw == "" {
			return nil, fmt.Errorf("configgen: inbound %q member %q missing password", spec.Tag, m.Name)
		}
		outerUsers = append(outerUsers, map[string]any{
			"name":     m.Name,
			"password": pw,
		})
	}

	detourTag := spec.Tag + "-detour"
	outer := map[string]any{
		"type":        "shadowtls",
		"tag":         spec.Tag,
		"listen":      spec.Listen,
		"listen_port": spec.ListenPort,
		"version":     3,
		"users":       outerUsers,
		"handshake": map[string]any{
			"server":      hsServer,
			"server_port": hsPort,
		},
		"strict_mode": strictMode,
		"detour":      detourTag,
	}

	detourProto := strings.ToLower(strings.TrimSpace(s.DetourProtocol))
	if detourProto == "" {
		detourProto = "shadowsocks"
	}

	var inner map[string]any
	var err error
	switch detourProto {
	case "shadowsocks":
		inner, err = buildShadowsocksObject(detourTag, "127.0.0.1", s.DetourPort, shadowsocksSettings{
			Method:         s.SSMethod,
			ServerPassword: s.SSServerPassword,
		}, spec.Members)
		if err != nil {
			return nil, err
		}
	case "vless":
		vlessUsers := make([]map[string]any, 0, len(spec.Members))
		for _, m := range spec.Members {
			uuid := strings.TrimSpace(m.UUID)
			if uuid == "" {
				return nil, fmt.Errorf("configgen: inbound %q member %q missing uuid for vless detour", spec.Tag, m.Name)
			}
			vlessUsers = append(vlessUsers, map[string]any{
				"name": m.Name,
				"uuid": uuid,
			})
		}
		inner = map[string]any{
			"type":   "vless",
			"tag":    detourTag,
			"listen": "127.0.0.1",
			"users":  vlessUsers,
		}
		if s.DetourPort > 0 {
			inner["listen_port"] = s.DetourPort
		}
	default:
		return nil, fmt.Errorf("configgen: inbound %q unsupported shadowtls detour_protocol %q", spec.Tag, s.DetourProtocol)
	}

	return []map[string]any{outer, inner}, nil
}

func compileDirect(spec InboundSpec) ([]map[string]any, error) {
	var s directSettings
	if err := decodeSettings(spec.Tag, spec.Settings, &s); err != nil {
		return nil, err
	}
	if s.OverridePort < 0 || s.OverridePort > 65535 {
		return nil, fmt.Errorf("configgen: inbound %q invalid override_port %d", spec.Tag, s.OverridePort)
	}

	ib := map[string]any{
		"type":        "direct",
		"tag":         spec.Tag,
		"listen":      spec.Listen,
		"listen_port": spec.ListenPort,
	}
	if netVal, err := validateNetwork(spec.Tag, s.Network); err != nil {
		return nil, err
	} else if netVal != "" {
		ib["network"] = netVal
	}
	if addr := strings.TrimSpace(s.OverrideAddress); addr != "" {
		ib["override_address"] = addr
	}
	if s.OverridePort > 0 {
		ib["override_port"] = s.OverridePort
	}

	return []map[string]any{ib}, nil
}

func compileLocalProxy(spec InboundSpec) ([]map[string]any, error) {
	var s localProxySettings
	if err := decodeSettings(spec.Tag, spec.Settings, &s); err != nil {
		return nil, err
	}

	ib := map[string]any{
		"type":        spec.Protocol,
		"tag":         spec.Tag,
		"listen":      spec.Listen,
		"listen_port": spec.ListenPort,
	}
	if s.AuthEnabled && len(spec.Members) > 0 {
		users := make([]map[string]any, 0, len(spec.Members))
		for _, m := range spec.Members {
			users = append(users, map[string]any{
				"username": m.Name,
				"password": m.Password,
			})
		}
		ib["users"] = users
	}

	return []map[string]any{ib}, nil
}

func buildTransport(tag, transport, wsPath, wsHost, grpcServiceName string) (map[string]any, error) {
	switch transport {
	case "", "tcp":
		return nil, nil
	case "ws":
		path := strings.TrimSpace(wsPath)
		if path == "" {
			path = "/"
		}
		tr := map[string]any{
			"type": "ws",
			"path": path,
		}
		if host := strings.TrimSpace(wsHost); host != "" {
			tr["headers"] = map[string]any{
				"Host": host,
			}
		}
		return tr, nil
	case "httpupgrade":
		path := strings.TrimSpace(wsPath)
		if path == "" {
			path = "/"
		}
		tr := map[string]any{
			"type": "httpupgrade",
			"path": path,
		}
		if host := strings.TrimSpace(wsHost); host != "" {
			tr["host"] = host
		}
		return tr, nil
	case "grpc":
		svc := strings.TrimSpace(grpcServiceName)
		if svc == "" {
			svc = "grpc"
		}
		return map[string]any{
			"type":         "grpc",
			"service_name": svc,
		}, nil
	default:
		return nil, fmt.Errorf("configgen: inbound %q unsupported transport %q", tag, transport)
	}
}

func requireCert(tag, protocol string, cert *CertRef) error {
	if cert == nil || strings.TrimSpace(cert.CertPath) == "" || strings.TrimSpace(cert.KeyPath) == "" {
		return fmt.Errorf("configgen: inbound %q (%s) requires a valid TLS certificate (cert_path and key_path)", tag, protocol)
	}
	return nil
}

func validateNetwork(tag, network string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(network))
	switch n {
	case "", "both", "tcp/udp":
		return "", nil
	case "tcp", "udp":
		return n, nil
	default:
		return "", fmt.Errorf("configgen: inbound %q invalid network %q (expected tcp, udp, or empty)", tag, network)
	}
}

func normalizeShortIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strings.TrimSpace(id))
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func decodeSettings(tag string, raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(trimmed, dst); err != nil {
		return fmt.Errorf("configgen: invalid settings JSON for inbound %q: %w", tag, err)
	}
	return nil
}

func marshalCompactNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
