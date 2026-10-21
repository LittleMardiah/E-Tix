package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// mwSetUserID injects the "user_id" claim consumed by RateLimitScopeUser.
func mwSetUserID(uid string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("user_id", uid)
		c.Next()
	}
}

// mwFakeRefreshToken builds an unsigned JWT-shaped token whose payload
// carries the given "sub" claim. extractUserIDFromRefreshToken only decodes
// the payload — it never verifies the signature.
func mwFakeRefreshToken(sub string) string {
	payload, err := json.Marshal(map[string]string{"sub": sub})
	if err != nil {
		panic(err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

// mwErrorCode extracts the envelope error code from an auth response body.
func mwErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &env))
	return env.Error.Code
}

// ---------- RBACMiddleware (internal/auth/rbac.go) ----------

// mwSetRole injects the "role" claim consumed by RBACMiddleware.
func mwSetRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if role != "" {
			c.Set("role", role)
		}
		c.Next()
	}
}

func TestRBACMiddleware_AllowsMatchingRole(t *testing.T) {
	r, reached := mwRouter(mwSetRole("admin"), RBACMiddleware("admin"))

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, *reached, "downstream handler must run for an allowed role")
}

func TestRBACMiddleware_AllowsAnyOfMultipleRoles(t *testing.T) {
	for _, role := range []string{"admin", "event_organizer", "gate_staff"} {
		t.Run(role, func(t *testing.T) {
			r, reached := mwRouter(mwSetRole(role), RBACMiddleware("admin", "event_organizer", "gate_staff"))

			w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

			assert.Equal(t, http.StatusOK, w.Code, "role %s must be allowed", role)
			assert.True(t, *reached)
		})
	}
}

func TestRBACMiddleware_DeniesUnlistedRole(t *testing.T) {
	r, reached := mwRouter(mwSetRole("customer"), RBACMiddleware("admin", "event_organizer"))

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "FORBIDDEN", mwErrorCode(t, w.Body.Bytes()))
	assert.False(t, *reached, "downstream handler must not run for a denied role")
}

func TestRBACMiddleware_MissingRoleReturnsUnauthorized(t *testing.T) {
	// rbac.go: a missing role is 401 (missing authentication context), not 403.
	r, reached := mwRouter(RBACMiddleware("admin"))

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "UNAUTHORIZED", mwErrorCode(t, w.Body.Bytes()))
	assert.False(t, *reached)
}

func TestRBACMiddleware_EmptyRoleStringIsUnauthorized(t *testing.T) {
	r, reached := mwRouter(mwSetRole(""), RBACMiddleware("admin"))

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, *reached)
}

func TestRBACMiddleware_NoAllowedRolesConfiguredDenies(t *testing.T) {
	r, _ := mwRouter(mwSetRole("admin"), RBACMiddleware())

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "FORBIDDEN", mwErrorCode(t, w.Body.Bytes()))
}

func TestRBACMiddleware_AbortsChainOnDeny(t *testing.T) {
	var ctx *gin.Context
	reached := false

	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx = c
		c.Set("role", "customer")
		c.Next()
	})
	r.Use(RBACMiddleware("admin"))
	r.Use(func(c *gin.Context) {
		reached = true
		c.Next()
	})
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, reached, "RBAC must abort the chain")
	require.NotNil(t, ctx)
	assert.True(t, ctx.IsAborted(), "RBAC must mark the context as aborted")
}

// ---------- AuthMiddleware (internal/auth/middleware.go) ----------

