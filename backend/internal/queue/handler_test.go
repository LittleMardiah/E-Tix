package queue

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	testUserIDHeader = "X-Test-User-ID"
	testUserID       = "11111111-2222-3333-4444-555555555555"
	testEventID      = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func buildTestRouter(h *QueueHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		if uid := c.GetHeader(testUserIDHeader); uid != "" {
			c.Set("user_id", uid)
		}
	})
	r.GET("/queue/status", h.GetStatus)
	r.POST("/queue/verify-captcha", h.VerifyCaptcha)
	return r
}

func newTestQueueHandler(t *testing.T, eventRepo EventRepository, captcha CaptchaService) *QueueHandler {
	t.Helper()
	return NewQueueHandler(newTestQueueService(t, eventRepo, captcha), zap.NewNop())
}

func doJSON(t *testing.T, router *gin.Engine, method, path, userID, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, path, reader)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID != "" {
		req.Header.Set(testUserIDHeader, userID)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

type queueStatusEnvelope struct {
	Success bool                `json:"success"`
	Data    QueueStatusResponse `json:"data"`
}

type verifyCaptchaEnvelope struct {
	Success bool                  `json:"success"`
	Data    VerifyCaptchaResponse `json:"data"`
}

func TestHandler_GetStatus_MissingUserID(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodGet, "/queue/status?event_id="+testEventID, "", "")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"success":false`)
}

func TestHandler_GetStatus_InvalidUUID(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodGet, "/queue/status?event_id="+testEventID, "not-uuid", "")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"success":false`)
}

func TestHandler_GetStatus_MissingEventID(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodGet, "/queue/status", testUserID, "")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"success":false`)
}

func TestHandler_GetStatus_InvalidEventID(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodGet, "/queue/status?event_id=not-uuid", testUserID, "")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"success":false`)
}

func TestHandler_GetStatus_Success(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodGet, "/queue/status?event_id="+testEventID, testUserID, "")

	require.Equal(t, http.StatusOK, w.Code)

	var env queueStatusEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.True(t, env.Success)
	assert.Equal(t, TierLow, env.Data.Tier)
	assert.Equal(t, 65, env.Data.LoyaltyScore)
	assert.GreaterOrEqual(t, env.Data.Position, 1)
	assert.Equal(t, env.Data.Position+1000, env.Data.TotalInQueue)
	assert.NotEmpty(t, env.Data.QueueToken)
}

func TestHandler_GetStatus_EventNotOnSale(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "DRAFT"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodGet, "/queue/status?event_id="+testEventID, testUserID, "")

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "event is not on sale")
}

func TestHandler_GetStatus_InternalError(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{err: errors.New("db down")}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodGet, "/queue/status?event_id="+testEventID, testUserID, "")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "INTERNAL_ERROR")
}

func TestHandler_VerifyCaptcha_MissingUserID(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodPost, "/queue/verify-captcha", "", `{"challenge_id":"c-1","solution":"s"}`)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"success":false`)
}

func TestHandler_VerifyCaptcha_InvalidUUID(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodPost, "/queue/verify-captcha", "not-uuid", `{"challenge_id":"c-1","solution":"s"}`)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"success":false`)
}

func TestHandler_VerifyCaptcha_BadBody(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodPost, "/queue/verify-captcha", testUserID, `{invalid-json`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"success":false`)
}

func TestHandler_VerifyCaptcha_EmptyChallengeID(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodPost, "/queue/verify-captcha", testUserID, `{"challenge_id":"","solution":"x"}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "challenge_id is required")
}

func TestHandler_VerifyCaptcha_Invalid(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: false})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodPost, "/queue/verify-captcha", testUserID, `{"challenge_id":"c-1","solution":"wrong"}`)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "invalid captcha")
}

func TestHandler_VerifyCaptcha_Success(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodPost, "/queue/verify-captcha", testUserID, `{"challenge_id":"c-1","solution":"correct"}`)

	require.Equal(t, http.StatusOK, w.Code)

	var env verifyCaptchaEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.True(t, env.Success)
	assert.True(t, env.Data.Verified)
	assert.NotEmpty(t, env.Data.QueueToken)
}

func TestHandler_VerifyCaptcha_InternalError(t *testing.T) {
	handler := newTestQueueHandler(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{err: errors.New("captcha down")})
	router := buildTestRouter(handler)

	w := doJSON(t, router, http.MethodPost, "/queue/verify-captcha", testUserID, `{"challenge_id":"c-1","solution":"s"}`)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "INTERNAL_ERROR")
}
