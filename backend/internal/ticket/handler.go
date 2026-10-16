package ticket

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
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

const assignIdempotencyTTL = 24 * time.Hour

// Assign handles PATCH /tickets/{ticket_id}/assign.
// Requires X-Idempotency-Key (L1 Redis, 24h TTL). Cache key = sha256(clientKey + ":" + ownerID + ":" + ticketID).
func (h *TicketHandler) Assign(c *gin.Context) {
	idemKey := c.GetHeader("X-Idempotency-Key")
	if idemKey == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "X-Idempotency-Key required")
		return
	}

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

	var req AssignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}
	if req.UserID == uuid.Nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "user_id required")
		return
	}

	sum := sha256.Sum256([]byte(idemKey + ":" + ownerID.String() + ":" + ticketID.String()))
	cacheKey := "idem:assign:" + hex.EncodeToString(sum[:])

	if cached, _ := h.svc.CacheGet(c.Request.Context(), cacheKey); cached != "" {
		c.Data(http.StatusOK, "application/json", []byte(cached))
		return
	}

	ticket, err := h.svc.Assign(c.Request.Context(), ownerID, ticketID, req.UserID)
	if err != nil {
		switch {
		case errors.Is(err, ErrTicketNotFound):
			writeError(c, http.StatusNotFound, "NOT_FOUND", "ticket not found")
		case errors.Is(err, ErrTicketForbidden):
			writeError(c, http.StatusForbidden, "FORBIDDEN", "not ticket owner")
		case errors.Is(err, ErrTicketAlreadyAssigned), errors.Is(err, ErrAssignConflict):
			writeError(c, http.StatusConflict, "CONFLICT", "ticket already assigned")
		case errors.Is(err, ErrTicketNotConfirmed):
			writeError(c, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", "ticket not confirmed")
		case errors.Is(err, ErrTargetUserInactive):
			writeError(c, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY", "target user inactive or blacklisted")
		default:
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	resp := gin.H{"success": true, "data": ticket, "error": nil, "meta": ticketMeta()}
	body, _ := json.Marshal(resp)
	if err := h.svc.CacheSet(c.Request.Context(), cacheKey, string(body), assignIdempotencyTTL); err != nil {
		zap.L().Warn("assign: cache set failed", zap.Error(err))
	}

	c.Data(http.StatusOK, "application/json", body)
}
