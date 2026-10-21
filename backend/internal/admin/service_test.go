package admin_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/admin"
	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

// ---------- test doubles ----------

type fakeTx struct {
	pgx.Tx
	committed   bool
	rolledBack  bool
	commitErr   error
	rollbackErr error
}

func (f *fakeTx) Commit(_ context.Context) error {
	f.committed = true
	return f.commitErr
}

func (f *fakeTx) Rollback(_ context.Context) error {
	f.rolledBack = true
	return f.rollbackErr
}

type fakeTxBeginner struct {
	tx        *fakeTx
	beginErr  error
	beginHits int
}

func (f *fakeTxBeginner) Begin(_ context.Context) (pgx.Tx, error) {
	f.beginHits++
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	if f.tx == nil {
		f.tx = &fakeTx{}
	}
	return f.tx, nil
}

type fakeGateway struct {
	refundResp *adapter.RefundResponse
	refundErr  error
	refundCall int
	lastRefund adapter.RefundRequest
}

func (f *fakeGateway) Initiate(_ context.Context, _ adapter.InitiateRequest) (*adapter.InitiateResponse, error) {
	return nil, errors.New("fake gateway: initiate not implemented")
}

func (f *fakeGateway) Refund(_ context.Context, req adapter.RefundRequest) (*adapter.RefundResponse, error) {
	f.refundCall++
	f.lastRefund = req
	if f.refundErr != nil {
		return nil, f.refundErr
	}
	if f.refundResp == nil {
		return &adapter.RefundResponse{RefundID: "refund-default"}, nil
	}
	return f.refundResp, nil
}

func (f *fakeGateway) Payout(_ context.Context, _ adapter.PayoutRequest) (*adapter.PayoutResponse, error) {
	return nil, errors.New("fake gateway: payout not implemented")
}

func (f *fakeGateway) GetStatus(_ context.Context, _ string) (*adapter.StatusResponse, error) {
	return nil, errors.New("fake gateway: get status not implemented")
}

func (f *fakeGateway) ValidateWebhookSignature(_ map[string]string, _ []byte) error {
	return nil
}

// fakeRepo implements admin.AdminRepository (16 methods) fully in memory.
type fakeRepo struct {
	user             *admin.AdminUser
	userErr          error
	users            []admin.AdminUser
	listTotal        int
	listErr          error
	updateStatusErr  error
	updateStatusHits int
	blEntries        []admin.BlacklistEntry
	blTotal          int
	blListErr        error
	insertBlErr      error
	insertBlHits     int
	deleteBlID       uuid.UUID
	deleteBlErr      error
	auditID          uuid.UUID
	auditErr         error
	auditHits        int
	leEntries        []admin.LedgerEntry
	leTotal          int
	leErr            error
	summary          *admin.LedgerSummary
	summaryErr       error
	paidObl          *admin.LedgerEntry
	obErr            error
	refundErr        error
	refundHits       int
	stuck            []admin.StuckLock
	stuckErr         error
	booking          *admin.StuckLock
	bookingErr       error
	cancelErr        error
	cancelHits       int
	countVal         int
	countErr         error
	sumVal           string
	sumErr           error
}

func (f *fakeRepo) GetUserByID(_ context.Context, _ uuid.UUID) (*admin.AdminUser, error) {
	return f.user, f.userErr
}

func (f *fakeRepo) ListUsers(_ context.Context, _, _, _ string, _, _ int) ([]admin.AdminUser, int, error) {
	return f.users, f.listTotal, f.listErr
}

func (f *fakeRepo) UpdateUserStatus(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	f.updateStatusHits++
	return f.updateStatusErr
}

func (f *fakeRepo) ListBlacklist(_ context.Context, _, _ int) ([]admin.BlacklistEntry, int, error) {
	return f.blEntries, f.blTotal, f.blListErr
}

func (f *fakeRepo) InsertBlacklist(_ context.Context, e *admin.BlacklistEntry) error {
	f.insertBlHits++
	if f.insertBlErr == nil {
		e.ID = uuid.New()
		e.AddedAt = time.Now()
	}
	return f.insertBlErr
}

