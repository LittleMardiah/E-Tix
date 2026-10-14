package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// ============================================================
// Fakes
// ============================================================

// fakeTx embeds pgx.Tx (nil) and overrides only Commit + Rollback, which the
// service calls. Embedding pgx.Tx lets the value satisfy the full interface
// without stubbing every method. Any other call would panic — none occur
// in the tested code paths.
type fakeTx struct {
	pgx.Tx
	committed  bool
	rolledBack bool
}

func (f *fakeTx) Commit(_ context.Context) error {
	f.committed = true
	return nil
}
func (f *fakeTx) Rollback(_ context.Context) error {
	if f.committed {
		return nil // mirror pgx: Rollback after Commit is a no-op
	}
	f.rolledBack = true
	return nil
}

// fakeBeginner implements TxBeginner.
type fakeBeginner struct {
	tx  *fakeTx
	err error
}

func (f *fakeBeginner) Begin(_ context.Context) (pgx.Tx, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.tx == nil {
		f.tx = &fakeTx{}
	}
	return f.tx, nil
}

// fakeBookingReader implements BookingReader.
type fakeBookingReader struct {
	snap *BookingSnapshot
	err  error
}

func (f *fakeBookingReader) GetByID(_ context.Context, _ uuid.UUID) (*BookingSnapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.snap, nil
}

func (f *fakeBookingReader) GetByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*BookingSnapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.snap, nil
}

// fakeGateway implements adapter.PaymentGateway.
type fakeGateway struct {
	initiateErr error
	refundErr   error
	lastTxID    string
}

func (g *fakeGateway) Initiate(_ context.Context, req adapter.InitiateRequest) (*adapter.InitiateResponse, error) {
	if g.initiateErr != nil {
		return nil, g.initiateErr
	}
	g.lastTxID = "fake-tx-" + req.BookingID[:8]
	return &adapter.InitiateResponse{
		TransactionID: g.lastTxID,
		PaymentLink:   "https://fake.pay/" + g.lastTxID,
	}, nil
}

func (g *fakeGateway) Refund(_ context.Context, _ adapter.RefundRequest) (*adapter.RefundResponse, error) {
	if g.refundErr != nil {
		return nil, g.refundErr
	}
	return &adapter.RefundResponse{RefundID: "refund-fake"}, nil
}

func (g *fakeGateway) Payout(_ context.Context, _ adapter.PayoutRequest) (*adapter.PayoutResponse, error) {
	return &adapter.PayoutResponse{PayoutID: "payout-fake"}, nil
}

func (g *fakeGateway) GetStatus(_ context.Context, _ string) (*adapter.StatusResponse, error) {
	return &adapter.StatusResponse{TransactionStatus: "settlement"}, nil
}

func (g *fakeGateway) ValidateWebhookSignature(_ map[string]string, _ []byte) error { return nil }

// ============================================================
// Helpers
// ============================================================

func newTestPaymentEnv(t *testing.T, snap *BookingSnapshot, bkErr error, gwErr error) (*PaymentService, *fakeBeginner, *fakeGateway, *PaymentObligationRepository) {
	t.Helper()

	mr := miniredis.RunT(t)
	rdb := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = rdb.Close() })

	memRepo := &memObligationRepo{}
	beginner := &fakeBeginner{tx: &fakeTx{}}
	reader := &fakeBookingReader{snap: snap, err: bkErr}
	gw := &fakeGateway{initiateErr: gwErr}
	idem := NewIdempotencyService(rdb, memRepo)
	svc := NewPaymentService(beginner, memRepo, reader, gw, idem)

	// Return pointer to interface as a convenience for callers.
	var repoPtr PaymentObligationRepository = memRepo
	return svc, beginner, gw, &repoPtr
}

// memObligationRepo is an in-memory PaymentObligationRepository.
type memObligationRepo struct {
	byID  map[uuid.UUID]*PaymentObligation
	byKey map[string]*PaymentObligation
}

func (m *memObligationRepo) ensure() {
	if m.byID == nil {
		m.byID = map[uuid.UUID]*PaymentObligation{}
		m.byKey = map[string]*PaymentObligation{}
	}
}

func (m *memObligationRepo) Create(_ context.Context, o *PaymentObligation) error {
	m.ensure()
	m.byID[o.ID] = o
	if o.IdempotencyKey != nil {
		m.byKey[*o.IdempotencyKey] = o
	}
	return nil
}

