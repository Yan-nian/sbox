package configgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ClashAPIConfig configures the local loopback Clash API endpoint exposed by sing-box.
type ClashAPIConfig struct {
	ListenAddr string
	Secret     string
}

// BuildInput holds all dynamic inputs required to compile a modern sing-box 1.12+ config.
type BuildInput struct {
	LogLevel      string
	CacheFilePath string
	ClashAPI      ClashAPIConfig
	Inbounds      []InboundSpec
	RawInbounds   []json.RawMessage
	RawOutbounds  []json.RawMessage
	RawEndpoints  []json.RawMessage
	RawRouteRules []json.RawMessage
	OverridePatch string
}

var validLogLevels = map[string]bool{
	"trace": true,
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
	"fatal": true,
	"panic": true,
}

// Build compiles a clean modern sing-box 1.12+ configuration JSON document.
// If in.OverridePatch is non-empty, it is applied using RFC 7396 JSON Merge Patch.
func Build(in BuildInput) ([]byte, error) {
	logLevel := strings.ToLower(strings.TrimSpace(in.LogLevel))
	if logLevel == "" {
		logLevel = "info"
	}
	if !validLogLevels[logLevel] {
		return nil, fmt.Errorf("configgen: unsupported log level %q", in.LogLevel)
	}

	compiledRawInbounds := make([]json.RawMessage, 0, len(in.Inbounds)+len(in.RawInbounds))
	for _, spec := range in.Inbounds {
		compiled, err := CompileInbound(spec)
		if err != nil {
			return nil, err
		}
		compiledRawInbounds = append(compiledRawInbounds, compiled...)
	}
	compiledRawInbounds = append(compiledRawInbounds, in.RawInbounds...)

	inbounds, err := decodeRawList("inbounds", compiledRawInbounds)
	if err != nil {
		return nil, err
	}
	if err := validateUniqueInboundTags(inbounds); err != nil {
		return nil, err
	}

	rawOutbounds, err := decodeRawList("outbounds", in.RawOutbounds)
	if err != nil {
		return nil, err
	}

	hasDirect := false
	for _, ob := range rawOutbounds {
		if m, ok := ob.(map[string]any); ok {
			if tag, ok := m["tag"].(string); ok && tag == "direct" {
				hasDirect = true
				break
			}
		}
	}
	outbounds := make([]any, 0, len(rawOutbounds)+1)
	if !hasDirect {
		outbounds = append(outbounds, map[string]any{
			"type": "direct",
			"tag":  "direct",
		})
	}
	outbounds = append(outbounds, rawOutbounds...)

	endpoints, err := decodeRawList("endpoints", in.RawEndpoints)
	if err != nil {
		return nil, err
	}

	rawRouteRules, err := decodeRawList("route.rules", in.RawRouteRules)
	if err != nil {
		return nil, err
	}
	routeRules := make([]any, 0, len(rawRouteRules)+1)
	if !hasSniffRule(rawRouteRules) {
		routeRules = append(routeRules, map[string]any{
			"action": "sniff",
		})
	}
	routeRules = append(routeRules, rawRouteRules...)

	cacheFile := map[string]any{
		"enabled": true,
	}
	if p := strings.TrimSpace(in.CacheFilePath); p != "" {
		cacheFile["path"] = p
	}

	listenAddr := strings.TrimSpace(in.ClashAPI.ListenAddr)
	if listenAddr == "" {
		listenAddr = "127.0.0.1:9090"
	}
	clashAPI := map[string]any{
		"external_controller": listenAddr,
	}
	if secret := strings.TrimSpace(in.ClashAPI.Secret); secret != "" {
		clashAPI["secret"] = secret
	}

	doc := map[string]any{
		"log": map[string]any{
			"level":     logLevel,
			"timestamp": true,
		},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{
					"type": "local",
					"tag":  "dns-local",
				},
			},
			"final": "dns-local",
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route": map[string]any{
			"rules":                   routeRules,
			"final":                   "direct",
			"default_domain_resolver": "dns-local",
		},
		"experimental": map[string]any{
			"cache_file": cacheFile,
			"clash_api":  clashAPI,
		},
	}

	if len(endpoints) > 0 {
		doc["endpoints"] = endpoints
	}

	baseBytes, err := marshalPretty(doc)
	if err != nil {
		return nil, fmt.Errorf("configgen: marshal base config: %w", err)
	}

	if strings.TrimSpace(in.OverridePatch) != "" {
		merged, err := MergeJSONPatch(baseBytes, []byte(in.OverridePatch))
		if err != nil {
			return nil, fmt.Errorf("configgen: apply override patch: %w", err)
		}
		return merged, nil
	}

	return baseBytes, nil
}

// MergeJSONPatch applies an RFC 7396 JSON Merge Patch to base and returns formatted JSON.
// An empty or whitespace-only patch returns formatted base JSON after validating base.
func MergeJSONPatch(base, patch []byte) ([]byte, error) {
	baseVal, err := decodeSingleJSON(base)
	if err != nil {
		return nil, fmt.Errorf("invalid base JSON: %w", err)
	}

	if len(bytes.TrimSpace(patch)) == 0 {
		return marshalPretty(baseVal)
	}

	patchVal, err := decodeSingleJSON(patch)
	if err != nil {
		return nil, fmt.Errorf("invalid patch JSON: %w", err)
	}

	merged := applyMergePatch(baseVal, patchVal)
	out, err := marshalPretty(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged JSON: %w", err)
	}
	return out, nil
}

// applyMergePatch implements RFC 7396 Section 2 MergePatch semantics recursively.
func applyMergePatch(target, patch any) any {
	patchObj, ok := patch.(map[string]any)
	if !ok {
		return patch
	}

	targetObj, ok := target.(map[string]any)
	if !ok {
		targetObj = make(map[string]any, len(patchObj))
	} else {
		// Copy to avoid mutating caller-owned maps.
		copied := make(map[string]any, len(targetObj))
		for k, v := range targetObj {
			copied[k] = v
		}
		targetObj = copied
	}

	for name, val := range patchObj {
		if val == nil {
			delete(targetObj, name)
		} else {
			targetObj[name] = applyMergePatch(targetObj[name], val)
		}
	}
	return targetObj
}

func decodeRawList(section string, items []json.RawMessage) ([]any, error) {
	out := make([]any, 0, len(items))
	for i, raw := range items {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			continue
		}
		val, err := decodeSingleJSON(trimmed)
		if err != nil {
			return nil, fmt.Errorf("configgen: invalid JSON in %s[%d]: %w", section, i, err)
		}
		out = append(out, val)
	}
	return out, nil
}

func validateUniqueInboundTags(inbounds []any) error {
	seen := make(map[string]struct{}, len(inbounds))
	for _, item := range inbounds {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tag, ok := m["tag"].(string)
		if !ok || strings.TrimSpace(tag) == "" {
			continue
		}
		if _, dup := seen[tag]; dup {
			return fmt.Errorf("configgen: duplicate inbound tag %q", tag)
		}
		seen[tag] = struct{}{}
	}
	return nil
}

func hasSniffRule(rules []any) bool {
	for _, r := range rules {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if action, ok := m["action"].(string); ok && action == "sniff" {
			return true
		}
	}
	return false
}

func decodeSingleJSON(data []byte) (any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("empty JSON input")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	// Ensure no trailing garbage after the first JSON value.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("unexpected trailing JSON data")
		}
		return nil, fmt.Errorf("trailing data: %w", err)
	}
	return v, nil
}

func marshalPretty(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
