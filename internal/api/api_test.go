package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/store"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
)

type mockCore struct {
	mu          sync.Mutex
	status      coremgr.Status
	clashAddr   string
	clashSecret string
	lastConfig  []byte
	releases    []coremgr.ReleaseInfo
	checkErr    error
	checkOut    string
	upgradeRes  coremgr.UpgradeResult
	uploadRes   coremgr.UpgradeResult
	rollbackErr error
}

func newMockCore() *mockCore {
	return &mockCore{
		status: coremgr.Status{
			Running:      false,
			BinaryPath:   "/tmp/sing-box",
			BinaryExists: false,
			Version:      "1.12.0",
			HasBackup:    true,
			ClashAPIAddr: "127.0.0.1:9090",
		},
		clashAddr:   "127.0.0.1:9090",
		clashSecret: "test-secret",
		releases: []coremgr.ReleaseInfo{
			{TagName: "v1.12.0", Name: "v1.12.0", Prerelease: false, AssetName: "sing-box-1.12.0-linux-amd64.tar.gz", DownloadURL: "https://example.com/sb.tar.gz", Size: 12345},
		},
		upgradeRes: coremgr.UpgradeResult{Success: true, FromVersion: "1.11.0", ToVersion: "1.12.0", Message: "upgraded"},
		uploadRes:  coremgr.UpgradeResult{Success: true, FromVersion: "1.11.0", ToVersion: "1.12.1", Message: "uploaded"},
	}
}

func (m *mockCore) Status() coremgr.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}
func (m *mockCore) ClashAPIConfig() (string, string) { return m.clashAddr, m.clashSecret }
func (m *mockCore) EnsureConfig(cfg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastConfig = append([]byte(nil), cfg...)
	return nil
}
func (m *mockCore) CheckConfig(_, _ string) (string, error) { return m.checkOut, m.checkErr }
func (m *mockCore) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Running = true
	m.status.PID = 4242
	return nil
}
func (m *mockCore) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Running = false
	m.status.PID = 0
	return nil
}
func (m *mockCore) Restart(ctx context.Context) error {
	_ = m.Stop()
	return m.Start(ctx)
}
func (m *mockCore) Reload(_ context.Context, cfg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastConfig = append([]byte(nil), cfg...)
	return nil
}
func (m *mockCore) ListReleases(_ context.Context, _ bool, _ string) ([]coremgr.ReleaseInfo, error) {
	return m.releases, nil
}
func (m *mockCore) Upgrade(_ context.Context, _ coremgr.UpgradeOptions) coremgr.UpgradeResult {
	return m.upgradeRes
}
func (m *mockCore) UploadBinary(_ context.Context, r io.Reader, _ string) coremgr.UpgradeResult {
	_, _ = io.ReadAll(r)
	return m.uploadRes
}
func (m *mockCore) Rollback(_ context.Context) error             { return m.rollbackErr }
func (m *mockCore) DetectBinaryVersion(_ string) (string, error) { return "1.12.0", nil }

type mockTelemetry struct {
	mu            sync.Mutex
	logs          []telemetry.LogEntry
	conns         []telemetry.ConnectionItem
	closedID      string
	logSubs       []chan telemetry.LogEntry
	proxyGroups   []telemetry.ProxyGroupInfo
	selectedGroup string
	selectedProxy string
}

func newMockTelemetry() *mockTelemetry {
	return &mockTelemetry{
		logs: []telemetry.LogEntry{
			{Time: time.Now().UTC(), Level: "info", Source: "panel", Message: "booted"},
		},
		conns: []telemetry.ConnectionItem{
			{ID: "conn-1", Inbound: "vless-in", User: "alice", Network: "tcp", Destination: "example.com:443", Chains: []string{"direct"}, Upload: 128, Download: 512},
		},
		proxyGroups: []telemetry.ProxyGroupInfo{
			{
				Name:   "airport",
				Type:   "Selector",
				Now:    "airport-jp-1",
				All:    []string{"airport-auto", "airport-jp-1", "airport-sg-1"},
				Delays: map[string]int{"airport-jp-1": 45, "airport-sg-1": 88},
			},
		},
	}
}

