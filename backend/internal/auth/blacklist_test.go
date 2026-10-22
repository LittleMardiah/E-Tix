package auth

// STP traceability: UT-AUTH-004 (mapped below)

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddToBlacklist_DefaultTTL(t *testing.T) {
	client, mr := newTestRedisClient(t)
	bl := NewTokenBlacklistService(client)

	require.NoError(t, bl.AddToBlacklist(context.Background(), "jti-default", 0))

	key := blacklistPrefix + "jti-default"
	require.True(t, mr.Exists(key))
	assert.Equal(t, time.Second, mr.TTL(key))
}

func TestAddToBlacklist_ValidTTL(t *testing.T) {
	client, mr := newTestRedisClient(t)
	bl := NewTokenBlacklistService(client)

	require.NoError(t, bl.AddToBlacklist(context.Background(), "jti-hour", time.Hour))

	key := blacklistPrefix + "jti-hour"
	require.True(t, mr.Exists(key))
	assert.Equal(t, time.Hour, mr.TTL(key))
}

// UT-AUTH-004: IsBlacklisted_RevokedJTI_ReturnsTrue
func TestIsBlacklisted_True(t *testing.T) {
	client, _ := newTestRedisClient(t)
	bl := NewTokenBlacklistService(client)
	ctx := context.Background()

	require.NoError(t, bl.AddToBlacklist(ctx, "jti-revoked", time.Hour))

	got, err := bl.IsBlacklisted(ctx, "jti-revoked")
	require.NoError(t, err)
	assert.True(t, got)
}

func TestIsBlacklisted_False(t *testing.T) {
	client, _ := newTestRedisClient(t)
	bl := NewTokenBlacklistService(client)

	got, err := bl.IsBlacklisted(context.Background(), "jti-absent")
	require.NoError(t, err)
	assert.False(t, got)
}
