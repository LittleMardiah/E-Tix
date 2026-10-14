package booking

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type BookingHandler struct {
	svc    *BookingService
	logger *zap.Logger
}

func NewBookingHandler(svc *BookingService, logger *zap.Logger) *BookingHandler {
	return &BookingHandler{svc: svc, logger: logger}
}

func writeError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"success": false,
		"data":    nil,
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}

func (h *BookingHandler) GetSeatAvailability(c *gin.Context) {
	eventIDStr := c.Param("event_id")
	sectionIDStr := c.Param("section_id")
	eventID, err := uuid.Parse(eventIDStr)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid event_id")
		return
	}
	sectionID, err := uuid.Parse(sectionIDStr)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid section_id")
		return
	}

	resp, err := h.svc.GetSeatAvailability(c.Request.Context(), eventID, sectionID)
	if err != nil {
		if errors.Is(err, ErrSectionNotFound) {
			writeError(c, http.StatusNotFound, "NOT_FOUND", "section not found")
			return
		}
		h.logger.Error("get seat availability failed", zap.Error(err))
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
		"meta": gin.H{
			"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
			"request_id": uuid.NewString(),
		},
	})
}

func (h *BookingHandler) LockSeats(c *gin.Context) {
	userIDStr := c.GetString("user_id")
	if userIDStr == "" {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication context")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id")
		return
	}

	eventID, err := uuid.Parse(c.Param("event_id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid event_id")
		return
	}
	sectionID, err := uuid.Parse(c.Param("section_id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid section_id")
		return
	}

	queueToken := c.GetHeader("X-Queue-Token")
	if queueToken == "" {
		writeError(c, http.StatusUnauthorized, "QUEUE_TOKEN_REQUIRED", "X-Queue-Token header is required for booking operations")
		return
	}

	var req LockSeatsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	resp, err := h.svc.LockSeats(c.Request.Context(), userID, eventID, sectionID, &req, queueToken)
	if err != nil {
		switch {
		case errors.Is(err, ErrQueueTokenInvalid):
			writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid queue token")
		case errors.Is(err, ErrTokenAlreadyUsed):
			writeError(c, http.StatusConflict, "TOKEN_ALREADY_USED", "queue token already used")
		case errors.Is(err, ErrSectionNotFound):
			writeError(c, http.StatusNotFound, "NOT_FOUND", "section not found")
		case errors.Is(err, ErrSeatsNotAvailable):
			writeError(c, http.StatusConflict, "CONFLICT", "seats no longer available")
		case errors.Is(err, ErrEmptySeatPositions):
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "seat_positions cannot be empty")
		case errors.Is(err, ErrSeatOutOfRange):
			writeError(c, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", "seat position out of range")
		case errors.Is(err, ErrMaxGroupSeatsExceeded):
			writeError(c, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", "max group seats exceeded")
		case errors.Is(err, ErrInvalidPaymentMode):
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "payment_mode must be SPLIT or BULK")
		default:
			h.logger.Error("lock seats failed", zap.Error(err))
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
		"meta": gin.H{
			"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
			"request_id": uuid.NewString(),
		},
	})
}