func (t *mockTelemetry) PublishLog(level, source, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := telemetry.LogEntry{Time: time.Now().UTC(), Level: level, Source: source, Message: msg}
	t.logs = append(t.logs, e)
	for _, ch := range t.logSubs {
		select {
		case ch <- e:
		default:
		}
	}
}
func (t *mockTelemetry) RecentLogs(limit int) []telemetry.LogEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	if limit > 0 && len(t.logs) > limit {
		return append([]telemetry.LogEntry(nil), t.logs[len(t.logs)-limit:]...)
	}
	return append([]telemetry.LogEntry(nil), t.logs...)
}
func (t *mockTelemetry) SubscribeLogs() (<-chan telemetry.LogEntry, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ch := make(chan telemetry.LogEntry, 16)
	t.logSubs = append(t.logSubs, ch)
	return ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		for i, sub := range t.logSubs {
			if sub == ch {
				t.logSubs = append(t.logSubs[:i], t.logSubs[i+1:]...)
				close(ch)
				break
			}
		}
	}
}
func (t *mockTelemetry) Snapshot() telemetry.Snapshot {
	return telemetry.Snapshot{
		UpSpeed:           1024,
		DownSpeed:         4096,
		MemoryInuse:       16 * 1024 * 1024,
		ActiveConnections: 1,
		CoreConnected:     true,
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
}
func (t *mockTelemetry) SubscribeMetrics() (<-chan telemetry.Snapshot, func()) {
	ch := make(chan telemetry.Snapshot, 1)
	return ch, func() { close(ch) }
}
func (t *mockTelemetry) Connections(_ context.Context) ([]telemetry.ConnectionItem, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]telemetry.ConnectionItem(nil), t.conns...), nil
}
func (t *mockTelemetry) CloseConnection(_ context.Context, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closedID = id
	return nil
}
func (t *mockTelemetry) ListProxyGroups(_ context.Context) ([]telemetry.ProxyGroupInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]telemetry.ProxyGroupInfo(nil), t.proxyGroups...), nil
}
func (t *mockTelemetry) TestProxyDelay(_ context.Context, proxyName, _ string, _ int) (int, error) {
	if proxyName == "airport-sg-1" {
		return 77, nil
	}
	return 42, nil
}
func (t *mockTelemetry) SelectProxy(_ context.Context, groupName, proxyName string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.selectedGroup = groupName
	t.selectedProxy = proxyName
	return nil
}

