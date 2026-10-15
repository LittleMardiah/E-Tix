package booking

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

type BookingHandler struct {
	svc    *BookingService
	rdb    *redis.Client
	logger *zap.Logger
}

func NewBookingHandler(svc *BookingService, rdb *redis.Client, logger *zap.Logger) *BookingHandler {
	return &BookingHandler{svc: svc, rdb: rdb, logger: logger}
}

// H4 FIX: X-Idempotency-Key support for POST /seats. Contract §9.1 requires
// the header; without it, a client-side retry would burn a fresh queue token
// and potentially double-charge. L1-only Redis cache (TTL 24h) mirrors the
// payment module's pattern.
const (
	bookingIdemPrefix = "idempotency:booking:lock:"
	bookingIdemTTL    = 24 * time.Hour
)

type cachedBookingResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

func computeBookingIdemHash(clientKey string, userID, eventID, sectionID uuid.UUID) string {
	h := sha256.New()
	h.Write([]byte(clientKey))
	h.Write([]byte(":"))
	h.Write([]byte(userID.String()))
	h.Write([]byte(":"))
	h.Write([]byte(eventID.String()))
	h.Write([]byte(":"))
	h.Write([]byte(sectionID.String()))
	return hex.EncodeToString(h.Sum(nil))
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

	// H4 FIX: contract §9.1 requires X-Idempotency-Key on POST /seats.
	idemKey := c.GetHeader("X-Idempotency-Key")
	if idemKey == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "X-Idempotency-Key header is required")
		return
	}

	var req LockSeatsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	idemHash := computeBookingIdemHash(idemKey, userID, eventID, sectionID)
	cacheKey := bookingIdemPrefix + idemHash

	// L1 cache: same key + same payload returns identical response.
	if cached, cErr := h.rdb.GetString(c.Request.Context(), cacheKey); cErr == nil && cached != "" {
		var stored cachedBookingResponse
		if jErr := json.Unmarshal([]byte(cached), &stored); jErr == nil && len(stored.Body) > 0 {
			c.Data(stored.Status, gin.MIMEJSON, stored.Body)
			return
		}
	} else if cErr != nil && !errors.Is(cErr, goredis.Nil) {
		h.logger.Warn("booking: idempotency read failed", zap.Error(cErr))
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
		case errors.Is(err, ErrDuplicateSeatPosition):
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "duplicate seat positions")
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

	bodyBytes, mErr := json.Marshal(gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
		"meta": gin.H{
			"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
			"request_id": uuid.NewString(),
		},
	})
	if mErr != nil {
		h.logger.Error("booking: marshal response failed", zap.Error(mErr))
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if cached, mErr := json.Marshal(cachedBookingResponse{
		Status: http.StatusCreated,
		Body:   bodyBytes,
	}); mErr == nil {
		if sErr := h.rdb.Set(c.Request.Context(), cacheKey, string(cached), bookingIdemTTL).Err(); sErr != nil {
			h.logger.Warn("booking: idempotency store failed", zap.Error(sErr))
		}
	}
	c.Data(http.StatusCreated, gin.MIMEJSON, bodyBytes)
}
