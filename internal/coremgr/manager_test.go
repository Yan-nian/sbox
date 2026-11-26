package coremgr

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const mockSingBoxSource = `package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	mockVersion = "1.12.0"
	mockMode    = "healthy" // healthy | bad-check | crash-run | bad-health
)

type minimalCfg struct {
	Experimental struct {
		ClashAPI struct {
			ExternalController string ` + "`json:\"external_controller\"`" + `
			Secret             string ` + "`json:\"secret\"`" + `
		} ` + "`json:\"clash_api\"`" + `
	} ` + "`json:\"experimental\"`" + `
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Printf("sing-box version %s\n\nEnvironment: go1.23.0 test/mock\n", mockVersion)
	case "check":
		if mockMode == "bad-check" {
			fmt.Fprintln(os.Stderr, "ERROR decode config: unknown field legacy_field")
			os.Exit(1)
		}
		cfgPath := findConfigFlag(os.Args[2:])
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR read config: %v\n", err)
			os.Exit(1)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR invalid JSON: %v\n", err)
			os.Exit(1)
		}
		if _, bad := doc["force_invalid"]; bad {
			fmt.Fprintln(os.Stderr, "ERROR invalid field force_invalid")
			os.Exit(1)
		}
		fmt.Println("INFO check passed")
	case "run":
		if mockMode == "crash-run" {
			fmt.Fprintln(os.Stderr, "FATAL simulated startup crash")
			os.Exit(1)
		}
		cfgPath := findConfigFlag(os.Args[2:])
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FATAL read config: %v\n", err)
			os.Exit(1)
		}
		var cfg minimalCfg
		_ = json.Unmarshal(data, &cfg)

		addr := cfg.Experimental.ClashAPI.ExternalController
		secret := cfg.Experimental.ClashAPI.Secret

		mux := http.NewServeMux()
		mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
			if mockMode == "bad-health" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if secret != "" && r.Header.Get("Authorization") != "Bearer "+secret {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "{\"version\":%q}", mockVersion)
		})

		srv := &http.Server{Addr: addr, Handler: mux}
		go func() {
			if addr != "" {
				_ = srv.ListenAndServe()
			}
		}()

		fmt.Printf("INFO mock sing-box %s started on %s\n", mockVersion, addr)
		hupCh := make(chan os.Signal, 2)
		signal.Notify(hupCh, syscall.SIGHUP)
		go func() {
			for range hupCh {
				fmt.Println("INFO mock sing-box received SIGHUP reload")
			}
		}()

		sigCh := make(chan os.Signal, 2)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		for range sigCh {
			_ = srv.Close()
			time.Sleep(10 * time.Millisecond)
			os.Exit(0)
		}
	default:
		os.Exit(2)
	}
}

func findConfigFlag(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" || args[i] == "--config" {
			return args[i+1]
		}
	}
	return ""
}
`

func findGoBinary(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	cand := filepath.Join(runtime.GOROOT(), "bin", exe)
	if fileExists(cand) {
		return cand
	}
	t.Fatal("cannot locate go binary for building mock sing-box")
	return ""
}

func buildMockSingBox(t *testing.T, outDir, version, mode string) string {
	t.Helper()
	srcDir := filepath.Join(outDir, fmt.Sprintf("src-%s-%s", version, mode))
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir mock src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(mockSingBoxSource), 0o600); err != nil {
		t.Fatalf("write mock main.go: %v", err)
	}
	outPath := filepath.Join(outDir, fmt.Sprintf("mock-%s-%s%s", version, mode, exeExt()))
	ldflags := fmt.Sprintf("-X main.mockVersion=%s -X main.mockMode=%s", version, mode)

	goBin := findGoBinary(t)
	cmd := exec.Command(goBin, "build", "-ldflags", ldflags, "-o", outPath, filepath.Join(srcDir, "main.go"))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile mock sing-box (%s/%s): %v\n%s", version, mode, err, string(out))
	}
	return outPath
}

func exeExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func makeTestConfig(addr, secret string) []byte {
	cfg := map[string]any{
		"log": map[string]any{"level": "info"},
		"experimental": map[string]any{
			"clash_api": map[string]any{
				"external_controller": addr,
				"secret":              secret,
			},
		},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return b
}

func makeZipArchive(t *testing.T, binaryPath string) []byte {
	t.Helper()
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read binary for zip: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	name := "sing-box-1.12.1/" + platformBinaryName(runtime.GOOS)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func makeTarGzArchive(t *testing.T, binaryPath string) []byte {
	t.Helper()
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read binary for tar.gz: %v", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := "sing-box-1.12.1/" + platformBinaryName(runtime.GOOS)
	hdr := &tar.Header{
		Name: name,
		Mode: 0o755,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestNewManager_AutoClashConfig(t *testing.T) {
	mgr := NewManager(Options{DataDir: t.TempDir()})
	addr, secret := mgr.ClashAPIConfig()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("expected loopback ClashAPIAddr, got %q", addr)
	}
	if len(secret) != 32 {
		t.Errorf("expected 32-char hex secret, got len=%d (%q)", len(secret), secret)
	}
	st := mgr.Status()
	if st.Running || st.BinaryExists || st.HasBackup {
		t.Errorf("unexpected initial status: %+v", st)
	}
	if st.ClashAPIAddr != addr {
		t.Errorf("status ClashAPIAddr=%q != %q", st.ClashAPIAddr, addr)
	}
}

func TestSelectReleaseAsset_Architectures(t *testing.T) {
	assets := []githubAsset{
		{Name: "sing-box-1.12.0-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux-amd64.tar.gz"},
		{Name: "sing-box-1.12.0-linux-amd64-musl.tar.gz", BrowserDownloadURL: "https://example.com/linux-amd64-musl.tar.gz"},
		{Name: "sing-box-1.12.0-linux-arm64.tar.gz", BrowserDownloadURL: "https://example.com/linux-arm64.tar.gz"},
		{Name: "sing-box-1.12.0-linux-armv7.tar.gz", BrowserDownloadURL: "https://example.com/linux-armv7.tar.gz"},
		{Name: "sing-box-1.12.0-windows-amd64.zip", BrowserDownloadURL: "https://example.com/windows-amd64.zip"},
		{Name: "sing-box-1.12.0-windows-amd64-legacy.zip", BrowserDownloadURL: "https://example.com/windows-amd64-legacy.zip"},
	}

	cases := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "sing-box-1.12.0-linux-amd64.tar.gz"},
		{"linux", "arm64", "sing-box-1.12.0-linux-arm64.tar.gz"},
		{"linux", "arm", "sing-box-1.12.0-linux-armv7.tar.gz"},
		{"windows", "amd64", "sing-box-1.12.0-windows-amd64.zip"},
	}

	for _, tc := range cases {
		got, ok := selectReleaseAsset(assets, tc.goos, tc.goarch)
		if !ok || got.Name != tc.want {
			t.Errorf("selectReleaseAsset(%s,%s) = (%q,%v), want %q", tc.goos, tc.goarch, got.Name, ok, tc.want)
		}
	}
}

func TestListReleases_StableAndPrerelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rels := []githubRelease{
			{
				TagName:     "v1.12.1-beta.1",
				Name:        "1.12.1-beta.1",
				Prerelease:  true,
				PublishedAt: time.Now().UTC(),
				Assets: []githubAsset{
					{Name: "sing-box-1.12.1-beta.1-linux-amd64.tar.gz", Size: 100, BrowserDownloadURL: "https://example.com/beta.tar.gz"},
					{Name: "sing-box-1.12.1-beta.1-windows-amd64.zip", Size: 100, BrowserDownloadURL: "https://example.com/beta.zip"},
				},
			},
			{
				TagName:     "v1.12.0",
				Name:        "1.12.0",
				Prerelease:  false,
				PublishedAt: time.Now().UTC().Add(-time.Hour),
				Assets: []githubAsset{
					{Name: "sing-box-1.12.0-linux-amd64.tar.gz", Size: 200, BrowserDownloadURL: "https://example.com/stable.tar.gz"},
					{Name: "sing-box-1.12.0-windows-amd64.zip", Size: 200, BrowserDownloadURL: "https://example.com/stable.zip"},
				},
			},
			{
				TagName: "v1.12.2-draft",
				Draft:   true,
				Assets: []githubAsset{
					{Name: "sing-box-1.12.2-linux-amd64.tar.gz", Size: 50, BrowserDownloadURL: "https://example.com/draft.tar.gz"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rels)
	}))
	defer srv.Close()

	mgr := NewManager(Options{DataDir: t.TempDir()})
	mgr.releasesAPIURL = srv.URL
	mgr.targetGOOS = "linux"
	mgr.targetGOARCH = "amd64"

	stable, err := mgr.ListReleases(context.Background(), false, "")
	if err != nil {
		t.Fatalf("ListReleases(stable) error: %v", err)
	}
	if len(stable) != 1 || stable[0].TagName != "v1.12.0" {
		t.Fatalf("expected only v1.12.0 stable release, got %+v", stable)
	}

	all, err := mgr.ListReleases(context.Background(), true, "")
	if err != nil {
		t.Fatalf("ListReleases(all) error: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 releases (stable + prerelease, no draft), got %+v", all)
	}
}

