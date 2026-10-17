package ticket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// QRTimestampBlockSeconds is the QR signature rotation window.
const QRTimestampBlockSeconds = 30

// GenerateQRData produces the base64-encoded QR payload for a ticket.
//
// Format (decoded): "<ticket_id>|<base64url(HMAC-SHA256(ticket_id|owner|block))>"
// The signature block rotates every QRTimestampBlockSeconds seconds.
// Verifier (E3-S1-T7) accepts ±1 block tolerance.
func GenerateQRData(ticketID, ownerUserID uuid.UUID, qrSecretKey string, now time.Time) (string, error) {
	if qrSecretKey == "" {
		return "", ErrQRSecretKeyRequired
	}
	block := now.Unix() / QRTimestampBlockSeconds
	payload := fmt.Sprintf("%s|%s|%d", ticketID, ownerUserID, block)

	mac := hmac.New(sha256.New, []byte(qrSecretKey))
	if _, err := mac.Write([]byte(payload)); err != nil {
		return "", fmt.Errorf("ticket: hmac write: %w", err)
	}
	signature := base64.URLEncoding.EncodeToString(mac.Sum(nil))

	encoded := base64.URLEncoding.EncodeToString([]byte(ticketID.String() + "|" + signature))
	return encoded, nil
}

// ErrInvalidQR is returned when a QR token fails validation (bad format,
// bad signature, or outside the ±QRToleranceBlocks window).
var ErrInvalidQR = errors.New("ticket: invalid or expired qr")

// ErrQRSecretKeyRequired is returned when GenerateQRData is called with an
// empty secret key.
var ErrQRSecretKeyRequired = errors.New("ticket: qr_secret_key is required")

// QRToleranceBlocks is the ± window (in blocks) accepted for verification.
const QRToleranceBlocks = 1

// VerifyDynamicQR validates a QR payload against owner + secret at time now.
// Accepts ±QRToleranceBlocks window. Returns the decoded ticket UUID on
// success, or ErrInvalidQR on any failure.
func VerifyDynamicQR(qrToken string, ownerUserID uuid.UUID, secretKey string, now time.Time) (uuid.UUID, error) {
	if secretKey == "" {
		return uuid.Nil, ErrInvalidQR
	}
	decoded, err := base64.URLEncoding.DecodeString(qrToken)
	if err != nil {
		return uuid.Nil, ErrInvalidQR
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 2 {
		return uuid.Nil, ErrInvalidQR
	}
	ticketID, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, ErrInvalidQR
	}
	signature := parts[1]

	currentBlock := now.Unix() / QRTimestampBlockSeconds
	for _, b := range []int64{currentBlock - QRToleranceBlocks, currentBlock, currentBlock + QRToleranceBlocks} {
		payload := fmt.Sprintf("%s|%s|%d", ticketID, ownerUserID, b)
		mac := hmac.New(sha256.New, []byte(secretKey))
		if _, err := mac.Write([]byte(payload)); err != nil {
			continue
		}
		expected := base64.URLEncoding.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(signature), []byte(expected)) {
			return ticketID, nil
		}
	}
	return uuid.Nil, ErrInvalidQR
}
