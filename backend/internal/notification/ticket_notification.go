package notification

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TicketNotification dispatches ticket fallback PINs to users.
type TicketNotification interface {
	SendFallbackPIN(ctx context.Context, userID uuid.UUID, email, phone, pin string) error
}

// LogTicketNotification is the dev-only implementation.
// E5-S1-T12: PIN is intentionally NOT logged (unlike OTP).
type LogTicketNotification struct {
	logger *zap.Logger
}

// NewLogTicketNotification constructs a LogTicketNotification.
func NewLogTicketNotification(logger *zap.Logger) *LogTicketNotification {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &LogTicketNotification{logger: logger}
}

// SendFallbackPIN records a dispatch event. PIN value is NOT logged.
func (n *LogTicketNotification) SendFallbackPIN(ctx context.Context, userID uuid.UUID, email, phone, pin string) error {
	if pin == "" {
		return nil
	}
	n.logger.Info("ticket fallback PIN dispatched (dev mode)",
		zap.String("user_id", userID.String()),
		zap.String("email", maskEmail(email)),
		zap.String("phone", maskPhone(phone)),
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

func maskPhone(phone string) string {
	if len(phone) < 4 {
		return "***"
	}
	return phone[:2] + "***" + phone[len(phone)-2:]
}
