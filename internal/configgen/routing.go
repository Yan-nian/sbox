package configgen

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
)

const (
	defaultURLTestURL       = "https://www.gstatic.com/generate_204"
	defaultURLTestInterval  = "5m"
	defaultURLTestTolerance = 50
	defaultRuleSetInterval  = "24h"
	defaultWARPEndpointHost = "engage.cloudflareclient.com"
	defaultWARPEndpointPort = 2408
	defaultWARPMTU          = 1280
)

// UpstreamGroupSpec describes an upstream subscription's parsed sing-box nodes and
// the selector / optional urltest outbound groups to synthesize for them.
type UpstreamGroupSpec struct {
	GroupTag      string            `json:"group_tag"`      // selector tag e.g. "sub-airport"
	URLTestTag    string            `json:"urltest_tag"`    // urltest tag e.g. "sub-airport-auto"
	EnableURLTest bool              `json:"enable_urltest"`
	TestURL       string            `json:"test_url"`       // default "https://www.gstatic.com/generate_204"
	TestInterval  string            `json:"test_interval"`  // default "5m"
	Tolerance     int               `json:"tolerance"`      // default 50
	Nodes         []json.RawMessage `json:"nodes"`          // sing-box outbound JSONs
	NodeTags      []string          `json:"node_tags"`
}

// ExtraOutboundSpec describes a manually added outbound or Cloudflare WARP / WireGuard endpoint.
type ExtraOutboundSpec struct {
	Tag      string          `json:"tag"`
	Type     string          `json:"type"` // "warp" | "wireguard" | "socks" | "http" | "raw"
	Settings json.RawMessage `json:"settings"`
}

// RuleSetSpec defines a remote binary (.srs) sing-box rule set.
type RuleSetSpec struct {
	Tag            string `json:"tag"`
	URL            string `json:"url"`
	DownloadDetour string `json:"download_detour"` // default "direct"
	UpdateInterval string `json:"update_interval"` // default "24h"
}

// RouteRuleSpec defines a structured sing-box 1.12+ routing rule.
type RouteRuleSpec struct {
	Priority      int      `json:"priority"`
	Action        string   `json:"action"` // "route" | "reject" | "hijack-dns" | "sniff"
	OutboundTag   string   `json:"outbound_tag"`
	RuleSets      []string `json:"rule_sets"`
	Domains       []string `json:"domains"`
	DomainSuffix  []string `json:"domain_suffix"`
	DomainKeyword []string `json:"domain_keyword"`
	IPCIDR        []string `json:"ip_cidr"`
	Protocols     []string `json:"protocol"`
	Ports         []int    `json:"port"`
	AuthUsers     []string `json:"auth_user"`
	Inbounds      []string `json:"inbound"`
	IPIsPrivate   bool     `json:"ip_is_private"`
}

