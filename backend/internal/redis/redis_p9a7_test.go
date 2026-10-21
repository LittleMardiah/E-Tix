package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// STP traceability: redis wrapper + QR store — no UT-* ID exists in STP v1.1
// §3.2 for this module (module gap, see TD-F5-09).

func p9a7Client(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := NewClient("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

func TestP9a7_NewClient_InvalidURL(t *testing.T) {
	c, err := NewClient("://bad-url")
	require.Error(t, err)
	assert.Nil(t, c)
}

func TestP9a7_NewClient_ValidURL(t *testing.T) {
	mr := miniredis.RunT(t)
	c, err := NewClient("redis://" + mr.Addr())
	require.NoError(t, err)
	require.NotNil(t, c)
	require.NoError(t, c.Health(context.Background()))
	require.NoError(t, c.Close())
}

func TestP9a7_Health_ServerDown(t *testing.T) {
	mr := miniredis.RunT(t)
	c, err := NewClient("redis://" + mr.Addr())
	require.NoError(t, err)
	mr.Close()
	require.Error(t, c.Health(context.Background()))
}

func TestP9a7_SetValue_GetString_Roundtrip(t *testing.T) {
	c, _ := p9a7Client(t)
	ctx := context.Background()

	require.NoError(t, c.SetValue(ctx, "k1", "v1", 0))
	val, err := c.GetString(ctx, "k1")
	require.NoError(t, err)
	assert.Equal(t, "v1", val)
}

func TestP9a7_GetString_CacheMissReturnsEmptyNoError(t *testing.T) {
	c, _ := p9a7Client(t)
	val, err := c.GetString(context.Background(), "missing-key")
	require.NoError(t, err, "cache miss must not be an error")
	assert.Equal(t, "", val)
}

func TestP9a7_SetValue_WithTTL(t *testing.T) {
	c, mr := p9a7Client(t)
	ctx := context.Background()

	require.NoError(t, c.SetValue(ctx, "ttl-key", "v", 30*time.Second))
	assert.Equal(t, 30*time.Second, mr.TTL("ttl-key"))
}

func TestP9a7_SetNX_FirstWins(t *testing.T) {
	c, _ := p9a7Client(t)
	ctx := context.Background()

	ok, err := c.SetNX(ctx, "lock-key", "owner-1", 10*time.Second)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = c.SetNX(ctx, "lock-key", "owner-2", 10*time.Second)
	require.NoError(t, err)
	assert.False(t, ok, "second SetNX must fail while key exists")

	val, err := c.GetString(ctx, "lock-key")
	require.NoError(t, err)
	assert.Equal(t, "owner-1", val)
}

func TestP9a7_IncrWithTTL_CountsAndSetsExpiryOnFirstIncr(t *testing.T) {
	c, mr := p9a7Client(t)
	ctx := context.Background()

	for want := int64(1); want <= 3; want++ {
		got, err := c.IncrWithTTL(ctx, "counter", 45*time.Second)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	assert.Equal(t, 45*time.Second, mr.TTL("counter"))
}

func TestP9a7_IncrWithTTL_NoTTLKeepsKeyPersistent(t *testing.T) {
	c, mr := p9a7Client(t)
	ctx := context.Background()

	got, err := c.IncrWithTTL(ctx, "counter-no-ttl", 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got)
	assert.Equal(t, time.Duration(0), mr.TTL("counter-no-ttl"))
}

func TestP9a7_EvalScript_RunsLua(t *testing.T) {
	c, _ := p9a7Client(t)
	ctx := context.Background()

	require.NoError(t, c.SetValue(ctx, "lua-key", "lua-val", 0))
	res, err := c.EvalScript(ctx, "return redis.call('GET', KEYS[1])", []string{"lua-key"})
	require.NoError(t, err)
	assert.Equal(t, "lua-val", res)
}

func TestP9a7_PipelineSet_EmptyItemsIsNoop(t *testing.T) {
	c, _ := p9a7Client(t)
	require.NoError(t, c.PipelineSet(context.Background(), nil, time.Minute))
	require.NoError(t, c.PipelineSet(context.Background(), []KVItem{}, time.Minute))
}

func TestP9a7_PipelineSet_WritesAllKeys(t *testing.T) {
	c, _ := p9a7Client(t)
	ctx := context.Background()

	err := c.PipelineSet(ctx, []KVItem{
		{Key: "p1", Value: "v1"},
		{Key: "p2", Value: "v2"},
	}, time.Minute)
	require.NoError(t, err)

	for k, want := range map[string]string{"p1": "v1", "p2": "v2"} {
		got, err := c.GetString(ctx, k)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
}

func TestP9a7_PipelineSet_ServerDownReturnsError(t *testing.T) {
	mr := miniredis.RunT(t)
	c, err := NewClient("redis://" + mr.Addr())
	require.NoError(t, err)
	mr.Close()

	err = c.PipelineSet(context.Background(), []KVItem{{Key: "p1", Value: "v1"}}, time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pipeline set")
}

func TestP9a7_QRStore_SetGet(t *testing.T) {
	mr := miniredis.RunT(t)
	raw := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = raw.Close() })

	store := NewQRStore(raw)
	ctx := context.Background()

	require.NoError(t, store.Set(ctx, "qr:1", "payload", 30*time.Second))
	got, err := store.Get(ctx, "qr:1")
	require.NoError(t, err)
	assert.Equal(t, "payload", got)
}

func TestP9a7_QRStore_Get_CacheMiss(t *testing.T) {
	mr := miniredis.RunT(t)
	raw := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = raw.Close() })

	store := NewQRStore(raw)
	got, err := store.Get(context.Background(), "qr:missing")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestP9a7_QRStore_ServerDown(t *testing.T) {
	mr := miniredis.RunT(t)
	raw := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = raw.Close() })
	mr.Close()

	store := NewQRStore(raw)
	ctx := context.Background()

	_, err := store.Get(ctx, "qr:1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "qr store get")

	require.Error(t, store.Set(ctx, "qr:1", "payload", time.Minute))
}
