package coremgr

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultReleasesAPIURL      = "https://api.github.com/repos/SagerNet/sing-box/releases?per_page=30"
	defaultHealthProbeTimeout  = 5 * time.Second
	defaultHealthProbeInterval = 250 * time.Millisecond
	defaultStopTimeout         = 5 * time.Second
	defaultCheckTimeout        = 15 * time.Second
	defaultVersionTimeout      = 10 * time.Second
)

var (
	errNoActiveProcess   = errors.New("core process is not running")
	errReloadUnsupported = errors.New("SIGHUP reload not supported on this platform")
	ansiEscapeRegexp     = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
)

// Status describes the current runtime and installation state of the managed sing-box core.
type Status struct {
	Running      bool      `json:"running"`
	PID          int       `json:"pid"`
	BinaryPath   string    `json:"binary_path"`
	BinaryExists bool      `json:"binary_exists"`
	Version      string    `json:"version"`
	HasBackup    bool      `json:"has_backup"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	UptimeSec    int64     `json:"uptime_sec"`
	ClashAPIAddr string    `json:"clash_api_addr"`
	LastError    string    `json:"last_error,omitempty"`
	Upgrading    bool      `json:"upgrading"`
}

// ReleaseInfo describes a GitHub release asset for SagerNet/sing-box matching the host platform.
type ReleaseInfo struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	AssetName   string    `json:"asset_name"`
	DownloadURL string    `json:"download_url"`
	Size        int64     `json:"size"`
}

// UpgradeOptions specifies parameters for an online release upgrade or downgrade.
type UpgradeOptions struct {
	TagName     string `json:"tag_name"`
	DownloadURL string `json:"download_url,omitempty"`
	ProxyURL    string `json:"proxy_url,omitempty"`
}

// UpgradeResult reports the outcome of an upgrade or manual binary upload.
type UpgradeResult struct {
	Success     bool   `json:"success"`
	RolledBack  bool   `json:"rolled_back"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	Message     string `json:"message"`
	CheckOutput string `json:"check_output,omitempty"`
}

// Options configures a core Manager instance.
type Options struct {
	DataDir      string
	ClashAPIAddr string
	ClashSecret  string
	OnLogLine    func(level, line string)
}

// Manager supervises an independent sing-box child process and orchestrates atomic upgrades.
type Manager struct {
	dataDir     string
	binDir      string
	binaryPath  string
	backupPath  string
	configPath  string
	clashAddr   string
	clashSecret string
	onLogLine   func(level, line string)

	// Configurable endpoints/timings (overridable in tests).
	releasesAPIURL      string
	httpClient          *http.Client
	healthProbeTimeout  time.Duration
	healthProbeInterval time.Duration
	stopTimeout         time.Duration
	targetGOOS          string
	targetGOARCH        string

	// opMu serializes lifecycle and upgrade mutations.
	opMu sync.Mutex

	// mu guards status and active process state; never held across blocking I/O.
	mu        sync.RWMutex
	cmd       *exec.Cmd
	running   bool
	pid       int
	startedAt time.Time
	version   string
	lastError string
	upgrading bool
	stopping  bool
	waitDone  chan struct{}
}

// NewManager creates a Manager configured under opts.DataDir.
// If ClashAPIAddr or ClashSecret are empty, a free loopback port and 32-char hex secret are generated.
func NewManager(opts Options) *Manager {
	dataDir := strings.TrimSpace(opts.DataDir)
	if dataDir == "" {
		dataDir = "./data"
	}
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}

	binDir := filepath.Join(dataDir, "bin")
	binName := platformBinaryName(runtime.GOOS)
	binaryPath := filepath.Join(binDir, binName)
	backupPath := filepath.Join(binDir, "sing-box.bak")
	configPath := filepath.Join(dataDir, "config.json")

	clashAddr := strings.TrimSpace(opts.ClashAPIAddr)
	if clashAddr == "" {
		clashAddr = pickFreeLoopbackAddr()
	}
	clashSecret := strings.TrimSpace(opts.ClashSecret)
	if clashSecret == "" {
		clashSecret = randomHexSecret(16)
	}

	m := &Manager{
		dataDir:             dataDir,
		binDir:              binDir,
		binaryPath:          binaryPath,
		backupPath:          backupPath,
		configPath:          configPath,
		clashAddr:           clashAddr,
		clashSecret:         clashSecret,
		onLogLine:           opts.OnLogLine,
		releasesAPIURL:      defaultReleasesAPIURL,
		httpClient:          &http.Client{Timeout: 60 * time.Second},
		healthProbeTimeout:  defaultHealthProbeTimeout,
		healthProbeInterval: defaultHealthProbeInterval,
		stopTimeout:         defaultStopTimeout,
		targetGOOS:          runtime.GOOS,
		targetGOARCH:        runtime.GOARCH,
	}

	if fileExists(binaryPath) {
		if ver, err := m.DetectBinaryVersion(binaryPath); err == nil {
			m.version = ver
		}
	}

	return m
}

