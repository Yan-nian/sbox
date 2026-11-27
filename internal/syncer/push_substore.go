package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type subStoreAPIResponse struct {
	Status  string          `json:"status"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type subStoreListItem struct {
	Name string `json:"name"`
}

// normalizeSubStoreBaseURL trims trailing slashes and strips accidental trailing
// `/api/subs`, `/api/sub`, or `/api` suffixes from a user-supplied Sub-Store URL.
func normalizeSubStoreBaseURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if base == "" {
		return ""
	}
	lower := strings.ToLower(base)
	for _, suffix := range []string{"/api/subs", "/api/sub", "/api"} {
		if strings.HasSuffix(lower, suffix) {
			base = strings.TrimRight(base[:len(base)-len(suffix)], "/")
			break
		}
	}
	return base
}

func pushSubStore(ctx context.Context, cfg PushTargetConfig, content string, nodeCount int) PushResult {
	baseURL := normalizeSubStoreBaseURL(cfg.SubStoreURL)
	if baseURL == "" {
		return PushResult{Success: false, NodeCount: nodeCount, Message: "sub_store requires sub_store_url"}
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return PushResult{Success: false, NodeCount: nodeCount, Message: fmt.Sprintf("sub_store invalid sub_store_url %q", cfg.SubStoreURL)}
	}

	name := strings.TrimSpace(cfg.SubStoreName)
	if name == "" {
		inst := sanitizeTagBody(cfg.InstanceID)
		if inst == "" {
			inst = "default"
		}
		name = "sbox-" + inst
	}

	// Sub-Store source: "local" expects plain URI/Clash/sing-box text rather than Base64-wrapped text.
	plainContent := content
	if strings.EqualFold(strings.TrimSpace(cfg.Format), "base64") {
		plainContent = decodeTextIfBase64(content)
	}

	client, err := newHTTPClient(cfg.ProxyURL, defaultHTTPTimeout)
	if err != nil {
		return PushResult{Success: false, NodeCount: nodeCount, Message: err.Error()}
	}

	escapedName := url.PathEscape(name)
	subsURL := fmt.Sprintf("%s/api/subs", baseURL)
	singleSubURL := fmt.Sprintf("%s/api/sub/%s", baseURL, escapedName)

	// Step 1: Probe existence via GET /api/subs (real Sub-Store throws HTTP 500 on GET /api/sub/{missing}).
	listStatus, listBody, listErr := doSubStoreRequest(ctx, client, http.MethodGet, subsURL, nil)
	if listErr != nil {
		return PushResult{Success: false, HTTPStatus: listStatus, NodeCount: nodeCount, Message: fmt.Sprintf("list sub-store subscriptions: %v", listErr)}
	}
	if listStatus < 200 || listStatus >= 300 {
		return PushResult{
			Success:    false,
			HTTPStatus: listStatus,
			NodeCount:  nodeCount,
			Message:    formatSubStoreError("list sub-store subscriptions", listStatus, listBody),
		}
	}

	exists, parseErr := subStoreListContains(listBody, name)
	if parseErr != nil {
		return PushResult{
			Success:    false,
			HTTPStatus: listStatus,
			NodeCount:  nodeCount,
			Message:    formatSubStoreError("list sub-store subscriptions", listStatus, listBody),
		}
	}

	// Step 2: PATCH existing subscription with minimal payload (preserving displayName/icon/tag/process)
	// or POST new subscription with full defaults.
	var payload map[string]any
	method := http.MethodPost
	targetURL := subsURL
	action := "create sub-store subscription"
	if exists {
		method = http.MethodPatch
		targetURL = singleSubURL
		action = "update sub-store subscription"
		payload = map[string]any{
			"name":    name,
			"source":  "local",
			"content": plainContent,
		}
	} else {
		payload = map[string]any{
			"name":        name,
			"displayName": name,
			"source":      "local",
			"url":         "",
			"content":     plainContent,
			"process":     []any{},
		}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return PushResult{Success: false, NodeCount: nodeCount, Message: fmt.Sprintf("marshal sub-store payload: %v", err)}
	}

	writeStatus, writeBody, writeErr := doSubStoreRequest(ctx, client, method, targetURL, payloadBytes)
	if writeErr != nil {
		return PushResult{Success: false, HTTPStatus: writeStatus, NodeCount: nodeCount, Message: fmt.Sprintf("%s: %v", action, writeErr)}
	}
	if writeStatus < 200 || writeStatus >= 300 {
		return PushResult{
			Success:    false,
			HTTPStatus: writeStatus,
			NodeCount:  nodeCount,
			Message:    formatSubStoreError(action, writeStatus, writeBody),
		}
	}

	var writeDoc subStoreAPIResponse
	if err := json.Unmarshal(writeBody, &writeDoc); err == nil {
		st := strings.ToLower(strings.TrimSpace(writeDoc.Status))
		if st == "failed" || st == "error" {
			return PushResult{
				Success:    false,
				HTTPStatus: writeStatus,
				NodeCount:  nodeCount,
				Message:    formatSubStoreError(action, writeStatus, writeBody),
			}
		}
	}

	remoteURL := fmt.Sprintf("%s/download/%s", baseURL, escapedName)
	verb := "created"
	if exists {
		verb = "updated"
	}
	return PushResult{
		Success:    true,
		HTTPStatus: writeStatus,
		RemoteURL:  remoteURL,
		NodeCount:  nodeCount,
		Message:    fmt.Sprintf("%s Sub-Store subscription %q (%d nodes)", verb, name, nodeCount),
	}
}

func subStoreListContains(body []byte, targetName string) (bool, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false, nil
	}

	// Support direct JSON array responses: [ { "name": "..." }, ... ]
	if trimmed[0] == '[' {
		var items []subStoreListItem
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return false, err
		}
		for _, item := range items {
			if item.Name == targetName {
				return true, nil
			}
		}
		return false, nil
	}

	// Standard Sub-Store envelope: { "status": "success", "data": [ { "name": "..." }, ... ] }
	var doc subStoreAPIResponse
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return false, err
	}
	st := strings.ToLower(strings.TrimSpace(doc.Status))
	if st == "failed" || st == "error" {
		return false, fmt.Errorf("sub-store returned status %q", doc.Status)
	}
	dataTrimmed := bytes.TrimSpace(doc.Data)
	if len(dataTrimmed) == 0 || bytes.Equal(dataTrimmed, []byte("null")) {
		return false, nil
	}
	var items []subStoreListItem
	if err := json.Unmarshal(dataTrimmed, &items); err != nil {
		return false, err
	}
	for _, item := range items {
		if item.Name == targetName {
			return true, nil
		}
	}
	return false, nil
}

func doSubStoreRequest(ctx context.Context, client *http.Client, method, targetURL string, body []byte) (int, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", pushUserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxPushRespBytes+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(respBytes) > maxPushRespBytes {
		return resp.StatusCode, nil, fmt.Errorf("response body exceeds %d MiB limit", maxPushRespBytes>>20)
	}
	return resp.StatusCode, respBytes, nil
}

func formatSubStoreError(action string, status int, body []byte) string {
	var doc subStoreAPIResponse
	if err := json.Unmarshal(body, &doc); err == nil {
		if doc.Error != nil && strings.TrimSpace(doc.Error.Message) != "" {
			return fmt.Sprintf("%s HTTP %d: %s", action, status, strings.TrimSpace(doc.Error.Message))
		}
		if strings.TrimSpace(doc.Message) != "" {
			return fmt.Sprintf("%s HTTP %d: %s", action, status, strings.TrimSpace(doc.Message))
		}
	}
	return formatHTTPError(action, status, body)
}
