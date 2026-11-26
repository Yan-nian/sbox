package auth

import (
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// GenerateTOTP creates a new TOTP secret and otpauth:// provisioning URL.
func GenerateTOTP(issuer, accountName string) (secret string, otpauthURL string, err error) {
	if strings.TrimSpace(issuer) == "" {
		issuer = "sbox-panel"
	}
	if strings.TrimSpace(accountName) == "" {
		accountName = "admin"
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", fmt.Errorf("generate totp key: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTP validates a 6-digit TOTP code against secret with standard ±1 time-step skew.
func ValidateTOTP(code, secret string) bool {
	return ValidateTOTPAt(code, secret, time.Now().UTC())
}

// ValidateTOTPAt validates a TOTP code at a specific timestamp (useful for deterministic tests).
func ValidateTOTPAt(code, secret string, t time.Time) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	secret = strings.TrimSpace(secret)
	if code == "" || secret == "" {
		return false
	}
	ok, err := totp.ValidateCustom(code, secret, t, totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}
