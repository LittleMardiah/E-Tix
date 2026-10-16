package ticket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
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
		return "", fmt.Errorf("ticket: qr_secret_key is required")
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
