package queue

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPublisher struct {
	mu        sync.Mutex
	publishes int
	done      chan struct{}
}

func newMockPublisher() *mockPublisher {
	return &mockPublisher{done: make(chan struct{}, 1)}
}

func (m *mockPublisher) Publish(_ context.Context, _ string, _ []byte) error {
	m.mu.Lock()
	m.publishes++
	m.mu.Unlock()

	select {
	case m.done <- struct{}{}:
	default:
	}
	return nil
}

func (m *mockPublisher) wait(t *testing.T) {
	t.Helper()

	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher.Publish was not called")
	}
}

func (m *mockPublisher) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.publishes
}

type failingTokenIssuer struct{}

func (failingTokenIssuer) Generate(uuid.UUID, string, int) (string, error) {
	return "", errors.New("issuer unavailable")
}

func buildIngressRouter(t *testing.T, cfg IngressConfig) (*gin.Engine, map[string]interface{}) {
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
		for _, key := range []string{"fan_score", "tier", "queue_token"} {
			if val, ok := c.Get(key); ok {
				captured[key] = val
			}
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r, captured
}

func TestIngress_NoUserID(t *testing.T) {
	router, _ := buildIngressRouter(t, IngressConfig{})

	w := doJSON(t, router, http.MethodGet, "/ingress", "", "")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "UNAUTHORIZED")
}

func TestIngress_InvalidUUID(t *testing.T) {
	router, _ := buildIngressRouter(t, IngressConfig{})

	w := doJSON(t, router, http.MethodGet, "/ingress", "not-uuid", "")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "UNAUTHORIZED")
}

func TestIngress_WithUserID_SetsContext(t *testing.T) {
	fanScore, _, _, _ := newTestFanScoreService(t)
	pub := newMockPublisher()
	cfg := IngressConfig{
		FanScore:         fanScore,
		QueueTokenIssuer: newTestQueueTokenService(t),
		Publisher:        pub,
	}
	router, captured := buildIngressRouter(t, cfg)

	w := doJSON(t, router, http.MethodGet, "/ingress", testUserID, "")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, captured, "fan_score")
	assert.Contains(t, captured, "tier")
	assert.Contains(t, captured, "queue_token")

	score, ok := captured["fan_score"].(int)
	require.True(t, ok)
	assert.Equal(t, 65, score)
	assert.Equal(t, TierLow, captured["tier"])

	token, ok := captured["queue_token"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, token)

	pub.wait(t)
	assert.Equal(t, 1, pub.count())
}

func TestIngress_IPBlacklist_Blocked(t *testing.T) {
	fanScore, _, _, _ := newTestFanScoreService(t)
	cfg := IngressConfig{
		FanScore:         fanScore,
		QueueTokenIssuer: newTestQueueTokenService(t),
		IPBlacklistCheck: func(_ context.Context, _ string) (bool, error) {
			return true, nil
		},
	}
	router, _ := buildIngressRouter(t, cfg)

	w := doJSON(t, router, http.MethodGet, "/ingress", testUserID, "")

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "BLACKLISTED")
}

func TestIngress_IPBlacklist_Err(t *testing.T) {
	fanScore, _, _, _ := newTestFanScoreService(t)
	cfg := IngressConfig{
		FanScore:         fanScore,
		QueueTokenIssuer: newTestQueueTokenService(t),
		IPBlacklistCheck: func(_ context.Context, _ string) (bool, error) {
			return false, errors.New("blacklist store unreachable")
		},
	}
	router, captured := buildIngressRouter(t, cfg)

	w := doJSON(t, router, http.MethodGet, "/ingress", testUserID, "")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, captured, "fan_score")
	assert.Contains(t, captured, "tier")
	assert.Contains(t, captured, "queue_token")
}

func TestIngress_FanScoreError(t *testing.T) {
	fanScore, _, _, repo := newTestFanScoreService(t)
	repo.err = errors.New("user lookup failed")
	cfg := IngressConfig{FanScore: fanScore}
	router, _ := buildIngressRouter(t, cfg)

	w := doJSON(t, router, http.MethodGet, "/ingress", testUserID, "")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "INTERNAL_ERROR")
}

func TestIngress_TokenIssuerError_SetsEmptyToken(t *testing.T) {
	fanScore, _, _, _ := newTestFanScoreService(t)
	cfg := IngressConfig{
		FanScore:         fanScore,
		QueueTokenIssuer: failingTokenIssuer{},
	}
	router, captured := buildIngressRouter(t, cfg)

	w := doJSON(t, router, http.MethodGet, "/ingress", testUserID, "")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, captured, "fan_score")
	assert.Contains(t, captured, "tier")

	token, ok := captured["queue_token"].(string)
	require.True(t, ok)
	assert.Empty(t, token)
}
