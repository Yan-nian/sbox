package configgen

import (
	"encoding/json"
	"strings"
	"testing"
)

func sampleMembers() []MemberCredential {
	return []MemberCredential{
		{
			Name:        "alice",
			UUID:        "11111111-1111-1111-1111-111111111111",
			Password:    "alice-pass",
			SS2022Key16: "AAAAAAAAAAAAAAAAAAAAAA==",
			SS2022Key32: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		},
		{
			Name:        "bob",
			UUID:        "22222222-2222-2222-2222-222222222222",
			Password:    "bob-pass",
			SS2022Key16: "BBBBBBBBBBBBBBBBBBBBBB==",
			SS2022Key32: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
		},
	}
}

func sampleCert() *CertRef {
	return &CertRef{
		CertPath: "/data/certs/example/fullchain.pem",
		KeyPath:  "/data/certs/example/privkey.pem",
	}
}

func mustUnmarshalMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal inbound json failed: %v (raw=%s)", err, string(raw))
	}
	return m
}

func TestCompileInbound_VLESS_ModesAndTransports(t *testing.T) {
	// 1. VLESS + Reality + Vision (TCP)
	items, err := CompileInbound(InboundSpec{
		Tag:        "vless-reality",
		Protocol:   "vless",
		ListenPort: 443,
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"tls_mode": "reality",
			"flow": "xtls-rprx-vision",
			"transport": "tcp",
			"reality_handshake_server": "www.microsoft.com",
			"reality_handshake_port": 443,
			"reality_private_key": "priv-key-b64",
			"reality_public_key": "pub-key-should-not-emit",
			"reality_short_ids": ["1a2b3c4d"]
		}`),
	})
	if err != nil {
		t.Fatalf("compile vless reality: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 inbound, got %d", len(items))
	}
	ib := mustUnmarshalMap(t, items[0])
	if ib["type"] != "vless" || ib["listen"] != "::" || ib["listen_port"] != float64(443) {
		t.Errorf("unexpected vless basics: %+v", ib)
	}
	if _, hasTransport := ib["transport"]; hasTransport {
		t.Errorf("tcp transport should be omitted")
	}
	users := ib["users"].([]any)
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	u0 := users[0].(map[string]any)
	if u0["name"] != "alice" || u0["uuid"] != "11111111-1111-1111-1111-111111111111" || u0["flow"] != "xtls-rprx-vision" {
		t.Errorf("unexpected vless user[0]: %+v", u0)
	}
	tlsObj := ib["tls"].(map[string]any)
	if tlsObj["enabled"] != true || tlsObj["server_name"] != "www.microsoft.com" {
		t.Errorf("unexpected reality tls: %+v", tlsObj)
	}
	reality := tlsObj["reality"].(map[string]any)
	if _, leaked := reality["public_key"]; leaked {
		t.Errorf("reality_public_key must not be emitted into sing-box inbound JSON")
	}
	hs := reality["handshake"].(map[string]any)
	if hs["server"] != "www.microsoft.com" || hs["server_port"] != float64(443) {
		t.Errorf("unexpected reality handshake: %+v", hs)
	}
	sids := reality["short_id"].([]any)
	if len(sids) != 1 || sids[0] != "1a2b3c4d" {
		t.Errorf("unexpected reality short_id: %+v", sids)
	}

	// 2. VLESS + TLS + WS: flow MUST be stripped even if provided in settings
	wsItems, err := CompileInbound(InboundSpec{
		Tag:        "vless-ws",
		Protocol:   "vless",
		ListenPort: 8443,
		Cert:       sampleCert(),
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"tls_mode": "tls",
			"flow": "xtls-rprx-vision",
			"transport": "ws",
			"ws_path": "/vless-ws",
			"ws_host": "cdn.example.com",
			"server_name": "cdn.example.com"
		}`),
	})
	if err != nil {
		t.Fatalf("compile vless ws: %v", err)
	}
	wsIB := mustUnmarshalMap(t, wsItems[0])
	wsU0 := wsIB["users"].([]any)[0].(map[string]any)
	if _, hasFlow := wsU0["flow"]; hasFlow {
		t.Errorf("expected flow omitted on ws transport, got %+v", wsU0)
	}
	tr := wsIB["transport"].(map[string]any)
	if tr["type"] != "ws" || tr["path"] != "/vless-ws" {
		t.Errorf("unexpected ws transport: %+v", tr)
	}
	if tr["headers"].(map[string]any)["Host"] != "cdn.example.com" {
		t.Errorf("expected Host header in ws transport, got %+v", tr)
	}

	// 3. VLESS + HTTPUpgrade
	huItems, err := CompileInbound(InboundSpec{
		Tag:        "vless-hu",
		Protocol:   "vless",
		ListenPort: 8080,
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"tls_mode": "none",
			"transport": "httpupgrade",
			"ws_path": "/upgrade",
			"ws_host": "edge.example.com"
		}`),
	})
	if err != nil {
		t.Fatalf("compile vless httpupgrade: %v", err)
	}
	huIB := mustUnmarshalMap(t, huItems[0])
	if _, hasTLS := huIB["tls"]; hasTLS {
		t.Errorf("expected no tls when tls_mode=none")
	}
	huTr := huIB["transport"].(map[string]any)
	if huTr["type"] != "httpupgrade" || huTr["path"] != "/upgrade" || huTr["host"] != "edge.example.com" {
		t.Errorf("unexpected httpupgrade transport: %+v", huTr)
	}

	// 4. VLESS + gRPC + TLS
	grpcItems, err := CompileInbound(InboundSpec{
		Tag:        "vless-grpc",
		Protocol:   "vless",
		ListenPort: 9443,
		Cert:       sampleCert(),
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"tls_mode": "tls",
			"transport": "grpc",
			"grpc_service_name": "tun"
		}`),
	})
	if err != nil {
		t.Fatalf("compile vless grpc: %v", err)
	}
	grpcTr := mustUnmarshalMap(t, grpcItems[0])["transport"].(map[string]any)
	if grpcTr["type"] != "grpc" || grpcTr["service_name"] != "tun" {
		t.Errorf("unexpected grpc transport: %+v", grpcTr)
	}
}

