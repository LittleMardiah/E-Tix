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
	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

// ============================================================
// Fakes
// ============================================================

type fakeReconRepo struct {
	reconcileRows int64
	reconcileErr  error
	adminActions  []string
	adminErr      error
}

func (f *fakeReconRepo) ReconcileUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string, _ string) (int64, error) {
	return f.reconcileRows, f.reconcileErr
}
func (f *fakeReconRepo) InsertAdminAction(_ context.Context, _ pgx.Tx, _ uuid.UUID, action, resourceType string, _ uuid.UUID, reason string) error {
	f.adminActions = append(f.adminActions, action+":"+reason)
	return f.adminErr
}

type fakeStatusFetcher struct {
	responses map[string]*adapter.StatusResponse
	errors    map[string]error
	calls     int
}

func (f *fakeStatusFetcher) GetStatus(_ context.Context, txID string) (*adapter.StatusResponse, error) {
	f.calls++
	if err, ok := f.errors[txID]; ok {
		return nil, err
	}
	if resp, ok := f.responses[txID]; ok {
		return resp, nil
	}
	return &adapter.StatusResponse{TransactionStatus: "pending"}, nil
}

type fakePaymentRepo struct {
	pending []payment.PaymentObligation
	listErr error
}

func (f *fakePaymentRepo) UpdateGatewayInfo(ctx context.Context, id uuid.UUID, txID, link string) error { return nil }

func (f *fakePaymentRepo) Create(_ context.Context, _ *payment.PaymentObligation) error { return nil }
func (f *fakePaymentRepo) GetByID(_ context.Context, _ uuid.UUID) (*payment.PaymentObligation, error) {
	return nil, payment.ErrObligationNotFound
}
func (f *fakePaymentRepo) GetByExternalTxID(_ context.Context, _ string) (*payment.PaymentObligation, error) {
	return nil, payment.ErrWebhookNotFound
}
func (f *fakePaymentRepo) GetByIdempotencyKey(_ context.Context, _ string) (*payment.PaymentObligation, error) {
	return nil, nil
}
func (f *fakePaymentRepo) ListByBooking(_ context.Context, _ uuid.UUID) ([]payment.PaymentObligation, error) {
	return nil, nil
}
func (f *fakePaymentRepo) GetByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*payment.PaymentObligation, error) {
	return nil, payment.ErrObligationNotFound
}
func (f *fakePaymentRepo) GetByExternalTxIDForUpdate(_ context.Context, _ pgx.Tx, _ string) (*payment.PaymentObligation, error) {
	return nil, payment.ErrWebhookNotFound
}
func (f *fakePaymentRepo) UpdateStatusWithWebhook(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	return nil
}
func (f *fakePaymentRepo) CheckUserActiveForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (bool, error) {
	return true, nil
}
func (f *fakePaymentRepo) AssignTicketAtomic(_ context.Context, _ pgx.Tx, _, _, _ uuid.UUID) (int64, error) {
	return 1, nil
}
func (f *fakePaymentRepo) MarkExpiredByBooking(_ context.Context, _ pgx.Tx, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakePaymentRepo) MarkRefunded(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	return nil
}
func (f *fakePaymentRepo) ListPendingOlderThan(_ context.Context, _ time.Time, _ int) ([]payment.PaymentObligation, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.pending, nil
}

func newReconWorker(t *testing.T, pending []payment.PaymentObligation, responses map[string]*adapter.StatusResponse) (*WebhookReconciliationWorker, *fakeReconRepo, *fakeStatusFetcher, *fakeTxBeginner) {
	t.Helper()
	if responses == nil {
		responses = map[string]*adapter.StatusResponse{}
	}
	beginner := &fakeTxBeginner{tx: &fakeTx{}}
	payRepo := &fakePaymentRepo{pending: pending}
	recon := &fakeReconRepo{reconcileRows: 1}
	fetcher := &fakeStatusFetcher{responses: responses}
	w := NewWebhookReconciliationWorker(beginner, payRepo, recon, fetcher, uuid.New(), zap.NewNop())
	w.interval = 10 * time.Millisecond // faster for tests
	return w, recon, fetcher, beginner
}

func pendingObligation(txID string) payment.PaymentObligation {
	id := txID
	return payment.PaymentObligation{
		ID:                    uuid.New(),
		Status:                payment.StatusPending,
		ExternalTransactionID: &id,
		Amount:                "1000.00",
	}
}

// ============================================================
// Tests
// ============================================================

func TestRunOnce_EmptyBatch(t *testing.T) {
	w, recon, fetcher, _ := newReconWorker(t, nil, nil)
	require.NoError(t, w.RunOnce(context.Background()))
	assert.Equal(t, 0, fetcher.calls, "no fetcher calls when batch empty")
	assert.Len(t, recon.adminActions, 0)
}

