package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/admin"
	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

func newP9a8Redis(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client, mr
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Error.Code
}

func new2FARouter(userID string, rdb *goredis.Client, svc *admin.TwoFAService, log *zap.Logger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/admin/mutate",
		func(c *gin.Context) {
			if userID != "" {
				c.Set("user_id", userID)
			}
			c.Next()
		},
		admin.Admin2FAMiddleware(rdb, svc, log),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) },
	)
	return r
}

func perform2FA(r *gin.Engine, code string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/admin/mutate", nil)
	if code != "" {
		req.Header.Set(admin.HeaderAdmin2FAToken, code)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTwoFAServiceP9a8(t *testing.T) {
	t.Run("default issuer", func(t *testing.T) {
		svc := admin.NewTwoFAService("")
		require.NotNil(t, svc)

		enroll, err := svc.GenerateSecret("admin@example.com")
		require.NoError(t, err)
		require.NotNil(t, enroll)
		assert.NotEmpty(t, enroll.Secret)
		assert.Contains(t, enroll.URL, "otpauth://totp/")
		assert.Contains(t, enroll.URL, "E-Tix%20Flash")
		assert.Contains(t, enroll.URL, "admin@example.com")
	})

	t.Run("custom issuer", func(t *testing.T) {
		svc := admin.NewTwoFAService("Acme")
		enroll, err := svc.GenerateSecret("ops@example.com")
		require.NoError(t, err)
		assert.Contains(t, enroll.URL, "Acme")
	})

	t.Run("validate", func(t *testing.T) {
		svc := admin.NewTwoFAService("")
		enroll, err := svc.GenerateSecret("admin@example.com")
		require.NoError(t, err)

		assert.False(t, svc.Validate("", enroll.Secret))
		assert.False(t, svc.Validate("123456", ""))
		assert.False(t, svc.Validate("000000", enroll.Secret))

		code, err := totp.GenerateCode(enroll.Secret, time.Now())
		require.NoError(t, err)
		assert.True(t, svc.Validate(code, enroll.Secret))
	})
}

func TestAdmin2FAMiddlewareP9a8(t *testing.T) {
	svc := admin.NewTwoFAService("")
	enroll, err := svc.GenerateSecret("admin@example.com")
	require.NoError(t, err)
	validCode, err := totp.GenerateCode(enroll.Secret, time.Now())
	require.NoError(t, err)

	t.Run("invalid user context", func(t *testing.T) {
		rdb, _ := newP9a8Redis(t)
		w := perform2FA(new2FARouter("", rdb, svc, zap.NewNop()), "123456")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "UNAUTHORIZED", errorCode(t, w))
	})

	t.Run("missing header", func(t *testing.T) {
		rdb, _ := newP9a8Redis(t)
		w := perform2FA(new2FARouter(uuid.NewString(), rdb, svc, zap.NewNop()), "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "ADMIN_2FA_REQUIRED", errorCode(t, w))
	})

	t.Run("lockout hit", func(t *testing.T) {
		rdb, mr := newP9a8Redis(t)
		uid := uuid.NewString()
		lockKey := fmt.Sprintf(admin.KeyAdmin2FALockout, uid)
		require.NoError(t, mr.Set(lockKey, "1"))
		mr.SetTTL(lockKey, 10*time.Second)

		w := perform2FA(new2FARouter(uid, rdb, svc, zap.NewNop()), "123456")
		assert.Equal(t, http.StatusTooManyRequests, w.Code)
		assert.Equal(t, "ADMIN_2FA_LOCKED", errorCode(t, w))
	})

	t.Run("not enrolled", func(t *testing.T) {
		rdb, _ := newP9a8Redis(t)
		w := perform2FA(new2FARouter(uuid.NewString(), rdb, svc, zap.NewNop()), "123456")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "ADMIN_2FA_NOT_ENROLLED", errorCode(t, w))
	})

	t.Run("valid token", func(t *testing.T) {
		rdb, mr := newP9a8Redis(t)
		uid := uuid.NewString()
		require.NoError(t, mr.Set(fmt.Sprintf(admin.KeyAdmin2FASecret, uid), enroll.Secret))
		require.NoError(t, mr.Set(fmt.Sprintf(admin.KeyAdmin2FAFails, uid), "2"))

		w := perform2FA(new2FARouter(uid, rdb, svc, nil), validCode)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.False(t, mr.Exists(fmt.Sprintf(admin.KeyAdmin2FAFails, uid)))
	})

	t.Run("invalid token increments fails", func(t *testing.T) {
		rdb, mr := newP9a8Redis(t)
		uid := uuid.NewString()
		require.NoError(t, mr.Set(fmt.Sprintf(admin.KeyAdmin2FASecret, uid), enroll.Secret))

		w := perform2FA(new2FARouter(uid, rdb, svc, nil), "000000")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "ADMIN_2FA_INVALID", errorCode(t, w))
		fails, err := mr.Get(fmt.Sprintf(admin.KeyAdmin2FAFails, uid))
		require.NoError(t, err)
		assert.Equal(t, "1", fails)
	})

	t.Run("third failure triggers lockout", func(t *testing.T) {
		rdb, mr := newP9a8Redis(t)
		uid := uuid.NewString()
		require.NoError(t, mr.Set(fmt.Sprintf(admin.KeyAdmin2FASecret, uid), enroll.Secret))

		router := new2FARouter(uid, rdb, svc, zap.NewNop())
		perform2FA(router, "000000")
		perform2FA(router, "000000")
		w := perform2FA(router, "000000")

		assert.Equal(t, http.StatusTooManyRequests, w.Code)
		assert.Equal(t, "ADMIN_2FA_LOCKED", errorCode(t, w))
		assert.True(t, mr.Exists(fmt.Sprintf(admin.KeyAdmin2FALockout, uid)))
	})
}

