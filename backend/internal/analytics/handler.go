package analytics

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AnalyticsHandler struct {
	svc *AnalyticsService
}

func NewAnalyticsHandler(svc *AnalyticsService) *AnalyticsHandler {
	return &AnalyticsHandler{svc: svc}
}

func writeErr(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"success": false, "data": nil,
		"error": gin.H{"code": code, "message": msg}})
}

func (h *AnalyticsHandler) callerEvent(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	uidStr := c.GetString("user_id")
	uid, err := uuid.Parse(uidStr)
	if err != nil {
		writeErr(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user context")
		return uuid.Nil, uuid.Nil, false
	}
	eid, err := uuid.Parse(c.Param("event_id"))
	if err != nil {
		writeErr(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid event id")
		return uuid.Nil, uuid.Nil, false
	}
	return uid, eid, true
}

func (h *AnalyticsHandler) GetDashboard(c *gin.Context) {
	uid, eid, ok := h.callerEvent(c)
	if !ok {
		return
	}
	resp, err := h.svc.GetDashboard(c.Request.Context(), uid, eid, c.GetString("role"))
	if err != nil {
		h.mapErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp, "error": nil})
}

func (h *AnalyticsHandler) GetAnalytics(c *gin.Context) {
	uid, eid, ok := h.callerEvent(c)
	if !ok {
		return
	}
	resp, err := h.svc.GetAnalytics(c.Request.Context(), uid, eid, c.GetString("role"))
	if err != nil {
		h.mapErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp, "error": nil})
}

func (h *AnalyticsHandler) GetPostEventReport(c *gin.Context) {
	uid, eid, ok := h.callerEvent(c)
	if !ok {
		return
	}
	format := c.Query("format")
	resp, err := h.svc.GetPostEventReport(c.Request.Context(), uid, eid, c.GetString("role"), format)
	if err != nil {
		if errors.Is(err, ErrPDFNotImplemented) {
			writeErr(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", "pdf format not implemented")
			return
		}
		h.mapErr(c, err)
		return
	}
	if b, ok := resp.([]byte); ok {
		c.Data(http.StatusOK, "text/csv; charset=utf-8", b)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp, "error": nil})
}

func (h *AnalyticsHandler) mapErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrEventForbidden):
		writeErr(c, http.StatusForbidden, "FORBIDDEN", "not event owner")
	case errors.Is(err, ErrAnalyticsNotFound), errors.Is(err, pgx.ErrNoRows):
		writeErr(c, http.StatusNotFound, "NOT_FOUND", "event not found")
	default:
		writeErr(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal error")
	}
}
