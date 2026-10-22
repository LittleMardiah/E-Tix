package worker

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Pool is the minimal PostgreSQL surface used by the worker layer.
//
// FIX P9a-9c: the workers used to hold a concrete *pgxpool.Pool, which made
// every DB-backed branch (Handle, RunOnce, ticker) untestable in unit tests
// because the pool cannot be substituted. *pgxpool.Pool satisfies Pool in
// production; unit tests inject a fake.
//
// TxBeginner (booking_expiry.go) already existed for the same reason; Pool
// reuses it so a single fake covers both Begin and Query/Exec.
type Pool interface {
	TxBeginner
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