func TestCompileInbound_Hysteria2(t *testing.T) {
	items, err := CompileInbound(InboundSpec{
		Tag:        "hy2-in",
		Protocol:   "hysteria2",
		ListenPort: 8443,
		Cert:       sampleCert(),
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"up_mbps": 100,
			"down_mbps": 500,
			"obfs_password": "salamander-secret",
			"masquerade": "https://bing.com",
			"server_ports": "20000:40000",
			"ignore_client_bandwidth": true
		}`),
	})
	if err != nil {
		t.Fatalf("compile hysteria2: %v", err)
	}
	ib := mustUnmarshalMap(t, items[0])
	if ib["type"] != "hysteria2" || ib["up_mbps"] != float64(100) || ib["down_mbps"] != float64(500) || ib["ignore_client_bandwidth"] != true {
		t.Errorf("unexpected hy2 fields: %+v", ib)
	}
	if _, leaked := ib["server_ports"]; leaked {
		t.Errorf("server_ports is subscription metadata only and must NOT appear in sing-box JSON")
	}
	obfs := ib["obfs"].(map[string]any)
	if obfs["type"] != "salamander" || obfs["password"] != "salamander-secret" {
		t.Errorf("unexpected hy2 obfs: %+v", obfs)
	}
	if ib["masquerade"] != "https://bing.com" {
		t.Errorf("unexpected hy2 masquerade: %v", ib["masquerade"])
	}
	tlsObj := ib["tls"].(map[string]any)
	if tlsObj["enabled"] != true || tlsObj["certificate_path"] != sampleCert().CertPath {
		t.Errorf("unexpected hy2 tls: %+v", tlsObj)
	}
}

func TestCompileInbound_TUIC(t *testing.T) {
	items, err := CompileInbound(InboundSpec{
		Tag:        "tuic-in",
		Protocol:   "tuic",
		ListenPort: 9443,
		Cert:       sampleCert(),
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"congestion_control": "bbr",
			"auth_timeout": "3s",
			"zero_rtt_handshake": true,
			"heartbeat": "10s"
		}`),
	})
	if err != nil {
		t.Fatalf("compile tuic: %v", err)
	}
	ib := mustUnmarshalMap(t, items[0])
	if ib["type"] != "tuic" || ib["congestion_control"] != "bbr" || ib["zero_rtt_handshake"] != true || ib["auth_timeout"] != "3s" || ib["heartbeat"] != "10s" {
		t.Errorf("unexpected tuic fields: %+v", ib)
	}
	u0 := ib["users"].([]any)[0].(map[string]any)
	if u0["uuid"] != "11111111-1111-1111-1111-111111111111" || u0["password"] != "alice-pass" {
		t.Errorf("unexpected tuic user: %+v", u0)
	}
	alpn := ib["tls"].(map[string]any)["alpn"].([]any)
	if len(alpn) != 1 || alpn[0] != "h3" {
		t.Errorf("expected tuic tls.alpn=[\"h3\"], got %+v", alpn)
	}
}

