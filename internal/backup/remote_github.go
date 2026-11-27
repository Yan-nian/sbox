package backup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	defaultGitHubAPIBase = "https://api.github.com"
	defaultGitHubBranch  = "main"
	defaultPathPrefix    = "backups/sbox-panel"
	defaultKeepCount     = 5
)

// RemoteGitHubConfig configures remote encrypted backup storage in a GitHub repository or Gist.
type RemoteGitHubConfig struct {
	Type       string `json:"type"`        // "github_repo" | "github_gist"
	Token      string `json:"token"`
	Repo       string `json:"repo"`        // "owner/repo"
	Branch     string `json:"branch"`      // default "main"
	PathPrefix string `json:"path_prefix"` // default "backups/sbox-panel"
	GistID     string `json:"gist_id"`
	KeepCount  int    `json:"keep_count"`  // default 5
	Passphrase string `json:"passphrase"`  // REQUIRED for remote push
	ProxyURL   string `json:"proxy_url"`
	APIBaseURL string `json:"api_base_url,omitempty"` // optional for httptest
}

// RemoteBackupEntry represents a single backup artifact stored remotely on GitHub.
type RemoteBackupEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SHA       string `json:"sha,omitempty"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at,omitempty"`
}

// PushGitHubBackup uploads an encrypted backup archive to GitHub Repo or Gist and prunes
// oldest snapshots exceeding cfg.KeepCount. A non-empty cfg.Passphrase is required.
func PushGitHubBackup(ctx context.Context, archive []byte, filename string, cfg RemoteGitHubConfig) (*RemoteBackupEntry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(archive) == 0 {
		return nil, errors.New("backup: archive payload is empty")
	}
	if strings.TrimSpace(cfg.Passphrase) == "" {
		return nil, errors.New("backup: passphrase is required for remote GitHub backup push")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("backup: github token is required")
	}

	// Defense-in-depth: never allow plaintext backups to be pushed remotely.
	payload := archive
	if !IsEncrypted(payload) {
		var err error
		payload, err = encryptPayload(payload, cfg.Passphrase)
		if err != nil {
			return nil, err
		}
	}

	filename = path.Base(strings.TrimSpace(filename))
	if filename == "" || filename == "." || filename == "/" {
		filename = fmt.Sprintf("sbox-panel-backup-%s.sboxbak", time.Now().UTC().Format("20060102-150405"))
	}

	cfg = normalizeRemoteConfig(cfg)
	client, err := newGitHubHTTPClient(cfg.ProxyURL)
	if err != nil {
		return nil, err
	}

	switch cfg.Type {
	case "github_repo":
		return pushGitHubRepoBackup(ctx, client, payload, filename, cfg)
	case "github_gist":
		return pushGitHubGistBackup(ctx, client, payload, filename, cfg)
	default:
		return nil, fmt.Errorf("backup: unsupported remote backup type %q", cfg.Type)
	}
}

// ListGitHubBackups lists existing remote backup entries sorted newest-first.
func ListGitHubBackups(ctx context.Context, cfg RemoteGitHubConfig) ([]RemoteBackupEntry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("backup: github token is required")
	}

	cfg = normalizeRemoteConfig(cfg)
	client, err := newGitHubHTTPClient(cfg.ProxyURL)
	if err != nil {
		return nil, err
	}

	switch cfg.Type {
	case "github_repo":
		return listGitHubRepoBackups(ctx, client, cfg)
	case "github_gist":
		return listGitHubGistBackups(ctx, client, cfg)
	default:
		return nil, fmt.Errorf("backup: unsupported remote backup type %q", cfg.Type)
	}
}

