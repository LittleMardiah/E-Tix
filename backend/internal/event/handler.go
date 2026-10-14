package event

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EventHandler exposes the public event catalog endpoints.
type EventHandler struct {
	svc *EventService
}

func NewEventHandler(svc *EventService) *EventHandler {
	return &EventHandler{svc: svc}
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

func (h *EventHandler) List(c *gin.Context) {
	filter := ListFilter{
		Status: c.Query("status"),
		Search: c.Query("search"),
		SortBy: c.Query("sort_by"),
		Page:   1,
		Limit:  20,
	}

	if !ValidateStatus(filter.Status) {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid status filter")
		return
	}
	if !ValidateSortBy(filter.SortBy) {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid sort_by")
		return
	}

	if v := c.Query("page"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 {
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid page")
			return
		}
		filter.Page = p
	}
	if v := c.Query("limit"); v != "" {
		l, err := strconv.Atoi(v)
		if err != nil || l < 1 || l > 50 {
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid limit (1-50)")
			return
		}
		filter.Limit = l
	}

	resp, err := h.svc.List(c.Request.Context(), filter)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"events": resp.Events,
		},
		"error": nil,
		"meta": gin.H{
			"total":        resp.Total,
			"current_page": resp.CurrentPage,
			"total_pages":  resp.TotalPages,
			"limit":        resp.Limit,
		},
	})
}

func (h *EventHandler) GetByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("event_id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid event id")
		return
	}

	resp, err := h.svc.GetByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) || errors.Is(err, pgx.ErrNoRows) {
			writeError(c, http.StatusNotFound, "NOT_FOUND", "event not found")
			return
		}
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