func TestAuthMiddleware_ValidTokenSetsContext(t *testing.T) {
	client, _ := newTestRedisClient(t)
	blacklist := NewTokenBlacklistService(client)
	jwtSvc := newTestJWTService(t)

	user := newTestUser()
	user.Role = "admin"
	token, err := jwtSvc.GenerateAccessToken(user, "gold")
	require.NoError(t, err)

	var gotUserID, gotRole, gotTier, gotJTI string
	r := gin.New()
	r.Use(AuthMiddleware(jwtSvc, blacklist))
	r.GET("/test", func(c *gin.Context) {
		gotUserID = c.GetString("user_id")
		gotRole = c.GetString("role")
		gotTier = c.GetString("tier")
		gotJTI = c.GetString("jti")
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := mwDo(r, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, user.ID.String(), gotUserID)
	assert.Equal(t, "admin", gotRole)
	assert.Equal(t, "gold", gotTier)
	assert.NotEmpty(t, gotJTI)
}

func TestAuthMiddleware_MissingAuthorizationHeader(t *testing.T) {
	client, _ := newTestRedisClient(t)
	r := gin.New()
	r.Use(AuthMiddleware(newTestJWTService(t), NewTokenBlacklistService(client)))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "UNAUTHORIZED", mwErrorCode(t, w.Body.Bytes()))
}

func TestAuthMiddleware_MalformedHeaderWithoutBearerPrefix(t *testing.T) {
	client, _ := newTestRedisClient(t)
	r := gin.New()
	r.Use(AuthMiddleware(newTestJWTService(t), NewTokenBlacklistService(client)))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "eyJhbGciOiJSUzI1NiJ9.fake.token")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "UNAUTHORIZED", mwErrorCode(t, w.Body.Bytes()))
}

func TestAuthMiddleware_BearerPrefixIsCaseSensitive(t *testing.T) {
	client, _ := newTestRedisClient(t)
	r := gin.New()
	r.Use(AuthMiddleware(newTestJWTService(t), NewTokenBlacklistService(client)))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "bearer some-token")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_EmptyBearerToken(t *testing.T) {
	client, _ := newTestRedisClient(t)
	r := gin.New()
	r.Use(AuthMiddleware(newTestJWTService(t), NewTokenBlacklistService(client)))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "UNAUTHORIZED", mwErrorCode(t, w.Body.Bytes()))
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	client, _ := newTestRedisClient(t)
	r := gin.New()
	r.Use(AuthMiddleware(newTestJWTService(t), NewTokenBlacklistService(client)))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-jwt")
	w := mwDo(r, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "UNAUTHORIZED", mwErrorCode(t, w.Body.Bytes()))
}

func TestAuthMiddleware_RefreshTokenRejectedAsAccessToken(t *testing.T) {
	client, _ := newTestRedisClient(t)
	jwtSvc := newTestJWTService(t)
	r := gin.New()
	r.Use(AuthMiddleware(jwtSvc, NewTokenBlacklistService(client)))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	refresh, err := jwtSvc.GenerateRefreshToken(newTestUser())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+refresh)
	w := mwDo(r, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "TOKEN_TYPE_MISMATCH", mwErrorCode(t, w.Body.Bytes()))
}

func TestAuthMiddleware_BlacklistedTokenRejected(t *testing.T) {
	client, _ := newTestRedisClient(t)
	blacklist := NewTokenBlacklistService(client)
	jwtSvc := newTestJWTService(t)

	user := newTestUser()
	token, err := jwtSvc.GenerateAccessToken(user, "silver")
	require.NoError(t, err)

	claims, err := jwtSvc.ValidateAccessToken(token)
	require.NoError(t, err)
	require.NoError(t, blacklist.AddToBlacklist(context.Background(), claims.ID, time.Hour))

	r := gin.New()
	r.Use(AuthMiddleware(jwtSvc, blacklist))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := mwDo(r, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "TOKEN_BLACKLISTED", mwErrorCode(t, w.Body.Bytes()))
}

func TestAuthMiddleware_RedisUnavailableFailsClosedWith503(t *testing.T) {
	client, mr := newTestRedisClient(t)
	jwtSvc := newTestJWTService(t)
	token, err := jwtSvc.GenerateAccessToken(newTestUser(), "bronze")
	require.NoError(t, err)

	r := gin.New()
	r.Use(AuthMiddleware(jwtSvc, NewTokenBlacklistService(client)))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	mr.Close()

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := mwDo(r, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "SERVICE_UNAVAILABLE", mwErrorCode(t, w.Body.Bytes()))
}

func TestAuthMiddleware_AbortsDownstreamHandlerOnFailure(t *testing.T) {
	client, _ := newTestRedisClient(t)
	reached := false
	r := gin.New()
	r.Use(AuthMiddleware(newTestJWTService(t), NewTokenBlacklistService(client)))
	r.GET("/test", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})

	w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, reached, "downstream handler must not run for unauthenticated request")
}

// ---------- RateLimitMiddlewareScoped: IP scope ----------

func TestRateLimitMiddleware_IP_UnderLimit(t *testing.T) {
	client, _ := newTestRedisClient(t)
	r, _ := mwRouter(RateLimitMiddleware(client, 5, time.Minute))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "1.2.3.4:12345"
		w := mwDo(r, req)
		assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "request %d must pass", i+1)
	}
}

