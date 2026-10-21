package queue

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/auth"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// ============================================================
// P9a-6 helpers (prefixed to avoid collisions with existing fixtures)
// ============================================================

func p9a6Redis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = client.Close() })
	return client, mr
}

type p9a6UserRepo struct {
	user *auth.User
	err  error
}

func (m *p9a6UserRepo) CreateUser(context.Context, *auth.User) error {
	return errors.New("p9a6UserRepo: CreateUser not implemented")
}

func (m *p9a6UserRepo) GetUserByEmail(context.Context, string) (*auth.User, error) {
	return nil, errors.New("p9a6UserRepo: GetUserByEmail not implemented")
}

func (m *p9a6UserRepo) GetUserByID(context.Context, uuid.UUID) (*auth.User, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.user, nil
}

type p9a6EventRepo struct {
	status string
	err    error
}

func (m *p9a6EventRepo) GetEventStatus(context.Context, uuid.UUID) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.status, nil
}

type p9a6Captcha struct {
	ok  bool
	err error
}

func (c *p9a6Captcha) Verify(context.Context, string, string) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	return c.ok, nil
}

type p9a6Publisher struct {
	mu       sync.Mutex
	queue    string
	body     []byte
	publishN int
}

func (p *p9a6Publisher) Publish(_ context.Context, queueName string, body []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queue = queueName
	p.body = append([]byte(nil), body...)
	p.publishN++
	return nil
}

func (p *p9a6Publisher) snapshot() (int, string, []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publishN, p.queue, p.body
}

func p9a6TokenService(t *testing.T) *QueueTokenService {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return NewQueueTokenServiceWithKeys(key, &key.PublicKey, "p9a6-queue")
}

func p9a6FanScore(t *testing.T, base int) (*FanScoreService, *redis.Client) {
	t.Helper()
	client, _ := p9a6Redis(t)
	repo := &p9a6UserRepo{user: &auth.User{ID: uuid.New(), BaseScore: base}}
	return NewFanScoreService(client, repo), client
}

// ============================================================
// FanScoreService — nil-Redis + error paths
// ============================================================

func TestP9a6_FanScore_GetBaseScore_NilRedis_FallsBackToRepo(t *testing.T) {
	repo := &p9a6UserRepo{user: &auth.User{ID: uuid.New(), BaseScore: 72}}
	svc := NewFanScoreService(nil, repo)

	score, err := svc.GetBaseScore(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 72, score)
}

func TestP9a6_FanScore_GetBaseScore_NilRedis_NonPositiveScore_UsesDefault(t *testing.T) {
	repo := &p9a6UserRepo{user: &auth.User{ID: uuid.New(), BaseScore: 0}}
	svc := NewFanScoreService(nil, repo)

	score, err := svc.GetBaseScore(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, defaultBaseScore, score)
}

