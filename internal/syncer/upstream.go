package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	defaultSubUserAgent = "sing-box/1.12.0"
	maxSubBodyBytes     = 10 << 20 // 10 MiB
)

// ParsedOutbound represents a single upstream proxy node normalized into a valid
// modern sing-box 1.12+ outbound JSON object.
type ParsedOutbound struct {
	Tag          string          `json:"tag"`
	OriginalName string          `json:"original_name"`
	Type         string          `json:"type"`
	Server       string          `json:"server"`
	ServerPort   int             `json:"server_port"`
	Raw          json.RawMessage `json:"raw"` // valid sing-box 1.12+ outbound JSON object with "tag" set to Tag
}

// FetchOptions configures upstream subscription fetching, regex filtering, and tag prefixing.
type FetchOptions struct {
	URL          string
	ProxyURL     string
	UserAgent    string // default "sing-box/1.12.0"
	IncludeRegex string
	ExcludeRegex string
	TagPrefix    string // e.g. "sub-1-"
}

// FetchResult holds the detected subscription format and finalized outbound nodes.
type FetchResult struct {
	Format string           `json:"format"` // "singbox" | "clash" | "uri"
	Nodes  []ParsedOutbound `json:"nodes"`
}

// parsedCandidate is an internal pre-filter outbound produced by format-specific parsers.
type parsedCandidate struct {
	OriginalName string
	Type         string
	Server       string
	ServerPort   int
	Obj          map[string]any
}

var singboxProxyTypes = map[string]bool{
	"vless":       true,
	"vmess":       true,
	"trojan":      true,
	"shadowsocks": true,
	"hysteria2":   true,
	"tuic":        true,
	"anytls":      true,
	"socks":       true,
	"http":        true,
	"ssh":         true,
}

var singboxMetaTypes = map[string]bool{
	"direct":   true,
	"block":    true,
	"dns":      true,
	"selector": true,
	"urltest":  true,
}

// FetchSubscription downloads an upstream subscription over HTTP(S) (optionally via an
// HTTP/HTTPS/SOCKS5 proxy) and parses it into modern sing-box 1.12+ outbound objects.
func FetchSubscription(ctx context.Context, opts FetchOptions) (*FetchResult, error) {
	rawURL := strings.TrimSpace(opts.URL)
	if rawURL == "" {
		return nil, errors.New("syncer: subscription URL is required")
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("syncer: invalid subscription URL: %w", err)
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("syncer: unsupported subscription URL scheme %q", parsedURL.Scheme)
	}
	if parsedURL.Host == "" {
		return nil, errors.New("syncer: subscription URL missing host")
	}

	client, err := newHTTPClient(opts.ProxyURL, defaultHTTPTimeout)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("syncer: build subscription request: %w", err)
	}

	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = defaultSubUserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("syncer: fetch subscription: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("syncer: read subscription body: %w", err)
	}
	if len(body) > maxSubBodyBytes {
		return nil, fmt.Errorf("syncer: subscription body exceeds %d MiB limit", maxSubBodyBytes>>20)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		if snippet != "" {
			return nil, fmt.Errorf("syncer: subscription HTTP %d: %s", resp.StatusCode, snippet)
		}
		return nil, fmt.Errorf("syncer: subscription HTTP %d", resp.StatusCode)
	}

	return ParseSubscription(body, opts)
}