func (f *fakeRepo) DeleteBlacklist(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return f.deleteBlID, f.deleteBlErr
}

func (f *fakeRepo) InsertAdminAction(_ context.Context, _ pgx.Tx, _ *admin.AdminActionRecord) (uuid.UUID, error) {
	f.auditHits++
	if f.auditID == uuid.Nil && f.auditErr == nil {
		f.auditID = uuid.New()
	}
	return f.auditID, f.auditErr
}

func (f *fakeRepo) ListLedgerEntries(_ context.Context, _, _, _ string, _, _ int) ([]admin.LedgerEntry, int, error) {
	return f.leEntries, f.leTotal, f.leErr
}

func (f *fakeRepo) ComputeLedgerSummary(_ context.Context, _, _, _ string) (*admin.LedgerSummary, error) {
	return f.summary, f.summaryErr
}

func (f *fakeRepo) GetPaymentObligationForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*admin.LedgerEntry, error) {
	return f.paidObl, f.obErr
}

func (f *fakeRepo) UpdatePaymentObligationRefunded(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	f.refundHits++
	return f.refundErr
}

func (f *fakeRepo) ListStuckLocks(_ context.Context) ([]admin.StuckLock, error) {
	return f.stuck, f.stuckErr
}

func (f *fakeRepo) GetBookingForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*admin.StuckLock, error) {
	return f.booking, f.bookingErr
}

func (f *fakeRepo) UpdateBookingCancelled(_ context.Context, _ pgx.Tx, _ uuid.UUID) error {
	f.cancelHits++
	return f.cancelErr
}

func (f *fakeRepo) CountCancellableTickets(_ context.Context, _ uuid.UUID) (int, error) {
	return f.countVal, f.countErr
}

func (f *fakeRepo) SumRefundableAmount(_ context.Context, _ uuid.UUID) (string, error) {
	return f.sumVal, f.sumErr
}

// svcFixture wires a fake repo + fake tx + miniredis into a real AdminServiceImpl.
// The MQ queue is intentionally nil (service must stay nil-safe on s.queue).
type svcFixture struct {
	repo *fakeRepo
	gw   *fakeGateway
	db   *fakeTxBeginner
	tx   *fakeTx
	rdb  *goredis.Client
	mr   *miniredis.Miniredis
	log  *zap.Logger
	sys  uuid.UUID
}

func newSvcFixture(t *testing.T) *svcFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	tx := &fakeTx{}
	return &svcFixture{
		repo: &fakeRepo{},
		gw:   &fakeGateway{},
		db:   &fakeTxBeginner{tx: tx},
		tx:   tx,
		rdb:  rdb,
		mr:   mr,
		log:  zap.NewNop(),
		sys:  uuid.MustParse("00000000-0000-0000-0000-000000000000"),
	}
}

func (fx *svcFixture) build() *admin.AdminServiceImpl {
	return admin.NewAdminService(fx.repo, fx.db, fx.rdb, fx.gw, nil, fx.log, fx.sys)
}

// ===== E3 BulkCancel =====

func TestSvc_BulkCancel_Success(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.countVal = 5
	fx.repo.sumVal = "5000000"
	svc := fx.build()
	eventID := uuid.New()

	resp, err := svc.BulkCancel(context.Background(), uuid.New(), eventID, &admin.BulkCancelRequest{Reason: "event cancelled"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 5, resp.CancelledTickets)
	assert.Equal(t, "5000000", resp.RefundTotal)
	assert.Equal(t, "PROCESSING", resp.Status)
	assert.Equal(t, eventID, resp.EventID)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.True(t, fx.tx.committed)
}

func TestSvc_BulkCancel_NilQueueIsSafe(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.countVal = 1
	fx.repo.sumVal = "100"
	svc := fx.build()

	// s.queue is nil in the fixture: Publish must be skipped, not panic.
	resp, err := svc.BulkCancel(context.Background(), uuid.New(), uuid.New(), &admin.BulkCancelRequest{Reason: "nil queue"})
	require.NoError(t, err)
	assert.Equal(t, "PROCESSING", resp.Status)
	assert.True(t, fx.tx.committed)
}

func TestSvc_BulkCancel_CountErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.countErr = errors.New("count failed")
	svc := fx.build()

	_, err := svc.BulkCancel(context.Background(), uuid.New(), uuid.New(), &admin.BulkCancelRequest{Reason: "x"})
	require.Error(t, err)
	assert.Zero(t, fx.repo.auditHits)
	assert.False(t, fx.tx.committed)
}

