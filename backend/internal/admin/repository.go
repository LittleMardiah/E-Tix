package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
func (r *PostgresAdminRepository) ListLedgerEntries(ctx context.Context, dateFrom, dateTo, statusFilter string, page, limit int) ([]LedgerEntry, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit
	if dateFrom == "" {
		dateFrom = "1970-01-01"
	}
	if dateTo == "" {
		dateTo = "2100-01-01"
	}

	union := `
		WITH all_entries AS (
			SELECT po.id AS event_id, 'PAYMENT_' || po.status::text AS event_type,
			       'payment_obligation' AS reference_type, po.id AS reference_id,
			       po.booking_id, po.user_id, po.amount::text AS amount,
			       po.status::text AS status, po.external_transaction_id AS gateway_tx_id,
			       NULL::uuid AS admin_id, (po.status::text = 'PAID') AS reconciled,
			       po.created_at
			FROM payment_obligations po
			WHERE po.created_at >= $1::date AND po.created_at < ($2::date + INTERVAL '1 day')
			UNION ALL
			SELECT rt.id, 'RESALE_' || rt.status::text, 'resale_transaction', rt.id,
			       NULL::uuid, rt.buyer_user_id, rt.resale_price::text, rt.status::text,
			       rt.payment_transaction_id, NULL::uuid, (rt.status::text = 'COMPLETED'), rt.created_at
			FROM resale_transactions rt
			WHERE rt.created_at >= $1::date AND rt.created_at < ($2::date + INTERVAL '1 day')
			UNION ALL
			SELECT aa.id, 'ADMIN_' || aa.action_type::text, 'admin_action', aa.id,
			       NULL::uuid, aa.target_user_id, '0'::text, 'PROCESSED'::text,
			       aa.target_resource_id::text, aa.admin_id, TRUE, aa.created_at
			FROM admin_actions aa
			WHERE aa.created_at >= $1::date AND aa.created_at < ($2::date + INTERVAL '1 day')
		)
		SELECT event_id, event_type, reference_type, reference_id,
		       booking_id, user_id, amount, status, gateway_tx_id,
		       admin_id, reconciled, created_at
		FROM all_entries
		WHERE ($3 = '' OR status = $3)
		ORDER BY created_at DESC LIMIT $4 OFFSET $5`

	rows, err := r.pool.Query(ctx, union, dateFrom, dateTo, statusFilter, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.EventID, &e.EventType, &e.ReferenceType, &e.ReferenceID,
			&e.BookingID, &e.UserID, &e.Amount, &e.Status, &e.GatewayTransactionID,
			&e.AdminID, &e.Reconciled, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	countSQL := `
		WITH all_entries AS (
			SELECT status::text AS status, created_at FROM payment_obligations
			WHERE created_at >= $1::date AND created_at < ($2::date + INTERVAL '1 day')
			UNION ALL
			SELECT status::text, created_at FROM resale_transactions
			WHERE created_at >= $1::date AND created_at < ($2::date + INTERVAL '1 day')
			UNION ALL
			SELECT 'PROCESSED'::text, created_at FROM admin_actions
			WHERE created_at >= $1::date AND created_at < ($2::date + INTERVAL '1 day')
		)
		SELECT COUNT(*) FROM all_entries WHERE ($3 = '' OR status = $3)`
	var total int
	if err := r.pool.QueryRow(ctx, countSQL, dateFrom, dateTo, statusFilter).Scan(&total); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}
func (r *PostgresAdminRepository) ComputeLedgerSummary(ctx context.Context, dateFrom, dateTo, _ string) (*LedgerSummary, error) {
	if dateFrom == "" {
		dateFrom = "1970-01-01"
	}
	if dateTo == "" {
		dateTo = "2100-01-01"
	}
	var s LedgerSummary
	err := r.pool.QueryRow(ctx, `
		WITH po AS (
			SELECT status::text AS status, amount::text AS amount, created_at
			FROM payment_obligations
			WHERE created_at >= $1::date AND created_at < ($2::date + INTERVAL '1 day')
		)
		SELECT
			COALESCE(SUM(amount::numeric) FILTER (WHERE status = 'PAID'), 0)::text,
			COALESCE(SUM(amount::numeric) FILTER (WHERE status = 'PENDING'), 0)::text,
			COALESCE(SUM(amount::numeric) FILTER (WHERE status = 'REFUNDED'), 0)::text,
			COUNT(*) FILTER (WHERE status = 'PENDING' AND created_at < NOW() - INTERVAL '10 minutes')
		FROM po`, dateFrom, dateTo).Scan(&s.TotalPaid, &s.TotalPending, &s.TotalRefunded, &s.UnreconciledCount)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
func (r *PostgresAdminRepository) GetPaymentObligationForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*LedgerEntry, error) {
	var (
		poID, bookingID, userID uuid.UUID
		amount                  string
		status                  string
		extTxID                 *string
		createdAt               time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT id, booking_id, user_id, amount::text, status::text,
		       external_transaction_id, created_at
		FROM payment_obligations WHERE id = $1 FOR UPDATE`, id).Scan(
		&poID, &bookingID, &userID, &amount, &status, &extTxID, &createdAt)
	if err == pgx.ErrNoRows {
		return nil, ErrAdminNotFound
	}
	if err != nil {
		return nil, err
	}
	return &LedgerEntry{
		EventID:              poID,
		EventType:            "PAYMENT_" + status,
		ReferenceType:        "payment_obligation",
		ReferenceID:          poID,
		BookingID:            &bookingID,
		UserID:               &userID,
		Amount:               amount,
		Status:               status,
		GatewayTransactionID: extTxID,
		Reconciled:           status == "PAID",
		CreatedAt:            createdAt,
	}, nil
}
func (r *PostgresAdminRepository) UpdatePaymentObligationRefunded(ctx context.Context, tx pgx.Tx, id uuid.UUID, refundTxID string) error {
	// Note: trigger_handle_ticket_cancellation will auto-CANCEL tickets.
	ct, err := tx.Exec(ctx, `
		UPDATE payment_obligations
		SET status = 'REFUNDED', refunded_at = NOW(),
		    refund_transaction_id = $1, updated_at = NOW()
		WHERE id = $2 AND status::text = 'PAID'`,
		refundTxID, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrAdminConflict
	}
	return nil
}
func (r *PostgresAdminRepository) ListStuckLocks(ctx context.Context) ([]StuckLock, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, s.event_id, b.section_id, s.name, s.layout_version, b.seat_positions, b.expires_at
		FROM bookings b JOIN sections s ON s.id = b.section_id
		WHERE b.payment_status::text = 'PENDING'
		  AND b.expires_at < NOW() - INTERVAL '10 minutes'
		ORDER BY b.expires_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var locks []StuckLock
	for rows.Next() {
		var (
			sl        StuckLock
			seatRaw   []byte
			expiresAt time.Time
		)
		if err := rows.Scan(&sl.BookingID, &sl.EventID, &sl.SectionID, &sl.SectionName, &sl.LayoutVersion, &seatRaw, &expiresAt); err != nil {
			return nil, err
		}
		var seats []int
		if err := json.Unmarshal(seatRaw, &seats); err != nil {
			return nil, err
		}
		sl.SeatPositions = seats
		sl.LockedSince = expiresAt.Add(-3 * time.Minute)
		sl.DurationMinutes = int(time.Since(sl.LockedSince).Minutes())
		sl.Status = "STUCK"
		locks = append(locks, sl)
	}
	return locks, rows.Err()
}
func (r *PostgresAdminRepository) GetBookingForUpdate(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID) (*StuckLock, error) {
	var (
		sl        StuckLock
		seatRaw   []byte
		expiresAt time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT b.id, s.event_id, b.section_id, s.name, s.layout_version, b.seat_positions, b.expires_at
		FROM bookings b JOIN sections s ON s.id = b.section_id
		WHERE b.id = $1 FOR UPDATE OF b`, bookingID).Scan(
		&sl.BookingID, &sl.EventID, &sl.SectionID, &sl.SectionName, &sl.LayoutVersion, &seatRaw, &expiresAt)
	if err == pgx.ErrNoRows {
		return nil, ErrAdminNotFound
	}
	if err != nil {
		return nil, err
	}
	var seats []int
	if err := json.Unmarshal(seatRaw, &seats); err != nil {
		return nil, err
	}
	sl.SeatPositions = seats
	sl.LockedSince = expiresAt.Add(-3 * time.Minute)
	sl.DurationMinutes = int(time.Since(sl.LockedSince).Minutes())
	sl.Status = "STUCK"
	return &sl, nil
}
func (r *PostgresAdminRepository) UpdateBookingCancelled(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID) error {
	// Trigger handle_booking_cancellation auto-CANCELLED tickets.
	// Redis release handled in service.go (P8g), NOT here.
	ct, err := tx.Exec(ctx, `
		UPDATE bookings SET payment_status = 'CANCELLED', updated_at = NOW()
		WHERE id = $1 AND payment_status::text = 'PENDING'`, bookingID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrAdminConflict
	}
	return nil
}
func (r *PostgresAdminRepository) CountCancellableTickets(ctx context.Context, eventID uuid.UUID) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM tickets t
		JOIN bookings b ON b.id = t.booking_id
		WHERE b.event_id = $1
		  AND t.status::text IN ('CONFIRMED', 'LOCKED', 'PENDING_SPLIT_PAY', 'RESALE_PENDING')`,
		eventID).Scan(&count)
	return count, err
}
func (r *PostgresAdminRepository) SumRefundableAmount(ctx context.Context, eventID uuid.UUID) (string, error) {
	var total string
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(po.amount), 0)::text
		FROM payment_obligations po
		JOIN bookings b ON b.id = po.booking_id
		WHERE b.event_id = $1 AND po.status::text = 'PAID'`,
		eventID).Scan(&total)
	return total, err
}

var _ AdminRepository = (*PostgresAdminRepository)(nil)
