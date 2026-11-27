package syncer

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRegisterWARPWithEndpoint_Success(t *testing.T) {
	var receivedReq warpRegRequest
	var receivedClientVer, receivedContentType string

	clientIDBytes := []byte{0x12, 0x34, 0x56}
	clientIDB64 := base64.StdEncoding.EncodeToString(clientIDBytes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		receivedClientVer = r.Header.Get("CF-Client-Version")
		receivedContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&receivedReq); err != nil {
			t.Errorf("decode request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"id":    "acc-12345-uuid",
			"token": "tok-abcdef-secret",
			"config": map[string]any{
				"client_id": clientIDB64,
				"peers": []map[string]any{
					{
						"public_key": "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
						"endpoint": map[string]any{
							"v4":   "162.159.192.1:0",
							"v6":   "[2606:4700:d0::a29f:c001]:0",
							"host": "engage.cloudflareclient.com:2408",
						},
					},
				},
				"interface": map[string]any{
					"addresses": map[string]any{
						"v4": "172.16.0.2",
						"v6": "2606:4700:110:8a36:1111:2222:3333:4444",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	acc, err := RegisterWARPWithEndpoint(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("RegisterWARPWithEndpoint failed: %v", err)
	}

	// Verify request headers and payload contract.
	if receivedClientVer != "a-6.11-2223" {
		t.Fatalf("unexpected CF-Client-Version header: %q", receivedClientVer)
	}
	if receivedContentType != "application/json; charset=UTF-8" {
		t.Fatalf("unexpected Content-Type header: %q", receivedContentType)
	}
	if receivedReq.Model != "PC" || receivedReq.Type != "Android" || receivedReq.Locale != "en_US" {
		t.Fatalf("unexpected registration metadata: %+v", receivedReq)
	}
	if receivedReq.InstallID == "" || receivedReq.FCMToken == "" {
		t.Fatalf("expected non-empty install_id and fcm_token")
	}
	if _, err := time.Parse(time.RFC3339, receivedReq.TOS); err != nil {
		t.Fatalf("expected valid RFC3339 tos, got %q: %v", receivedReq.TOS, err)
	}

	// Verify real X25519 keypair cryptographic consistency.
	privRaw, err := base64.StdEncoding.DecodeString(acc.PrivateKey)
	if err != nil || len(privRaw) != 32 {
		t.Fatalf("invalid base64 32B private_key %q: err=%v len=%d", acc.PrivateKey, err, len(privRaw))
	}
	pubRaw, err := base64.StdEncoding.DecodeString(acc.PublicKey)
	if err != nil || len(pubRaw) != 32 {
		t.Fatalf("invalid base64 32B public_key %q: err=%v len=%d", acc.PublicKey, err, len(pubRaw))
	}
	parsedPriv, err := ecdh.X25519().NewPrivateKey(privRaw)
	if err != nil {
		t.Fatalf("NewPrivateKey failed: %v", err)
	}
	if !bytes.Equal(parsedPriv.PublicKey().Bytes(), pubRaw) {
		t.Fatal("derived X25519 public key does not match account.PublicKey")
	}
	if receivedReq.Key != acc.PublicKey {
		t.Fatalf("public key sent to Cloudflare %q != returned %q", receivedReq.Key, acc.PublicKey)
	}

	// Verify extracted account fields, CIDR suffixes, endpoint, MTU, and 3-byte reserved.
	if acc.AccountID != "acc-12345-uuid" || acc.AccessToken != "tok-abcdef-secret" {
		t.Fatalf("unexpected account credentials: %+v", acc)
	}
	if acc.PeerPubKey != "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=" {
		t.Fatalf("unexpected peer public key: %q", acc.PeerPubKey)
	}
	if acc.EndpointHost != "engage.cloudflareclient.com" || acc.EndpointPort != 2408 {
		t.Fatalf("unexpected endpoint: %s:%d", acc.EndpointHost, acc.EndpointPort)
	}
	if acc.LocalIPv4 != "172.16.0.2/32" {
		t.Fatalf("expected LocalIPv4 172.16.0.2/32, got %q", acc.LocalIPv4)
	}
	if acc.LocalIPv6 != "2606:4700:110:8a36:1111:2222:3333:4444/128" {
		t.Fatalf("expected LocalIPv6 /128 CIDR, got %q", acc.LocalIPv6)
	}
	if acc.MTU != 1280 {
		t.Fatalf("expected MTU 1280, got %d", acc.MTU)
	}
	if len(acc.Reserved) != 3 || acc.Reserved[0] != 0x12 || acc.Reserved[1] != 0x34 || acc.Reserved[2] != 0x56 {
		t.Fatalf("unexpected reserved bytes: %v", acc.Reserved)
	}
}

func TestRegisterWARPWithEndpoint_FallbacksAndExistingCIDR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":    "acc-fallback",
			"token": "tok-fallback",
			"config": map[string]any{
				"client_id": base64.RawStdEncoding.EncodeToString([]byte{255, 0, 42}),
				"peers": []map[string]any{
					{
						"public_key": "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
						"endpoint": map[string]any{
							"host": "",
							"v4":   "",
						},
					},
				},
				"interface": map[string]any{
					"addresses": map[string]any{
						"v4": "172.16.0.9/32",
						"v6": "2606:4700:110::9/128",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	acc, err := RegisterWARPWithEndpoint(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("RegisterWARPWithEndpoint failed: %v", err)
	}
	if acc.EndpointHost != "engage.cloudflareclient.com" || acc.EndpointPort != 2408 {
		t.Fatalf("expected default endpoint fallback, got %s:%d", acc.EndpointHost, acc.EndpointPort)
	}
	if acc.LocalIPv4 != "172.16.0.9/32" || acc.LocalIPv6 != "2606:4700:110::9/128" {
		t.Fatalf("existing CIDR suffixes should not be duplicated: v4=%q v6=%q", acc.LocalIPv4, acc.LocalIPv6)
	}
	if len(acc.Reserved) != 3 || acc.Reserved[0] != 255 || acc.Reserved[1] != 0 || acc.Reserved[2] != 42 {
		t.Fatalf("unexpected reserved bytes: %v", acc.Reserved)
	}
}

func TestRegisterWARPWithEndpoint_Errors(t *testing.T) {
	t.Run("http_error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errors":[{"message":"rate limited"}]}`))
		}))
		defer srv.Close()
		if _, err := RegisterWARPWithEndpoint(context.Background(), srv.URL, ""); err == nil {
			t.Fatal("expected error for HTTP 429")
		}
	})

	t.Run("missing_peers", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"id":"1","token":"t","config":{"peers":[],"interface":{"addresses":{"v4":"172.16.0.2"}}}}`))
		}))
		defer srv.Close()
		if _, err := RegisterWARPWithEndpoint(context.Background(), srv.URL, ""); err == nil {
			t.Fatal("expected error when peers array is empty")
		}
	})

	t.Run("invalid_client_id", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"id":"1","token":"t","config":{"client_id":"AA","peers":[{"public_key":"abc"}],"interface":{"addresses":{"v4":"172.16.0.2"}}}}`))
		}))
		defer srv.Close()
		if _, err := RegisterWARPWithEndpoint(context.Background(), srv.URL, ""); err == nil {
			t.Fatal("expected error when client_id decodes to fewer than 3 bytes")
		}
	})
}
