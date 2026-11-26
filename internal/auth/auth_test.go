package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("s3cur3-P@ssw0rd!")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := VerifyPassword("s3cur3-P@ssw0rd!", hash)
	if err != nil || !ok {
		t.Fatalf("expected Argon2id password verify success, ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong-password", hash)
	if err != nil || ok {
		t.Fatalf("expected Argon2id password verify false, ok=%v err=%v", ok, err)
	}

	// Verify bcrypt compatibility
	bHash, err := bcrypt.GenerateFromPassword([]byte("legacy-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	ok, err = VerifyPassword("legacy-pass", string(bHash))
	if err != nil || !ok {
		t.Fatalf("expected bcrypt verify success, ok=%v err=%v", ok, err)
	}
}

func TestJWTIssueVerifyAndExtract(t *testing.T) {
	tm := NewTokenManager("super-secret-hmac-key-for-tests", time.Hour)
	token, err := tm.IssueToken("admin")
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	claims, err := tm.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if claims.Username != "admin" {
		t.Fatalf("expected username=admin, got %q", claims.Username)
	}

	// Wrong secret must fail
	other := NewTokenManager("different-secret", time.Hour)
	if _, err := other.VerifyToken(token); err == nil {
		t.Fatalf("expected verification error with wrong secret")
	}

	// Bearer extraction
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if got := ExtractToken(req); got != token {
		t.Fatalf("ExtractToken Bearer mismatch: %q", got)
	}

	// Cookie extraction
	rec := httptest.NewRecorder()
	SetAuthCookie(rec, token, "/sbox-1234", false, time.Hour)
	req2 := httptest.NewRequest("GET", "/sbox-1234/api/v1/auth/status", nil)
	for _, c := range rec.Result().Cookies() {
		req2.AddCookie(c)
	}
	if got := ExtractToken(req2); got != token {
		t.Fatalf("ExtractToken Cookie mismatch: %q", got)
	}
}

func TestLoginRateLimiter(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	rl := NewLoginRateLimiter(3, 5*time.Minute, 10*time.Minute)
	rl.SetClock(func() time.Time { return now })

	ip := "203.0.113.10"
	if ok, _ := rl.Allow(ip); !ok {
		t.Fatalf("expected initial Allow=true")
	}

	// 2 failures still allowed
	if blocked, _ := rl.RecordFailure(ip); blocked {
		t.Fatalf("should not block on 1st failure")
	}
	if blocked, _ := rl.RecordFailure(ip); blocked {
		t.Fatalf("should not block on 2nd failure")
	}
	if ok, _ := rl.Allow(ip); !ok {
		t.Fatalf("should still allow before 3rd failure")
	}

	// 3rd failure blocks
	blocked, retry := rl.RecordFailure(ip)
	if !blocked || retry <= 0 {
		t.Fatalf("expected block on 3rd failure, blocked=%v retry=%v", blocked, retry)
	}
	if ok, rem := rl.Allow(ip); ok || rem <= 0 {
		t.Fatalf("expected Allow=false while blocked, ok=%v rem=%v", ok, rem)
	}

	// Advance past block duration
	now = now.Add(11 * time.Minute)
	if ok, _ := rl.Allow(ip); !ok {
		t.Fatalf("expected Allow=true after block expires")
	}

	// Reset clears failures
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)
	rl.Reset(ip)
	if blocked, _ := rl.RecordFailure(ip); blocked {
		t.Fatalf("expected Reset to clear previous attempts")
	}
}

func TestTOTPGenerateAndValidate(t *testing.T) {
	secret, url, err := GenerateTOTP("sbox-panel", "admin")
	if err != nil {
		t.Fatalf("GenerateTOTP: %v", err)
	}
	if secret == "" || url == "" {
		t.Fatalf("empty TOTP secret or URL")
	}

	now := time.Now().UTC()
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !ValidateTOTPAt(code, secret, now) {
		t.Fatalf("expected valid TOTP code %q to pass", code)
	}
	if ValidateTOTPAt("000000", secret, now) && code != "000000" {
		t.Fatalf("expected invalid TOTP code to fail")
	}
}