func (m *memObligationRepo) GetByID(_ context.Context, id uuid.UUID) (*PaymentObligation, error) {
	m.ensure()
	if o, ok := m.byID[id]; ok {
		return o, nil
	}
	return nil, ErrObligationNotFound
}

func (m *memObligationRepo) GetByExternalTxID(_ context.Context, txID string) (*PaymentObligation, error) {
	m.ensure()
	for _, o := range m.byID {
		if o.ExternalTransactionID != nil && *o.ExternalTransactionID == txID {
			return o, nil
		}
	}
	return nil, ErrWebhookNotFound
}

func (m *memObligationRepo) GetByIdempotencyKey(_ context.Context, key string) (*PaymentObligation, error) {
	m.ensure()
	if o, ok := m.byKey[key]; ok {
		return o, nil
	}
	return nil, nil
}

func (m *memObligationRepo) ListByBooking(_ context.Context, bookingID uuid.UUID) ([]PaymentObligation, error) {
	m.ensure()
	var out []PaymentObligation
	for _, o := range m.byID {
		if o.BookingID == bookingID {
			out = append(out, *o)
		}
	}
	return out, nil
}

func (m *memObligationRepo) GetByIDForUpdate(_ context.Context, _ pgx.Tx, id uuid.UUID) (*PaymentObligation, error) {
	return m.GetByID(context.Background(), id)
}

func (m *memObligationRepo) GetByExternalTxIDForUpdate(_ context.Context, _ pgx.Tx, txID string) (*PaymentObligation, error) {
	return m.GetByExternalTxID(context.Background(), txID)
}

func (m *memObligationRepo) UpdateStatusWithWebhook(_ context.Context, _ pgx.Tx, id uuid.UUID, status string) error {
	m.ensure()
	o, ok := m.byID[id]
	if !ok {
		return ErrObligationNotFound
	}
	o.Status = status
	now := time.Now().UTC()
	o.WebhookReceivedAt = &now
	return nil
}

// ============================================================
// Tests
// ============================================================

func TestInitiatePayments_Split_Success(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{
		ID:            bid,
		LeaderUserID:  leader,
		TotalAmount:   "3000000.00",
		PaymentMode:   ModeSplit,
		PaymentStatus: StatusPending,
	}
	svc, tx, gw, _ := newTestPaymentEnv(t, snap, nil, nil)

	req := &InitiatePaymentRequest{
		PaymentMode: ModeSplit,
		Members: []InitiateMember{
			{UserID: uuid.New(), SeatPosition: 1, Amount: "1000000"},
			{UserID: uuid.New(), SeatPosition: 2, Amount: "1000000"},
			{UserID: uuid.New(), SeatPosition: 3, Amount: "1000000"},
		},
	}

	resp, err := svc.InitiatePayments(context.Background(), leader, bid, req, "idem-key-split-1")
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, bid.String(), resp.BookingID)
	assert.Equal(t, StatusPending, resp.PaymentStatus)
	assert.Len(t, resp.Obligations, 3)
	assert.NotEmpty(t, gw.lastTxID)
	assert.True(t, tx.tx.committed, "tx must be committed on success")

	for i, o := range resp.Obligations {
		assert.Equal(t, "1000000.00", o.Amount, "member %d amount", i)
		require.NotNil(t, o.SeatPosition)
		assert.NotEmpty(t, o.PaymentLink)
		assert.NotEmpty(t, o.ExternalTransactionID)
	}
}

func TestInitiatePayments_Bulk_Success(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{
		ID:            bid,
		LeaderUserID:  leader,
		TotalAmount:   "4000000.00",
		PaymentMode:   ModeBulk,
		PaymentStatus: StatusPending,
	}
	svc, tx, _, _ := newTestPaymentEnv(t, snap, nil, nil)

	req := &InitiatePaymentRequest{
		PaymentMode:   ModeBulk,
		LeaderPaysAll: true,
		Amount:        "4000000",
	}

	resp, err := svc.InitiatePayments(context.Background(), leader, bid, req, "idem-key-bulk-1")
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.Obligations, 1)
	assert.Equal(t, "4000000.00", resp.Obligations[0].Amount)
	assert.Nil(t, resp.Obligations[0].SeatPosition, "BULK seat_position must be NULL")
	assert.True(t, tx.tx.committed)
}

