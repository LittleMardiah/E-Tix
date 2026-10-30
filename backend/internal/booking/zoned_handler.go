package booking

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ZonedHandler struct {
	svc    *ZonedService
	logger *zap.Logger
}

func NewZonedHandler(svc *ZonedService, logger *zap.Logger) *ZonedHandler {
	return &ZonedHandler{svc: svc, logger: logger}
}

func (h *ZonedHandler) CheckoutZoned(c *gin.Context) {
	userIDStr := c.GetString("user_id")
	var userID uuid.UUID
	if userIDStr != "" {
		if parsed, err := uuid.Parse(userIDStr); err == nil {
			userID = parsed
		}
	}

	var req ZonedBookingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if userID != uuid.Nil {
		req.UserID = userID
	}
	if req.EventID == uuid.Nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid event_id")
		return
	}
	if req.ZoneName == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "zone_name is required")
		return
	}
	if req.Quantity <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "quantity must be positive")
		return
	}
	if req.UserID == uuid.Nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication context")
		return
	}

	resp, err := h.svc.CheckoutZoned(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrInsufficientQuota):
			writeError(c, http.StatusConflict, "CONFLICT", "insufficient zone quota")
		case errors.Is(err, ErrQuotaNotInitialized):
			writeError(c, http.StatusConflict, "CONFLICT", "zone quota not initialized")
		default:
			h.logger.Error("zoned checkout failed", zap.Error(err))
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
	})
}

func (h *ZonedHandler) ZonedCheckout(c *gin.Context) { h.CheckoutZoned(c) }
func (h *ZonedHandler) BookZoned(c *gin.Context)     { h.CheckoutZoned(c) }