func TestSvc_BulkCancel_SumErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.sumErr = errors.New("sum failed")
	svc := fx.build()

	_, err := svc.BulkCancel(context.Background(), uuid.New(), uuid.New(), &admin.BulkCancelRequest{Reason: "x"})
	require.Error(t, err)
	assert.Zero(t, fx.repo.auditHits)
	assert.False(t, fx.tx.committed)
}

func TestSvc_BulkCancel_BeginErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.db.beginErr = errors.New("begin failed")
	svc := fx.build()

	_, err := svc.BulkCancel(context.Background(), uuid.New(), uuid.New(), &admin.BulkCancelRequest{Reason: "x"})
	require.Error(t, err)
	assert.Equal(t, 1, fx.db.beginHits)
	assert.Zero(t, fx.repo.auditHits)
}

func TestSvc_BulkCancel_AuditErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.auditErr = errors.New("audit failed")
	svc := fx.build()

	_, err := svc.BulkCancel(context.Background(), uuid.New(), uuid.New(), &admin.BulkCancelRequest{Reason: "x"})
	require.Error(t, err)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.False(t, fx.tx.committed)
	assert.True(t, fx.tx.rolledBack)
}

func TestSvc_BulkCancel_CommitErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.tx.commitErr = errors.New("commit failed")
	svc := fx.build()

	_, err := svc.BulkCancel(context.Background(), uuid.New(), uuid.New(), &admin.BulkCancelRequest{Reason: "x"})
	require.Error(t, err)
	assert.True(t, fx.tx.committed)
}

// ===== E4 GetLedger =====

func TestSvc_GetLedger_Success(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.leEntries = []admin.LedgerEntry{{ReferenceType: "payment_obligation", Amount: "1000", Status: "PAID"}}
	fx.repo.leTotal = 1
	fx.repo.summary = &admin.LedgerSummary{TotalPaid: "1000", TotalPending: "0", TotalRefunded: "0"}
	svc := fx.build()

	resp, err := svc.GetLedger(context.Background(), uuid.New(), "2026-01-01", "2026-12-31", "PAID", 1, 10)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Entries, 1)
	assert.Equal(t, "1000", resp.Summary.TotalPaid)
}

func TestSvc_GetLedger_EntriesErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.leErr = errors.New("entries query failed")
	svc := fx.build()

	_, err := svc.GetLedger(context.Background(), uuid.New(), "", "", "", 1, 10)
	require.Error(t, err)
}

func TestSvc_GetLedger_SummaryErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.summaryErr = errors.New("summary query failed")
	svc := fx.build()

	_, err := svc.GetLedger(context.Background(), uuid.New(), "", "", "", 1, 10)
	require.Error(t, err)
}

// ===== E5 ManualReverse =====

