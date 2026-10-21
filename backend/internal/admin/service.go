package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
)

type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type AdminServiceImpl struct {
	repo    AdminRepository
	db      TxBeginner
	rdb     *redis.Client
	gw      adapter.PaymentGateway
	queue   *queue.Connection
	log     *zap.Logger
	sysUUID uuid.UUID
}

func NewAdminService(
	repo AdminRepository,
	db TxBeginner,
	rdb *redis.Client,
	gw adapter.PaymentGateway,
	q *queue.Connection,
	log *zap.Logger,
	sysUUID uuid.UUID,
) *AdminServiceImpl {
	return &AdminServiceImpl{repo: repo, db: db, rdb: rdb, gw: gw, queue: q, log: log, sysUUID: sysUUID}
}

var _ AdminService = (*AdminServiceImpl)(nil)

// ---- E4: GetLedger ----
func (s *AdminServiceImpl) GetLedger(ctx context.Context, _ uuid.UUID, dateFrom, dateTo, status string, page, limit int) (*LedgerResponse, error) {
	entries, total, err := s.repo.ListLedgerEntries(ctx, dateFrom, dateTo, status, page, limit)
	if err != nil {
		return nil, err
	}
	summary, err := s.repo.ComputeLedgerSummary(ctx, dateFrom, dateTo, status)
	if err != nil {
		return nil, err
	}
	_ = total
	return &LedgerResponse{Entries: entries, Summary: *summary}, nil
}

// ---- E6: GetStuckLocks ----
func (s *AdminServiceImpl) GetStuckLocks(ctx context.Context, _ uuid.UUID) (*StuckLocksResponse, error) {
	locks, err := s.repo.ListStuckLocks(ctx)
	if err != nil {
		return nil, err
	}
	return &StuckLocksResponse{StuckLocks: locks}, nil
}

// ---- E7: ListUsers ----
func (s *AdminServiceImpl) ListUsers(ctx context.Context, _ uuid.UUID, role, status, search string, page, limit int) (*UserListResponse, error) {
	users, _, err := s.repo.ListUsers(ctx, role, status, search, page, limit)
	if err != nil {
		return nil, err
	}
	return &UserListResponse{Users: users}, nil
}

// ---- E8: ListBlacklist ----
func (s *AdminServiceImpl) ListBlacklist(ctx context.Context, _ uuid.UUID, page, limit int) (*BlacklistListResponse, error) {
	entries, _, err := s.repo.ListBlacklist(ctx, page, limit)
	if err != nil {
		return nil, err
	}
	return &BlacklistListResponse{Entries: entries}, nil
}

// ---- E3: BulkCancel (async — returns 202 immediately) ----
func (s *AdminServiceImpl) BulkCancel(ctx context.Context, adminID, eventID uuid.UUID, req *BulkCancelRequest) (*BulkCancelResponse, error) {
	count, err := s.repo.CountCancellableTickets(ctx, eventID)
	if err != nil {
		return nil, err
	}
	refundTotal, err := s.repo.SumRefundableAmount(ctx, eventID)
	if err != nil {
		return nil, err
	}

	// Audit log
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	action := &AdminActionRecord{
		AdminID: adminID, ActionType: "BULK_CANCEL",
		TargetResourceType: strPtr("event"), TargetResourceID: &eventID,
		Reason:        req.Reason,
		ChangesBefore: map[string]interface{}{"event_id": eventID.String()},
	}
	if _, err := s.repo.InsertAdminAction(ctx, tx, action); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// Enqueue async worker
	if s.queue != nil {
		payload := []byte(fmt.Sprintf(`{"event_id":"%s","admin_id":"%s","reason":%q}`,
			eventID.String(), adminID.String(), req.Reason))
		_ = s.queue.PublishToQueue(ctx, "admin_bulk_cancel", payload)
	}

	return &BulkCancelResponse{
		EventID: eventID, CancelledTickets: count, RefundTotal: refundTotal, Status: "PROCESSING",
	}, nil
}

// ---- E5: ManualReverse ----
func (s *AdminServiceImpl) ManualReverse(ctx context.Context, adminID uuid.UUID, req *LedgerReverseRequest) (*LedgerReverseResponse, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ob, err := s.repo.GetPaymentObligationForUpdate(ctx, tx, req.TransactionID)
	if err != nil {
		return nil, err
	}
	if ob.Status != "PAID" {
		return nil, ErrAdminConflict
	}

	// Gateway refund (via external_transaction_id)
	if ob.GatewayTransactionID == nil {
		return nil, errors.New("admin: missing gateway transaction id")
	}
	refundResp, err := s.gw.Refund(ctx, adapter.RefundRequest{
		ExternalTransactionID: *ob.GatewayTransactionID,
		Amount:                ob.Amount,
		Reason:                req.Reason,
	})
	if err != nil {
		return nil, err
	}

	if err := s.repo.UpdatePaymentObligationRefunded(ctx, tx, req.TransactionID, refundResp.RefundID); err != nil {
		return nil, err
	}

	actionID, err := s.repo.InsertAdminAction(ctx, tx, &AdminActionRecord{
		AdminID: adminID, ActionType: "MANUAL_REFUND",
		TargetResourceType: strPtr("payment_obligation"),
		TargetResourceID:   &req.TransactionID,
		Reason:             req.Reason,
		ChangesBefore:      map[string]interface{}{"status": "PAID"},
		ChangesAfter:       map[string]interface{}{"status": "REFUNDED"},
	})
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &LedgerReverseResponse{
		PaymentObligationID: req.TransactionID, Status: "REFUNDED",
		RefundReason: req.Reason, GatewayRefundTransactionID: refundResp.RefundID,
		AdminActionID: actionID, RefundedAt: time.Now(),
	}, nil
}

