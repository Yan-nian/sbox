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

func pushSubStore(ctx context.Context, cfg PushTargetConfig, content string, nodeCount int) PushResult {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.SubStoreURL), "/")
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
	singleSubURL := fmt.Sprintf("%s/api/sub/%s", baseURL, escapedName)
	createSubURL := fmt.Sprintf("%s/api/subs", baseURL)

	// Step 1: Probe whether the subscription already exists in Sub-Store.
	checkStatus, checkBody, checkErr := doSubStoreRequest(ctx, client, http.MethodGet, singleSubURL, nil)
	if checkErr != nil {
		return PushResult{Success: false, HTTPStatus: checkStatus, NodeCount: nodeCount, Message: fmt.Sprintf("check sub-store subscription: %v", checkErr)}
	}

	var exists bool
	switch {
	case checkStatus == http.StatusNotFound:
		exists = false
	case checkStatus >= 200 && checkStatus < 300:
		exists = subStoreCheckExists(checkBody)
	default:
		return PushResult{
			Success:    false,
			HTTPStatus: checkStatus,
			NodeCount:  nodeCount,
			Message:    formatSubStoreError("check sub-store subscription", checkStatus, checkBody),
		}
	}

	// Step 2: PATCH existing subscription or POST new subscription.
	payload := map[string]any{
		"name":        name,
		"displayName": name,
		"source":      "local",
		"content":     plainContent,
		"process":     []any{},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return PushResult{Success: false, NodeCount: nodeCount, Message: fmt.Sprintf("marshal sub-store payload: %v", err)}
	}

	method := http.MethodPost
	targetURL := createSubURL
	action := "create sub-store subscription"
	if exists {
		method = http.MethodPatch
		targetURL = singleSubURL
		action = "update sub-store subscription"
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

func subStoreCheckExists(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	var doc subStoreAPIResponse
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return false
	}
	st := strings.ToLower(strings.TrimSpace(doc.Status))
	if st == "failed" || st == "error" {
		return false
	}
	dataTrimmed := bytes.TrimSpace(doc.Data)
	if bytes.Equal(dataTrimmed, []byte("null")) {
		return false
	}
	if st == "success" {
		return true
	}
	if len(dataTrimmed) > 0 &&
		!bytes.Equal(dataTrimmed, []byte("{}")) &&
		!bytes.Equal(dataTrimmed, []byte(`""`)) {
		return true
	}
	return false
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
