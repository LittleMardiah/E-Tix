package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment"
)

// ============================================================
// Fakes
// ============================================================

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
		return nil
	}
	f.rolledBack = true
	return nil
}

type fakeTxBeginner struct {
	tx  *fakeTx
	err error
}

func (f *fakeTxBeginner) Begin(_ context.Context) (pgx.Tx, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.tx == nil {
		f.tx = &fakeTx{}
	}
	return f.tx, nil
}

type fakeBookingExpiryRepo struct {
	status          string
	statusErr       error
	statusForUpdate string
	statusFUErr     error
	markExpiredRows int64
	markExpiredErr  error
	adminActions    []string
	adminErr        error
	standaloneAdmin []string
}

func (f *fakeBookingExpiryRepo) GetPaymentStatus(_ context.Context, _ uuid.UUID) (string, error) {
	return f.status, f.statusErr
}
func (f *fakeBookingExpiryRepo) GetPaymentStatusForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (string, error) {
	return f.statusForUpdate, f.statusFUErr
}
func (f *fakeBookingExpiryRepo) MarkBookingExpired(_ context.Context, _ pgx.Tx, _ uuid.UUID) (int64, error) {
	return f.markExpiredRows, f.markExpiredErr
}
func (f *fakeBookingExpiryRepo) InsertAdminAction(_ context.Context, _ pgx.Tx, _ uuid.UUID, action, resourceType string, _ uuid.UUID, reason string) error {
	f.adminActions = append(f.adminActions, action+":"+reason)
	return f.adminErr
}
func (f *fakeBookingExpiryRepo) InsertAdminActionStandalone(_ context.Context, _ uuid.UUID, action, resourceType string, _ uuid.UUID, reason string) error {
	f.standaloneAdmin = append(f.standaloneAdmin, action+":"+reason)
	return f.adminErr
}

type fakeSeatReleaser struct {
	calls int
	err   error
}

func (f *fakeSeatReleaser) ReleaseSeatsForExpiry(_ context.Context, _ uuid.UUID) error {
	f.calls++
	return f.err
}

type fakeRefundGateway struct {
	calls     int
	shouldErr bool
	refundID  string
}

func (f *fakeRefundGateway) Refund(_ context.Context, _, _ string) (string, error) {
	f.calls++
	if f.shouldErr {
		return "", errors.New("gateway down")
	}
	if f.refundID == "" {
		f.refundID = "refund-xyz"
	}
	return f.refundID, nil
}

type fakeObligations struct {
	byBooking []payment.PaymentObligation
	markRfd   []uuid.UUID
	listErr   error
}

func (f *fakeObligations) Create(_ context.Context, _ *payment.PaymentObligation) error { return nil }
func (f *fakeObligations) GetByID(_ context.Context, _ uuid.UUID) (*payment.PaymentObligation, error) {
	return nil, payment.ErrObligationNotFound
}
func (f *fakeObligations) GetByExternalTxID(_ context.Context, _ string) (*payment.PaymentObligation, error) {
	return nil, payment.ErrWebhookNotFound
}
func (f *fakeObligations) GetByIdempotencyKey(_ context.Context, _ string) (*payment.PaymentObligation, error) {
	return nil, nil
}
func (f *fakeObligations) ListByBooking(_ context.Context, _ uuid.UUID) ([]payment.PaymentObligation, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.byBooking, nil
}
func (f *fakeObligations) GetByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*payment.PaymentObligation, error) {
	return nil, payment.ErrObligationNotFound
}
func (f *fakeObligations) GetByExternalTxIDForUpdate(_ context.Context, _ pgx.Tx, _ string) (*payment.PaymentObligation, error) {
	return nil, payment.ErrWebhookNotFound
}
func (f *fakeObligations) UpdateStatusWithWebhook(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	return nil
}
func (f *fakeObligations) CheckUserActiveForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (bool, error) {
	return true, nil
}
func (f *fakeObligations) AssignTicketAtomic(_ context.Context, _ pgx.Tx, _, _, _ uuid.UUID) (int64, error) {
	return 1, nil
}
func (f *fakeObligations) MarkExpiredByBooking(_ context.Context, _ pgx.Tx, _ uuid.UUID) (int64, error) {
	return 1, nil
}
func (f *fakeObligations) MarkRefunded(_ context.Context, _ pgx.Tx, id uuid.UUID, _ string) error {
	f.markRfd = append(f.markRfd, id)
	return nil
}
func (f *fakeObligations) ListPendingOlderThan(_ context.Context, _ time.Time, _ int) ([]payment.PaymentObligation, error) {
	return nil, nil
}

