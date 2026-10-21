package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func init() { gin.SetMode(gin.TestMode) }

// ---------- helpers ----------

// mwRouter builds a gin engine with the given middlewares and a terminal
// handler that records whether it was reached.
func mwRouter(middlewares ...gin.HandlerFunc) (*gin.Engine, *bool) {
	reached := false
	r := gin.New()
	r.Use(middlewares...)
	r.GET("/test", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	return r, &reached
}

func mwDo(r *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// mwObservedLogger returns a logger that records entries in memory plus the
// observer core, so assertions can inspect what was logged.
func mwObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

// mwSanitizedHeaders extracts the sanitized_headers field of a log entry.
func mwSanitizedHeaders(t *testing.T, entry observer.LoggedEntry) map[string]string {
	t.Helper()
	raw, ok := entry.ContextMap()["sanitized_headers"]
	require.True(t, ok, "sanitized_headers field missing from log entry")
	m, ok := raw.(map[string]string)
	require.True(t, ok, "sanitized_headers has unexpected type %T", raw)
	return m
}

// mwHeaderValue looks a header up case-insensitively. Go canonicalises
// header keys, so "X-Request-ID" is stored as "X-Request-Id".
func mwHeaderValue(headers map[string]string, key string) string {
	for k, v := range headers {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// mwContainsCI reports whether haystack contains needle, ignoring case.
// gin-contrib/cors normalises header values to canonical MIME form
// ("X-RateLimit-Limit" is emitted as "X-Ratelimit-Limit").
func mwContainsCI(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// ---------- CORSMiddleware nil-logger regression (BUG #103) ----------

func TestCORSMiddleware_NilLoggerDoesNotPanic(t *testing.T) {
	// BUG #103 FIX: CORSMiddleware now substitutes zap.NewNop() for a nil
	// logger. Previously the "no origins configured" branch called
	// logger.Warn(...) on a nil *zap.Logger and panicked, taking down the
	// whole API process at wiring time.
	assert.NotPanics(t, func() {
		r, _ := mwRouter(CORSMiddleware([]string{}, nil))
		w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestCORSMiddleware_NilLoggerInvalidOriginDoesNotPanic(t *testing.T) {
	// The invalid-origin branch calls logger.Error(...) — same nil-logger
	// panic risk as the empty-origins branch.
	assert.NotPanics(t, func() {
		r, _ := mwRouter(CORSMiddleware([]string{"app.example.com"}, nil))
		w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestCORSMiddleware_NilLoggerValidOriginStillSetsCORSHeaders(t *testing.T) {
	// A nil logger must not disable CORS — only the logging is stubbed out.
	r, _ := mwRouter(CORSMiddleware([]string{"https://app.example.com"}, nil))
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")

	w := mwDo(r, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mwContainsCI(w.Header().Get("Access-Control-Allow-Origin"), "app.example.com"))
}

// ---------- CORSMiddleware (internal/middleware/cors.go) ----------

func TestCORSMiddleware_EmptyOriginsDisablesCORS(t *testing.T) {
	logger, _ := mwObservedLogger()
	r, _ := mwRouter(CORSMiddleware([]string{}, logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_AllowedOriginSetsAllowOriginHeader(t *testing.T) {
	logger, _ := mwObservedLogger()
	r, _ := mwRouter(CORSMiddleware([]string{"https://app.example.com"}, logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://app.example.com", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCORSMiddleware_DisallowedOriginHasNoAllowOriginHeader(t *testing.T) {
	logger, _ := mwObservedLogger()
	r, _ := mwRouter(CORSMiddleware([]string{"https://app.example.com"}, logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := mwDo(r, req)

	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_SameOriginRequestSkipsCORSHeaders(t *testing.T) {
	// gin-contrib/cors skips CORS headers when Origin matches the request
	// host (same-origin request): httptest sets Host to "example.com".
	logger, _ := mwObservedLogger()
	r, _ := mwRouter(CORSMiddleware([]string{"https://example.com"}, logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_PreflightReturnsNoContent(t *testing.T) {
	logger, _ := mwObservedLogger()
	r := gin.New()
	r.Use(CORSMiddleware([]string{"https://app.example.com"}, logger))
	r.POST("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "https://app.example.com", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	assert.True(t, mwContainsCI(w.Header().Get("Access-Control-Allow-Methods"), "POST"))
}

func TestCORSMiddleware_PreflightAllowsConfiguredHeaders(t *testing.T) {
	logger, _ := mwObservedLogger()
	r := gin.New()
	r.Use(CORSMiddleware([]string{"https://app.example.com"}, logger))
	r.POST("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization")
	w := mwDo(r, req)

	allowed := w.Header().Get("Access-Control-Allow-Headers")
	for _, h := range []string{"Authorization", "Content-Type", "X-Idempotency-Key", "X-Queue-Token", "X-Admin-2FA-Token", "X-Request-ID"} {
		assert.True(t, mwContainsCI(allowed, h), "expected %s in Allow-Headers, got %q", h, allowed)
	}
}

func TestCORSMiddleware_ExposesRateLimitHeaders(t *testing.T) {
	logger, _ := mwObservedLogger()
	r, _ := mwRouter(CORSMiddleware([]string{"https://app.example.com"}, logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := mwDo(r, req)

	exposed := w.Header().Get("Access-Control-Expose-Headers")
	for _, h := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		assert.True(t, mwContainsCI(exposed, h), "expected %s in Expose-Headers, got %q", h, exposed)
	}
}

func TestCORSMiddleware_InvalidOriginDisablesCORS(t *testing.T) {
	// M1 FIX: an origin without an http(s):// scheme disables CORS instead
	// of panicking inside gin-contrib/cors.
	logger, logs := mwObservedLogger()
	r, _ := mwRouter(CORSMiddleware([]string{"app.example.com"}, logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	require.True(t, logs.Len() > 0, "invalid origin must be logged")
}

func TestCORSMiddleware_WildcardOrigin(t *testing.T) {
	logger, logs := mwObservedLogger()
	r, _ := mwRouter(CORSMiddleware([]string{"*"}, logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Origin"))
	require.True(t, logs.Len() > 0, "wildcard origin must trigger a warning")
}

// ---------- LoggerMiddleware (internal/middleware/logger.go) ----------

func TestLoggerMiddleware_LogsRequestMetadata(t *testing.T) {
	logger, logs := mwObservedLogger()
	r, _ := mwRouter(LoggerMiddleware(logger))

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]
	assert.Equal(t, "http request", entry.Message)
	ctx := entry.ContextMap()
	assert.Equal(t, http.MethodGet, ctx["method"])
	assert.Equal(t, "/test", ctx["path"])
	assert.Equal(t, int64(http.StatusOK), ctx["status"])
}

func TestLoggerMiddleware_RedactsAuthorizationHeader(t *testing.T) {
	logger, logs := mwObservedLogger()
	r, _ := mwRouter(LoggerMiddleware(logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiJ9.secret.token")
	mwDo(r, req)

	require.Equal(t, 1, logs.Len())
	headers := mwSanitizedHeaders(t, logs.All()[0])
	assert.Equal(t, "[REDACTED]", mwHeaderValue(headers, "Authorization"))
}

func TestLoggerMiddleware_RedactsAdmin2FAToken(t *testing.T) {
	logger, logs := mwObservedLogger()
	r, _ := mwRouter(LoggerMiddleware(logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Admin-2FA-Token", "123456")
	mwDo(r, req)

	require.Equal(t, 1, logs.Len())
	headers := mwSanitizedHeaders(t, logs.All()[0])
	assert.Equal(t, "[REDACTED]", mwHeaderValue(headers, "X-Admin-2FA-Token"))
}

func TestLoggerMiddleware_RedactsQueueToken(t *testing.T) {
	logger, logs := mwObservedLogger()
	r, _ := mwRouter(LoggerMiddleware(logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Queue-Token", "queue-jwt-token")
	mwDo(r, req)

	require.Equal(t, 1, logs.Len())
	headers := mwSanitizedHeaders(t, logs.All()[0])
	assert.Equal(t, "[REDACTED]", mwHeaderValue(headers, "X-Queue-Token"))
}

func TestLoggerMiddleware_KeepsNonSensitiveHeaders(t *testing.T) {
	logger, logs := mwObservedLogger()
	r, _ := mwRouter(LoggerMiddleware(logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req-123")
	mwDo(r, req)

	require.Equal(t, 1, logs.Len())
	headers := mwSanitizedHeaders(t, logs.All()[0])
	assert.Equal(t, "application/json", mwHeaderValue(headers, "Content-Type"))
	assert.Equal(t, "req-123", mwHeaderValue(headers, "X-Request-ID"))
}

func TestLoggerMiddleware_LogsClientIP(t *testing.T) {
	logger, logs := mwObservedLogger()
	r, _ := mwRouter(LoggerMiddleware(logger))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	mwDo(r, req)

	require.Equal(t, 1, logs.Len())
	assert.Equal(t, "203.0.113.7", logs.All()[0].ContextMap()["client_ip"])
}

func TestLoggerMiddleware_LogsLatency(t *testing.T) {
	logger, logs := mwObservedLogger()
	r, _ := mwRouter(LoggerMiddleware(logger))

	mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	require.Equal(t, 1, logs.Len())
	_, ok := logs.All()[0].ContextMap()["latency_ms"]
	assert.True(t, ok, "latency_ms must be logged")
}

// ---------- SanitizeHeaders / isSensitiveHeader ----------

func TestSanitizeHeaders_JoinsMultipleValues(t *testing.T) {
	out := SanitizeHeaders(map[string][]string{"Accept": {"a", "b"}})

	assert.Equal(t, "a, b", out["Accept"])
}

func TestSanitizeHeaders_RedactsCaseInsensitiveSensitiveHeader(t *testing.T) {
	out := SanitizeHeaders(map[string][]string{"authorization": {"Bearer abc"}})

	assert.Equal(t, "[REDACTED]", out["authorization"])
}

func TestSanitizeHeaders_EmptyInput(t *testing.T) {
	out := SanitizeHeaders(map[string][]string{})

	assert.Empty(t, out)
}

func TestIsSensitiveHeader_MatchesKnownHeaders(t *testing.T) {
	for _, h := range []string{"Authorization", "authorization", "AUTHORIZATION", "X-Admin-2FA-Token", "x-admin-2fa-token", "X-Queue-Token"} {
		assert.True(t, isSensitiveHeader(h), "%s must be sensitive", h)
	}
}

func TestIsSensitiveHeader_IgnoresOtherHeaders(t *testing.T) {
	for _, h := range []string{"Content-Type", "X-Request-ID", "X-Idempotency-Key", "", "AuthorizationX"} {
		assert.False(t, isSensitiveHeader(h), "%s must not be sensitive", h)
	}
}

// ---------- RecoveryMiddleware (internal/middleware/recovery.go) ----------

func TestRecoveryMiddleware_CatchesPanicAndReturns500(t *testing.T) {
	logger, _ := mwObservedLogger()
	r := gin.New()
	r.Use(RecoveryMiddleware(logger))
	r.GET("/test", func(c *gin.Context) { panic("test panic") })

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRecoveryMiddleware_NormalRequestPasses(t *testing.T) {
	logger, _ := mwObservedLogger()
	r, reached := mwRouter(RecoveryMiddleware(logger))

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, *reached)
}

func TestRecoveryMiddleware_NilPointerDereference(t *testing.T) {
	logger, _ := mwObservedLogger()
	r := gin.New()
	r.Use(RecoveryMiddleware(logger))
	r.GET("/test", func(c *gin.Context) {
		var p *int
		_ = *p
	})

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRecoveryMiddleware_PanicWithErrorValue(t *testing.T) {
	logger, _ := mwObservedLogger()
	r := gin.New()
	r.Use(RecoveryMiddleware(logger))
	r.GET("/test", func(c *gin.Context) { panic(errors.New("boom")) })

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRecoveryMiddleware_LogsPanic(t *testing.T) {
	logger, logs := mwObservedLogger()
	r := gin.New()
	r.Use(RecoveryMiddleware(logger))
	r.GET("/test", func(c *gin.Context) { panic("test panic") })

	mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	require.Equal(t, 1, logs.Len())
	assert.Equal(t, "panic recovered", logs.All()[0].Message)
}

func TestRecoveryMiddleware_ErrorBodyShape(t *testing.T) {
	logger, _ := mwObservedLogger()
	r := gin.New()
	r.Use(RecoveryMiddleware(logger))
	r.GET("/test", func(c *gin.Context) { panic("test panic") })

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Contains(t, w.Body.String(), "INTERNAL_ERROR")
	assert.Contains(t, w.Body.String(), `"success":false`)
}

// ---------- middleware chain integration ----------

func TestMiddlewareChain_LoggerRecoveryCORS(t *testing.T) {
	logger, logs := mwObservedLogger()
	r, _ := mwRouter(
		LoggerMiddleware(logger),
		RecoveryMiddleware(logger),
		CORSMiddleware([]string{"https://app.example.com"}, logger),
	)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("X-Request-ID", "req-1")
	req.Header.Set("Authorization", "Bearer secret")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://app.example.com", w.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, 1, logs.Len())
	headers := mwSanitizedHeaders(t, logs.All()[0])
	assert.Equal(t, "[REDACTED]", mwHeaderValue(headers, "Authorization"))
}

func TestMiddlewareChain_RecoveryCatchesPanicThroughLogger(t *testing.T) {
	logger, logs := mwObservedLogger()
	r := gin.New()
	r.Use(LoggerMiddleware(logger))
	r.Use(RecoveryMiddleware(logger))
	r.GET("/test", func(c *gin.Context) { panic("chain panic") })

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, 2, logs.Len(), "logger and recovery must both log")
}