func TestCompileInbound_AnyTLS(t *testing.T) {
	items, err := CompileInbound(InboundSpec{
		Tag:        "anytls-in",
		Protocol:   "anytls",
		ListenPort: 8444,
		Cert:       sampleCert(),
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"padding_scheme": ["stop=8", "0=30-30", "1=100-400"]
		}`),
	})
	if err != nil {
		t.Fatalf("compile anytls: %v", err)
	}
	ib := mustUnmarshalMap(t, items[0])
	if ib["type"] != "anytls" {
		t.Errorf("expected type anytls, got %v", ib["type"])
	}
	ps := ib["padding_scheme"].([]any)
	if len(ps) != 3 || ps[0] != "stop=8" {
		t.Errorf("unexpected anytls padding_scheme: %+v", ps)
	}
	if ib["tls"].(map[string]any)["enabled"] != true {
		t.Errorf("expected tls enabled on anytls")
	}
}

func TestCompileInbound_Shadowsocks_AllMethods(t *testing.T) {
	// SS-2022 AES-128-GCM EIH multi-user
	ss128, err := CompileInbound(InboundSpec{
		Tag:        "ss2022-128",
		Protocol:   "shadowsocks",
		ListenPort: 10086,
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"method": "2022-blake3-aes-128-gcm",
			"server_password": "SSSSSSSSSSSSSSSSSSSSSS==",
			"network": "tcp"
		}`),
	})
	if err != nil {
		t.Fatalf("compile ss2022-128: %v", err)
	}
	ib128 := mustUnmarshalMap(t, ss128[0])
	if ib128["password"] != "SSSSSSSSSSSSSSSSSSSSSS==" || ib128["network"] != "tcp" {
		t.Errorf("unexpected ss128 fields: %+v", ib128)
	}
	u128 := ib128["users"].([]any)[0].(map[string]any)
	if u128["password"] != "AAAAAAAAAAAAAAAAAAAAAA==" {
		t.Errorf("expected ss2022_key16 for aes-128-gcm, got %+v", u128)
	}

	// SS-2022 AES-256-GCM EIH multi-user
	ss256, err := CompileInbound(InboundSpec{
		Tag:        "ss2022-256",
		Protocol:   "shadowsocks",
		ListenPort: 10087,
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"method": "2022-blake3-aes-256-gcm",
			"server_password": "SSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSS="
		}`),
	})
	if err != nil {
		t.Fatalf("compile ss2022-256: %v", err)
	}
	u256 := mustUnmarshalMap(t, ss256[0])["users"].([]any)[1].(map[string]any)
	if u256["password"] != "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=" {
		t.Errorf("expected ss2022_key32 for aes-256-gcm, got %+v", u256)
	}

	// Classic AEAD with members
	aead, err := CompileInbound(InboundSpec{
		Tag:        "ss-aead",
		Protocol:   "shadowsocks",
		ListenPort: 10088,
		Members:    sampleMembers(),
		Settings:   json.RawMessage(`{"method": "aes-256-gcm"}`),
	})
	if err != nil {
		t.Fatalf("compile ss-aead: %v", err)
	}
	ibAEAD := mustUnmarshalMap(t, aead[0])
	if _, hasPw := ibAEAD["password"]; hasPw {
		t.Errorf("classic AEAD with users should not emit top-level password")
	}
	if ibAEAD["users"].([]any)[0].(map[string]any)["password"] != "alice-pass" {
		t.Errorf("expected member password for classic AEAD, got %+v", ibAEAD["users"])
	}

	// Classic AEAD fallback without members
	aeadSingle, err := CompileInbound(InboundSpec{
		Tag:        "ss-aead-single",
		Protocol:   "shadowsocks",
		ListenPort: 10089,
		Settings:   json.RawMessage(`{"method": "chacha20-ietf-poly1305", "server_password": "fallback-pw"}`),
	})
	if err != nil {
		t.Fatalf("compile ss-aead single: %v", err)
	}
	ibSingle := mustUnmarshalMap(t, aeadSingle[0])
	if ibSingle["password"] != "fallback-pw" {
		t.Errorf("expected fallback server_password, got %+v", ibSingle)
	}
}

func TestCompileInbound_Trojan(t *testing.T) {
	items, err := CompileInbound(InboundSpec{
		Tag:        "trojan-grpc",
		Protocol:   "trojan",
		ListenPort: 4430,
		Cert:       sampleCert(),
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"transport": "grpc",
			"grpc_service_name": "trojan-svc",
			"server_name": "t.example.com"
		}`),
	})
	if err != nil {
		t.Fatalf("compile trojan: %v", err)
	}
	ib := mustUnmarshalMap(t, items[0])
	if ib["type"] != "trojan" {
		t.Errorf("expected trojan, got %v", ib["type"])
	}
	if ib["tls"].(map[string]any)["server_name"] != "t.example.com" {
		t.Errorf("unexpected trojan tls: %+v", ib["tls"])
	}
	if ib["transport"].(map[string]any)["service_name"] != "trojan-svc" {
		t.Errorf("unexpected trojan transport: %+v", ib["transport"])
	}
}