func newExpiryWorker(t *testing.T, repo *fakeBookingExpiryRepo, obs *fakeObligations) (*BookingExpiryWorker, *fakeTxBeginner, *fakeSeatReleaser, *fakeRefundGateway) {
	t.Helper()
	beginner := &fakeTxBeginner{tx: &fakeTx{}}
	rel := &fakeSeatReleaser{}
	rfd := &fakeRefundGateway{}
	w := NewBookingExpiryWorker(nil, beginner, repo, obs, rel, rfd, uuid.New(), zap.NewNop())
	return w, beginner, rel, rfd
}

func mustJSON(bid uuid.UUID) []byte {
	return []byte(`{"type":"RELEASE_SEAT_CHECK","booking_id":"` + bid.String() + `"}`)
}

// ============================================================
// Tests
// ============================================================

func TestProcessOne_PaidNoOp(t *testing.T) {
	bid := uuid.New()
	repo := &fakeBookingExpiryRepo{status: payment.StatusPaid}
	w, tx, rel, _ := newExpiryWorker(t, repo, &fakeObligations{})

	require.NoError(t, w.ProcessOne(context.Background(), mustJSON(bid)))
	assert.False(t, tx.tx.committed, "no tx should begin")
	assert.Equal(t, 0, rel.calls, "seats must not be released")
}

func TestProcessOne_NonPendingNoOp(t *testing.T) {
	bid := uuid.New()
	repo := &fakeBookingExpiryRepo{status: payment.StatusCancelled}
	w, tx, rel, _ := newExpiryWorker(t, repo, &fakeObligations{})

	require.NoError(t, w.ProcessOne(context.Background(), mustJSON(bid)))
	assert.False(t, tx.tx.committed)
	assert.Equal(t, 0, rel.calls)
}

func TestProcessOne_PoisonJSON(t *testing.T) {
	w, _, _, _ := newExpiryWorker(t, &fakeBookingExpiryRepo{}, &fakeObligations{})
	err := w.ProcessOne(context.Background(), []byte("{not json"))
	require.Error(t, err)
	assert.ErrorIs(t, err, errPoison)
}

