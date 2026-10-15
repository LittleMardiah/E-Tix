package payment

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

// fakeRepoL2 implements PaymentObligationRepository for L2 tests.
type fakeRepoL2 struct {
	stored *PaymentObligation
	err    error
}

func (f *fakeRepoL2) Create(ctx context.Context, o *PaymentObligation) error { return nil }
func (f *fakeRepoL2) GetByID(ctx context.Context, id uuid.UUID) (*PaymentObligation, error) {
	return nil, ErrObligationNotFound
}
func (f *fakeRepoL2) GetByExternalTxID(ctx context.Context, txID string) (*PaymentObligation, error) {
	return nil, ErrWebhookNotFound
}
func (f *fakeRepoL2) GetByIdempotencyKey(ctx context.Context, key string) (*PaymentObligation, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.stored, nil
}
func (f *fakeRepoL2) ListByBooking(ctx context.Context, bookingID uuid.UUID) ([]PaymentObligation, error) {
	return nil, nil
}

func (f *fakeRepoL2) GetByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*PaymentObligation, error) {
	return nil, ErrObligationNotFound
}

func (f *fakeRepoL2) GetByExternalTxIDForUpdate(ctx context.Context, tx pgx.Tx, txID string) (*PaymentObligation, error) {
	return nil, ErrWebhookNotFound
}

func (f *fakeRepoL2) UpdateStatusWithWebhook(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	return nil
}

func (f *fakeRepoL2) CheckUserActiveForUpdate(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (bool, error) {
	return true, nil
}

func (f *fakeRepoL2) AssignTicketAtomic(ctx context.Context, tx pgx.Tx, ticketID, bookingID, targetUserID uuid.UUID) (int64, error) {
	return 1, nil
}

func newTestIdempotencyService(t *testing.T, repo PaymentObligationRepository) (*IdempotencyService, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = rdb.Close() })
	return NewIdempotencyService(rdb, repo), mr
}

func TestComputeIdempotencyHash_Deterministic(t *testing.T) {
	uid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	bid := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	h1 := ComputeIdempotencyHash("client-key-abc", uid, bid)
	h2 := ComputeIdempotencyHash("client-key-abc", uid, bid)
	assert.Equal(t, h1, h2, "same inputs must produce same hash")

	// SHA-256 hex = 64 chars.
	assert.Len(t, h1, 64)

	// Different client key -> different hash.
	h3 := ComputeIdempotencyHash("client-key-xyz", uid, bid)
	assert.NotEqual(t, h1, h3)

	// Different user -> different hash.
	h4 := ComputeIdempotencyHash("client-key-abc", uuid.New(), bid)
	assert.NotEqual(t, h1, h4)

	// Different booking -> different hash.
	h5 := ComputeIdempotencyHash("client-key-abc", uid, uuid.New())
	assert.NotEqual(t, h1, h5)
}

func TestIdempotency_L1_HitAndMiss(t *testing.T) {
	svc, _ := newTestIdempotencyService(t, &fakeRepoL2{})
	ctx := context.Background()

	hash := "hash-l1-test"
	body := []byte(`{"booking_id":"b123","payment_status":"PENDING"}`)

	// Miss first.
	got, hit, err := svc.LookupL1(ctx, hash)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Nil(t, got)

	// Store then hit.
	require.NoError(t, svc.StoreL1(ctx, hash, body))
	got, hit, err = svc.LookupL1(ctx, hash)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, body, got)
}

func TestIdempotency_L1_TTL24h(t *testing.T) {
	svc, mr := newTestIdempotencyService(t, &fakeRepoL2{})
	ctx := context.Background()

	hash := "hash-ttl-test"
	require.NoError(t, svc.StoreL1(ctx, hash, []byte(`{"ok":true}`)))

	// TTL should be approx 24h — check it's between 23h and 25h.
	ttl := mr.TTL(IdempotencyL1Prefix + hash)
	assert.Greater(t, ttl, 23*time.Hour)
	assert.LessOrEqual(t, ttl, IdempotencyL1TTL)
}

func TestIdempotency_L2_Miss(t *testing.T) {
	svc, _ := newTestIdempotencyService(t, &fakeRepoL2{stored: nil})
	ctx := context.Background()

	o, err := svc.LookupL2(ctx, "non-existent")
	require.NoError(t, err)
	assert.Nil(t, o, "nil,nil means safe to proceed")
}

func TestIdempotency_L2_Hit(t *testing.T) {
	existing := &PaymentObligation{
		ID:     uuid.New(),
		Status: StatusPending,
	}
	svc, _ := newTestIdempotencyService(t, &fakeRepoL2{stored: existing})
	ctx := context.Background()

	o, err := svc.LookupL2(ctx, "existing-key")
	require.NoError(t, err)
	require.NotNil(t, o)
	assert.Equal(t, existing.ID, o.ID)
}