// RoutingPreset describes a one-click visual routing preset backed by official SagerNet .srs rule sets.
type RoutingPreset struct {
	Key           string        `json:"key"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	Category      string        `json:"category"`       // "unlock" | "block"
	NeedsOutbound bool          `json:"needs_outbound"`
	DefaultAction string        `json:"default_action"` // "route" | "reject"
	RuleSets      []RuleSetSpec `json:"rule_sets,omitempty"`
	RuleTemplate  RouteRuleSpec `json:"rule_template"`
}

var builtinRuleSetURLs = map[string]string{
	"geosite-openai":           "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-openai.srs",
	"geosite-anthropic":        "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-anthropic.srs",
	"geosite-google-gemini":    "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-google-gemini.srs",
	"geosite-netflix":          "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-netflix.srs",
	"geosite-disney":           "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-disney.srs",
	"geosite-youtube":          "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-youtube.srs",
	"geosite-cn":               "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs",
	"geoip-cn":                 "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs",
	"geosite-geolocation-!cn":  "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-geolocation-!cn.srs",
	"geosite-category-ads-all": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ads-all.srs",
	"geosite-telegram":         "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-telegram.srs",
	"geoip-telegram":           "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-telegram.srs",
}

// BuiltinRuleSetURL resolves an official SagerNet binary .srs URL for a built-in rule set tag.
func BuiltinRuleSetURL(tag string) (string, bool) {
	u, ok := builtinRuleSetURLs[strings.TrimSpace(tag)]
	return u, ok
}

// PresetRuleDefinitions returns the catalog of built-in high-frequency routing presets.
func PresetRuleDefinitions() []RoutingPreset {
	mkRuleSets := func(tags ...string) []RuleSetSpec {
		out := make([]RuleSetSpec, 0, len(tags))
		for _, tag := range tags {
			u, _ := BuiltinRuleSetURL(tag)
			out = append(out, RuleSetSpec{
				Tag:            tag,
				URL:            u,
				DownloadDetour: "direct",
				UpdateInterval: defaultRuleSetInterval,
			})
		}
		return out
	}

	return []RoutingPreset{
		{
			Key:           "ai_services",
			Name:          "AI 服务解锁",
			Description:   "将 OpenAI / Claude / Google Gemini 等 AI 流量分流至指定落地或 WARP 出站",
			Category:      "unlock",
			NeedsOutbound: true,
			DefaultAction: "route",
			RuleSets:      mkRuleSets("geosite-openai", "geosite-anthropic", "geosite-google-gemini"),
			RuleTemplate: RouteRuleSpec{
				Priority: 100,
				Action:   "route",
				RuleSets: []string{"geosite-openai", "geosite-anthropic", "geosite-google-gemini"},
			},
		},
		{
			Key:           "streaming",
			Name:          "流媒体解锁",
			Description:   "将 Netflix / Disney+ / YouTube 流量分流至指定解锁出站",
			Category:      "unlock",
			NeedsOutbound: true,
			DefaultAction: "route",
			RuleSets:      mkRuleSets("geosite-netflix", "geosite-disney", "geosite-youtube"),
			RuleTemplate: RouteRuleSpec{
				Priority: 110,
				Action:   "route",
				RuleSets: []string{"geosite-netflix", "geosite-disney", "geosite-youtube"},
			},
		},
		{
			Key:           "block_bt",
			Name:          "拦截 BT 流量",
			Description:   "拒绝 BitTorrent 协议流量，避免 VPS 收到版权投诉 (DMCA)",
			Category:      "block",
			NeedsOutbound: false,
			DefaultAction: "reject",
			RuleTemplate: RouteRuleSpec{
				Priority:  10,
				Action:    "reject",
				Protocols: []string{"bittorrent"},
			},
		},
		{
			Key:           "block_private",
			Name:          "拦截私有内网",
			Description:   "阻止代理访问服务器局域网/回环私有网段，防止 SSRF 与内网探测",
			Category:      "block",
			NeedsOutbound: false,
			DefaultAction: "reject",
			RuleTemplate: RouteRuleSpec{
				Priority:    20,
				Action:      "reject",
				IPIsPrivate: true,
			},
		},
		{
			Key:           "block_spam_smtp",
			Name:          "拦截垃圾邮件端口",
			Description:   "封禁 SMTP 发信端口 (25/465/587)，避免滥用发信导致 IP 被封",
			Category:      "block",
			NeedsOutbound: false,
			DefaultAction: "reject",
			RuleTemplate: RouteRuleSpec{
				Priority: 30,
				Action:   "reject",
				Ports:    []int{25, 465, 587},
			},
		},
		{
			Key:           "block_cn",
			Name:          "拦截回国流量",
			Description:   "拒绝匹配中国大陆域名与 IP 的出站请求（geosite-cn / geoip-cn）",
			Category:      "block",
			NeedsOutbound: false,
			DefaultAction: "reject",
			RuleSets:      mkRuleSets("geosite-cn", "geoip-cn"),
			RuleTemplate: RouteRuleSpec{
				Priority: 40,
				Action:   "reject",
				RuleSets: []string{"geosite-cn", "geoip-cn"},
			},
		},
	}
}

type wireguardSettings struct {
	PrivateKey                  string   `json:"private_key"`
	PeerPublicKey               string   `json:"peer_public_key"`
	ServerPublicKey             string   `json:"server_public_key"`
	PublicKey                   string   `json:"public_key"`
	PreSharedKey                string   `json:"pre_shared_key"`
	EndpointHost                string   `json:"endpoint_host"`
	Server                      string   `json:"server"`
	EndpointPort                int      `json:"endpoint_port"`
	ServerPort                  int      `json:"server_port"`
	Port                        int      `json:"port"`
	Address                     []string `json:"address"`
	LocalIPv4                   string   `json:"local_ipv4"`
	LocalIPv6                   string   `json:"local_ipv6"`
	IPv6Only                    bool     `json:"ipv6_only"`
	MTU                         int      `json:"mtu"`
	Reserved                    []int    `json:"reserved"`
	AllowedIPs                  []string `json:"allowed_ips"`
	PersistentKeepaliveInterval int      `json:"persistent_keepalive_interval"`
}

type proxyOutboundSettings struct {
	Server     string          `json:"server"`
	ServerPort int             `json:"server_port"`
	Port       int             `json:"port"`
	Username   string          `json:"username"`
	Password   string          `json:"password"`
	Version    string          `json:"version"`
	TLSEnabled bool            `json:"tls_enabled"`
	TLS        json.RawMessage `json:"tls"`
	ServerName string          `json:"server_name"`
	Insecure   bool            `json:"insecure"`
}

type compiledExtra struct {
	isEndpoint bool
	tag        string
	obj        map[string]any
}

func compileExtraOutbound(spec ExtraOutboundSpec) (compiledExtra, error) {
	tag := strings.TrimSpace(spec.Tag)
	if tag == "" {
		return compiledExtra{}, fmt.Errorf("configgen: extra outbound tag is required")
	}
	typ := strings.ToLower(strings.TrimSpace(spec.Type))

	switch typ {
	case "warp", "wireguard":
		obj, err := compileWireGuardEndpoint(tag, typ == "warp", spec.Settings)
		if err != nil {
			return compiledExtra{}, err
		}
		return compiledExtra{isEndpoint: true, tag: tag, obj: obj}, nil

	case "socks", "http":
		obj, err := compileSimpleProxyOutbound(tag, typ, spec.Settings)
		if err != nil {
			return compiledExtra{}, err
		}
		return compiledExtra{isEndpoint: false, tag: tag, obj: obj}, nil

	case "raw":
		val, err := decodeSingleJSON(spec.Settings)
		if err != nil {
			return compiledExtra{}, fmt.Errorf("configgen: extra outbound %q invalid raw JSON: %w", tag, err)
		}
		obj, ok := val.(map[string]any)
		if !ok {
			return compiledExtra{}, fmt.Errorf("configgen: extra outbound %q raw settings must be a JSON object", tag)
		}
		copied := make(map[string]any, len(obj)+1)
		for k, v := range obj {
			copied[k] = v
		}
		copied["tag"] = tag
		return compiledExtra{isEndpoint: false, tag: tag, obj: copied}, nil

	default:
		return compiledExtra{}, fmt.Errorf("configgen: extra outbound %q unsupported type %q", tag, spec.Type)
	}
}

func compileWireGuardEndpoint(tag string, isWARP bool, raw json.RawMessage) (map[string]any, error) {
	var s wireguardSettings
	if err := decodeSettings(tag, raw, &s); err != nil {
		return nil, err
	}

	privKey := strings.TrimSpace(s.PrivateKey)
	if privKey == "" {
		return nil, fmt.Errorf("configgen: endpoint %q requires private_key", tag)
	}

	peerPub := firstNonEmptyStr(s.PeerPublicKey, s.ServerPublicKey, s.PublicKey)
	if peerPub == "" {
		return nil, fmt.Errorf("configgen: endpoint %q requires peer_public_key", tag)
	}

	host := firstNonEmptyStr(s.EndpointHost, s.Server)
	if host == "" && isWARP {
		host = defaultWARPEndpointHost
	}
	if host == "" {
		return nil, fmt.Errorf("configgen: endpoint %q requires endpoint_host", tag)
	}

	port := s.EndpointPort
	if port == 0 {
		port = s.ServerPort
	}
	if port == 0 {
		port = s.Port
	}
	if port == 0 && isWARP {
		port = defaultWARPEndpointPort
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("configgen: endpoint %q invalid endpoint_port %d", tag, port)
	}

	rawAddrs := make([]string, 0, len(s.Address)+2)
	for _, a := range s.Address {
		if trimmed := normalizeCIDR(a); trimmed != "" {
			rawAddrs = append(rawAddrs, trimmed)
		}
	}
	if len(rawAddrs) == 0 {
		if v4 := normalizeCIDR(s.LocalIPv4); v4 != "" {
			rawAddrs = append(rawAddrs, v4)
		}
		if v6 := normalizeCIDR(s.LocalIPv6); v6 != "" {
			rawAddrs = append(rawAddrs, v6)
		}
	}

	addrs := make([]string, 0, len(rawAddrs))
	for _, a := range rawAddrs {
		if s.IPv6Only && !strings.Contains(a, ":") {
			continue
		}
		addrs = append(addrs, a)
	}
	if len(addrs) == 0 {
		if s.IPv6Only {
			return nil, fmt.Errorf("configgen: endpoint %q has ipv6_only enabled but no IPv6 local address", tag)
		}
		return nil, fmt.Errorf("configgen: endpoint %q requires at least one local address", tag)
	}

	mtu := s.MTU
	if mtu <= 0 {
		mtu = defaultWARPMTU
	}

	allowedIPs := cleanStringList(s.AllowedIPs)
	if len(allowedIPs) == 0 {
		allowedIPs = []string{"0.0.0.0/0", "::/0"}
	}

	peer := map[string]any{
		"address":     host,
		"port":        port,
		"public_key":  peerPub,
		"allowed_ips": allowedIPs,
	}
	if psk := strings.TrimSpace(s.PreSharedKey); psk != "" {
		peer["pre_shared_key"] = psk
	}
	if len(s.Reserved) > 0 {
		peer["reserved"] = s.Reserved
	}
	if s.PersistentKeepaliveInterval > 0 {
		peer["persistent_keepalive_interval"] = s.PersistentKeepaliveInterval
	}

	return map[string]any{
		"type":        "wireguard",
		"tag":         tag,
		"address":     addrs,
		"private_key": privKey,
		"mtu":         mtu,
		"peers":       []any{peer},
	}, nil
}

func compileSimpleProxyOutbound(tag, typ string, raw json.RawMessage) (map[string]any, error) {
	var s proxyOutboundSettings
	if err := decodeSettings(tag, raw, &s); err != nil {
		return nil, err
	}

	server := strings.TrimSpace(s.Server)
	if server == "" {
		return nil, fmt.Errorf("configgen: outbound %q (%s) requires server", tag, typ)
	}
	port := s.ServerPort
	if port == 0 {
		port = s.Port
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("configgen: outbound %q (%s) invalid server_port %d", tag, typ, port)
	}

	ob := map[string]any{
		"type":        typ,
		"tag":         tag,
		"server":      server,
		"server_port": port,
	}
	if typ == "socks" {
		if ver := strings.TrimSpace(s.Version); ver != "" {
			ob["version"] = ver
		}
	}
	if u := strings.TrimSpace(s.Username); u != "" {
		ob["username"] = u
	}
	if p := strings.TrimSpace(s.Password); p != "" {
		ob["password"] = p
	}

	if typ == "http" {
		tlsObj, err := parseOutboundTLS(s)
		if err != nil {
			return nil, fmt.Errorf("configgen: outbound %q invalid tls settings: %w", tag, err)
		}
		if tlsObj != nil {
			ob["tls"] = tlsObj
		}
	}
	return ob, nil
}

func parseOutboundTLS(s proxyOutboundSettings) (map[string]any, error) {
	enabled := s.TLSEnabled
	serverName := strings.TrimSpace(s.ServerName)
	insecure := s.Insecure

	if len(s.TLS) > 0 && string(s.TLS) != "null" {
		var b bool
		if err := json.Unmarshal(s.TLS, &b); err == nil {
			enabled = b
		} else {
			var obj struct {
				Enabled    *bool  `json:"enabled"`
				ServerName string `json:"server_name"`
				Insecure   bool   `json:"insecure"`
			}
			if err := json.Unmarshal(s.TLS, &obj); err != nil {
				return nil, err
			}
			if obj.Enabled != nil {
				enabled = *obj.Enabled
			} else {
				enabled = true
			}
			if strings.TrimSpace(obj.ServerName) != "" {
				serverName = strings.TrimSpace(obj.ServerName)
			}
			if obj.Insecure {
				insecure = true
			}
		}
	}

	if !enabled {
		return nil, nil
	}
	tlsObj := map[string]any{
		"enabled": true,
	}
	if serverName != "" {
		tlsObj["server_name"] = serverName
	}
	if insecure {
		tlsObj["insecure"] = true
	}
	return tlsObj, nil
}

func compileUpstreamGroups(groups []UpstreamGroupSpec, seenTags map[string]struct{}) ([]any, error) {
	if len(groups) == 0 {
		return nil, nil
	}

	var groupObjs []any
	var nodeObjs []any

	for idx, g := range groups {
		decodedNodes := make([]map[string]any, 0, len(g.Nodes))
		derivedTags := make([]string, 0, len(g.Nodes))
		seenDerived := make(map[string]struct{}, len(g.Nodes))

		for j, raw := range g.Nodes {
			val, err := decodeSingleJSON(raw)
			if err != nil {
				return nil, fmt.Errorf("configgen: upstream_groups[%d].nodes[%d] invalid JSON: %w", idx, j, err)
			}
			obj, ok := val.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("configgen: upstream_groups[%d].nodes[%d] must be a JSON object", idx, j)
			}
			tag, _ := obj["tag"].(string)
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if _, seenLocally := seenDerived[tag]; !seenLocally {
				seenDerived[tag] = struct{}{}
				derivedTags = append(derivedTags, tag)
			}
			if _, dup := seenTags[tag]; dup {
				continue
			}
			seenTags[tag] = struct{}{}
			decodedNodes = append(decodedNodes, obj)
		}

		var nodeTags []string
		if len(g.NodeTags) > 0 {
			seenInGroup := make(map[string]struct{}, len(g.NodeTags))
			for _, t := range g.NodeTags {
				tag := strings.TrimSpace(t)
				if tag == "" {
					continue
				}
				if _, dup := seenInGroup[tag]; dup {
					continue
				}
				seenInGroup[tag] = struct{}{}
				nodeTags = append(nodeTags, tag)
			}
		} else {
			nodeTags = derivedTags
		}

		// Skip empty upstream groups cleanly so sing-box check never fails on empty selector/urltest.
		if len(nodeTags) == 0 {
			continue
		}

		groupTag := strings.TrimSpace(g.GroupTag)
		if groupTag == "" {
			return nil, fmt.Errorf("configgen: upstream_groups[%d] requires group_tag", idx)
		}

		selectorOutbounds := make([]string, 0, len(nodeTags)+1)
		if g.EnableURLTest {
			urltestTag := strings.TrimSpace(g.URLTestTag)
			if urltestTag == "" {
				urltestTag = groupTag + "-auto"
			}
			testURL := strings.TrimSpace(g.TestURL)
			if testURL == "" {
				testURL = defaultURLTestURL
			}
			interval := strings.TrimSpace(g.TestInterval)
			if interval == "" {
				interval = defaultURLTestInterval
			}
			tolerance := g.Tolerance
			if tolerance <= 0 {
				tolerance = defaultURLTestTolerance
			}

			if _, dup := seenTags[urltestTag]; !dup {
				seenTags[urltestTag] = struct{}{}
				groupObjs = append(groupObjs, map[string]any{
					"type":      "urltest",
					"tag":       urltestTag,
					"outbounds": nodeTags,
					"url":       testURL,
					"interval":  interval,
					"tolerance": tolerance,
				})
			}
			selectorOutbounds = append(selectorOutbounds, urltestTag)
		}

		selectorOutbounds = append(selectorOutbounds, nodeTags...)
		if _, dup := seenTags[groupTag]; !dup {
			seenTags[groupTag] = struct{}{}
			groupObjs = append(groupObjs, map[string]any{
				"type":      "selector",
				"tag":       groupTag,
				"outbounds": selectorOutbounds,
			})
		}

		for _, n := range decodedNodes {
			nodeObjs = append(nodeObjs, n)
		}
	}

	out := make([]any, 0, len(groupObjs)+len(nodeObjs))
	out = append(out, groupObjs...)
	out = append(out, nodeObjs...)
	return out, nil
}

func compileStructuredRouteRules(specs []RouteRuleSpec) ([]any, []string, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}

	sorted := make([]RouteRuleSpec, len(specs))
	copy(sorted, specs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	rules := make([]any, 0, len(sorted))
	var referencedRuleSets []string

	for i, r := range sorted {
		action := strings.ToLower(strings.TrimSpace(r.Action))
		if action == "" {
			action = "route"
		}
		switch action {
		case "route", "reject", "hijack-dns", "sniff":
		default:
			return nil, nil, fmt.Errorf("configgen: route rule[%d] unsupported action %q", i, r.Action)
		}

		obj := map[string]any{}

		if rs := cleanStringList(r.RuleSets); len(rs) > 0 {
			obj["rule_set"] = rs
			referencedRuleSets = append(referencedRuleSets, rs...)
		}
		if v := cleanStringList(r.Domains); len(v) > 0 {
			obj["domain"] = v
		}
		if v := cleanStringList(r.DomainSuffix); len(v) > 0 {
			obj["domain_suffix"] = v
		}
		if v := cleanStringList(r.DomainKeyword); len(v) > 0 {
			obj["domain_keyword"] = v
		}
		if v := cleanStringList(r.IPCIDR); len(v) > 0 {
			obj["ip_cidr"] = v
		}
		if v := cleanStringList(r.Protocols); len(v) > 0 {
			obj["protocol"] = v
		}
		if len(r.Ports) > 0 {
			for _, p := range r.Ports {
				if p < 1 || p > 65535 {
					return nil, nil, fmt.Errorf("configgen: route rule[%d] invalid port %d", i, p)
				}
			}
			ports := make([]int, len(r.Ports))
			copy(ports, r.Ports)
			obj["port"] = ports
		}
		if v := cleanStringList(r.AuthUsers); len(v) > 0 {
			obj["auth_user"] = v
		}
		if v := cleanStringList(r.Inbounds); len(v) > 0 {
			obj["inbound"] = v
		}
		if r.IPIsPrivate {
			obj["ip_is_private"] = true
		}

		switch action {
		case "route":
			outTag := strings.TrimSpace(r.OutboundTag)
			if outTag == "" {
				return nil, nil, fmt.Errorf("configgen: route rule[%d] with action \"route\" requires outbound_tag", i)
			}
			obj["action"] = "route"
			obj["outbound"] = outTag
		case "reject":
			obj["action"] = "reject"
		case "hijack-dns":
			obj["action"] = "hijack-dns"
		case "sniff":
			obj["action"] = "sniff"
		}

		rules = append(rules, obj)
	}

	return rules, referencedRuleSets, nil
}

func compileRuleSets(explicit []RuleSetSpec, referencedTags []string) ([]any, error) {
	seen := make(map[string]struct{}, len(explicit)+len(referencedTags))
	out := make([]any, 0, len(explicit)+len(referencedTags))

	appendSpec := func(rs RuleSetSpec) {
		tag := strings.TrimSpace(rs.Tag)
		if tag == "" {
			return
		}
		if _, dup := seen[tag]; dup {
			return
		}
		seen[tag] = struct{}{}

		detour := strings.TrimSpace(rs.DownloadDetour)
		if detour == "" {
			detour = "direct"
		}
		interval := strings.TrimSpace(rs.UpdateInterval)
		if interval == "" {
			interval = defaultRuleSetInterval
		}

		out = append(out, map[string]any{
			"type":            "remote",
			"tag":             tag,
			"format":          "binary",
			"url":             strings.TrimSpace(rs.URL),
			"download_detour": detour,
			"update_interval": interval,
		})
	}

	for i, rs := range explicit {
		tag := strings.TrimSpace(rs.Tag)
		if tag == "" {
			return nil, fmt.Errorf("configgen: rule_sets[%d] tag is required", i)
		}
		u := strings.TrimSpace(rs.URL)
		if u == "" {
			if builtin, ok := BuiltinRuleSetURL(tag); ok {
				rs.URL = builtin
			} else {
				return nil, fmt.Errorf("configgen: rule_sets[%d] (%s) url is required", i, tag)
			}
		}
		appendSpec(rs)
	}

	for _, tag := range referencedTags {
		if _, exists := seen[tag]; exists {
			continue
		}
		if builtinURL, ok := BuiltinRuleSetURL(tag); ok {
			appendSpec(RuleSetSpec{
				Tag:            tag,
				URL:            builtinURL,
				DownloadDetour: "direct",
				UpdateInterval: defaultRuleSetInterval,
			})
		}
	}

	return out, nil
}

func cleanStringList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

func normalizeCIDR(addr string) string {
	s := strings.TrimSpace(addr)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "/") {
		return s
	}
	if ip := net.ParseIP(s); ip != nil {
		if ip.To4() != nil {
			return s + "/32"
		}
		return s + "/128"
	}
	return s
}
