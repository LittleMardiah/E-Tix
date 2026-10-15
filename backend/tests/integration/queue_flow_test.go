package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/LittleMardiah/E-Tix/backend/internal/auth"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

type queueEnv struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	userRepo *auth.PostgresUserRepository
	jwt      *auth.JWTService
	client   *redis.Client
	router   *gin.Engine
}

func setupQueueEnv(t *testing.T) *queueEnv {
	t.Helper()

	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping integration tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))

	seedBytes, err := os.ReadFile("../fixtures/seed.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seedBytes), pgx.QueryExecModeSimpleProtocol)
	require.NoError(t, err)

	mr := miniredis.RunT(t)
	client := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}

	key := generateIntegrationKeys(t)
	jwtSvc := auth.NewJWTServiceWithKeys(key, &key.PublicKey, integrationIssuer)

	userRepo := auth.NewPostgresUserRepository(pool)
	blacklist := auth.NewTokenBlacklistService(client)
	fanScore := queue.NewFanScoreService(client, userRepo)
	queueToken := queue.NewQueueTokenServiceWithKeys(key, &key.PublicKey, "etix-integration-queue")
	captcha := queue.NewMockCaptchaService(false)
	eventRepo := queue.NewPostgresEventRepository(pool)
	queueSvc := queue.NewQueueService(fanScore, queueToken, captcha, eventRepo, client)
	handler := queue.NewQueueHandler(queueSvc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/queue/status", auth.AuthMiddleware(jwtSvc, blacklist), handler.GetStatus)
	router.POST("/queue/verify-captcha", auth.AuthMiddleware(jwtSvc, blacklist), handler.VerifyCaptcha)

	env := &queueEnv{
		ctx:      ctx,
		pool:     pool,
		userRepo: userRepo,
		jwt:      jwtSvc,
		client:   client,
		router:   router,
	}
	t.Cleanup(func() {
		pool.Close()
		_ = client.Close()
	})
	return env
}

func (e *queueEnv) createUser(t *testing.T) *auth.User {
	t.Helper()

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	email := "itest_queue_" + suffix + "@test.local"
	phone := fmt.Sprintf("+628%d", 1000000000+rand.Int63n(9000000000))

	hash, err := bcrypt.GenerateFromPassword([]byte(integrationDefaultPassword), 12)
	require.NoError(t, err)

	user := &auth.User{
		Email:        email,
		Phone:        phone,
		Name:         "Queue Integration User",
		PasswordHash: string(hash),
		Role:         "customer",
		Status:       "ACTIVE",
	}
	require.NoError(t, e.userRepo.CreateUser(e.ctx, user))

	t.Cleanup(func() {
		_, _ = e.pool.Exec(e.ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})
	return user
}

func (e *queueEnv) accessToken(t *testing.T, user *auth.User) string {
	t.Helper()
	token, err := e.jwt.GenerateAccessToken(user, "TIER_2")
	require.NoError(t, err)
	refresh, err := e.jwt.GenerateRefreshToken(user)
	require.NoError(t, err)
	require.NotEmpty(t, refresh)
	return token
}

func (e *queueEnv) doRequest(t *testing.T, method, target, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *queueEnv) offSaleEventID(t *testing.T) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	err := e.pool.QueryRow(e.ctx, "SELECT id FROM events WHERE status <> 'ON_SALE' LIMIT 1").Scan(&id)
	if err == nil {
		return id
	}
	require.ErrorIs(t, err, pgx.ErrNoRows)

	var organizerID uuid.UUID
	err = e.pool.QueryRow(e.ctx, "SELECT id FROM users WHERE role = 'event_organizer' LIMIT 1").Scan(&organizerID)
	require.NoError(t, err)

	id = uuid.New()
	_, err = e.pool.Exec(e.ctx,
		"INSERT INTO events (id, organizer_id, name, venue_name, venue_address, event_date, event_start_time, status) "+
			"VALUES ($1, $2, $3, $4, $5, CURRENT_DATE + INTERVAL '30 days', '19:00', 'DRAFT')",
		id, organizerID, "Queue IT Off-Sale "+id.String(), "IT Venue", "IT Address")
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = e.pool.Exec(e.ctx, "DELETE FROM events WHERE id = $1", id)
	})
	return id
}

func TestQueueFlow_GetStatus_Unauthorized(t *testing.T) {
	env := setupQueueEnv(t)

	w := env.doRequest(t, http.MethodGet, "/queue/status?event_id="+uuid.NewString(), "", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestQueueFlow_VerifyCaptcha_Unauthorized(t *testing.T) {
	env := setupQueueEnv(t)

	body := "{\"challenge_id\":\"chal-1\",\"solution\":\"test_pass\"}"
	w := env.doRequest(t, http.MethodPost, "/queue/verify-captcha", body, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestQueueFlow_VerifyCaptcha_HappyPath(t *testing.T) {
	env := setupQueueEnv(t)
	user := env.createUser(t)
	token := env.accessToken(t, user)

	body := "{\"challenge_id\":\"chal-1\",\"solution\":\"test_pass\"}"
	w := env.doRequest(t, http.MethodPost, "/queue/verify-captcha", body, token)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var out struct {
		Success bool `json:"success"`
		Data    struct {
			Verified   bool   `json:"verified"`
			QueueToken string `json:"queue_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.True(t, out.Success)
	assert.True(t, out.Data.Verified)
	assert.NotEmpty(t, out.Data.QueueToken)
}

func TestQueueFlow_GetStatus_EventNotOnSale(t *testing.T) {
	env := setupQueueEnv(t)
	user := env.createUser(t)
	token := env.accessToken(t, user)
	eventID := env.offSaleEventID(t)

	w := env.doRequest(t, http.MethodGet, "/queue/status?event_id="+eventID.String(), "", token)
	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}

func TestQueueFlow_GetStatus_EventOnSale(t *testing.T) {
	env := setupQueueEnv(t)
	user := env.createUser(t)
	token := env.accessToken(t, user)

	var eventID uuid.UUID
	err := env.pool.QueryRow(env.ctx, "SELECT id FROM events WHERE status = 'ON_SALE' LIMIT 1").Scan(&eventID)
	if err != nil {
		t.Skip("no ON_SALE event in DB; skipping")
	}

	w := env.doRequest(t, http.MethodGet, "/queue/status?event_id="+eventID.String(), "", token)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var out struct {
		Data struct {
			Tier       string `json:"tier"`
			QueueToken string `json:"queue_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.NotEmpty(t, out.Data.Tier)
	assert.NotEmpty(t, out.Data.QueueToken)
}

// ============================================================
// Invalid CAPTCHA integration case (audit gap fix)
// ============================================================

// TestQueueFlow_VerifyCaptcha_Invalid asserts that an invalid CAPTCHA solution
// is rejected with 401 and does not return a queue token. Uses the same
// setup pattern as TestQueueFlow_VerifyCaptcha_HappyPath.
func TestQueueFlow_VerifyCaptcha_Invalid(t *testing.T) {
	e := setupQueueEnv(t)
	user := e.createUser(t)
	token := e.accessToken(t, user)

	body := `{"challenge_id":"x","solution":"definitely_wrong"}`
	w := e.doRequest(t, http.MethodPost, "/queue/verify-captcha", body, token)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	// Response must NOT leak a queue token on failure.
	assert.NotContains(t, w.Body.String(), `"queue_token":"eyJ`)
}
