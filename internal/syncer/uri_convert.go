package syncer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// parseURISubscription parses either a plain newline-separated proxy URI list or a
// Base64-encoded (Std/Raw/URL-safe, ignoring whitespace) proxy URI list.
func parseURISubscription(data []byte) ([]parsedCandidate, bool, error) {
	text := string(data)

	// Reject obvious HTML documents before scanning lines for http:// links.
	lowerPrefix := strings.ToLower(strings.TrimSpace(text))
	if strings.HasPrefix(lowerPrefix, "<!doctype") || strings.HasPrefix(lowerPrefix, "<html") {
		return nil, false, nil
	}

	if candidates := parseURILines(text); len(candidates) > 0 {
		return candidates, true, nil
	}

	// Try Base64 decoding (ignoring all whitespace/newlines).
	decoded, ok := decodeFlexibleBase64(data)
	if !ok || !utf8.Valid(decoded) {
		return nil, false, nil
	}
	decodedText := string(bytes.TrimPrefix(decoded, []byte{0xEF, 0xBB, 0xBF}))
	if candidates := parseURILines(decodedText); len(candidates) > 0 {
		return candidates, true, nil
	}

	return nil, false, nil
}

func parseURILines(text string) []parsedCandidate {
	lines := strings.Split(text, "\n")
	out := make([]parsedCandidate, 0, len(lines))
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if c, ok := parseSingleURI(line); ok {
			out = append(out, c)
		}
	}
	return out
}

func parseSingleURI(line string) (parsedCandidate, bool) {
	schemeIdx := strings.Index(line, "://")
	if schemeIdx <= 0 {
		return parsedCandidate{}, false
	}
	scheme := strings.ToLower(line[:schemeIdx])

	switch scheme {
	case "vless":
		return parseVLESSURI(line)
	case "vmess":
		return parseVMessURI(line)
	case "trojan":
		return parseTrojanURI(line)
	case "ss":
		return parseSSURI(line)
	case "hysteria2", "hy2":
		return parseHysteria2URI(line)
	case "tuic":
		return parseTUICURI(line)
	case "anytls":
		return parseAnyTLSURI(line)
	case "socks5", "socks", "socks5h":
		return parseSocksURI(line)
	case "http", "https":
		return parseHTTPURI(line, scheme == "https")
	default:
		return parsedCandidate{}, false
	}
}

func splitURIFragment(raw string) (withoutFrag, name string) {
	idx := strings.IndexByte(raw, '#')
	if idx < 0 {
		return raw, ""
	}
	frag := raw[idx+1:]
	if unescaped, err := url.PathUnescape(frag); err == nil {
		name = strings.TrimSpace(unescaped)
	} else {
		name = strings.TrimSpace(frag)
	}
	return raw[:idx], name
}

func parseURLWithFragment(raw string) (*url.URL, string, error) {
	clean, name := splitURIFragment(raw)
	u, err := url.Parse(clean)
	if err != nil {
		return nil, "", err
	}
	return u, name, nil
}

func extractHostPort(u *url.URL, defaultPort int) (string, int, bool) {
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return "", 0, false
	}
	portStr := u.Port()
	if portStr == "" {
		if defaultPort > 0 {
			return host, defaultPort, true
		}
		return "", 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	return host, port, true
}

func extractRawPasswordFromUserinfo(u *url.URL) string {
	if u.User == nil {
		return ""
	}
	user := u.User.Username()
	if pw, hasPw := u.User.Password(); hasPw {
		return user + ":" + pw
	}
	return user
}

func queryBool(q url.Values, keys ...string) bool {
	for _, k := range keys {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			if b, ok := asBool(v); ok {
				return b
			}
		}
	}
	return false
}

func queryFirst(q url.Values, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			return v
		}
	}
	return ""
}

