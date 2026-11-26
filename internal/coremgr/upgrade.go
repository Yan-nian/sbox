package coremgr

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxBinaryBytes = 256 << 20 // 256 MiB safety cap

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// ListReleases fetches SagerNet/sing-box GitHub releases matching the host OS/architecture.
func (m *Manager) ListReleases(ctx context.Context, includePrerelease bool, proxyURL string) ([]ReleaseInfo, error) {
	client, reqURL, err := m.clientAndURLForProxy(m.releasesAPIURL, proxyURL, false)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build releases request: %w", err)
	}
	req.Header.Set("User-Agent", "sbox-panel")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch github releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github releases API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rawReleases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rawReleases); err != nil {
		return nil, fmt.Errorf("decode github releases: %w", err)
	}

	goos := m.targetGOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := m.targetGOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	results := make([]ReleaseInfo, 0, len(rawReleases))
	for _, rel := range rawReleases {
		if rel.Draft {
			continue
		}
		if !includePrerelease && rel.Prerelease {
			continue
		}
		asset, ok := selectReleaseAsset(rel.Assets, goos, goarch)
		if !ok {
			continue
		}
		name := rel.Name
		if name == "" {
			name = rel.TagName
		}
		results = append(results, ReleaseInfo{
			TagName:     rel.TagName,
			Name:        name,
			Prerelease:  rel.Prerelease,
			PublishedAt: rel.PublishedAt,
			AssetName:   asset.Name,
			DownloadURL: asset.BrowserDownloadURL,
			Size:        asset.Size,
		})
	}
	return results, nil
}

// Upgrade executes the 4-step atomic upgrade workflow:
// 1. Download & extract new binary to staging path.
// 2. Pre-check with `sing-box check -c config.json` (zero downtime on failure).
// 3. Backup active binary to sing-box.bak, stop old process, atomic swap, start new process.
// 4. Probe Clash API `/version` for 5 seconds; automatically roll back on crash or timeout.
func (m *Manager) Upgrade(ctx context.Context, opts UpgradeOptions) UpgradeResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.setUpgrading(true)
	defer m.setUpgrading(false)

	fromVersion := m.currentInstalledVersion()
	downloadURL := strings.TrimSpace(opts.DownloadURL)
	assetName := ""

	if downloadURL == "" {
		tag := strings.TrimSpace(opts.TagName)
		if tag == "" {
			return UpgradeResult{
				Success:     false,
				FromVersion: fromVersion,
				Message:     "either tag_name or download_url is required",
			}
		}
		releases, err := m.ListReleases(ctx, true, opts.ProxyURL)
		if err != nil {
			return UpgradeResult{
				Success:     false,
				FromVersion: fromVersion,
				Message:     fmt.Sprintf("failed to list releases: %v", err),
			}
		}
		for _, r := range releases {
			if r.TagName == tag || strings.TrimPrefix(r.TagName, "v") == strings.TrimPrefix(tag, "v") {
				downloadURL = r.DownloadURL
				assetName = r.AssetName
				break
			}
		}
		if downloadURL == "" {
			return UpgradeResult{
				Success:     false,
				FromVersion: fromVersion,
				Message:     fmt.Sprintf("release %q not found for %s/%s", tag, m.targetGOOS, m.targetGOARCH),
			}
		}
	} else {
		if u, err := url.Parse(downloadURL); err == nil {
			assetName = filepath.Base(u.Path)
		}
	}

	if err := os.MkdirAll(m.binDir, 0o755); err != nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     fmt.Sprintf("create bin dir: %v", err),
		}
	}

	m.emitLog("info", fmt.Sprintf("downloading sing-box release %s from %s", opts.TagName, downloadURL))

	tmpDownload, err := m.downloadToTempFile(ctx, downloadURL, opts.ProxyURL)
	if err != nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     fmt.Sprintf("download failed: %v", err),
		}
	}
	defer os.Remove(tmpDownload)

	stagingPath := m.stagingBinaryPath()
	_ = os.Remove(stagingPath)
	defer os.Remove(stagingPath)

	if err := extractOrCopyBinary(tmpDownload, assetName, stagingPath); err != nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     fmt.Sprintf("extract binary failed: %v", err),
		}
	}

	return m.installStagedBinaryLocked(ctx, stagingPath, fromVersion)
}

