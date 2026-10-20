package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func (r *PostgresAdminRepository) GetUserByID(ctx context.Context, id uuid.UUID) (*AdminUser, error) {
	var u AdminUser
	var baseScore, totalPurchases int
	err := r.pool.QueryRow(ctx, `
		SELECT id, email, phone, name, role::text, status::text,
		       base_score, total_purchases, created_at
		FROM users WHERE id = $1 AND deleted_at IS NULL`, id).Scan(
		&u.UserID, &u.Email, &u.Phone, &u.Name, &u.Role, &u.Status,
		&baseScore, &totalPurchases, &u.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrAdminNotFound
	}
	if err != nil {
		return nil, err
	}
	u.TotalPurchases = totalPurchases
	u.LoyaltyTier = computeLoyaltyTier(baseScore, totalPurchases)
	return &u, nil
}

// computeLoyaltyTier — mirrors DATABASE_SCHEMA Query 4 formula.
func computeLoyaltyTier(baseScore, totalPurchases int) string {
	score := baseScore + totalPurchases*10
	switch {
	case score >= 90:
		return "TIER_1"
	case score >= 70:
		return "TIER_2"
	default:
		return "TIER_3"
	}
}
func (r *PostgresAdminRepository) ListUsers(ctx context.Context, roleFilter, statusFilter, search string, page, limit int) ([]AdminUser, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Build WHERE clause dynamically (parameterized)
	where := "WHERE deleted_at IS NULL"
	args := []interface{}{}
	argIdx := 1
	if roleFilter != "" {
		where += fmt.Sprintf(" AND role::text = $%d", argIdx)
		args = append(args, roleFilter)
		argIdx++
	}
	if statusFilter != "" {
		where += fmt.Sprintf(" AND status::text = $%d", argIdx)
		args = append(args, statusFilter)
		argIdx++
	}
	if search != "" {
		where += fmt.Sprintf(" AND (email ILIKE $%d OR name ILIKE $%d OR phone ILIKE $%d)", argIdx, argIdx, argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}

	// Count total
	var total int
	countSQL := "SELECT COUNT(*) FROM users " + where
	if err := r.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Fetch rows
	rowsSQL := fmt.Sprintf(`
		SELECT id, email, phone, name, role::text, status::text,
		       base_score, total_purchases, created_at
		FROM users %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := r.pool.Query(ctx, rowsSQL, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []AdminUser
	for rows.Next() {
		var u AdminUser
		var baseScore, totalPurchases int
		if err := rows.Scan(&u.UserID, &u.Email, &u.Phone, &u.Name, &u.Role, &u.Status,
			&baseScore, &totalPurchases, &u.CreatedAt); err != nil {
			return nil, 0, err
		}
		u.TotalPurchases = totalPurchases
		u.LoyaltyTier = computeLoyaltyTier(baseScore, totalPurchases)
		users = append(users, u)
	}
	return users, total, rows.Err()
}
func (r *PostgresAdminRepository) UpdateUserStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, newStatus string) error {
	ct, err := tx.Exec(ctx,
		`UPDATE users SET status = $1::user_status_enum, updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL`,
		newStatus, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrAdminNotFound
	}
	return nil
}
func (r *PostgresAdminRepository) ListBlacklist(ctx context.Context, page, limit int) ([]BlacklistEntry, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM blacklist`).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, type, value, reason, added_by, added_at, expires_at
		FROM blacklist ORDER BY added_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []BlacklistEntry
	for rows.Next() {
		var e BlacklistEntry
		if err := rows.Scan(&e.ID, &e.Type, &e.Value, &e.Reason, &e.AddedBy, &e.AddedAt, &e.ExpiresAt); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}
func (r *PostgresAdminRepository) InsertBlacklist(ctx context.Context, e *BlacklistEntry) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO blacklist (type, value, reason, added_by, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, added_at`,
		e.Type, e.Value, e.Reason, e.AddedBy, e.ExpiresAt).Scan(&e.ID, &e.AddedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrAdminConflict
		}
		return err
	}
	return nil
}
func (r *PostgresAdminRepository) DeleteBlacklist(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var deleted uuid.UUID
	err := r.pool.QueryRow(ctx, `DELETE FROM blacklist WHERE id = $1 RETURNING id`, id).Scan(&deleted)
	if err == pgx.ErrNoRows {
		return uuid.Nil, ErrAdminNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	return deleted, nil
}
func (r *PostgresAdminRepository) InsertAdminAction(ctx context.Context, tx pgx.Tx, action *AdminActionRecord) (uuid.UUID, error) {
	var changesBefore, changesAfter interface{}
	if action.ChangesBefore != nil {
		b, err := json.Marshal(action.ChangesBefore)
		if err != nil {
			return uuid.Nil, fmt.Errorf("admin: marshal changes_before: %w", err)
		}
		changesBefore = b
	}
	if action.ChangesAfter != nil {
		b, err := json.Marshal(action.ChangesAfter)
		if err != nil {
			return uuid.Nil, fmt.Errorf("admin: marshal changes_after: %w", err)
		}
		changesAfter = b
	}

	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO admin_actions (admin_id, action_type, target_user_id,
			target_resource_type, target_resource_id, description, reason,
			changes_before, changes_after, ip_address, user_agent)
		VALUES ($1, $2::admin_action_type_enum, $3, $4, $5, $6, $7, $8, $9, $10::inet, $11)
		RETURNING id`,
		action.AdminID, action.ActionType, action.TargetUserID,
		action.TargetResourceType, action.TargetResourceID,
		action.Description, action.Reason,
		changesBefore, changesAfter, action.IPAddress, action.UserAgent).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
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