// ParseSubscription auto-detects sing-box JSON, Clash (Mihomo) YAML, or plain/Base64 URI
// subscription payloads, converts nodes to modern sing-box 1.12+ outbound JSON, applies
// include/exclude regex filters, and produces sanitized unique tags.
func ParseSubscription(data []byte, opts FetchOptions) (*FetchResult, error) {
	var includeRe, excludeRe *regexp.Regexp
	if pat := strings.TrimSpace(opts.IncludeRegex); pat != "" {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("syncer: invalid include_regex %q: %w", pat, err)
		}
		includeRe = re
	}
	if pat := strings.TrimSpace(opts.ExcludeRegex); pat != "" {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("syncer: invalid exclude_regex %q: %w", pat, err)
		}
		excludeRe = re
	}

	// Strip UTF-8 BOM if present.
	cleaned := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	trimmed := bytes.TrimSpace(cleaned)
	if len(trimmed) == 0 {
		return nil, errors.New("syncer: empty subscription content")
	}

	var (
		format     string
		candidates []parsedCandidate
		matched    bool
		err        error
	)

	// 1. Format 1: sing-box JSON with "outbounds" array.
	if candidates, matched, err = parseSingBoxSubscription(trimmed); err != nil {
		return nil, err
	} else if matched {
		format = "singbox"
	}

	// 2. Format 2: Clash (Mihomo) YAML with "proxies" list.
	if !matched {
		if candidates, matched, err = parseClashSubscription(trimmed); err != nil {
			return nil, err
		} else if matched {
			format = "clash"
		}
	}

	// 3. Format 3: Plain or Base64 URI list.
	if !matched {
		if candidates, matched, err = parseURISubscription(trimmed); err != nil {
			return nil, err
		} else if matched {
			format = "uri"
		}
	}

	if !matched {
		return nil, errors.New("syncer: unrecognized subscription format or no valid proxy nodes found")
	}

	nodes, err := finalizeCandidates(candidates, includeRe, excludeRe, opts.TagPrefix)
	if err != nil {
		return nil, err
	}

	return &FetchResult{
		Format: format,
		Nodes:  nodes,
	}, nil
}

// parseSingBoxSubscription checks if data is a valid JSON object containing an "outbounds" array
// and extracts real proxy outbounds while skipping meta outbounds.
func parseSingBoxSubscription(data []byte) ([]parsedCandidate, bool, error) {
	if len(data) == 0 || data[0] != '{' {
		return nil, false, nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, false, nil
	}
	rawList, ok := doc["outbounds"]
	if !ok {
		return nil, false, nil
	}
	arr, ok := rawList.([]any)
	if !ok {
		return nil, false, nil
	}

	candidates := make([]parsedCandidate, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(asString(m["type"])))
		if typ == "" || singboxMetaTypes[typ] || !singboxProxyTypes[typ] {
			continue
		}

		// Clone top-level map so we never mutate decoded structure unexpectedly.
		obj := make(map[string]any, len(m))
		for k, v := range m {
			obj[k] = v
		}
		obj["type"] = typ

		server := strings.TrimSpace(asString(obj["server"]))
		port, _ := asInt(obj["server_port"])
		origName := strings.TrimSpace(asString(obj["tag"]))
		if origName == "" {
			if server != "" && port > 0 {
				origName = fmt.Sprintf("%s-%s-%d", typ, server, port)
			} else if server != "" {
				origName = fmt.Sprintf("%s-%s", typ, server)
			} else {
				origName = typ
			}
		}

		candidates = append(candidates, parsedCandidate{
			OriginalName: origName,
			Type:         typ,
			Server:       server,
			ServerPort:   port,
			Obj:          obj,
		})
	}

	return candidates, true, nil
}

func finalizeCandidates(candidates []parsedCandidate, includeRe, excludeRe *regexp.Regexp, tagPrefix string) ([]ParsedOutbound, error) {
	prefix := sanitizeTagPrefix(tagPrefix)
	used := make(map[string]struct{}, len(candidates))
	out := make([]ParsedOutbound, 0, len(candidates))

	for i, c := range candidates {
		name := strings.TrimSpace(c.OriginalName)
		if includeRe != nil && !includeRe.MatchString(name) {
			continue
		}
		if excludeRe != nil && excludeRe.MatchString(name) {
			continue
		}

		base := sanitizeTagBody(name)
		if base == "" {
			if c.Server != "" {
				base = sanitizeTagBody(fmt.Sprintf("%s-%s", c.Type, c.Server))
			}
		}
		if base == "" {
			base = fmt.Sprintf("%s-%d", c.Type, i+1)
		}

		var candidateTag string
		if prefix != "" {
			if strings.HasSuffix(prefix, "-") && strings.HasPrefix(base, "-") {
				candidateTag = prefix + strings.TrimLeft(base, "-")
			} else {
				candidateTag = prefix + base
			}
		} else {
			candidateTag = base
		}

		tag := candidateTag
		if _, exists := used[tag]; exists {
			for n := 2; ; n++ {
				alt := fmt.Sprintf("%s-%d", candidateTag, n)
				if _, dup := used[alt]; !dup {
					tag = alt
					break
				}
			}
		}
		used[tag] = struct{}{}

		c.Obj["tag"] = tag
		rawBytes, err := marshalCompactNoEscape(c.Obj)
		if err != nil {
			return nil, fmt.Errorf("syncer: marshal outbound %q: %w", tag, err)
		}

		out = append(out, ParsedOutbound{
			Tag:          tag,
			OriginalName: name,
			Type:         c.Type,
			Server:       c.Server,
			ServerPort:   c.ServerPort,
			Raw:          json.RawMessage(rawBytes),
		})
	}

	return out, nil
}