func TestCompileInbound_ShadowTLS_DualInbound(t *testing.T) {
	// 1. ShadowTLS v3 + Shadowsocks-2022 detour (no fixed detour_port)
	items, err := CompileInbound(InboundSpec{
		Tag:        "stls-ss",
		Protocol:   "shadowtls",
		ListenPort: 443,
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"handshake_server": "www.apple.com",
			"handshake_port": 443,
			"detour_protocol": "shadowsocks",
			"ss_method": "2022-blake3-aes-128-gcm",
			"ss_server_password": "SSSSSSSSSSSSSSSSSSSSSS=="
		}`),
	})
	if err != nil {
		t.Fatalf("compile shadowtls ss: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 coordinated inbounds for shadowtls, got %d", len(items))
	}
	outer := mustUnmarshalMap(t, items[0])
	inner := mustUnmarshalMap(t, items[1])

	if outer["type"] != "shadowtls" || outer["version"] != float64(3) || outer["strict_mode"] != true || outer["detour"] != "stls-ss-detour" {
		t.Errorf("unexpected shadowtls outer: %+v", outer)
	}
	if outer["handshake"].(map[string]any)["server"] != "www.apple.com" {
		t.Errorf("unexpected shadowtls handshake: %+v", outer["handshake"])
	}
	if inner["type"] != "shadowsocks" || inner["tag"] != "stls-ss-detour" || inner["listen"] != "127.0.0.1" {
		t.Errorf("unexpected shadowtls inner detour: %+v", inner)
	}
	if _, hasPort := inner["listen_port"]; hasPort {
		t.Errorf("expected listen_port omitted on inner detour when detour_port==0, got %+v", inner)
	}

	// 2. ShadowTLS v3 + VLESS detour with explicit detour_port and strict_mode=false
	vItems, err := CompileInbound(InboundSpec{
		Tag:        "stls-vless",
		Protocol:   "shadowtls",
		ListenPort: 1443,
		Members:    sampleMembers(),
		Settings: json.RawMessage(`{
			"handshake_server": "www.bing.com",
			"strict_mode": false,
			"detour_protocol": "vless",
			"detour_port": 24433
		}`),
	})
	if err != nil {
		t.Fatalf("compile shadowtls vless: %v", err)
	}
	if len(vItems) != 2 {
		t.Fatalf("expected 2 inbounds, got %d", len(vItems))
	}
	vOuter := mustUnmarshalMap(t, vItems[0])
	vInner := mustUnmarshalMap(t, vItems[1])
	if vOuter["strict_mode"] != false {
		t.Errorf("expected strict_mode=false respected")
	}
	if vInner["type"] != "vless" || vInner["tag"] != "stls-vless-detour" || vInner["listen_port"] != float64(24433) {
		t.Errorf("unexpected vless detour inner: %+v", vInner)
	}
}

func TestCompileInbound_DirectAndLocalProxies(t *testing.T) {
	// Direct
	dItems, err := CompileInbound(InboundSpec{
		Tag:        "direct-fwd",
		Protocol:   "direct",
		Listen:     "0.0.0.0",
		ListenPort: 5000,
		Settings: json.RawMessage(`{
			"override_address": "1.2.3.4",
			"override_port": 8080,
			"network": "tcp"
		}`),
	})
	if err != nil {
		t.Fatalf("compile direct: %v", err)
	}
	dIB := mustUnmarshalMap(t, dItems[0])
	if dIB["type"] != "direct" || dIB["override_address"] != "1.2.3.4" || dIB["override_port"] != float64(8080) || dIB["network"] != "tcp" {
		t.Errorf("unexpected direct inbound: %+v", dIB)
	}

	// Mixed with auth enabled
	for _, proto := range []string{"mixed", "socks", "http"} {
		pItems, err := CompileInbound(InboundSpec{
			Tag:        proto + "-in",
			Protocol:   proto,
			Listen:     "127.0.0.1",
			ListenPort: 1080,
			Members:    sampleMembers(),
			Settings:   json.RawMessage(`{"auth_enabled": true}`),
		})
		if err != nil {
			t.Fatalf("compile %s: %v", proto, err)
		}
		pIB := mustUnmarshalMap(t, pItems[0])
		if pIB["type"] != proto {
			t.Errorf("expected type %s, got %v", proto, pIB["type"])
		}
		u0 := pIB["users"].([]any)[0].(map[string]any)
		if u0["username"] != "alice" || u0["password"] != "alice-pass" {
			t.Errorf("expected username/password for %s, got %+v", proto, u0)
		}
	}

	// Mixed with auth disabled -> no users field
	noAuth, err := CompileInbound(InboundSpec{
		Tag:        "mixed-noauth",
		Protocol:   "mixed",
		ListenPort: 1081,
		Members:    sampleMembers(),
		Settings:   json.RawMessage(`{"auth_enabled": false}`),
	})
	if err != nil {
		t.Fatalf("compile mixed noauth: %v", err)
	}
	if _, hasUsers := mustUnmarshalMap(t, noAuth[0])["users"]; hasUsers {
		t.Errorf("expected users omitted when auth_enabled=false")
	}
}

func TestCompileInbound_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		spec InboundSpec
	}{
		{"empty tag", InboundSpec{Protocol: "mixed", ListenPort: 1080}},
		{"port zero", InboundSpec{Tag: "a", Protocol: "mixed", ListenPort: 0}},
		{"port overflow", InboundSpec{Tag: "a", Protocol: "mixed", ListenPort: 70000}},
		{"unknown protocol", InboundSpec{Tag: "a", Protocol: "vmess-legacy", ListenPort: 443}},
		{"hysteria2 missing cert", InboundSpec{Tag: "h", Protocol: "hysteria2", ListenPort: 443}},
		{"tuic missing cert", InboundSpec{Tag: "t", Protocol: "tuic", ListenPort: 443}},
		{"anytls missing cert", InboundSpec{Tag: "a", Protocol: "anytls", ListenPort: 443}},
		{"trojan missing cert", InboundSpec{Tag: "tr", Protocol: "trojan", ListenPort: 443}},
		{"vless tls missing cert", InboundSpec{Tag: "v", Protocol: "vless", ListenPort: 443, Settings: json.RawMessage(`{"tls_mode":"tls"}`)}},
		{"vless reality missing handshake", InboundSpec{Tag: "vr", Protocol: "vless", ListenPort: 443, Settings: json.RawMessage(`{"tls_mode":"reality","reality_private_key":"k"}`)}},
		{"vless reality missing key", InboundSpec{Tag: "vr2", Protocol: "vless", ListenPort: 443, Settings: json.RawMessage(`{"tls_mode":"reality","reality_handshake_server":"a.com"}`)}},
		{"shadowsocks 2022 missing server password", InboundSpec{Tag: "ss", Protocol: "shadowsocks", ListenPort: 8388, Settings: json.RawMessage(`{"method":"2022-blake3-aes-128-gcm"}`)}},
		{"shadowtls missing handshake", InboundSpec{Tag: "st", Protocol: "shadowtls", ListenPort: 443}},
		{"bad settings json", InboundSpec{Tag: "bad", Protocol: "mixed", ListenPort: 1080, Settings: json.RawMessage(`{broken`)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CompileInbound(tc.spec); err == nil {
				t.Errorf("expected validation error for %s, got nil", tc.name)
			}
		})
	}
}

func TestBuild_MultipleStructuredInboundsAndDuplicateTagCheck(t *testing.T) {
	members := sampleMembers()
	cert := sampleCert()

	raw, err := Build(BuildInput{
		LogLevel: "info",
		ClashAPI: ClashAPIConfig{ListenAddr: "127.0.0.1:19090", Secret: "sec"},
		Inbounds: []InboundSpec{
			{
				Tag:        "vless-in",
				Protocol:   "vless",
				ListenPort: 443,
				Members:    members,
				Settings:   json.RawMessage(`{"tls_mode":"reality","flow":"xtls-rprx-vision","reality_handshake_server":"www.microsoft.com","reality_private_key":"priv","reality_short_ids":["abcd"]}`),
			},
			{
				Tag:        "hy2-in",
				Protocol:   "hysteria2",
				ListenPort: 8443,
				Cert:       cert,
				Members:    members,
			},
			{
				Tag:        "stls-in",
				Protocol:   "shadowtls",
				ListenPort: 2443,
				Members:    members,
				Settings:   json.RawMessage(`{"handshake_server":"www.apple.com","detour_protocol":"shadowsocks","ss_method":"2022-blake3-aes-128-gcm","ss_server_password":"SSSSSSSSSSSSSSSSSSSSSS=="}`),
			},
		},
		RawInbounds: []json.RawMessage{
			json.RawMessage(`{"type":"socks","tag":"raw-socks","listen":"127.0.0.1","listen_port":1080}`),
		},
	})
	if err != nil {
		t.Fatalf("Build with structured inbounds failed: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal built config: %v", err)
	}

	// 1 (vless) + 1 (hy2) + 2 (shadowtls + detour) + 1 (raw socks) = 5 inbounds
	inbounds := doc["inbounds"].([]any)
	if len(inbounds) != 5 {
		t.Fatalf("expected 5 total inbounds, got %d", len(inbounds))
	}

	// Duplicate tag check (colliding with auto-generated shadowtls detour tag!)
	_, err = Build(BuildInput{
		Inbounds: []InboundSpec{
			{
				Tag:        "stls-in",
				Protocol:   "shadowtls",
				ListenPort: 2443,
				Members:    members,
				Settings:   json.RawMessage(`{"handshake_server":"www.apple.com","detour_protocol":"vless"}`),
			},
			{
				Tag:        "stls-in-detour",
				Protocol:   "mixed",
				ListenPort: 1080,
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate inbound tag") {
		t.Fatalf("expected duplicate inbound tag error, got %v", err)
	}
}