// FetchGitHubBackup downloads a remote backup archive by its entry path/name.
func FetchGitHubBackup(ctx context.Context, entryPath string, cfg RemoteGitHubConfig) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	entryPath = strings.TrimSpace(entryPath)
	if entryPath == "" {
		return nil, errors.New("backup: entry path is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("backup: github token is required")
	}

	cfg = normalizeRemoteConfig(cfg)
	client, err := newGitHubHTTPClient(cfg.ProxyURL)
	if err != nil {
		return nil, err
	}

	switch cfg.Type {
	case "github_repo":
		return fetchGitHubRepoBackup(ctx, client, entryPath, cfg)
	case "github_gist":
		return fetchGitHubGistBackup(ctx, client, entryPath, cfg)
	default:
		return nil, fmt.Errorf("backup: unsupported remote backup type %q", cfg.Type)
	}
}

func normalizeRemoteConfig(cfg RemoteGitHubConfig) RemoteGitHubConfig {
	cfg.Type = strings.ToLower(strings.TrimSpace(cfg.Type))
	if cfg.Type == "" {
		if strings.TrimSpace(cfg.GistID) != "" && strings.TrimSpace(cfg.Repo) == "" {
			cfg.Type = "github_gist"
		} else {
			cfg.Type = "github_repo"
		}
	}
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.Repo = strings.Trim(strings.TrimSpace(cfg.Repo), "/")
	cfg.Branch = strings.TrimSpace(cfg.Branch)
	if cfg.Branch == "" {
		cfg.Branch = defaultGitHubBranch
	}
	cfg.PathPrefix = strings.Trim(strings.TrimSpace(cfg.PathPrefix), "/")
	if cfg.PathPrefix == "" {
		cfg.PathPrefix = defaultPathPrefix
	}
	cfg.GistID = strings.TrimSpace(cfg.GistID)
	if cfg.KeepCount <= 0 {
		cfg.KeepCount = defaultKeepCount
	}
	cfg.APIBaseURL = strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultGitHubAPIBase
	}
	return cfg
}

func newGitHubHTTPClient(proxyURL string) (*http.Client, error) {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
	if raw := strings.TrimSpace(proxyURL); raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("backup: invalid proxy_url %q: %w", raw, err)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{
		Transport: tr,
		Timeout:   60 * time.Second,
	}, nil
}

func doGitHubRequest(ctx context.Context, client *http.Client, method, reqURL, token string, body any) (int, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("backup: marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("backup: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "sbox-panel-backup")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("backup: github %s %s failed: %w", method, reqURL, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("backup: read response body: %w", err)
	}
	return resp.StatusCode, respBytes, nil
}

func escapeGitHubPath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return strings.Join(parts, "/")
}

// --- GitHub Repo Implementation ---

type ghContentItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	SHA         string `json:"sha"`
	Size        int64  `json:"size"`
	Type        string `json:"type"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding"`
	DownloadURL string `json:"download_url"`
}

func pushGitHubRepoBackup(ctx context.Context, client *http.Client, payload []byte, filename string, cfg RemoteGitHubConfig) (*RemoteBackupEntry, error) {
	if !strings.Contains(cfg.Repo, "/") {
		return nil, fmt.Errorf("backup: invalid github repo %q (expected owner/repo)", cfg.Repo)
	}

	fullPath := path.Join(cfg.PathPrefix, filename)
	contentsURL := fmt.Sprintf("%s/repos/%s/contents/%s", cfg.APIBaseURL, cfg.Repo, escapeGitHubPath(fullPath))

	// Check if target file already exists to supply its SHA for update.
	var existingSHA string
	status, respBody, err := doGitHubRequest(ctx, client, http.MethodGet, contentsURL+"?ref="+url.QueryEscape(cfg.Branch), cfg.Token, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		var existing ghContentItem
		if json.Unmarshal(respBody, &existing) == nil {
			existingSHA = existing.SHA
		}
	}

	putBody := map[string]any{
		"message": "sbox-panel encrypted backup: " + filename,
		"content": base64.StdEncoding.EncodeToString(payload),
		"branch":  cfg.Branch,
	}
	if existingSHA != "" {
		putBody["sha"] = existingSHA
	}

	status, respBody, err = doGitHubRequest(ctx, client, http.MethodPut, contentsURL, cfg.Token, putBody)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("backup: github repo push failed (HTTP %d): %s", status, truncateErrBody(respBody))
	}

	var putResp struct {
		Content ghContentItem `json:"content"`
	}
	_ = json.Unmarshal(respBody, &putResp)

	entry := &RemoteBackupEntry{
		Name:      filename,
		Path:      fullPath,
		SHA:       putResp.Content.SHA,
		Size:      int64(len(payload)),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if putResp.Content.Name != "" {
		entry.Name = putResp.Content.Name
	}
	if putResp.Content.Path != "" {
		entry.Path = putResp.Content.Path
	}
	if putResp.Content.Size > 0 {
		entry.Size = putResp.Content.Size
	}

	// Prune oldest backups beyond KeepCount.
	if err := pruneGitHubRepoBackups(ctx, client, cfg); err != nil {
		return entry, fmt.Errorf("backup: pushed %q but failed pruning old backups: %w", entry.Path, err)
	}

	return entry, nil
}

