package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
)

type contextKey string

const ctxKeyUsername contextKey = "username"

// Server handles dynamic Web Base Path gating, REST/SSE APIs, and embedded SPA serving.
type Server struct {
	store             *store.Store
	tokens            *auth.TokenManager
	limiter           *auth.LoginRateLimiter
	core              CoreManager
	telemetry         TelemetryHub
	staticFS          fs.FS
	dataDir           string
	panelVersion      string
	startedAt         time.Time
	onPanelCertChange func()

	basePath  atomic.Value // string
	decoyHTML atomic.Value // string
	apiRouter chi.Router
}

// NewServer creates and wires the HTTP server handler.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("api: store is required")
	}
	if cfg.Tokens == nil {
		return nil, errors.New("api: token manager is required")
	}
	if cfg.RateLimiter == nil {
		cfg.RateLimiter = auth.NewLoginRateLimiter(5, 5*time.Minute, 15*time.Minute)
	}
	if cfg.PanelVersion == "" {
		cfg.PanelVersion = "0.1.0-dev"
	}
	if cfg.StartedAt.IsZero() {
		cfg.StartedAt = time.Now().UTC()
	}

	s := &Server{
		store:             cfg.Store,
		tokens:            cfg.Tokens,
		limiter:           cfg.RateLimiter,
		core:              cfg.Core,
		telemetry:         cfg.Telemetry,
		staticFS:          cfg.StaticFS,
		dataDir:           cfg.DataDir,
		panelVersion:      cfg.PanelVersion,
		startedAt:         cfg.StartedAt,
		onPanelCertChange: cfg.OnPanelCertChange,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bp, _, err := cfg.Store.GetSetting(ctx, model.SettingWebBasePath)
	if err != nil {
		return nil, fmt.Errorf("load web_base_path: %w", err)
	}
	s.SetBasePath(bp)

	decoy, _, _ := cfg.Store.GetSetting(ctx, model.SettingDecoyHTML)
	s.decoyHTML.Store(decoy)

	s.apiRouter = s.buildAPIRouter()
	return s, nil
}

// BasePath returns the current normalized secret Web Base Path (e.g. "/sbox-9f3k2a").
func (s *Server) BasePath() string {
	if v, ok := s.basePath.Load().(string); ok {
		return v
	}
	return ""
}

// SetBasePath updates the live secret Web Base Path immediately without process restart.
func (s *Server) SetBasePath(bp string) {
	norm := store.NormalizeBasePath(bp)
	if norm == "/" {
		norm = ""
	}
	s.basePath.Store(norm)
}

// SetDecoyHTML configures optional decoy HTML returned on root and unknown paths.
func (s *Server) SetDecoyHTML(html string) {
	s.decoyHTML.Store(html)
}

func (s *Server) buildAPIRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/api/v1", func(r chi.Router) {
		// Public auth routes
		r.Get("/auth/status", s.handleAuthStatus)
		r.Post("/auth/setup", s.handleAuthSetup)
		r.Post("/auth/login", s.handleAuthLogin)
		r.Post("/auth/logout", s.handleAuthLogout)

		// Authenticated routes
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)

			r.Post("/auth/password", s.handleChangePassword)
			r.Post("/auth/totp/setup", s.handleTOTPSetup)
			r.Post("/auth/totp/enable", s.handleTOTPEnable)
			r.Post("/auth/totp/disable", s.handleTOTPDisable)

			r.Get("/dashboard/summary", s.handleDashboardSummary)
			r.Get("/dashboard/stream", s.handleDashboardStream)

			r.Get("/core/status", s.handleCoreStatus)
			r.Post("/core/start", s.handleCoreStart)
			r.Post("/core/stop", s.handleCoreStop)
			r.Post("/core/restart", s.handleCoreRestart)
			r.Post("/core/reload", s.handleCoreReload)
			r.Post("/core/rollback", s.handleCoreRollback)
			r.Get("/core/releases", s.handleCoreReleases)
			r.Post("/core/upgrade", s.handleCoreUpgrade)
			r.Post("/core/upload", s.handleCoreUpload)
			r.Get("/core/config", s.handleCoreConfig)
			r.Put("/core/config/override", s.handleCoreConfigOverride)

			r.Get("/logs/recent", s.handleLogsRecent)
			r.Get("/logs/stream", s.handleLogsStream)

			r.Get("/connections", s.handleConnectionsList)
			r.Delete("/connections/{id}", s.handleConnectionClose)

			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handleUpdateSettings)

			// Milestone 2: Certificates & Reality/SS2022 Tools
			r.Get("/certificates", s.handleListCertificates)
			r.Post("/certificates/self-signed", s.handleCreateSelfSignedCert)
			r.Post("/certificates/external", s.handleCreateExternalCert)
			r.Post("/certificates/acme", s.handleCreateACMECert)
			r.Post("/certificates/{id}/renew", s.handleRenewCertificate)
			r.Delete("/certificates/{id}", s.handleDeleteCertificate)

			r.Post("/tools/reality-keypair", s.handleToolRealityKeypair)
			r.Post("/tools/reality-probe", s.handleToolRealityProbe)
			r.Post("/tools/ss2022-key", s.handleToolSS2022Key)

			// Milestone 2: Members
			r.Get("/members", s.handleListMembers)
			r.Post("/members", s.handleCreateMember)
			r.Put("/members/{id}", s.handleUpdateMember)
			r.Post("/members/{id}/toggle", s.handleToggleMember)
			r.Post("/members/{id}/reset-credentials", s.handleResetMemberCredentials)
			r.Delete("/members/{id}", s.handleDeleteMember)

			// Milestone 2: Multi-Protocol Inbounds
			r.Get("/inbounds", s.handleListInbounds)
			r.Get("/inbounds/defaults/{protocol}", s.handleInboundDefaults)
			r.Post("/inbounds", s.handleCreateInbound)
			r.Put("/inbounds/{id}", s.handleUpdateInbound)
			r.Post("/inbounds/{id}/toggle", s.handleToggleInbound)
			r.Delete("/inbounds/{id}", s.handleDeleteInbound)

			// Milestone 2: Historical Traffic Stats
			r.Get("/traffic/stats", s.handleTrafficStats)
			r.Post("/traffic/reset", s.handleTrafficReset)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})

	return r
}