func buildURITransport(q url.Values) map[string]any {
	netType := strings.ToLower(queryFirst(q, "type", "net"))
	headerType := strings.ToLower(queryFirst(q, "headerType", "header_type"))
	if (netType == "" || netType == "tcp") && headerType == "http" {
		netType = "http"
	}

	switch netType {
	case "", "tcp", "raw", "none":
		return nil

	case "ws":
		path := queryFirst(q, "path")
		cleanPath, ed := extractWSEarlyData(path)
		tr := map[string]any{
			"type": "ws",
			"path": cleanPath,
		}
		if host := queryFirst(q, "host"); host != "" {
			tr["headers"] = map[string]string{
				"Host": host,
			}
		}
		if ed > 0 {
			tr["max_early_data"] = ed
			tr["early_data_header_name"] = "Sec-WebSocket-Protocol"
		}
		return tr

	case "grpc":
		tr := map[string]any{
			"type": "grpc",
		}
		if svc := queryFirst(q, "serviceName", "service_name", "path"); svc != "" {
			tr["service_name"] = svc
		}
		return tr

	case "h2", "http":
		tr := map[string]any{
			"type": "http",
		}
		if host := queryFirst(q, "host"); host != "" {
			if hosts := splitCommaList(host); len(hosts) > 0 {
				tr["host"] = hosts
			}
		}
		if path := queryFirst(q, "path"); path != "" {
			tr["path"] = path
		}
		return tr

	case "httpupgrade":
		path := queryFirst(q, "path")
		if path == "" {
			path = "/"
		}
		tr := map[string]any{
			"type": "httpupgrade",
			"path": path,
		}
		if host := queryFirst(q, "host"); host != "" {
			tr["host"] = host
		}
		return tr

	default:
		return nil
	}
}

