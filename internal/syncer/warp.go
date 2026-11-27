package syncer

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultWARPRegURL is the official Cloudflare WARP free account registration endpoint.
	DefaultWARPRegURL = "https://api.cloudflareclient.com/v0a2158/reg"

	defaultWARPEndpointHost = "engage.cloudflareclient.com"
	defaultWARPEndpointPort = 2408
	defaultWARPMTU          = 1280
	maxWARPRespBytes        = 1 << 20 // 1 MiB
)

// WARPAccount holds the WireGuard keypair and Cloudflare WARP endpoint metadata returned
// by a free Cloudflare WARP registration.
type WARPAccount struct {
	AccountID    string `json:"account_id"`
	AccessToken  string `json:"access_token"`
	PrivateKey   string `json:"private_key"`     // standard base64 32B WireGuard privkey
	PublicKey    string `json:"public_key"`      // standard base64 32B WireGuard pubkey
	PeerPubKey   string `json:"peer_public_key"` // Cloudflare peer pubkey
	EndpointHost string `json:"endpoint_host"`   // e.g. "engage.cloudflareclient.com"
	EndpointPort int    `json:"endpoint_port"`   // e.g. 2408
	LocalIPv4    string `json:"local_ipv4"`      // CIDR e.g. "172.16.0.2/32"
	LocalIPv6    string `json:"local_ipv6"`      // CIDR e.g. "2606:4700:.../128"
	Reserved     []int  `json:"reserved"`        // 3-byte client_id reserved [r0, r1, r2]
	MTU          int    `json:"mtu"`             // default 1280
}

type warpRegRequest struct {
	Key       string `json:"key"`
	InstallID string `json:"install_id"`
	FCMToken  string `json:"fcm_token"`
	TOS       string `json:"tos"`
	Model     string `json:"model"`
	Type      string `json:"type"`
	Locale    string `json:"locale"`
}

