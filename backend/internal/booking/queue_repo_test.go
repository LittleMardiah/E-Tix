//go:build integration

package booking

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newQueueTestPool connects to TEST_DB_URL and loads fixtures/seed.sql.
func newQueueTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping seat-lock queue integration tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))
	t.Cleanup(pool.Close)

	seedBytes, err := os.ReadFile("../../tests/fixtures/seed.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seedBytes), pgx.QueryExecModeSimpleProtocol)
	require.NoError(t, err)

	// Start from a clean queue (a crashed previous run may have left rows).
	_, err = pool.Exec(ctx, `DELETE FROM seat_lock_queue`)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM seat_lock_queue`)
	})

	return pool
}

func queuedRequest(t *testing.T, tokenID string) QueuedSeatLock {
	t.Helper()
	return QueuedSeatLock{
		ID:            uuid.New(),
		TokenID:       tokenID,
		EventID:       repoSeedEventID,
		SectionID:     repoSeedVIPSectID,
		LeaderUserID:  repoSeedCustomerID,
		SeatPositions: []int{1, 2},
		TotalSeats:    2,
		TotalAmount:   "4000000.00",
		PaymentMode:   "SPLIT",
		ExpiresAt:     time.Now().Add(3 * time.Minute),
	}
}

func TestQueueRepo_EnqueueDequeueAck(t *testing.T) {
	pool := newQueueTestPool(t)
	q := NewPgxSeatLockQueue(pool)
	ctx := context.Background()

	req := queuedRequest(t, uuid.NewString())
	require.NoError(t, q.Enqueue(ctx, req))

	items, err := q.Dequeue(ctx, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, req.ID, items[0].ID)
	assert.Equal(t, req.TokenID, items[0].TokenID)
	assert.Equal(t, []int{1, 2}, items[0].SeatPositions)
	assert.Equal(t, repoSeedVIPSectID, items[0].SectionID)

	require.NoError(t, q.Ack(ctx, req.ID))

	items, err = q.Dequeue(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, items, "the acknowledged request must be gone")
}

func TestQueueRepo_EnqueueIsIdempotentPerToken(t *testing.T) {
	pool := newQueueTestPool(t)
	q := NewPgxSeatLockQueue(pool)
	ctx := context.Background()

	tokenID := uuid.NewString()
	require.NoError(t, q.Enqueue(ctx, queuedRequest(t, tokenID)))
	// Same token, different payload: the second insert must be ignored.
	second := queuedRequest(t, tokenID)
	second.SeatPositions = []int{7}
	require.NoError(t, q.Enqueue(ctx, second))

	items, err := q.Dequeue(ctx, 10)
	require.NoError(t, err)
	require.Len(t, items, 1, "the same queue token must not be parked twice")
	assert.Equal(t, []int{1, 2}, items[0].SeatPositions)
}

func TestQueueRepo_DequeueIsFIFO(t *testing.T) {
	pool := newQueueTestPool(t)
	q := NewPgxSeatLockQueue(pool)
	ctx := context.Background()

	first := queuedRequest(t, uuid.NewString())
	second := queuedRequest(t, uuid.NewString())
	require.NoError(t, q.Enqueue(ctx, first))
	require.NoError(t, q.Enqueue(ctx, second))

	items, err := q.Dequeue(ctx, 10)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, first.ID, items[0].ID)
	assert.Equal(t, second.ID, items[1].ID)
}

func TestQueueRepo_FailRemovesRequest(t *testing.T) {
	pool := newQueueTestPool(t)
	q := NewPgxSeatLockQueue(pool)
	ctx := context.Background()

	req := queuedRequest(t, uuid.NewString())
	require.NoError(t, q.Enqueue(ctx, req))
	require.NoError(t, q.Fail(ctx, req.ID, "seats taken"))

	items, err := q.Dequeue(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestQueueRepo_DiscardByToken(t *testing.T) {
	pool := newQueueTestPool(t)
	q := NewPgxSeatLockQueue(pool)
	ctx := context.Background()

	tokenID := uuid.NewString()
	req := queuedRequest(t, tokenID)
	require.NoError(t, q.Enqueue(ctx, req))
	require.NoError(t, q.DiscardByToken(ctx, tokenID))

	items, err := q.Dequeue(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, items)
}