func listGitHubRepoBackups(ctx context.Context, client *http.Client, cfg RemoteGitHubConfig) ([]RemoteBackupEntry, error) {
	if !strings.Contains(cfg.Repo, "/") {
		return nil, fmt.Errorf("backup: invalid github repo %q (expected owner/repo)", cfg.Repo)
	}

	dirURL := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s",
		cfg.APIBaseURL, cfg.Repo, escapeGitHubPath(cfg.PathPrefix), url.QueryEscape(cfg.Branch))

	status, respBody, err := doGitHubRequest(ctx, client, http.MethodGet, dirURL, cfg.Token, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return []RemoteBackupEntry{}, nil
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("backup: github repo list failed (HTTP %d): %s", status, truncateErrBody(respBody))
	}

	var items []ghContentItem
	if err := json.Unmarshal(respBody, &items); err != nil {
		return nil, fmt.Errorf("backup: parse github repo contents: %w", err)
	}

	entries := make([]RemoteBackupEntry, 0, len(items))
	for _, it := range items {
		if it.Type != "" && it.Type != "file" {
			continue
		}
		if !isBackupFilename(it.Name) {
			continue
		}
		entries = append(entries, RemoteBackupEntry{
			Name: it.Name,
			Path: it.Path,
			SHA:  it.SHA,
			Size: it.Size,
		})
	}

	sortEntriesNewestFirst(entries)
	return entries, nil
}

func pruneGitHubRepoBackups(ctx context.Context, client *http.Client, cfg RemoteGitHubConfig) error {
	entries, err := listGitHubRepoBackups(ctx, client, cfg)
	if err != nil {
		return err
	}
	if len(entries) <= cfg.KeepCount {
		return nil
	}

	for _, old := range entries[cfg.KeepCount:] {
		if old.SHA == "" {
			continue
		}
		delURL := fmt.Sprintf("%s/repos/%s/contents/%s", cfg.APIBaseURL, cfg.Repo, escapeGitHubPath(old.Path))
		delBody := map[string]any{
			"message": "prune old sbox-panel backup: " + old.Name,
			"sha":     old.SHA,
			"branch":  cfg.Branch,
		}
		status, respBody, err := doGitHubRequest(ctx, client, http.MethodDelete, delURL, cfg.Token, delBody)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("delete %q failed (HTTP %d): %s", old.Path, status, truncateErrBody(respBody))
		}
	}
	return nil
}

