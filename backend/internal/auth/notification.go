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
// the application log instead of sending it through a provider. Plaintext OTP
// is only emitted when the environment is "development" or "test"; every other
// environment logs a redacted code so credentials never leak into production
// logs. A real SMS/Email provider (Twilio/SendGrid) must be wired for prod.
type LogNotification struct {
	logger     *zap.Logger
	exposeCode bool
}

func NewLogNotification(logger *zap.Logger, environment string) *LogNotification {
	exposeCode := environment == "development" || environment == "test"
	return &LogNotification{logger: logger, exposeCode: exposeCode}
}

func (n *LogNotification) SendOTP(_ context.Context, userID uuid.UUID, email string, code string) error {
	loggedCode := "******"
	if n.exposeCode {
		loggedCode = code
	}
	n.logger.Info("OTP issued (log fallback)",
		zap.String("user_id", userID.String()),
		zap.String("email", maskEmail(email)),
		zap.String("code", loggedCode),
		zap.Bool("code_redacted", !n.exposeCode),
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
