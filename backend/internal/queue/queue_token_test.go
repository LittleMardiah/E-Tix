package queue

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const testQueueTokenIssuer = "etix-queue-test"

func newQueueTokenPEMs(t *testing.T) (string, string) {
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

	return privPath, pubPath
}

func newTestQueueTokenService(t *testing.T) *QueueTokenService {
	t.Helper()

	privPath, pubPath := newQueueTokenPEMs(t)
	svc, err := NewQueueTokenService(privPath, pubPath, testQueueTokenIssuer)
	require.NoError(t, err)
	return svc
}

func TestQueueToken_Generate_Validate(t *testing.T) {
	svc := newTestQueueTokenService(t)
	userID := uuid.New()

	token, err := svc.Generate(userID, TierVIP, 91)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := svc.Validate(token)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims.Subject)
	assert.Equal(t, TierVIP, claims.Tier)
	assert.Equal(t, 91, claims.FinalScore)
	assert.Equal(t, queueTokenType, claims.TokenType)
	assert.Equal(t, testQueueTokenIssuer, claims.Issuer)
	assert.NotEmpty(t, claims.ID)
}

func TestQueueToken_Validate_Tampered(t *testing.T) {
	svc := newTestQueueTokenService(t)

	token, err := svc.Generate(uuid.New(), TierRegular, 85)
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

func TestQueueToken_MarkUsed_FirstAndReplay(t *testing.T) {
	svc := newTestQueueTokenService(t)
	ctx := t.Context()

	mr := miniredis.RunT(t)
	client := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = client.Close() })

	jti := uuid.NewString()

	first, err := svc.MarkUsed(ctx, client, jti)
	require.NoError(t, err)
	assert.True(t, first)

	replay, err := svc.MarkUsed(ctx, client, jti)
	require.NoError(t, err)
	assert.False(t, replay)

	other, err := svc.MarkUsed(ctx, client, uuid.NewString())
	require.NoError(t, err)
	assert.True(t, other)
}

func TestQueueToken_NewService_EmptyIssuer(t *testing.T) {
	privPath, pubPath := newQueueTokenPEMs(t)

	svc, err := NewQueueTokenService(privPath, pubPath, "")
	require.Error(t, err)
	assert.Nil(t, svc)
}
