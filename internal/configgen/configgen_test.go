package configgen

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuild_Defaults(t *testing.T) {
	raw, err := Build(BuildInput{
		CacheFilePath: "/data/cache.db",
		ClashAPI: ClashAPIConfig{
			ListenAddr: "127.0.0.1:19090",
			Secret:     "supersecret",
		},
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	// log section
	logSec := doc["log"].(map[string]any)
	if logSec["level"] != "info" {
		t.Errorf("expected default log level info, got %v", logSec["level"])
	}
	if logSec["timestamp"] != true {
		t.Errorf("expected log.timestamp=true, got %v", logSec["timestamp"])
	}

	// dns section (modern sing-box 1.12+ typed server)
	dnsSec := doc["dns"].(map[string]any)
	servers := dnsSec["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("expected 1 default dns server, got %d", len(servers))
	}
	srv0 := servers[0].(map[string]any)
	if srv0["type"] != "local" || srv0["tag"] != "dns-local" {
		t.Errorf("unexpected dns server: %+v", srv0)
	}

	// inbounds empty array (not null)
	inbounds, ok := doc["inbounds"].([]any)
	if !ok || len(inbounds) != 0 {
		t.Errorf("expected empty inbounds slice, got %T %v", doc["inbounds"], doc["inbounds"])
	}

	// outbounds contains direct
	outbounds := doc["outbounds"].([]any)
	if len(outbounds) != 1 {
		t.Fatalf("expected 1 outbound, got %d", len(outbounds))
	}
	ob0 := outbounds[0].(map[string]any)
	if ob0["type"] != "direct" || ob0["tag"] != "direct" {
		t.Errorf("expected direct outbound, got %+v", ob0)
	}

	// endpoints omitted when empty
	if _, exists := doc["endpoints"]; exists {
		t.Errorf("expected endpoints key to be omitted when empty")
	}

	// route section
	routeSec := doc["route"].(map[string]any)
	if routeSec["final"] != "direct" {
		t.Errorf("expected route.final=direct, got %v", routeSec["final"])
	}
	rules := routeSec["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("expected 1 route rule, got %d", len(rules))
	}
	r0 := rules[0].(map[string]any)
	if r0["action"] != "sniff" {
		t.Errorf("expected modern sniff rule, got %+v", r0)
	}

	// experimental section
	expSec := doc["experimental"].(map[string]any)
	cacheFile := expSec["cache_file"].(map[string]any)
	if cacheFile["enabled"] != true || cacheFile["path"] != "/data/cache.db" {
		t.Errorf("unexpected cache_file: %+v", cacheFile)
	}
	clashAPI := expSec["clash_api"].(map[string]any)
	if clashAPI["external_controller"] != "127.0.0.1:19090" || clashAPI["secret"] != "supersecret" {
		t.Errorf("unexpected clash_api: %+v", clashAPI)
	}
}

func TestBuild_WithInboundsEndpointsAndOverride(t *testing.T) {
	raw, err := Build(BuildInput{
		LogLevel: "debug",
		ClashAPI: ClashAPIConfig{
			ListenAddr: "127.0.0.1:19191",
		},
		RawInbounds: []json.RawMessage{
			json.RawMessage(`{"type":"vless","tag":"vless-in","listen":"::","listen_port":443}`),
		},
		RawOutbounds: []json.RawMessage{
			json.RawMessage(`{"type":"direct","tag":"direct"}`),
			json.RawMessage(`{"type":"socks","tag":"proxy-out","server":"127.0.0.1","server_port":1080}`),
		},
		RawEndpoints: []json.RawMessage{
			json.RawMessage(`{"type":"wireguard","tag":"warp-ep","address":["172.16.0.2/32"]}`),
		},
		RawRouteRules: []json.RawMessage{
			json.RawMessage(`{"protocol":"bittorrent","action":"reject"}`),
		},
		OverridePatch: `{"log":{"level":"warn","disabled":false},"route":{"auto_detect_interface":true}}`,
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// Override patch took effect
	logSec := doc["log"].(map[string]any)
	if logSec["level"] != "warn" {
		t.Errorf("expected overridden log level warn, got %v", logSec["level"])
	}
	if logSec["timestamp"] != true {
		t.Errorf("expected log.timestamp preserved after merge, got %v", logSec["timestamp"])
	}

	// Outbounds did not duplicate direct
	outbounds := doc["outbounds"].([]any)
	if len(outbounds) != 2 {
		t.Errorf("expected 2 outbounds without duplicate direct, got %d", len(outbounds))
	}

	// Endpoints present
	endpoints, ok := doc["endpoints"].([]any)
	if !ok || len(endpoints) != 1 {
		t.Errorf("expected 1 endpoint, got %v", doc["endpoints"])
	}

	// Route rules include sniff + custom rule and override field
	routeSec := doc["route"].(map[string]any)
	if routeSec["auto_detect_interface"] != true {
		t.Errorf("expected auto_detect_interface=true from override")
	}
	rules := routeSec["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("expected 2 route rules, got %d", len(rules))
	}
	if rules[0].(map[string]any)["action"] != "sniff" {
		t.Errorf("expected sniff first, got %+v", rules[0])
	}
	if rules[1].(map[string]any)["action"] != "reject" {
		t.Errorf("expected custom reject rule second, got %+v", rules[1])
	}
}

func TestBuild_InvalidInputs(t *testing.T) {
	if _, err := Build(BuildInput{LogLevel: "bogus"}); err == nil {
		t.Errorf("expected error for invalid log level")
	}
	if _, err := Build(BuildInput{
		RawInbounds: []json.RawMessage{json.RawMessage(`{invalid}`)},
	}); err == nil {
		t.Errorf("expected error for malformed inbound JSON")
	}
	if _, err := Build(BuildInput{
		OverridePatch: `{"log":`,
	}); err == nil {
		t.Errorf("expected error for malformed override patch")
	}
}

func TestMergeJSONPatch_RFC7396(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		patch string
		want  string
	}{
		{
			name:  "replace scalar and keep siblings",
			base:  `{"a":"b","c":"d"}`,
			patch: `{"a":"z"}`,
			want:  `{"a":"z","c":"d"}`,
		},
		{
			name:  "delete key with null",
			base:  `{"a":"b","c":"d"}`,
			patch: `{"c":null}`,
			want:  `{"a":"b"}`,
		},
		{
			name:  "nested object merge and deletion",
			base:  `{"a":{"b":"c","d":"e"},"f":1}`,
			patch: `{"a":{"b":"z","d":null,"g":2}}`,
			want:  `{"a":{"b":"z","g":2},"f":1}`,
		},
		{
			name:  "array replaced whole not concatenated",
			base:  `{"a":[1,2,3]}`,
			patch: `{"a":[4,5]}`,
			want:  `{"a":[4,5]}`,
		},
		{
			name:  "replace scalar with object",
			base:  `{"a":"scalar"}`,
			patch: `{"a":{"nested":true}}`,
			want:  `{"a":{"nested":true}}`,
		},
		{
			name:  "replace object with scalar",
			base:  `{"a":{"nested":true}}`,
			patch: `{"a":"scalar"}`,
			want:  `{"a":"scalar"}`,
		},
		{
			name:  "preserve large integer precision",
			base:  `{"id":9007199254740993}`,
			patch: `{"extra":1}`,
			want:  `{"extra":1,"id":9007199254740993}`,
		},
		{
			name:  "empty patch returns base unchanged",
			base:  `{"x":1}`,
			patch: `   `,
			want:  `{"x":1}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeJSONPatch([]byte(tc.base), []byte(tc.patch))
			if err != nil {
				t.Fatalf("MergeJSONPatch error: %v", err)
			}
			// Compare compact canonical representation to ignore formatting whitespace
			gotCompact := compactJSON(t, got)
			wantCompact := compactJSON(t, []byte(tc.want))
			if gotCompact != wantCompact {
				t.Errorf("got %s, want %s", gotCompact, wantCompact)
			}
		})
	}
}

func TestMergeJSONPatch_Errors(t *testing.T) {
	if _, err := MergeJSONPatch([]byte(`not json`), []byte(`{}`)); err == nil {
		t.Errorf("expected error for invalid base JSON")
	}
	if _, err := MergeJSONPatch([]byte(`{}`), []byte(`{"a":1}{"b":2}`)); err == nil {
		t.Errorf("expected error for trailing JSON in patch")
	}
}

func compactJSON(t *testing.T, data []byte) string {
	t.Helper()
	var v any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("compactJSON decode error: %v (data=%s)", err, string(data))
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("compactJSON marshal error: %v", err)
	}
	return string(out)
}
