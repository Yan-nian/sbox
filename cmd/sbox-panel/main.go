package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sbox-panel/sbox-panel/internal/api"
	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/cluster"
	"github.com/sbox-panel/sbox-panel/internal/coremgr"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
	"github.com/sbox-panel/sbox-panel/internal/telemetry"
	"github.com/sbox-panel/sbox-panel/web"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		if err := runServe(nil); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	// Allow invoking `sbox-panel --data-dir ... --listen ...` without explicit `serve`.
	if strings.HasPrefix(cmd, "-") && cmd != "-h" && cmd != "--help" && cmd != "-v" && cmd != "--version" {
		if err := runServe(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "agent":
		err = runAgent(args)
	case "status":
		err = runStatus(args)
	case "show-url":
		err = runShowURL(args)
	case "reset-auth":
		err = runResetAuth(args)
	case "set-port":
		err = runSetPort(args)
	case "rollback-core":
		err = runRollbackCore(args)
	case "version", "-v", "--version":
		fmt.Printf("sbox-panel %s\n", Version)
		return
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`sbox-panel %s — Lightweight sing-box management panel & multi-VPS cluster agent

Usage:
  sbox-panel <command> [flags]

Commands:
  serve          Start the Master web panel server, cluster hub, and local sing-box supervisor
  agent          Start in reverse-connect Agent mode managed by a remote Master panel
  status         Display current panel configuration and sing-box core status
  show-url       Print the panel access URL and secret Web Base Path
  reset-auth     Reset admin password and optionally disable TOTP 2FA
  set-port       Change the persisted HTTP listen port
  rollback-core  Rollback sing-box binary from sing-box.bak
  version        Print version information

Environment Variables:
  SBOX_DATA_DIR  Default data directory (serve: ./data, agent: /var/lib/sbox-agent)
  SBOX_LISTEN    Default listen address for serve (default: 0.0.0.0:18080)
  SBOX_MASTER    Master panel base URL for agent mode (e.g. https://master:18080/sbox-xxxx)
  SBOX_TOKEN     Authentication token for agent mode
  SBOX_INSECURE  Skip TLS verification for self-signed Master HTTPS (true/1)
`, Version)
}

func defaultDataDir() string {
	if v := strings.TrimSpace(os.Getenv("SBOX_DATA_DIR")); v != "" {
		return v
	}
	return "./data"
}

func defaultAgentDataDir() string {
	if v := strings.TrimSpace(os.Getenv("SBOX_DATA_DIR")); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		return "./agent-data"
	}
	return "/var/lib/sbox-agent"
}

func defaultListenEnv() string {
	return strings.TrimSpace(os.Getenv("SBOX_LISTEN"))
}

func defaultInsecureEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SBOX_INSECURE")))
	return v == "1" || v == "true" || v == "yes"
}

func openStore(dataDir string) (*store.Store, string, error) {
	absDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(absDir, 0o750); err != nil {
		return nil, "", fmt.Errorf("create data dir: %w", err)
	}
	st, err := store.Open(filepath.Join(absDir, "panel.db"))
	if err != nil {
		return nil, "", err
	}
	return st, absDir, nil
}

func parsePortFromListen(addr string, fallback int) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fallback
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 || p > 65535 {
		return fallback
	}
	return p
}