func TestP9a6_FanScore_GetBaseScore_RepoError(t *testing.T) {
	repo := &p9a6UserRepo{err: errors.New("db down")}
	svc := NewFanScoreService(nil, repo)

	score, err := svc.GetBaseScore(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Equal(t, 0, score)
	assert.Contains(t, err.Error(), "db down")
}

func TestP9a6_FanScore_GetBaseScore_CorruptCacheValue_FallsBackToRepo(t *testing.T) {
	svc, client := p9a6FanScore(t, 61)
	userID := uuid.New()
	require.NoError(t, client.Set(context.Background(), baseScoreKey(userID), "not-a-number", time.Hour).Err())

	score, err := svc.GetBaseScore(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, 61, score)
}

func TestP9a6_FanScore_GetDynamicScore_NilRedis_ReturnsZero(t *testing.T) {
	svc := NewFanScoreService(nil, &p9a6UserRepo{user: &auth.User{BaseScore: 50}})

	score, err := svc.GetDynamicScore(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 0, score)
}

func TestP9a6_FanScore_ComputeFinalScore_RepoError_Propagates(t *testing.T) {
	svc := NewFanScoreService(nil, &p9a6UserRepo{err: errors.New("boom")})

	score, tier, err := svc.ComputeFinalScore(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Equal(t, 0, score)
	assert.Empty(t, tier)
}

func TestP9a6_FanScore_TierLabel_AllTiers(t *testing.T) {
	svc := NewFanScoreService(nil, nil)

	assert.Equal(t, "VIP Fan", svc.TierLabel(TierVIP))
	assert.Equal(t, "Regular Fan", svc.TierLabel(TierRegular))
	assert.Equal(t, "Low Priority", svc.TierLabel(TierLow))
	assert.Equal(t, "Unknown", svc.TierLabel(""))
}

func TestP9a6_FanScore_TierConstants(t *testing.T) {
	assert.Equal(t, "TIER_1", TierVIP)
	assert.Equal(t, "TIER_2", TierRegular)
	assert.Equal(t, "TIER_3", TierLow)
}

// ============================================================
// QueueTokenService — WithKeys constructor, Refund, validation
// ============================================================

func TestP9a6_QueueToken_WithKeys_GenerateValidate(t *testing.T) {
	svc := p9a6TokenService(t)
	userID := uuid.New()

	tok, err := svc.Generate(userID, TierVIP, 95)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	claims, err := svc.Validate(tok)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims.Subject)
	assert.Equal(t, TierVIP, claims.Tier)
	assert.Equal(t, 95, claims.FinalScore)
	assert.Equal(t, queueTokenType, claims.TokenType)
	assert.Equal(t, "p9a6-queue", claims.Issuer)
	assert.NotEmpty(t, claims.ID)
}

func TestP9a6_QueueToken_WithKeys_DefaultIssuer_WhenEmpty(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	svc := NewQueueTokenServiceWithKeys(key, &key.PublicKey, "")

	tok, err := svc.Generate(uuid.New(), TierLow, 10)
	require.NoError(t, err)

	claims, err := svc.Validate(tok)
	require.NoError(t, err)
	assert.Equal(t, "e-tix-flash-queue", claims.Issuer)
}

func TestP9a6_QueueToken_Validate_RejectsWrongIssuer(t *testing.T) {
	svc := p9a6TokenService(t)

	claims := &QueueTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			ID:        uuid.NewString(),
			Issuer:    "some-other-issuer",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
		Tier:       TierVIP,
		TokenType:  queueTokenType,
		FinalScore: 99,
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(svc.priv)
	require.NoError(t, err)

	_, err = svc.Validate(tok)
	require.Error(t, err)
}

func TestP9a6_QueueToken_Validate_RejectsWrongTokenType(t *testing.T) {
	svc := p9a6TokenService(t)

	claims := &QueueTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			ID:        uuid.NewString(),
			Issuer:    svc.issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
		Tier:       TierVIP,
		TokenType:  "not-a-queue-token",
		FinalScore: 99,
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(svc.priv)
	require.NoError(t, err)

	_, err = svc.Validate(tok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token_type")
}

func TestP9a6_QueueToken_Validate_RejectsExpiredToken(t *testing.T) {
	svc := p9a6TokenService(t)

	claims := &QueueTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			ID:        uuid.NewString(),
			Issuer:    svc.issuer,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
		Tier:       TierVIP,
		TokenType:  queueTokenType,
		FinalScore: 99,
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(svc.priv)
	require.NoError(t, err)

	_, err = svc.Validate(tok)
	require.Error(t, err)
}

func TestP9a6_QueueToken_Validate_RejectsHS256AlgConfusion(t *testing.T) {
	svc := p9a6TokenService(t)

	claims := &QueueTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			ID:        uuid.NewString(),
			Issuer:    svc.issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
		Tier:       TierVIP,
		TokenType:  queueTokenType,
		FinalScore: 99,
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("secret"))
	require.NoError(t, err)

	_, err = svc.Validate(tok)
	require.Error(t, err)
}

func TestP9a6_QueueToken_Validate_GarbageInput(t *testing.T) {
	svc := p9a6TokenService(t)

	claims, err := svc.Validate("not-a-jwt")
	require.Error(t, err)
	assert.Nil(t, claims)
}

func TestP9a6_QueueToken_Refund_ClearsUsedMarker(t *testing.T) {
	svc := p9a6TokenService(t)
	client, _ := p9a6Redis(t)
	ctx := context.Background()
	jti := uuid.NewString()

	used, err := svc.MarkUsed(ctx, client, jti)
	require.NoError(t, err)
	require.True(t, used)

	require.NoError(t, svc.Refund(ctx, client, jti))

	// After refund the same jti can be consumed again.
	usedAgain, err := svc.MarkUsed(ctx, client, jti)
	require.NoError(t, err)
	assert.True(t, usedAgain, "refund must release the single-use marker")
}

func TestP9a6_QueueToken_Refund_UnknownJTI_IsNoError(t *testing.T) {
	svc := p9a6TokenService(t)
	client, _ := p9a6Redis(t)

	require.NoError(t, svc.Refund(context.Background(), client, uuid.NewString()))
}

func TestP9a6_QueueToken_MarkUsed_DistinctJTIsAreIndependent(t *testing.T) {
	svc := p9a6TokenService(t)
	client, _ := p9a6Redis(t)
	ctx := context.Background()

	first, err := svc.MarkUsed(ctx, client, uuid.NewString())
	require.NoError(t, err)
	second, err := svc.MarkUsed(ctx, client, uuid.NewString())
	require.NoError(t, err)
	assert.True(t, first)
	assert.True(t, second)
}

func TestP9a6_QueueToken_NewService_MissingKeyFile(t *testing.T) {
	svc, err := NewQueueTokenService("/nonexistent/priv.pem", "/nonexistent/pub.pem", "p9a6")
	require.Error(t, err)
	assert.Nil(t, svc)
}

func TestP9a6_QueueToken_UsedPrefix_IsolatesKeys(t *testing.T) {
	svc := p9a6TokenService(t)
	client, _ := p9a6Redis(t)
	ctx := context.Background()
	jti := uuid.NewString()

	_, err := svc.MarkUsed(ctx, client, jti)
	require.NoError(t, err)

	exists, err := client.Exists(ctx, queueTokenUsedPrefix+jti).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists)
}

