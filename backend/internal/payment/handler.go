package payment

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// PaymentHandler is the HTTP layer for FASE 4 payment endpoints.
type PaymentHandler struct {
	svc     *PaymentService
	repo    PaymentObligationRepository
	booking BookingReader
	logger  *zap.Logger
}

// NewPaymentHandler wires the handler. booking may be shared with the service.
func NewPaymentHandler(svc *PaymentService, repo PaymentObligationRepository, booking BookingReader, logger *zap.Logger) *PaymentHandler {
	return &PaymentHandler{svc: svc, repo: repo, booking: booking, logger: logger}
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

func paymentMeta() gin.H {
	return gin.H{
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
		"request_id": uuid.NewString(),
	}
}

// InitiatePayments handles POST /bookings/{booking_id}/payments.
//
// Headers: Authorization (JWT), X-Idempotency-Key (required).
func (h *PaymentHandler) InitiatePayments(c *gin.Context) {
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

	bookingID, err := uuid.Parse(c.Param("booking_id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid booking_id")
		return
	}

	idemKey := c.GetHeader("X-Idempotency-Key")
	if idemKey == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "X-Idempotency-Key header is required")
		return
	}

	var req InitiatePaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	resp, err := h.svc.InitiatePayments(c.Request.Context(), userID, bookingID, &req, idemKey)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidPaymentMode):
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "payment_mode must be SPLIT or BULK")
		case errors.Is(err, ErrInvalidMembers):
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid members payload")
		case errors.Is(err, ErrNotLeader):
			writeError(c, http.StatusForbidden, "FORBIDDEN", "caller is not the booking leader")
		case errors.Is(err, ErrBookingNotFound):
			writeError(c, http.StatusNotFound, "NOT_FOUND", "booking not found")
		case errors.Is(err, ErrBookingNotPending):
			writeError(c, http.StatusConflict, "CONFLICT", "booking is not pending")
		default:
			h.logger.Error("initiate payments failed", zap.Error(err))
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
		"meta":    paymentMeta(),
	})
}

// GetBookingStatus handles GET /bookings/{booking_id}/status.
//
// IDOR safety (ROADMAP FASE 4 §6.1): caller must be either
//
//	(a) the booking leader, OR
//	(b) an obligation holder (member).
//
// Others get 403.
func (h *PaymentHandler) GetBookingStatus(c *gin.Context) {
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

	bookingID, err := uuid.Parse(c.Param("booking_id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid booking_id")
		return
	}

	ctx := c.Request.Context()
	obligations, err := h.repo.ListByBooking(ctx, bookingID)
	if err != nil {
		h.logger.Error("list obligations failed", zap.Error(err))
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if len(obligations) == 0 {
		writeError(c, http.StatusNotFound, "NOT_FOUND", "booking not found")
		return
	}

	isMember := false
	for _, o := range obligations {
		if o.UserID == userID {
			isMember = true
			break
		}
	}

	if !isMember {
		// Not a member — must be leader to view.
		bk, bkErr := h.booking.GetByID(ctx, bookingID)
		if bkErr != nil || bk.LeaderUserID != userID {
			writeError(c, http.StatusForbidden, "FORBIDDEN", "caller is neither leader nor member")
			return
		}
	}

	resp := buildStatusResponse(bookingID, obligations)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
		"meta":    paymentMeta(),
	})
}

func buildStatusResponse(bookingID uuid.UUID, obs []PaymentObligation) *BookingStatusResponse {
	out := make([]ObligationResponse, 0, len(obs))
	allPaid := true
	anyTicketGen := false

	for i := range obs {
		o := &obs[i]
		r := ObligationResponse{
			UserID: o.UserID.String(),
			Amount: o.Amount,
			Status: o.Status,
		}
		if o.SeatPosition != nil {
			sp := *o.SeatPosition
			r.SeatPosition = &sp
		}
		if o.PaymentLink != nil {
			r.PaymentLink = *o.PaymentLink
		}
		if o.ExternalTransactionID != nil {
			r.ExternalTransactionID = *o.ExternalTransactionID
		}
		out = append(out, r)

		if o.Status != StatusPaid {
			allPaid = false
		}
	}
	// tickets_generated = true when the whole group is paid (trigger fires).
	anyTicketGen = allPaid

	bookingStatus := StatusPending
	if allPaid {
		bookingStatus = StatusPaid
	}

	return &BookingStatusResponse{
		BookingID:        bookingID.String(),
		PaymentStatus:    bookingStatus,
		Obligations:      out,
		TicketsGenerated: anyTicketGen,
	}
}
