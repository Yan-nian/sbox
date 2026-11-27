package api

import (
	"crypto/subtle"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/sbox-panel/sbox-panel/internal/auth"
	"github.com/sbox-panel/sbox-panel/internal/model"
	"github.com/sbox-panel/sbox-panel/internal/store"
)

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	initVal, _ := s.store.GetSettingDefault(ctx, model.SettingInitialized, "false")
	totpVal, _ := s.store.GetSettingDefault(ctx, model.SettingTOTPEnabled, "false")
	initialized := initVal == "true"

	authenticated := false
	username := ""
	if initialized {
		if token := auth.ExtractToken(r); token != "" {
			if claims, err := s.tokens.VerifyToken(token); err == nil {
				authenticated = true
				username = claims.Username
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"initialized":   initialized,
		"authenticated": authenticated,
		"username":      username,
		"totp_enabled":  totpVal == "true",
		"base_path":     s.BasePath(),
	})
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	initVal, _ := s.store.GetSettingDefault(ctx, model.SettingInitialized, "false")
	if initVal == "true" {
		writeError(w, http.StatusConflict, "panel is already initialized")
		return
	}

	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		InstanceID  string `json:"instance_id"`
		WebBasePath string `json:"web_base_path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 2 || len(req.Username) > 64 {
		writeError(w, http.StatusBadRequest, "username must be 2 to 64 characters")
		return
	}
	if len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}

	pwHash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	activeBasePath := s.BasePath()
	updates := map[string]string{
		model.SettingAdminUsername:     req.Username,
		model.SettingAdminPasswordHash: pwHash,
		model.SettingInitialized:       "true",
	}

	if id := strings.TrimSpace(req.InstanceID); id != "" {
		updates[model.SettingInstanceID] = id
	}
	if bp := strings.TrimSpace(req.WebBasePath); bp != "" {
		norm := store.NormalizeBasePath(bp)
		if !isValidBasePath(norm) {
			writeError(w, http.StatusBadRequest, "invalid web_base_path: use letters, digits, '-', '_' and avoid '/' or '/api'")
			return
		}
		updates[model.SettingWebBasePath] = norm
		activeBasePath = norm
	}

	if err := s.store.SetSettings(ctx, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save admin setup")
		return
	}

	if id := strings.TrimSpace(req.InstanceID); id != "" {
		if local, err := s.store.GetNode(ctx, 1); err == nil {
			local.Name = id
			_ = s.store.UpdateNode(ctx, local)
		}
	}

	if activeBasePath != s.BasePath() {
		s.SetBasePath(activeBasePath)
	}

	token, err := s.tokens.IssueToken(req.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session token")
		return
	}

	auth.SetAuthCookie(w, token, activeBasePath, isRequestSecure(r), s.tokens.TTL())
	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", "admin account initialized for user "+req.Username)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"base_path": activeBasePath,
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := auth.ClientIP(r)

	if allowed, retryAfter := s.limiter.Allow(ip); !allowed {
		sec := int(math.Ceil(retryAfter.Seconds()))
		if sec < 1 {
			sec = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(sec))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":           "too many failed login attempts, please try again later",
			"retry_after_sec": sec,
		})
		return
	}

	initVal, _ := s.store.GetSettingDefault(ctx, model.SettingInitialized, "false")
	if initVal != "true" {
		writeError(w, http.StatusBadRequest, "panel is not initialized yet")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	storedUser, _ := s.store.GetSettingDefault(ctx, model.SettingAdminUsername, "")
	storedHash, _ := s.store.GetSettingDefault(ctx, model.SettingAdminPasswordHash, "")

	userOK := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(req.Username)), []byte(storedUser)) == 1
	passOK, _ := auth.VerifyPassword(req.Password, storedHash)
	if !userOK || !passOK {
		if blocked, retryAfter := s.limiter.RecordFailure(ip); blocked {
			sec := int(math.Ceil(retryAfter.Seconds()))
			if sec < 1 {
				sec = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(sec))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":           "too many failed login attempts, IP temporarily blocked",
				"retry_after_sec": sec,
			})
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	totpEnabled, _ := s.store.GetSettingDefault(ctx, model.SettingTOTPEnabled, "false")
	if totpEnabled == "true" {
		if strings.TrimSpace(req.TOTPCode) == "" {
			writeJSON(w, http.StatusOK, map[string]any{
				"token":         "",
				"requires_totp": true,
			})
			return
		}
		totpSecret, _ := s.store.GetSettingDefault(ctx, model.SettingTOTPSecret, "")
		if !auth.ValidateTOTP(req.TOTPCode, totpSecret) {
			if blocked, retryAfter := s.limiter.RecordFailure(ip); blocked {
				sec := int(math.Ceil(retryAfter.Seconds()))
				if sec < 1 {
					sec = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(sec))
				writeJSON(w, http.StatusTooManyRequests, map[string]any{
					"error":           "too many failed attempts, IP temporarily blocked",
					"retry_after_sec": sec,
				})
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "invalid 2FA verification code",
				"requires_totp": true,
			})
			return
		}
	}

	s.limiter.Reset(ip)

	token, err := s.tokens.IssueToken(storedUser)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session token")
		return
	}

	auth.SetAuthCookie(w, token, s.BasePath(), isRequestSecure(r), s.tokens.TTL())
	writeJSON(w, http.StatusOK, map[string]any{
		"token":         token,
		"requires_totp": false,
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	auth.ClearAuthCookie(w, s.BasePath(), isRequestSecure(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.NewPassword) < 6 {
		writeError(w, http.StatusBadRequest, "new password must be at least 6 characters")
		return
	}

	storedHash, _ := s.store.GetSettingDefault(ctx, model.SettingAdminPasswordHash, "")
	ok, _ := auth.VerifyPassword(req.CurrentPassword, storedHash)
	if !ok {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetSetting(ctx, model.SettingAdminPasswordHash, newHash); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", "admin password changed")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	username := usernameFromContext(ctx)
	if username == "" {
		username, _ = s.store.GetSettingDefault(ctx, model.SettingAdminUsername, "admin")
	}
	instanceID, _ := s.store.GetSettingDefault(ctx, model.SettingInstanceID, "")
	account := username
	if instanceID != "" {
		account = username + "@" + instanceID
	}

	secret, otpURL, err := auth.GenerateTOTP("sbox-panel", account)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate TOTP secret")
		return
	}
	if err := s.store.SetSetting(ctx, model.SettingTOTPPendingSecret, secret); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist pending TOTP secret")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"secret":      secret,
		"otpauth_url": otpURL,
	})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	pending, ok, err := s.store.GetSetting(ctx, model.SettingTOTPPendingSecret)
	if err != nil || !ok || strings.TrimSpace(pending) == "" {
		writeError(w, http.StatusBadRequest, "TOTP setup has not been initiated")
		return
	}

	if !auth.ValidateTOTP(req.Code, pending) {
		writeError(w, http.StatusBadRequest, "invalid 2FA verification code")
		return
	}

	if err := s.store.SetSettings(ctx, map[string]string{
		model.SettingTOTPSecret:        pending,
		model.SettingTOTPEnabled:       "true",
		model.SettingTOTPPendingSecret: "",
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enable TOTP")
		return
	}

	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", "TOTP 2FA enabled")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "totp_enabled": true})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	storedHash, _ := s.store.GetSettingDefault(ctx, model.SettingAdminPasswordHash, "")
	passOK, _ := auth.VerifyPassword(req.Password, storedHash)
	if !passOK {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	secret, _ := s.store.GetSettingDefault(ctx, model.SettingTOTPSecret, "")
	if secret != "" && !auth.ValidateTOTP(req.Code, secret) {
		writeError(w, http.StatusBadRequest, "invalid 2FA verification code")
		return
	}

	if err := s.store.SetSettings(ctx, map[string]string{
		model.SettingTOTPEnabled:       "false",
		model.SettingTOTPSecret:        "",
		model.SettingTOTPPendingSecret: "",
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to disable TOTP")
		return
	}

	if s.telemetry != nil {
		s.telemetry.PublishLog("info", "panel", "TOTP 2FA disabled")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "totp_enabled": false})
}