func runAgent(args []string) error {
	fsFlags := flag.NewFlagSet("agent", flag.ContinueOnError)
	masterFlag := fsFlags.String("master", strings.TrimSpace(os.Getenv("SBOX_MASTER")), "Master panel URL including secret base path (env: SBOX_MASTER)")
	tokenFlag := fsFlags.String("token", strings.TrimSpace(os.Getenv("SBOX_TOKEN")), "Node authentication token from Master (env: SBOX_TOKEN)")
	dataDirFlag := fsFlags.String("data-dir", defaultAgentDataDir(), "Persistent agent data directory (env: SBOX_DATA_DIR)")
	insecureFlag := fsFlags.Bool("insecure", defaultInsecureEnv(), "Skip TLS certificate verification for self-signed Master HTTPS (env: SBOX_INSECURE)")
	if err := fsFlags.Parse(args); err != nil {
		return err
	}

	masterURL := strings.TrimSpace(*masterFlag)
	token := strings.TrimSpace(*tokenFlag)
	if masterURL == "" {
		return errors.New("master URL is required (use --master or SBOX_MASTER)")
	}
	if token == "" {
		return errors.New("node token is required (use --token or SBOX_TOKEN)")
	}

	absDataDir, err := filepath.Abs(*dataDirFlag)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absDataDir, 0o750); err != nil {
		return fmt.Errorf("create agent data dir: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("============================================================\n")
	fmt.Printf(" sbox-panel %s starting in AGENT mode\n", Version)
	fmt.Printf(" Master URL    : %s\n", masterURL)
	fmt.Printf(" Data Dir      : %s\n", absDataDir)
	fmt.Printf(" Insecure TLS  : %v\n", *insecureFlag)
	fmt.Printf("============================================================\n")

	err = cluster.RunAgent(ctx, cluster.AgentConfig{
		MasterURL:    masterURL,
		Token:        token,
		DataDir:      absDataDir,
		AgentVersion: Version,
		InsecureTLS:  *insecureFlag,
	})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func runServe(args []string) error {
	fsFlags := flag.NewFlagSet("serve", flag.ContinueOnError)
	dataDirFlag := fsFlags.String("data-dir", defaultDataDir(), "Persistent data directory")
	listenFlag := fsFlags.String("listen", defaultListenEnv(), "HTTP listen address (e.g. 0.0.0.0:18080)")
	if err := fsFlags.Parse(args); err != nil {
		return err
	}

	st, absDataDir, err := openStore(*dataDirFlag)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	bootstrapPort := 18080
	if *listenFlag != "" {
		bootstrapPort = parsePortFromListen(*listenFlag, 18080)
	}

	basePath, instanceID, jwtSecret, err := st.EnsureBootstrapDefaults(ctx, bootstrapPort)
	if err != nil {
		return fmt.Errorf("bootstrap defaults: %w", err)
	}

	panelSettings, err := st.GetPanelSettings(ctx)
	if err != nil {
		return fmt.Errorf("load panel settings: %w", err)
	}

	listenAddr := strings.TrimSpace(*listenFlag)
	if listenAddr == "" {
		port := panelSettings.ListenPort
		if port <= 0 {
			port = 18080
		}
		listenAddr = fmt.Sprintf("0.0.0.0:%d", port)
	} else {
		// Keep persisted listen_port aligned if user explicitly passes --listen
		if p := parsePortFromListen(listenAddr, 0); p > 0 && p != panelSettings.ListenPort {
			_ = st.SetSetting(ctx, model.SettingListenPort, strconv.Itoa(p))
			panelSettings.ListenPort = p
		}
	}

	persistTraffic := func(nodeID int64, deltas []telemetry.TrafficDelta) {
		if len(deltas) == 0 {
			return
		}
		inputs := make([]store.TrafficDeltaInput, 0, len(deltas))
		for _, d := range deltas {
			inputs = append(inputs, store.TrafficDeltaInput{
				InboundTag: d.Inbound,
				MemberName: d.User,
				Upload:     d.Upload,
				Download:   d.Download,
			})
		}
		writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer writeCancel()
		if err := st.RecordTrafficDeltas(writeCtx, nodeID, time.Now().UTC(), inputs); err != nil {
			log.Printf("warn: persist traffic deltas (node=%d): %v", nodeID, err)
		}
	}

	hub := telemetry.NewHub(func(deltas []telemetry.TrafficDelta) {
		persistTraffic(1, deltas)
	})

	coreMgr := coremgr.NewManager(coremgr.Options{
		DataDir: absDataDir,
		OnLogLine: func(level, line string) {
			hub.PublishLog(level, "sing-box", line)
		},
	})

	clashAddr, clashSecret := coreMgr.ClashAPIConfig()

	// Persist local master node runtime metadata
	localHost, _ := os.Hostname()
	_ = st.UpdateNodeRuntimeInfo(ctx, 1, store.NodeRuntimeUpdate{
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Hostname:     localHost,
		AgentVersion: Version,
		CoreVersion:  coreMgr.Status().Version,
		DataDir:      absDataDir,
		LastSeenAt:   time.Now().UTC(),
	})

	localDriver := cluster.NewLocalDriver(1, absDataDir, Version, coreMgr, hub)

	var apiServer *api.Server
	clusterHub := cluster.NewHub(localDriver, cluster.HubCallbacks{
		AuthenticateToken: func(token string) (int64, bool) {
			authCtx, authCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer authCancel()
			node, err := st.GetNodeByToken(authCtx, token)
			if err != nil {
				return 0, false
			}
			return node.ID, true
		},
		OnAgentConnected: func(nodeID int64, hello cluster.NodeHello) {
			updCtx, updCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer updCancel()
			_ = st.UpdateNodeRuntimeInfo(updCtx, nodeID, store.NodeRuntimeUpdate{
				OS:           hello.OS,
				Arch:         hello.Arch,
				Hostname:     hello.Hostname,
				AgentVersion: hello.AgentVersion,
				CoreVersion:  hello.CoreStatus.Version,
				DataDir:      hello.DataDir,
				PublicIPv4:   hello.PublicIPv4,
				PublicIPv6:   hello.PublicIPv6,
				LastSeenAt:   time.Now().UTC(),
			})
			hub.PublishLog("info", "cluster", fmt.Sprintf("agent connected: node_id=%d host=%s (%s/%s)", nodeID, hello.Hostname, hello.OS, hello.Arch))

			// Asynchronously push compiled config + managed cert bundles to the newly connected agent
			if apiServer != nil {
				go func() {
					syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
					defer syncCancel()
					if err := apiServer.RecompileAndApplyNode(syncCtx, nodeID); err != nil {
						log.Printf("warn: initial config sync to agent node=%d failed: %v", nodeID, err)
					}
				}()
			}
		},
		OnAgentDisconnected: func(nodeID int64) {
			hub.PublishLog("warn", "cluster", fmt.Sprintf("agent disconnected: node_id=%d", nodeID))
		},
		OnTrafficDeltas: func(nodeID int64, deltas []telemetry.TrafficDelta) {
			persistTraffic(nodeID, deltas)
		},
	})
	defer clusterHub.Close()

	var staticFS fs.FS
	if embeddedFS, err := web.DistFS(); err == nil {
		staticFS = embeddedFS
	} else {
		log.Printf("warn: embedded web assets unavailable: %v", err)
	}

	tlsHolder := newPanelTLSHolder(st)
	httpsActive := false
	if panelSettings.PanelCertID > 0 {
		if err := tlsHolder.Reload(); err != nil {
			log.Printf("warn: failed to load panel HTTPS certificate (id=%d), falling back to HTTP: %v", panelSettings.PanelCertID, err)
			hub.PublishLog("warn", "panel", fmt.Sprintf("panel HTTPS cert id=%d unavailable, falling back to HTTP: %v", panelSettings.PanelCertID, err))
		} else if tlsHolder.HasCert() {
			httpsActive = true
		}
	}

	apiServer, err = api.NewServer(api.ServerConfig{
		Store:        st,
		Tokens:       auth.NewTokenManager(jwtSecret, auth.DefaultTokenTTL),
		RateLimiter:  auth.NewLoginRateLimiter(5, 5*time.Minute, 15*time.Minute),
		Hub:          clusterHub,
		Core:         coreMgr,
		Telemetry:    hub,
		StaticFS:     staticFS,
		DataDir:      absDataDir,
		PanelVersion: Version,
		StartedAt:    time.Now().UTC(),
		OnPanelCertChange: func() {
			tlsHolder.HandleCertChange(httpsActive)
		},
	})
	if err != nil {
		return fmt.Errorf("init api server: %w", err)
	}

	if err := apiServer.RecompileAndApplyNode(ctx, 1); err != nil {
		log.Printf("warn: build initial sing-box config: %v", err)
	}

	hub.Start(ctx, clashAddr, clashSecret)
	defer hub.Stop()

	if coreMgr.Status().BinaryExists {
		if err := coreMgr.Start(ctx); err != nil {
			log.Printf("warn: auto-start sing-box failed: %v", err)
			hub.PublishLog("error", "panel", "auto-start sing-box failed: "+err.Error())
		} else {
			hub.PublishLog("info", "panel", "sing-box core started automatically")
		}
	} else {
		hub.PublishLog("info", "panel", "sing-box binary not found yet; install it from Core & Logs")
	}

	// Background ACME certificate renewal loop (every 12h; renews auto_renew certs within 30d of expiry)
	go func() {
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				apiServer.RenewDueCertificates(ctx)
			}
		}
	}()

	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           apiServer,
		ReadHeaderTimeout: 15 * time.Second,
	}

	rawLn, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	ln := rawLn
	scheme := "http"
	if httpsActive {
		scheme = "https"
		ln = tls.NewListener(rawLn, &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: tlsHolder.GetCertificate,
		})
	}

	displayHost := panelSettings.PublicIPv4
	if displayHost == "" {
		displayHost = "127.0.0.1"
	}
	actualPort := parsePortFromListen(rawLn.Addr().String(), panelSettings.ListenPort)

	fmt.Printf("============================================================\n")
	fmt.Printf(" sbox-panel %s started (%s Master)\n", Version, strings.ToUpper(scheme))
	fmt.Printf(" Instance ID   : %s\n", instanceID)
	fmt.Printf(" Data Dir      : %s\n", absDataDir)
	fmt.Printf(" Listen Addr   : %s\n", rawLn.Addr().String())
	fmt.Printf(" Web Base Path : %s\n", basePath)
	fmt.Printf(" Panel URL     : %s://%s:%d%s/\n", scheme, displayHost, actualPort, basePath)
	fmt.Printf(" Clash API     : http://%s\n", clashAddr)
	fmt.Printf("============================================================\n")

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		log.Println("shutting down sbox-panel...")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		_ = httpServer.Shutdown(shutCtx)
		clusterHub.Close()
		_ = coreMgr.Stop()
		return nil
	case err := <-errCh:
		clusterHub.Close()
		_ = coreMgr.Stop()
		return err
	}
}

