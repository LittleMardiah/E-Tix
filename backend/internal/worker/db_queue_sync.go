package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/booking"
)

// QueueRepository is the durable seat-lock queue drained by
// DBQueueSyncWorker.
//
// P9a-12 TD-F9-17: the queue is the PostgreSQL counterpart of the Redis seat
// bitmap. While the Redis circuit breaker is open the booking service parks
// the seat-lock requests here; once Redis recovered the worker replays them
// in FIFO order (LOGIC FLOW §9.4).
type QueueRepository interface {
	// Dequeue returns up to limit pending requests, oldest first.
	Dequeue(ctx context.Context, limit int) ([]booking.QueuedSeatLock, error)
	// Ack removes a successfully replayed request from the queue.
	Ack(ctx context.Context, id uuid.UUID) error
	// Fail removes a request that can never be replayed.
	Fail(ctx context.Context, id uuid.UUID, reason string) error
}

// SeatLocker replays a parked seat-lock request against Redis.
//
// It is implemented by *booking.BookingService (LockQueuedSeats).
type SeatLocker interface {
	LockQueuedSeats(ctx context.Context, req booking.QueuedSeatLock) error
}

// DBQueueSyncWorker drains the durable seat-lock queue.
//
// P9a-12 TD-F9-17 / LOGIC FLOW §9.4:
//   - Redis down  -> circuit breaker OPEN  -> requests are parked in the PG
//     queue, the worker stays idle (no data loss).
//   - Redis up    -> circuit breaker CLOSED -> the worker replays the parked
//     requests in FIFO order and removes them from the queue.
//
// The worker never drains while the breaker is open: the replay would fail
// anyway and the requests would pile up as errors.
type DBQueueSyncWorker struct {
	repo   QueueRepository
	locker SeatLocker

	// redisAvailable reports whether Redis is reachable again (the circuit
	// breaker closed). It must never panic; a false value only postpones
	// the drain.
	redisAvailable func() bool

	interval time.Duration
	limit    int
	logger   *zap.Logger
}

// Default queue sync tuning.
const (
	defaultQueueSyncInterval = 5 * time.Second
	defaultQueueSyncLimit    = 50
)

// NewDBQueueSyncWorker wires the worker.
func NewDBQueueSyncWorker(repo QueueRepository, locker SeatLocker, redisAvailable func() bool, logger *zap.Logger) *DBQueueSyncWorker {
	return &DBQueueSyncWorker{
		repo:           repo,
		locker:         locker,
		redisAvailable: redisAvailable,
		interval:       defaultQueueSyncInterval,
		limit:          defaultQueueSyncLimit,
		logger:         logger,
	}
}

// WithInterval overrides the drain interval (tests).
func (w *DBQueueSyncWorker) WithInterval(d time.Duration) *DBQueueSyncWorker {
	if d > 0 {
		w.interval = d
	}
	return w
}

// WithLimit overrides the number of requests drained per cycle (tests).
func (w *DBQueueSyncWorker) WithLimit(n int) *DBQueueSyncWorker {
	if n > 0 {
		w.limit = n
	}
	return w
}

// Run drains the queue periodically until ctx is cancelled.
func (w *DBQueueSyncWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.Sync(ctx); err != nil {
				w.logger.Error("db queue sync failed", zap.Error(err))
			}
		}
	}
}

// Sync drains the queue once.
//
// It is a no-op while the circuit breaker is open (degraded mode still
// active). Requests that fail with a permanent error (seats taken meanwhile,
// expired request) are removed from the queue; transient errors keep the
// request queued for the next cycle.
func (w *DBQueueSyncWorker) Sync(ctx context.Context) error {
	if w.redisAvailable != nil && !w.redisAvailable() {
		w.logger.Debug("db queue sync skipped: redis circuit breaker open")
		return nil
	}
	if w.repo == nil || w.locker == nil {
		return nil
	}

	items, err := w.repo.Dequeue(ctx, w.limit)
	if err != nil {
		return fmt.Errorf("db queue sync: dequeue: %w", err)
	}
	if len(items) == 0 {
		return nil
	}

	replayed, failed := 0, 0
	for _, item := range items {
		if err := w.locker.LockQueuedSeats(ctx, item); err != nil {
			if isPermanentQueueError(err) {
				w.logger.Warn("db queue sync: request failed permanently",
					zap.String("queue_id", item.ID.String()),
					zap.String("token_id", item.TokenID),
					zap.Error(err))
				if ferr := w.repo.Fail(ctx, item.ID, err.Error()); ferr != nil {
					w.logger.Error("db queue sync: fail request failed",
						zap.String("queue_id", item.ID.String()),
						zap.Error(ferr))
					continue
				}
				failed++
				continue
			}
			// Transient error: keep the request queued and retry next cycle.
			w.logger.Error("db queue sync: replay failed, request stays queued",
				zap.String("queue_id", item.ID.String()),
				zap.String("token_id", item.TokenID),
				zap.Error(err))
			continue
		}
		if aerr := w.repo.Ack(ctx, item.ID); aerr != nil {
			w.logger.Error("db queue sync: ack request failed",
				zap.String("queue_id", item.ID.String()),
				zap.Error(aerr))
			continue
		}
		replayed++
	}

	w.logger.Info("db queue sync finished",
		zap.Int("replayed", replayed),
		zap.Int("failed", failed),
		zap.Int("pending", len(items)-replayed-failed))
	return nil
}

// isPermanentQueueError reports whether a replay error means the request can
// never succeed. Those requests are removed from the queue instead of being
// retried forever.
func isPermanentQueueError(err error) bool {
	switch {
	case errors.Is(err, booking.ErrSeatsNotAvailable),
		errors.Is(err, booking.ErrQueuedSeatLockExpired),
		errors.Is(err, booking.ErrSeatOutOfRange),
		errors.Is(err, booking.ErrEmptySeatPositions),
		errors.Is(err, booking.ErrSectionNotFound),
		errors.Is(err, booking.ErrDuplicateSeatPosition):
		return true
	default:
		return false
	}
}
