package payment

// STP traceability: UT-PAY-004 (mapped below).

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// bulkFakeRepo extends memObligationRepo with controllable behavior.
type bulkFakeRepo struct {
	*memObligationRepo
	checkActiveFn    func(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (bool, error)
	assignFn         func(ctx context.Context, tx pgx.Tx, ticketID, bookingID, targetUserID uuid.UUID) (int64, error)
	lastAssignedCall []uuid.UUID
}

func (b *bulkFakeRepo) CheckUserActiveForUpdate(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (bool, error) {
	if b.checkActiveFn != nil {
		return b.checkActiveFn(ctx, tx, userID)
	}
	return true, nil
}

func (b *bulkFakeRepo) AssignTicketAtomic(ctx context.Context, tx pgx.Tx, ticketID, bookingID, targetUserID uuid.UUID) (int64, error) {
	b.lastAssignedCall = append(b.lastAssignedCall, ticketID)
	if b.assignFn != nil {
		return b.assignFn(ctx, tx, ticketID, bookingID, targetUserID)
	}
	return 1, nil
}

// bulkFakeReader extends fakeBookingReader with controllable start time.
type bulkFakeReader struct {
	snap    *BookingSnapshot
	err     error
	startAt time.Time
}

func (b *bulkFakeReader) GetByID(_ context.Context, _ uuid.UUID) (*BookingSnapshot, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.snap, nil
}
func (b *bulkFakeReader) GetByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*BookingSnapshot, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.snap, nil
}
func (b *bulkFakeReader) GetEventStartAt(_ context.Context, _ uuid.UUID) (time.Time, error) {
	return b.startAt, nil
}

func newBulkEnv(t *testing.T, snap *BookingSnapshot, startAt time.Time, repo *bulkFakeRepo) (*PaymentService, *bulkFakeReader, *bulkFakeRepo) {
	t.Helper()

	if repo == nil {
		repo = &bulkFakeRepo{memObligationRepo: &memObligationRepo{}}
	}
	reader := &bulkFakeReader{snap: snap, startAt: startAt}
	beginner := &fakeBeginner{tx: &fakeTx{}}
	gw := &fakeGateway{}
	idem := NewIdempotencyService(newTestRedis(t), repo)
	svc := NewPaymentService(beginner, repo, reader, gw, idem)
	return svc, reader, repo
}

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func validBulkReq(bookingID uuid.UUID, n int) *BulkAssignRequest {
	assigns := make([]BulkAssignEntry, 0, n)
	for i := 0; i < n; i++ {
		assigns = append(assigns, BulkAssignEntry{
			TicketID: uuid.New(),
			UserID:   uuid.New(),
		})
	}
	return &BulkAssignRequest{BookingID: bookingID, Assignments: assigns}
}

// ============================================================
// Tests
// ============================================================

func TestBulkAssign_Success(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc, _, repo := newBulkEnv(t, snap, time.Now().UTC().Add(24*time.Hour), nil)

	req := validBulkReq(bid, 3)
	resp, err := svc.BulkAssign(context.Background(), leader, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 3, resp.Assigned)
	assert.Len(t, resp.Tickets, 3)
	assert.Len(t, repo.lastAssignedCall, 3)
	for _, r := range resp.Tickets {
		assert.Equal(t, "SUCCESS", r.Status)
		assert.NotEmpty(t, r.AssignedTo)
	}
}

func TestBulkAssign_NotLeader(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	other := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc, _, _ := newBulkEnv(t, snap, time.Now().UTC().Add(24*time.Hour), nil)

	req := validBulkReq(bid, 1)
	_, err := svc.BulkAssign(context.Background(), other, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotLeader)
}

// UT-PAY-004: BulkAssign_InactiveTarget_Returns422
func TestBulkAssign_InactiveTarget(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}

	repo := &bulkFakeRepo{
		memObligationRepo: &memObligationRepo{},
		checkActiveFn: func(_ context.Context, _ pgx.Tx, _ uuid.UUID) (bool, error) {
			return false, nil // inactive
		},
	}
	svc, _, _ := newBulkEnv(t, snap, time.Now().UTC().Add(24*time.Hour), repo)

	req := validBulkReq(bid, 2)
	_, err := svc.BulkAssign(context.Background(), leader, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTargetUserInactive)
}

func TestBulkAssign_TicketAlreadyAssigned(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}

	repo := &bulkFakeRepo{
		memObligationRepo: &memObligationRepo{},
		assignFn: func(_ context.Context, _ pgx.Tx, _, _, _ uuid.UUID) (int64, error) {
			return 0, nil // RowsAffected == 0 → already assigned
		},
	}
	svc, _, _ := newBulkEnv(t, snap, time.Now().UTC().Add(24*time.Hour), repo)

	req := validBulkReq(bid, 1)
	_, err := svc.BulkAssign(context.Background(), leader, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTicketAlreadyAssigned)
}

func TestBulkAssign_EventAlreadyStarted(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc, _, _ := newBulkEnv(t, snap, time.Now().UTC().Add(-1*time.Hour), nil)

	req := validBulkReq(bid, 1)
	_, err := svc.BulkAssign(context.Background(), leader, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEventAlreadyStarted)
}

func TestBulkAssign_EmptyAssignments(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc, _, _ := newBulkEnv(t, snap, time.Now().UTC().Add(24*time.Hour), nil)

	req := &BulkAssignRequest{BookingID: bid, Assignments: []BulkAssignEntry{}}
	_, err := svc.BulkAssign(context.Background(), leader, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidMembers)
}

func TestBulkAssign_NilBookingID(t *testing.T) {
	leader := uuid.New()
	svc, _, _ := newBulkEnv(t, nil, time.Now().UTC().Add(24*time.Hour), nil)

	req := &BulkAssignRequest{BookingID: uuid.Nil, Assignments: []BulkAssignEntry{
		{TicketID: uuid.New(), UserID: uuid.New()},
	}}
	_, err := svc.BulkAssign(context.Background(), leader, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidMembers)
}

func TestBulkAssign_DeterministicLockOrder(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc, _, repo := newBulkEnv(t, snap, time.Now().UTC().Add(24*time.Hour), nil)

	// Same ticket IDs but different insertion order.
	t1 := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	t2 := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	t3 := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")

	req := &BulkAssignRequest{
		BookingID: bid,
		Assignments: []BulkAssignEntry{
			{TicketID: t3, UserID: uuid.New()},
			{TicketID: t1, UserID: uuid.New()},
			{TicketID: t2, UserID: uuid.New()},
		},
	}
	_, err := svc.BulkAssign(context.Background(), leader, req)
	require.NoError(t, err)

	// Must be sorted ascending.
	require.Len(t, repo.lastAssignedCall, 3)
	assert.Equal(t, t1, repo.lastAssignedCall[0])
	assert.Equal(t, t2, repo.lastAssignedCall[1])
	assert.Equal(t, t3, repo.lastAssignedCall[2])
}

func TestBulkAssign_TxRollbackOnPartial(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}

	callCount := 0
	repo := &bulkFakeRepo{
		memObligationRepo: &memObligationRepo{},
		assignFn: func(_ context.Context, _ pgx.Tx, _, _, _ uuid.UUID) (int64, error) {
			callCount++
			if callCount == 2 {
				return 0, nil // 2nd assignment fails
			}
			return 1, nil
		},
	}
	svc, _, _ := newBulkEnv(t, snap, time.Now().UTC().Add(24*time.Hour), repo)

	req := validBulkReq(bid, 3)
	_, err := svc.BulkAssign(context.Background(), leader, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTicketAlreadyAssigned)
}
