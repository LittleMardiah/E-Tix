package gate

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/gin-gonic/gin"
)

// GateHandler holds HTTP handlers for gate endpoints.
type GateHandler struct {
	svc *GateService
}

// NewGateHandler constructs a GateHandler.
func NewGateHandler(svc *GateService) *GateHandler {
	return &GateHandler{svc: svc}
}

func writeError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"success": false,
		"data":    nil,
		"error":   gin.H{"code": code, "message": message},
	})
}

// ScanQR handles POST /gate/scan-qr.
func (h *GateHandler) ScanQR(c *gin.Context) {
	userIDStr := c.GetString("user_id")
	scannerUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user context")
		return
	}

	var req ScanQRRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}

	result, err := h.svc.ScanQR(c.Request.Context(), scannerUserID, &req)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidQR):
			writeError(c, http.StatusUnauthorized, "INVALID_QR_OR_TICKET_MISMATCH", "invalid qr")
		case errors.Is(err, ErrAlreadyUsed):
			writeError(c, http.StatusConflict, "CONFLICT", "ticket already used")
		case errors.Is(err, ErrTicketNotActive):
			writeError(c, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", "ticket not active")
		case errors.Is(err, ErrTicketNotFound):
			writeError(c, http.StatusNotFound, "NOT_FOUND", "ticket not found")
		default:
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal error")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result, "error": nil})
}