func TestProcessOne_PoisonEmptyBookingID(t *testing.T) {
	w, _, _, _ := newExpiryWorker(t, &fakeBookingExpiryRepo{}, &fakeObligations{})
	err := w.ProcessOne(context.Background(), []byte(`{"type":"X","booking_id":""}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, errPoison)
}

func TestProcessOne_PoisonBadUUID(t *testing.T) {
	w, _, _, _ := newExpiryWorker(t, &fakeBookingExpiryRepo{}, &fakeObligations{})
	err := w.ProcessOne(context.Background(), []byte(`{"booking_id":"not-a-uuid"}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, errPoison)
}

func TestProcessOne_RollbackSuccess(t *testing.T) {
	bid := uuid.New()
	repo := &fakeBookingExpiryRepo{
		status:          payment.StatusPending,
		statusForUpdate: payment.StatusPending,
		markExpiredRows: 1,
	}
	obs := &fakeObligations{}
	w, tx, rel, _ := newExpiryWorker(t, repo, obs)

	require.NoError(t, w.ProcessOne(context.Background(), mustJSON(bid)))
	assert.True(t, tx.tx.committed)
	assert.Equal(t, 1, rel.calls, "seats released after commit")
	assert.Len(t, repo.adminActions, 1)
}

func TestProcessOne_H1Race_WebhookPaidInsideTx(t *testing.T) {
	// H-1 regression test: state changes to PAID between outer check and
	// inside-tx re-check. Worker must abort without seat release/audit.
	bid := uuid.New()
	repo := &fakeBookingExpiryRepo{
		status:          payment.StatusPending, // outer sees PENDING
		statusForUpdate: payment.StatusPaid,    // inner sees PAID (webhook won)
		markExpiredRows: 1,                     // would succeed if we let it
	}
	obs := &fakeObligations{}
	w, tx, rel, _ := newExpiryWorker(t, repo, obs)

	require.NoError(t, w.ProcessOne(context.Background(), mustJSON(bid)))
	assert.False(t, tx.tx.committed, "must NOT commit on race")
	assert.Equal(t, 0, rel.calls, "must NOT release seats (no oversell)")
	assert.Len(t, repo.adminActions, 0, "must NOT record fake audit")
}

func TestProcessOne_H1Race_MarkExpiredZeroRows(t *testing.T) {
	// H-1 defensive: MarkBookingExpired returns 0 rows → abort, no seat release.
	bid := uuid.New()
	repo := &fakeBookingExpiryRepo{
		status:          payment.StatusPending,
		statusForUpdate: payment.StatusPending,
		markExpiredRows: 0, // race lost at UPDATE
	}
	obs := &fakeObligations{}
	w, tx, rel, _ := newExpiryWorker(t, repo, obs)

	require.NoError(t, w.ProcessOne(context.Background(), mustJSON(bid)))
	assert.False(t, tx.tx.committed, "must NOT commit when zero rows affected")
	assert.Equal(t, 0, rel.calls, "must NOT release seats (no oversell)")
	assert.Len(t, repo.adminActions, 0)
}

func TestProcessOne_RefundSuccess_MarkRefunded(t *testing.T) {
	bid := uuid.New()
	txID := "ext-tx-1"
	obs := &fakeObligations{
		byBooking: []payment.PaymentObligation{
			{
				ID:                    uuid.New(),
				BookingID:             bid,
				Status:                payment.StatusPaid,
				Amount:                "1000.00",
				ExternalTransactionID: &txID,
			},
		},
	}
	repo := &fakeBookingExpiryRepo{
		status:          payment.StatusPending,
		statusForUpdate: payment.StatusPending,
		markExpiredRows: 1,
	}
	w, _, _, rfd := newExpiryWorker(t, repo, obs)

	require.NoError(t, w.ProcessOne(context.Background(), mustJSON(bid)))
	assert.Equal(t, 1, rfd.calls)
	assert.Len(t, obs.markRfd, 1, "MarkRefunded must be called after successful refund")
}

func TestProcessOne_RefundFailed_RecordsAudit(t *testing.T) {
	bid := uuid.New()
	txID := "ext-tx-fail"
	obs := &fakeObligations{
		byBooking: []payment.PaymentObligation{
			{
				ID:                    uuid.New(),
				BookingID:             bid,
				Status:                payment.StatusPaid,
				Amount:                "1000.00",
				ExternalTransactionID: &txID,
			},
		},
	}
	repo := &fakeBookingExpiryRepo{
		status:          payment.StatusPending,
		statusForUpdate: payment.StatusPending,
		markExpiredRows: 1,
	}
	beginner := &fakeTxBeginner{tx: &fakeTx{}}
	rel := &fakeSeatReleaser{}
	rfd := &fakeRefundGateway{shouldErr: true}
	w := NewBookingExpiryWorker(nil, beginner, repo, obs, rel, rfd, uuid.New(), zap.NewNop())

	require.NoError(t, w.ProcessOne(context.Background(), mustJSON(bid)))
	assert.Equal(t, 3, rfd.calls, "3 retry attempts")
	// H-2: audit row via standalone insertion
	found := false
	for _, a := range repo.standaloneAdmin {
		if a == "MANUAL_REFUND:REFUND_FAILED_RETRY_EXHAUSTED" {
			found = true
			break
		}
	}
	assert.True(t, found, "refund failure audit must be recorded")
}