func newTestServer(t *testing.T) (*Server, *store.Store, *mockCore, *mockTelemetry, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	bp, _, jwtSecret, err := st.EnsureBootstrapDefaults(context.Background(), 18080)
	if err != nil {
		t.Fatalf("bootstrap defaults: %v", err)
	}

	mc := newMockCore()
	mt := newMockTelemetry()
	staticFS := fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte("<!doctype html><html><head><title>Test</title></head><body><div id='root'></div></body></html>")},
		"assets/app-123.js": &fstest.MapFile{Data: []byte("console.log('ok');")},
	}

	rl := auth.NewLoginRateLimiter(3, 5*time.Minute, 10*time.Minute)
	srv, err := NewServer(ServerConfig{
		Store:        st,
		Tokens:       auth.NewTokenManager(jwtSecret, time.Hour),
		RateLimiter:  rl,
		Core:         mc,
		Telemetry:    mt,
		StaticFS:     staticFS,
		DataDir:      dir,
		PanelVersion: "0.1.0-test",
		StartedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv, st, mc, mt, bp
}

func doJSON(t *testing.T, h http.Handler, method, target string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, target, reader)
	req.RemoteAddr = "198.51.100.77:54321"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBasePathProtectionAndSPAInjection(t *testing.T) {
	srv, _, _, _, bp := newTestServer(t)

	// Root and wrong prefixes MUST return clean 404 without leaking panel HTML
	for _, badPath := range []string{"/", "/api/v1/auth/status", "/wrong-prefix/", bp + "extra/"} {
		rec := doJSON(t, srv, http.MethodGet, badPath, nil, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("path %q expected 404, got %d", badPath, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "__PANEL_BASE_PATH__") {
			t.Fatalf("path %q leaked panel HTML!", badPath)
		}
	}

	// Exact basePath without trailing slash redirects to basePath/
	rec := doJSON(t, srv, http.MethodGet, bp, nil, "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != bp+"/" {
		t.Fatalf("expected 302 -> %s/, got %d loc=%q", bp, rec.Code, rec.Header().Get("Location"))
	}

	// Base path root returns index.html with injected window.__PANEL_BASE_PATH__
	rec = doJSON(t, srv, http.MethodGet, bp+"/", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on %s/, got %d", bp, rec.Code)
	}
	expectedScript := `window.__PANEL_BASE_PATH__ = "` + bp + `";`
	if !strings.Contains(rec.Body.String(), expectedScript) {
		t.Fatalf("missing injected base path script %q in body:\n%s", expectedScript, rec.Body.String())
	}

	// Static asset served cleanly
	rec = doJSON(t, srv, http.MethodGet, bp+"/assets/app-123.js", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log('ok')") {
		t.Fatalf("expected static asset 200, got %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestAuthLifecycleTOTPAndRateLimiting(t *testing.T) {
	srv, _, _, _, bp := newTestServer(t)

	// Initial status: uninitialized
	rec := doJSON(t, srv, http.MethodGet, bp+"/api/v1/auth/status", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("auth/status got %d", rec.Code)
	}
	var stResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &stResp)
	if stResp["initialized"] != false || stResp["authenticated"] != false || stResp["base_path"] != bp {
		t.Fatalf("unexpected initial auth/status: %+v", stResp)
	}

	// Protected route must reject unauthenticated requests
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/dashboard/summary", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 before auth, got %d", rec.Code)
	}

	// Setup admin + dynamically update base path
	newBP := "/secret-m1"
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/auth/setup", map[string]any{
		"username":      "admin",
		"password":      "SuperPass123!",
		"instance_id":   "tokyo-01",
		"web_base_path": newBP,
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("auth/setup failed: %d %s", rec.Code, rec.Body.String())
	}
	var setupResp struct {
		Token    string `json:"token"`
		BasePath string `json:"base_path"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &setupResp)
	if setupResp.Token == "" || setupResp.BasePath != newBP {
		t.Fatalf("unexpected setup response: %+v", setupResp)
	}

	// Old base path MUST immediately return 404 without restart!
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/auth/status", nil, setupResp.Token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("old base path should immediately return 404, got %d", rec.Code)
	}

	// New base path works immediately
	rec = doJSON(t, srv, http.MethodGet, newBP+"/api/v1/auth/status", nil, setupResp.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("new base path auth/status failed: %d", rec.Code)
	}

	// Duplicate setup must return 409 Conflict
	rec = doJSON(t, srv, http.MethodPost, newBP+"/api/v1/auth/setup", map[string]any{
		"username": "hacker",
		"password": "password123",
	}, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate setup, got %d", rec.Code)
	}

	// Rate limiting: 3 bad logins triggers 429
	for i := 0; i < 2; i++ {
		rec = doJSON(t, srv, http.MethodPost, newBP+"/api/v1/auth/login", map[string]any{
			"username": "admin",
			"password": "wrong-password",
		}, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d expected 401, got %d", i+1, rec.Code)
		}
	}
	rec = doJSON(t, srv, http.MethodPost, newBP+"/api/v1/auth/login", map[string]any{
		"username": "admin",
		"password": "wrong-password",
	}, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd failed login expected 429, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header on 429")
	}

	// Reset limiter for the test IP to test TOTP 2FA flow cleanly
	srv.limiter.Reset("198.51.100.77")

	// Setup & Enable TOTP
	rec = doJSON(t, srv, http.MethodPost, newBP+"/api/v1/auth/totp/setup", nil, setupResp.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("totp/setup failed: %d %s", rec.Code, rec.Body.String())
	}
	var totpSetup struct {
		Secret     string `json:"secret"`
		OtpauthURL string `json:"otpauth_url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &totpSetup)
	if totpSetup.Secret == "" || !strings.HasPrefix(totpSetup.OtpauthURL, "otpauth://totp/") {
		t.Fatalf("unexpected totp setup: %+v", totpSetup)
	}

	code, err := totp.GenerateCode(totpSetup.Secret, time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	rec = doJSON(t, srv, http.MethodPost, newBP+"/api/v1/auth/totp/enable", map[string]any{"code": code}, setupResp.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("totp/enable failed: %d %s", rec.Code, rec.Body.String())
	}

	// Login without totp_code returns requires_totp=true
	rec = doJSON(t, srv, http.MethodPost, newBP+"/api/v1/auth/login", map[string]any{
		"username": "admin",
		"password": "SuperPass123!",
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login step 1 failed: %d", rec.Code)
	}
	var login1 struct {
		Token        string `json:"token"`
		RequiresTOTP bool   `json:"requires_totp"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &login1)
	if !login1.RequiresTOTP || login1.Token != "" {
		t.Fatalf("expected requires_totp=true and empty token, got %+v", login1)
	}

	// Login with valid totp_code succeeds
	rec = doJSON(t, srv, http.MethodPost, newBP+"/api/v1/auth/login", map[string]any{
		"username":  "admin",
		"password":  "SuperPass123!",
		"totp_code": code,
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login step 2 failed: %d %s", rec.Code, rec.Body.String())
	}
	var login2 struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &login2)
	if login2.Token == "" {
		t.Fatalf("expected token after TOTP login")
	}

	// Disable TOTP
	rec = doJSON(t, srv, http.MethodPost, newBP+"/api/v1/auth/totp/disable", map[string]any{
		"password": "SuperPass123!",
		"code":     code,
	}, login2.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("totp/disable failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCoreLogsConnectionsSettingsAndSSE(t *testing.T) {
	srv, _, mc, mt, bp := newTestServer(t)

	// Setup admin
	rec := doJSON(t, srv, http.MethodPost, bp+"/api/v1/auth/setup", map[string]any{
		"username": "admin",
		"password": "Password123!",
	}, "")
	var setupResp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &setupResp)
	tok := setupResp.Token

	// Dashboard summary
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/dashboard/summary", nil, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"panel_version":"0.1.0-test"`) {
		t.Fatalf("dashboard/summary unexpected: %d %s", rec.Code, rec.Body.String())
	}

	// Core lifecycle
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/core/start", nil, tok)
	if rec.Code != http.StatusOK || !mc.Status().Running {
		t.Fatalf("core/start failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/core/reload", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("core/reload failed: %d", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/core/stop", nil, tok)
	if rec.Code != http.StatusOK || mc.Status().Running {
		t.Fatalf("core/stop failed: %d", rec.Code)
	}

	// Releases & Upgrade & Upload
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/core/releases?prerelease=false", nil, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "v1.12.0") {
		t.Fatalf("core/releases failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodPost, bp+"/api/v1/core/upgrade", map[string]any{"tag_name": "v1.12.0"}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("core/upgrade failed: %d %s", rec.Code, rec.Body.String())
	}

	// Multipart upload
	var formBuf bytes.Buffer
	mw := multipart.NewWriter(&formBuf)
	fw, _ := mw.CreateFormFile("binary", "sing-box")
	_, _ = fw.Write([]byte("fake-elf-binary"))
	_ = mw.Close()
	upReq := httptest.NewRequest(http.MethodPost, bp+"/api/v1/core/upload", &formBuf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upReq.Header.Set("Authorization", "Bearer "+tok)
	upRec := httptest.NewRecorder()
	srv.ServeHTTP(upRec, upReq)
	if upRec.Code != http.StatusOK || !strings.Contains(upRec.Body.String(), `"to_version":"1.12.1"`) {
		t.Fatalf("core/upload failed: %d %s", upRec.Code, upRec.Body.String())
	}

	// Config preview & override patch validation
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/core/config", nil, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"config_json"`) {
		t.Fatalf("core/config failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodPut, bp+"/api/v1/core/config/override", map[string]any{
		"override_patch": `{"log":{"level":"debug"}}`,
	}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `debug`) {
		t.Fatalf("core/config/override failed: %d %s", rec.Code, rec.Body.String())
	}

	// Reject override if sing-box check fails
	mc.mu.Lock()
	mc.status.BinaryExists = true
	mc.checkErr = errors.New("exit status 1")
	mc.checkOut = "invalid outbound tag"
	mc.mu.Unlock()
	rec = doJSON(t, srv, http.MethodPut, bp+"/api/v1/core/config/override", map[string]any{
		"override_patch": `{"route":{"final":"missing"}}`,
	}, tok)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid outbound tag") {
		t.Fatalf("expected 400 on failed check, got %d %s", rec.Code, rec.Body.String())
	}

	// Logs & Connections
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/logs/recent?limit=50", nil, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "booted") {
		t.Fatalf("logs/recent failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodGet, bp+"/api/v1/connections", nil, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "conn-1") {
		t.Fatalf("connections failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodDelete, bp+"/api/v1/connections/conn-1", nil, tok)
	if rec.Code != http.StatusOK || mt.closedID != "conn-1" {
		t.Fatalf("close connection failed: %d closedID=%q", rec.Code, mt.closedID)
	}

	// Settings dynamic web_base_path update
	rec = doJSON(t, srv, http.MethodPut, bp+"/api/v1/settings", map[string]any{
		"web_base_path": "/moved-path",
		"github_proxy":  "https://mirror.example.com/",
		"log_level":     "warn",
	}, tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"/moved-path"`) {
		t.Fatalf("PUT /settings failed: %d %s", rec.Code, rec.Body.String())
	}
	if recOld := doJSON(t, srv, http.MethodGet, bp+"/api/v1/settings", nil, tok); recOld.Code != http.StatusNotFound {
		t.Fatalf("old base path should 404 after PUT /settings, got %d", recOld.Code)
	}
	if recNew := doJSON(t, srv, http.MethodGet, "/moved-path/api/v1/settings", nil, tok); recNew.Code != http.StatusOK {
		t.Fatalf("new base path should 200 after PUT /settings, got %d", recNew.Code)
	}

	// SSE dashboard stream sends initial event: metrics immediately
	sseCtx, cancel := context.WithCancel(context.Background())
	sseReq := httptest.NewRequest(http.MethodGet, "/moved-path/api/v1/dashboard/stream?token="+tok, nil).WithContext(sseCtx)
	sseRec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.ServeHTTP(sseRec, sseReq)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if !strings.Contains(sseRec.Body.String(), "event: metrics") {
		t.Fatalf("expected SSE metrics event, got %q", sseRec.Body.String())
	}
}
