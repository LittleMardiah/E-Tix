package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// STP traceability: DB pool construction — no UT-* ID exists in STP v1.1 §3.2
// for this module (module gap, see TD-F5-09).

func TestP9a7_NewDBPool_InvalidConnString(t *testing.T) {
	pool, err := NewDBPool(context.Background(), "://not-a-postgres-url")
	require.Error(t, err)
	assert.Nil(t, pool)
}

func TestP9a7_NewDBPoolWithOptions_InvalidConnString(t *testing.T) {
	pool, err := NewDBPoolWithOptions(context.Background(), "://not-a-postgres-url", PoolOptions{})
	require.Error(t, err)
	assert.Nil(t, pool)
}

func TestP9a7_NewDBPoolWithOptions_ValidConnString(t *testing.T) {
	pool, err := NewDBPoolWithOptions(context.Background(),
		"postgres://user:pass@127.0.0.1:59999/e_tix?connect_timeout=1",
		PoolOptions{MaxConns: 7, StatementTimeout: "1500ms"})
	require.NoError(t, err)
	require.NotNil(t, pool)
	defer pool.Close()

	cfg := pool.Config()
	assert.Equal(t, int32(7), cfg.MaxConns)
	assert.Equal(t, "1500ms", cfg.ConnConfig.RuntimeParams["statement_timeout"])
}

func TestP9a7_NewDBPoolWithOptions_DefaultsApplied(t *testing.T) {
	pool, err := NewDBPoolWithOptions(context.Background(),
		"postgres://user:pass@127.0.0.1:59999/e_tix?connect_timeout=1",
		PoolOptions{})
	require.NoError(t, err)
	require.NotNil(t, pool)
	defer pool.Close()

	cfg := pool.Config()
	assert.Equal(t, int32(15), cfg.MaxConns, "default MaxConns must be 15")
	assert.Equal(t, "3000ms", cfg.ConnConfig.RuntimeParams["statement_timeout"],
		"default statement_timeout must be 3000ms")
}

func TestP9a7_NewDBPool_ValidConnString(t *testing.T) {
	pool, err := NewDBPool(context.Background(),
		"postgres://user:pass@127.0.0.1:59999/e_tix?connect_timeout=1")
	require.NoError(t, err)
	require.NotNil(t, pool)
	pool.Close()
}