func TestIdempotencyMiddlewareP9a8(t *testing.T) {
	newRedis := func(t *testing.T) (*redisclient.Client, *miniredis.Miniredis) {
		t.Helper()
		mr := miniredis.RunT(t)
		c := &redisclient.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
		t.Cleanup(func() { _ = c.Close() })
		return c, mr
	}

	build := func(rdb *redisclient.Client, status int, calls *int) *gin.Engine {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.POST("/mutate", admin.IdempotencyMiddleware(rdb, time.Hour), func(c *gin.Context) {
			*calls++
			c.JSON(status, gin.H{"call": *calls})
		})
		return r
	}

	do := func(r *gin.Engine, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mutate", nil)
		if key != "" {
			req.Header.Set("X-Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("nil redis passes through", func(t *testing.T) {
		calls := 0
		w := do(build(nil, http.StatusOK, &calls), "k1")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, calls)
	})

	t.Run("missing key passes through", func(t *testing.T) {
		rdb, _ := newRedis(t)
		calls := 0
		w := do(build(rdb, http.StatusOK, &calls), "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, calls)
	})

	t.Run("second request replays cached body", func(t *testing.T) {
		rdb, mr := newRedis(t)
		calls := 0
		r := build(rdb, http.StatusCreated, &calls)

		first := do(r, "abc")
		assert.Equal(t, http.StatusCreated, first.Code)
		assert.Equal(t, 1, calls)
		assert.True(t, mr.Exists("admin:idem:abc"))

		second := do(r, "abc")
		assert.Equal(t, http.StatusOK, second.Code)
		assert.Equal(t, first.Body.String(), second.Body.String())
		assert.Equal(t, 1, calls)
	})

	t.Run("non-2xx not cached", func(t *testing.T) {
		rdb, mr := newRedis(t)
		calls := 0
		w := do(build(rdb, http.StatusBadRequest, &calls), "bad")
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.False(t, mr.Exists("admin:idem:bad"))
	})
}
