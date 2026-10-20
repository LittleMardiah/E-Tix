package admin

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FASE 8 P8b — AdminRepository skeleton.
// Method bodies filled by E3-E9. Stubs return not-implemented errors.
// All ctx-first. tx param used for multi-step operations (FOR UPDATE + audit).

// AdminRepository = data access for admin endpoints.
type AdminRepository interface {
	// --- User management (E7: suspend/unsuspend, list) ---
	GetUserByID(ctx context.Context, id uuid.UUID) (*AdminUser, error)
	ListUsers(ctx context.Context, roleFilter, statusFilter, search string, page, limit int) ([]AdminUser, int, error)
	UpdateUserStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, newStatus string) error

	// --- Blacklist CRUD (E8) ---
	ListBlacklist(ctx context.Context, page, limit int) ([]BlacklistEntry, int, error)
	InsertBlacklist(ctx context.Context, entry *BlacklistEntry) error
	DeleteBlacklist(ctx context.Context, id uuid.UUID) (uuid.UUID, error) // returns deleted ID for 404 mapping

	// --- Audit log (E2-E8: every mutation) ---
	InsertAdminAction(ctx context.Context, tx pgx.Tx, action *AdminActionRecord) (uuid.UUID, error)

	// --- Ledger view (E4: event-sourced UNION) ---
	ListLedgerEntries(ctx context.Context, dateFrom, dateTo, statusFilter string, page, limit int) ([]LedgerEntry, int, error)
	ComputeLedgerSummary(ctx context.Context, dateFrom, dateTo, statusFilter string) (*LedgerSummary, error)

	// --- Manual reverse (E5: Payment Gateway refund) ---
	GetPaymentObligationForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*LedgerEntry, error)
	UpdatePaymentObligationRefunded(ctx context.Context, tx pgx.Tx, id uuid.UUID, refundTxID string) error

	// --- Force release + stuck locks (E6) ---
	ListStuckLocks(ctx context.Context) ([]StuckLock, error)
	GetBookingForUpdate(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID) (*StuckLock, error)
	UpdateBookingCancelled(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID) error

	// --- Bulk cancel (E3) ---
	CountCancellableTickets(ctx context.Context, eventID uuid.UUID) (int, error)
	SumRefundableAmount(ctx context.Context, eventID uuid.UUID) (string, error)
}

// PostgresAdminRepository = pgx/v5 implementation (stubs).
type PostgresAdminRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresAdminRepository(pool *pgxpool.Pool) *PostgresAdminRepository {
	return &PostgresAdminRepository{pool: pool}
}

// --- All stub bodies return not-implemented. Fill per E<N>. ---

func (r *PostgresAdminRepository) GetUserByID(_ context.Context, _ uuid.UUID) (*AdminUser, error) {
	return nil, errors.New("admin.GetUserByID: not implemented (E7)")
}
func (r *PostgresAdminRepository) ListUsers(_ context.Context, _, _, _ string, _, _ int) ([]AdminUser, int, error) {
	return nil, 0, errors.New("admin.ListUsers: not implemented (E7)")
}
func (r *PostgresAdminRepository) UpdateUserStatus(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	return errors.New("admin.UpdateUserStatus: not implemented (E7)")
}
func (r *PostgresAdminRepository) ListBlacklist(_ context.Context, _, _ int) ([]BlacklistEntry, int, error) {
	return nil, 0, errors.New("admin.ListBlacklist: not implemented (E8)")
}
func (r *PostgresAdminRepository) InsertBlacklist(_ context.Context, _ *BlacklistEntry) error {
	return errors.New("admin.InsertBlacklist: not implemented (E8)")
}
func (r *PostgresAdminRepository) DeleteBlacklist(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, errors.New("admin.DeleteBlacklist: not implemented (E8)")
}
func (r *PostgresAdminRepository) InsertAdminAction(_ context.Context, _ pgx.Tx, _ *AdminActionRecord) (uuid.UUID, error) {
	return uuid.Nil, errors.New("admin.InsertAdminAction: not implemented (E2)")
}
func (r *PostgresAdminRepository) ListLedgerEntries(_ context.Context, _, _, _ string, _, _ int) ([]LedgerEntry, int, error) {
	return nil, 0, errors.New("admin.ListLedgerEntries: not implemented (E4)")
}
func (r *PostgresAdminRepository) ComputeLedgerSummary(_ context.Context, _, _, _ string) (*LedgerSummary, error) {
	return nil, errors.New("admin.ComputeLedgerSummary: not implemented (E4)")
}
func (r *PostgresAdminRepository) GetPaymentObligationForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*LedgerEntry, error) {
	return nil, errors.New("admin.GetPaymentObligationForUpdate: not implemented (E5)")
}
func (r *PostgresAdminRepository) UpdatePaymentObligationRefunded(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	return errors.New("admin.UpdatePaymentObligationRefunded: not implemented (E5)")
}
func (r *PostgresAdminRepository) ListStuckLocks(_ context.Context) ([]StuckLock, error) {
	return nil, errors.New("admin.ListStuckLocks: not implemented (E6)")
}
func (r *PostgresAdminRepository) GetBookingForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*StuckLock, error) {
	return nil, errors.New("admin.GetBookingForUpdate: not implemented (E6)")
}
func (r *PostgresAdminRepository) UpdateBookingCancelled(_ context.Context, _ pgx.Tx, _ uuid.UUID) error {
	return errors.New("admin.UpdateBookingCancelled: not implemented (E6)")
}
func (r *PostgresAdminRepository) CountCancellableTickets(_ context.Context, _ uuid.UUID) (int, error) {
	return 0, errors.New("admin.CountCancellableTickets: not implemented (E3)")
}
func (r *PostgresAdminRepository) SumRefundableAmount(_ context.Context, _ uuid.UUID) (string, error) {
	return "", errors.New("admin.SumRefundableAmount: not implemented (E3)")
}

var _ AdminRepository = (*PostgresAdminRepository)(nil)