func fetchGitHubRepoBackup(ctx context.Context, client *http.Client, entryPath string, cfg RemoteGitHubConfig) ([]byte, error) {
	if !strings.Contains(cfg.Repo, "/") {
		return nil, fmt.Errorf("backup: invalid github repo %q (expected owner/repo)", cfg.Repo)
	}

	reqURL := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s",
		cfg.APIBaseURL, cfg.Repo, escapeGitHubPath(entryPath), url.QueryEscape(cfg.Branch))

	status, respBody, err := doGitHubRequest(ctx, client, http.MethodGet, reqURL, cfg.Token, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("backup: github repo fetch %q failed (HTTP %d): %s", entryPath, status, truncateErrBody(respBody))
	}

	var item ghContentItem
	if err := json.Unmarshal(respBody, &item); err != nil {
		return nil, fmt.Errorf("backup: parse github repo file %q: %w", entryPath, err)
	}

	if strings.EqualFold(item.Encoding, "base64") && item.Content != "" {
		raw, err := decodeBase64Clean(item.Content)
		if err != nil {
			return nil, fmt.Errorf("backup: decode repo file base64: %w", err)
		}
		return unwrapOptionalB64(entryPath, raw), nil
	}
	if item.DownloadURL != "" {
		dlStatus, dlBody, err := doGitHubRequest(ctx, client, http.MethodGet, item.DownloadURL, cfg.Token, nil)
		if err != nil {
			return nil, err
		}
		if dlStatus < 200 || dlStatus >= 300 {
			return nil, fmt.Errorf("backup: download repo file %q failed (HTTP %d)", entryPath, dlStatus)
		}
		return unwrapOptionalB64(entryPath, dlBody), nil
	}

	return nil, fmt.Errorf("backup: github repo file %q returned empty content", entryPath)
}

// --- GitHub Gist Implementation ---

