package subgen

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDetectFormatFromUA(t *testing.T) {
	cases := []struct {
		ua   string
		want string
	}{
		{"SFA/1.12.0 (Android)", "singbox"},
		{"SFI/1.12.1", "singbox"},
		{"SFM/1.12.0", "singbox"},
		{"SFT/1.12.0", "singbox"},
		{"sing-box 1.12.0", "singbox"},
		{"singbox-client/1.0", "singbox"},
		{"HiddifyNext/2.0.5", "singbox"},
		{"clash-verge/v2.0.1", "clash"},
		{"Clash.Meta/1.18.0", "clash"},
		{"mihomo/1.19.1", "clash"},
		{"Stash/2.5.0", "clash"},
		{"FlClash/0.8.0", "clash"},
		{"v2rayN/7.1.0", "base64"},
		{"Shadowrocket/2200", "base64"},
		{"Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 Version/17.0 Safari/605.1.15", "base64"},
		{"curl/8.5.0", "base64"},
		{"", "base64"},
	}

	for _, tc := range cases {
		got := DetectFormatFromUA(tc.ua)
		if got != tc.want {
			t.Errorf("DetectFormatFromUA(%q) = %q, want %q", tc.ua, got, tc.want)
		}
	}
}

func sampleClusterInput() RenderInput {
	return RenderInput{
		InstanceID: "jp-master",
		Member: MemberCred{
			ID:          7,
			Name:        "alice",
			UUID:        "11111111-2222-3333-4444-555555555555",
			Password:    "Al1ce#Pass!",
			SS2022Key16: "AAAAAAAAAAAAAAAAAAAAAA==",
			SS2022Key32: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
			SubToken:    "tok-alice",
		},
		Nodes: []NodeEndpoint{
			{
				NodeID:     1,
				NodeName:   "JP-Tokyo",
				PublicIPv4: "203.0.113.10",
				PublicIPv6: "2001:db8::10",
			},
			{
				NodeID:     2,
				NodeName:   "US-LA",
				PublicIPv6: "2001:db8::20",
			},
		},
		Inbounds: []InboundEntry{
			{
				ID:         1,
				NodeID:     1,
				Name:       "VLESS Reality",
				Tag:        "vless-reality",
				Protocol:   "vless",
				ListenPort: 443,
				Settings: json.RawMessage(`{
					"tls_mode": "reality",
					"flow": "xtls-rprx-vision",
					"transport": "tcp",
					"reality_handshake_server": "www.microsoft.com",
					"reality_public_key": "PUBKEY_XYZ123",
					"reality_short_ids": ["a1b2c3d4"]
				}`),
			},
			{
				ID:             2,
				NodeID:         1,
				Name:           "VLESS WS",
				Tag:            "vless-ws",
				Protocol:       "vless",
				ListenPort:     8443,
				CertSelfSigned: true,
				CertDomains:    []string{"*.jp.example.com", "ws.jp.example.com"},
				Settings: json.RawMessage(`{
					"tls_mode": "tls",
					"transport": "ws",
					"ws_path": "/chat",
					"ws_host": "cdn.example.com"
				}`),
			},
			{
				ID:          3,
				NodeID:      1,
				Name:        "VLESS HTTPUpgrade",
				Tag:         "vless-hu",
				Protocol:    "vless",
				ListenPort:  8444,
				CertDomains: []string{"hu.jp.example.com"},
				Settings: json.RawMessage(`{
					"tls_mode": "tls",
					"transport": "httpupgrade",
					"ws_path": "/upgrade",
					"ws_host": "hu.jp.example.com"
				}`),
			},
			{
				ID:          4,
				NodeID:      1,
				Name:        "VLESS gRPC",
				Tag:         "vless-grpc",
				Protocol:    "vless",
				ListenPort:  8445,
				CertDomains: []string{"grpc.jp.example.com"},
				Settings: json.RawMessage(`{
					"tls_mode": "tls",
					"transport": "grpc",
					"grpc_service_name": "tun"
				}`),
			},
			{
				ID:              5,
				NodeID:          1,
				Name:            "Hysteria2",
				Tag:             "hy2-in",
				Protocol:        "hysteria2",
				ListenPort:      4430,
				CertSelfSigned:  true,
				CertFingerprint: "SHA256:ABCDEF1234",
				CertDomains:     []string{"*.hy2.example.com", "hy2.example.com"},
				ExtraEntries: []ExtraEntry{
					{Name: "GZ Relay", Host: "198.51.100.5", Port: 24430},
					{Name: "SH Relay", Host: "198.51.100.6", Port: 34430},
				},
				Settings: json.RawMessage(`{
					"up_mbps": 100,
					"down_mbps": 500,
					"obfs_password": "salamander-secret",
					"server_ports": "20000-40000"
				}`),
			},
			{
				ID:             6,
				NodeID:         2,
				Name:           "TUIC v5",
				Tag:            "tuic-in",
				Protocol:       "tuic",
				ListenPort:     5443,
				CertSelfSigned: true,
				Settings: json.RawMessage(`{
					"congestion_control": "bbr",
					"zero_rtt_handshake": true,
					"heartbeat": "10s"
				}`),
			},
			{
				ID:          7,
				NodeID:      1,
				Name:        "AnyTLS",
				Tag:         "anytls-in",
				Protocol:    "anytls",
				ListenPort:  6443,
				CertDomains: []string{"any.jp.example.com"},
				Settings:    json.RawMessage(`{"padding_scheme":["stop=8"]}`),
			},
			{
				ID:         8,
				NodeID:     1,
				Name:       "SS2022-128",
				Tag:        "ss2022-128",
				Protocol:   "shadowsocks",
				ListenPort: 8388,
				Settings: json.RawMessage(`{
					"method": "2022-blake3-aes-128-gcm",
					"server_password": "SERVERKEY16BYTES======"
				}`),
			},
			{
				ID:         9,
				NodeID:     1,
				Name:       "SS2022-256",
				Tag:        "ss2022-256",
				Protocol:   "shadowsocks",
				ListenPort: 8389,
				Settings: json.RawMessage(`{
					"method": "2022-blake3-aes-256-gcm",
					"server_password": "SERVERKEY32BYTES==========================="
				}`),
			},
			{
				ID:         10,
				NodeID:     2,
				Name:       "SS Classic",
				Tag:        "ss-classic",
				Protocol:   "shadowsocks",
				ListenPort: 8390,
				Settings: json.RawMessage(`{
					"method": "aes-256-gcm",
					"network": "tcp"
				}`),
			},
			{
				ID:           11,
				NodeID:       1,
				Name:         "Trojan gRPC",
				Tag:          "trojan-grpc",
				Protocol:     "trojan",
				ListenPort:   9443,
				ExternalHost: "trojan-edge.example.com",
				ExternalPort: 443,
				CertDomains:  []string{"origin.example.com"},
				Settings: json.RawMessage(`{
					"transport": "grpc",
					"grpc_service_name": "trgrpc",
					"server_name": "trojan-sni.example.com"
				}`),
			},
			{
				ID:         12,
				NodeID:     1,
				Name:       "ShadowTLS SS",
				Tag:        "stls-ss",
				Protocol:   "shadowtls",
				ListenPort: 10443,
				Settings: json.RawMessage(`{
					"handshake_server": "www.apple.com",
					"detour_protocol": "shadowsocks",
					"ss_method": "2022-blake3-aes-128-gcm",
					"ss_server_password": "STLS_SRV_KEY16========"
				}`),
			},
			{
				ID:         13,
				NodeID:     1,
				Name:       "ShadowTLS VLESS",
				Tag:        "stls-vless",
				Protocol:   "shadowtls",
				ListenPort: 10444,
				Settings: json.RawMessage(`{
					"handshake_server": "www.bing.com",
					"detour_protocol": "vless"
				}`),
			},
			{
				ID:         14,
				NodeID:     1,
				Name:       "Socks5 Auth",
				Tag:        "socks-in",
				Protocol:   "socks",
				ListenPort: 1080,
				Settings:   json.RawMessage(`{"auth_enabled": true}`),
			},
			{
				ID:         15,
				NodeID:     1,
				Name:       "Mixed Open",
				Tag:        "mixed-in",
				Protocol:   "mixed",
				ListenPort: 1081,
				Settings:   json.RawMessage(`{"auth_enabled": false}`),
			},
			{
				ID:         16,
				NodeID:     1,
				Name:       "HTTP Auth",
				Tag:        "http-in",
				Protocol:   "http",
				ListenPort: 8080,
				Settings:   json.RawMessage(`{"auth_enabled": true}`),
			},
			{
				ID:         17,
				NodeID:     1,
				Name:       "Port Forwarder",
				Tag:        "direct-fwd",
				Protocol:   "direct",
				ListenPort: 2222,
				Settings:   json.RawMessage(`{"override_address":"10.0.0.1","override_port":22}`),
			},
		},
	}
}

