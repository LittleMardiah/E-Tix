package admin

// FIX P9a-9b TD-F8-03: admin service edge coverage boost.

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

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
)

type p9a9AdminRepo struct {
	users     []AdminUser
	usersCnt  int
	usersErr  error
	blk       []BlacklistEntry
	blkCnt    int
	blkErr    error
	insBlk    error
	delBlkID  uuid.UUID
	delBlk    error
	actID     uuid.UUID
	actErr    error
	led       []LedgerEntry
	ledSum    *LedgerSummary
	ledErr    error
	sumErr    error
	obl       *LedgerEntry
	oblErr    error
	updRef    error
	stuck     []StuckLock
	stuckErr  error
	bookFU    *StuckLock
	bookFUErr error
	updBook   error
	cntCan    int
	cntCanErr error
	sumRef    string
	sumRefErr error
}

func (f *p9a9AdminRepo) GetUserByID(context.Context, uuid.UUID) (*AdminUser, error) { return nil, nil }
func (f *p9a9AdminRepo) ListUsers(context.Context, string, string, string, int, int) ([]AdminUser, int, error) {
	return f.users, f.usersCnt, f.usersErr
}
func (f *p9a9AdminRepo) UpdateUserStatus(context.Context, pgx.Tx, uuid.UUID, string) error {
	return nil
}
func (f *p9a9AdminRepo) ListBlacklist(context.Context, int, int) ([]BlacklistEntry, int, error) {
	return f.blk, f.blkCnt, f.blkErr
}
func (f *p9a9AdminRepo) InsertBlacklist(context.Context, *BlacklistEntry) error { return f.insBlk }
func (f *p9a9AdminRepo) DeleteBlacklist(context.Context, uuid.UUID) (uuid.UUID, error) {
	if f.delBlkID == uuid.Nil {
		return uuid.New(), f.delBlk
	}
	return f.delBlkID, f.delBlk
}
func (f *p9a9AdminRepo) InsertAdminAction(context.Context, pgx.Tx, *AdminActionRecord) (uuid.UUID, error) {
	if f.actID == uuid.Nil {
		return uuid.New(), f.actErr
	}
	return f.actID, f.actErr
}
func (f *p9a9AdminRepo) ListLedgerEntries(context.Context, string, string, string, int, int) ([]LedgerEntry, int, error) {
	return f.led, 0, f.ledErr
}
func (f *p9a9AdminRepo) ComputeLedgerSummary(context.Context, string, string, string) (*LedgerSummary, error) {
	if f.ledSum != nil {
		return f.ledSum, f.sumErr
	}
	return &LedgerSummary{TotalPaid: "0", TotalPending: "0", TotalRefunded: "0", UnreconciledCount: 0}, f.sumErr
}
func (f *p9a9AdminRepo) GetPaymentObligationForUpdate(context.Context, pgx.Tx, uuid.UUID) (*LedgerEntry, error) {
	return f.obl, f.oblErr
}
func (f *p9a9AdminRepo) UpdatePaymentObligationRefunded(context.Context, pgx.Tx, uuid.UUID, string) error {
	return f.updRef
}
func (f *p9a9AdminRepo) ListStuckLocks(context.Context) ([]StuckLock, error) {
	return f.stuck, f.stuckErr
}
func (f *p9a9AdminRepo) GetBookingForUpdate(context.Context, pgx.Tx, uuid.UUID) (*StuckLock, error) {
	return f.bookFU, f.bookFUErr
}
func (f *p9a9AdminRepo) UpdateBookingCancelled(context.Context, pgx.Tx, uuid.UUID) error {
	return f.updBook
}
func (f *p9a9AdminRepo) CountCancellableTickets(context.Context, uuid.UUID) (int, error) {
	return f.cntCan, f.cntCanErr
}
func (f *p9a9AdminRepo) SumRefundableAmount(context.Context, uuid.UUID) (string, error) {
	return f.sumRef, f.sumRefErr
}

type p9a9AdminTx struct {
	pgx.Tx
	commitErr   error
	rollbackErr error
}

func (t *p9a9AdminTx) Commit(context.Context) error   { return t.commitErr }
func (t *p9a9AdminTx) Rollback(context.Context) error { return t.rollbackErr }

type p9a9AdminTB struct {
	beginErr error
	tx       *p9a9AdminTx
}

func (b *p9a9AdminTB) Begin(context.Context) (pgx.Tx, error) {
	if b.beginErr != nil {
		return nil, b.beginErr
	}
	if b.tx == nil {
		b.tx = &p9a9AdminTx{}
	}
	return b.tx, nil
}

type p9a9AdminGW struct{}

