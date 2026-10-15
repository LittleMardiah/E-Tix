package booking

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const (
	testUserIDHeader  = "X-Test-User-ID"
	testQueueTokenHdr = "X-Queue-Token"
)

func buildBookingTestRouter(h *BookingHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if uid := c.GetHeader(testUserIDHeader); uid != "" {
			c.Set("user_id", uid)
		}
	})
	r.GET("/events/:event_id/sections/:section_id/seats", h.GetSeatAvailability)
	r.POST("/events/:event_id/sections/:section_id/seats", h.LockSeats)
	return r
}

func doBookingRequest(t *testing.T, router *gin.Engine, method, path, userID, queueToken, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, path, reader)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID != "" {
		req.Header.Set(testUserIDHeader, userID)
	}
	if queueToken != "" {
		req.Header.Set(testQueueTokenHdr, queueToken)
	}
	// H4: POST /seats requires X-Idempotency-Key (per API CONTRACT §9.1).
	if method == http.MethodPost {
		req.Header.Set("X-Idempotency-Key", "test-idem-"+uuid.NewString())
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func newTestBookingHandler(t *testing.T, repo BookingRepository, rdb *redis.Client, pub DelayedPublisher) (*BookingHandler, *queue.QueueTokenService) {
	t.Helper()
	svc, qt := newTestService(t, repo, rdb, pub)
	return NewBookingHandler(svc, rdb, zap.NewNop()), qt
}

// ============================================================
// Handler: GetSeatAvailability
// ============================================================

func TestHandler_GetSeatAvailability_Success(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 5), taken: []int{2, 4}}
	h, _ := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	w := doBookingRequest(t, router, http.MethodGet,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), "", "")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"total_seats":5`)
	assert.Contains(t, w.Body.String(), `"available_seats":3`)
}

func TestHandler_GetSeatAvailability_InvalidEventUUID(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	h, _ := newTestBookingHandler(t, &fakeRepo{}, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	w := doBookingRequest(t, router, http.MethodGet,
		"/events/not-a-uuid/sections/"+uuid.NewString()+"/seats",
		uuid.NewString(), "", "")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_GetSeatAvailability_InvalidSectionUUID(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	h, _ := newTestBookingHandler(t, &fakeRepo{}, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	w := doBookingRequest(t, router, http.MethodGet,
		"/events/"+uuid.NewString()+"/sections/not-a-uuid/seats",
		uuid.NewString(), "", "")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_GetSeatAvailability_SectionNotFound(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	// repo.section == nil → GetSectionByID returns ErrSectionNotFound
	h, _ := newTestBookingHandler(t, &fakeRepo{section: nil}, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	w := doBookingRequest(t, router, http.MethodGet,
		"/events/"+uuid.NewString()+"/sections/"+uuid.NewString()+"/seats",
		uuid.NewString(), "", "")

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NOT_FOUND"`)
}

// ============================================================
// Handler: LockSeats
// ============================================================

func TestHandler_LockSeats_NoUserContext(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	h, _ := newTestBookingHandler(t, &fakeRepo{}, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	w := doBookingRequest(t, router, http.MethodPost,
		"/events/"+uuid.NewString()+"/sections/"+uuid.NewString()+"/seats",
		"", "token", `{"seat_positions":[1],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"UNAUTHORIZED"`)
}

func TestHandler_LockSeats_NoQueueToken(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	h, _ := newTestBookingHandler(t, &fakeRepo{}, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	w := doBookingRequest(t, router, http.MethodPost,
		"/events/"+uuid.NewString()+"/sections/"+uuid.NewString()+"/seats",
		uuid.NewString(), "", `{"seat_positions":[1],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"QUEUE_TOKEN_REQUIRED"`)
}

func TestHandler_LockSeats_InvalidEventUUID(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	h, _ := newTestBookingHandler(t, &fakeRepo{}, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	w := doBookingRequest(t, router, http.MethodPost,
		"/events/not-a-uuid/sections/"+uuid.NewString()+"/seats",
		uuid.NewString(), "token", `{"seat_positions":[1],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_LockSeats_InvalidBody(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	h, _ := newTestBookingHandler(t, &fakeRepo{}, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	w := doBookingRequest(t, router, http.MethodPost,
		"/events/"+uuid.NewString()+"/sections/"+uuid.NewString()+"/seats",
		uuid.NewString(), "token", `{not-json`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_LockSeats_Conflict(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	// Pre-lock bit at offset 0 (seat 1).
	require.NoError(t, rdb.SetBit(context.Background(), bitmapKey(eid, sid), 0, 1).Err())

	tok := newValidToken(t, qt)
	w := doBookingRequest(t, router, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), tok, `{"seat_positions":[1,2],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"CONFLICT"`)
}

func TestHandler_LockSeats_MaxGroupExceeded(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 20)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	tok := newValidToken(t, qt)
	body := `{"seat_positions":[1,2,3,4,5,6,7,8,9,10,11],"payment_mode":"SPLIT"}`
	w := doBookingRequest(t, router, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), tok, body)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"UNPROCESSABLE_ENTITY"`)
}

func TestHandler_LockSeats_Success(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	tok := newValidToken(t, qt)
	w := doBookingRequest(t, router, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), tok, `{"seat_positions":[1,2,3],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"total_seats":3`)
	assert.Contains(t, w.Body.String(), `"total_amount":"3000000"`)
	assert.Contains(t, w.Body.String(), `"payment_status":"PENDING"`)
}