func TestSvc_ManualReverse_Success(t *testing.T) {
	fx := newSvcFixture(t)
	extTx := "ext-123"
	obID := uuid.New()
	fx.repo.paidObl = &admin.LedgerEntry{
		ReferenceID:          obID,
		Status:               "PAID",
		Amount:               "500000",
		GatewayTransactionID: &extTx,
	}
	fx.gw.refundResp = &adapter.RefundResponse{RefundID: "refund-xyz"}
	svc := fx.build()

	resp, err := svc.ManualReverse(context.Background(), uuid.New(), &admin.LedgerReverseRequest{
		TransactionID: obID,
		Reason:        "customer dispute",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "REFUNDED", resp.Status)
	assert.Equal(t, obID, resp.PaymentObligationID)
	assert.Equal(t, "refund-xyz", resp.GatewayRefundTransactionID)
	assert.Equal(t, 1, fx.repo.refundHits)
	assert.Equal(t, 1, fx.gw.refundCall)
	assert.Equal(t, extTx, fx.gw.lastRefund.ExternalTransactionID)
	assert.Equal(t, "500000", fx.gw.lastRefund.Amount)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.True(t, fx.tx.committed)
}

func TestSvc_ManualReverse_BeginErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.db.beginErr = errors.New("begin failed")
	svc := fx.build()

	_, err := svc.ManualReverse(context.Background(), uuid.New(), &admin.LedgerReverseRequest{
		TransactionID: uuid.New(),
		Reason:        "x",
	})
	require.Error(t, err)
	assert.Equal(t, 1, fx.db.beginHits)
}

func TestSvc_ManualReverse_ObligationErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.obErr = errors.New("lock failed")
	svc := fx.build()

	_, err := svc.ManualReverse(context.Background(), uuid.New(), &admin.LedgerReverseRequest{
		TransactionID: uuid.New(),
		Reason:        "x",
	})
	require.Error(t, err)
	assert.Zero(t, fx.gw.refundCall)
	assert.True(t, fx.tx.rolledBack)
}

func TestSvc_ManualReverse_NotPaid(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.paidObl = &admin.LedgerEntry{Status: "PENDING", Amount: "100"}
	svc := fx.build()

	_, err := svc.ManualReverse(context.Background(), uuid.New(), &admin.LedgerReverseRequest{
		TransactionID: uuid.New(),
		Reason:        "x",
	})
	assert.ErrorIs(t, err, admin.ErrAdminConflict)
	assert.Zero(t, fx.gw.refundCall)
}

func TestSvc_ManualReverse_NoExtTxID(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.paidObl = &admin.LedgerEntry{Status: "PAID", Amount: "100", GatewayTransactionID: nil}
	svc := fx.build()

	_, err := svc.ManualReverse(context.Background(), uuid.New(), &admin.LedgerReverseRequest{
		TransactionID: uuid.New(),
		Reason:        "x",
	})
	require.Error(t, err)
	assert.Zero(t, fx.gw.refundCall)
}

func TestSvc_ManualReverse_GatewayFail(t *testing.T) {
	fx := newSvcFixture(t)
	extTx := "ext-x"
	fx.repo.paidObl = &admin.LedgerEntry{Status: "PAID", Amount: "100", GatewayTransactionID: &extTx}
	fx.gw.refundErr = errors.New("gateway down")
	svc := fx.build()

	_, err := svc.ManualReverse(context.Background(), uuid.New(), &admin.LedgerReverseRequest{
		TransactionID: uuid.New(),
		Reason:        "x",
	})
	require.Error(t, err)
	assert.Equal(t, 1, fx.gw.refundCall)
	assert.Zero(t, fx.repo.refundHits)
}

func TestSvc_ManualReverse_RefundUpdateErr(t *testing.T) {
	fx := newSvcFixture(t)
	extTx := "ext-x"
	fx.repo.paidObl = &admin.LedgerEntry{Status: "PAID", Amount: "100", GatewayTransactionID: &extTx}
	fx.repo.refundErr = errors.New("update failed")
	svc := fx.build()

	_, err := svc.ManualReverse(context.Background(), uuid.New(), &admin.LedgerReverseRequest{
		TransactionID: uuid.New(),
		Reason:        "x",
	})
	require.Error(t, err)
	assert.Equal(t, 1, fx.repo.refundHits)
	assert.Zero(t, fx.repo.auditHits)
}

func TestSvc_ManualReverse_AuditErr(t *testing.T) {
	fx := newSvcFixture(t)
	extTx := "ext-x"
	fx.repo.paidObl = &admin.LedgerEntry{Status: "PAID", Amount: "100", GatewayTransactionID: &extTx}
	fx.repo.auditErr = errors.New("audit failed")
	svc := fx.build()

	_, err := svc.ManualReverse(context.Background(), uuid.New(), &admin.LedgerReverseRequest{
		TransactionID: uuid.New(),
		Reason:        "x",
	})
	require.Error(t, err)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.False(t, fx.tx.committed)
}