// UploadBinary installs a user-uploaded sing-box binary or archive (.tar.gz/.zip/raw)
// using the exact same 4-step pre-check, backup, swap, and health-probe rollback pipeline.
func (m *Manager) UploadBinary(ctx context.Context, r io.Reader, filename string) UpgradeResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.setUpgrading(true)
	defer m.setUpgrading(false)

	fromVersion := m.currentInstalledVersion()
	if r == nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     "empty upload stream",
		}
	}

	if err := os.MkdirAll(m.binDir, 0o755); err != nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     fmt.Sprintf("create bin dir: %v", err),
		}
	}

	tmpUpload, err := os.CreateTemp(m.binDir, ".upload-*")
	if err != nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     fmt.Sprintf("create temp upload file: %v", err),
		}
	}
	tmpPath := tmpUpload.Name()
	defer os.Remove(tmpPath)

	n, err := io.Copy(tmpUpload, io.LimitReader(r, maxBinaryBytes+1))
	closeErr := tmpUpload.Close()
	if err != nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     fmt.Sprintf("read upload stream: %v", err),
		}
	}
	if closeErr != nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     fmt.Sprintf("finalize upload file: %v", closeErr),
		}
	}
	if n == 0 {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     "uploaded file is empty",
		}
	}
	if n > maxBinaryBytes {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     "uploaded file exceeds 256 MiB limit",
		}
	}

	stagingPath := m.stagingBinaryPath()
	_ = os.Remove(stagingPath)
	defer os.Remove(stagingPath)

	if err := extractOrCopyBinary(tmpPath, filename, stagingPath); err != nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     fmt.Sprintf("extract uploaded binary: %v", err),
		}
	}

	return m.installStagedBinaryLocked(ctx, stagingPath, fromVersion)
}

// Rollback restores sing-box.bak over the active binary. If the core was running, it restarts it.
func (m *Manager) Rollback(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	if !fileExists(m.backupPath) {
		return errors.New("no backup binary (sing-box.bak) available to roll back")
	}

	wasRunning := m.isRunning()
	if err := m.stopLocked(); err != nil {
		return fmt.Errorf("stop current core before rollback: %w", err)
	}

	if err := copyFileAtomic(m.backupPath, m.binaryPath, 0o755); err != nil {
		return fmt.Errorf("restore backup binary: %w", err)
	}

	if ver, err := m.DetectBinaryVersion(m.binaryPath); err == nil {
		m.mu.Lock()
		m.version = ver
		m.lastError = ""
		m.mu.Unlock()
	}

	m.emitLog("warn", "rolled back sing-box binary from sing-box.bak")

	if wasRunning {
		if err := m.startLocked(ctx); err != nil {
			return fmt.Errorf("restart rolled-back core: %w", err)
		}
	}
	return nil
}