func runStatus(args []string) error {
	fsFlags := flag.NewFlagSet("status", flag.ContinueOnError)
	dataDirFlag := fsFlags.String("data-dir", defaultDataDir(), "Persistent data directory")
	if err := fsFlags.Parse(args); err != nil {
		return err
	}

	st, absDataDir, err := openStore(*dataDirFlag)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	if _, _, _, err := st.EnsureBootstrapDefaults(ctx, 18080); err != nil {
		return err
	}
	ps, err := st.GetPanelSettings(ctx)
	if err != nil {
		return err
	}
	initVal, _ := st.GetSettingDefault(ctx, model.SettingInitialized, "false")
	adminUser, _ := st.GetSettingDefault(ctx, model.SettingAdminUsername, "")
	nodes, _ := st.ListNodes(ctx)

	coreMgr := coremgr.NewManager(coremgr.Options{DataDir: absDataDir})
	cs := coreMgr.Status()

	fmt.Printf("sbox-panel %s\n", Version)
	fmt.Printf("Data Dir      : %s\n", absDataDir)
	fmt.Printf("Database      : %s\n", st.Path())
	fmt.Printf("Initialized   : %s\n", initVal)
	if adminUser != "" {
		fmt.Printf("Admin User    : %s\n", adminUser)
	}
	fmt.Printf("Instance ID   : %s\n", ps.InstanceID)
	fmt.Printf("Listen Port   : %d\n", ps.ListenPort)
	fmt.Printf("Web Base Path : %s\n", ps.WebBasePath)
	fmt.Printf("Panel Cert ID : %d\n", ps.PanelCertID)
	fmt.Printf("Cluster Nodes : %d\n", len(nodes))
	fmt.Printf("TOTP 2FA      : %v\n", ps.TOTPEnabled)
	fmt.Printf("Core Binary   : %s (exists=%v, version=%s, has_backup=%v)\n", cs.BinaryPath, cs.BinaryExists, cs.Version, cs.HasBackup)
	return nil
}

