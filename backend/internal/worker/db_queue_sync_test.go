package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/booking"
)

// ============================================================
// P9a-12 TD-F9-17 — DB queue sync worker
// ============================================================

type fakeQueueRepo struct {
	items     []booking.QueuedSeatLock
	dequeued  int
	acked     []uuid.UUID
	failed    []uuid.UUID
	failCalls int
	deqErr    error
}

func (f *fakeQueueRepo) Dequeue(_ context.Context, limit int) ([]booking.QueuedSeatLock, error) {
	f.dequeued++
	if f.deqErr != nil {
		return nil, f.deqErr
	}
	if limit > len(f.items) {
		return f.items, nil
	}
	return f.items[:limit], nil
}

func (f *fakeQueueRepo) Ack(_ context.Context, id uuid.UUID) error {
	f.acked = append(f.acked, id)
	return nil
}

func (f *fakeQueueRepo) Fail(_ context.Context, id uuid.UUID, _ string) error {
	f.failed = append(f.failed, id)
	f.failCalls++
	return nil
}

type fakeSeatLocker struct {
	calls   []booking.QueuedSeatLock
	err     error
	errOnce error
}

func (f *fakeSeatLocker) LockQueuedSeats(_ context.Context, req booking.QueuedSeatLock) error {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return f.err
	}
	return nil
}

func queuedItem(id uuid.UUID) booking.QueuedSeatLock {
	return booking.QueuedSeatLock{
		ID:            id,
		TokenID:       uuid.NewString(),
		EventID:       uuid.New(),
		SectionID:     uuid.New(),
		LeaderUserID:  uuid.New(),
		SeatPositions: []int{1},
		TotalSeats:    1,
		PaymentMode:   "SPLIT",
		ExpiresAt:     time.Now().Add(time.Minute),
	}
}

// TestDBQueueSyncWorker_SkipsWhileCircuitBreakerOpen proves the worker does
// not touch the queue while Redis is still down (degraded mode).
func TestDBQueueSyncWorker_SkipsWhileCircuitBreakerOpen(t *testing.T) {
	repo := &fakeQueueRepo{items: []booking.QueuedSeatLock{queuedItem(uuid.New())}}
	locker := &fakeSeatLocker{}
	w := NewDBQueueSyncWorker(repo, locker, func() bool { return false }, zap.NewNop())

	require.NoError(t, w.Sync(context.Background()))
	assert.Zero(t, repo.dequeued, "the queue must not be read while the breaker is open")
	assert.Empty(t, locker.calls)
}

// TestDBQueueSyncWorker_DrainsQueuedRequests proves the FIFO drain once
// Redis recovered.
func TestDBQueueSyncWorker_DrainsQueuedRequests(t *testing.T) {
	first, second := queuedItem(uuid.New()), queuedItem(uuid.New())
	repo := &fakeQueueRepo{items: []booking.QueuedSeatLock{first, second}}
	locker := &fakeSeatLocker{}
	w := NewDBQueueSyncWorker(repo, locker, func() bool { return true }, zap.NewNop())

	require.NoError(t, w.Sync(context.Background()))
	require.Len(t, locker.calls, 2)
	assert.Equal(t, first.ID, locker.calls[0].ID, "FIFO order must be preserved")
	assert.Equal(t, second.ID, locker.calls[1].ID)
	assert.ElementsMatch(t, []uuid.UUID{first.ID, second.ID}, repo.acked)
	assert.Empty(t, repo.failed)
}

// TestDBQueueSyncWorker_PermanentErrorDropsRequest proves a request that can
// never succeed is removed from the queue instead of being retried forever.
func TestDBQueueSyncWorker_PermanentErrorDropsRequest(t *testing.T) {
	item := queuedItem(uuid.New())
	repo := &fakeQueueRepo{items: []booking.QueuedSeatLock{item}}
	locker := &fakeSeatLocker{err: booking.ErrSeatsNotAvailable}
	w := NewDBQueueSyncWorker(repo, locker, func() bool { return true }, zap.NewNop())

	require.NoError(t, w.Sync(context.Background()))
	assert.Empty(t, repo.acked)
	require.Len(t, repo.failed, 1)
	assert.Equal(t, item.ID, repo.failed[0])
}

// TestDBQueueSyncWorker_TransientErrorKeepsRequest proves a transient error
// keeps the request queued (zero data loss).
func TestDBQueueSyncWorker_TransientErrorKeepsRequest(t *testing.T) {
	repo := &fakeQueueRepo{items: []booking.QueuedSeatLock{queuedItem(uuid.New())}}
	locker := &fakeSeatLocker{err: errors.New("redis timeout")}
	w := NewDBQueueSyncWorker(repo, locker, func() bool { return true }, zap.NewNop())

	require.NoError(t, w.Sync(context.Background()))
	assert.Empty(t, repo.acked, "a transient error must not remove the request")
	assert.Empty(t, repo.failed)
}

// TestDBQueueSyncWorker_DequeueError proves a DB failure is reported.
func TestDBQueueSyncWorker_DequeueError(t *testing.T) {
	repo := &fakeQueueRepo{deqErr: errors.New("db down")}
	w := NewDBQueueSyncWorker(repo, &fakeSeatLocker{}, func() bool { return true }, zap.NewNop())

	err := w.Sync(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dequeue")
}

// TestDBQueueSyncWorker_RunStopsOnContextCancel proves the worker loop
// respects the context.
func TestDBQueueSyncWorker_RunStopsOnContextCancel(t *testing.T) {
	repo := &fakeQueueRepo{}
	w := NewDBQueueSyncWorker(repo, &fakeSeatLocker{}, func() bool { return true }, zap.NewNop()).
		WithInterval(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop on context cancel")
	}
}

// TestDBQueueSyncWorker_EmptyQueueIsNoop proves an empty queue costs nothing.
func TestDBQueueSyncWorker_EmptyQueueIsNoop(t *testing.T) {
	repo := &fakeQueueRepo{}
	locker := &fakeSeatLocker{}
	w := NewDBQueueSyncWorker(repo, locker, func() bool { return true }, zap.NewNop())

	require.NoError(t, w.Sync(context.Background()))
	assert.Empty(t, locker.calls)
	assert.Empty(t, repo.acked)
}