func parseVLESSURI(raw string) (parsedCandidate, bool) {
	u, name, err := parseURLWithFragment(raw)
	if err != nil {
		return parsedCandidate{}, false
	}
	server, port, ok := extractHostPort(u, 443)
	if !ok {
		return parsedCandidate{}, false
	}
	uuid := ""
	if u.User != nil {
		uuid = strings.TrimSpace(u.User.Username())
	}
	if uuid == "" {
		return parsedCandidate{}, false
	}
	if name == "" {
		name = fmt.Sprintf("vless-%s-%d", server, port)
	}

	q := u.Query()
	obj := map[string]any{
		"type":        "vless",
		"server":      server,
		"server_port": port,
		"uuid":        uuid,
	}

	if flow := queryFirst(q, "flow"); flow != "" && !strings.EqualFold(flow, "none") {
		obj["flow"] = flow
	}
	if pe := queryFirst(q, "packetEncoding", "packet_encoding"); pe != "" {
		obj["packet_encoding"] = pe
	}

	security := strings.ToLower(queryFirst(q, "security"))
	pbk := queryFirst(q, "pbk")
	sid := queryFirst(q, "sid")
	tlsEnabled := security == "tls" || security == "reality" || pbk != ""
	if tlsObj := buildTLSObject(tlsBuildParams{
		Enabled:     tlsEnabled,
		ServerName:  queryFirst(q, "sni", "peer"),
		Insecure:    queryBool(q, "allowInsecure", "insecure", "allow_insecure"),
		ALPN:        splitCommaList(queryFirst(q, "alpn")),
		Fingerprint: queryFirst(q, "fp", "fingerprint"),
		RealityPub:  pbk,
		RealitySID:  sid,
	}); tlsObj != nil {
		obj["tls"] = tlsObj
	}

	if tr := buildURITransport(q); tr != nil {
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

func parseVMessURI(raw string) (parsedCandidate, bool) {
	rest := strings.TrimSpace(raw[len("vmess://"):])
	if rest == "" {
		return parsedCandidate{}, false
	}

	// Primary format: V2RayN Base64-encoded JSON.
	payloadNoFrag, fragName := splitURIFragment(rest)
	if decoded, ok := decodeFlexibleBase64([]byte(payloadNoFrag)); ok {
		dec := json.NewDecoder(bytes.NewReader(decoded))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err == nil && len(m) > 0 {
			return convertVMessJSON(m, fragName)
		}
	}

	// Fallback: URL-style vmess://uuid@host:port?...#name
	u, name, err := parseURLWithFragment(raw)
	if err != nil {
		return parsedCandidate{}, false
	}
	server, port, ok := extractHostPort(u, 443)
	if !ok {
		return parsedCandidate{}, false
	}
	uuid := ""
	if u.User != nil {
		uuid = strings.TrimSpace(u.User.Username())
	}
	if uuid == "" {
		return parsedCandidate{}, false
	}
	if name == "" {
		name = fmt.Sprintf("vmess-%s-%d", server, port)
	}

	q := u.Query()
	sec := queryFirst(q, "encryption", "scy", "security")
	if sec == "" {
		sec = "auto"
	}
	obj := map[string]any{
		"type":        "vmess",
		"server":      server,
		"server_port": port,
		"uuid":        uuid,
		"security":    sec,
	}
	if aidStr := queryFirst(q, "aid", "alterId"); aidStr != "" {
		if aid, err := strconv.Atoi(aidStr); err == nil && aid > 0 {
			obj["alter_id"] = aid
		}
	}
	tlsEnabled := strings.EqualFold(queryFirst(q, "security"), "tls") || queryBool(q, "tls")
	if tlsObj := buildTLSObject(tlsBuildParams{
		Enabled:     tlsEnabled,
		ServerName:  queryFirst(q, "sni", "peer"),
		Insecure:    queryBool(q, "allowInsecure", "insecure"),
		ALPN:        splitCommaList(queryFirst(q, "alpn")),
		Fingerprint: queryFirst(q, "fp", "fingerprint"),
	}); tlsObj != nil {
		obj["tls"] = tlsObj
	}
	if tr := buildURITransport(q); tr != nil {
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

func convertVMessJSON(m map[string]any, fallbackName string) (parsedCandidate, bool) {
	server := strings.TrimSpace(asString(m["add"]))
	port, ok := asInt(m["port"])
	uuid := strings.TrimSpace(asString(m["id"]))
	if server == "" || !ok || port < 1 || port > 65535 || uuid == "" {
		return parsedCandidate{}, false
	}

	name := strings.TrimSpace(asString(m["ps"]))
	if name == "" {
		name = fallbackName
	}
	if name == "" {
		name = fmt.Sprintf("vmess-%s-%d", server, port)
	}

	security := strings.TrimSpace(asString(m["scy"]))
	if security == "" {
		security = strings.TrimSpace(asString(m["security"]))
	}
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
	if aid, ok := asInt(m["aid"]); ok && aid > 0 {
		obj["alter_id"] = aid
	}

	tlsVal := strings.ToLower(strings.TrimSpace(asString(m["tls"])))
	tlsEnabled := tlsVal == "tls" || tlsVal == "true" || tlsVal == "1"
	sni := strings.TrimSpace(asString(m["sni"]))
	host := strings.TrimSpace(asString(m["host"]))
	if tlsEnabled && sni == "" && host != "" {
		sni = host
	}
	insecure := false
	if v, ok := asBool(m["allowInsecure"]); ok && v {
		insecure = true
	} else if v, ok := asBool(m["skip-cert-verify"]); ok && v {
		insecure = true
	} else if v, ok := asBool(m["insecure"]); ok && v {
		insecure = true
	}

	if tlsObj := buildTLSObject(tlsBuildParams{
		Enabled:     tlsEnabled,
		ServerName:  sni,
		Insecure:    insecure,
		ALPN:        asStringSlice(m["alpn"]),
		Fingerprint: strings.TrimSpace(asString(m["fp"])),
	}); tlsObj != nil {
		obj["tls"] = tlsObj
	}

	netType := strings.ToLower(strings.TrimSpace(asString(m["net"])))
	headerType := strings.ToLower(strings.TrimSpace(asString(m["type"])))
	path := strings.TrimSpace(asString(m["path"]))
	if (netType == "" || netType == "tcp") && headerType == "http" {
		netType = "http"
	}

	switch netType {
	case "ws":
		cleanPath, ed := extractWSEarlyData(path)
		tr := map[string]any{
			"type": "ws",
			"path": cleanPath,
		}
		if host != "" {
			tr["headers"] = map[string]string{
				"Host": host,
			}
		}
		if ed > 0 {
			tr["max_early_data"] = ed
			tr["early_data_header_name"] = "Sec-WebSocket-Protocol"
		}
		obj["transport"] = tr

	case "grpc":
		tr := map[string]any{
			"type": "grpc",
		}
		if path != "" {
			tr["service_name"] = path
		}
		obj["transport"] = tr

	case "h2", "http":
		tr := map[string]any{
			"type": "http",
		}
		if hosts := splitCommaList(host); len(hosts) > 0 {
			tr["host"] = hosts
		}
		if path != "" {
			tr["path"] = path
		}
		obj["transport"] = tr

	case "httpupgrade":
		if path == "" {
			path = "/"
		}
		tr := map[string]any{
			"type": "httpupgrade",
			"path": path,
		}
		if host != "" {
			tr["host"] = host
		}
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

func parseTrojanURI(raw string) (parsedCandidate, bool) {
	u, name, err := parseURLWithFragment(raw)
	if err != nil {
		return parsedCandidate{}, false
	}
	server, port, ok := extractHostPort(u, 443)
	if !ok {
		return parsedCandidate{}, false
	}
	password := extractRawPasswordFromUserinfo(u)
	if password == "" {
		return parsedCandidate{}, false
	}
	if name == "" {
		name = fmt.Sprintf("trojan-%s-%d", server, port)
	}

	q := u.Query()
	obj := map[string]any{
		"type":        "trojan",
		"server":      server,
		"server_port": port,
		"password":    password,
	}

	security := strings.ToLower(queryFirst(q, "security"))
	tlsEnabled := security != "none"
	if tlsObj := buildTLSObject(tlsBuildParams{
		Enabled:     tlsEnabled,
		ServerName:  queryFirst(q, "sni", "peer"),
		Insecure:    queryBool(q, "allowInsecure", "insecure", "allow_insecure"),
		ALPN:        splitCommaList(queryFirst(q, "alpn")),
		Fingerprint: queryFirst(q, "fp", "fingerprint"),
		RealityPub:  queryFirst(q, "pbk"),
		RealitySID:  queryFirst(q, "sid"),
	}); tlsObj != nil {
		obj["tls"] = tlsObj
	}

	if tr := buildURITransport(q); tr != nil {
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

func parseSSURI(raw string) (parsedCandidate, bool) {
	withoutFrag, name := splitURIFragment(raw)
	rest := strings.TrimSpace(withoutFrag[len("ss://"):])
	if rest == "" {
		return parsedCandidate{}, false
	}

	var (
		method     string
		password   string
		server     string
		port       int
		pluginRaw  string
		queryVals  url.Values
	)

	// Separate optional query string first.
	if qIdx := strings.IndexByte(rest, '?'); qIdx >= 0 {
		queryVals, _ = url.ParseQuery(rest[qIdx+1:])
		pluginRaw = strings.TrimSpace(queryVals.Get("plugin"))
		rest = rest[:qIdx]
	}
	rest = strings.TrimSuffix(rest, "/")

	if atIdx := strings.LastIndexByte(rest, '@'); atIdx >= 0 {
		// SIP002 format: <userinfo>@<host>:<port>
		userInfoRaw := rest[:atIdx]
		hostPort := rest[atIdx+1:]

		var ok bool
		server, port, ok = splitHostPortInt(hostPort)
		if !ok {
			return parsedCandidate{}, false
		}

		userInfoUnescaped, err := url.PathUnescape(userInfoRaw)
		if err != nil {
			userInfoUnescaped = userInfoRaw
		}

		if strings.Contains(userInfoUnescaped, ":") {
			parts := strings.SplitN(userInfoUnescaped, ":", 2)
			method = strings.TrimSpace(parts[0])
			password = parts[1]
		} else if decoded, decOK := decodeFlexibleBase64([]byte(userInfoUnescaped)); decOK {
			decStr := string(decoded)
			parts := strings.SplitN(decStr, ":", 2)
			if len(parts) != 2 {
				return parsedCandidate{}, false
			}
			method = strings.TrimSpace(parts[0])
			password = parts[1]
		} else {
			return parsedCandidate{}, false
		}
	} else {
		// Legacy Base64 format: ss://<base64(method:password@host:port)>
		decoded, decOK := decodeFlexibleBase64([]byte(rest))
		if !decOK {
			return parsedCandidate{}, false
		}
		decStr := strings.TrimSpace(string(decoded))
		atIdx := strings.LastIndexByte(decStr, '@')
		if atIdx < 0 {
			return parsedCandidate{}, false
		}
		credPart := decStr[:atIdx]
		hostPort := decStr[atIdx+1:]

		var ok bool
		server, port, ok = splitHostPortInt(hostPort)
		if !ok {
			return parsedCandidate{}, false
		}
		parts := strings.SplitN(credPart, ":", 2)
		if len(parts) != 2 {
			return parsedCandidate{}, false
		}
		method = strings.TrimSpace(parts[0])
		password = parts[1]
	}

	if method == "" || server == "" || port < 1 || port > 65535 {
		return parsedCandidate{}, false
	}
	if name == "" {
		name = fmt.Sprintf("ss-%s-%d", server, port)
	}

	obj := map[string]any{
		"type":        "shadowsocks",
		"server":      server,
		"server_port": port,
		"method":      method,
		"password":    password,
	}

	if pluginRaw != "" {
		pluginParts := strings.SplitN(pluginRaw, ";", 2)
		pluginName := strings.TrimSpace(pluginParts[0])
		if strings.EqualFold(pluginName, "simple-obfs") || strings.EqualFold(pluginName, "obfs") {
			pluginName = "obfs-local"
		}
		if pluginName != "" {
			obj["plugin"] = pluginName
			if len(pluginParts) == 2 && strings.TrimSpace(pluginParts[1]) != "" {
				obj["plugin_opts"] = strings.TrimSpace(pluginParts[1])
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

func parseHysteria2URI(raw string) (parsedCandidate, bool) {
	u, name, err := parseURLWithFragment(raw)
	if err != nil {
		return parsedCandidate{}, false
	}
	server, port, ok := extractHostPort(u, 443)
	if !ok {
		return parsedCandidate{}, false
	}
	password := extractRawPasswordFromUserinfo(u)
	if password == "" {
		return parsedCandidate{}, false
	}
	if name == "" {
		name = fmt.Sprintf("hysteria2-%s-%d", server, port)
	}

	q := u.Query()
	obj := map[string]any{
		"type":        "hysteria2",
		"server":      server,
		"server_port": port,
		"password":    password,
	}

	if mport := queryFirst(q, "mport", "ports"); mport != "" {
		if ranges := normalizePortRanges(mport); len(ranges) > 0 {
			obj["server_ports"] = ranges
		}
	}
	if up := parseBandwidthMbps(queryFirst(q, "up", "upmbps")); up > 0 {
		obj["up_mbps"] = up
	}
	if down := parseBandwidthMbps(queryFirst(q, "down", "downmbps")); down > 0 {
		obj["down_mbps"] = down
	}

	obfsType := strings.ToLower(queryFirst(q, "obfs"))
	obfsPw := queryFirst(q, "obfs-password", "obfs_password")
	if obfsType != "" && obfsType != "none" && obfsPw != "" {
		obj["obfs"] = map[string]any{
			"type":     obfsType,
			"password": obfsPw,
		}
	}

	obj["tls"] = buildTLSObject(tlsBuildParams{
		Enabled:    true,
		ServerName: queryFirst(q, "sni", "peer"),
		Insecure:   queryBool(q, "insecure", "allowInsecure", "allow_insecure"),
		ALPN:       splitCommaList(queryFirst(q, "alpn")),
	})

	return parsedCandidate{
		OriginalName: name,
		Type:         "hysteria2",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func parseTUICURI(raw string) (parsedCandidate, bool) {
	u, name, err := parseURLWithFragment(raw)
	if err != nil {
		return parsedCandidate{}, false
	}
	server, port, ok := extractHostPort(u, 443)
	if !ok {
		return parsedCandidate{}, false
	}
	if u.User == nil {
		return parsedCandidate{}, false
	}
	uuid := strings.TrimSpace(u.User.Username())
	password, _ := u.User.Password()
	if uuid == "" {
		return parsedCandidate{}, false
	}
	if name == "" {
		name = fmt.Sprintf("tuic-%s-%d", server, port)
	}

	q := u.Query()
	obj := map[string]any{
		"type":        "tuic",
		"server":      server,
		"server_port": port,
		"uuid":        uuid,
		"password":    password,
	}

	if cc := queryFirst(q, "congestion_control", "congestion-control"); cc != "" {
		obj["congestion_control"] = cc
	}
	if mode := queryFirst(q, "udp_relay_mode", "udp-relay-mode"); mode != "" {
		obj["udp_relay_mode"] = mode
	}
	if queryBool(q, "reduce_rtt", "reduce-rtt", "zero_rtt_handshake") {
		obj["zero_rtt_handshake"] = true
	}
	if hb := queryFirst(q, "heartbeat"); hb != "" {
		obj["heartbeat"] = hb
	}

	obj["tls"] = buildTLSObject(tlsBuildParams{
		Enabled:    true,
		ServerName: queryFirst(q, "sni", "peer"),
		Insecure:   queryBool(q, "allow_insecure", "allowInsecure", "insecure"),
		DisableSNI: queryBool(q, "disable_sni", "disable-sni"),
		ALPN:       splitCommaList(queryFirst(q, "alpn")),
	})

	return parsedCandidate{
		OriginalName: name,
		Type:         "tuic",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func parseAnyTLSURI(raw string) (parsedCandidate, bool) {
	u, name, err := parseURLWithFragment(raw)
	if err != nil {
		return parsedCandidate{}, false
	}
	server, port, ok := extractHostPort(u, 443)
	if !ok {
		return parsedCandidate{}, false
	}
	password := extractRawPasswordFromUserinfo(u)
	if password == "" {
		return parsedCandidate{}, false
	}
	if name == "" {
		name = fmt.Sprintf("anytls-%s-%d", server, port)
	}

	q := u.Query()
	obj := map[string]any{
		"type":        "anytls",
		"server":      server,
		"server_port": port,
		"password":    password,
	}
	if v := queryFirst(q, "idle_session_check_interval"); v != "" {
		obj["idle_session_check_interval"] = v
	}
	if v := queryFirst(q, "idle_session_timeout"); v != "" {
		obj["idle_session_timeout"] = v
	}
	if v := queryFirst(q, "min_idle_session"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			obj["min_idle_session"] = n
		}
	}

	obj["tls"] = buildTLSObject(tlsBuildParams{
		Enabled:     true,
		ServerName:  queryFirst(q, "sni", "peer"),
		Insecure:    queryBool(q, "insecure", "allowInsecure", "allow_insecure"),
		ALPN:        splitCommaList(queryFirst(q, "alpn")),
		Fingerprint: queryFirst(q, "fp", "fingerprint"),
	})

	return parsedCandidate{
		OriginalName: name,
		Type:         "anytls",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func parseSocksURI(raw string) (parsedCandidate, bool) {
	u, name, err := parseURLWithFragment(raw)
	if err != nil {
		return parsedCandidate{}, false
	}
	server, port, ok := extractHostPort(u, 0)
	var username, password string
	if ok {
		if u.User != nil {
			username = u.User.Username()
			password, _ = u.User.Password()
		}
	} else {
		// Handle Shadowrocket style socks://<base64(user:pass@host:port)>#name
		withoutFrag, _ := splitURIFragment(raw)
		schemeIdx := strings.Index(withoutFrag, "://")
		if schemeIdx < 0 {
			return parsedCandidate{}, false
		}
		decoded, decOK := decodeFlexibleBase64([]byte(withoutFrag[schemeIdx+3:]))
		if !decOK {
			return parsedCandidate{}, false
		}
		decStr := strings.TrimSpace(string(decoded))
		if atIdx := strings.LastIndexByte(decStr, '@'); atIdx >= 0 {
			cred := decStr[:atIdx]
			hp := decStr[atIdx+1:]
			server, port, ok = splitHostPortInt(hp)
			if !ok {
				return parsedCandidate{}, false
			}
			parts := strings.SplitN(cred, ":", 2)
			username = parts[0]
			if len(parts) == 2 {
				password = parts[1]
			}
		} else {
			server, port, ok = splitHostPortInt(decStr)
			if !ok {
				return parsedCandidate{}, false
			}
		}
	}

	if name == "" {
		name = fmt.Sprintf("socks-%s-%d", server, port)
	}

	obj := map[string]any{
		"type":        "socks",
		"server":      server,
		"server_port": port,
		"version":     "5",
	}
	if username != "" {
		obj["username"] = username
	}
	if password != "" {
		obj["password"] = password
	}

	return parsedCandidate{
		OriginalName: name,
		Type:         "socks",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func parseHTTPURI(raw string, isHTTPS bool) (parsedCandidate, bool) {
	// Reject lines with whitespace before fragment (not valid proxy URIs).
	clean, _ := splitURIFragment(raw)
	if strings.ContainsAny(clean, " \t") {
		return parsedCandidate{}, false
	}

	u, name, err := parseURLWithFragment(raw)
	if err != nil {
		return parsedCandidate{}, false
	}
	// Bare webpage URLs with deep paths are not HTTP proxy URIs.
	if u.Path != "" && u.Path != "/" {
		return parsedCandidate{}, false
	}
	defaultPort := 80
	if isHTTPS {
		defaultPort = 443
	}
	// Require explicit port or credentials for http/https proxy lines so plain URLs aren't misread.
	if u.Port() == "" && u.User == nil {
		return parsedCandidate{}, false
	}
	server, port, ok := extractHostPort(u, defaultPort)
	if !ok {
		return parsedCandidate{}, false
	}
	if name == "" {
		if isHTTPS {
			name = fmt.Sprintf("https-%s-%d", server, port)
		} else {
			name = fmt.Sprintf("http-%s-%d", server, port)
		}
	}

	obj := map[string]any{
		"type":        "http",
		"server":      server,
		"server_port": port,
	}
	if u.User != nil {
		if user := u.User.Username(); user != "" {
			obj["username"] = user
		}
		if pw, hasPw := u.User.Password(); hasPw && pw != "" {
			obj["password"] = pw
		}
	}
	if isHTTPS {
		q := u.Query()
		obj["tls"] = buildTLSObject(tlsBuildParams{
			Enabled:    true,
			ServerName: queryFirst(q, "sni", "peer"),
			Insecure:   queryBool(q, "insecure", "allowInsecure", "allow_insecure"),
			ALPN:       splitCommaList(queryFirst(q, "alpn")),
		})
	}

	return parsedCandidate{
		OriginalName: name,
		Type:         "http",
		Server:       server,
		ServerPort:   port,
		Obj:          obj,
	}, true
}

func splitHostPortInt(hostPort string) (string, int, bool) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(hostPort))
	if err != nil {
		return "", 0, false
	}
	host = strings.TrimSpace(host)
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || host == "" || port < 1 || port > 65535 {
		return "", 0, false
	}
	return host, port, true
}

func decodeFlexibleBase64(data []byte) ([]byte, bool) {
	// Strip all ASCII whitespace.
	compact := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			compact = append(compact, b)
		}
	}
	if len(compact) == 0 {
		return nil, false
	}

	s := string(compact)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		if out, err := enc.DecodeString(s); err == nil && len(out) > 0 {
			return out, true
		}
	}
	// Retry after stripping trailing '=' in case padding length was malformed.
	unpadded := strings.TrimRight(s, "=")
	if unpadded != s {
		for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
			if out, err := enc.DecodeString(unpadded); err == nil && len(out) > 0 {
				return out, true
			}
		}
	}
	return nil, false
}