// ---- E6: ForceRelease ----
func (s *AdminServiceImpl) ForceRelease(ctx context.Context, adminID uuid.UUID, req *ForceReleaseRequest) (*ForceReleaseResponse, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	booking, err := s.repo.GetBookingForUpdate(ctx, tx, req.BookingID)
	if err != nil {
		return nil, err
	}

	// F8-F02 fix: granular bitmask release (0-based offset per LOGIC_FLOW §9.3).
	// Key shape matches FASE 3 lock: seats:{event_id}:{section_id}:v{layout_version}.
	if s.rdb != nil && len(booking.SeatPositions) > 0 {
		key := fmt.Sprintf("seats:%s:%s:v%d",
			booking.EventID.String(), booking.SectionID.String(), booking.LayoutVersion)
		for _, pos := range booking.SeatPositions {
			_ = s.rdb.SetBit(ctx, key, int64(pos-1), 0).Err()
		}
	}

	if err := s.repo.UpdateBookingCancelled(ctx, tx, req.BookingID); err != nil {
		return nil, err
	}
	if _, err := s.repo.InsertAdminAction(ctx, tx, &AdminActionRecord{
		AdminID: adminID, ActionType: "FORCE_RELEASE_SEAT",
		TargetResourceType: strPtr("booking"), TargetResourceID: &req.BookingID,
		Reason: req.Reason,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ForceReleaseResponse{
		BookingID: req.BookingID, ReleasedSeats: booking.SeatPositions,
		NewStatus: "CANCELLED", ReleasedAt: time.Now(),
	}, nil
}

// ---- E7: SuspendUser ----
func (s *AdminServiceImpl) SuspendUser(ctx context.Context, adminID, userID uuid.UUID, req *SuspendUserRequest) (*SuspendUserResponse, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.repo.UpdateUserStatus(ctx, tx, userID, "SUSPENDED"); err != nil {
		return nil, err
	}
	if _, err := s.repo.InsertAdminAction(ctx, tx, &AdminActionRecord{
		AdminID: adminID, ActionType: "SUSPEND_USER",
		TargetUserID: &userID, Reason: req.Reason,
		ChangesBefore: map[string]interface{}{"status": "ACTIVE"},
		ChangesAfter:  map[string]interface{}{"status": "SUSPENDED"},
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &SuspendUserResponse{
		UserID: userID, Status: "SUSPENDED", PreviousStatus: "ACTIVE",
		SuspendedReason: req.Reason, UpdatedAt: time.Now(),
	}, nil
}

// ---- E7: UnsuspendUser ----
func (s *AdminServiceImpl) UnsuspendUser(ctx context.Context, adminID, userID uuid.UUID) (*UnsuspendUserResponse, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.repo.UpdateUserStatus(ctx, tx, userID, "ACTIVE"); err != nil {
		return nil, err
	}
	if _, err := s.repo.InsertAdminAction(ctx, tx, &AdminActionRecord{
		AdminID: adminID, ActionType: "UNSUSPEND_USER",
		TargetUserID: &userID, Reason: "manual unsuspend",
		ChangesBefore: map[string]interface{}{"status": "SUSPENDED"},
		ChangesAfter:  map[string]interface{}{"status": "ACTIVE"},
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &UnsuspendUserResponse{
		UserID: userID, Status: "ACTIVE", PreviousStatus: "SUSPENDED", UpdatedAt: time.Now(),
	}, nil
}

// ---- E8: AddBlacklist ----
func (s *AdminServiceImpl) AddBlacklist(ctx context.Context, adminID uuid.UUID, req *AddBlacklistRequest) (*AddBlacklistResponse, error) {
	entry := &BlacklistEntry{
		Type: req.Type, Value: req.Value,
		Reason: &req.Reason, AddedBy: &adminID, ExpiresAt: req.ExpiresAt,
	}
	if err := s.repo.InsertBlacklist(ctx, entry); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err == nil {
		_, _ = s.repo.InsertAdminAction(ctx, tx, &AdminActionRecord{
			AdminID: adminID, ActionType: "BLACKLIST_IP",
			Reason:       req.Reason,
			ChangesAfter: map[string]interface{}{"type": req.Type, "value": req.Value},
		})
		_ = tx.Commit(ctx)
	}
	return &AddBlacklistResponse{ID: entry.ID, Type: entry.Type, Value: entry.Value, AddedAt: entry.AddedAt}, nil
}

// ---- E8: RemoveBlacklist ----
func (s *AdminServiceImpl) RemoveBlacklist(ctx context.Context, adminID uuid.UUID, id uuid.UUID) (*RemoveBlacklistResponse, error) {
	if _, err := s.repo.DeleteBlacklist(ctx, id); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err == nil {
		_, _ = s.repo.InsertAdminAction(ctx, tx, &AdminActionRecord{
			AdminID: adminID, ActionType: "REMOVE_BLACKLIST_IP",
			TargetResourceType: strPtr("blacklist"), TargetResourceID: &id,
			Reason: "manual remove",
		})
		_ = tx.Commit(ctx)
	}
	return &RemoveBlacklistResponse{ID: id, RemovedAt: time.Now()}, nil
}

// helpers
func strPtr(s string) *string { return &s }