func runShowURL(args []string) error {
	fsFlags := flag.NewFlagSet("show-url", flag.ContinueOnError)
	dataDirFlag := fsFlags.String("data-dir", defaultDataDir(), "Persistent data directory")
	if err := fsFlags.Parse(args); err != nil {
		return err
	}

	st, _, err := openStore(*dataDirFlag)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	if _, _, _, err := st.EnsureBootstrapDefaults(ctx, 18080); err != nil {
		return err
	}
	ps, err := st.GetPanelSettings(ctx)
	if err != nil {
		return err
	}

	scheme := "http"
	if ps.PanelCertID > 0 {
		scheme = "https"
	}
	host := ps.PublicIPv4
	if host == "" {
		host = "<YOUR_SERVER_IP>"
	}

	fmt.Printf("Instance ID   : %s\n", ps.InstanceID)
	fmt.Printf("Listen Port   : %d\n", ps.ListenPort)
	fmt.Printf("Web Base Path : %s\n", ps.WebBasePath)
	fmt.Printf("Local URL     : %s://127.0.0.1:%d%s/\n", scheme, ps.ListenPort, ps.WebBasePath)
	fmt.Printf("Access URL    : %s://%s:%d%s/\n", scheme, host, ps.ListenPort, ps.WebBasePath)
	return nil
}

