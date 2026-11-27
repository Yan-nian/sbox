package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// CookieName is the HttpOnly session cookie storing the signed JWT.
	CookieName = "sbox_token"
	// DefaultTokenTTL is the default validity period of an admin session token.
	DefaultTokenTTL = 7 * 24 * time.Hour
)

// Claims defines the JWT payload for authenticated panel admins.
type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// TokenManager issues and verifies HS256 JWT tokens.
type TokenManager struct {
	mu     sync.RWMutex
	secret []byte
	ttl    time.Duration
}

// NewTokenManager constructs a TokenManager with the provided HMAC secret and optional TTL.
func NewTokenManager(secret string, ttl time.Duration) *TokenManager {
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	return &TokenManager{
		secret: []byte(secret),
		ttl:    ttl,
	}
}

// SetSecret updates the HMAC signing secret atomically (used after backup restore).
func (tm *TokenManager) SetSecret(secret string) {
	if secret == "" {
		return
	}
	tm.mu.Lock()
	tm.secret = []byte(secret)
	tm.mu.Unlock()
}

func (tm *TokenManager) getSecret() []byte {
	tm.mu.RLock()
	sec := append([]byte(nil), tm.secret...)
	tm.mu.RUnlock()
	return sec
}

// IssueToken creates a signed HS256 JWT for username.
func (tm *TokenManager) IssueToken(username string) (string, error) {
	sec := tm.getSecret()
	if len(sec) == 0 {
		return "", errors.New("empty jwt secret")
	}
	now := time.Now().UTC()
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "sbox-panel",
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tm.ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(sec)
}

// VerifyToken validates signature, algorithm, and expiry, returning parsed Claims.
func (tm *TokenManager) VerifyToken(tokenStr string) (*Claims, error) {
	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" {
		return nil, errors.New("missing token")
	}
	sec := tm.getSecret()
	parsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok || t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return sec, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

// TTL returns the configured token duration.
func (tm *TokenManager) TTL() time.Duration {
	return tm.ttl
}

// ExtractToken retrieves a JWT from Authorization Bearer header, Cookie, or query parameter `token`.
func ExtractToken(r *http.Request) string {
	if authz := r.Header.Get("Authorization"); authz != "" {
		if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
			return strings.TrimSpace(authz[7:])
		}
	}
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return strings.TrimSpace(c.Value)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("token")); q != "" {
		return q
	}
	return ""
}

// SetAuthCookie writes an HttpOnly SameSite=Lax JWT session cookie.
func SetAuthCookie(w http.ResponseWriter, token, basePath string, secure bool, ttl time.Duration) {
	cookiePath := "/"
	if basePath != "" && basePath != "/" {
		cookiePath = basePath
	}
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     cookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
		Expires:  time.Now().UTC().Add(ttl),
	})
}

// ClearAuthCookie expires the JWT session cookie for both the active base path and root.
func ClearAuthCookie(w http.ResponseWriter, basePath string, secure bool) {
	paths := []string{"/"}
	if basePath != "" && basePath != "/" {
		paths = append(paths, basePath)
	}
	for _, p := range paths {
		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Value:    "",
			Path:     p,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0).UTC(),
		})
	}
}