func sanitizeTagPrefix(prefix string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(prefix) {
		if unicode.IsControl(r) || isUnsafeTagRune(r) || unicode.IsSpace(r) {
			b.WriteByte('-')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sanitizeTagBody converts a node's display name into a clean, URL-path-safe tag
// while preserving readable ASCII and Unicode letters/numbers.
func sanitizeTagBody(name string) string {
	const maxRunes = 64
	var b strings.Builder
	prevDash := false
	written := 0

	for _, r := range strings.TrimSpace(name) {
		if written >= maxRunes {
			break
		}
		if unicode.IsControl(r) || unicode.IsSpace(r) || isUnsafeTagRune(r) {
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
				written++
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			if r == '-' {
				if prevDash || b.Len() == 0 {
					continue
				}
				prevDash = true
			} else {
				prevDash = false
			}
			b.WriteRune(r)
			written++
			continue
		}
		// Other punctuation/symbols/emojis act as soft separators.
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
			written++
		}
	}

	return strings.Trim(b.String(), "-._")
}

func isUnsafeTagRune(r rune) bool {
	switch r {
	case '/', '\\', '?', '#', '%', '&', '"', '\'', '`', ';', ':', '@', '=':
		return true
	default:
		return false
	}
}

func marshalCompactNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// Shared flexible conversion helpers used by sing-box, Clash YAML, and URI parsers.

func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

func asInt(v any) (int, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case int:
		return x, true
	case int64:
		return int(x), true
	case uint64:
		return int(x), true
	case float64:
		return int(x), true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i), true
		}
		if f, err := x.Float64(); err == nil {
			return int(f), true
		}
		return 0, false
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		if i, err := strconv.Atoi(s); err == nil {
			return i, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func asBool(v any) (bool, bool) {
	switch x := v.(type) {
	case nil:
		return false, false
	case bool:
		return x, true
	case int:
		return x != 0, true
	case int64:
		return x != 0, true
	case float64:
		return x != 0, true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i != 0, true
		}
		return false, false
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		switch s {
		case "1", "true", "yes", "on", "tls":
			return true, true
		case "0", "false", "no", "off", "none", "":
			return false, true
		default:
			return false, false
		}
	default:
		return false, false
	}
}

func asStringSlice(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case []string:
		out := make([]string, 0, len(x))
		for _, s := range x {
			if t := strings.TrimSpace(s); t != "" {
				out = append(out, t)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if t := strings.TrimSpace(asString(item)); t != "" {
				out = append(out, t)
			}
		}
		return out
	case string:
		return splitCommaList(x)
	default:
		if t := strings.TrimSpace(asString(x)); t != "" {
			return []string{t}
		}
		return nil
	}
}

func asMap(v any) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return x
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[asString(k)] = val
		}
		return out
	default:
		return nil
	}
}

