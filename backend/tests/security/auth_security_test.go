package security

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/LittleMardiah/E-Tix/backend/internal/auth"
	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

func setupTestRedis(t *testing.T) *redisclient.Client {
	mr := miniredis.RunT(t)
	rdb, err := redisclient.NewClient("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	return rdb
}

func setupTestServerWithRateLimit(t *testing.T, limit int, window time.Duration, rdb *redisclient.Client) *httptest.Server {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(auth.RateLimitMiddleware(rdb, limit, window))
	r.POST("/register", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.POST("/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestSecurity_RateLimit_Register(t *testing.T) {
	rdb := setupTestRedis(t)
	srv := setupTestServerWithRateLimit(t, 10, time.Hour, rdb)

	client := &http.Client{}

	for i := 0; i < 10; i++ {
		req, err := http.NewRequest("POST", srv.URL+"/register", strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "192.168.1.100")

		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated,
			"expected 200/201 for request %d, got %d", i+1, resp.StatusCode)
	}

	req, err := http.NewRequest("POST", srv.URL+"/register", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "192.168.1.100")

	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestSecurity_RateLimit_Login(t *testing.T) {
	rdb := setupTestRedis(t)
	srv := setupTestServerWithRateLimit(t, 5, time.Minute, rdb)

	client := &http.Client{}

	for i := 0; i < 5; i++ {
		req, err := http.NewRequest("POST", srv.URL+"/login", strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "10.0.0.50")

		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated,
			"expected 200/201 for request %d, got %d", i+1, resp.StatusCode)
	}

	req, err := http.NewRequest("POST", srv.URL+"/login", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "10.0.0.50")

	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestSecurity_BcryptCost12(t *testing.T) {
	password := "TestPass123!"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	require.NoError(t, err)
	cost, err := bcrypt.Cost(hash)
	require.NoError(t, err)
	assert.Equal(t, 12, cost)
}

func TestSecurity_RefreshTokenReplay(t *testing.T) {
	rdb := setupTestRedis(t)
	blacklist := auth.NewTokenBlacklistService(rdb)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwtSvc := auth.NewJWTServiceWithKeys(priv, &priv.PublicKey, "test-issuer")

	user := &auth.User{
		ID:    uuid.New(),
		Email: "test@example.com",
		Name:  "Test User",
		Role:  "customer",
	}

	refreshToken, err := jwtSvc.GenerateRefreshToken(user)
	require.NoError(t, err)

	claims, err := jwtSvc.ParseRefreshToken(refreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, claims.ID)

	ctx := context.Background()
	err = blacklist.AddToBlacklist(ctx, claims.ID, time.Hour)
	require.NoError(t, err)

	blacklisted, err := blacklist.IsBlacklisted(ctx, claims.ID)
	require.NoError(t, err)
	assert.True(t, blacklisted)

	_, err = jwtSvc.ParseRefreshToken(refreshToken)
	assert.NoError(t, err)
}

func TestSecurity_TokenTypeMismatch(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwtSvc := auth.NewJWTServiceWithKeys(priv, &priv.PublicKey, "test-issuer")

	user := &auth.User{
		ID:    uuid.New(),
		Email: "test@example.com",
		Name:  "Test User",
		Role:  "customer",
	}

	accessToken, err := jwtSvc.GenerateAccessToken(user, "TIER_3")
	require.NoError(t, err)

	_, err = jwtSvc.ValidateRefreshToken(accessToken)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrTokenTypeMismatch)
}

func TestSecurity_JWTSignatureTampered(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwtSvc := auth.NewJWTServiceWithKeys(priv, &priv.PublicKey, "test-issuer")

	user := &auth.User{
		ID:    uuid.New(),
		Email: "test@example.com",
		Name:  "Test User",
		Role:  "customer",
	}

	refreshToken, err := jwtSvc.GenerateRefreshToken(user)
	require.NoError(t, err)

	parts := strings.Split(refreshToken, ".")
	require.Len(t, parts, 3)

	// tamper signature
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	if len(sig) > 10 {
		sig[len(sig)-1] ^= 0xFF
		parts[2] = base64.RawURLEncoding.EncodeToString(sig)
	} else {
		parts[2] = parts[2] + "A"
	}
	tampered := strings.Join(parts, ".")

	_, err = jwtSvc.ParseRefreshToken(tampered)
	assert.Error(t, err)
}
