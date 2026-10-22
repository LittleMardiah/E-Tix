package integration

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/booking"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
	"github.com/LittleMardiah/E-Tix/backend/internal/worker"
)

// ============================================================
// P9a-12 TD-F9-17 — Redis circuit breaker + durable DB queue
//
// IT-CB-001: the complete degraded-mode flow.
//
//	Redis down  -> circuit breaker OPEN  -> the seat-lock request is parked
//	               in the PostgreSQL queue (no data loss)
//	Redis up    -> circuit breaker CLOSED -> the DB queue sync worker replays
//	               the parked request and creates the booking
// ============================================================

// deadRedisAddr returns the address of a server that accepts and closes every
// connection immediately (a Redis outage).
func deadRedisAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})

	return ln.Addr().String()
}

func TestIntegration_CircuitBreaker_RedisOutage_ParksRequest_AndWorkerDrains(t *testing.T) {
	_, qt, pool, mr := setupBookingEnv(t)
	ctx := context.Background()

	queueRepo := booking.NewPgxSeatLockQueue(pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM seat_lock_queue`)
	})

	lockLua, err := os.ReadFile("../../internal/booking/scripts/lock_seats.lua")
	require.NoError(t, err)
	releaseLua, err := os.ReadFile("../../internal/booking/scripts/release_seats.lua")
	require.NoError(t, err)
	scripts := &booking.LuaScripts{Lock: string(lockLua), Release: string(releaseLua)}
	repo := booking.NewPostgresBookingRepository(pool)

	// A breaker that closes quickly so the test can observe the recovery.
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "degraded-mode-test",
		MaxRequests: 1,
		Interval:    time.Hour,
		Timeout:     20 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 1
		},
	})

	// The outage service talks to a dead Redis.
	deadRdb := &redis.Client{Client: goredis.NewClient(&goredis.Options{
		Addr:        deadRedisAddr(t),
		MaxRetries:  -1,
		DialTimeout: 50 * time.Millisecond,
	})}
	t.Cleanup(func() { _ = deadRdb.Close() })

	outageSvc := booking.NewBookingServiceWithCircuitBreaker(
		repo, deadRdb, scripts, qt, &fakeDelayedPublisher{}, cb, queueRepo)

	tok := newIntegrationToken(t, qt, seedCustomerID)

	// Two failures trip the breaker, the third request is rejected.
	for i := 0; i < 2; i++ {
		_, err := outageSvc.LockSeats(ctx, seedCustomerID, seedEventID, seedVIPSectID,
			&booking.LockSeatsRequest{SeatPositions: []int{7, 8}, PaymentMode: "SPLIT"}, tok)
		require.Error(t, err)
	}
	_, err = outageSvc.LockSeats(ctx, seedCustomerID, seedEventID, seedVIPSectID,
		&booking.LockSeatsRequest{SeatPositions: []int{7, 8}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	require.ErrorIs(t, err, booking.ErrServiceUnavailable)

	// The request must be parked in the durable queue.
	var queued int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM seat_lock_queue`).Scan(&queued))
	require.Equal(t, 1, queued, "the request must be parked in the DB queue")

	// Phase 2 — Redis recovered: the worker replays the parked request.
	time.Sleep(40 * time.Millisecond) // breaker timeout -> half open
	require.False(t, outageSvc.RedisCircuitBreakerOpen(), "the breaker must allow the drain again")

	healthyRdb := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = healthyRdb.Close() })
	healthySvc := booking.NewBookingServiceWithCircuitBreaker(
		repo, healthyRdb, scripts, qt, &fakeDelayedPublisher{}, cb, queueRepo)

	w := worker.NewDBQueueSyncWorker(queueRepo, healthySvc, func() bool {
		return !outageSvc.RedisCircuitBreakerOpen()
	}, zap.NewNop())
	require.NoError(t, w.Sync(ctx))

	// The booking must exist now and the queue must be empty.
	var bookingID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM bookings WHERE leader_user_id = $1 ORDER BY created_at DESC LIMIT 1`,
		seedCustomerID).Scan(&bookingID))

	var positions []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT seat_positions FROM bookings WHERE id = $1`, bookingID).Scan(&positions))
	assert.JSONEq(t, `[7,8]`, string(positions))

	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM seat_lock_queue`).Scan(&queued))
	assert.Equal(t, 0, queued, "the replayed request must be removed from the queue")
}