func TestSvc_ManualReverse_CommitErr(t *testing.T) {
	fx := newSvcFixture(t)
	extTx := "ext-x"
	fx.repo.paidObl = &admin.LedgerEntry{Status: "PAID", Amount: "100", GatewayTransactionID: &extTx}
	fx.tx.commitErr = errors.New("commit failed")
	svc := fx.build()

	_, err := svc.ManualReverse(context.Background(), uuid.New(), &admin.LedgerReverseRequest{
		TransactionID: uuid.New(),
		Reason:        "x",
	})
	require.Error(t, err)
	assert.True(t, fx.tx.committed)
}

// ===== E6 GetStuckLocks =====

func TestSvc_GetStuckLocks_Success(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.stuck = []admin.StuckLock{{BookingID: uuid.New(), Status: "STUCK"}}
	svc := fx.build()

	resp, err := svc.GetStuckLocks(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.StuckLocks, 1)
}

func TestSvc_GetStuckLocks_Empty(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.stuck = []admin.StuckLock{}
	svc := fx.build()

	resp, err := svc.GetStuckLocks(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Len(t, resp.StuckLocks, 0)
}

func TestSvc_GetStuckLocks_Err(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.stuckErr = errors.New("db failed")
	svc := fx.build()

	_, err := svc.GetStuckLocks(context.Background(), uuid.New())
	require.Error(t, err)
}

// ===== E6 ForceRelease =====

func TestSvc_ForceRelease_Success(t *testing.T) {
	fx := newSvcFixture(t)
	bookingID := uuid.New()
	fx.repo.booking = &admin.StuckLock{
		BookingID:     bookingID,
		EventID:       uuid.New(),
		SectionID:     uuid.New(),
		LayoutVersion: 3,
		SeatPositions: []int{1, 2},
		Status:        "STUCK",
	}
	svc := fx.build()

	resp, err := svc.ForceRelease(context.Background(), uuid.New(), &admin.ForceReleaseRequest{
		BookingID: bookingID,
		Reason:    "stuck lock",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "CANCELLED", resp.NewStatus)
	assert.Equal(t, bookingID, resp.BookingID)
	assert.Equal(t, []int{1, 2}, resp.ReleasedSeats)
	assert.Equal(t, 1, fx.repo.cancelHits)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.True(t, fx.tx.committed)

	// Redis bitmap bit for each released seat must be cleared (F8-F02).
	key := fmt.Sprintf("seats:%s:%s:v%d",
		fx.repo.booking.EventID.String(), fx.repo.booking.SectionID.String(), fx.repo.booking.LayoutVersion)
	assert.True(t, fx.mr.Exists(key), "seat bitmap key must exist in redis")
	for _, offset := range []int64{0, 1} {
		bit, err := fx.rdb.GetBit(context.Background(), key, offset).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(0), bit)
	}
}

func TestSvc_ForceRelease_NilRedisIsSafe(t *testing.T) {
	fx := newSvcFixture(t)
	fx.rdb = nil
	fx.repo.booking = &admin.StuckLock{BookingID: uuid.New(), SeatPositions: []int{1, 2}}
	svc := fx.build()

	resp, err := svc.ForceRelease(context.Background(), uuid.New(), &admin.ForceReleaseRequest{
		BookingID: uuid.New(),
		Reason:    "nil redis",
	})
	require.NoError(t, err)
	assert.Equal(t, "CANCELLED", resp.NewStatus)
	assert.Equal(t, 1, fx.repo.cancelHits)
}

func TestSvc_ForceRelease_BeginErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.db.beginErr = errors.New("begin failed")
	svc := fx.build()

	_, err := svc.ForceRelease(context.Background(), uuid.New(), &admin.ForceReleaseRequest{
		BookingID: uuid.New(),
		Reason:    "x",
	})
	require.Error(t, err)
	assert.Equal(t, 1, fx.db.beginHits)
}