func (m *Manager) installStagedBinaryLocked(ctx context.Context, stagingPath, fromVersion string) UpgradeResult {
	// Step 2a: Verify staged binary is executable and detect version.
	toVersion, err := m.DetectBinaryVersion(stagingPath)
	if err != nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			Message:     fmt.Sprintf("staged binary version check failed: %v", err),
		}
	}

	// Step 2b: Pre-check current config against staged binary before touching active core.
	var checkOut string
	hasConfig := fileExists(m.configPath)
	if hasConfig {
		out, checkErr := m.CheckConfig(stagingPath, m.configPath)
		checkOut = out
		if checkErr != nil {
			m.emitLog("error", fmt.Sprintf("upgrade aborted: sing-box check failed for %s: %s", toVersion, out))
			return UpgradeResult{
				Success:     false,
				RolledBack:  false,
				FromVersion: fromVersion,
				ToVersion:   toVersion,
				Message:     fmt.Sprintf("config compatibility pre-check failed: %v", checkErr),
				CheckOutput: checkOut,
			}
		}
	}

	// Step 3: Backup existing binary -> stop old process -> atomic swap.
	hadBinary := fileExists(m.binaryPath)
	wasRunning := m.isRunning()

	if err := m.stopLocked(); err != nil {
		return UpgradeResult{
			Success:     false,
			FromVersion: fromVersion,
			ToVersion:   toVersion,
			Message:     fmt.Sprintf("failed to stop running core: %v", err),
			CheckOutput: checkOut,
		}
	}

	if hadBinary {
		if err := copyFileAtomic(m.binaryPath, m.backupPath, 0o755); err != nil {
			if wasRunning {
				_ = m.startLocked(ctx)
			}
			return UpgradeResult{
				Success:     false,
				FromVersion: fromVersion,
				ToVersion:   toVersion,
				Message:     fmt.Sprintf("failed to create sing-box.bak backup: %v", err),
				CheckOutput: checkOut,
			}
		}
	}

	if err := replaceFile(stagingPath, m.binaryPath); err != nil {
		rolledBack := false
		if hadBinary && fileExists(m.backupPath) {
			if rbErr := copyFileAtomic(m.backupPath, m.binaryPath, 0o755); rbErr == nil {
				rolledBack = true
				if wasRunning {
					_ = m.startLocked(ctx)
				}
			}
		}
		return UpgradeResult{
			Success:     false,
			RolledBack:  rolledBack,
			FromVersion: fromVersion,
			ToVersion:   toVersion,
			Message:     fmt.Sprintf("failed to swap sing-box binary: %v", err),
			CheckOutput: checkOut,
		}
	}

	if !hasConfig {
		m.mu.Lock()
		m.version = toVersion
		m.lastError = ""
		m.mu.Unlock()
		return UpgradeResult{
			Success:     true,
			FromVersion: fromVersion,
			ToVersion:   toVersion,
			Message:     fmt.Sprintf("installed sing-box %s (config not initialized yet)", toVersion),
		}
	}

	// Start new binary and execute Step 4: 5s Clash API /version health probe.
	startErr := m.startLocked(ctx)
	var probeErr error
	if startErr != nil {
		probeErr = startErr
	} else {
		probeErr = m.probeClashHealth(ctx, m.currentWaitDone())
	}

	if probeErr != nil {
		m.emitLog("error", fmt.Sprintf("new sing-box %s failed health probe: %v", toVersion, probeErr))
		_ = m.stopLocked()

		rolledBack := false
		if hadBinary && fileExists(m.backupPath) {
			if rbErr := copyFileAtomic(m.backupPath, m.binaryPath, 0o755); rbErr == nil {
				rolledBack = true
				m.mu.Lock()
				m.version = fromVersion
				m.mu.Unlock()
				if wasRunning {
					if restartErr := m.startLocked(ctx); restartErr != nil {
						m.emitLog("error", fmt.Sprintf("failed to restart rolled-back sing-box: %v", restartErr))
					}
				}
				m.emitLog("warn", fmt.Sprintf("automatically rolled back sing-box to %s", fromVersion))
			}
		} else if !hadBinary {
			_ = os.Remove(m.binaryPath)
		}

		m.setLastError(probeErr.Error())
		msg := fmt.Sprintf("health probe failed: %v", probeErr)
		if rolledBack {
			msg = fmt.Sprintf("health probe failed, automatically rolled back to %s: %v", fromVersion, probeErr)
		}
		return UpgradeResult{
			Success:     false,
			RolledBack:  rolledBack,
			FromVersion: fromVersion,
			ToVersion:   toVersion,
			Message:     msg,
			CheckOutput: checkOut,
		}
	}

	m.mu.Lock()
	m.version = toVersion
	m.lastError = ""
	m.mu.Unlock()

	m.emitLog("info", fmt.Sprintf("upgraded sing-box from %s to %s", fromVersion, toVersion))
	return UpgradeResult{
		Success:     true,
		RolledBack:  false,
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		Message:     fmt.Sprintf("upgraded sing-box to %s", toVersion),
		CheckOutput: checkOut,
	}
}

