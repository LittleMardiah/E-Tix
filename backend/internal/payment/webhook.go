package payment

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// maxWebhookBody caps inbound webhook payload size (defensive).
const maxWebhookBody = 1 << 20 // 1 MiB

// HandleWebhook handles POST /webhook/payment.
//
// No auth middleware — signature validation is the auth. Rate limit is
// not applied per API_CONTRACT §9.5 ("Unlimited — trusted source").
func (h *PaymentHandler) HandleWebhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBody))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "cannot read body")
		return
	}
	if len(body) == 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "empty body")
		return
	}

	// Dual-source signature extraction: headers primary, body fallback
	// (handled by the adapter via payload inspection).
	headers := map[string]string{
		"X-Gateway-Signature":  c.GetHeader("X-Gateway-Signature"),
		"X-Midtrans-Signature": c.GetHeader("X-Midtrans-Signature"),
	}

	if err := h.svc.HandleWebhook(c.Request.Context(), body, headers); err != nil {
		switch {
		case errors.Is(err, ErrWebhookInvalidSig):
			writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid webhook signature")
		case errors.Is(err, ErrWebhookNotFound):
			writeError(c, http.StatusNotFound, "NOT_FOUND", "transaction not found")
		default:
			h.logger.Error("webhook handle failed", zap.Error(err))
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"status": "processed"},
		"error":   nil,
		"meta":    paymentMeta(),
	})
}