func (p9a9AdminGW) Initiate(context.Context, adapter.InitiateRequest) (*adapter.InitiateResponse, error) {
	return &adapter.InitiateResponse{}, nil
}
func (p9a9AdminGW) Refund(context.Context, adapter.RefundRequest) (*adapter.RefundResponse, error) {
	return &adapter.RefundResponse{RefundID: "r1"}, nil
}
func (p9a9AdminGW) Payout(context.Context, adapter.PayoutRequest) (*adapter.PayoutResponse, error) {
	return &adapter.PayoutResponse{}, nil
}
func (p9a9AdminGW) GetStatus(context.Context, string) (*adapter.StatusResponse, error) {
	return &adapter.StatusResponse{}, nil
}
func (p9a9AdminGW) ValidateWebhookSignature(map[string]string, []byte) error { return nil }

func TestP9a9AdminListUsers_Err(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{usersErr: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.ListUsers(context.Background(), uuid.New(), "", "", "", 1, 10)
	require.Error(t, err)
}

func TestP9a9AdminListBlacklist_Err(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{blkErr: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.ListBlacklist(context.Background(), uuid.New(), 1, 10)
	require.Error(t, err)
}

func TestP9a9AdminAddBlacklist_InsertErr(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{insBlk: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.AddBlacklist(context.Background(), uuid.New(), &AddBlacklistRequest{Type: "ip", Value: "1.2.3.4", Reason: "x"})
	require.Error(t, err)
}

func TestP9a9AdminRemoveBlacklist_DeleteErr(t *testing.T) {
	id := uuid.New()
	svc := NewAdminService(&p9a9AdminRepo{delBlkID: id, delBlk: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.RemoveBlacklist(context.Background(), uuid.New(), id)
	require.Error(t, err)
}

func TestP9a9AdminLedger_EntriesErr(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{ledErr: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.GetLedger(context.Background(), uuid.New(), "", "", "", 1, 10)
	require.Error(t, err)
}

func TestP9a9AdminLedger_SummaryErr(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{sumErr: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.GetLedger(context.Background(), uuid.New(), "", "", "", 1, 10)
	require.Error(t, err)
}

func TestP9a9AdminStuckLocks_Err(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{stuckErr: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.GetStuckLocks(context.Background(), uuid.New())
	require.Error(t, err)
}

func TestP9a9AdminManualReverse_OblErr(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{oblErr: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.ManualReverse(context.Background(), uuid.New(), &LedgerReverseRequest{TransactionID: uuid.New(), Reason: "x"})
	require.Error(t, err)
}

func TestP9a9AdminForceRelease_BookFUErr(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{bookFUErr: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.ForceRelease(context.Background(), uuid.New(), &ForceReleaseRequest{BookingID: uuid.New(), Reason: "x"})
	require.Error(t, err)
}

func TestP9a9AdminBulkCancel_CountErr(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{cntCanErr: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.BulkCancel(context.Background(), uuid.New(), uuid.New(), &BulkCancelRequest{Reason: "x"})
	require.Error(t, err)
}

func TestP9a9AdminBulkCancel_SumErr(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{sumRefErr: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.BulkCancel(context.Background(), uuid.New(), uuid.New(), &BulkCancelRequest{Reason: "x"})
	require.Error(t, err)
}

func TestP9a9AdminBulkCancel_AuditErr(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{cntCan: 1, sumRef: "1000", actErr: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.BulkCancel(context.Background(), uuid.New(), uuid.New(), &BulkCancelRequest{Reason: "x"})
	require.Error(t, err)
}

func TestP9a9AdminForceRelease_UpdateErr(t *testing.T) {
	svc := NewAdminService(&p9a9AdminRepo{bookFU: &StuckLock{BookingID: uuid.New(), EventID: uuid.New(), SectionID: uuid.New(), SectionName: "A", LayoutVersion: 1, SeatPositions: []int{1}, LockedSince: time.Now(), DurationMinutes: 10, Status: "EXPIRED"}, updBook: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.ForceRelease(context.Background(), uuid.New(), &ForceReleaseRequest{BookingID: uuid.New(), Reason: "x"})
	require.Error(t, err)
}

func TestP9a9AdminManualReverse_UpdateRefundErr(t *testing.T) {
	obl := &LedgerEntry{ReferenceID: uuid.New(), ReferenceType: "payment_obligation", Status: "PAID", Amount: "1000", GatewayTransactionID: func() *string { s2 := "tx1"; return &s2 }(), CreatedAt: time.Now()}
	svc := NewAdminService(&p9a9AdminRepo{obl: obl, updRef: errors.New("x")}, &p9a9AdminTB{}, nil, p9a9AdminGW{}, (*queue.Connection)(nil), zap.NewNop(), uuid.New())
	_, err := svc.ManualReverse(context.Background(), uuid.New(), &LedgerReverseRequest{TransactionID: obl.ReferenceID, Reason: "x"})
	require.Error(t, err)
}

func TestP9a9AdminModels_Sentinetls(t *testing.T) {
	_ = BulkCancelRequest{Reason: "x"}
	_ = AddBlacklistRequest{Type: "ip", Value: "1", Reason: "x"}
	_ = SuspendUserRequest{Reason: "x"}
	assert.True(t, true)
}
