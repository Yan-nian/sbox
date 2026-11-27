package syncer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultGitHubAPIBase = "https://api.github.com"
	defaultPushBranch    = "main"
	defaultPushFilePath  = "sbox-nodes.txt"
	pushUserAgent        = "sbox-panel/1.0"
	maxPushRespBytes     = 4 << 20 // 4 MiB
)

// PushTargetConfig configures an outbound subscription push destination (GitHub Gist,
// GitHub Repo file, or remote Sub-Store backend).
type PushTargetConfig struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`       // "github_gist" | "github_repo" | "sub_store"
	Format     string `json:"format"`     // "uri" | "base64" | "singbox" | "clash"
	MergeMode  string `json:"merge_mode"` // "replace" | "merge_by_instance"
	InstanceID string `json:"instance_id"`
	// GitHub Gist / Repo
	Token    string `json:"token"`
	GistID   string `json:"gist_id,omitempty"`
	Repo     string `json:"repo,omitempty"`      // "owner/repo"
	Branch   string `json:"branch,omitempty"`    // default "main"
	FilePath string `json:"file_path,omitempty"` // default "sbox-nodes.txt"
	// Sub-Store API
	SubStoreURL  string `json:"sub_store_url,omitempty"`  // e.g. "https://sub.example.com/secret_prefix"
	SubStoreName string `json:"sub_store_name,omitempty"` // default "sbox-<instance_id>"
	ProxyURL     string `json:"proxy_url,omitempty"`
	APIBaseURL   string `json:"api_base_url,omitempty"` // optional override for httptest (default "https://api.github.com")
}

// PushResult describes the outcome of pushing rendered subscription content to a remote target.
type PushResult struct {
	Success    bool   `json:"success"`
	HTTPStatus int    `json:"http_status"`
	RemoteURL  string `json:"remote_url,omitempty"`
	NodeCount  int    `json:"node_count"`
	Message    string `json:"message"`
}

// MergeInstanceBlock updates only the delimited section belonging to instanceID inside
// existingContent while preserving all other instances' blocks and manual lines intact.
// When isBase64 is true, existingContent and newContent are gracefully decoded from Base64
// into UTF-8 text before merging (falling back to raw text if existingContent is empty or
// plain) and the merged result is re-encoded with standard Base64.
func MergeInstanceBlock(existingContent, instanceID, newContent string, isBase64 bool) string {
	id := strings.TrimSpace(instanceID)
	if id == "" {
		id = "default"
	}

	existingPlain := existingContent
	newPlain := newContent
	if isBase64 {
		existingPlain = decodeTextIfBase64(existingContent)
		newPlain = decodeTextIfBase64(newContent)
	}

	existingPlain = normalizeNewlines(existingPlain)
	newPlain = normalizeNewlines(newPlain)

	startMarker := fmt.Sprintf("# >>> sbox-panel:%s >>>", id)
	endMarker := fmt.Sprintf("# <<< sbox-panel:%s <<<", id)

	body := strings.Trim(newPlain, "\n")
	blockLines := make([]string, 0, 4)
	blockLines = append(blockLines, startMarker)
	if body != "" {
		blockLines = append(blockLines, strings.Split(body, "\n")...)
	}
	blockLines = append(blockLines, endMarker)

	var mergedLines []string
	if strings.TrimSpace(existingPlain) == "" {
		mergedLines = blockLines
	} else {
		trimmedExisting := strings.TrimRight(existingPlain, "\n")
		existingLines := strings.Split(trimmedExisting, "\n")

		startIdx := -1
		endIdx := -1
		for i, line := range existingLines {
			t := strings.TrimSpace(line)
			if startIdx < 0 {
				if t == startMarker {
					startIdx = i
				}
			} else if t == endMarker {
				endIdx = i
				break
			}
		}

		if startIdx >= 0 && endIdx > startIdx {
			mergedLines = make([]string, 0, len(existingLines)-(endIdx-startIdx+1)+len(blockLines))
			mergedLines = append(mergedLines, existingLines[:startIdx]...)
			mergedLines = append(mergedLines, blockLines...)
			mergedLines = append(mergedLines, existingLines[endIdx+1:]...)
		} else {
			// Trim trailing blank lines before appending so repeated merges stay tidy.
			for len(existingLines) > 0 && strings.TrimSpace(existingLines[len(existingLines)-1]) == "" {
				existingLines = existingLines[:len(existingLines)-1]
			}
			mergedLines = make([]string, 0, len(existingLines)+len(blockLines))
			mergedLines = append(mergedLines, existingLines...)
			mergedLines = append(mergedLines, blockLines...)
		}
	}

	mergedPlain := strings.Join(mergedLines, "\n")
	if isBase64 {
		return base64.StdEncoding.EncodeToString([]byte(mergedPlain))
	}
	return mergedPlain
}

// PushToTarget dispatches an outbound subscription push to GitHub Gist, GitHub Repo,
// or Sub-Store using the shared proxy-aware HTTP client.
func PushToTarget(ctx context.Context, cfg PushTargetConfig, content string, nodeCount int) PushResult {
	targetType := strings.ToLower(strings.TrimSpace(cfg.Type))
	switch targetType {
	case "github_gist":
		return pushGitHubGist(ctx, cfg, content, nodeCount)
	case "github_repo":
		return pushGitHubRepo(ctx, cfg, content, nodeCount)
	case "sub_store":
		return pushSubStore(ctx, cfg, content, nodeCount)
	default:
		return PushResult{
			Success:   false,
			NodeCount: nodeCount,
			Message:   fmt.Sprintf("unsupported push target type %q", cfg.Type),
		}
	}
}

func shouldMergeByInstance(cfg PushTargetConfig) (merge bool, isBase64 bool) {
	mode := strings.ToLower(strings.TrimSpace(cfg.MergeMode))
	format := strings.ToLower(strings.TrimSpace(cfg.Format))
	if mode != "merge_by_instance" {
		return false, format == "base64"
	}
	if format == "uri" || format == "base64" {
		return true, format == "base64"
	}
	return false, false
}

func githubAPIBase(cfg PushTargetConfig) string {
	base := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if base == "" {
		return defaultGitHubAPIBase
	}
	return base
}

type githubGistFile struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
	RawURL    string `json:"raw_url"`
}

type githubGistResponse struct {
	HTMLURL string                    `json:"html_url"`
	Files   map[string]githubGistFile `json:"files"`
}

func pushGitHubGist(ctx context.Context, cfg PushTargetConfig, content string, nodeCount int) PushResult {
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return PushResult{Success: false, NodeCount: nodeCount, Message: "github_gist requires token"}
	}
	gistID := strings.TrimSpace(cfg.GistID)
	if gistID == "" {
		return PushResult{Success: false, NodeCount: nodeCount, Message: "github_gist requires gist_id"}
	}
	filePath := strings.TrimSpace(cfg.FilePath)
	if filePath == "" {
		filePath = defaultPushFilePath
	}

	client, err := newHTTPClient(cfg.ProxyURL, defaultHTTPTimeout)
	if err != nil {
		return PushResult{Success: false, NodeCount: nodeCount, Message: err.Error()}
	}

	apiBase := githubAPIBase(cfg)
	gistURL := fmt.Sprintf("%s/gists/%s", apiBase, url.PathEscape(gistID))
	finalContent := content

	if merge, isB64 := shouldMergeByInstance(cfg); merge {
		status, respBody, reqErr := doGitHubRequest(ctx, client, http.MethodGet, gistURL, token, nil)
		if reqErr != nil {
			return PushResult{Success: false, HTTPStatus: status, NodeCount: nodeCount, Message: fmt.Sprintf("fetch existing gist: %v", reqErr)}
		}
		if status < 200 || status >= 300 {
			return PushResult{Success: false, HTTPStatus: status, NodeCount: nodeCount, Message: formatHTTPError("fetch existing gist", status, respBody)}
		}

		var gistDoc githubGistResponse
		if err := json.Unmarshal(respBody, &gistDoc); err != nil {
			return PushResult{Success: false, HTTPStatus: status, NodeCount: nodeCount, Message: fmt.Sprintf("decode existing gist JSON: %v", err)}
		}

		var existing string
		if f, exists := gistDoc.Files[filePath]; exists {
			existing = f.Content
			if f.Truncated && strings.TrimSpace(f.RawURL) != "" {
				rawStatus, rawBody, rawErr := doRawGET(ctx, client, f.RawURL, token)
				if rawErr != nil {
					return PushResult{Success: false, HTTPStatus: rawStatus, NodeCount: nodeCount, Message: fmt.Sprintf("fetch truncated gist raw_url: %v", rawErr)}
				}
				if rawStatus < 200 || rawStatus >= 300 {
					return PushResult{Success: false, HTTPStatus: rawStatus, NodeCount: nodeCount, Message: formatHTTPError("fetch truncated gist raw_url", rawStatus, rawBody)}
				}
				existing = string(rawBody)
			}
		}

		finalContent = MergeInstanceBlock(existing, cfg.InstanceID, content, isB64)
	}

	// GitHub Gist PATCH deletes a file if content is strictly empty.
	if finalContent == "" {
		finalContent = "\n"
	}

	patchPayload := map[string]any{
		"files": map[string]any{
			filePath: map[string]string{
				"content": finalContent,
			},
		},
	}
	patchBytes, err := json.Marshal(patchPayload)
	if err != nil {
		return PushResult{Success: false, NodeCount: nodeCount, Message: fmt.Sprintf("marshal gist patch: %v", err)}
	}

	status, respBody, err := doGitHubRequest(ctx, client, http.MethodPatch, gistURL, token, patchBytes)
	if err != nil {
		return PushResult{Success: false, HTTPStatus: status, NodeCount: nodeCount, Message: fmt.Sprintf("patch gist: %v", err)}
	}
	if status < 200 || status >= 300 {
		return PushResult{Success: false, HTTPStatus: status, NodeCount: nodeCount, Message: formatHTTPError("patch gist", status, respBody)}
	}

	var updated githubGistResponse
	var remoteURL string
	if err := json.Unmarshal(respBody, &updated); err == nil {
		if f, ok := updated.Files[filePath]; ok && strings.TrimSpace(f.RawURL) != "" {
			remoteURL = strings.TrimSpace(f.RawURL)
		} else if strings.TrimSpace(updated.HTMLURL) != "" {
			remoteURL = strings.TrimSpace(updated.HTMLURL)
		}
	}

	return PushResult{
		Success:    true,
		HTTPStatus: status,
		RemoteURL:  remoteURL,
		NodeCount:  nodeCount,
		Message:    fmt.Sprintf("pushed %d nodes to GitHub Gist %s (%s)", nodeCount, gistID, filePath),
	}
}

type githubRepoContentResponse struct {
	SHA         string `json:"sha"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding"`
	DownloadURL string `json:"download_url"`
}

