package admin

import "time"

// FASE 8 P8b — 2FA lockout constants.
// Policy per SECURITY_SPEC v1.4 §6.3 + ROADMAP FASE 8 FLAG #1.
// Storage: Redis (admin_2fa:* keyspace). Enrollment CLI-only.

const (
	// --- Redis key patterns (fmt.Printf style: %s = user_id) ---
	KeyAdmin2FASecret  = "admin_2fa:%s"         // TOTP secret (persistent)
	KeyAdmin2FAFails   = "admin_2fa_fails:%s"   // failure counter
	KeyAdmin2FALockout = "admin_2fa_lockout:%s" // lockout flag (SET when fails>=3)

	// --- Lockout policy ---
	Admin2FAMaxFails   = 3                // failures before lockout
	Admin2FAFailsTTL   = 15 * time.Minute // counter TTL (rolling window)
	Admin2FALockoutTTL = 15 * time.Minute // lockout duration

	// --- TOTP parameters (RFC 6238) ---
	Admin2FATOTPPeriod    = 30 * time.Second // time-step
	Admin2FATOTPDigits    = 6                // output length
	Admin2FATOTPAlgorithm = "SHA1"           // HMAC-SHA1 (Google Auth default)

	// --- HTTP header ---
	HeaderAdmin2FAToken = "X-Admin-2FA-Token"
)