// ============================================================
// Captcha
// ============================================================

func TestP9a6_Turnstile_EmptySecretKey_ReturnsError(t *testing.T) {
	svc := NewTurnstileCaptchaService("")

	ok, err := svc.Verify(context.Background(), "challenge-1", "solution")
	require.Error(t, err)
	assert.False(t, ok)
	assert.Contains(t, err.Error(), "secret key empty")
}

func TestP9a6_Turnstile_Constructor_KeepsSecretKey(t *testing.T) {
	svc := NewTurnstileCaptchaService("secret-value")
	require.NotNil(t, svc)
	assert.Equal(t, "secret-value", svc.secretKey)
}

func TestP9a6_MockCaptcha_AutoPassTrue_IgnoresSolution(t *testing.T) {
	m := NewMockCaptchaService(true)

	ok, err := m.Verify(context.Background(), "anything", "garbage")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestP9a6_MockCaptcha_AutoPassFalse_TestPassSolution(t *testing.T) {
	m := NewMockCaptchaService(false)

	ok, err := m.Verify(context.Background(), "challenge-1", "test_pass")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestP9a6_MockCaptcha_AutoPassFalse_WrongSolution(t *testing.T) {
	m := NewMockCaptchaService(false)

	ok, err := m.Verify(context.Background(), "challenge-1", "nope")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestP9a6_CaptchaService_InterfaceSatisfiedByMock(t *testing.T) {
	var svc CaptchaService = NewMockCaptchaService(false)
	require.NotNil(t, svc)
}

func TestP9a6_ChallengeThreshold_Constant(t *testing.T) {
	assert.Equal(t, 50, ChallengeRequiredScoreThreshold)
}

// ============================================================
// QueueService
// ============================================================

func TestP9a6_Service_GetStatus_LowScore_ChallengeRequired_NoToken(t *testing.T) {
	fanScore, client := p9a6FanScore(t, 10)
	svc := NewQueueService(fanScore, p9a6TokenService(t), &p9a6Captcha{ok: true},
		&p9a6EventRepo{status: "ON_SALE"}, client)

	resp, err := svc.GetStatus(context.Background(), uuid.New(), uuid.New())
	require.NoError(t, err)
	assert.True(t, resp.ChallengeRequired)
	assert.Empty(t, resp.QueueToken, "below threshold no token may be issued")
	assert.Equal(t, TierLow, resp.Tier)
}

func TestP9a6_Service_GetStatus_HighScore_TokenIssued(t *testing.T) {
	fanScore, client := p9a6FanScore(t, 80)
	svc := NewQueueService(fanScore, p9a6TokenService(t), &p9a6Captcha{ok: true},
		&p9a6EventRepo{status: "ON_SALE"}, client)

	resp, err := svc.GetStatus(context.Background(), uuid.New(), uuid.New())
	require.NoError(t, err)
	assert.False(t, resp.ChallengeRequired)
	assert.NotEmpty(t, resp.QueueToken)
	assert.Equal(t, TierRegular, resp.Tier)
	assert.Equal(t, "Regular Fan", resp.TierLabel)
	assert.Positive(t, resp.Position)
	assert.Equal(t, resp.Position+1000, resp.TotalInQueue)
	assert.Equal(t, resp.Position*2, resp.EstimatedWaitSeconds)
}

func TestP9a6_Service_GetStatus_EventRepoError(t *testing.T) {
	fanScore, client := p9a6FanScore(t, 80)
	svc := NewQueueService(fanScore, p9a6TokenService(t), &p9a6Captcha{ok: true},
		&p9a6EventRepo{err: errors.New("event read failed")}, client)

	resp, err := svc.GetStatus(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestP9a6_Service_VerifyCaptcha_Success_ReturnsToken(t *testing.T) {
	fanScore, client := p9a6FanScore(t, 80)
	svc := NewQueueService(fanScore, p9a6TokenService(t), &p9a6Captcha{ok: true},
		&p9a6EventRepo{status: "ON_SALE"}, client)

	resp, err := svc.VerifyCaptcha(context.Background(), uuid.New(), &VerifyCaptchaRequest{
		ChallengeID: "challenge-1", Solution: "test_pass",
	})
	require.NoError(t, err)
	assert.True(t, resp.Verified)
	assert.NotEmpty(t, resp.QueueToken)
}

func TestP9a6_Service_VerifyCaptcha_CaptchaError_Propagates(t *testing.T) {
	fanScore, client := p9a6FanScore(t, 80)
	svc := NewQueueService(fanScore, p9a6TokenService(t),
		&p9a6Captcha{err: errors.New("provider down")},
		&p9a6EventRepo{status: "ON_SALE"}, client)

	resp, err := svc.VerifyCaptcha(context.Background(), uuid.New(), &VerifyCaptchaRequest{
		ChallengeID: "challenge-1", Solution: "test_pass",
	})
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestP9a6_Service_Sentinels_Distinct(t *testing.T) {
	assert.NotEqual(t, ErrEventNotOnSale, ErrCaptchaInvalid)
	assert.Contains(t, ErrEventNotOnSale.Error(), "not on sale")
	assert.Contains(t, ErrCaptchaInvalid.Error(), "captcha invalid")
}

// ============================================================
// estimatedPosition (unexported — internal test package)
// ============================================================

func TestP9a6_EstimatedPosition_Deterministic(t *testing.T) {
	userID := uuid.New()
	assert.Equal(t, estimatedPosition(userID), estimatedPosition(userID))
}

func TestP9a6_EstimatedPosition_InRange1To1000(t *testing.T) {
	for i := 0; i < 50; i++ {
		pos := estimatedPosition(uuid.New())
		assert.GreaterOrEqual(t, pos, 1)
		assert.LessOrEqual(t, pos, 1000)
	}
}

// ============================================================
// Ingress middleware
// ============================================================

func p9a6IngressRouter(t *testing.T, cfg IngressConfig) (*gin.Engine, *map[string]interface{}) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	captured := map[string]interface{}{}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if uid := c.GetHeader(testUserIDHeader); uid != "" {
			c.Set("user_id", uid)
		}
	})
	r.Use(IngressMiddleware(cfg))
	r.GET("/ingress", func(c *gin.Context) {
		for _, key := range []string{"fan_score", "tier", "queue_token", "challenge_required"} {
			if val, ok := c.Get(key); ok {
				captured[key] = val
			}
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r, &captured
}

func TestP9a6_Ingress_LowScore_SetsChallengeRequired_NoToken(t *testing.T) {
	fanScore, _ := p9a6FanScore(t, 10)
	router, captured := p9a6IngressRouter(t, IngressConfig{
		FanScore:         fanScore,
		QueueTokenIssuer: p9a6TokenService(t),
	})

	req := httptest.NewRequest(http.MethodGet, "/ingress", nil)
	req.Header.Set(testUserIDHeader, uuid.NewString())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, true, (*captured)["challenge_required"])
	assert.Equal(t, "", (*captured)["queue_token"])
}

func TestP9a6_Ingress_HighScore_SetsTokenAndTier(t *testing.T) {
	fanScore, _ := p9a6FanScore(t, 95)
	router, captured := p9a6IngressRouter(t, IngressConfig{
		FanScore:         fanScore,
		QueueTokenIssuer: p9a6TokenService(t),
	})

	req := httptest.NewRequest(http.MethodGet, "/ingress", nil)
	req.Header.Set(testUserIDHeader, uuid.NewString())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 95, (*captured)["fan_score"])
	assert.Equal(t, TierVIP, (*captured)["tier"])
	assert.NotEmpty(t, (*captured)["queue_token"])
	assert.Equal(t, false, (*captured)["challenge_required"])
}

func TestP9a6_Ingress_PublisherReceivesFanScorePayload(t *testing.T) {
	fanScore, _ := p9a6FanScore(t, 95)
	pub := &p9a6Publisher{}
	router, _ := p9a6IngressRouter(t, IngressConfig{
		FanScore:         fanScore,
		QueueTokenIssuer: p9a6TokenService(t),
		Publisher:        pub,
	})

	req := httptest.NewRequest(http.MethodGet, "/ingress", nil)
	req.Header.Set(testUserIDHeader, uuid.NewString())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	// The publisher is invoked from a goroutine — poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, queueName, body := pub.snapshot()
		if n > 0 {
			assert.Equal(t, "fan_score", queueName)
			assert.Contains(t, string(body), "final_score")
			assert.Contains(t, string(body), "user_id")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("publisher.Publish was never called")
}

func TestP9a6_Ingress_NoPublisherConfigured_IsSafe(t *testing.T) {
	fanScore, _ := p9a6FanScore(t, 95)
	router, captured := p9a6IngressRouter(t, IngressConfig{
		FanScore:         fanScore,
		QueueTokenIssuer: p9a6TokenService(t),
	})

	req := httptest.NewRequest(http.MethodGet, "/ingress", nil)
	req.Header.Set(testUserIDHeader, uuid.NewString())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, (*captured)["fan_score"])
}

func TestP9a6_Ingress_NilQueueTokenIssuer_IsSafe(t *testing.T) {
	fanScore, _ := p9a6FanScore(t, 95)
	router, captured := p9a6IngressRouter(t, IngressConfig{FanScore: fanScore})

	req := httptest.NewRequest(http.MethodGet, "/ingress", nil)
	req.Header.Set(testUserIDHeader, uuid.NewString())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "", (*captured)["queue_token"])
}

func TestP9a6_Ingress_BlacklistCheck_BlocksRequest(t *testing.T) {
	fanScore, _ := p9a6FanScore(t, 95)
	router, _ := p9a6IngressRouter(t, IngressConfig{
		FanScore:         fanScore,
		QueueTokenIssuer: p9a6TokenService(t),
		IPBlacklistCheck: func(context.Context, string) (bool, error) { return true, nil },
	})

	req := httptest.NewRequest(http.MethodGet, "/ingress", nil)
	req.Header.Set(testUserIDHeader, uuid.NewString())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ============================================================
// QueueHandler — extra paths
// ============================================================

func TestP9a6_Handler_GetStatus_LowScore_ReturnsChallengeRequired(t *testing.T) {
	fanScore, client := p9a6FanScore(t, 10)
	svc := NewQueueService(fanScore, p9a6TokenService(t), &p9a6Captcha{ok: true},
		&p9a6EventRepo{status: "ON_SALE"}, client)
	h := NewQueueHandler(svc, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", uuid.NewString()) })
	r.GET("/queue/status", h.GetStatus)

	req := httptest.NewRequest(http.MethodGet, "/queue/status?event_id="+uuid.NewString(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "challenge_required")
	assert.Contains(t, w.Body.String(), `"success":true`)
}

func TestP9a6_Handler_VerifyCaptcha_SuccessBodyShape(t *testing.T) {
	fanScore, client := p9a6FanScore(t, 80)
	svc := NewQueueService(fanScore, p9a6TokenService(t), &p9a6Captcha{ok: true},
		&p9a6EventRepo{status: "ON_SALE"}, client)
	h := NewQueueHandler(svc, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", uuid.NewString()) })
	r.POST("/queue/verify-captcha", h.VerifyCaptcha)

	req := httptest.NewRequest(http.MethodPost, "/queue/verify-captcha",
		newJSONBody(`{"challenge_id":"c1","solution":"test_pass"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"verified":true`)
	assert.Contains(t, w.Body.String(), "queue_token")
}

// ============================================================
// Models / constants
// ============================================================

func TestP9a6_QueueStatusResponse_JSONShape(t *testing.T) {
	resp := QueueStatusResponse{
		Position: 1, TotalInQueue: 2, EstimatedWaitSeconds: 3,
		Tier: TierVIP, TierLabel: "VIP Fan", LoyaltyScore: 95,
		QueueToken: "tok", ChallengeRequired: false,
	}

	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, `"position":1`)
	assert.Contains(t, body, `"total_in_queue":2`)
	assert.Contains(t, body, `"estimated_wait_seconds":3`)
	assert.Contains(t, body, `"tier":"TIER_1"`)
	assert.Contains(t, body, `"tier_label":"VIP Fan"`)
	assert.Contains(t, body, `"loyalty_score":95`)
	assert.Contains(t, body, `"queue_token":"tok"`)
	assert.Contains(t, body, `"challenge_required":false`)
}

func TestP9a6_FanScoreMetadata_Fields(t *testing.T) {
	now := time.Now().UTC()
	meta := FanScoreMetadata{UserID: uuid.NewString(), IP: "10.0.0.1", FinalScore: 88, Timestamp: now}

	assert.NotEmpty(t, meta.UserID)
	assert.Equal(t, "10.0.0.1", meta.IP)
	assert.Equal(t, 88, meta.FinalScore)
	assert.Equal(t, now, meta.Timestamp)
}

func TestP9a6_VerifyCaptchaRequest_Fields(t *testing.T) {
	req := VerifyCaptchaRequest{ChallengeID: "c1", Solution: "s1"}
	assert.Equal(t, "c1", req.ChallengeID)
	assert.Equal(t, "s1", req.Solution)
}

func TestP9a6_VerifyCaptchaResponse_Fields(t *testing.T) {
	resp := VerifyCaptchaResponse{Verified: true, QueueToken: "tok"}
	assert.True(t, resp.Verified)
	assert.Equal(t, "tok", resp.QueueToken)
}

func TestP9a6_QueueNames_ContainsExpectedQueues(t *testing.T) {
	assert.Contains(t, QueueNames, "fan_score")
	assert.Contains(t, QueueNames, "release_seat_check")
	assert.Contains(t, QueueNames, "resale_expiry_check")
	assert.Contains(t, QueueNames, "report_generation")
	assert.Contains(t, QueueNames, "reconciliation_cycle")
}

func TestP9a6_DelayedExchangeConstants(t *testing.T) {
	assert.Equal(t, "delayed_exchange", ExchangeDelayed)
	assert.Equal(t, "x-delayed-message", ExchangeTypeDelayed)
}

func TestP9a6_QueueTokenTTL_IsFiveMinutes(t *testing.T) {
	assert.Equal(t, 5*time.Minute, queueTokenTTL)
}

// newJSONBody returns a reader over the given JSON string.
func newJSONBody(s string) *bytes.Reader {
	return bytes.NewReader([]byte(s))
}