func pushGitHubRepo(ctx context.Context, cfg PushTargetConfig, content string, nodeCount int) PushResult {
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return PushResult{Success: false, NodeCount: nodeCount, Message: "github_repo requires token"}
	}
	repo := strings.Trim(strings.TrimSpace(cfg.Repo), "/")
	repoParts := strings.Split(repo, "/")
	if len(repoParts) != 2 || strings.TrimSpace(repoParts[0]) == "" || strings.TrimSpace(repoParts[1]) == "" {
		return PushResult{Success: false, NodeCount: nodeCount, Message: fmt.Sprintf("github_repo invalid repo %q (expected owner/repo)", cfg.Repo)}
	}
	owner, repoName := strings.TrimSpace(repoParts[0]), strings.TrimSpace(repoParts[1])
	repoClean := owner + "/" + repoName

	branch := strings.TrimSpace(cfg.Branch)
	if branch == "" {
		branch = defaultPushBranch
	}
	filePath := strings.TrimLeft(strings.TrimSpace(cfg.FilePath), "/")
	if filePath == "" {
		filePath = defaultPushFilePath
	}

	client, err := newHTTPClient(cfg.ProxyURL, defaultHTTPTimeout)
	if err != nil {
		return PushResult{Success: false, NodeCount: nodeCount, Message: err.Error()}
	}

	apiBase := githubAPIBase(cfg)
	contentsBaseURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s",
		apiBase,
		url.PathEscape(owner),
		url.PathEscape(repoName),
		escapeURLPathSegments(filePath),
	)
	getURL := contentsBaseURL + "?ref=" + url.QueryEscape(branch)
	merge, isB64 := shouldMergeByInstance(cfg)

	instanceLabel := strings.TrimSpace(cfg.InstanceID)
	if instanceLabel == "" {
		instanceLabel = "default"
	}

	// Up to 2 attempts: automatically retry once on 409 Conflict (optimistic lock).
	for attempt := 0; attempt < 2; attempt++ {
		var existingSHA string
		var existingContent string

		getStatus, getBody, getErr := doGitHubRequest(ctx, client, http.MethodGet, getURL, token, nil)
		if getErr != nil {
			return PushResult{Success: false, HTTPStatus: getStatus, NodeCount: nodeCount, Message: fmt.Sprintf("fetch repo file: %v", getErr)}
		}

		switch {
		case getStatus == http.StatusNotFound:
			// File does not exist yet; create it.
		case getStatus >= 200 && getStatus < 300:
			var fileDoc githubRepoContentResponse
			if err := json.Unmarshal(getBody, &fileDoc); err != nil {
				return PushResult{Success: false, HTTPStatus: getStatus, NodeCount: nodeCount, Message: fmt.Sprintf("decode repo file JSON: %v", err)}
			}
			existingSHA = strings.TrimSpace(fileDoc.SHA)
			if merge {
				if strings.TrimSpace(fileDoc.Content) != "" && (fileDoc.Encoding == "" || strings.EqualFold(fileDoc.Encoding, "base64")) {
					if decoded, ok := decodeFlexibleBase64([]byte(fileDoc.Content)); ok {
						existingContent = string(decoded)
					} else {
						existingContent = fileDoc.Content
					}
				} else if strings.TrimSpace(fileDoc.Content) != "" {
					existingContent = fileDoc.Content
				} else if strings.TrimSpace(fileDoc.DownloadURL) != "" {
					dlStatus, dlBody, dlErr := doRawGET(ctx, client, fileDoc.DownloadURL, token)
					if dlErr != nil {
						return PushResult{Success: false, HTTPStatus: dlStatus, NodeCount: nodeCount, Message: fmt.Sprintf("fetch repo download_url: %v", dlErr)}
					}
					if dlStatus < 200 || dlStatus >= 300 {
						return PushResult{Success: false, HTTPStatus: dlStatus, NodeCount: nodeCount, Message: formatHTTPError("fetch repo download_url", dlStatus, dlBody)}
					}
					existingContent = string(dlBody)
				}
			}
		default:
			return PushResult{Success: false, HTTPStatus: getStatus, NodeCount: nodeCount, Message: formatHTTPError("fetch repo file", getStatus, getBody)}
		}

		finalContent := content
		if merge {
			finalContent = MergeInstanceBlock(existingContent, cfg.InstanceID, content, isB64)
		}

		putPayload := map[string]any{
			"message": fmt.Sprintf("chore(sbox-panel): sync %s (%d nodes)", instanceLabel, nodeCount),
			"content": base64.StdEncoding.EncodeToString([]byte(finalContent)),
			"branch":  branch,
		}
		if existingSHA != "" {
			putPayload["sha"] = existingSHA
		}
		putBytes, err := json.Marshal(putPayload)
		if err != nil {
			return PushResult{Success: false, NodeCount: nodeCount, Message: fmt.Sprintf("marshal repo put: %v", err)}
		}

		putStatus, putBody, putErr := doGitHubRequest(ctx, client, http.MethodPut, contentsBaseURL, token, putBytes)
		if putErr != nil {
			return PushResult{Success: false, HTTPStatus: putStatus, NodeCount: nodeCount, Message: fmt.Sprintf("put repo file: %v", putErr)}
		}
		if putStatus == http.StatusConflict && attempt == 0 {
			// Optimistic lock conflict: re-fetch latest sha & content and retry once.
			continue
		}
		if putStatus < 200 || putStatus >= 300 {
			return PushResult{Success: false, HTTPStatus: putStatus, NodeCount: nodeCount, Message: formatHTTPError("put repo file", putStatus, putBody)}
		}

		remoteURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repoClean, branch, filePath)
		return PushResult{
			Success:    true,
			HTTPStatus: putStatus,
			RemoteURL:  remoteURL,
			NodeCount:  nodeCount,
			Message:    fmt.Sprintf("pushed %d nodes to GitHub Repo %s@%s (%s)", nodeCount, repoClean, branch, filePath),
		}
	}

	return PushResult{
		Success:    false,
		HTTPStatus: http.StatusConflict,
		NodeCount:  nodeCount,
		Message:    "put repo file failed after 409 conflict retry",
	}
}

