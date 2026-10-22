package booking

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxSeatLockQueue is the PostgreSQL implementation of SeatLockQueue.
//
// P9a-12 TD-F9-17: the queue is the durable counterpart of the Redis seat
// bitmap. It is written while the Redis circuit breaker is open and drained
// by the DBQueueSyncWorker once Redis recovered.
type PgxSeatLockQueue struct {
	db *pgxpool.Pool
}

// NewPgxSeatLockQueue builds a queue backed by the seat_lock_queue table.
func NewPgxSeatLockQueue(db *pgxpool.Pool) *PgxSeatLockQueue {
	return &PgxSeatLockQueue{db: db}
}

// Enqueue parks a request at the tail of the queue.
//
// The insert is idempotent per queue token (token_id is UNIQUE): a client
// that retries with the same token while Redis is down must not create a
// second parked request, otherwise the replay would book the buyer twice.
func (q *PgxSeatLockQueue) Enqueue(ctx context.Context, req QueuedSeatLock) error {
	seats, err := json.Marshal(req.SeatPositions)
	if err != nil {
		return fmt.Errorf("booking: marshal queued seat positions: %w", err)
	}
	_, err = q.db.Exec(ctx, `
		INSERT INTO seat_lock_queue
			(id, token_id, event_id, section_id, leader_user_id, seat_positions,
			 total_seats, total_amount, payment_mode, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (token_id) DO NOTHING`,
		req.ID, req.TokenID, req.EventID, req.SectionID, req.LeaderUserID, seats,
		req.TotalSeats, req.TotalAmount, req.PaymentMode, req.ExpiresAt)
	if err != nil {
		return fmt.Errorf("booking: enqueue seat lock: %w", err)
	}
	return nil
}

// Dequeue returns up to limit pending requests, oldest first.
//
// The rows stay in the table: the caller acknowledges them with Ack once the
// replay succeeded, so a crash between replay and ack replays the request
// again instead of losing it (the seat lock itself is atomic, so the replay
// either wins or conflicts).
func (q *PgxSeatLockQueue) Dequeue(ctx context.Context, limit int) ([]QueuedSeatLock, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := q.db.Query(ctx, `
		SELECT id, token_id, event_id, section_id, leader_user_id, seat_positions,
		       total_seats, total_amount, payment_mode, expires_at
		FROM seat_lock_queue
		ORDER BY created_at ASC, id ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("booking: dequeue seat locks: %w", err)
	}
	defer rows.Close()

	var out []QueuedSeatLock
	for rows.Next() {
		var (
			item  QueuedSeatLock
			seats []byte
		)
		if err := rows.Scan(&item.ID, &item.TokenID, &item.EventID, &item.SectionID,
			&item.LeaderUserID, &seats, &item.TotalSeats, &item.TotalAmount,
			&item.PaymentMode, &item.ExpiresAt); err != nil {
			return nil, fmt.Errorf("booking: scan queued seat lock: %w", err)
		}
		if err := json.Unmarshal(seats, &item.SeatPositions); err != nil {
			return nil, fmt.Errorf("booking: unmarshal queued seat positions: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("booking: iterate queued seat locks: %w", err)
	}
	return out, nil
}

// Ack removes a successfully replayed request from the queue.
func (q *PgxSeatLockQueue) Ack(ctx context.Context, id uuid.UUID) error {
	if _, err := q.db.Exec(ctx, `DELETE FROM seat_lock_queue WHERE id = $1`, id); err != nil {
		return fmt.Errorf("booking: ack queued seat lock: %w", err)
	}
	return nil
}

// Fail removes a request that can never be replayed (e.g. the seats were
// taken meanwhile). The reason is logged by the caller.
func (q *PgxSeatLockQueue) Fail(ctx context.Context, id uuid.UUID, reason string) error {
	if _, err := q.db.Exec(ctx, `DELETE FROM seat_lock_queue WHERE id = $1`, id); err != nil {
		return fmt.Errorf("booking: fail queued seat lock (%s): %w", reason, err)
	}
	return nil
}

// DiscardByToken removes a parked request that was superseded by a direct
// booking (the client retried successfully once Redis recovered).
func (q *PgxSeatLockQueue) DiscardByToken(ctx context.Context, tokenID string) error {
	if _, err := q.db.Exec(ctx, `DELETE FROM seat_lock_queue WHERE token_id = $1`, tokenID); err != nil {
		return fmt.Errorf("booking: discard queued seat lock: %w", err)
	}
	return nil
}
