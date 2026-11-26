package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ipRecord struct {
	failures     []time.Time
	blockedUntil time.Time
	lastSeen     time.Time
}

// LoginRateLimiter implements an in-memory per-IP sliding-window brute-force limiter.
type LoginRateLimiter struct {
	mu            sync.Mutex
	maxAttempts   int
	window        time.Duration
	blockDuration time.Duration
	records       map[string]*ipRecord
	now           func() time.Time
	lastPrune     time.Time
}

// NewLoginRateLimiter creates a rate limiter. Defaults: 5 failures within 5m triggers a 15m block.
func NewLoginRateLimiter(maxAttempts int, window, blockDuration time.Duration) *LoginRateLimiter {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	if blockDuration <= 0 {
		blockDuration = 15 * time.Minute
	}
	now := time.Now
	return &LoginRateLimiter{
		maxAttempts:   maxAttempts,
		window:        window,
		blockDuration: blockDuration,
		records:       make(map[string]*ipRecord),
		now:           now,
		lastPrune:     now(),
	}
}

// SetClock overrides the time source (used in unit tests).
func (l *LoginRateLimiter) SetClock(fn func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = fn
}

// Allow checks whether an IP is permitted to attempt login right now.
func (l *LoginRateLimiter) Allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.maybePruneLocked(now)

	rec, ok := l.records[ip]
	if !ok {
		return true, 0
	}
	if now.Before(rec.blockedUntil) {
		return false, rec.blockedUntil.Sub(now)
	}
	return true, 0
}

// RecordFailure records a failed authentication attempt and returns whether the IP is now blocked.
func (l *LoginRateLimiter) RecordFailure(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.maybePruneLocked(now)

	rec, ok := l.records[ip]
	if !ok {
		rec = &ipRecord{}
		l.records[ip] = rec
	}
	rec.lastSeen = now

	cutoff := now.Add(-l.window)
	kept := rec.failures[:0]
	for _, ts := range rec.failures {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	rec.failures = kept

	if len(rec.failures) >= l.maxAttempts {
		rec.blockedUntil = now.Add(l.blockDuration)
		rec.failures = nil
		return true, l.blockDuration
	}
	return false, 0
}

// Reset clears failure counters for an IP (e.g., after successful login).
func (l *LoginRateLimiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.records, ip)
}

func (l *LoginRateLimiter) maybePruneLocked(now time.Time) {
	if now.Sub(l.lastPrune) < 5*time.Minute {
		return
	}
	l.lastPrune = now
	staleBefore := now.Add(-(l.window + l.blockDuration))
	for ip, rec := range l.records {
		if now.After(rec.blockedUntil) && rec.lastSeen.Before(staleBefore) {
			delete(l.records, ip)
		}
	}
}

// ClientIP extracts the caller IP address from RemoteAddr (or X-Real-IP / X-Forwarded-For only if loopback proxy).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	parsed := net.ParseIP(host)
	// Only trust forwarding headers when the direct peer is loopback (e.g. local Nginx/Caddy reverse proxy).
	if parsed != nil && parsed.IsLoopback() {
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			parts := strings.Split(xff, ",")
			if first := strings.TrimSpace(parts[0]); first != "" {
				return first
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			return xri
		}
	}
	if host == "" {
		return "unknown"
	}
	return host
}
