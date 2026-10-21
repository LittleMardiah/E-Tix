package queue

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const testClientIP = "192.0.2.1"

// STP traceability: no UT-QUEUE-* / UT-CAPTCHA-* ID exists in STP v1.1 §3.2
// (module gap tracked in TD-F5-09).
// FASE 9 P9a-7 TD-F2-02: CAPTCHA failed-attempt lockout.

type p9a7Captcha struct {
	ok  bool
	err error
}

func (m *p9a7Captcha) Verify(_ context.Context, _, _ string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.ok, nil
}

func newP9a7QueueHandler(t *testing.T, captcha CaptchaService) (*QueueHandler, *miniredis.Miniredis) {
	t.Helper()
	fanScore, client, mr, _ := newTestFanScoreService(t)
	svc := NewQueueService(fanScore, newTestQueueTokenService(t), captcha,
		&mockEventRepo{status: "ON_SALE"}, client)
	return NewQueueHandler(svc, zap.NewNop()), mr
}

// doJSONFromIP issues the request with a real client IP in RemoteAddr
// (doJSON leaves RemoteAddr empty, so c.ClientIP() would be "").
func doJSONFromIP(t *testing.T, router *gin.Engine, method, path, userID, body, clientIP string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, path, reader)
	require.NoError(t, err)
	req.RemoteAddr = clientIP + ":54321"
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

func TestP9a7_VerifyCaptcha_ThreeFailuresLocksOutIP(t *testing.T) {
	captcha := &p9a7Captcha{ok: false}
	h, mr := newP9a7QueueHandler(t, captcha)
	router := buildTestRouter(h)

	// Fail #1 and #2 → 401 invalid captcha.
	for i := 1; i <= 2; i++ {
		w := doJSONFromIP(t, router, http.MethodPost, "/queue/verify-captcha", testUserID,
			`{"challenge_id":"c-1","solution":"wrong"}`, testClientIP)
		require.Equal(t, http.StatusUnauthorized, w.Code, "fail #%d must be 401", i)
		assert.Contains(t, w.Body.String(), "invalid captcha")
	}

	// Fail #3 → lockout engaged → 429.
	w := doJSONFromIP(t, router, http.MethodPost, "/queue/verify-captcha", testUserID,
		`{"challenge_id":"c-1","solution":"wrong"}`, testClientIP)
	require.Equal(t, http.StatusTooManyRequests, w.Code, "third failure must lock out the IP")
	assert.Contains(t, w.Body.String(), "CAPTCHA_LOCKED")

	// Lockout flag must exist in redis with a TTL.
	key := "captcha_lockout:" + testClientIP
	assert.True(t, mr.Exists(key), "lockout key must exist")
	ttl := mr.TTL(key)
	assert.Greater(t, ttl, time.Duration(0), "lockout key must have a TTL")
	assert.LessOrEqual(t, ttl, CaptchaLockoutTTL)

	// Fail #4 → still locked, even with a correct solution.
	captcha.ok = true
	w = doJSONFromIP(t, router, http.MethodPost, "/queue/verify-captcha", testUserID,
		`{"challenge_id":"c-1","solution":"correct"}`, testClientIP)
	require.Equal(t, http.StatusTooManyRequests, w.Code, "locked IP must stay locked")
	assert.Contains(t, w.Body.String(), "CAPTCHA_LOCKED")
}

func TestP9a7_VerifyCaptcha_SuccessClearsFailureCounter(t *testing.T) {
	captcha := &p9a7Captcha{ok: false}
	h, mr := newP9a7QueueHandler(t, captcha)
	router := buildTestRouter(h)

	// One failure → counter = 1.
	w := doJSONFromIP(t, router, http.MethodPost, "/queue/verify-captcha", testUserID,
		`{"challenge_id":"c-1","solution":"wrong"}`, testClientIP)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, mr.Exists("captcha_fails:"+testClientIP))

	// Success → counter cleared.
	captcha.ok = true
	w = doJSONFromIP(t, router, http.MethodPost, "/queue/verify-captcha", testUserID,
		`{"challenge_id":"c-1","solution":"correct"}`, testClientIP)
	require.Equal(t, http.StatusOK, w.Code)
	assert.False(t, mr.Exists("captcha_fails:"+testClientIP), "successful verify must reset the counter")
}

func TestP9a7_QueueService_CaptchaLockoutLifecycle(t *testing.T) {
	fanScore, client, mr, _ := newTestFanScoreService(t)
	svc := NewQueueService(fanScore, newTestQueueTokenService(t), &p9a7Captcha{ok: true},
		&mockEventRepo{status: "ON_SALE"}, client)
	ctx := context.Background()

	locked, ttl := svc.CaptchaLockoutState(ctx, "10.0.0.1")
	assert.False(t, locked)
	assert.Zero(t, ttl)

	fails, locked := svc.RecordCaptchaFailure(ctx, "10.0.0.1")
	assert.EqualValues(t, 1, fails)
	assert.False(t, locked)

	fails, locked = svc.RecordCaptchaFailure(ctx, "10.0.0.1")
	assert.EqualValues(t, 2, fails)
	assert.False(t, locked)

	fails, locked = svc.RecordCaptchaFailure(ctx, "10.0.0.1")
	assert.EqualValues(t, CaptchaMaxFails, fails)
	assert.True(t, locked, "third failure must lock the IP out")

	locked, ttl = svc.CaptchaLockoutState(ctx, "10.0.0.1")
	assert.True(t, locked)
	assert.Greater(t, ttl, time.Duration(0))
	assert.True(t, mr.Exists("captcha_lockout:10.0.0.1"))

	// A different IP is unaffected.
	locked, _ = svc.CaptchaLockoutState(ctx, "10.0.0.2")
	assert.False(t, locked, "lockout must be scoped per IP")

	// Clear removes the counter (no lockout flag to clear).
	svc.ClearCaptchaFailures(ctx, "10.0.0.1")
	assert.False(t, mr.Exists("captcha_fails:10.0.0.1"))
}

func TestP9a7_QueueService_CaptchaLockout_NoRedisIsSafe(t *testing.T) {
	fanScore, _, _, _ := newTestFanScoreService(t)
	svc := NewQueueService(fanScore, newTestQueueTokenService(t), &p9a7Captcha{ok: true},
		&mockEventRepo{status: "ON_SALE"}, nil)
	ctx := context.Background()

	locked, ttl := svc.CaptchaLockoutState(ctx, "10.0.0.3")
	assert.False(t, locked)
	assert.Zero(t, ttl)

	fails, locked := svc.RecordCaptchaFailure(ctx, "10.0.0.3")
	assert.Zero(t, fails)
	assert.False(t, locked)

	svc.ClearCaptchaFailures(ctx, "10.0.0.3")
}
