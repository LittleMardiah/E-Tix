package admin

import (
	"fmt"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// FASE 8 P8c — TOTP service (RFC 6238).
// Wraps pquerna/otp. No Redis — that's middleware.go concern.

// TwoFAService generates and validates TOTP codes.
type TwoFAService struct {
	issuer string // "E-Tix Flash" for QR display
}

func NewTwoFAService(issuer string) *TwoFAService {
	if issuer == "" {
		issuer = "E-Tix Flash"
	}
	return &TwoFAService{issuer: issuer}
}

// EnrollResult contains the enrollment material for QR display.
// Secret = base32-encoded shared secret. URL = otpauth:// for QR scanner.
type EnrollResult struct {
	Secret string
	URL    string
}

// GenerateSecret creates a new TOTP secret for a user.
// Account name = user email. Issuer = "E-Tix Flash".
// Default params: SHA1, 6 digits, 30s period (Google Authenticator compatible).
func (s *TwoFAService) GenerateSecret(accountName string) (*EnrollResult, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuer,
		AccountName: accountName,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return nil, fmt.Errorf("admin: totp generate: %w", err)
	}
	return &EnrollResult{
		Secret: key.Secret(),
		URL:    key.URL(),
	}, nil
}

// Validate checks a 6-digit code against the stored secret.
// Uses totp.Validate which internally compares with constant-time
// (hmac.Equal) and allows ±1 time-step skew (30s default).
func (s *TwoFAService) Validate(code, secret string) bool {
	if code == "" || secret == "" {
		return false
	}
	return totp.Validate(code, secret)
}
