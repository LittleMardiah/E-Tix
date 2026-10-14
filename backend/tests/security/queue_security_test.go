package security

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/auth"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
)

func newSecurityQueueTokenService(t *testing.T) *queue.QueueTokenService {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	dir := t.TempDir()

	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	privPath := filepath.Join(dir, "queue_private.pem")
	require.NoError(t, os.WriteFile(privPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600))

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	pubPath := filepath.Join(dir, "queue_public.pem")
	require.NoError(t, os.WriteFile(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o600))

	svc, err := queue.NewQueueTokenService(privPath, pubPath, "etix-queue-security-test")
	require.NoError(t, err)
	return svc
}

func TestSecurity_QueueTokenSingleUse(t *testing.T) {
	svc := newSecurityQueueTokenService(t)
	rdb := setupTestRedis(t)
	ctx := context.Background()

	token, err := svc.Generate(uuid.New(), queue.TierRegular, 75)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := svc.Validate(token)
	require.NoError(t, err)
	require.NotEmpty(t, claims.ID)

	first, err := svc.MarkUsed(ctx, rdb, claims.ID)
	require.NoError(t, err)
	assert.True(t, first)

	replay, err := svc.MarkUsed(ctx, rdb, claims.ID)
	require.NoError(t, err)
	assert.False(t, replay)

	// Single-use guard lives in Redis (SETNX), not the JWT signature, so the
	// token itself still validates cryptographically after consumption.
	again, err := svc.Validate(token)
	require.NoError(t, err)
	assert.Equal(t, claims.ID, again.ID)
}

func TestSecurity_QueueTokenTampered(t *testing.T) {
	svc := newSecurityQueueTokenService(t)

	token, err := svc.Generate(uuid.New(), queue.TierVIP, 95)
	require.NoError(t, err)

	mid := len(token) / 2
	tampered := []byte(token)
	if tampered[mid] == 'A' {
		tampered[mid] = 'B'
	} else {
		tampered[mid] = 'A'
	}
	require.NotEqual(t, token, string(tampered))

	claims, err := svc.Validate(string(tampered))
	require.Error(t, err)
	assert.Nil(t, claims)
}

func TestSecurity_RateLimit_VerifyCaptcha(t *testing.T) {
	rdb := setupTestRedis(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/queue/verify-captcha",
		auth.RateLimitMiddlewareScoped(rdb, 3, 5*time.Minute, auth.RateLimitScopeIP),
		func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"success": true})
		})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	client := &http.Client{}

	for i := 0; i < 3; i++ {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/queue/verify-captcha", strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "203.0.113.10")

		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "request %d should pass", i+1)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/queue/verify-captcha", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.10")

	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestSecurity_CaptchaThreshold(t *testing.T) {
	assert.Equal(t, 50, queue.ChallengeRequiredScoreThreshold)

	mock := queue.NewMockCaptchaService(false)
	ok, err := mock.Verify(context.Background(), "chal-1", "wrong")
	require.NoError(t, err)
	assert.False(t, ok)
}
