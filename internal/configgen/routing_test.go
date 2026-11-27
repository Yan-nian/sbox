package configgen

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPresetRuleDefinitionsAndBuiltinURLs(t *testing.T) {
	presets := PresetRuleDefinitions()
	expectedKeys := []string{
		"ai_services",
		"streaming",
		"block_bt",
		"block_private",
		"block_spam_smtp",
		"block_cn",
	}
	if len(presets) != len(expectedKeys) {
		t.Fatalf("expected %d presets, got %d", len(expectedKeys), len(presets))
	}

	byKey := make(map[string]RoutingPreset, len(presets))
	for _, p := range presets {
		byKey[p.Key] = p
	}
	for _, k := range expectedKeys {
		p, ok := byKey[k]
		if !ok {
			t.Fatalf("missing required preset %q", k)
		}
		if p.Name == "" || p.DefaultAction == "" {
			t.Errorf("preset %q has empty Name or DefaultAction: %+v", k, p)
		}
		for _, rs := range p.RuleSets {
			u, ok := BuiltinRuleSetURL(rs.Tag)
			if !ok || u == "" || !strings.HasSuffix(u, ".srs") {
				t.Errorf("preset %q ruleset %q missing valid .srs URL (got %q, %v)", k, rs.Tag, u, ok)
			}
			if rs.URL != u {
				t.Errorf("preset %q ruleset %q URL mismatch: %q != %q", k, rs.Tag, rs.URL, u)
			}
		}
	}

	if !byKey["ai_services"].NeedsOutbound || byKey["ai_services"].Category != "unlock" {
		t.Errorf("ai_services should require outbound & category unlock")
	}
	if byKey["block_bt"].NeedsOutbound || byKey["block_bt"].RuleTemplate.Action != "reject" {
		t.Errorf("block_bt should be reject without outbound")
	}
	if _, ok := BuiltinRuleSetURL("nonexistent-ruleset"); ok {
		t.Errorf("expected unknown ruleset to return false")
	}
}