type warpRegResponse struct {
	ID     string `json:"id"`
	Token  string `json:"token"`
	Config struct {
		ClientID string `json:"client_id"`
		Peers    []struct {
			PublicKey string `json:"public_key"`
			Endpoint  struct {
				V4   string `json:"v4"`
				V6   string `json:"v6"`
				Host string `json:"host"`
			} `json:"endpoint"`
		} `json:"peers"`
		Interface struct {
			Addresses struct {
				V4 string `json:"v4"`
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
	} `json:"config"`
}

// RegisterWARP registers a free Cloudflare WARP account against the official Cloudflare
// API endpoint, optionally routing the registration HTTP request through proxyURL.
func RegisterWARP(ctx context.Context, proxyURL string) (*WARPAccount, error) {
	return RegisterWARPWithEndpoint(ctx, DefaultWARPRegURL, proxyURL)
}

// RegisterWARPWithEndpoint registers a Cloudflare WARP account against a custom apiURL
// (useful for testing or mirrored endpoints), optionally via proxyURL.
func RegisterWARPWithEndpoint(ctx context.Context, apiURL, proxyURL string) (*WARPAccount, error) {
	endpoint := strings.TrimSpace(apiURL)
	if endpoint == "" {
		endpoint = DefaultWARPRegURL
	}

	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("syncer: generate X25519 keypair: %w", err)
	}
	privB64 := base64.StdEncoding.EncodeToString(privKey.Bytes())
	pubB64 := base64.StdEncoding.EncodeToString(privKey.PublicKey().Bytes())

	installID, err := randomAlphaNum(22)
	if err != nil {
		return nil, fmt.Errorf("syncer: generate install_id: %w", err)
	}
	fcmSuffix, err := randomAlphaNum(134)
	if err != nil {
		return nil, fmt.Errorf("syncer: generate fcm_token: %w", err)
	}

	payload := warpRegRequest{
		Key:       pubB64,
		InstallID: installID,
		FCMToken:  installID + ":APA91b" + fcmSuffix,
		TOS:       time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Model:     "PC",
		Type:      "Android",
		Locale:    "en_US",
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("syncer: marshal WARP registration payload: %w", err)
	}

	client, err := newHTTPClient(proxyURL, defaultHTTPTimeout)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("syncer: build WARP registration request: %w", err)
	}
	req.Header.Set("CF-Client-Version", "a-6.11-2223")
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("User-Agent", "okhttp/3.12.1")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("syncer: WARP registration request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxWARPRespBytes+1))
	if err != nil {
		return nil, fmt.Errorf("syncer: read WARP registration response: %w", err)
	}
	if len(respBody) > maxWARPRespBytes {
		return nil, errors.New("syncer: WARP registration response exceeds size limit")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(respBody))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		if snippet != "" {
			return nil, fmt.Errorf("syncer: WARP registration HTTP %d: %s", resp.StatusCode, snippet)
		}
		return nil, fmt.Errorf("syncer: WARP registration HTTP %d", resp.StatusCode)
	}

	var reg warpRegResponse
	if err := json.Unmarshal(respBody, &reg); err != nil {
		return nil, fmt.Errorf("syncer: decode WARP registration JSON: %w", err)
	}

	if len(reg.Config.Peers) == 0 {
		return nil, errors.New("syncer: WARP response missing peer configuration")
	}
	peer := reg.Config.Peers[0]
	peerPubKey := strings.TrimSpace(peer.PublicKey)
	if peerPubKey == "" {
		return nil, errors.New("syncer: WARP response missing peer public key")
	}

	v4 := ensureCIDR(reg.Config.Interface.Addresses.V4, "32")
	v6 := ensureCIDR(reg.Config.Interface.Addresses.V6, "128")
	if v4 == "" && v6 == "" {
		return nil, errors.New("syncer: WARP response missing interface IP addresses")
	}

	epHost, epPort := parseWARPEndpoint(peer.Endpoint.Host, peer.Endpoint.V4)

	reserved, err := decodeWARPReserved(reg.Config.ClientID)
	if err != nil {
		return nil, err
	}

	return &WARPAccount{
		AccountID:    strings.TrimSpace(reg.ID),
		AccessToken:  strings.TrimSpace(reg.Token),
		PrivateKey:   privB64,
		PublicKey:    pubB64,
		PeerPubKey:   peerPubKey,
		EndpointHost: epHost,
		EndpointPort: epPort,
		LocalIPv4:    v4,
		LocalIPv6:    v6,
		Reserved:     reserved,
		MTU:          defaultWARPMTU,
	}, nil
}

func ensureCIDR(addr, defaultPrefix string) string {
	s := strings.TrimSpace(addr)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "/") {
		return s
	}
	return s + "/" + defaultPrefix
}

func parseWARPEndpoint(hostField, fallbackV4 string) (string, int) {
	candidates := []string{strings.TrimSpace(hostField), strings.TrimSpace(fallbackV4)}
	for _, raw := range candidates {
		if raw == "" {
			continue
		}
		if h, pStr, err := net.SplitHostPort(raw); err == nil {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			port, err := strconv.Atoi(strings.TrimSpace(pStr))
			if err != nil || port < 1 || port > 65535 {
				port = defaultWARPEndpointPort
			}
			return h, port
		}
		// Host without port.
		if !strings.Contains(raw, ":") {
			return raw, defaultWARPEndpointPort
		}
	}
	return defaultWARPEndpointHost, defaultWARPEndpointPort
}

func decodeWARPReserved(clientID string) ([]int, error) {
	cid := strings.TrimSpace(clientID)
	if cid == "" {
		return nil, nil
	}
	decoded, ok := decodeFlexibleBase64([]byte(cid))
	if !ok {
		return nil, fmt.Errorf("syncer: invalid WARP client_id base64 %q", cid)
	}
	if len(decoded) < 3 {
		return nil, fmt.Errorf("syncer: WARP client_id decoded length %d < 3", len(decoded))
	}
	return []int{int(decoded[0]), int(decoded[1]), int(decoded[2])}, nil
}

func randomAlphaNum(n int) (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf), nil
}
