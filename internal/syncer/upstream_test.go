package syncer

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeRawMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("failed to unmarshal Raw JSON %s: %v", string(raw), err)
	}
	return m
}

func assertUniqueValidTags(t *testing.T, nodes []ParsedOutbound) {
	t.Helper()
	seen := make(map[string]struct{}, len(nodes))
	for i, n := range nodes {
		if strings.TrimSpace(n.Tag) == "" {
			t.Fatalf("node[%d] has empty Tag", i)
		}
		if _, dup := seen[n.Tag]; dup {
			t.Fatalf("duplicate node Tag %q", n.Tag)
		}
		seen[n.Tag] = struct{}{}
		m := decodeRawMap(t, n.Raw)
		if got, _ := m["tag"].(string); got != n.Tag {
			t.Fatalf("node[%d] Raw.tag=%q != ParsedOutbound.Tag=%q", i, got, n.Tag)
		}
		if gotType, _ := m["type"].(string); gotType != n.Type {
			t.Fatalf("node[%d] Raw.type=%q != ParsedOutbound.Type=%q", i, gotType, n.Type)
		}
	}
}

func TestParseSubscription_SingBoxJSON(t *testing.T) {
	payload := "\xef\xbb\xbf" + `{
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "block", "tag": "block"},
    {"type": "dns", "tag": "dns-out"},
    {"type": "selector", "tag": "proxy", "outbounds": ["hk-vless"]},
    {"type": "urltest", "tag": "auto", "outbounds": ["hk-vless"]},
    {"type": "vless", "tag": "HK VLESS", "server": "1.1.1.1", "server_port": 443, "uuid": "11111111-1111-1111-1111-111111111111", "flow": "xtls-rprx-vision"},
    {"type": "vmess", "tag": "JP VMess", "server": "2.2.2.2", "server_port": 8443, "uuid": "22222222-2222-2222-2222-222222222222"},
    {"type": "trojan", "tag": "SG Trojan", "server": "3.3.3.3", "server_port": 443, "password": "secret"},
    {"type": "shadowsocks", "tag": "US SS", "server": "4.4.4.4", "server_port": 8388, "method": "aes-256-gcm", "password": "pw"},
    {"type": "hysteria2", "tag": "DE Hy2", "server": "5.5.5.5", "server_port": 443, "password": "hy2"},
    {"type": "tuic", "tag": "UK TUIC", "server": "6.6.6.6", "server_port": 443, "uuid": "33333333-3333-3333-3333-333333333333", "password": "tpw"},
    {"type": "anytls", "tag": "KR AnyTLS", "server": "7.7.7.7", "server_port": 443, "password": "apw"},
    {"type": "socks", "tag": "Local Socks", "server": "8.8.8.8", "server_port": 1080},
    {"type": "http", "tag": "Corp HTTP", "server": "9.9.9.9", "server_port": 8080},
    {"type": "ssh", "tag": "Jump SSH", "server": "10.0.0.1", "server_port": 22, "user": "root"}
  ]
}`

	res, err := ParseSubscription([]byte(payload), FetchOptions{TagPrefix: "sb-"})
	if err != nil {
		t.Fatalf("ParseSubscription failed: %v", err)
	}
	if res.Format != "singbox" {
		t.Fatalf("expected format singbox, got %q", res.Format)
	}
	if len(res.Nodes) != 10 {
		t.Fatalf("expected 10 real proxy nodes (meta outbounds skipped), got %d", len(res.Nodes))
	}
	assertUniqueValidTags(t, res.Nodes)

	if res.Nodes[0].Tag != "sb-HK-VLESS" || res.Nodes[0].OriginalName != "HK VLESS" || res.Nodes[0].Server != "1.1.1.1" || res.Nodes[0].ServerPort != 443 {
		t.Fatalf("unexpected first node: %+v", res.Nodes[0])
	}
	if res.Nodes[9].Type != "ssh" || res.Nodes[9].ServerPort != 22 {
		t.Fatalf("unexpected ssh node: %+v", res.Nodes[9])
	}
}