// ClashAPIConfig returns the configured loopback Clash API listen address and Bearer secret.
func (m *Manager) ClashAPIConfig() (addr, secret string) {
	return m.clashAddr, m.clashSecret
}

// Status returns a point-in-time snapshot of the core supervisor status without blocking upgrades.
func (m *Manager) Status() Status {
	m.mu.RLock()
	running := m.running
	pid := m.pid
	startedAt := m.startedAt
	version := m.version
	lastError := m.lastError
	upgrading := m.upgrading
	m.mu.RUnlock()

	var uptimeSec int64
	if running && !startedAt.IsZero() {
		if sec := int64(time.Since(startedAt).Seconds()); sec > 0 {
			uptimeSec = sec
		}
	}

	return Status{
		Running:      running,
		PID:          pid,
		BinaryPath:   m.binaryPath,
		BinaryExists: fileExists(m.binaryPath),
		Version:      version,
		HasBackup:    fileExists(m.backupPath),
		StartedAt:    startedAt,
		UptimeSec:    uptimeSec,
		ClashAPIAddr: m.clashAddr,
		LastError:    lastError,
		Upgrading:    upgrading,
	}
}

// EnsureConfig atomically writes the sing-box configuration JSON to <DataDir>/config.json.
func (m *Manager) EnsureConfig(cfg []byte) error {
	if err := os.MkdirAll(m.dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	return writeFileAtomic(m.configPath, cfg, 0o600)
}

// CheckConfig validates configPath against binaryPath using `sing-box check -c <configPath>`.
func (m *Manager) CheckConfig(binaryPath, configPath string) (string, error) {
	if strings.TrimSpace(binaryPath) == "" {
		binaryPath = m.binaryPath
	}
	if strings.TrimSpace(configPath) == "" {
		configPath = m.configPath
	}
	if !fileExists(binaryPath) {
		return "", fmt.Errorf("sing-box binary not found at %s", binaryPath)
	}
	if !fileExists(configPath) {
		return "", fmt.Errorf("config file not found at %s", configPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultCheckTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "check", "-c", configPath)
	cmd.Dir = m.dataDir
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(stripANSI(string(out)))
	if err != nil {
		if output != "" {
			return output, fmt.Errorf("config check failed: %s (%w)", output, err)
		}
		return output, fmt.Errorf("config check failed: %w", err)
	}
	return output, nil
}

// DetectBinaryVersion executes `<binaryPath> version` and parses the reported version string.
func (m *Manager) DetectBinaryVersion(binaryPath string) (string, error) {
	if strings.TrimSpace(binaryPath) == "" {
		binaryPath = m.binaryPath
	}
	if !fileExists(binaryPath) {
		return "", fmt.Errorf("binary not found: %s", binaryPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultVersionTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "version")
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(stripANSI(string(out)))
	if err != nil {
		return "", fmt.Errorf("detect version failed (%s): %w", output, err)
	}
	ver := parseVersionOutput(output)
	if ver == "" {
		return "", fmt.Errorf("unable to parse version output: %q", output)
	}
	return ver, nil
}

// Start launches the supervised sing-box process if not already running.
func (m *Manager) Start(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.startLocked(ctx)
}

func (m *Manager) startLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.isRunning() {
		return nil
	}
	if !fileExists(m.binaryPath) {
		err := fmt.Errorf("sing-box binary not found at %s", m.binaryPath)
		m.setLastError(err.Error())
		return err
	}
	if !fileExists(m.configPath) {
		err := fmt.Errorf("sing-box config not found at %s", m.configPath)
		m.setLastError(err.Error())
		return err
	}
	if err := os.MkdirAll(m.dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	if ver, err := m.DetectBinaryVersion(m.binaryPath); err == nil {
		m.mu.Lock()
		m.version = ver
		m.mu.Unlock()
	}

	// Do NOT bind the long-lived child process to the caller's HTTP request context.
	cmd := exec.Command(m.binaryPath, "run", "-c", m.configPath)
	cmd.Dir = m.dataDir

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("attach stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("attach stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		m.setLastError(err.Error())
		return fmt.Errorf("start sing-box: %w", err)
	}

	waitDone := make(chan struct{})
	now := time.Now().UTC()

	m.mu.Lock()
	m.cmd = cmd
	m.running = true
	m.pid = cmd.Process.Pid
	m.startedAt = now
	m.lastError = ""
	m.stopping = false
	m.waitDone = waitDone
	m.mu.Unlock()

	m.emitLog("info", fmt.Sprintf("sing-box started (pid=%d, clash_api=%s)", cmd.Process.Pid, m.clashAddr))

	var pipeWG sync.WaitGroup
	pipeWG.Add(2)
	go func() {
		defer pipeWG.Done()
		m.streamPipe(stdoutPipe, "info")
	}()
	go func() {
		defer pipeWG.Done()
		m.streamPipe(stderrPipe, "warn")
	}()

	go func(c *exec.Cmd, done chan struct{}) {
		defer close(done)
		// Drain pipes before Wait per os/exec contract.
		pipeWG.Wait()
		waitErr := c.Wait()

		m.mu.Lock()
		isCurrent := m.cmd == c
		wasStopping := m.stopping
		if isCurrent {
			m.running = false
			m.pid = 0
			m.cmd = nil
			m.startedAt = time.Time{}
			m.stopping = false
			if !wasStopping {
				if waitErr != nil {
					m.lastError = waitErr.Error()
				} else {
					m.lastError = "process exited unexpectedly"
				}
			}
		}
		m.mu.Unlock()

		if isCurrent && !wasStopping {
			if waitErr != nil {
				m.emitLog("error", fmt.Sprintf("sing-box exited unexpectedly: %v", waitErr))
			} else {
				m.emitLog("warn", "sing-box exited unexpectedly with code 0")
			}
		}
	}(cmd, waitDone)

	return nil
}

// Stop gracefully terminates the supervised sing-box process and waits for exit.
func (m *Manager) Stop() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.stopLocked()
}

func (m *Manager) stopLocked() error {
	m.mu.Lock()
	if !m.running || m.cmd == nil || m.cmd.Process == nil {
		m.mu.Unlock()
		return nil
	}
	m.stopping = true
	proc := m.cmd.Process
	waitDone := m.waitDone
	pid := m.pid
	m.mu.Unlock()

	if err := terminateProcess(proc); err != nil {
		// Process may already have exited; check waitDone below.
		_ = err
	}

	timeout := m.stopTimeout
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}

	select {
	case <-waitDone:
		m.emitLog("info", fmt.Sprintf("sing-box stopped (pid=%d)", pid))
		return nil
	case <-time.After(timeout):
		_ = proc.Kill()
		select {
		case <-waitDone:
			m.emitLog("warn", fmt.Sprintf("sing-box force-killed after %s (pid=%d)", timeout, pid))
			return nil
		case <-time.After(2 * time.Second):
			return fmt.Errorf("timed out waiting for sing-box (pid=%d) to exit", pid)
		}
	}
}

// Restart stops (if running) and starts the managed sing-box core.
func (m *Manager) Restart(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.stopLocked(); err != nil {
		return err
	}
	return m.startLocked(ctx)
}

// Reload validates and applies newConfig (when non-empty), then sends SIGHUP on Unix
// or falls back to a graceful restart on Windows.
func (m *Manager) Reload(ctx context.Context, newConfig []byte) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	if len(newConfig) > 0 {
		if err := os.MkdirAll(m.dataDir, 0o755); err != nil {
			return fmt.Errorf("create data dir: %w", err)
		}
		tmpCfg := filepath.Join(m.dataDir, fmt.Sprintf("config.json.reload.%d.tmp", time.Now().UnixNano()))
		if err := os.WriteFile(tmpCfg, newConfig, 0o600); err != nil {
			return fmt.Errorf("write staging config: %w", err)
		}
		defer os.Remove(tmpCfg)

		if fileExists(m.binaryPath) {
			if _, err := m.CheckConfig(m.binaryPath, tmpCfg); err != nil {
				return err
			}
		}

		if err := replaceFile(tmpCfg, m.configPath); err != nil {
			return fmt.Errorf("apply reloaded config: %w", err)
		}
	} else if fileExists(m.binaryPath) && fileExists(m.configPath) {
		if _, err := m.CheckConfig(m.binaryPath, m.configPath); err != nil {
			return err
		}
	}

	m.mu.RLock()
	running := m.running
	var proc *os.Process
	if m.cmd != nil {
		proc = m.cmd.Process
	}
	m.mu.RUnlock()

	if !running || proc == nil {
		return nil
	}

	if supportsSIGHUP {
		if err := sendReloadSignal(proc); err == nil {
			m.emitLog("info", "sent SIGHUP reload signal to sing-box")
			return nil
		} else {
			m.emitLog("warn", fmt.Sprintf("SIGHUP reload failed (%v), falling back to graceful restart", err))
		}
	}

	if err := m.stopLocked(); err != nil {
		return err
	}
	return m.startLocked(ctx)
}