func (m *Manager) probeClashHealth(ctx context.Context, waitDone <-chan struct{}) error {
	timeout := m.healthProbeTimeout
	if timeout <= 0 {
		timeout = defaultHealthProbeTimeout
	}
	interval := m.healthProbeInterval
	if interval <= 0 {
		interval = defaultHealthProbeInterval
	}

	baseURL := normalizeHTTPAddr(m.clashAddr) + "/version"
	// Direct loopback transport (bypass any system HTTP_PROXY env vars).
	client := &http.Client{
		Timeout: 1 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}

	deadline := time.Now().Add(timeout)
	var lastErr error

	for {
		// Check immediate crash first.
		if waitDone != nil {
			select {
			case <-waitDone:
				return errors.New("core process exited before passing Clash API /version probe")
			default:
			}
		}

		reqCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL, nil)
		if err == nil {
			if m.clashSecret != "" {
				req.Header.Set("Authorization", "Bearer "+m.clashSecret)
			}
			resp, doErr := client.Do(req)
			if doErr == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				cancel()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
				lastErr = fmt.Errorf("clash API /version returned HTTP %d", resp.StatusCode)
			} else {
				cancel()
				lastErr = doErr
			}
		} else {
			cancel()
			lastErr = err
		}

		if time.Now().After(deadline) {
			if lastErr == nil {
				lastErr = errors.New("timed out waiting for Clash API /version")
			}
			return fmt.Errorf("clash API /version health probe timed out after %s: %w", timeout, lastErr)
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-waitDone:
			timer.Stop()
			return errors.New("core process exited before passing Clash API /version probe")
		case <-timer.C:
		}
	}
}

func (m *Manager) currentInstalledVersion() string {
	m.mu.RLock()
	ver := m.version
	m.mu.RUnlock()
	if ver == "" && fileExists(m.binaryPath) {
		if detected, err := m.DetectBinaryVersion(m.binaryPath); err == nil {
			return detected
		}
	}
	return ver
}

func (m *Manager) stagingBinaryPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(m.binDir, "sing-box.staging.exe")
	}
	return filepath.Join(m.binDir, "sing-box.staging")
}

func (m *Manager) downloadToTempFile(ctx context.Context, rawURL, proxyURL string) (string, error) {
	client, finalURL, err := m.clientAndURLForProxy(rawURL, proxyURL, true)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, finalURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "sbox-panel")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("download HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	tmp, err := os.CreateTemp(m.binDir, ".download-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()

	n, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, maxBinaryBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", closeErr
	}
	if n == 0 {
		_ = os.Remove(tmpPath)
		return "", errors.New("downloaded file is empty")
	}
	if n > maxBinaryBytes {
		_ = os.Remove(tmpPath)
		return "", errors.New("downloaded file exceeds 256 MiB limit")
	}
	return tmpPath, nil
}

func (m *Manager) clientAndURLForProxy(rawURL, proxyURL string, isDownload bool) (*http.Client, string, error) {
	baseClient := m.httpClient
	if baseClient == nil {
		baseClient = &http.Client{Timeout: 60 * time.Second}
	}

	trimmed := strings.TrimSpace(proxyURL)
	if trimmed == "" {
		return baseClient, rawURL, nil
	}

	// Explicit template form e.g. https://mirror.example.com/{url}
	if strings.Contains(trimmed, "{url}") {
		if isDownload {
			return baseClient, strings.ReplaceAll(trimmed, "{url}", rawURL), nil
		}
		return baseClient, rawURL, nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, "", fmt.Errorf("malformed proxy URL %q", proxyURL)
	}

	scheme := strings.ToLower(parsed.Scheme)
	// Mirror URL prefix heuristic: trailing slash OR https mirror domain without explicit port.
	isMirrorPrefix := false
	if scheme == "http" || scheme == "https" {
		if strings.HasSuffix(trimmed, "/") {
			isMirrorPrefix = true
		} else if scheme == "https" && parsed.Port() == "" && !isLoopbackOrIP(parsed.Hostname()) {
			isMirrorPrefix = true
		}
	}

	if isMirrorPrefix {
		if isDownload {
			return baseClient, strings.TrimRight(trimmed, "/") + "/" + rawURL, nil
		}
		return baseClient, rawURL, nil
	}

	if scheme != "http" && scheme != "https" && scheme != "socks5" && scheme != "socks5h" {
		return nil, "", fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
	}

	var transport *http.Transport
	if baseTr, ok := baseClient.Transport.(*http.Transport); ok && baseTr != nil {
		transport = baseTr.Clone()
	} else {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	transport.Proxy = http.ProxyURL(parsed)

	cloned := *baseClient
	cloned.Transport = transport
	return &cloned, rawURL, nil
}

func isLoopbackOrIP(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	return net.ParseIP(host) != nil
}

func normalizeHTTPAddr(addr string) string {
	a := strings.TrimSpace(addr)
	if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
		return strings.TrimRight(a, "/")
	}
	return "http://" + strings.TrimRight(a, "/")
}