func TestParseSubscription_ClashYAML(t *testing.T) {
	clashYAML := `
port: 7890
proxies:
  - name: "HK Reality"
    type: vless
    server: hk.example.com
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
    tls: true
    servername: www.microsoft.com
    flow: xtls-rprx-vision
    client-fingerprint: chrome
    reality-opts:
      public-key: pubkey123
      short-id: abcd1234

  - name: "JP VLESS WS"
    type: vless
    server: jp.example.com
    port: 443
    uuid: 22222222-2222-2222-2222-222222222222
    tls: true
    skip-cert-verify: true
    servername: jp.cdn.example.com
    network: ws
    ws-opts:
      path: /vless?ed=2048
      headers:
        Host: jp.cdn.example.com

  - name: "SG VMess gRPC"
    type: vmess
    server: sg.example.com
    port: 443
    uuid: 33333333-3333-3333-3333-333333333333
    alterId: 0
    cipher: aes-128-gcm
    udp: false
    tls: true
    servername: sg.example.com
    network: grpc
    grpc-opts:
      grpc-service-name: my-grpc

  - name: "US Trojan Upgrade"
    type: trojan
    server: us.example.com
    port: 443
    password: trojan-password
    sni: us.example.com
    alpn:
      - h2
      - http/1.1
    network: httpupgrade
    httpupgrade-opts:
      path: /upgrade
      host: cdn.us.example.com

  - name: "TW SS Obfs"
    type: ss
    server: tw.example.com
    port: 8388
    cipher: 2022-blake3-aes-128-gcm
    password: serverkey:userkey
    udp-over-tcp: true
    plugin: obfs
    plugin-opts:
      mode: tls
      host: www.bing.com

  - name: "DE Hysteria2"
    type: hysteria2
    server: de.example.com
    port: 443
    password: hy2-password
    ports: "20000-30000,40000"
    hop-interval: 30
    up: "100 Mbps"
    down: "250 Mbps"
    obfs: salamander
    obfs-password: obfs-secret
    sni: de.example.com
    skip-cert-verify: true
    alpn:
      - h3

  - name: "UK TUIC"
    type: tuic
    server: uk.example.com
    port: 8443
    uuid: 44444444-4444-4444-4444-444444444444
    password: tuic-password
    congestion-controller: bbr
    udp-relay-mode: native
    reduce-rtt: true
    heartbeat-interval: 10000
    disable-sni: true
    sni: uk.example.com
    alpn:
      - h3

  - name: "KR AnyTLS"
    type: anytls
    server: kr.example.com
    port: 443
    password: anytls-password
    sni: kr.example.com
    client-fingerprint: safari
    idle-session-check-interval: 30
    idle-session-timeout: 60
    min-idle-session: 2

  - name: "Relay Socks5"
    type: socks5
    server: 127.0.0.1
    port: 1080
    username: alice
    password: bob
    udp: false

  - name: "Corp HTTPS"
    type: http
    server: proxy.corp.local
    port: 8443
    username: u1
    password: p1
    tls: true
    sni: proxy.corp.local
    skip-cert-verify: true

  - name: "NL VLESS H2"
    type: vless
    server: nl.example.com
    port: 443
    uuid: 55555555-5555-5555-5555-555555555555
    tls: true
    network: h2
    h2-opts:
      host:
        - h2.example.com
      path: /h2path
`

	res, err := ParseSubscription([]byte(clashYAML), FetchOptions{TagPrefix: "sub-1-"})
	if err != nil {
		t.Fatalf("ParseSubscription clash failed: %v", err)
	}
	if res.Format != "clash" {
		t.Fatalf("expected format clash, got %q", res.Format)
	}
	if len(res.Nodes) != 11 {
		t.Fatalf("expected 11 nodes, got %d", len(res.Nodes))
	}
	assertUniqueValidTags(t, res.Nodes)

	// 1. VLESS Reality
	m0 := decodeRawMap(t, res.Nodes[0].Raw)
	if m0["flow"] != "xtls-rprx-vision" {
		t.Fatalf("expected flow xtls-rprx-vision, got %v", m0["flow"])
	}
	tls0 := m0["tls"].(map[string]any)
	if tls0["enabled"] != true || tls0["server_name"] != "www.microsoft.com" {
		t.Fatalf("unexpected vless reality tls: %#v", tls0)
	}
	utls0 := tls0["utls"].(map[string]any)
	if utls0["fingerprint"] != "chrome" {
		t.Fatalf("unexpected utls: %#v", utls0)
	}
	reality0 := tls0["reality"].(map[string]any)
	if reality0["public_key"] != "pubkey123" || reality0["short_id"] != "abcd1234" {
		t.Fatalf("unexpected reality: %#v", reality0)
	}

	// 2. VLESS WS with ?ed=2048
	m1 := decodeRawMap(t, res.Nodes[1].Raw)
	tr1 := m1["transport"].(map[string]any)
	if tr1["type"] != "ws" || tr1["path"] != "/vless" || tr1["max_early_data"] != float64(2048) {
		t.Fatalf("unexpected ws transport: %#v", tr1)
	}
	if m1["tls"].(map[string]any)["insecure"] != true {
		t.Fatalf("expected insecure=true on JP VLESS WS")
	}

	// 3. VMess gRPC + udp: false -> network: tcp
	m2 := decodeRawMap(t, res.Nodes[2].Raw)
	if m2["security"] != "aes-128-gcm" || m2["network"] != "tcp" {
		t.Fatalf("unexpected vmess fields: %#v", m2)
	}
	tr2 := m2["transport"].(map[string]any)
	if tr2["type"] != "grpc" || tr2["service_name"] != "my-grpc" {
		t.Fatalf("unexpected grpc transport: %#v", tr2)
	}

	// 4. Trojan HTTPUpgrade
	m3 := decodeRawMap(t, res.Nodes[3].Raw)
	tr3 := m3["transport"].(map[string]any)
	if tr3["type"] != "httpupgrade" || tr3["host"] != "cdn.us.example.com" || tr3["path"] != "/upgrade" {
		t.Fatalf("unexpected httpupgrade transport: %#v", tr3)
	}

	// 5. SS obfs + udp_over_tcp
	m4 := decodeRawMap(t, res.Nodes[4].Raw)
	if m4["type"] != "shadowsocks" || m4["method"] != "2022-blake3-aes-128-gcm" || m4["password"] != "serverkey:userkey" {
		t.Fatalf("unexpected ss fields: %#v", m4)
	}
	if m4["plugin"] != "obfs-local" || m4["plugin_opts"] != "obfs=tls;obfs-host=www.bing.com" || m4["udp_over_tcp"] != true {
		t.Fatalf("unexpected ss plugin/uot: %#v", m4)
	}

	// 6. Hysteria2
	m5 := decodeRawMap(t, res.Nodes[5].Raw)
	if m5["up_mbps"] != float64(100) || m5["down_mbps"] != float64(250) || m5["hop_interval"] != "30s" {
		t.Fatalf("unexpected hy2 fields: %#v", m5)
	}
	ports5 := m5["server_ports"].([]any)
	if len(ports5) != 2 || ports5[0] != "20000:30000" || ports5[1] != "40000:40000" {
		t.Fatalf("unexpected hy2 server_ports: %#v", ports5)
	}
	obfs5 := m5["obfs"].(map[string]any)
	if obfs5["type"] != "salamander" || obfs5["password"] != "obfs-secret" {
		t.Fatalf("unexpected hy2 obfs: %#v", obfs5)
	}

	// 7. TUIC
	m6 := decodeRawMap(t, res.Nodes[6].Raw)
	if m6["congestion_control"] != "bbr" || m6["udp_relay_mode"] != "native" || m6["zero_rtt_handshake"] != true || m6["heartbeat"] != "10s" {
		t.Fatalf("unexpected tuic fields: %#v", m6)
	}
	if m6["tls"].(map[string]any)["disable_sni"] != true {
		t.Fatalf("expected tuic tls.disable_sni=true")
	}

	// 8. AnyTLS
	m7 := decodeRawMap(t, res.Nodes[7].Raw)
	if m7["idle_session_check_interval"] != "30s" || m7["idle_session_timeout"] != "60s" || m7["min_idle_session"] != float64(2) {
		t.Fatalf("unexpected anytls fields: %#v", m7)
	}

	// 9. Socks5 -> sing-box "socks"
	m8 := decodeRawMap(t, res.Nodes[8].Raw)
	if m8["type"] != "socks" || m8["version"] != "5" || m8["username"] != "alice" || m8["network"] != "tcp" {
		t.Fatalf("unexpected socks fields: %#v", m8)
	}

	// 10. HTTP with TLS
	m9 := decodeRawMap(t, res.Nodes[9].Raw)
	if m9["type"] != "http" || m9["tls"].(map[string]any)["enabled"] != true {
		t.Fatalf("unexpected http fields: %#v", m9)
	}

	// 11. VLESS H2 -> sing-box transport.type="http"
	m10 := decodeRawMap(t, res.Nodes[10].Raw)
	tr10 := m10["transport"].(map[string]any)
	if tr10["type"] != "http" || tr10["path"] != "/h2path" {
		t.Fatalf("unexpected h2 transport: %#v", tr10)
	}
	hosts10 := tr10["host"].([]any)
	if len(hosts10) != 1 || hosts10[0] != "h2.example.com" {
		t.Fatalf("unexpected h2 hosts: %#v", hosts10)
	}
}