func TestRateLimitMiddleware_IP_OverLimitReturns429(t *testing.T) {
	client, _ := newTestRedisClient(t)
	r, _ := mwRouter(RateLimitMiddleware(client, 5, time.Minute))

	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "1.2.3.4:12345"
		last = mwDo(r, req)
	}

	assert.Equal(t, http.StatusTooManyRequests, last.Code)
	assert.Equal(t, "RATE_LIMIT_EXCEEDED", mwErrorCode(t, last.Body.Bytes()))
}

func TestRateLimitMiddleware_IP_UsesSeparateBucketPerIP(t *testing.T) {
	client, _ := newTestRedisClient(t)
	r, _ := mwRouter(RateLimitMiddleware(client, 2, time.Minute))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "1.1.1.1:1111"
		mwDo(r, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "2.2.2.2:2222"
	w := mwDo(r, req)

	assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "a different IP gets its own bucket")
}

func TestRateLimitMiddleware_IP_KeyFormat(t *testing.T) {
	client, mr := newTestRedisClient(t)
	r, _ := mwRouter(RateLimitMiddleware(client, 5, time.Minute))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "9.9.9.9:9999"
	mwDo(r, req)

	assert.True(t, mr.Exists("rate:ip:9.9.9.9:/test"), "expected key rate:ip:9.9.9.9:/test, got %v", mr.Keys())
}

// ---------- RateLimitMiddlewareScoped: headers ----------

func TestRateLimitMiddleware_SetsRateLimitHeaders(t *testing.T) {
	client, _ := newTestRedisClient(t)
	r, _ := mwRouter(RateLimitMiddleware(client, 10, time.Minute))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "3.3.3.3:3333"
	w := mwDo(r, req)

	assert.Equal(t, "10", w.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "9", w.Header().Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Reset"))
}

func TestRateLimitMiddleware_RemainingDecrements(t *testing.T) {
	client, _ := newTestRedisClient(t)
	r, _ := mwRouter(RateLimitMiddleware(client, 10, time.Minute))

	var remaining []string
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "4.4.4.4:4444"
		w := mwDo(r, req)
		remaining = append(remaining, w.Header().Get("X-RateLimit-Remaining"))
	}

	assert.Equal(t, []string{"9", "8", "7"}, remaining)
}

// ---------- RateLimitMiddlewareScoped: user scope ----------

func TestRateLimitMiddlewareScoped_User_UnderLimit(t *testing.T) {
	client, _ := newTestRedisClient(t)
	uid := uuid.NewString()
	r, _ := mwRouter(mwSetUserID(uid), RateLimitMiddlewareScoped(client, 5, time.Minute, RateLimitScopeUser))

	for i := 0; i < 5; i++ {
		w := mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))
		assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "request %d must pass", i+1)
	}
}

func TestRateLimitMiddlewareScoped_User_OverLimit(t *testing.T) {
	client, _ := newTestRedisClient(t)
	uid := uuid.NewString()
	r, _ := mwRouter(mwSetUserID(uid), RateLimitMiddlewareScoped(client, 3, time.Minute, RateLimitScopeUser))

	var last *httptest.ResponseRecorder
	for i := 0; i < 4; i++ {
		last = mwDo(r, httptest.NewRequest(http.MethodGet, "/test", nil))
	}

	assert.Equal(t, http.StatusTooManyRequests, last.Code)
}

func TestRateLimitMiddlewareScoped_User_UsesSeparateBucketPerUser(t *testing.T) {
	client, _ := newTestRedisClient(t)
	u1 := uuid.NewString()
	u2 := uuid.NewString()

	r1, _ := mwRouter(mwSetUserID(u1), RateLimitMiddlewareScoped(client, 2, time.Minute, RateLimitScopeUser))
	for i := 0; i < 2; i++ {
		mwDo(r1, httptest.NewRequest(http.MethodGet, "/test", nil))
	}

	r2, _ := mwRouter(mwSetUserID(u2), RateLimitMiddlewareScoped(client, 2, time.Minute, RateLimitScopeUser))
	w := mwDo(r2, httptest.NewRequest(http.MethodGet, "/test", nil))

	assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "a different user gets its own bucket")
}

