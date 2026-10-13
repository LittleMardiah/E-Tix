package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const (
	idempotencyKeyHeader      = "X-Idempotency-Key"
	registerIdempotencyPrefix = "idempotency:auth:register:"
	registerIdempotencyTTL    = 24 * time.Hour
)

type cachedResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

type AuthHandler struct {
	svc   *AuthService
	redis *redis.Client
}

func NewAuthHandler(svc *AuthService, rdb *redis.Client) *AuthHandler {
	return &AuthHandler{svc: svc, redis: rdb}
}

func (h *AuthHandler) Register(c *gin.Context) {
	idempotencyKey := c.GetHeader(idempotencyKeyHeader)
	if idempotencyKey == "" {
		c.JSON(http.StatusBadRequest, errorResponse("INVALID_REQUEST", "X-Idempotency-Key header is required"))
		return
	}

	ctx := c.Request.Context()
	cacheKey := registerIdempotencyPrefix + idempotencyKey

	if cached, err := h.redis.GetString(ctx, cacheKey); err == nil && cached != "" {
		var stored cachedResponse
		if jsonErr := json.Unmarshal([]byte(cached), &stored); jsonErr == nil && len(stored.Body) > 0 {
			c.Data(stored.Status, gin.MIMEJSON, stored.Body)
			return
		}
	}

	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("INVALID_REQUEST", "invalid request body"))
		return
	}

	resp, err := h.svc.Register(ctx, &req)
	if err != nil {
		writeRegisterError(c, err)
		return
	}

	envelope := gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
		"meta": gin.H{
			"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
			"request_id": uuid.NewString(),
		},
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse("INTERNAL_ERROR", "internal server error"))
		return
	}

	c.Data(http.StatusCreated, gin.MIMEJSON, body)

	if cached, marshalErr := json.Marshal(cachedResponse{Status: http.StatusCreated, Body: body}); marshalErr == nil {
		_ = h.redis.Set(ctx, cacheKey, string(cached), registerIdempotencyTTL).Err()
	}
}

func writeRegisterError(c *gin.Context, err error) {
	var validationErr *ValidationError
	switch {
	case errors.As(err, &validationErr):
		c.JSON(http.StatusBadRequest, errorResponse("INVALID_REQUEST", validationErr.Error()))
	case errors.Is(err, ErrEmailExists):
		c.JSON(http.StatusConflict, errorResponse("EMAIL_EXISTS", "email already registered"))
	case errors.Is(err, ErrPhoneExists):
		c.JSON(http.StatusConflict, errorResponse("PHONE_EXISTS", "phone already registered"))
	default:
		c.JSON(http.StatusInternalServerError, errorResponse("INTERNAL_ERROR", "internal server error"))
	}
}

func errorResponse(code, message string) gin.H {
	return gin.H{
		"success": false,
		"data":    nil,
		"error": gin.H{
			"code":    code,
			"message": message,
		},
		"meta": gin.H{
			"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
			"request_id": uuid.NewString(),
		},
	}
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("INVALID_REQUEST", "invalid request body"))
		return
	}

	resp, err := h.svc.Login(c.Request.Context(), &req)
	if err != nil {
		writeLoginError(c, err)
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

func writeLoginError(c *gin.Context, err error) {
	var validationErr *ValidationError
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, errorResponse("UNAUTHORIZED", "invalid email or password"))
	case errors.As(err, &validationErr):
		c.JSON(http.StatusBadRequest, errorResponse("INVALID_REQUEST", validationErr.Error()))
	case errors.Is(err, ErrForbidden):
		c.JSON(http.StatusForbidden, errorResponse("FORBIDDEN", "account is not active"))
	default:
		c.JSON(http.StatusInternalServerError, errorResponse("INTERNAL_ERROR", "internal server error"))
	}
}