type ghGistFile struct {
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	RawURL    string `json:"raw_url"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type ghGistResponse struct {
	ID        string                `json:"id"`
	UpdatedAt string                `json:"updated_at"`
	Files     map[string]ghGistFile `json:"files"`
}

func pushGitHubGistBackup(ctx context.Context, client *http.Client, payload []byte, filename string, cfg RemoteGitHubConfig) (*RemoteBackupEntry, error) {
	if cfg.GistID == "" {
		return nil, errors.New("backup: gist_id is required for github_gist backup")
	}

	gistFilename := ensureGistBackupName(filename)
	gistURL := fmt.Sprintf("%s/gists/%s", cfg.APIBaseURL, url.PathEscape(cfg.GistID))

	// Fetch existing gist files to prune old backups exceeding KeepCount.
	existingGist, err := getGist(ctx, client, gistURL, cfg.Token)
	if err != nil {
		return nil, err
	}

	encoded := base64.StdEncoding.EncodeToString(payload)
	patchFiles := map[string]any{
		gistFilename: map[string]any{
			"content": encoded,
		},
	}

	// Collect all backup filenames (existing + new), sort newest-first, delete excess.
	nameSet := make(map[string]struct{}, len(existingGist.Files)+1)
	for name := range existingGist.Files {
		if isBackupFilename(name) {
			nameSet[name] = struct{}{}
		}
	}
	nameSet[gistFilename] = struct{}{}

	allNames := make([]string, 0, len(nameSet))
	for name := range nameSet {
		allNames = append(allNames, name)
	}
	sort.Slice(allNames, func(i, j int) bool {
		return allNames[i] > allNames[j]
	})

	if len(allNames) > cfg.KeepCount {
		for _, oldName := range allNames[cfg.KeepCount:] {
			if oldName == gistFilename {
				continue
			}
			patchFiles[oldName] = nil
		}
	}

	status, respBody, err := doGitHubRequest(ctx, client, http.MethodPatch, gistURL, cfg.Token, map[string]any{
		"files": patchFiles,
	})
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("backup: github gist push failed (HTTP %d): %s", status, truncateErrBody(respBody))
	}

	return &RemoteBackupEntry{
		Name:      gistFilename,
		Path:      gistFilename,
		Size:      int64(len(payload)),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func listGitHubGistBackups(ctx context.Context, client *http.Client, cfg RemoteGitHubConfig) ([]RemoteBackupEntry, error) {
	if cfg.GistID == "" {
		return nil, errors.New("backup: gist_id is required for github_gist backup")
	}

	gistURL := fmt.Sprintf("%s/gists/%s", cfg.APIBaseURL, url.PathEscape(cfg.GistID))
	gist, err := getGist(ctx, client, gistURL, cfg.Token)
	if err != nil {
		return nil, err
	}

	entries := make([]RemoteBackupEntry, 0, len(gist.Files))
	for name, f := range gist.Files {
		if !isBackupFilename(name) {
			continue
		}
		fname := f.Filename
		if fname == "" {
			fname = name
		}
		entries = append(entries, RemoteBackupEntry{
			Name:      fname,
			Path:      fname,
			Size:      f.Size,
			CreatedAt: gist.UpdatedAt,
		})
	}

	sortEntriesNewestFirst(entries)
	return entries, nil
}

func fetchGitHubGistBackup(ctx context.Context, client *http.Client, entryPath string, cfg RemoteGitHubConfig) ([]byte, error) {
	if cfg.GistID == "" {
		return nil, errors.New("backup: gist_id is required for github_gist backup")
	}

	gistURL := fmt.Sprintf("%s/gists/%s", cfg.APIBaseURL, url.PathEscape(cfg.GistID))
	gist, err := getGist(ctx, client, gistURL, cfg.Token)
	if err != nil {
		return nil, err
	}

	f, ok := gist.Files[entryPath]
	if !ok {
		f, ok = gist.Files[path.Base(entryPath)]
	}
	if !ok {
		return nil, fmt.Errorf("backup: gist file %q not found", entryPath)
	}

	content := f.Content
	if (f.Truncated || content == "") && f.RawURL != "" {
		status, rawBytes, err := doGitHubRequest(ctx, client, http.MethodGet, f.RawURL, cfg.Token, nil)
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("backup: fetch gist raw file %q failed (HTTP %d)", entryPath, status)
		}
		content = string(rawBytes)
	}

	decoded, err := decodeBase64Clean(content)
	if err != nil {
		return nil, fmt.Errorf("backup: decode gist backup %q: %w", entryPath, err)
	}
	return decoded, nil
}

func getGist(ctx context.Context, client *http.Client, gistURL, token string) (*ghGistResponse, error) {
	status, respBody, err := doGitHubRequest(ctx, client, http.MethodGet, gistURL, token, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("backup: fetch gist failed (HTTP %d): %s", status, truncateErrBody(respBody))
	}
	var gist ghGistResponse
	if err := json.Unmarshal(respBody, &gist); err != nil {
		return nil, fmt.Errorf("backup: parse gist response: %w", err)
	}
	return &gist, nil
}

func ensureGistBackupName(filename string) string {
	low := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(low, ".sboxbak.b64"):
		return filename
	case strings.HasSuffix(low, ".sboxbak"):
		return filename + ".b64"
	case strings.HasSuffix(low, ".b64"):
		return filename
	default:
		return filename + ".sboxbak.b64"
	}
}

func isBackupFilename(name string) bool {
	low := strings.ToLower(strings.TrimSpace(name))
	return strings.HasSuffix(low, ".sboxbak") ||
		strings.HasSuffix(low, ".sboxbak.b64") ||
		strings.HasSuffix(low, ".tar.gz") ||
		strings.HasSuffix(low, ".tar.gz.b64")
}

func sortEntriesNewestFirst(entries []RemoteBackupEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name > entries[j].Name
	})
}

func decodeBase64Clean(s string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	return base64.StdEncoding.DecodeString(cleaned)
}

func unwrapOptionalB64(entryPath string, raw []byte) []byte {
	if !strings.HasSuffix(strings.ToLower(entryPath), ".b64") {
		return raw
	}
	if IsEncrypted(raw) || (len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b) {
		return raw
	}
	if decoded, err := decodeBase64Clean(string(raw)); err == nil && len(decoded) > 0 {
		return decoded
	}
	return raw
}

func truncateErrBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 256 {
		return s[:256] + "..."
	}
	return s
}
