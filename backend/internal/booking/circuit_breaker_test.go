package booking

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sony/gobreaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// ============================================================
// P9a-12 TD-F9-17 — Redis circuit breaker + degraded mode
// ============================================================

// fakeSeatLockQueue records the parked requests.
type fakeSeatLockQueue struct {
	enqueued  []QueuedSeatLock
	acked     []uuid.UUID
	failed    []uuid.UUID
	discarded []string
	enqErr    error
}

func (f *fakeSeatLockQueue) Enqueue(_ context.Context, req QueuedSeatLock) error {
	if f.enqErr != nil {
		return f.enqErr
	}
	f.enqueued = append(f.enqueued, req)
	return nil
}

func (f *fakeSeatLockQueue) Dequeue(_ context.Context, _ int) ([]QueuedSeatLock, error) {
	return nil, nil
}

func (f *fakeSeatLockQueue) Ack(_ context.Context, id uuid.UUID) error {
	f.acked = append(f.acked, id)
	return nil
}

func (f *fakeSeatLockQueue) Fail(_ context.Context, id uuid.UUID, _ string) error {
	f.failed = append(f.failed, id)
	return nil
}

func (f *fakeSeatLockQueue) DiscardByToken(_ context.Context, tokenID string) error {
	f.discarded = append(f.discarded, tokenID)
	return nil
}

func newCBService(t *testing.T, repo BookingRepository, rdb *redis.Client, pub DelayedPublisher, cb *gobreaker.CircuitBreaker, q SeatLockQueue) *BookingService {
	t.Helper()
	scripts := &LuaScripts{Lock: lockSeatsLua, Release: releaseSeatsLua}
	qt := newBookingTestQueueToken(t)
	return NewBookingServiceWithCircuitBreaker(repo, rdb, scripts, qt, pub, cb, q)
}

// TestLockSeats_CircuitBreakerTripsAfterConsecutiveFailures proves the
// breaker counts Redis failures and trips after the configured number of
// consecutive failures (LOGIC FLOW §9.4: "Redis down > 100ms -> CB OPEN").
func TestLockSeats_CircuitBreakerTripsAfterConsecutiveFailures(t *testing.T) {
	rdb := newDeadRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	pub := &fakePublisher{}
	q := &fakeSeatLockQueue{}
	cb := DefaultRedisCircuitBreaker()
	svc := newCBService(t, repo, rdb, pub, cb, q)

	ctx := context.Background()
	// The queue-token marker is the first Redis call of the flow, so the
	// breaker sees the outage first. The token stays usable because the
	// marker write never succeeds.
	tok := newValidToken(t, svc.queueToken)
	for i := 0; i < int(cbConsecutiveFailures); i++ {
		_, err := svc.LockSeats(ctx, uuid.New(), eid, sid,
			&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrServiceUnavailable,
			"attempt %d must still be a normal Redis failure", i+1)
	}
	assert.Equal(t, gobreaker.StateClosed, cb.State(),
		"breaker must stay closed until the failure threshold is exceeded")

	// Next failure trips the breaker (> 3 consecutive failures).
	_, err := svc.LockSeats(ctx, uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.Equal(t, gobreaker.StateOpen, cb.State(), "breaker must be open after the threshold")
	assert.True(t, svc.RedisCircuitBreakerOpen())
}

// TestLockSeats_CircuitBreakerOpen_ReturnsServiceUnavailableAndQueues proves
// the degraded mode: the request is parked in the durable queue and the
// client gets 503 instead of a Redis timeout.
func TestLockSeats_CircuitBreakerOpen_ReturnsServiceUnavailableAndQueues(t *testing.T) {
	rdb := newDeadRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	pub := &fakePublisher{}
	q := &fakeSeatLockQueue{}
	cb := DefaultRedisCircuitBreaker()
	svc := newCBService(t, repo, rdb, pub, cb, q)

	// Trip the breaker with a failing Redis call.
	for i := 0; i <= int(cbConsecutiveFailures); i++ {
		_, _ = cb.Execute(func() (interface{}, error) { return nil, errors.New("redis down") })
	}
	require.Equal(t, gobreaker.StateOpen, cb.State())

	tok := newValidToken(t, svc.queueToken)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1, 2}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrServiceUnavailable)

	require.Len(t, q.enqueued, 1, "the request must be parked in the DB queue")
	assert.Equal(t, eid, q.enqueued[0].EventID)
	assert.Equal(t, sid, q.enqueued[0].SectionID)
	assert.Equal(t, []int{1, 2}, q.enqueued[0].SeatPositions)
	assert.Equal(t, 2, q.enqueued[0].TotalSeats)
	assert.Equal(t, "SPLIT", q.enqueued[0].PaymentMode)
	assert.NotEmpty(t, q.enqueued[0].TokenID)
}