func (m *Manager) isRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

func (m *Manager) currentWaitDone() <-chan struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.waitDone
}

func (m *Manager) setLastError(msg string) {
	m.mu.Lock()
	m.lastError = msg
	m.mu.Unlock()
}

func (m *Manager) setUpgrading(v bool) {
	m.mu.Lock()
	m.upgrading = v
	m.mu.Unlock()
}

func (m *Manager) emitLog(level, line string) {
	if m.onLogLine != nil {
		m.onLogLine(level, line)
	}
}

func (m *Manager) streamPipe(r io.Reader, defaultLevel string) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		raw := strings.TrimSpace(stripANSI(scanner.Text()))
		if raw == "" {
			continue
		}
		level := detectLogLevel(raw, defaultLevel)
		m.emitLog(level, raw)
	}
}

func stripANSI(s string) string {
	return ansiEscapeRegexp.ReplaceAllString(s, "")
}

func detectLogLevel(line, fallback string) string {
	upper := strings.ToUpper(line)
	switch {
	case strings.Contains(upper, " FATAL ") || strings.HasPrefix(upper, "FATAL") || strings.Contains(upper, " PANIC "):
		return "fatal"
	case strings.Contains(upper, " ERROR ") || strings.HasPrefix(upper, "ERROR"):
		return "error"
	case strings.Contains(upper, " WARN ") || strings.Contains(upper, " WARNING ") || strings.HasPrefix(upper, "WARN"):
		return "warn"
	case strings.Contains(upper, " DEBUG ") || strings.HasPrefix(upper, "DEBUG"):
		return "debug"
	case strings.Contains(upper, " TRACE ") || strings.HasPrefix(upper, "TRACE"):
		return "trace"
	case strings.Contains(upper, " INFO ") || strings.HasPrefix(upper, "INFO"):
		return "info"
	default:
		return fallback
	}
}

func parseVersionOutput(out string) string {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if idx := strings.Index(lower, "sing-box version "); idx >= 0 {
			rest := strings.TrimSpace(trimmed[idx+len("sing-box version "):])
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				return fields[0]
			}
		}
		// Fallback: first token of first non-empty line.
		fields := strings.Fields(trimmed)
		if len(fields) > 0 {
			return fields[len(fields)-1]
		}
	}
	return ""
}

func platformBinaryName(goos string) string {
	if goos == "windows" {
		return "sing-box.exe"
	}
	return "sing-box"
}

func pickFreeLoopbackAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "127.0.0.1:19090"
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func randomHexSecret(numBytes int) string {
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func writeFileAtomic(dst string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
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

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := replaceFile(tmpName, dst); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func copyFileAtomic(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".copy-*")
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

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := replaceFile(tmpName, dst); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func replaceFile(src, dst string) error {
	var lastErr error
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 12
	}
	for i := 0; i < attempts; i++ {
		if err := os.Rename(src, dst); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if runtime.GOOS == "windows" {
			// On Windows, a just-exited process may briefly hold the executable mapping.
			_ = os.Remove(dst)
			if err := os.Rename(src, dst); err == nil {
				return nil
			} else {
				lastErr = err
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return lastErr
}
