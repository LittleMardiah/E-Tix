package auth

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// OTPNotification dispatches OTP codes to users. Implementations may use an
// SMS/Email provider (Twilio/SendGrid); LogNotification is the dev fallback.
type OTPNotification interface {
	SendOTP(ctx context.Context, userID uuid.UUID, email string, code string) error
}

// LogNotification is a development-only OTPNotification that writes the OTP to
// the application log instead of sending it through a provider.
type LogNotification struct {
	logger *zap.Logger
}

func NewLogNotification(logger *zap.Logger) *LogNotification {
	return &LogNotification{logger: logger}
}

func (n *LogNotification) SendOTP(ctx context.Context, userID uuid.UUID, email string, code string) error {
	// TODO(prod): OTP code is logged in plaintext for dev only. Remove/mask
	// this field and wire a real SMS/Email provider (Twilio/SendGrid).
	n.logger.Info("OTP sent (dev mode)",
		zap.String("user_id", userID.String()),
		zap.String("email", maskEmail(email)),
		zap.String("code", code),
	)
	return nil
}

func maskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}