func TestRateLimitMiddlewareScoped_User_MissingUserIDFallsBackToIP(t *testing.T) {
	client, mr := newTestRedisClient(t)
	r, _ := mwRouter(RateLimitMiddlewareScoped(client, 5, time.Minute, RateLimitScopeUser))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "7.7.7.7:7777"
	mwDo(r, req)

	assert.True(t, mr.Exists("rate:user:7.7.7.7:/test"), "empty user_id must fall back to client IP, got %v", mr.Keys())
}

// ---------- RateLimitMiddlewareScoped: user_via_token scope ----------

func TestRateLimitMiddlewareScoped_UserViaToken_ExtractsSubClaim(t *testing.T) {
	client, mr := newTestRedisClient(t)
	sub := uuid.NewString()
	r := gin.New()
	r.Use(RateLimitMiddlewareScoped(client, 5, time.Minute, RateLimitScopeUserViaToken))
	r.POST("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	body := `{"refresh_token":"` + mwFakeRefreshToken(sub) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
	w := mwDo(r, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mr.Exists("rate:user_via_token:"+sub+":/test"), "expected sub-scoped key, got %v", mr.Keys())
}

func TestRateLimitMiddlewareScoped_UserViaToken_InvalidBodyFallsBackToIP(t *testing.T) {
	client, mr := newTestRedisClient(t)
	r := gin.New()
	r.Use(RateLimitMiddlewareScoped(client, 5, time.Minute, RateLimitScopeUserViaToken))
	r.POST("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("not-json"))
	req.RemoteAddr = "8.8.8.8:8888"
	w := mwDo(r, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mr.Exists("rate:user_via_token:8.8.8.8:/test"), "expected IP fallback key, got %v", mr.Keys())
}

// ---------- extractUserIDFromRefreshToken ----------

func TestExtractUserIDFromRefreshToken_Valid(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"refresh_token":"`+mwFakeRefreshToken("user-123")+`"}`))

	assert.Equal(t, "user-123", extractUserIDFromRefreshToken(c))
}

func TestExtractUserIDFromRefreshToken_InvalidJSON(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("{invalid"))

	assert.Equal(t, "", extractUserIDFromRefreshToken(c))
}

func TestExtractUserIDFromRefreshToken_MissingRefreshTokenField(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"other":"x"}`))

	assert.Equal(t, "", extractUserIDFromRefreshToken(c))
}

func TestExtractUserIDFromRefreshToken_NotThreeSegments(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"refresh_token":"only.two"}`))

	assert.Equal(t, "", extractUserIDFromRefreshToken(c))
}

func TestExtractUserIDFromRefreshToken_InvalidBase64Payload(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"refresh_token":"header.!!!notbase64!!!.sig"}`))

	assert.Equal(t, "", extractUserIDFromRefreshToken(c))
}

func TestExtractUserIDFromRefreshToken_PayloadWithoutSub(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"a@b.com"}`))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"refresh_token":"header.`+payload+`.sig"}`))

	assert.Equal(t, "", extractUserIDFromRefreshToken(c))
}

func TestExtractUserIDFromRefreshToken_RestoresRequestBody(t *testing.T) {
	body := `{"refresh_token":"` + mwFakeRefreshToken("user-123") + `"}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))

	_ = extractUserIDFromRefreshToken(c)

	restored, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	assert.Equal(t, body, string(restored), "request body must remain readable downstream")
}

// ---------- rate limit resilience & scope constants ----------

func TestRateLimitMiddleware_RedisDownFailsOpen(t *testing.T) {
	client, mr := newTestRedisClient(t)
	r, _ := mwRouter(RateLimitMiddleware(client, 1, time.Minute))

	mr.Close()

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "6.6.6.6:6666"
	w := mwDo(r, req)

	assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "B6 trade-off: rate limit fails OPEN on redis error")
}

func TestRateLimitScope_Values(t *testing.T) {
	assert.Equal(t, RateLimitScope("ip"), RateLimitScopeIP)
	assert.Equal(t, RateLimitScope("user"), RateLimitScopeUser)
	assert.Equal(t, RateLimitScope("user_via_token"), RateLimitScopeUserViaToken)
	assert.NotEqual(t, RateLimitScopeIP, RateLimitScopeUser)
	assert.NotEqual(t, RateLimitScopeUser, RateLimitScopeUserViaToken)
}