func TestSvc_ForceRelease_NotFound(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.bookingErr = admin.ErrAdminNotFound
	svc := fx.build()

	_, err := svc.ForceRelease(context.Background(), uuid.New(), &admin.ForceReleaseRequest{
		BookingID: uuid.New(),
		Reason:    "x",
	})
	assert.ErrorIs(t, err, admin.ErrAdminNotFound)
	assert.Zero(t, fx.repo.cancelHits)
}

func TestSvc_ForceRelease_StateConflict(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.bookingErr = errors.New("booking already paid")
	svc := fx.build()

	_, err := svc.ForceRelease(context.Background(), uuid.New(), &admin.ForceReleaseRequest{
		BookingID: uuid.New(),
		Reason:    "x",
	})
	require.Error(t, err)
	assert.NotErrorIs(t, err, admin.ErrAdminNotFound)
	assert.Zero(t, fx.repo.cancelHits)
}

func TestSvc_ForceRelease_CancelErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.booking = &admin.StuckLock{BookingID: uuid.New(), SeatPositions: []int{1}}
	fx.repo.cancelErr = errors.New("cancel failed")
	svc := fx.build()

	_, err := svc.ForceRelease(context.Background(), uuid.New(), &admin.ForceReleaseRequest{
		BookingID: uuid.New(),
		Reason:    "x",
	})
	require.Error(t, err)
	assert.Equal(t, 1, fx.repo.cancelHits)
	assert.Zero(t, fx.repo.auditHits)
}

func TestSvc_ForceRelease_AuditErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.booking = &admin.StuckLock{BookingID: uuid.New()}
	fx.repo.auditErr = errors.New("audit failed")
	svc := fx.build()

	_, err := svc.ForceRelease(context.Background(), uuid.New(), &admin.ForceReleaseRequest{
		BookingID: uuid.New(),
		Reason:    "x",
	})
	require.Error(t, err)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.False(t, fx.tx.committed)
}

func TestSvc_ForceRelease_CommitErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.booking = &admin.StuckLock{BookingID: uuid.New()}
	fx.tx.commitErr = errors.New("commit failed")
	svc := fx.build()

	_, err := svc.ForceRelease(context.Background(), uuid.New(), &admin.ForceReleaseRequest{
		BookingID: uuid.New(),
		Reason:    "x",
	})
	require.Error(t, err)
	assert.True(t, fx.tx.committed)
}

// ===== E7 ListUsers =====

func TestSvc_ListUsers_Success(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.users = []admin.AdminUser{{UserID: uuid.New(), Email: "u@x", Status: "ACTIVE"}}
	fx.repo.listTotal = 1
	svc := fx.build()

	resp, err := svc.ListUsers(context.Background(), uuid.New(), "", "", "", 1, 10)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Users, 1)
}

func TestSvc_ListUsers_Empty(t *testing.T) {
	fx := newSvcFixture(t)
	svc := fx.build()

	resp, err := svc.ListUsers(context.Background(), uuid.New(), "", "", "", 1, 10)
	require.NoError(t, err)
	assert.Len(t, resp.Users, 0)
}

func TestSvc_ListUsers_Err(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.listErr = errors.New("list failed")
	svc := fx.build()

	_, err := svc.ListUsers(context.Background(), uuid.New(), "", "", "", 1, 10)
	require.Error(t, err)
}

// ===== E8 ListBlacklist =====

func TestSvc_ListBlacklist_Success(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.blEntries = []admin.BlacklistEntry{{ID: uuid.New(), Type: "ip", Value: "1.2.3.4"}}
	fx.repo.blTotal = 1
	svc := fx.build()

	resp, err := svc.ListBlacklist(context.Background(), uuid.New(), 1, 10)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Entries, 1)
}

func TestSvc_ListBlacklist_Empty(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.blEntries = []admin.BlacklistEntry{}
	svc := fx.build()

	resp, err := svc.ListBlacklist(context.Background(), uuid.New(), 1, 10)
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 0)
}