func TestLifecycle_StartReloadStopUpgradeRollback(t *testing.T) {
	ctx := context.Background()
	tmpBin := t.TempDir()

	// Build mock sing-box binaries for each test scenario.
	v1Bin := buildMockSingBox(t, tmpBin, "1.12.0", "healthy")
	v2Bin := buildMockSingBox(t, tmpBin, "1.12.1", "healthy")
	badCheckBin := buildMockSingBox(t, tmpBin, "1.13.0-badcheck", "bad-check")
	crashBin := buildMockSingBox(t, tmpBin, "1.13.0-crash", "crash-run")

	var logMu sync.Mutex
	var logs []string
	dataDir := t.TempDir()
	mgr := NewManager(Options{
		DataDir: dataDir,
		OnLogLine: func(level, line string) {
			logMu.Lock()
			logs = append(logs, level+":"+line)
			logMu.Unlock()
		},
	})
	mgr.healthProbeTimeout = 2 * time.Second
	mgr.healthProbeInterval = 50 * time.Millisecond

	addr, secret := mgr.ClashAPIConfig()
	if err := mgr.EnsureConfig(makeTestConfig(addr, secret)); err != nil {
		t.Fatalf("EnsureConfig: %v", err)
	}

	// 1. Install v1 via raw UploadBinary.
	v1Bytes, err := os.ReadFile(v1Bin)
	if err != nil {
		t.Fatalf("read v1: %v", err)
	}
	upRes := mgr.UploadBinary(ctx, bytes.NewReader(v1Bytes), platformBinaryName(runtime.GOOS))
	if !upRes.Success || upRes.ToVersion != "1.12.0" {
		t.Fatalf("initial UploadBinary failed: %+v", upRes)
	}
	defer func() { _ = mgr.Stop() }()

	st := mgr.Status()
	if !st.Running || !st.BinaryExists || st.Version != "1.12.0" || st.PID == 0 {
		t.Fatalf("expected v1 running after upload, got %+v", st)
	}

	// 2. Reload with invalid config should fail and keep running core intact.
	origPID := st.PID
	if err := mgr.Reload(ctx, []byte(`{"force_invalid": true}`)); err == nil {
		t.Fatalf("expected Reload to reject invalid config")
	}
	if !mgr.Status().Running || mgr.Status().PID != origPID {
		t.Fatalf("core process should stay untouched after failed reload check")
	}

	// 3. Reload with valid config succeeds.
	if err := mgr.Reload(ctx, makeTestConfig(addr, secret)); err != nil {
		t.Fatalf("valid Reload failed: %v", err)
	}
	if !mgr.Status().Running {
		t.Fatalf("expected core running after valid reload")
	}

	// 4. Online Upgrade to v2 via mock GitHub Release + archive download (.zip on Windows, .tar.gz on Unix).
	var archiveBytes []byte
	var assetName string
	if runtime.GOOS == "windows" {
		archiveBytes = makeZipArchive(t, v2Bin)
		assetName = "sing-box-1.12.1-windows-" + normalizeSingBoxArch(runtime.GOARCH) + ".zip"
	} else {
		archiveBytes = makeTarGzArchive(t, v2Bin)
		assetName = "sing-box-1.12.1-" + runtime.GOOS + "-" + normalizeSingBoxArch(runtime.GOARCH) + ".tar.gz"
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			rels := []githubRelease{
				{
					TagName:     "v1.12.1",
					Name:        "1.12.1",
					PublishedAt: time.Now().UTC(),
					Assets: []githubAsset{
						{
							Name:               assetName,
							Size:               int64(len(archiveBytes)),
							BrowserDownloadURL: srv.URL + "/download/" + assetName,
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(rels)
		case "/download/" + assetName:
			_, _ = w.Write(archiveBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	mgr.releasesAPIURL = srv.URL + "/releases"

	upg := mgr.Upgrade(ctx, UpgradeOptions{TagName: "v1.12.1"})
	if !upg.Success || upg.RolledBack || upg.FromVersion != "1.12.0" || upg.ToVersion != "1.12.1" {
		t.Fatalf("Upgrade to v1.12.1 failed: %+v", upg)
	}
	st = mgr.Status()
	if !st.Running || st.Version != "1.12.1" || !st.HasBackup {
		t.Fatalf("expected v1.12.1 running with backup, got %+v", st)
	}

	// 5. Upgrade with pre-check failure -> aborts with zero downtime (PID unchanged, RolledBack=false).
	v2PID := st.PID
	badCheckArchive := makeTarGzArchive(t, badCheckBin)
	badRes := mgr.UploadBinary(ctx, bytes.NewReader(badCheckArchive), "sing-box-bad.tar.gz")
	if badRes.Success || badRes.RolledBack {
		t.Fatalf("expected pre-check abort without rollback, got %+v", badRes)
	}
	if !strings.Contains(badRes.CheckOutput, "legacy_field") {
		t.Errorf("expected check output detail, got %q", badRes.CheckOutput)
	}
	st = mgr.Status()
	if !st.Running || st.PID != v2PID || st.Version != "1.12.1" {
		t.Fatalf("expected zero-downtime v1.12.1 unchanged, got %+v", st)
	}

	// 6. Upgrade with runtime crash -> automatic rollback to v1.12.1 and auto-restart!
	crashZip := makeZipArchive(t, crashBin)
	crashRes := mgr.UploadBinary(ctx, bytes.NewReader(crashZip), "sing-box-crash.zip")
	if crashRes.Success || !crashRes.RolledBack {
		t.Fatalf("expected automatic rollback on crash, got %+v", crashRes)
	}
	st = mgr.Status()
	if !st.Running || st.Version != "1.12.1" {
		t.Fatalf("expected v1.12.1 restored and running after auto-rollback, got %+v", st)
	}

	// 7. Manual Rollback from sing-box.bak (which is currently v1.12.1; copy v1 to backup to verify swap).
	if err := copyFileAtomic(v1Bin, mgr.backupPath, 0o755); err != nil {
		t.Fatalf("seed backup with v1: %v", err)
	}
	if err := mgr.Rollback(ctx); err != nil {
		t.Fatalf("manual Rollback failed: %v", err)
	}
	st = mgr.Status()
	if !st.Running || st.Version != "1.12.0" {
		t.Fatalf("expected v1.12.0 running after manual Rollback, got %+v", st)
	}

	// 8. Stop cleanly.
	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if mgr.Status().Running {
		t.Fatalf("expected Running=false after Stop")
	}

	logMu.Lock()
	logCount := len(logs)
	logMu.Unlock()
	if logCount == 0 {
		t.Errorf("expected OnLogLine callback to receive supervisor/process logs")
	}
}

func TestUpgrade_HealthProbeTimeoutRollbackAndProxyPrefix(t *testing.T) {
	ctx := context.Background()
	tmpBin := t.TempDir()

	v1Bin := buildMockSingBox(t, tmpBin, "1.12.0", "healthy")
	badHealthBin := buildMockSingBox(t, tmpBin, "1.13.0-unhealthy", "bad-health")

	mgr := NewManager(Options{DataDir: t.TempDir()})
	mgr.healthProbeTimeout = 400 * time.Millisecond
	mgr.healthProbeInterval = 50 * time.Millisecond

	addr, secret := mgr.ClashAPIConfig()
	if err := mgr.EnsureConfig(makeTestConfig(addr, secret)); err != nil {
		t.Fatalf("EnsureConfig: %v", err)
	}

	v1Bytes, err := os.ReadFile(v1Bin)
	if err != nil {
		t.Fatalf("read v1: %v", err)
	}
	res := mgr.UploadBinary(ctx, bytes.NewReader(v1Bytes), platformBinaryName(runtime.GOOS))
	if !res.Success {
		t.Fatalf("seed v1 failed: %+v", res)
	}
	defer func() { _ = mgr.Stop() }()

	// Serve badHealthBin via a mock mirror proxy prefix (http://.../https://github.com/...).
	badHealthZip := makeZipArchive(t, badHealthBin)
	var requestedPath string
	mirrorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = w.Write(badHealthZip)
	}))
	defer mirrorSrv.Close()

	upg := mgr.Upgrade(ctx, UpgradeOptions{
		TagName:     "v1.13.0-unhealthy",
		DownloadURL: "https://github.com/SagerNet/sing-box/releases/download/v1.13.0/sing-box.zip",
		ProxyURL:    mirrorSrv.URL + "/",
	})

	if !strings.Contains(requestedPath, "https://github.com/SagerNet/sing-box") {
		t.Errorf("expected mirror prefix download path, got %q", requestedPath)
	}
	if upg.Success || !upg.RolledBack {
		t.Fatalf("expected rollback on /version 503 timeout, got %+v", upg)
	}
	st := mgr.Status()
	if !st.Running || st.Version != "1.12.0" {
		t.Fatalf("expected v1.12.0 restored after health probe timeout, got %+v", st)
	}
}