// TestLockSeats_CircuitBreakerOpen_WithoutQueue_ReturnsServiceUnavailable
// proves the service degrades safely even without a configured queue.
func TestLockSeats_CircuitBreakerOpen_WithoutQueue_ReturnsServiceUnavailable(t *testing.T) {
	rdb := newDeadRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	cb := DefaultRedisCircuitBreaker()
	svc := newCBService(t, repo, rdb, &fakePublisher{}, cb, nil)

	for i := 0; i <= int(cbConsecutiveFailures); i++ {
		_, _ = cb.Execute(func() (interface{}, error) { return nil, errors.New("redis down") })
	}

	tok := newValidToken(t, svc.queueToken)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrServiceUnavailable)
}

// TestLockSeats_HealthyRedis_CircuitBreakerStaysClosed proves the breaker
// does not trip on the happy path.
func TestLockSeats_HealthyRedis_CircuitBreakerStaysClosed(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	cb := DefaultRedisCircuitBreaker()
	svc := newCBService(t, repo, rdb, &fakePublisher{}, cb, nil)

	tok := newValidToken(t, svc.queueToken)
	resp, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, gobreaker.StateClosed, cb.State())
	assert.False(t, svc.RedisCircuitBreakerOpen())
}

// TestLockSeats_Success_DiscardsSupersededQueuedRequest proves a direct
// booking supersedes the parked request with the same queue token (no double
// booking after the drain).
func TestLockSeats_Success_DiscardsSupersededQueuedRequest(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	q := &fakeSeatLockQueue{}
	svc := newCBService(t, repo, rdb, &fakePublisher{}, DefaultRedisCircuitBreaker(), q)

	tok := newValidToken(t, svc.queueToken)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)
	require.NoError(t, err)
	require.Len(t, q.discarded, 1, "the parked copy of the token must be discarded")
}

// TestLockQueuedSeats_ReplayCreatesBooking proves the drain replays a parked
// request into a real booking (zero data loss).
func TestLockQueuedSeats_ReplayCreatesBooking(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	uid := uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	pub := &fakePublisher{}
	svc := newCBService(t, repo, rdb, pub, DefaultRedisCircuitBreaker(), nil)

	req := QueuedSeatLock{
		ID:            uuid.New(),
		TokenID:       uuid.New().String(),
		EventID:       eid,
		SectionID:     sid,
		LeaderUserID:  uid,
		SeatPositions: []int{3, 4},
		TotalSeats:    2,
		PaymentMode:   "SPLIT",
		ExpiresAt:     time.Now().Add(time.Minute),
	}
	require.NoError(t, svc.LockQueuedSeats(context.Background(), req))

	require.NotNil(t, repo.createdBooking, "the replay must create the booking")
	assert.Equal(t, uid, repo.createdBooking.LeaderUserID)
	assert.Equal(t, []int{3, 4}, repo.createdBooking.SeatPositions)
	assert.Equal(t, "PENDING", repo.createdBooking.PaymentStatus)

	// The seats are really locked in the Redis bitmap.
	b0, err := rdb.GetBit(context.Background(), bitmapKey(eid, sid), 2).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), b0)
}

// TestLockQueuedSeats_ExpiredAndConflict proves the permanent failures are
// reported so the worker can drop the request instead of retrying forever.
func TestLockQueuedSeats_ExpiredAndConflict(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc := newCBService(t, repo, rdb, &fakePublisher{}, DefaultRedisCircuitBreaker(), nil)

	// Expired request.
	err := svc.LockQueuedSeats(context.Background(), QueuedSeatLock{
		EventID:       eid,
		SectionID:     sid,
		SeatPositions: []int{1},
		ExpiresAt:     time.Now().Add(-time.Minute),
	})
	assert.ErrorIs(t, err, ErrQueuedSeatLockExpired)

	// Conflicting seats.
	ctx := context.Background()
	require.NoError(t, rdb.SetBit(ctx, bitmapKey(eid, sid), 0, 1).Err())
	err = svc.LockQueuedSeats(ctx, QueuedSeatLock{
		EventID:       eid,
		SectionID:     sid,
		SeatPositions: []int{1},
		ExpiresAt:     time.Now().Add(time.Minute),
	})
	assert.ErrorIs(t, err, ErrSeatsNotAvailable)
}

// TestLockSeatsHandler_CircuitBreakerOpen_Returns503 proves the HTTP layer
// maps the degraded mode to 503 Service Unavailable.
func TestLockSeatsHandler_CircuitBreakerOpen_Returns503(t *testing.T) {
	rdb := newDeadRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	cb := DefaultRedisCircuitBreaker()
	svc := newCBService(t, repo, rdb, &fakePublisher{}, cb, &fakeSeatLockQueue{})

	for i := 0; i <= int(cbConsecutiveFailures); i++ {
		_, _ = cb.Execute(func() (interface{}, error) { return nil, errors.New("redis down") })
	}

	h := NewBookingHandler(svc, rdb, zap.NewNop())
	r := buildBookingTestRouter(h)

	w := doBookingRequestWithIdem(t, r, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), newValidToken(t, svc.queueToken), "idem-503",
		`{"seat_positions":[1],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"code":"SERVICE_UNAVAILABLE"`)
	assert.Equal(t, "30", w.Header().Get("Retry-After"))
}
