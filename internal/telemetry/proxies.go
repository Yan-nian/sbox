package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultProxyDelayTestURL   = "https://www.gstatic.com/generate_204"
	defaultProxyDelayTimeoutMS = 5000
)

// ProxyGroupInfo describes an outbound proxy group (Selector / URLTest / Fallback / LoadBalance)
// exposed by sing-box's Clash API, including its current selection and latest cached member delays.
type ProxyGroupInfo struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Now    string         `json:"now"`
	All    []string       `json:"all"`
	Delays map[string]int `json:"delays"`
}

type clashProxiesResponse struct {
	Proxies map[string]clashProxyEntry `json:"proxies"`
}

type clashProxyEntry struct {
	Name    string              `json:"name"`
	Type    string              `json:"type"`
	Now     string              `json:"now"`
	All     []string            `json:"all"`
	History []clashDelayHistory `json:"history"`
}

type clashDelayHistory struct {
	Time  string `json:"time"`
	Delay int    `json:"delay"`
}

// ListProxyGroups queries Clash API GET /proxies and extracts all outbound proxy groups
// (entries with non-empty `all`) along with each member's latest positive delay in milliseconds.
func (h *Hub) ListProxyGroups(ctx context.Context) ([]ProxyGroupInfo, error) {
	addr, secret := h.getTarget()
	if addr == "" {
		return []ProxyGroupInfo{}, errors.New("clash API address is not configured")
	}

	reqURL := toHTTPBaseURL(addr) + "/proxies"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build proxies request: %w", err)
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch proxies: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("clash API GET /proxies returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload clashProxiesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode proxies response: %w", err)
	}

	groups := make([]ProxyGroupInfo, 0)
	for key, entry := range payload.Proxies {
		if len(entry.All) == 0 {
			continue
		}
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = key
		}

		allCopy := make([]string, len(entry.All))
		copy(allCopy, entry.All)

		delays := make(map[string]int)
		for _, memberName := range allCopy {
			if member, ok := payload.Proxies[memberName]; ok && len(member.History) > 0 {
				latest := member.History[len(member.History)-1]
				if latest.Delay > 0 {
					delays[memberName] = latest.Delay
				}
			}
		}

		groups = append(groups, ProxyGroupInfo{
			Name:   name,
			Type:   entry.Type,
			Now:    entry.Now,
			All:    allCopy,
			Delays: delays,
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Name < groups[j].Name
	})

	return groups, nil
}

// TestProxyDelay runs a live URL delay test for proxyName via Clash API GET /proxies/{name}/delay.
// Defaults: testURL="https://www.gstatic.com/generate_204", timeoutMS=5000.
func (h *Hub) TestProxyDelay(ctx context.Context, proxyName, testURL string, timeoutMS int) (int, error) {
	name := strings.TrimSpace(proxyName)
	if name == "" {
		return 0, errors.New("proxy name is required")
	}
	addr, secret := h.getTarget()
	if addr == "" {
		return 0, errors.New("clash API address is not configured")
	}

	u := strings.TrimSpace(testURL)
	if u == "" {
		u = defaultProxyDelayTestURL
	}
	if timeoutMS <= 0 {
		timeoutMS = defaultProxyDelayTimeoutMS
	}

	reqURL := fmt.Sprintf("%s/proxies/%s/delay?timeout=%s&url=%s",
		toHTTPBaseURL(addr),
		url.PathEscape(name),
		strconv.Itoa(timeoutMS),
		url.QueryEscape(u),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, fmt.Errorf("build delay test request: %w", err)
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	// Allow headroom above Clash API's internal timeout so HTTP client doesn't abort prematurely.
	delayClient := &http.Client{
		Transport: h.httpClient.Transport,
		Timeout:   time.Duration(timeoutMS)*time.Millisecond + 3*time.Second,
	}

	resp, err := delayClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("test proxy delay %q: %w", name, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &errBody) == nil && strings.TrimSpace(errBody.Message) != "" {
			return 0, fmt.Errorf("clash API delay test failed (HTTP %d): %s", resp.StatusCode, errBody.Message)
		}
		return 0, fmt.Errorf("clash API delay test returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Delay int `json:"delay"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("decode delay test response: %w", err)
	}
	return result.Delay, nil
}

// SelectProxy switches the active outbound of groupName to proxyName via Clash API PUT /proxies/{group}.
func (h *Hub) SelectProxy(ctx context.Context, groupName, proxyName string) error {
	group := strings.TrimSpace(groupName)
	proxy := strings.TrimSpace(proxyName)
	if group == "" {
		return errors.New("group name is required")
	}
	if proxy == "" {
		return errors.New("proxy name is required")
	}

	addr, secret := h.getTarget()
	if addr == "" {
		return errors.New("clash API address is not configured")
	}

	payload, err := json.Marshal(map[string]string{"name": proxy})
	if err != nil {
		return fmt.Errorf("encode select proxy payload: %w", err)
	}

	reqURL := toHTTPBaseURL(addr) + "/proxies/" + url.PathEscape(group)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build select proxy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("select proxy %q in group %q: %w", proxy, group, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("clash API PUT /proxies/%s returned HTTP %d: %s", group, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