func doGitHubRequest(ctx context.Context, client *http.Client, method, targetURL, token string, body []byte) (int, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
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

func doRawGET(ctx context.Context, client *http.Client, targetURL, token string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", pushUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxSubBodyBytes+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(respBytes) > maxSubBodyBytes {
		return resp.StatusCode, nil, fmt.Errorf("raw file exceeds %d MiB limit", maxSubBodyBytes>>20)
	}
	return resp.StatusCode, respBytes, nil
}

func escapeURLPathSegments(path string) string {
	parts := strings.Split(path, "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return strings.Join(parts, "/")
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// decodeTextIfBase64 decodes standard/URL/unpadded Base64 text into plain UTF-8 lines,
// falling back to raw text when s is empty, already plain URI/marker lines, or not valid UTF-8 text.
func decodeTextIfBase64(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") || strings.Contains(trimmed, "# >>> sbox-panel:") {
		return s
	}
	decoded, ok := decodeFlexibleBase64([]byte(trimmed))
	if !ok || !utf8.Valid(decoded) || !isTextPayload(decoded) {
		return s
	}
	return string(bytes.TrimPrefix(decoded, []byte{0xEF, 0xBB, 0xBF}))
}

func isTextPayload(data []byte) bool {
	for _, r := range string(data) {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func formatHTTPError(action string, status int, body []byte) string {
	var ghErr struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &ghErr); err == nil && strings.TrimSpace(ghErr.Message) != "" {
		return fmt.Sprintf("%s HTTP %d: %s", action, status, strings.TrimSpace(ghErr.Message))
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}
	if snippet != "" {
		return fmt.Sprintf("%s HTTP %d: %s", action, status, snippet)
	}
	return fmt.Sprintf("%s HTTP %d", action, status)
}
