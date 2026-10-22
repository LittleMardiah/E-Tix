package booking

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// QueuedSeatLock is a seat-lock request parked in the durable DB queue while
// the Redis circuit breaker is open.
//
// P9a-12 TD-F9-17: Redis holds the seat bitmap, so a Redis outage must not
// lose the request. The request is stored in PostgreSQL (table
// seat_lock_queue) and replayed by the DBQueueSyncWorker as soon as the
// breaker closed again (LOGIC FLOW §9.4, ROADMAP FASE 9 B1-T13: "zero data
// loss").
type QueuedSeatLock struct {
	ID uuid.UUID `json:"id"`
	// TokenID is the queue-token jti and the dedup key of the queue: the
	// same token must never be parked twice.
	TokenID       string    `json:"token_id"`
	EventID       uuid.UUID `json:"event_id"`
	SectionID     uuid.UUID `json:"section_id"`
	LeaderUserID  uuid.UUID `json:"leader_user_id"`
	SeatPositions []int     `json:"seat_positions"`
	TotalSeats    int       `json:"total_seats"`
	TotalAmount   string    `json:"total_amount"`
	PaymentMode   string    `json:"payment_mode"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// SeatLockQueue is the durable FIFO queue of parked seat-lock requests.
//
// Implementations MUST preserve the FIFO order of Enqueue calls: the queue is
// the fallback path of the seat-lock flow and a reordering could hand a seat
// to another buyer.
type SeatLockQueue interface {
	// Enqueue parks a request at the tail of the queue.
	Enqueue(ctx context.Context, req QueuedSeatLock) error

	// Dequeue returns up to limit parked requests, oldest first. The rows are
	// NOT removed: the caller acknowledges them with Ack once replayed.
	Dequeue(ctx context.Context, limit int) ([]QueuedSeatLock, error)

	// Ack removes a successfully replayed request from the queue.
	Ack(ctx context.Context, id uuid.UUID) error

	// Fail marks a request as permanently failed (e.g. seats already taken)
	// so it is not replayed again.
	Fail(ctx context.Context, id uuid.UUID, reason string) error

	// DiscardByToken removes a parked request that was superseded by a
	// direct booking (the client retried successfully once Redis recovered).
	DiscardByToken(ctx context.Context, tokenID string) error
}