// ServeHTTP enforces the dynamic secret Web Base Path before routing API or SPA traffic.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bp := s.BasePath()
	reqPath := r.URL.Path

	var relPath string
	if bp != "" {
		if reqPath == bp {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				target := bp + "/"
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusFound)
				return
			}
			s.serveNotFoundOrDecoy(w, r)
			return
		}
		if !strings.HasPrefix(reqPath, bp+"/") {
			s.serveNotFoundOrDecoy(w, r)
			return
		}
		relPath = strings.TrimPrefix(reqPath, bp)
	} else {
		relPath = reqPath
	}

	if relPath == "" {
		relPath = "/"
	}

	if relPath == "/api" || strings.HasPrefix(relPath, "/api/") {
		r2 := r.Clone(r.Context())
		u := *r.URL
		u.Path = relPath
		u.RawPath = ""
		r2.URL = &u
		s.apiRouter.ServeHTTP(w, r2)
		return
	}

	s.serveSPA(w, r, relPath, bp)
}

func (s *Server) serveNotFoundOrDecoy(w http.ResponseWriter, _ *http.Request) {
	if html, ok := s.decoyHTML.Load().(string); ok && strings.TrimSpace(html) != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(html))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("404 page not found\n"))
}

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request, relPath, basePath string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.staticFS == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(injectBasePath([]byte("<!doctype html><html><head><title>sbox-panel</title></head><body><h1>sbox-panel</h1></body></html>"), basePath))
		return
	}

	cleaned := strings.TrimPrefix(path.Clean("/"+relPath), "/")
	if cleaned != "" && cleaned != "." && cleaned != "index.html" {
		if info, err := fs.Stat(s.staticFS, cleaned); err == nil && !info.IsDir() {
			if strings.HasPrefix(cleaned, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.staticFS, cleaned)
			return
		}
		// Missing asset files under assets/ should return 404 rather than index.html
		if strings.HasPrefix(cleaned, "assets/") {
			http.NotFound(w, r)
			return
		}
	}

	rawIndex, err := fs.ReadFile(s.staticFS, "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "embedded frontend index.html missing")
		return
	}

	injected := injectBasePath(rawIndex, basePath)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(injected)
	}
}

func injectBasePath(htmlBytes []byte, basePath string) []byte {
	encodedBP, _ := json.Marshal(basePath)
	var snippet string
	if basePath != "" {
		snippet = fmt.Sprintf(`<base href="%s/"><script>window.__PANEL_BASE_PATH__ = %s;</script>`, escapeAttr(basePath), string(encodedBP))
	} else {
		snippet = fmt.Sprintf(`<script>window.__PANEL_BASE_PATH__ = %s;</script>`, string(encodedBP))
	}

	lower := bytes.ToLower(htmlBytes)
	if idx := bytes.Index(lower, []byte("<head>")); idx >= 0 {
		insertAt := idx + len("<head>")
		var buf bytes.Buffer
		buf.Grow(len(htmlBytes) + len(snippet) + 2)
		buf.Write(htmlBytes[:insertAt])
		buf.WriteString("\n    ")
		buf.WriteString(snippet)
		buf.Write(htmlBytes[insertAt:])
		return buf.Bytes()
	}
	if idx := bytes.Index(lower, []byte("</head>")); idx >= 0 {
		var buf bytes.Buffer
		buf.Grow(len(htmlBytes) + len(snippet) + 2)
		buf.Write(htmlBytes[:idx])
		buf.WriteString(snippet)
		buf.WriteByte('\n')
		buf.Write(htmlBytes[idx:])
		return buf.Bytes()
	}
	return append([]byte(snippet+"\n"), htmlBytes...)
}

func escapeAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := auth.ExtractToken(r)
		claims, err := s.tokens.VerifyToken(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyUsername, claims.Username)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func usernameFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyUsername).(string); ok {
		return v
	}
	return ""
}

func isRequestSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": msg,
	})
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 2<<20))
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func isValidBasePath(bp string) bool {
	if bp == "" || bp == "/" {
		return false
	}
	if !strings.HasPrefix(bp, "/") {
		return false
	}
	if strings.HasPrefix(bp, "/api") && (len(bp) == 4 || bp[4] == '/') {
		return false
	}
	if len(bp) > 64 {
		return false
	}
	for _, r := range bp {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '/' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