func TestInitiatePayments_InvalidMode(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc, tx, _, _ := newTestPaymentEnv(t, snap, nil, nil)

	req := &InitiatePaymentRequest{PaymentMode: "WIRE", Amount: "1000"}
	_, err := svc.InitiatePayments(context.Background(), leader, bid, req, "idem-key")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPaymentMode)
	assert.False(t, tx.tx.committed, "tx must not commit on validation failure")
}

func TestInitiatePayments_NotLeader(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	other := uuid.New()
	snap := &BookingSnapshot{
		ID:            bid,
		LeaderUserID:  leader,
		PaymentStatus: StatusPending,
	}
	svc, tx, _, _ := newTestPaymentEnv(t, snap, nil, nil)

	req := &InitiatePaymentRequest{
		PaymentMode: ModeBulk,
		Amount:      "1000",
	}
	_, err := svc.InitiatePayments(context.Background(), other, bid, req, "idem-key")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotLeader)
	assert.False(t, tx.tx.committed)
}

func TestInitiatePayments_BookingNotPending(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{
		ID:            bid,
		LeaderUserID:  leader,
		PaymentStatus: StatusPaid, // already paid
	}
	svc, tx, _, _ := newTestPaymentEnv(t, snap, nil, nil)

	req := &InitiatePaymentRequest{PaymentMode: ModeBulk, Amount: "1000"}
	_, err := svc.InitiatePayments(context.Background(), leader, bid, req, "idem-key")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBookingNotPending)
	assert.False(t, tx.tx.committed)
}

func TestInitiatePayments_L1Hit_ReturnsCached(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{
		ID:            bid,
		LeaderUserID:  leader,
		PaymentStatus: StatusPending,
	}
	svc, tx, gw, _ := newTestPaymentEnv(t, snap, nil, nil)
	ctx := context.Background()

	req := &InitiatePaymentRequest{
		PaymentMode: ModeBulk,
		Amount:      "1000",
	}

	// First call: obligations created, L1 cached.
	resp1, err := svc.InitiatePayments(ctx, leader, bid, req, "idem-cache-hit")
	require.NoError(t, err)
	require.NotNil(t, resp1)
	firstTxID := gw.lastTxID
	assert.True(t, tx.tx.committed)

	// Reset tx state marker; second call must NOT begin a new tx (L1 short-circuit).
	tx.tx.committed = false

	resp2, err := svc.InitiatePayments(ctx, leader, bid, req, "idem-cache-hit")
	require.NoError(t, err)
	require.NotNil(t, resp2)

	assert.Equal(t, resp1.BookingID, resp2.BookingID)
	assert.Equal(t, len(resp1.Obligations), len(resp2.Obligations))
	assert.Equal(t, firstTxID, gw.lastTxID, "gateway must NOT be called again on cache hit")
	assert.False(t, tx.tx.committed, "tx must NOT be begun on L1 cache hit")
}

func TestInitiatePayments_DuplicateSeat(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{
		ID:            bid,
		LeaderUserID:  leader,
		PaymentStatus: StatusPending,
	}
	svc, tx, _, _ := newTestPaymentEnv(t, snap, nil, nil)

	req := &InitiatePaymentRequest{
		PaymentMode: ModeSplit,
		Members: []InitiateMember{
			{UserID: uuid.New(), SeatPosition: 5, Amount: "1000"},
			{UserID: uuid.New(), SeatPosition: 5, Amount: "1000"}, // duplicate
		},
	}
	_, err := svc.InitiatePayments(context.Background(), leader, bid, req, "idem-dup-seat")
	require.Error(t, err)
	assert.False(t, tx.tx.committed)
	assert.Contains(t, err.Error(), "duplicate seat_position")
}

func TestInitiatePayments_GatewayError(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{
		ID:            bid,
		LeaderUserID:  leader,
		PaymentStatus: StatusPending,
	}
	gwErr := errors.New("gateway down")
	svc, tx, _, _ := newTestPaymentEnv(t, snap, nil, gwErr)

	req := &InitiatePaymentRequest{PaymentMode: ModeBulk, Amount: "1000"}
	_, err := svc.InitiatePayments(context.Background(), leader, bid, req, "idem-gw-err")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway")
	assert.False(t, tx.tx.committed)
}
