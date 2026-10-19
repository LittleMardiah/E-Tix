package gate

import (
	"errors"
	"net/http"
	"strings"

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

// VerifyPIN handles POST /gate/verify-pin.
func (h *GateHandler) VerifyPIN(c *gin.Context) {
	userIDStr := c.GetString("user_id")
	scannerUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user context")
		return
	}

	var req VerifyPINRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}

	result, err := h.svc.VerifyPIN(c.Request.Context(), scannerUserID, &req)
	if err != nil {
		switch {
		case errors.Is(err, ErrTicketNotFound):
			// Wrong PIN → spec §11.2 treats as 401 (invalid credential).
			writeError(c, http.StatusUnauthorized, "INVALID_PIN", "invalid pin")
		case errors.Is(err, ErrTicketMismatch):
			writeError(c, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", "ticket_id does not match pin owner")
		case errors.Is(err, ErrAlreadyUsed):
			writeError(c, http.StatusConflict, "CONFLICT", "ticket already used")
		case errors.Is(err, ErrTicketNotActive):
			writeError(c, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", "ticket not active")
		default:
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal error")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result, "error": nil})
}

// SyncOfflineScans handles POST /gate/sync.
func (h *GateHandler) SyncOfflineScans(c *gin.Context) {
	// Enforce 1MB body limit (SECURITY_SPEC 7.2) — no middleware present.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)

	userIDStr := c.GetString("user_id")
	scannerUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user context")
		return
	}

	var req SyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) || strings.Contains(err.Error(), "request body too large") {
			writeError(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "body exceeds 1MB")
			return
		}
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}

	if len(req.Scans) > 500 {
		writeError(c, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", "batch exceeds 500 scans")
		return
	}

	result, err := h.svc.SyncOfflineScans(c.Request.Context(), scannerUserID, &req)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal error")
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result, "error": nil})
}
