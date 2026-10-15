package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolOptions carries values historically read from env vars. Zero-value
// fields fall back to the compile-time defaults (15 / 3000ms).
type PoolOptions struct {
	MaxConns         int    // 0 → 15
	StatementTimeout string // "" → "3000ms"
}

func NewDBPool(ctx context.Context, connStr string) (*pgxpool.Pool, error) {
	return NewDBPoolWithOptions(ctx, connStr, PoolOptions{})
}

// NewDBPoolWithOptions is the configurable variant used by the composition
// root. M4 FIX: DB_MAX_CONNECTIONS + DB_STATEMENT_TIMEOUT env vars are now
// actually consumed instead of being silently ignored.
func NewDBPoolWithOptions(ctx context.Context, connStr string, opts PoolOptions) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, err
	}

	maxConns := opts.MaxConns
	if maxConns <= 0 {
		maxConns = 15
	}
	timeout := opts.StatementTimeout
	if timeout == "" {
		timeout = "3000ms"
	}

	config.MaxConns = int32(maxConns)
	config.MinConns = 2
	config.MaxConnIdleTime = 5 * time.Minute
	config.MaxConnLifetime = 30 * time.Minute
	config.HealthCheckPeriod = 1 * time.Minute
	config.ConnConfig.RuntimeParams["statement_timeout"] = timeout

	return pgxpool.NewWithConfig(ctx, config)
}