// selectReleaseAsset chooses the best official sing-box archive asset for goos/goarch.
func selectReleaseAsset(assets []githubAsset, goos, goarch string) (githubAsset, bool) {
	arch := normalizeSingBoxArch(goarch)
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}

	exactSuffix := fmt.Sprintf("-%s-%s%s", goos, arch, ext)
	for _, a := range assets {
		if strings.HasSuffix(strings.ToLower(a.Name), exactSuffix) {
			return a, true
		}
	}

	// Fallback: match os + arch + ext while avoiding legacy/fips variants first.
	osArchToken := fmt.Sprintf("-%s-%s", goos, arch)
	var fallback *githubAsset
	for i := range assets {
		nameLower := strings.ToLower(assets[i].Name)
		if !strings.HasSuffix(nameLower, ext) || !strings.Contains(nameLower, osArchToken) {
			continue
		}
		if !strings.Contains(nameLower, "legacy") && !strings.Contains(nameLower, "fips") {
			return assets[i], true
		}
		if fallback == nil {
			fallback = &assets[i]
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return githubAsset{}, false
}

func normalizeSingBoxArch(goarch string) string {
	switch strings.ToLower(goarch) {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	case "arm", "armv7", "armv7l":
		return "armv7"
	case "386":
		return "386"
	default:
		return strings.ToLower(goarch)
	}
}

func extractOrCopyBinary(srcPath, hintFilename, dstPath string) error {
	kind, err := detectArchiveKind(srcPath, hintFilename)
	if err != nil {
		return err
	}

	switch kind {
	case "tar.gz":
		return extractFromTarGz(srcPath, dstPath)
	case "zip":
		return extractFromZip(srcPath, dstPath)
	default:
		return copyFileAtomic(srcPath, dstPath, 0o755)
	}
}

func detectArchiveKind(srcPath, hintFilename string) (string, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var magic [4]byte
	n, _ := io.ReadFull(f, magic[:])

	// Prefer magic bytes over filename extension.
	if n >= 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		return "tar.gz", nil
	}
	if n >= 4 && magic[0] == 0x50 && magic[1] == 0x4b && magic[2] == 0x03 && magic[3] == 0x04 {
		return "zip", nil
	}

	lower := strings.ToLower(strings.TrimSpace(hintFilename))
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return "tar.gz", nil
	case strings.HasSuffix(lower, ".zip"):
		return "zip", nil
	default:
		return "raw", nil
	}
}

func isTargetBinaryName(name string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	return base == "sing-box" || base == "sing-box.exe"
}

func extractFromTarGz(archivePath, dstPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if !isTargetBinaryName(hdr.Name) {
			continue
		}
		return writeReaderToExecutable(tr, dstPath)
	}
	return errors.New("sing-box binary not found inside tar.gz archive")
}

func extractFromZip(archivePath, dstPath string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer zr.Close()

	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if !isTargetBinaryName(entry.Name) {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %s: %w", entry.Name, err)
		}
		writeErr := writeReaderToExecutable(rc, dstPath)
		_ = rc.Close()
		return writeErr
	}
	return errors.New("sing-box binary not found inside zip archive")
}

func writeReaderToExecutable(r io.Reader, dstPath string) error {
	dir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".extract-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	n, err := io.Copy(tmp, io.LimitReader(r, maxBinaryBytes+1))
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if n > maxBinaryBytes {
		_ = tmp.Close()
		return errors.New("extracted binary exceeds 256 MiB limit")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := replaceFile(tmpName, dstPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}