func asStringMap(v any) map[string]string {
	m := asMap(v)
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		ks := strings.TrimSpace(k)
		vs := strings.TrimSpace(asString(val))
		if ks != "" && vs != "" {
			out[ks] = vs
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func splitCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// normalizePortRanges converts Clash/URI port-hopping specs (e.g. "20000-30000,40000")
// into sing-box 1.12+ server_ports syntax (using ":" for ranges, e.g. ["20000:30000", "40000:40000"]).
func normalizePortRanges(raw string) []string {
	parts := splitCommaList(raw)
	if len(parts) == 0 {
		return nil
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, "-") {
			bounds := strings.SplitN(p, "-", 2)
			a, errA := strconv.Atoi(strings.TrimSpace(bounds[0]))
			b, errB := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if errA == nil && errB == nil && a >= 1 && b <= 65535 && a <= b {
				out = append(out, fmt.Sprintf("%d:%d", a, b))
			}
			continue
		}
		if strings.Contains(p, ":") {
			bounds := strings.SplitN(p, ":", 2)
			a, errA := strconv.Atoi(strings.TrimSpace(bounds[0]))
			b, errB := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if errA == nil && errB == nil && a >= 1 && b <= 65535 && a <= b {
				out = append(out, fmt.Sprintf("%d:%d", a, b))
			}
			continue
		}
		if single, err := strconv.Atoi(p); err == nil && single >= 1 && single <= 65535 {
			out = append(out, fmt.Sprintf("%d:%d", single, single))
		}
	}
	return out
}

// parseBandwidthMbps parses Clash/URI bandwidth values such as 100, "100", "100 Mbps", "1 Gbps".
func parseBandwidthMbps(v any) int {
	if i, ok := asInt(v); ok && i > 0 {
		return i
	}
	s := strings.ToLower(strings.TrimSpace(asString(v)))
	if s == "" {
		return 0
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "gbps"):
		mult = 1000.0
		s = strings.TrimSpace(strings.TrimSuffix(s, "gbps"))
	case strings.HasSuffix(s, "mbps"):
		mult = 1.0
		s = strings.TrimSpace(strings.TrimSuffix(s, "mbps"))
	case strings.HasSuffix(s, "kbps"):
		mult = 0.001
		s = strings.TrimSpace(strings.TrimSuffix(s, "kbps"))
	case strings.HasSuffix(s, "bps"):
		mult = 0.000001
		s = strings.TrimSpace(strings.TrimSuffix(s, "bps"))
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0
	}
	res := int(f * mult)
	if res < 1 && f > 0 {
		return 1
	}
	return res
}

// extractWSEarlyData splits common "?ed=2048" early-data query parameters out of a WS path.
func extractWSEarlyData(wsPath string) (cleanPath string, earlyData int) {
	wsPath = strings.TrimSpace(wsPath)
	if wsPath == "" {
		return "/", 0
	}
	idx := strings.Index(wsPath, "?")
	if idx < 0 {
		return wsPath, 0
	}
	q, err := url.ParseQuery(wsPath[idx+1:])
	if err != nil {
		return wsPath, 0
	}
	edStr := strings.TrimSpace(q.Get("ed"))
	if edStr == "" {
		return wsPath, 0
	}
	ed, err := strconv.Atoi(edStr)
	if err != nil || ed <= 0 {
		return wsPath, 0
	}
	q.Del("ed")
	base := wsPath[:idx]
	if base == "" {
		base = "/"
	}
	if encoded := q.Encode(); encoded != "" {
		return base + "?" + encoded, ed
	}
	return base, ed
}

type tlsBuildParams struct {
	Enabled     bool
	ServerName  string
	Insecure    bool
	DisableSNI  bool
	ALPN        []string
	Fingerprint string
	RealityPub  string
	RealitySID  string
}

func buildTLSObject(p tlsBuildParams) map[string]any {
	hasReality := strings.TrimSpace(p.RealityPub) != ""
	if !p.Enabled && !hasReality {
		return nil
	}

	tlsObj := map[string]any{
		"enabled": true,
	}
	if sn := strings.TrimSpace(p.ServerName); sn != "" {
		tlsObj["server_name"] = sn
	}
	if p.Insecure {
		tlsObj["insecure"] = true
	}
	if p.DisableSNI {
		tlsObj["disable_sni"] = true
	}
	if len(p.ALPN) > 0 {
		tlsObj["alpn"] = p.ALPN
	}

	fp := strings.TrimSpace(p.Fingerprint)
	if hasReality && fp == "" {
		// sing-box requires uTLS to be enabled when Reality is configured.
		fp = "chrome"
	}
	if fp != "" {
		tlsObj["utls"] = map[string]any{
			"enabled":     true,
			"fingerprint": fp,
		}
	}
	if hasReality {
		realityObj := map[string]any{
			"enabled":    true,
			"public_key": strings.TrimSpace(p.RealityPub),
		}
		if sid := strings.TrimSpace(p.RealitySID); sid != "" {
			realityObj["short_id"] = sid
		}
		tlsObj["reality"] = realityObj
	}

	return tlsObj
}
