package syncer

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseClashSubscription detects a Clash / Mihomo YAML document containing a top-level
// "proxies" sequence and converts supported proxy entries to modern sing-box 1.12+ outbounds.
func parseClashSubscription(data []byte) ([]parsedCandidate, bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, false, nil
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, false, nil
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, false, nil
	}

	var proxiesNode *yaml.Node
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "proxies" {
			proxiesNode = top.Content[i+1]
			break
		}
	}
	if proxiesNode == nil {
		return nil, false, nil
	}
	if proxiesNode.Kind == yaml.ScalarNode && proxiesNode.Tag == "!!null" {
		return []parsedCandidate{}, true, nil
	}
	if proxiesNode.Kind != yaml.SequenceNode {
		return nil, false, nil
	}

	candidates := make([]parsedCandidate, 0, len(proxiesNode.Content))
	for _, itemNode := range proxiesNode.Content {
		var raw map[string]any
		if err := itemNode.Decode(&raw); err != nil || len(raw) == 0 {
			continue
		}
		c, ok := convertClashProxy(raw)
		if !ok {
			continue
		}
		candidates = append(candidates, c)
	}

	return candidates, true, nil
}

func convertClashProxy(p map[string]any) (parsedCandidate, bool) {
	clashType := strings.ToLower(strings.TrimSpace(asString(p["type"])))
	server := strings.TrimSpace(asString(p["server"]))
	port, hasPort := asInt(p["port"])
	if server == "" || !hasPort || port < 1 || port > 65535 {
		return parsedCandidate{}, false
	}

	name := strings.TrimSpace(asString(p["name"]))
	if name == "" {
		name = fmt.Sprintf("%s-%s-%d", clashType, server, port)
	}

	switch clashType {
	case "vless":
		return convertClashVLESS(name, server, port, p)
	case "vmess":
		return convertClashVMess(name, server, port, p)
	case "trojan":
		return convertClashTrojan(name, server, port, p)
	case "ss", "shadowsocks":
		return convertClashSS(name, server, port, p)
	case "hysteria2", "hy2":
		return convertClashHysteria2(name, server, port, p)
	case "tuic":
		return convertClashTUIC(name, server, port, p)
	case "anytls":
		return convertClashAnyTLS(name, server, port, p)
	case "socks5", "socks":
		return convertClashSocks(name, server, port, p)
	case "http":
		return convertClashHTTP(name, server, port, p)
	default:
		return parsedCandidate{}, false
	}
}