func TestRunOnce_SettlementToPaid(t *testing.T) {
	obs := []payment.PaymentObligation{pendingObligation("tx-1")}
	responses := map[string]*adapter.StatusResponse{
		"tx-1": {TransactionStatus: "settlement", FraudStatus: "accept", Amount: "1000.00"},
	}
	w, recon, fetcher, _ := newReconWorker(t, obs, responses)

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Equal(t, 1, fetcher.calls)
	assert.Len(t, recon.adminActions, 1)
	assert.Contains(t, recon.adminActions[0], "MANUAL_PAID")
}

func TestRunOnce_CaptureToPaid(t *testing.T) {
	obs := []payment.PaymentObligation{pendingObligation("tx-2")}
	responses := map[string]*adapter.StatusResponse{
		"tx-2": {TransactionStatus: "capture", FraudStatus: "accept", Amount: "1000.00"},
	}
	w, recon, _, _ := newReconWorker(t, obs, responses)

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Len(t, recon.adminActions, 1)
}

func TestRunOnce_DenyToFailed(t *testing.T) {
	obs := []payment.PaymentObligation{pendingObligation("tx-3")}
	responses := map[string]*adapter.StatusResponse{
		"tx-3": {TransactionStatus: "deny"},
	}
	w, recon, _, _ := newReconWorker(t, obs, responses)

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Len(t, recon.adminActions, 1)
}

func TestRunOnce_PendingStatusSkips(t *testing.T) {
	obs := []payment.PaymentObligation{pendingObligation("tx-4")}
	responses := map[string]*adapter.StatusResponse{
		"tx-4": {TransactionStatus: "pending"},
	}
	w, recon, fetcher, _ := newReconWorker(t, obs, responses)

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Equal(t, 1, fetcher.calls, "fetcher still called")
	assert.Len(t, recon.adminActions, 0, "no audit for transitional status")
}

func TestRunOnce_M2RowsAffectedZero_NoAudit(t *testing.T) {
	// M-2 regression: webhook won the race → UPDATE affects 0 rows →
	// admin_action must NOT be inserted (prevents fake audit trail).
	obs := []payment.PaymentObligation{pendingObligation("tx-5")}
	responses := map[string]*adapter.StatusResponse{
		"tx-5": {TransactionStatus: "settlement"},
	}
	w, recon, fetcher, _ := newReconWorker(t, obs, responses)
	recon.reconcileRows = 0 // simulate race

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Equal(t, 1, fetcher.calls)
	assert.Len(t, recon.adminActions, 0, "no audit when RowsAffected==0")
}

func TestRunOnce_NilStatusResponse(t *testing.T) {
	// L-3: interface contract allows nil, must not panic.
	obs := []payment.PaymentObligation{pendingObligation("tx-6")}
	responses := map[string]*adapter.StatusResponse{}
	w, recon, fetcher, _ := newReconWorker(t, obs, responses)
	// Override fetcher to return nil, nil
	fetcher.responses = map[string]*adapter.StatusResponse{}
	w.fetcher = &nilReturningFetcher{}

	require.NoError(t, w.RunOnce(context.Background()), "must not error the whole batch")
	assert.Len(t, recon.adminActions, 0)
}

// nilReturningFetcher simulates a contract-violating adapter.
type nilReturningFetcher struct{}

func (n *nilReturningFetcher) GetStatus(_ context.Context, _ string) (*adapter.StatusResponse, error) {
	return nil, nil
}

func TestRunOnce_PerObligationFailureContinues(t *testing.T) {
	obs := []payment.PaymentObligation{
		pendingObligation("tx-fail"),
		pendingObligation("tx-ok"),
	}
	responses := map[string]*adapter.StatusResponse{
		"tx-ok": {TransactionStatus: "settlement"},
	}
	beginner := &fakeTxBeginner{tx: &fakeTx{}}
	payRepo := &fakePaymentRepo{pending: obs}
	recon := &fakeReconRepo{reconcileRows: 1}
	fetcher := &fakeStatusFetcher{
		responses: responses,
		errors:    map[string]error{"tx-fail": errors.New("network timeout")},
	}
	w := NewWebhookReconciliationWorker(beginner, payRepo, recon, fetcher, uuid.New(), zap.NewNop())

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Equal(t, 2, fetcher.calls, "both obligations attempted")
	assert.Len(t, recon.adminActions, 1, "only the successful one recorded")
}

func TestRunOnce_ListError(t *testing.T) {
	beginner := &fakeTxBeginner{tx: &fakeTx{}}
	payRepo := &fakePaymentRepo{listErr: errors.New("db down")}
	recon := &fakeReconRepo{}
	fetcher := &fakeStatusFetcher{}
	w := NewWebhookReconciliationWorker(beginner, payRepo, recon, fetcher, uuid.New(), zap.NewNop())

	err := w.RunOnce(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list pending")
}

func TestRunOnce_ObligationMissingExternalTxID(t *testing.T) {
	obs := []payment.PaymentObligation{
		{
			ID:     uuid.New(),
			Status: payment.StatusPending,
			Amount: "1000.00",
			// ExternalTransactionID nil
		},
	}
	w, recon, fetcher, _ := newReconWorker(t, obs, nil)

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Equal(t, 0, fetcher.calls, "skip obligation without tx id")
	assert.Len(t, recon.adminActions, 0)
}
