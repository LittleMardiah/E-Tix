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
		if errors.Is(err, err) && err.Error() == "booking: section not found" {
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