func convertClashVLESS(name, server string, port int, p map[string]any) (parsedCandidate, bool) {
	uuid := strings.TrimSpace(asString(p["uuid"]))
	if uuid == "" {
		return parsedCandidate{}, false
	}

	obj := map[string]any{
		"type":        "vless",
		"server":      server,
		"server_port": port,
		"uuid":        uuid,
	}

	if flow := strings.TrimSpace(asString(p["flow"])); flow != "" && !strings.EqualFold(flow, "none") {
		obj["flow"] = flow
	}
	if pe := strings.TrimSpace(asString(p["packet-encoding"])); pe != "" {
		obj["packet_encoding"] = pe
	}
	applyClashUDPNetwork(obj, p)

	tlsEnabled, _ := asBool(p["tls"])
	if tlsObj := buildClashTLS(p, tlsEnabled); tlsObj != nil {
		obj["tls"] = tlsObj
	}
	if tr := buildClashTransport(p); tr != nil {
		obj["transport"] = tr
	}

	return parsedCandidate{
		OriginalName: name,
		Type:         "vless",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func convertClashVMess(name, server string, port int, p map[string]any) (parsedCandidate, bool) {
	uuid := strings.TrimSpace(asString(p["uuid"]))
	if uuid == "" {
		return parsedCandidate{}, false
	}

	security := strings.TrimSpace(asString(p["cipher"]))
	if security == "" {
		security = "auto"
	}

	obj := map[string]any{
		"type":        "vmess",
		"server":      server,
		"server_port": port,
		"uuid":        uuid,
		"security":    security,
	}

	if aid, ok := asInt(p["alterId"]); ok && aid > 0 {
		obj["alter_id"] = aid
	}
	if gp, ok := asBool(p["global-padding"]); ok && gp {
		obj["global_padding"] = true
	}
	if al, ok := asBool(p["authenticated-length"]); ok && al {
		obj["authenticated_length"] = true
	}
	if pe := strings.TrimSpace(asString(p["packet-encoding"])); pe != "" {
		obj["packet_encoding"] = pe
	}
	applyClashUDPNetwork(obj, p)

	tlsEnabled, _ := asBool(p["tls"])
	if tlsObj := buildClashTLS(p, tlsEnabled); tlsObj != nil {
		obj["tls"] = tlsObj
	}
	if tr := buildClashTransport(p); tr != nil {
		obj["transport"] = tr
	}

	return parsedCandidate{
		OriginalName: name,
		Type:         "vmess",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func convertClashTrojan(name, server string, port int, p map[string]any) (parsedCandidate, bool) {
	password := strings.TrimSpace(asString(p["password"]))
	if password == "" {
		return parsedCandidate{}, false
	}

	obj := map[string]any{
		"type":        "trojan",
		"server":      server,
		"server_port": port,
		"password":    password,
	}
	applyClashUDPNetwork(obj, p)

	tlsEnabled := true
	if v, ok := asBool(p["tls"]); ok {
		tlsEnabled = v
	}
	if tlsObj := buildClashTLS(p, tlsEnabled); tlsObj != nil {
		obj["tls"] = tlsObj
	}
	if tr := buildClashTransport(p); tr != nil {
		obj["transport"] = tr
	}

	return parsedCandidate{
		OriginalName: name,
		Type:         "trojan",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func convertClashSS(name, server string, port int, p map[string]any) (parsedCandidate, bool) {
	method := strings.TrimSpace(asString(p["cipher"]))
	if method == "" {
		method = strings.TrimSpace(asString(p["method"]))
	}
	password := asString(p["password"])
	if method == "" {
		return parsedCandidate{}, false
	}

	obj := map[string]any{
		"type":        "shadowsocks",
		"server":      server,
		"server_port": port,
		"method":      method,
		"password":    password,
	}
	applyClashUDPNetwork(obj, p)

	if uot, ok := asBool(p["udp-over-tcp"]); ok && uot {
		obj["udp_over_tcp"] = true
	}

	plugin := strings.TrimSpace(asString(p["plugin"]))
	if plugin != "" {
		pluginOpts := asMap(p["plugin-opts"])
		switch strings.ToLower(plugin) {
		case "obfs", "simple-obfs", "obfs-local":
			obj["plugin"] = "obfs-local"
			var parts []string
			if mode := strings.TrimSpace(asString(pluginOpts["mode"])); mode != "" {
				parts = append(parts, "obfs="+mode)
			}
			if host := strings.TrimSpace(asString(pluginOpts["host"])); host != "" {
				parts = append(parts, "obfs-host="+host)
			}
			if len(parts) > 0 {
				obj["plugin_opts"] = strings.Join(parts, ";")
			}
		case "v2ray-plugin":
			obj["plugin"] = "v2ray-plugin"
			var parts []string
			if mode := strings.TrimSpace(asString(pluginOpts["mode"])); mode != "" {
				parts = append(parts, "mode="+mode)
			}
			if tlsOn, ok := asBool(pluginOpts["tls"]); ok && tlsOn {
				parts = append(parts, "tls")
			}
			if host := strings.TrimSpace(asString(pluginOpts["host"])); host != "" {
				parts = append(parts, "host="+host)
			}
			if path := strings.TrimSpace(asString(pluginOpts["path"])); path != "" {
				parts = append(parts, "path="+path)
			}
			if mux, ok := asBool(pluginOpts["mux"]); ok && !mux {
				parts = append(parts, "mux=0")
			}
			if len(parts) > 0 {
				obj["plugin_opts"] = strings.Join(parts, ";")
			}
		default:
			obj["plugin"] = plugin
			if rawOpts := strings.TrimSpace(asString(p["plugin-opts"])); rawOpts != "" && pluginOpts == nil {
				obj["plugin_opts"] = rawOpts
			} else if len(pluginOpts) > 0 {
				keys := make([]string, 0, len(pluginOpts))
				for k := range pluginOpts {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, fmt.Sprintf("%s=%s", k, asString(pluginOpts[k])))
				}
				obj["plugin_opts"] = strings.Join(parts, ";")
			}
		}
	}

	return parsedCandidate{
		OriginalName: name,
		Type:         "shadowsocks",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func convertClashHysteria2(name, server string, port int, p map[string]any) (parsedCandidate, bool) {
	password := strings.TrimSpace(asString(p["password"]))
	if password == "" {
		password = strings.TrimSpace(asString(p["auth"]))
	}
	if password == "" {
		return parsedCandidate{}, false
	}

	obj := map[string]any{
		"type":        "hysteria2",
		"server":      server,
		"server_port": port,
		"password":    password,
	}

	if portsRaw := strings.TrimSpace(asString(p["ports"])); portsRaw != "" {
		if ranges := normalizePortRanges(portsRaw); len(ranges) > 0 {
			obj["server_ports"] = ranges
		}
	}
	if hopRaw := p["hop-interval"]; hopRaw != nil {
		if sec, ok := asInt(hopRaw); ok && sec > 0 {
			obj["hop_interval"] = fmt.Sprintf("%ds", sec)
		} else if s := strings.TrimSpace(asString(hopRaw)); s != "" {
			obj["hop_interval"] = s
		}
	}
	if up := parseBandwidthMbps(p["up"]); up > 0 {
		obj["up_mbps"] = up
	}
	if down := parseBandwidthMbps(p["down"]); down > 0 {
		obj["down_mbps"] = down
	}

	obfsType := strings.ToLower(strings.TrimSpace(asString(p["obfs"])))
	obfsPw := strings.TrimSpace(asString(p["obfs-password"]))
	if obfsType != "" && obfsType != "none" && obfsPw != "" {
		obj["obfs"] = map[string]any{
			"type":     obfsType,
			"password": obfsPw,
		}
	}

	tlsObj := buildClashTLS(p, true)
	obj["tls"] = tlsObj

	return parsedCandidate{
		OriginalName: name,
		Type:         "hysteria2",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func convertClashTUIC(name, server string, port int, p map[string]any) (parsedCandidate, bool) {
	uuid := strings.TrimSpace(asString(p["uuid"]))
	if uuid == "" {
		return parsedCandidate{}, false
	}
	password := asString(p["password"])

	obj := map[string]any{
		"type":        "tuic",
		"server":      server,
		"server_port": port,
		"uuid":        uuid,
		"password":    password,
	}

	cc := strings.TrimSpace(asString(p["congestion-controller"]))
	if cc == "" {
		cc = strings.TrimSpace(asString(p["congestion-control"]))
	}
	if cc != "" {
		obj["congestion_control"] = cc
	}
	if mode := strings.TrimSpace(asString(p["udp-relay-mode"])); mode != "" {
		obj["udp_relay_mode"] = mode
	}
	if rtt, ok := asBool(p["reduce-rtt"]); ok && rtt {
		obj["zero_rtt_handshake"] = true
	}
	if hbMs, ok := asInt(p["heartbeat-interval"]); ok && hbMs > 0 {
		if hbMs%1000 == 0 {
			obj["heartbeat"] = fmt.Sprintf("%ds", hbMs/1000)
		} else {
			obj["heartbeat"] = fmt.Sprintf("%dms", hbMs)
		}
	} else if hb := strings.TrimSpace(asString(p["heartbeat"])); hb != "" {
		obj["heartbeat"] = hb
	}

	tlsObj := buildClashTLS(p, true)
	if disableSNI, ok := asBool(p["disable-sni"]); ok && disableSNI {
		tlsObj["disable_sni"] = true
	}
	obj["tls"] = tlsObj

	return parsedCandidate{
		OriginalName: name,
		Type:         "tuic",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func convertClashAnyTLS(name, server string, port int, p map[string]any) (parsedCandidate, bool) {
	password := strings.TrimSpace(asString(p["password"]))
	if password == "" {
		return parsedCandidate{}, false
	}

	obj := map[string]any{
		"type":        "anytls",
		"server":      server,
		"server_port": port,
		"password":    password,
	}

	if v := p["idle-session-check-interval"]; v != nil {
		if sec, ok := asInt(v); ok && sec > 0 {
			obj["idle_session_check_interval"] = fmt.Sprintf("%ds", sec)
		} else if s := strings.TrimSpace(asString(v)); s != "" {
			obj["idle_session_check_interval"] = s
		}
	}
	if v := p["idle-session-timeout"]; v != nil {
		if sec, ok := asInt(v); ok && sec > 0 {
			obj["idle_session_timeout"] = fmt.Sprintf("%ds", sec)
		} else if s := strings.TrimSpace(asString(v)); s != "" {
			obj["idle_session_timeout"] = s
		}
	}
	if v, ok := asInt(p["min-idle-session"]); ok && v > 0 {
		obj["min_idle_session"] = v
	}

	obj["tls"] = buildClashTLS(p, true)

	return parsedCandidate{
		OriginalName: name,
		Type:         "anytls",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func convertClashSocks(name, server string, port int, p map[string]any) (parsedCandidate, bool) {
	obj := map[string]any{
		"type":        "socks",
		"server":      server,
		"server_port": port,
		"version":     "5",
	}
	if u := strings.TrimSpace(asString(p["username"])); u != "" {
		obj["username"] = u
	}
	if pw := asString(p["password"]); pw != "" {
		obj["password"] = pw
	}
	applyClashUDPNetwork(obj, p)

	return parsedCandidate{
		OriginalName: name,
		Type:         "socks",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func convertClashHTTP(name, server string, port int, p map[string]any) (parsedCandidate, bool) {
	obj := map[string]any{
		"type":        "http",
		"server":      server,
		"server_port": port,
	}
	if u := strings.TrimSpace(asString(p["username"])); u != "" {
		obj["username"] = u
	}
	if pw := asString(p["password"]); pw != "" {
		obj["password"] = pw
	}
	if tlsEnabled, _ := asBool(p["tls"]); tlsEnabled {
		obj["tls"] = buildClashTLS(p, true)
	}

	return parsedCandidate{
		OriginalName: name,
		Type:         "http",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func applyClashUDPNetwork(obj, p map[string]any) {
	if udp, ok := asBool(p["udp"]); ok && !udp {
		obj["network"] = "tcp"
	}
}

func buildClashTLS(p map[string]any, enabled bool) map[string]any {
	sni := strings.TrimSpace(asString(p["servername"]))
	if sni == "" {
		sni = strings.TrimSpace(asString(p["sni"]))
	}
	insecure, _ := asBool(p["skip-cert-verify"])
	alpn := asStringSlice(p["alpn"])
	fp := strings.TrimSpace(asString(p["client-fingerprint"]))

	var realityPub, realitySID string
	if ro := asMap(p["reality-opts"]); len(ro) > 0 {
		realityPub = strings.TrimSpace(asString(ro["public-key"]))
		realitySID = strings.TrimSpace(asString(ro["short-id"]))
		if realityPub != "" {
			enabled = true
		}
	}

	return buildTLSObject(tlsBuildParams{
		Enabled:     enabled,
		ServerName:  sni,
		Insecure:    insecure,
		ALPN:        alpn,
		Fingerprint: fp,
		RealityPub:  realityPub,
		RealitySID:  realitySID,
	})
}

func buildClashTransport(p map[string]any) map[string]any {
	network := strings.ToLower(strings.TrimSpace(asString(p["network"])))
	switch network {
	case "", "tcp", "raw":
		return nil

	case "ws":
		opts := asMap(p["ws-opts"])
		path := strings.TrimSpace(asString(opts["path"]))
		cleanPath, edFromPath := extractWSEarlyData(path)
		tr := map[string]any{
			"type": "ws",
			"path": cleanPath,
		}
		if hdrs := asStringMap(opts["headers"]); len(hdrs) > 0 {
			tr["headers"] = hdrs
		}
		ed, ok := asInt(opts["max-early-data"])
		if !ok || ed <= 0 {
			ed = edFromPath
		}
		if ed > 0 {
			tr["max_early_data"] = ed
			edHeader := strings.TrimSpace(asString(opts["early-data-header-name"]))
			if edHeader == "" {
				edHeader = "Sec-WebSocket-Protocol"
			}
			tr["early_data_header_name"] = edHeader
		}
		return tr

	case "grpc":
		opts := asMap(p["grpc-opts"])
		svc := strings.TrimSpace(asString(opts["grpc-service-name"]))
		if svc == "" {
			svc = strings.TrimSpace(asString(opts["service-name"]))
		}
		tr := map[string]any{
			"type": "grpc",
		}
		if svc != "" {
			tr["service_name"] = svc
		}
		return tr

	case "h2":
		opts := asMap(p["h2-opts"])
		tr := map[string]any{
			"type": "http",
		}
		if hosts := asStringSlice(opts["host"]); len(hosts) > 0 {
			tr["host"] = hosts
		}
		if path := strings.TrimSpace(asString(opts["path"])); path != "" {
			tr["path"] = path
		}
		return tr

	case "http":
		opts := asMap(p["http-opts"])
		tr := map[string]any{
			"type": "http",
		}
		if method := strings.TrimSpace(asString(opts["method"])); method != "" {
			tr["method"] = method
		}
		if paths := asStringSlice(opts["path"]); len(paths) > 0 {
			tr["path"] = paths[0]
		}
		if hdrMap := asMap(opts["headers"]); len(hdrMap) > 0 {
			cleanHdrs := make(map[string]string, len(hdrMap))
			for k, v := range hdrMap {
				vals := asStringSlice(v)
				if len(vals) == 0 {
					continue
				}
				if strings.EqualFold(k, "host") {
					tr["host"] = vals
				} else {
					cleanHdrs[k] = vals[0]
				}
			}
			if len(cleanHdrs) > 0 {
				tr["headers"] = cleanHdrs
			}
		}
		return tr

	case "httpupgrade":
		opts := asMap(p["httpupgrade-opts"])
		if len(opts) == 0 {
			opts = asMap(p["ws-opts"])
		}
		path := strings.TrimSpace(asString(opts["path"]))
		if path == "" {
			path = "/"
		}
		tr := map[string]any{
			"type": "httpupgrade",
			"path": path,
		}
		host := strings.TrimSpace(asString(opts["host"]))
		if hdrs := asStringMap(opts["headers"]); len(hdrs) > 0 {
			if host == "" {
				for k, v := range hdrs {
					if strings.EqualFold(k, "host") {
						host = v
						delete(hdrs, k)
						break
					}
				}
			}
			if len(hdrs) > 0 {
				tr["headers"] = hdrs
			}
		}
		if host != "" {
			tr["host"] = host
		}
		return tr

	default:
		return nil
	}
}
