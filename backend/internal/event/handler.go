package event

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// maxMapUploadBytes caps an uploaded layout image (5 MiB).
const maxMapUploadBytes = 5 << 20

// mapImageExtensions whitelists sniffed MIME types for layout uploads.
var mapImageExtensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
}

// mapUploadDir resolves the destination folder. The default points at the
// Next.js public directory so the returned /uploads/maps URL is served
// statically without an extra file server.
func mapUploadDir() string {
	if dir := os.Getenv("MAP_UPLOAD_DIR"); dir != "" {
		return dir
	}
	return filepath.Join("..", "frontend", "public", "uploads", "maps")
}

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

	if v := c.Query("organizer_id"); v != "" {
		organizerID, err := uuid.Parse(v)
		if err != nil {
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid organizer_id")
			return
		}
		filter.OrganizerID = organizerID
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

func (h *EventHandler) UpdateLayout(c *gin.Context) {
	id, err := uuid.Parse(c.Param("event_id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid event id")
		return
	}
	var req UpdateLayoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if err := h.svc.UpdateLayout(c.Request.Context(), id, req); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    nil,
		"error":   nil,
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

// Create handles POST /eo/events (event_organizer | admin).
//
// The organizer is taken from the JWT, never the body, so an EO cannot create
// an event on somebody else's behalf.
func (h *EventHandler) Create(c *gin.Context) {
	organizerID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid session")
		return
	}
	var req CreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	resp, err := h.svc.Create(c.Request.Context(), organizerID, req)
	if err != nil {
		if errors.Is(err, ErrInvalidEventInput) {
			message := strings.TrimPrefix(err.Error(), ErrInvalidEventInput.Error()+": ")
			writeError(c, http.StatusBadRequest, "INVALID_REQUEST", message)
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    resp,
		"error":   nil,
	})
}

// UploadMap handles POST /eo/events/:event_id/upload-map.
//
// Multipart field "file" (png/jpeg/webp, <= 5 MiB). The file is written to
// MAP_UPLOAD_DIR (default frontend/public/uploads/maps) and only
// layout_image_url is updated; the editor still owns width/height/zones.
func (h *EventHandler) UploadMap(c *gin.Context) {
	eventID, err := uuid.Parse(c.Param("event_id"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid event id")
		return
	}
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		writeError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid session")
		return
	}
	ownerID, err := h.svc.GetOrganizerID(c.Request.Context(), eventID)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			writeError(c, http.StatusNotFound, "NOT_FOUND", "event not found")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if c.GetString("role") != "admin" && ownerID != userID {
		writeError(c, http.StatusForbidden, "FORBIDDEN", "not the organizer of this event")
		return
	}

	header, err := c.FormFile("file")
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "file is required")
		return
	}
	if header.Size <= 0 || header.Size > maxMapUploadBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "file must be <= 5MB")
		return
	}

	src, err := header.Open()
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "cannot read file")
		return
	}
	defer func() { _ = src.Close() }()

	head := make([]byte, 512)
	n, readErr := io.ReadFull(src, head)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "cannot read file")
		return
	}
	ext, ok := mapImageExtensions[http.DetectContentType(head[:n])]
	if !ok {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "unsupported image type (png, jpeg, webp only)")
		return
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "cannot read file")
		return
	}

	dir := mapUploadDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "cannot prepare upload directory")
		return
	}
	filename := fmt.Sprintf("%s-%s%s", eventID, uuid.NewString(), ext)
	dstPath := filepath.Join(dir, filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "cannot store file")
		return
	}
	_, copyErr := io.Copy(dst, io.LimitReader(src, maxMapUploadBytes))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dstPath)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "cannot store file")
		return
	}

	layoutURL := "/uploads/maps/" + filename
	if err := h.svc.UpdateLayoutImageURL(c.Request.Context(), eventID, layoutURL); err != nil {
		_ = os.Remove(dstPath)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"layout_image_url": layoutURL},
		"error":   nil,
	})
}