func TestRenderAllProtocolsAndFormats(t *testing.T) {
	in := sampleClusterInput()
	// 16 non-direct inbounds + 2 extra_entries on Hysteria2 = 18 nodes
	const expectedNodeCount = 18

	// 1. URI format
	uriOut, err := Render(in, "uri")
	if err != nil {
		t.Fatalf("Render(uri) failed: %v", err)
	}
	if uriOut.Format != "uri" || uriOut.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected uri output metadata: %+v", uriOut)
	}
	if len(uriOut.Nodes) != expectedNodeCount {
		t.Fatalf("expected %d nodes, got %d", expectedNodeCount, len(uriOut.Nodes))
	}
	lines := strings.Split(uriOut.Content, "\n")
	if len(lines) != expectedNodeCount {
		t.Fatalf("expected %d URI lines, got %d", expectedNodeCount, len(lines))
	}

	// Verify direct inbound was skipped and extra entries were expanded
	nodeByName := make(map[string]RenderedNode, len(uriOut.Nodes))
	for _, n := range uriOut.Nodes {
		if n.Protocol == "direct" {
			t.Fatalf("direct inbound should have been skipped: %+v", n)
		}
		if _, dup := nodeByName[n.Name]; dup {
			t.Fatalf("duplicate rendered node name %q", n.Name)
		}
		nodeByName[n.Name] = n
	}

	// Check VLESS Reality URI
	vlessReality := nodeByName["[JP-Tokyo] VLESS Reality"]
	if !strings.HasPrefix(vlessReality.URI, "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?") {
		t.Errorf("unexpected VLESS Reality URI: %s", vlessReality.URI)
	}
	for _, want := range []string{"security=reality", "pbk=PUBKEY_XYZ123", "sid=a1b2c3d4", "flow=xtls-rprx-vision", "sni=www.microsoft.com", "#%5BJP-Tokyo%5D%20VLESS%20Reality"} {
		if !strings.Contains(vlessReality.URI, want) {
			t.Errorf("VLESS Reality URI missing %q: %s", want, vlessReality.URI)
		}
	}
	if idx := strings.LastIndex(vlessReality.URI, "#"); idx >= 0 && strings.Contains(vlessReality.URI[idx:], "+") {
		t.Errorf("URI fragment must encode spaces as %%20, not '+': %s", vlessReality.URI)
	}

	// Check Hysteria2 primary + extra entries
	hy2Primary := nodeByName["[JP-Tokyo] Hysteria2"]
	if !strings.Contains(hy2Primary.URI, "@hy2.example.com:4430/") {
		t.Errorf("expected Hy2 primary host resolved to non-wildcard cert domain hy2.example.com, got: %s", hy2Primary.URI)
	}
	for _, want := range []string{"obfs=salamander", "obfs-password=salamander-secret", "mport=20000-40000", "insecure=1"} {
		if !strings.Contains(hy2Primary.URI, want) {
			t.Errorf("Hysteria2 URI missing %q: %s", want, hy2Primary.URI)
		}
	}
	hy2Extra1, ok := nodeByName["[JP-Tokyo] Hysteria2 - GZ Relay"]
	if !ok || !strings.Contains(hy2Extra1.URI, "@198.51.100.5:24430/") {
		t.Errorf("Hysteria2 GZ Relay extra entry mismatch: %+v", hy2Extra1)
	}
	hy2Extra2, ok := nodeByName["[JP-Tokyo] Hysteria2 - SH Relay"]
	if !ok || !strings.Contains(hy2Extra2.URI, "@198.51.100.6:34430/") {
		t.Errorf("Hysteria2 SH Relay extra entry mismatch: %+v", hy2Extra2)
	}

	// Check TUIC on IPv6-only node
	tuicNode := nodeByName["[US-LA] TUIC v5"]
	if !strings.Contains(tuicNode.URI, "@[2001:db8::20]:5443?") || !strings.Contains(tuicNode.URI, "reduce_rtt=1") || !strings.Contains(tuicNode.URI, "allow_insecure=1") {
		t.Errorf("unexpected TUIC IPv6 URI: %s", tuicNode.URI)
	}

	// Check SS-2022 URI contains method:server_password:user_key
	ss128 := nodeByName["[JP-Tokyo] SS2022-128"]
	uSS, err := url.Parse(ss128.URI)
	if err != nil {
		t.Fatalf("parse SS URI: %v", err)
	}
	decSS, err := base64.StdEncoding.DecodeString(uSS.User.Username())
	if err != nil {
		t.Fatalf("decode SS userinfo: %v", err)
	}
	wantSS := "2022-blake3-aes-128-gcm:SERVERKEY16BYTES======:AAAAAAAAAAAAAAAAAAAAAA=="
	if string(decSS) != wantSS {
		t.Errorf("SS-2022 decoded userinfo = %q, want %q", string(decSS), wantSS)
	}

	// Check Trojan external host/port override
	trojanNode := nodeByName["[JP-Tokyo] Trojan gRPC"]
	if !strings.Contains(trojanNode.URI, "@trojan-edge.example.com:443?") || !strings.Contains(trojanNode.URI, "sni=trojan-sni.example.com") {
		t.Errorf("unexpected Trojan URI: %s", trojanNode.URI)
	}

	// Check ShadowTLS SS URI plugin
	stlsSS := nodeByName["[JP-Tokyo] ShadowTLS SS"]
	if !strings.HasPrefix(stlsSS.URI, "ss://") || !strings.Contains(stlsSS.URI, "shadow-tls") || !strings.Contains(stlsSS.URI, "www.apple.com") {
		t.Errorf("unexpected ShadowTLS SS URI: %s", stlsSS.URI)
	}

	// 2. Base64 format
	b64Out, err := Render(in, "base64")
	if err != nil {
		t.Fatalf("Render(base64) failed: %v", err)
	}
	decB64, err := base64.StdEncoding.DecodeString(b64Out.Content)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if string(decB64) != uriOut.Content {
		t.Errorf("base64 decoded content does not match uri content")
	}

	// 3. sing-box 1.12+ JSON format
	sbOut, err := Render(in, "singbox")
	if err != nil {
		t.Fatalf("Render(singbox) failed: %v", err)
	}
	if sbOut.ContentType != "application/json; charset=utf-8" || !strings.HasSuffix(sbOut.Filename, ".json") {
		t.Fatalf("unexpected singbox output metadata: %+v", sbOut)
	}

	var sbDoc map[string]any
	if err := json.Unmarshal([]byte(sbOut.Content), &sbDoc); err != nil {
		t.Fatalf("singbox JSON invalid: %v\n%s", err, sbOut.Content)
	}

	// Verify modern 1.12+ sections
	for _, key := range []string{"log", "dns", "inbounds", "outbounds", "route", "experimental"} {
		if _, ok := sbDoc[key]; !ok {
			t.Errorf("singbox config missing top-level key %q", key)
		}
	}
	// Verify inbounds mixed:7890
	sbInbounds := sbDoc["inbounds"].([]any)
	ib0 := sbInbounds[0].(map[string]any)
	if ib0["type"] != "mixed" || int(ib0["listen_port"].(float64)) != 7890 {
		t.Errorf("unexpected singbox inbound[0]: %+v", ib0)
	}
	// Verify route rules & rule_set
	sbRoute := sbDoc["route"].(map[string]any)
	if sbRoute["final"] != "proxy" {
		t.Errorf("expected route.final=proxy, got %v", sbRoute["final"])
	}
	rules := sbRoute["rules"].([]any)
	hasSniff, hasHijackDNS, hasPrivate := false, false, false
	for _, r := range rules {
		rm := r.(map[string]any)
		if rm["action"] == "sniff" {
			hasSniff = true
		}
		if rm["action"] == "hijack-dns" {
			hasHijackDNS = true
		}
		if rm["ip_is_private"] == true && rm["outbound"] == "direct" {
			hasPrivate = true
		}
	}
	if !hasSniff || !hasHijackDNS || !hasPrivate {
		t.Errorf("missing modern route rules: sniff=%v hijack=%v private=%v", hasSniff, hasHijackDNS, hasPrivate)
	}
	ruleSets := sbRoute["rule_set"].([]any)
	if len(ruleSets) != 2 {
		t.Errorf("expected 2 remote .srs rule_sets, got %d", len(ruleSets))
	}

	// Verify outbounds including ShadowTLS v3 pair and Hy2 server_ports
	sbOutbounds := sbDoc["outbounds"].([]any)
	obByTag := make(map[string]map[string]any, len(sbOutbounds))
	for _, o := range sbOutbounds {
		om := o.(map[string]any)
		tag := om["tag"].(string)
		if _, dup := obByTag[tag]; dup {
			t.Fatalf("duplicate singbox outbound tag %q", tag)
		}
		obByTag[tag] = om
	}

	// ShadowTLS SS pair
	stlsInner := obByTag["[JP-Tokyo] ShadowTLS SS"]
	if stlsInner == nil || stlsInner["type"] != "shadowsocks" {
		t.Fatalf("missing inner shadowsocks outbound for ShadowTLS SS: %+v", stlsInner)
	}
	detourTag, _ := stlsInner["detour"].(string)
	stlsOuter := obByTag[detourTag]
	if stlsOuter == nil || stlsOuter["type"] != "shadowtls" || int(stlsOuter["version"].(float64)) != 3 {
		t.Fatalf("missing outer shadowtls v3 outbound %q: %+v", detourTag, stlsOuter)
	}
	// Ensure helper outer tag is NOT in proxy/auto groups
	proxyGroup := obByTag["proxy"]["outbounds"].([]any)
	for _, m := range proxyGroup {
		if m.(string) == detourTag {
			t.Errorf("helper shadowtls tag %q should not be listed directly in proxy selector", detourTag)
		}
	}
	// Verify Hysteria2 server_ports uses colon notation
	hy2Ob := obByTag["[JP-Tokyo] Hysteria2"]
	spList := hy2Ob["server_ports"].([]any)
	if len(spList) != 1 || spList[0].(string) != "20000:40000" {
		t.Errorf("expected singbox hy2 server_ports [\"20000:40000\"], got %+v", spList)
	}

	// 4. Clash (Mihomo) YAML format
	clashOut, err := Render(in, "clash")
	if err != nil {
		t.Fatalf("Render(clash) failed: %v", err)
	}
	if clashOut.ContentType != "application/yaml; charset=utf-8" || !strings.HasSuffix(clashOut.Filename, ".yaml") {
		t.Fatalf("unexpected clash output metadata: %+v", clashOut)
	}

	var clashDoc struct {
		MixedPort   int              `yaml:"mixed-port"`
		Mode        string           `yaml:"mode"`
		Proxies     []map[string]any `yaml:"proxies"`
		ProxyGroups []struct {
			Name    string   `yaml:"name"`
			Type    string   `yaml:"type"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal([]byte(clashOut.Content), &clashDoc); err != nil {
		t.Fatalf("clash YAML invalid: %v\n%s", err, clashOut.Content)
	}
	if clashDoc.MixedPort != 7890 || clashDoc.Mode != "rule" {
		t.Errorf("unexpected clash top-level fields: mixed-port=%d mode=%q", clashDoc.MixedPort, clashDoc.Mode)
	}
	if len(clashDoc.Proxies) != expectedNodeCount {
		t.Fatalf("expected %d clash proxies, got %d", expectedNodeCount, len(clashDoc.Proxies))
	}
	if len(clashDoc.ProxyGroups) != 2 || clashDoc.ProxyGroups[0].Name != "🚀 节点选择" || clashDoc.ProxyGroups[1].Name != "♻️ 自动选择" {
		t.Errorf("unexpected clash proxy groups: %+v", clashDoc.ProxyGroups)
	}
	if len(clashDoc.Rules) == 0 {
		t.Errorf("expected non-empty clash rules")
	}

	clashByName := make(map[string]map[string]any, len(clashDoc.Proxies))
	for _, p := range clashDoc.Proxies {
		clashByName[p["name"].(string)] = p
	}
	if p := clashByName["[JP-Tokyo] Hysteria2"]; p["ports"] != "20000-40000" || p["obfs"] != "salamander" {
		t.Errorf("unexpected clash Hysteria2 proxy: %+v", p)
	}
	if p := clashByName["[JP-Tokyo] ShadowTLS SS"]; p["plugin"] != "shadow-tls" {
		t.Errorf("unexpected clash ShadowTLS proxy: %+v", p)
	}
	if p := clashByName["[JP-Tokyo] VLESS HTTPUpgrade"]; p["network"] != "ws" {
		t.Errorf("unexpected clash VLESS HTTPUpgrade proxy: %+v", p)
	}
}

func TestHostResolutionPriorityUniqueNamesAndEdgeCases(t *testing.T) {
	in := RenderInput{
		InstanceID: "edge",
		Member:     MemberCred{Name: "bob", UUID: "u-1", Password: "p-1"},
		Nodes: []NodeEndpoint{
			{NodeID: 1, NodeName: "N1", PublicIPv4: "198.51.100.1", PublicIPv6: "2001:db8::1"},
			{NodeID: 2, NodeName: "N2"}, // no IP -> fallback 127.0.0.1
		},
		Inbounds: []InboundEntry{
			// Same name twice -> must deduplicate cleanly
			{ID: 1, NodeID: 1, Name: "Dup", Protocol: "anytls", ListenPort: 443, CertDomains: []string{"*.wild.com", "exact.com"}},
			{ID: 2, NodeID: 1, Name: "Dup", Protocol: "anytls", ListenPort: 444, ExternalHost: "ext.com", CertDomains: []string{"exact.com"}},
			// Fallback to 127.0.0.1 when node has no IPs
			{ID: 3, NodeID: 2, Name: "LocalOnly", Protocol: "socks", ListenPort: 1080},
		},
	}

	out, err := Render(in, "uri")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if len(out.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(out.Nodes))
	}
	if out.Nodes[0].Name != "[N1] Dup" || !strings.Contains(out.Nodes[0].URI, "@exact.com:443/") {
		t.Errorf("node 0 mismatch: %+v", out.Nodes[0])
	}
	if out.Nodes[1].Name != "[N1] Dup #2" || !strings.Contains(out.Nodes[1].URI, "@ext.com:444/") {
		t.Errorf("node 1 mismatch: %+v", out.Nodes[1])
	}
	if !strings.Contains(out.Nodes[2].URI, "127.0.0.1:1080") {
		t.Errorf("expected 127.0.0.1 fallback, got: %s", out.Nodes[2].URI)
	}

	// Empty inbounds still yields valid JSON & YAML
	emptyIn := RenderInput{InstanceID: "empty", Member: MemberCred{Name: "empty"}}
	for _, f := range []string{"uri", "base64", "singbox", "clash"} {
		res, err := Render(emptyIn, f)
		if err != nil {
			t.Fatalf("Render(empty, %s) failed: %v", f, err)
		}
		if len(res.Nodes) != 0 {
			t.Errorf("expected 0 nodes for %s", f)
		}
	}

	// Unsupported format error
	if _, err := Render(emptyIn, "quantum"); err == nil {
		t.Errorf("expected error for unsupported format")
	}
}

func TestRenderLoonAllProtocols(t *testing.T) {
	in := sampleClusterInput()
	nodes, err := RenderLoon(in)
	if err != nil {
		t.Fatalf("RenderLoon failed: %v", err)
	}
	// 16 non-direct inbounds + 2 extra_entries on Hysteria2 = 18 nodes
	if len(nodes) != 18 {
		t.Fatalf("expected 18 Loon nodes, got %d", len(nodes))
	}

	byName := make(map[string]LoonRenderedNode, len(nodes))
	for _, n := range nodes {
		byName[n.Name] = n
	}

	// Verify VLESS Reality Loon syntax
	vlessReality := byName["[JP-Tokyo] VLESS Reality"].Line
	wantVLESS := `[JP-Tokyo] VLESS Reality = VLESS, 203.0.113.10, 443, "11111111-2222-3333-4444-555555555555", transport=tcp, flow=xtls-rprx-vision, public-key="PUBKEY_XYZ123", short-id=a1b2c3d4, over-tls=true, sni=www.microsoft.com, udp=true`
	if vlessReality != wantVLESS {
		t.Errorf("VLESS Reality Loon mismatch:\n got: %s\nwant: %s", vlessReality, wantVLESS)
	}

	// Verify VLESS WS + self-signed TLS
	vlessWS := byName["[JP-Tokyo] VLESS WS"].Line
	for _, sub := range []string{"= VLESS, ws.jp.example.com, 8443", "transport=ws", "path=/chat", "host=cdn.example.com", "over-tls=true", "skip-cert-verify=true", "udp=true"} {
		if !strings.Contains(vlessWS, sub) {
			t.Errorf("VLESS WS Loon missing %q: %s", sub, vlessWS)
		}
	}

	// Verify Hysteria2 with salamander obfs & self-signed cert
	hy2 := byName["[JP-Tokyo] Hysteria2"].Line
	for _, sub := range []string{"= Hysteria2, hy2.example.com, 4430", `salamander-password="salamander-secret"`, "sni=hy2.example.com", "skip-cert-verify=true", "udp=true"} {
		if !strings.Contains(hy2, sub) {
			t.Errorf("Hysteria2 Loon missing %q: %s", sub, hy2)
		}
	}

	// Verify TUIC v5, AnyTLS, Shadowsocks SS-2022, Trojan, SOCKS5, HTTP
	tuic := byName["[US-LA] TUIC v5"].Line
	if !strings.Contains(tuic, "= TUIC, 2001:db8::20, 5443") || !strings.Contains(tuic, "version=5") || !strings.Contains(tuic, "skip-cert-verify=true") {
		t.Errorf("unexpected TUIC Loon line: %s", tuic)
	}
	anytls := byName["[JP-Tokyo] AnyTLS"].Line
	if !strings.Contains(anytls, "= AnyTLS, any.jp.example.com, 6443") || !strings.Contains(anytls, "skip-cert-verify=false") {
		t.Errorf("unexpected AnyTLS Loon line: %s", anytls)
	}
	ss128 := byName["[JP-Tokyo] SS2022-128"].Line
	if !strings.Contains(ss128, `= Shadowsocks, 203.0.113.10, 8388, 2022-blake3-aes-128-gcm, "SERVERKEY16BYTES======:AAAAAAAAAAAAAAAAAAAAAA==", udp=true`) {
		t.Errorf("unexpected Shadowsocks Loon line: %s", ss128)
	}
	trojan := byName["[JP-Tokyo] Trojan gRPC"].Line
	if !strings.Contains(trojan, "= Trojan, trojan-edge.example.com, 443") || !strings.Contains(trojan, "sni=trojan-sni.example.com") {
		t.Errorf("unexpected Trojan Loon line: %s", trojan)
	}
	socksAuth := byName["[JP-Tokyo] Socks5 Auth"].Line
	if !strings.Contains(socksAuth, "= SOCKS5, 203.0.113.10, 1080, alice, Al1ce#Pass!") {
		t.Errorf("unexpected SOCKS5 Loon line: %s", socksAuth)
	}
	httpAuth := byName["[JP-Tokyo] HTTP Auth"].Line
	if !strings.Contains(httpAuth, "= HTTP, 203.0.113.10, 8080, alice, Al1ce#Pass!") {
		t.Errorf("unexpected HTTP Loon line: %s", httpAuth)
	}

	// Verify Render(...) populates RenderedNode.Loon identically
	uriOut, err := Render(in, "uri")
	if err != nil {
		t.Fatalf("Render(uri) failed: %v", err)
	}
	if uriOut.Nodes[0].Loon != nodes[0].Line {
		t.Errorf("Render().Nodes[0].Loon=%q != RenderLoon[0].Line=%q", uriOut.Nodes[0].Loon, nodes[0].Line)
	}
}
