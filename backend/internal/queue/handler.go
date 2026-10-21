package queue

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// QueueHandler exposes the queue HTTP endpoints.
type QueueHandler struct {
	svc    *QueueService
	logger *zap.Logger
}

func NewQueueHandler(svc *QueueService, logger *zap.Logger) *QueueHandler {
	return &QueueHandler{svc: svc, logger: logger}
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

func queueMeta() gin.H {
	return gin.H{
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
		"request_id": uuid.NewString(),
	}
}

func (h *QueueHandler) GetStatus(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication context")
		return
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id")
		return
	}

	eventIDStr := c.Query("event_id")
	if eventIDStr == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "event_id is required")
		return
	}

	eid, err := uuid.Parse(eventIDStr)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid event id")
		return
	}

	// H1 FIX: if IngressMiddleware already generated a queue token for this
	// request, reuse it (and skip a second Generate). Falls back to service
	// when middleware is not in the chain (unit tests, alternative routes).
	resp, err := h.svc.GetStatus(c.Request.Context(), uid, eid)
	if err != nil {
		switch {
		case errors.Is(err, ErrEventNotOnSale):
			writeError(c, http.StatusNotFound, "NOT_FOUND", "event is not on sale")
		default:
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	// Prefer middleware-generated token (single source, avoids double Generate).
	if ctxToken := c.GetString("queue_token"); ctxToken != "" {
		resp.QueueToken = ctxToken
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
		"meta":    queueMeta(),
	})
}

func (h *QueueHandler) VerifyCaptcha(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication context")
		return
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id")
		return
	}

	var req VerifyCaptchaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	if req.ChallengeID == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "challenge_id is required")
		return
	}

	// FASE 9 P9a-7 TD-F2-02: failed-attempt lockout. Brute-forcing
	// /queue/verify-captcha from one IP is blocked for 15m after 3 failed
	// verifications (pattern: admin 2FA lockout, internal/admin/middleware.go).
	clientIP := c.ClientIP()
	if locked, ttl := h.svc.CaptchaLockoutState(c.Request.Context(), clientIP); locked {
		writeError(c, http.StatusTooManyRequests, "CAPTCHA_LOCKED",
			fmt.Sprintf("too many failed captcha attempts, retry in %ds", int(ttl.Seconds())))
		return
	}

	resp, err := h.svc.VerifyCaptcha(c.Request.Context(), uid, &req)
	if err != nil {
		switch {
		case errors.Is(err, ErrCaptchaInvalid):
			if _, locked := h.svc.RecordCaptchaFailure(c.Request.Context(), clientIP); locked {
				writeError(c, http.StatusTooManyRequests, "CAPTCHA_LOCKED",
					fmt.Sprintf("too many failed captcha attempts, locked for %ds", int(CaptchaLockoutTTL.Seconds())))
				return
			}
			writeError(c, http.StatusUnauthorized, "INVALID_REQUEST", "invalid captcha")
		default:
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}
		return
	}

	h.svc.ClearCaptchaFailures(c.Request.Context(), clientIP)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
		"meta":    queueMeta(),
	})
}