func TestParseSubscription_URIPlainAndBase64(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"VMess WS","add":"vmess.example.com","port":"443","id":"11111111-2222-3333-4444-555555555555","aid":"0","scy":"auto","net":"ws","host":"cdn.vmess.example.com","path":"/vmess?ed=2048","tls":"tls","sni":"cdn.vmess.example.com","fp":"chrome"}`
	vmessURI := "vmess://" + base64.StdEncoding.EncodeToString([]byte(vmessJSON))

	ssSIP002User := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:sip002-pass"))
	ssSIP002URI := "ss://" + ssSIP002User + "@ss1.example.com:8388/?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dexample.org#SS%20SIP002"
	ss2022URI := "ss://2022-blake3-aes-128-gcm:srvKey16:usrKey16@ss2022.example.com:9443#SS2022"
	ssLegacyURI := "ss://" + base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:legacy-pass@ss-legacy.example.com:8443")) + "#SS+Legacy"

	lines := []string{
		"# comment line should be ignored",
		"vless://aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee@vless.example.com:443?encryption=none&security=reality&sni=www.apple.com&pbk=realPubKey&sid=0102&flow=xtls-rprx-vision#VLESS%20Reality",
		vmessURI,
		"trojan://tr%3Apass@trojan.example.com:443?sni=trojan.example.com&alpn=h2,http/1.1&allowInsecure=1&type=grpc&serviceName=tr-grpc#Trojan%20gRPC",
		ssSIP002URI,
		ss2022URI,
		ssLegacyURI,
		"hysteria2://hy2pass@hy2.example.com:443/?sni=hy2.example.com&insecure=1&obfs=salamander&obfs-password=salpw&mport=20000-30000#Hy2%20Full",
		"hy2://user:pass@hy2short.example.com:8443?sni=hy2short.example.com&up=50&down=100#Hy2%20Short",
		"tuic://bbbbbbbb-cccc-dddd-eeee-ffffffffffff:tuicpw@tuic.example.com:443?congestion_control=bbr&udp_relay_mode=native&reduce_rtt=1&alpn=h3&allow_insecure=1&disable_sni=1#TUIC%20v5",
		"anytls://anypw@anytls.example.com:443?sni=anytls.example.com&fp=firefox&insecure=1#AnyTLS%20Node",
		"socks5://suser:spass@[2001:db8::1]:1080#Socks5%20IPv6",
		"socks://socks.example.com:1081#Socks%20Plain",
		"http://huser:hpass@http.example.com:8080#HTTP%20Proxy",
		"https://https.example.com:8443?sni=https.example.com&insecure=1#HTTPS%20Proxy",
	}
	rawPlain := strings.Join(lines, "\n")

	// Test plain URI list, StdEncoding Base64 with CRLF wrapping, and RawURLEncoding Base64.
	b64Std := base64.StdEncoding.EncodeToString([]byte(rawPlain))
	b64Wrapped := b64Std[:40] + "\r\n  " + b64Std[40:80] + "\n" + b64Std[80:]
	b64RawURL := base64.RawURLEncoding.EncodeToString([]byte(rawPlain))

	inputs := map[string][]byte{
		"plain":          []byte(rawPlain),
		"base64_wrapped": []byte(b64Wrapped),
		"base64_rawurl":  []byte(b64RawURL),
	}

	for label, payload := range inputs {
		t.Run(label, func(t *testing.T) {
			res, err := ParseSubscription(payload, FetchOptions{TagPrefix: "u-"})
			if err != nil {
				t.Fatalf("ParseSubscription(%s) failed: %v", label, err)
			}
			if res.Format != "uri" {
				t.Fatalf("expected format uri, got %q", res.Format)
			}
			if len(res.Nodes) != 14 {
				t.Fatalf("expected 14 parsed URI nodes, got %d", len(res.Nodes))
			}
			assertUniqueValidTags(t, res.Nodes)

			// Verify VLESS Reality auto-enables uTLS when fp is omitted.
			vlessMap := decodeRawMap(t, res.Nodes[0].Raw)
			vlessTLS := vlessMap["tls"].(map[string]any)
			if vlessTLS["utls"].(map[string]any)["fingerprint"] != "chrome" {
				t.Fatalf("expected default chrome utls for reality: %#v", vlessTLS)
			}

			// Verify VMess WS + ?ed=2048.
			vmessMap := decodeRawMap(t, res.Nodes[1].Raw)
			vmessTr := vmessMap["transport"].(map[string]any)
			if vmessTr["path"] != "/vmess" || vmessTr["max_early_data"] != float64(2048) {
				t.Fatalf("unexpected vmess ws transport: %#v", vmessTr)
			}

			// Verify SS SIP002 plugin, SS2022 colon password, and SS legacy base64.
			ss1 := decodeRawMap(t, res.Nodes[3].Raw)
			if ss1["method"] != "aes-256-gcm" || ss1["password"] != "sip002-pass" || ss1["plugin"] != "obfs-local" {
				t.Fatalf("unexpected ss sip002: %#v", ss1)
			}
			ss2 := decodeRawMap(t, res.Nodes[4].Raw)
			if ss2["method"] != "2022-blake3-aes-128-gcm" || ss2["password"] != "srvKey16:usrKey16" {
				t.Fatalf("unexpected ss2022: %#v", ss2)
			}
			ss3 := decodeRawMap(t, res.Nodes[5].Raw)
			if ss3["method"] != "chacha20-ietf-poly1305" || ss3["password"] != "legacy-pass" || ss3["server"] != "ss-legacy.example.com" {
				t.Fatalf("unexpected ss legacy: %#v", ss3)
			}
			if res.Nodes[5].OriginalName != "SS+Legacy" {
				t.Fatalf("expected literal + preserved in fragment name, got %q", res.Nodes[5].OriginalName)
			}

			// Verify Hysteria2 mport + obfs and hy2 user:pass auth.
			hy2Full := decodeRawMap(t, res.Nodes[6].Raw)
			if hy2Full["obfs"].(map[string]any)["password"] != "salpw" {
				t.Fatalf("unexpected hy2 obfs: %#v", hy2Full)
			}
			if hy2Full["server_ports"].([]any)[0] != "20000:30000" {
				t.Fatalf("unexpected hy2 server_ports: %#v", hy2Full["server_ports"])
			}
			hy2Short := decodeRawMap(t, res.Nodes[7].Raw)
			if hy2Short["password"] != "user:pass" || hy2Short["up_mbps"] != float64(50) || hy2Short["down_mbps"] != float64(100) {
				t.Fatalf("unexpected hy2 short: %#v", hy2Short)
			}

			// Verify TUIC & AnyTLS & Socks5 IPv6 & HTTPS.
			tuicMap := decodeRawMap(t, res.Nodes[8].Raw)
			if tuicMap["zero_rtt_handshake"] != true || tuicMap["tls"].(map[string]any)["disable_sni"] != true {
				t.Fatalf("unexpected tuic: %#v", tuicMap)
			}
			anyMap := decodeRawMap(t, res.Nodes[9].Raw)
			if anyMap["tls"].(map[string]any)["utls"].(map[string]any)["fingerprint"] != "firefox" {
				t.Fatalf("unexpected anytls: %#v", anyMap)
			}
			socksIPv6 := decodeRawMap(t, res.Nodes[10].Raw)
			if socksIPv6["type"] != "socks" || socksIPv6["server"] != "2001:db8::1" || socksIPv6["username"] != "suser" {
				t.Fatalf("unexpected socks5 ipv6: %#v", socksIPv6)
			}
			httpsMap := decodeRawMap(t, res.Nodes[13].Raw)
			if httpsMap["type"] != "http" || httpsMap["tls"].(map[string]any)["enabled"] != true {
				t.Fatalf("unexpected https: %#v", httpsMap)
			}
		})
	}
}

func TestParseSubscription_RegexFilterAndTagDedup(t *testing.T) {
	raw := strings.Join([]string{
		"trojan://p1@1.1.1.1:443#HK 01",
		"trojan://p2@1.1.1.2:443#HK/01",
		"trojan://p3@1.1.1.3:443#HK-01",
		"trojan://p4@1.1.1.4:443#HK 01 Expire",
		"trojan://p5@1.1.1.5:443#US 01",
		"trojan://p6@1.1.1.6:443#🔥🇺🇸",
	}, "\n")

	res, err := ParseSubscription([]byte(raw), FetchOptions{
		IncludeRegex: `(?i)hk|🔥`,
		ExcludeRegex: `(?i)expire`,
		TagPrefix:    "sub-9-",
	})
	if err != nil {
		t.Fatalf("ParseSubscription failed: %v", err)
	}
	if len(res.Nodes) != 4 {
		t.Fatalf("expected 4 filtered nodes, got %d: %+v", len(res.Nodes), res.Nodes)
	}
	assertUniqueValidTags(t, res.Nodes)

	wantTags := []string{"sub-9-HK-01", "sub-9-HK-01-2", "sub-9-HK-01-3"}
	for i, want := range wantTags {
		if res.Nodes[i].Tag != want {
			t.Fatalf("node[%d] tag=%q, want %q", i, res.Nodes[i].Tag, want)
		}
	}
	// Emoji-only name must still produce a clean, non-empty fallback tag.
	if !strings.HasPrefix(res.Nodes[3].Tag, "sub-9-trojan-") {
		t.Fatalf("expected clean fallback tag for emoji-only node, got %q", res.Nodes[3].Tag)
	}

	// Invalid regex must return an error.
	if _, err := ParseSubscription([]byte(raw), FetchOptions{IncludeRegex: "[invalid"}); err == nil {
		t.Fatal("expected error for invalid include_regex")
	}
	if _, err := ParseSubscription([]byte(raw), FetchOptions{ExcludeRegex: "(unclosed"}); err == nil {
		t.Fatal("expected error for invalid exclude_regex")
	}
}

func TestParseSubscription_InvalidInputs(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"whitespace", "   \n\t  "},
		{"html", "<!DOCTYPE html><html><body><a href=\"http://example.com\">hi</a></body></html>"},
		{"random_text", "not a valid subscription at all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseSubscription([]byte(tc.data), FetchOptions{}); err == nil {
				t.Fatalf("expected error for %s input", tc.name)
			}
		})
	}
}

func TestFetchSubscription_HTTPAndErrors(t *testing.T) {
	var seenUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUA = r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/clash":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("proxies:\n  - {name: HK-1, type: trojan, server: hk.test, port: 443, password: pw}\n"))
		case "/error":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream down"))
		case "/huge":
			w.WriteHeader(http.StatusOK)
			chunk := strings.Repeat("a", 1024*1024)
			for i := 0; i < 11; i++ {
				_, _ = w.Write([]byte(chunk))
			}
		}
	}))
	defer srv.Close()

	res, err := FetchSubscription(context.Background(), FetchOptions{
		URL:       srv.URL + "/clash",
		TagPrefix: "srv-",
	})
	if err != nil {
		t.Fatalf("FetchSubscription failed: %v", err)
	}
	if seenUA != defaultSubUserAgent {
		t.Fatalf("expected default User-Agent %q, got %q", defaultSubUserAgent, seenUA)
	}
	if res.Format != "clash" || len(res.Nodes) != 1 || res.Nodes[0].Tag != "srv-HK-1" {
		t.Fatalf("unexpected fetch result: %+v", res)
	}

	// Custom UA override
	_, err = FetchSubscription(context.Background(), FetchOptions{
		URL:       srv.URL + "/clash",
		UserAgent: "CustomAgent/1.0",
	})
	if err != nil {
		t.Fatalf("FetchSubscription custom UA failed: %v", err)
	}
	if seenUA != "CustomAgent/1.0" {
		t.Fatalf("expected custom User-Agent, got %q", seenUA)
	}

	// Non-2xx error
	if _, err := FetchSubscription(context.Background(), FetchOptions{URL: srv.URL + "/error"}); err == nil {
		t.Fatal("expected error for HTTP 502")
	}

	// > 10 MiB body limit error
	if _, err := FetchSubscription(context.Background(), FetchOptions{URL: srv.URL + "/huge"}); err == nil {
		t.Fatal("expected error for body exceeding 10 MiB")
	}
}

func TestFetchSubscription_SOCKS5Proxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("trojan://pw@1.2.3.4:443#Via-Socks5\n"))
	}))
	defer target.Close()

	// Spin up minimal in-process RFC 1928 + RFC 1929 SOCKS5 proxy requiring user:pass auth.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5 mock: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var hdr [2]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return
		}
		methods := make([]byte, hdr[1])
		if _, err := io.ReadFull(conn, methods); err != nil {
			return
		}
		// Require username/password auth (0x02).
		_, _ = conn.Write([]byte{0x05, 0x02})

		var authHdr [2]byte
		if _, err := io.ReadFull(conn, authHdr[:]); err != nil {
			return
		}
		user := make([]byte, authHdr[1])
		if _, err := io.ReadFull(conn, user); err != nil {
			return
		}
		var passLen [1]byte
		if _, err := io.ReadFull(conn, passLen[:]); err != nil {
			return
		}
		pass := make([]byte, passLen[0])
		if _, err := io.ReadFull(conn, pass); err != nil {
			return
		}
		if string(user) != "suser" || string(pass) != "spass" {
			_, _ = conn.Write([]byte{0x01, 0x01})
			return
		}
		_, _ = conn.Write([]byte{0x01, 0x00})

		var reqHdr [4]byte
		if _, err := io.ReadFull(conn, reqHdr[:]); err != nil {
			return
		}
		var destHost string
		switch reqHdr[3] {
		case 0x01:
			var ip [4]byte
			_, _ = io.ReadFull(conn, ip[:])
			destHost = net.IP(ip[:]).String()
		case 0x03:
			var dlen [1]byte
			_, _ = io.ReadFull(conn, dlen[:])
			dom := make([]byte, dlen[0])
			_, _ = io.ReadFull(conn, dom)
			destHost = string(dom)
		case 0x04:
			var ip [16]byte
			_, _ = io.ReadFull(conn, ip[:])
			destHost = net.IP(ip[:]).String()
		}
		var portBuf [2]byte
		_, _ = io.ReadFull(conn, portBuf[:])
		destPort := binary.BigEndian.Uint16(portBuf[:])

		upstream, err := net.Dial("tcp", net.JoinHostPort(destHost, stringInt(int(destPort))))
		if err != nil {
			_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			return
		}
		defer upstream.Close()
		_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})

		go func() { _, _ = io.Copy(upstream, conn) }()
		_, _ = io.Copy(conn, upstream)
	}()

	res, err := FetchSubscription(context.Background(), FetchOptions{
		URL:      target.URL,
		ProxyURL: "socks5://suser:spass@" + ln.Addr().String(),
	})
	if err != nil {
		t.Fatalf("FetchSubscription via SOCKS5 failed: %v", err)
	}
	if res.Format != "uri" || len(res.Nodes) != 1 || res.Nodes[0].OriginalName != "Via-Socks5" {
		t.Fatalf("unexpected SOCKS5 fetch result: %+v", res)
	}
}

func stringInt(n int) string {
	return asString(n)
}