func TestBuild_WARPEndpointAndIPv6Only(t *testing.T) {
	raw, err := Build(BuildInput{
		ExtraOutbounds: []ExtraOutboundSpec{
			{
				Tag:  "warp-dual",
				Type: "warp",
				Settings: json.RawMessage(`{
					"private_key": "priv-key-1",
					"peer_public_key": "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
					"public_key": "local-pubkey-must-not-override-peer",
					"endpoint_host": "engage.cloudflareclient.com",
					"endpoint_port": 2408,
					"local_ipv4": "172.16.0.2/32",
					"local_ipv6": "2606:4700:110::2/128",
					"reserved": [12, 34, 56],
					"mtu": 1280
				}`),
			},
			{
				Tag:  "warp-v6",
				Type: "wireguard",
				Settings: json.RawMessage(`{
					"private_key": "priv-key-2",
					"peer_public_key": "peer-pub-2",
					"endpoint_host": "162.159.192.1",
					"endpoint_port": 2408,
					"local_ipv4": "172.16.0.3",
					"local_ipv6": "2606:4700:110::3",
					"ipv6_only": true
				}`),
			},
		},
		DefaultOutbound: "warp-dual",
	})
	if err != nil {
		t.Fatalf("Build with WARP endpoints failed: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Ensure NO legacy wireguard outbound in outbounds!
	for _, ob := range doc["outbounds"].([]any) {
		m := ob.(map[string]any)
		if m["type"] == "wireguard" {
			t.Fatalf("legacy wireguard outbound must NEVER be emitted in sing-box 1.12+: %+v", m)
		}
	}

	endpoints := doc["endpoints"].([]any)
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 wireguard endpoints, got %d", len(endpoints))
	}

	ep0 := endpoints[0].(map[string]any)
	if ep0["type"] != "wireguard" || ep0["tag"] != "warp-dual" || ep0["private_key"] != "priv-key-1" || ep0["mtu"] != float64(1280) {
		t.Errorf("unexpected warp-dual endpoint: %+v", ep0)
	}
	addrs0 := ep0["address"].([]any)
	if len(addrs0) != 2 || addrs0[0] != "172.16.0.2/32" || addrs0[1] != "2606:4700:110::2/128" {
		t.Errorf("unexpected warp-dual addresses: %+v", addrs0)
	}
	peer0 := ep0["peers"].([]any)[0].(map[string]any)
	if peer0["address"] != "engage.cloudflareclient.com" || peer0["port"] != float64(2408) || peer0["public_key"] != "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=" {
		t.Errorf("unexpected warp-dual peer: %+v", peer0)
	}
	res0 := peer0["reserved"].([]any)
	if len(res0) != 3 || res0[0] != float64(12) || res0[2] != float64(56) {
		t.Errorf("unexpected warp-dual reserved: %+v", res0)
	}

	// Verify ipv6_only stripped IPv4 and normalized bare IPv6 to /128
	ep1 := endpoints[1].(map[string]any)
	addrs1 := ep1["address"].([]any)
	if len(addrs1) != 1 || addrs1[0] != "2606:4700:110::3/128" {
		t.Errorf("expected ipv6_only address [2606:4700:110::3/128], got %+v", addrs1)
	}

	// Verify route.final accepted endpoint tag "warp-dual"
	routeSec := doc["route"].(map[string]any)
	if routeSec["final"] != "warp-dual" {
		t.Errorf("expected route.final=warp-dual, got %v", routeSec["final"])
	}
}

func TestBuild_ExtraOutbounds_SocksHTTPAndRaw(t *testing.T) {
	raw, err := Build(BuildInput{
		ExtraOutbounds: []ExtraOutboundSpec{
			{
				Tag:  "landing-socks",
				Type: "socks",
				Settings: json.RawMessage(`{
					"server": "1.2.3.4",
					"server_port": 1080,
					"username": "u1",
					"password": "p1",
					"version": "5"
				}`),
			},
			{
				Tag:  "landing-https",
				Type: "http",
				Settings: json.RawMessage(`{
					"server": "proxy.example.com",
					"server_port": 443,
					"username": "u2",
					"password": "p2",
					"tls": true,
					"server_name": "proxy.example.com",
					"insecure": true
				}`),
			},
			{
				Tag:  "custom-raw",
				Type: "raw",
				Settings: json.RawMessage(`{
					"type": "ssh",
					"tag": "should-be-overwritten",
					"server": "ssh.example.com",
					"server_port": 22
				}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("Build extra outbounds failed: %v", err)
	}

	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)

	byTag := map[string]map[string]any{}
	for _, ob := range doc["outbounds"].([]any) {
		m := ob.(map[string]any)
		byTag[m["tag"].(string)] = m
	}

	socks := byTag["landing-socks"]
	if socks["type"] != "socks" || socks["server"] != "1.2.3.4" || socks["username"] != "u1" || socks["version"] != "5" {
		t.Errorf("unexpected socks outbound: %+v", socks)
	}
	httpOb := byTag["landing-https"]
	if httpOb["type"] != "http" || httpOb["tls"].(map[string]any)["enabled"] != true || httpOb["tls"].(map[string]any)["insecure"] != true {
		t.Errorf("unexpected http outbound: %+v", httpOb)
	}
	rawOb := byTag["custom-raw"]
	if rawOb["type"] != "ssh" || rawOb["tag"] != "custom-raw" {
		t.Errorf("expected raw outbound tag enforced to custom-raw, got %+v", rawOb)
	}
}

func TestBuild_UpstreamGroupsRoutingRulesAndAutoRuleSets(t *testing.T) {
	raw, err := Build(BuildInput{
		UpstreamGroups: []UpstreamGroupSpec{
			{
				GroupTag:      "sub-airport",
				URLTestTag:    "sub-airport-auto",
				EnableURLTest: true,
				Nodes: []json.RawMessage{
					json.RawMessage(`{"type":"vless","tag":"sub-1-hk","server":"hk.example.com","server_port":443,"uuid":"11111111-1111-1111-1111-111111111111"}`),
					json.RawMessage(`{"type":"trojan","tag":"sub-1-jp","server":"jp.example.com","server_port":443,"password":"pw"}`),
				},
				NodeTags: []string{"sub-1-hk", "sub-1-jp"},
			},
			{
				// Empty subscription (not yet fetched) must be skipped cleanly!
				GroupTag:      "sub-empty",
				EnableURLTest: true,
			},
		},
		RuleSets: []RuleSetSpec{
			{
				Tag: "custom-rs",
				URL: "https://example.com/custom.srs",
			},
		},
		RouteRules: []RouteRuleSpec{
			// Intentionally unsorted priorities to verify ascending sort!
			{
				Priority:    100,
				Action:      "route",
				OutboundTag: "sub-airport",
				RuleSets:    []string{"geosite-openai", "geosite-anthropic", "custom-rs"},
			},
			{
				Priority:  10,
				Action:    "reject",
				Protocols: []string{"bittorrent"},
			},
			{
				Priority:    20,
				Action:      "reject",
				IPIsPrivate: true,
			},
			{
				Priority: 30,
				Action:   "reject",
				Ports:    []int{25, 465, 587},
			},
			{
				Priority:      50,
				Action:        "route",
				OutboundTag:   "direct",
				Domains:       []string{"exact.example.com"},
				DomainSuffix:  []string{".example.org"},
				DomainKeyword: []string{"internal"},
				IPCIDR:        []string{"203.0.113.0/24"},
				AuthUsers:     []string{"alice"},
				Inbounds:      []string{"vless-in"},
			},
		},
		DefaultOutbound: "sub-airport",
	})
	if err != nil {
		t.Fatalf("Build full routing config failed: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify outbounds: direct + urltest + selector + 2 nodes = 5 (no sub-empty, no legacy block!)
	outbounds := doc["outbounds"].([]any)
	if len(outbounds) != 5 {
		t.Fatalf("expected 5 outbounds, got %d: %s", len(outbounds), string(raw))
	}
	byTag := map[string]map[string]any{}
	for _, ob := range outbounds {
		m := ob.(map[string]any)
		if m["type"] == "block" {
			t.Fatalf("legacy block outbound must NOT exist in sing-box 1.12+")
		}
		byTag[m["tag"].(string)] = m
	}

	urltest := byTag["sub-airport-auto"]
	if urltest["type"] != "urltest" || urltest["url"] != defaultURLTestURL || urltest["interval"] != "5m" || urltest["tolerance"] != float64(50) {
		t.Errorf("unexpected urltest group: %+v", urltest)
	}
	selector := byTag["sub-airport"]
	if selector["type"] != "selector" {
		t.Errorf("unexpected selector group: %+v", selector)
	}
	selMembers := selector["outbounds"].([]any)
	if len(selMembers) != 3 || selMembers[0] != "sub-airport-auto" || selMembers[1] != "sub-1-hk" || selMembers[2] != "sub-1-jp" {
		t.Errorf("unexpected selector outbounds: %+v", selMembers)
	}

	// Verify route.final and sorted rules starting with sniff
	routeSec := doc["route"].(map[string]any)
	if routeSec["final"] != "sub-airport" {
		t.Errorf("expected route.final=sub-airport, got %v", routeSec["final"])
	}

	rules := routeSec["rules"].([]any)
	if len(rules) != 6 {
		t.Fatalf("expected 6 route rules (sniff + 5 rules), got %d", len(rules))
	}
	if rules[0].(map[string]any)["action"] != "sniff" {
		t.Errorf("rule[0] must be sniff, got %+v", rules[0])
	}
	if rules[1].(map[string]any)["action"] != "reject" || rules[1].(map[string]any)["protocol"].([]any)[0] != "bittorrent" {
		t.Errorf("rule[1] should be priority-10 reject BT, got %+v", rules[1])
	}
	if _, hasLegacyOutbound := rules[1].(map[string]any)["outbound"]; hasLegacyOutbound {
		t.Errorf("reject rule must not have outbound field")
	}
	if rules[2].(map[string]any)["ip_is_private"] != true || rules[2].(map[string]any)["action"] != "reject" {
		t.Errorf("rule[2] should be priority-20 reject private IP, got %+v", rules[2])
	}
	if len(rules[3].(map[string]any)["port"].([]any)) != 3 {
		t.Errorf("rule[3] should be priority-30 reject SMTP ports, got %+v", rules[3])
	}
	if rules[4].(map[string]any)["outbound"] != "direct" || rules[4].(map[string]any)["auth_user"].([]any)[0] != "alice" {
		t.Errorf("rule[4] should be priority-50 custom rule, got %+v", rules[4])
	}
	if rules[5].(map[string]any)["outbound"] != "sub-airport" {
		t.Errorf("rule[5] should be priority-100 AI rule, got %+v", rules[5])
	}

	// Verify route.rule_set includes custom-rs + auto-resolved geosite-openai & geosite-anthropic
	rsList := routeSec["rule_set"].([]any)
	if len(rsList) != 3 {
		t.Fatalf("expected 3 deduplicated rule sets, got %d: %+v", len(rsList), rsList)
	}
	rsByTag := map[string]map[string]any{}
	for _, r := range rsList {
		m := r.(map[string]any)
		rsByTag[m["tag"].(string)] = m
	}
	if rsByTag["geosite-openai"]["format"] != "binary" || rsByTag["geosite-openai"]["type"] != "remote" || rsByTag["geosite-openai"]["download_detour"] != "direct" {
		t.Errorf("unexpected auto-resolved geosite-openai: %+v", rsByTag["geosite-openai"])
	}
	if rsByTag["custom-rs"]["url"] != "https://example.com/custom.srs" {
		t.Errorf("unexpected custom-rs: %+v", rsByTag["custom-rs"])
	}
}

func TestBuild_DefaultOutboundFallbackAndValidation(t *testing.T) {
	// Unknown DefaultOutbound must safely fall back to "direct"
	raw, err := Build(BuildInput{
		DefaultOutbound: "non-existent-tag",
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	if doc["route"].(map[string]any)["final"] != "direct" {
		t.Errorf("expected fallback route.final=direct, got %v", doc["route"].(map[string]any)["final"])
	}

	// Invalid extra outbound / route rule cases
	errCases := []struct {
		name string
		in   BuildInput
	}{
		{
			name: "warp missing private_key",
			in: BuildInput{
				ExtraOutbounds: []ExtraOutboundSpec{{Tag: "w", Type: "warp", Settings: json.RawMessage(`{"peer_public_key":"pub","local_ipv4":"172.16.0.2/32"}`)}},
			},
		},
		{
			name: "warp ipv6_only without ipv6 address",
			in: BuildInput{
				ExtraOutbounds: []ExtraOutboundSpec{{Tag: "w", Type: "warp", Settings: json.RawMessage(`{"private_key":"k","peer_public_key":"pub","local_ipv4":"172.16.0.2/32","ipv6_only":true}`)}},
			},
		},
		{
			name: "socks missing server",
			in: BuildInput{
				ExtraOutbounds: []ExtraOutboundSpec{{Tag: "s", Type: "socks", Settings: json.RawMessage(`{"server_port":1080}`)}},
			},
		},
		{
			name: "duplicate extra outbound tag",
			in: BuildInput{
				ExtraOutbounds: []ExtraOutboundSpec{
					{Tag: "dup", Type: "socks", Settings: json.RawMessage(`{"server":"1.1.1.1","server_port":1080}`)},
					{Tag: "dup", Type: "http", Settings: json.RawMessage(`{"server":"2.2.2.2","server_port":8080}`)},
				},
			},
		},
		{
			name: "route action missing outbound_tag",
			in: BuildInput{
				RouteRules: []RouteRuleSpec{{Action: "route", Domains: []string{"example.com"}}},
			},
		},
		{
			name: "route invalid action",
			in: BuildInput{
				RouteRules: []RouteRuleSpec{{Action: "drop-legacy"}},
			},
		},
		{
			name: "route invalid port",
			in: BuildInput{
				RouteRules: []RouteRuleSpec{{Action: "reject", Ports: []int{70000}}},
			},
		},
	}

	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Build(tc.in); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}
