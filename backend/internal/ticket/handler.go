package ticket

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TicketHandler holds HTTP handlers for ticket endpoints.
type TicketHandler struct {
	svc *TicketService
}

// NewTicketHandler constructs a TicketHandler.
func NewTicketHandler(svc *TicketService) *TicketHandler {
	return &TicketHandler{svc: svc}
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

func ticketMeta() gin.H {
	return gin.H{
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
		"request_id": uuid.NewString(),
	}
}

// List handles GET /tickets.
func (h *TicketHandler) List(c *gin.Context) {
	ownerIDStr := c.GetString("user_id")
	if ownerIDStr == "" {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication context")
		return
	}
	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id")
		return
	}

	statusFilter := c.Query("status")
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid page")
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid limit")
		return
	}

	items, total, err := h.svc.ListByOwner(c.Request.Context(), ownerID, statusFilter, page, limit)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidStatusFilter):
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid status filter")
		case errors.Is(err, ErrTicketNotFound):
			writeError(c, http.StatusNotFound, "NOT_FOUND", "ticket not found")
		default:
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tickets": items,
			"total":   total,
			"page":    page,
			"limit":   limit,
		},
		"error": nil,
		"meta":  ticketMeta(),
	})
}

// GetByID handles GET /tickets/{ticket_id}.
func (h *TicketHandler) GetByID(c *gin.Context) {
	ownerIDStr := c.GetString("user_id")
	if ownerIDStr == "" {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication context")
		return
	}
	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id")
		return
	}

	ticketID, err := uuid.Parse(c.Param("ticket_id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid ticket id")
		return
	}

	td, err := h.svc.GetByID(c.Request.Context(), ownerID, ticketID)
	if err != nil {
		switch {
		case errors.Is(err, ErrTicketNotFound):
			writeError(c, http.StatusNotFound, "NOT_FOUND", "ticket not found")
		case errors.Is(err, ErrTicketForbidden):
			writeError(c, http.StatusForbidden, "FORBIDDEN", "not ticket owner")
		default:
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    td,
		"error":   nil,
		"meta":    ticketMeta(),
	})
}

// GetQR handles GET /tickets/{ticket_id}/qr.
func (h *TicketHandler) GetQR(c *gin.Context) {
	ownerIDStr := c.GetString("user_id")
	if ownerIDStr == "" {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication context")
		return
	}
	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id")
		return
	}

	ticketID, err := uuid.Parse(c.Param("ticket_id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid ticket id")
		return
	}

	resp, err := h.svc.GetQR(c.Request.Context(), ownerID, ticketID)
	if err != nil {
		switch {
		case errors.Is(err, ErrTicketNotFound):
			writeError(c, http.StatusNotFound, "NOT_FOUND", "ticket not found")
		case errors.Is(err, ErrTicketForbidden):
			writeError(c, http.StatusForbidden, "FORBIDDEN", "not ticket owner")
		case errors.Is(err, ErrTicketNotActive):
			writeError(c, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", "ticket not active for QR")
		default:
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
		"meta":    ticketMeta(),
	})
}