func TestSvc_ListBlacklist_Err(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.blListErr = errors.New("list failed")
	svc := fx.build()

	_, err := svc.ListBlacklist(context.Background(), uuid.New(), 1, 10)
	require.Error(t, err)
}

// ===== E8 AddBlacklist =====

func TestSvc_AddBlacklist_Success(t *testing.T) {
	fx := newSvcFixture(t)
	svc := fx.build()
	expires := time.Now().Add(time.Hour)

	resp, err := svc.AddBlacklist(context.Background(), uuid.New(), &admin.AddBlacklistRequest{
		Type:      "ip",
		Value:     "10.0.0.1",
		Reason:    "bot",
		ExpiresAt: &expires,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEqual(t, uuid.Nil, resp.ID)
	assert.Equal(t, "ip", resp.Type)
	assert.Equal(t, "10.0.0.1", resp.Value)
	assert.Equal(t, 1, fx.repo.insertBlHits)
	assert.Equal(t, 1, fx.repo.auditHits)
}

func TestSvc_AddBlacklist_InsertConflict(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.insertBlErr = admin.ErrAdminConflict
	svc := fx.build()

	_, err := svc.AddBlacklist(context.Background(), uuid.New(), &admin.AddBlacklistRequest{
		Type:   "ip",
		Value:  "10.0.0.1",
		Reason: "bot",
	})
	assert.ErrorIs(t, err, admin.ErrAdminConflict)
	assert.Zero(t, fx.repo.auditHits)
}

func TestSvc_AddBlacklist_AuditNil(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.auditErr = errors.New("audit failed")
	svc := fx.build()

	// Audit write is best-effort: blacklist insert still succeeds.
	resp, err := svc.AddBlacklist(context.Background(), uuid.New(), &admin.AddBlacklistRequest{
		Type:   "ip",
		Value:  "10.0.0.1",
		Reason: "bot",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEqual(t, uuid.Nil, resp.ID)
	assert.Equal(t, 1, fx.repo.auditHits)
}

func TestSvc_AddBlacklist_BeginErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.db.beginErr = errors.New("begin failed")
	svc := fx.build()

	resp, err := svc.AddBlacklist(context.Background(), uuid.New(), &admin.AddBlacklistRequest{
		Type:   "ip",
		Value:  "10.0.0.1",
		Reason: "bot",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, fx.repo.insertBlHits)
	assert.Zero(t, fx.repo.auditHits)
}

// ===== E8 RemoveBlacklist =====

func TestSvc_RemoveBlacklist_Success(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.deleteBlID = uuid.New()
	svc := fx.build()
	id := uuid.New()

	resp, err := svc.RemoveBlacklist(context.Background(), uuid.New(), id)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, id, resp.ID)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.True(t, fx.tx.committed)
}

func TestSvc_RemoveBlacklist_NotFound(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.deleteBlErr = admin.ErrAdminNotFound
	svc := fx.build()

	_, err := svc.RemoveBlacklist(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, admin.ErrAdminNotFound)
	assert.Zero(t, fx.repo.auditHits)
}

func TestSvc_RemoveBlacklist_AuditNil(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.auditErr = errors.New("audit failed")
	svc := fx.build()

	// Audit write is best-effort: delete still succeeds.
	resp, err := svc.RemoveBlacklist(context.Background(), uuid.New(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, fx.repo.auditHits)
}

func TestSvc_RemoveBlacklist_BeginErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.db.beginErr = errors.New("begin failed")
	svc := fx.build()

	resp, err := svc.RemoveBlacklist(context.Background(), uuid.New(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Zero(t, fx.repo.auditHits)
}

// ===== E7 SuspendUser =====

func TestSvc_SuspendUser_Success(t *testing.T) {
	fx := newSvcFixture(t)
	svc := fx.build()
	userID := uuid.New()

	resp, err := svc.SuspendUser(context.Background(), uuid.New(), userID, &admin.SuspendUserRequest{Reason: "spam"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "SUSPENDED", resp.Status)
	assert.Equal(t, userID, resp.UserID)
	assert.Equal(t, "ACTIVE", resp.PreviousStatus)
	assert.Equal(t, 1, fx.repo.updateStatusHits)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.True(t, fx.tx.committed)
}

func TestSvc_SuspendUser_BeginErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.db.beginErr = errors.New("begin failed")
	svc := fx.build()

	_, err := svc.SuspendUser(context.Background(), uuid.New(), uuid.New(), &admin.SuspendUserRequest{Reason: "x"})
	require.Error(t, err)
	assert.Zero(t, fx.repo.updateStatusHits)
}

func TestSvc_SuspendUser_UserNotFound(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.updateStatusErr = admin.ErrAdminNotFound
	svc := fx.build()

	_, err := svc.SuspendUser(context.Background(), uuid.New(), uuid.New(), &admin.SuspendUserRequest{Reason: "x"})
	assert.ErrorIs(t, err, admin.ErrAdminNotFound)
	assert.Equal(t, 1, fx.repo.updateStatusHits)
	assert.Zero(t, fx.repo.auditHits)
}

func TestSvc_SuspendUser_UpdateErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.updateStatusErr = errors.New("update failed")
	svc := fx.build()

	_, err := svc.SuspendUser(context.Background(), uuid.New(), uuid.New(), &admin.SuspendUserRequest{Reason: "x"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, admin.ErrAdminNotFound)
	assert.True(t, fx.tx.rolledBack)
}

func TestSvc_SuspendUser_AuditErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.auditErr = errors.New("audit failed")
	svc := fx.build()

	_, err := svc.SuspendUser(context.Background(), uuid.New(), uuid.New(), &admin.SuspendUserRequest{Reason: "x"})
	require.Error(t, err)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.False(t, fx.tx.committed)
}

func TestSvc_SuspendUser_CommitErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.tx.commitErr = errors.New("commit failed")
	svc := fx.build()

	_, err := svc.SuspendUser(context.Background(), uuid.New(), uuid.New(), &admin.SuspendUserRequest{Reason: "x"})
	require.Error(t, err)
	assert.True(t, fx.tx.committed)
}

// ===== E7 UnsuspendUser =====

func TestSvc_UnsuspendUser_Success(t *testing.T) {
	fx := newSvcFixture(t)
	svc := fx.build()
	userID := uuid.New()

	resp, err := svc.UnsuspendUser(context.Background(), uuid.New(), userID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ACTIVE", resp.Status)
	assert.Equal(t, userID, resp.UserID)
	assert.Equal(t, "SUSPENDED", resp.PreviousStatus)
	assert.Equal(t, 1, fx.repo.updateStatusHits)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.True(t, fx.tx.committed)
}

func TestSvc_UnsuspendUser_BeginErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.db.beginErr = errors.New("begin failed")
	svc := fx.build()

	_, err := svc.UnsuspendUser(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.Zero(t, fx.repo.updateStatusHits)
}

func TestSvc_UnsuspendUser_NotFound(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.updateStatusErr = admin.ErrAdminNotFound
	svc := fx.build()

	_, err := svc.UnsuspendUser(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, admin.ErrAdminNotFound)
	assert.Equal(t, 1, fx.repo.updateStatusHits)
	assert.Zero(t, fx.repo.auditHits)
}

func TestSvc_UnsuspendUser_UpdateErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.updateStatusErr = errors.New("update failed")
	svc := fx.build()

	_, err := svc.UnsuspendUser(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.NotErrorIs(t, err, admin.ErrAdminNotFound)
	assert.True(t, fx.tx.rolledBack)
}

func TestSvc_UnsuspendUser_AuditErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.auditErr = errors.New("audit failed")
	svc := fx.build()

	_, err := svc.UnsuspendUser(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.Equal(t, 1, fx.repo.auditHits)
	assert.False(t, fx.tx.committed)
}

func TestSvc_UnsuspendUser_CommitErr(t *testing.T) {
	fx := newSvcFixture(t)
	fx.tx.commitErr = errors.New("commit failed")
	svc := fx.build()

	_, err := svc.UnsuspendUser(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.True(t, fx.tx.committed)
}