func runResetAuth(args []string) error {
	fsFlags := flag.NewFlagSet("reset-auth", flag.ContinueOnError)
	dataDirFlag := fsFlags.String("data-dir", defaultDataDir(), "Persistent data directory")
	usernameFlag := fsFlags.String("username", "", "New admin username (keeps current or defaults to 'admin')")
	passwordFlag := fsFlags.String("password", "", "New admin password (random password generated if omitted)")
	disableTOTP := fsFlags.Bool("disable-totp", true, "Disable TOTP 2FA during rescue reset")
	if err := fsFlags.Parse(args); err != nil {
		return err
	}

	st, _, err := openStore(*dataDirFlag)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	basePath, _, _, err := st.EnsureBootstrapDefaults(ctx, 18080)
	if err != nil {
		return err
	}

	username := strings.TrimSpace(*usernameFlag)
	if username == "" {
		username, _ = st.GetSettingDefault(ctx, model.SettingAdminUsername, "admin")
		if username == "" {
			username = "admin"
		}
	}

	password := *passwordFlag
	generated := false
	if password == "" {
		rnd, err := store.RandomHex(8)
		if err != nil {
			return fmt.Errorf("generate password: %w", err)
		}
		password = rnd
		generated = true
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	newJWTSecret, err := store.RandomHex(32)
	if err != nil {
		return err
	}

	updates := map[string]string{
		model.SettingAdminUsername:     username,
		model.SettingAdminPasswordHash: hash,
		model.SettingInitialized:       "true",
		model.SettingJWTSecret:         newJWTSecret,
	}
	if *disableTOTP {
		updates[model.SettingTOTPEnabled] = "false"
		updates[model.SettingTOTPSecret] = ""
		updates[model.SettingTOTPPendingSecret] = ""
	}

	if err := st.SetSettings(ctx, updates); err != nil {
		return err
	}

	fmt.Printf("Admin authentication reset complete.\n")
	fmt.Printf("Username      : %s\n", username)
	if generated {
		fmt.Printf("New Password  : %s\n", password)
	} else {
		fmt.Printf("New Password  : (updated as provided)\n")
	}
	fmt.Printf("TOTP Disabled : %v\n", *disableTOTP)
	fmt.Printf("Web Base Path : %s\n", basePath)
	fmt.Printf("Note: Restart the running sbox-panel service so existing sessions are invalidated.\n")
	return nil
}

func runSetPort(args []string) error {
	fsFlags := flag.NewFlagSet("set-port", flag.ContinueOnError)
	dataDirFlag := fsFlags.String("data-dir", defaultDataDir(), "Persistent data directory")
	portFlag := fsFlags.Int("port", 0, "New HTTP listen port (1-65535)")
	if err := fsFlags.Parse(args); err != nil {
		return err
	}

	port := *portFlag
	if port == 0 && fsFlags.NArg() > 0 {
		p, err := strconv.Atoi(fsFlags.Arg(0))
		if err != nil {
			return fmt.Errorf("invalid port %q", fsFlags.Arg(0))
		}
		port = p
	}
	if port < 1 || port > 65535 {
		return errors.New("port must be between 1 and 65535 (use --port <number>)")
	}

	st, _, err := openStore(*dataDirFlag)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	if _, _, _, err := st.EnsureBootstrapDefaults(ctx, port); err != nil {
		return err
	}
	if err := st.SetSetting(ctx, model.SettingListenPort, strconv.Itoa(port)); err != nil {
		return err
	}
	fmt.Printf("Updated listen_port to %d. Restart sbox-panel for the new port to take effect.\n", port)
	return nil
}

func runRollbackCore(args []string) error {
	fsFlags := flag.NewFlagSet("rollback-core", flag.ContinueOnError)
	dataDirFlag := fsFlags.String("data-dir", defaultDataDir(), "Persistent data directory")
	if err := fsFlags.Parse(args); err != nil {
		return err
	}

	absDataDir, err := filepath.Abs(*dataDirFlag)
	if err != nil {
		return err
	}
	coreMgr := coremgr.NewManager(coremgr.Options{DataDir: absDataDir})
	if err := coreMgr.Rollback(context.Background()); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}
	cs := coreMgr.Status()
	fmt.Printf("Rollback succeeded. Active sing-box binary: %s (version: %s)\n", cs.BinaryPath, cs.Version)
	return nil
}
