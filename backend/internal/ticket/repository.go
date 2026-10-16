package ticket

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrTicketNotFound is returned when a ticket does not exist.
var ErrTicketNotFound = errors.New("ticket not found")

// TicketRepository defines data access for tickets.
type TicketRepository interface {
	ListByOwner(ctx context.Context, ownerID uuid.UUID, statusFilter string, page, limit int) ([]TicketListItem, int, error)
	GetByID(ctx context.Context, ticketID uuid.UUID) (*TicketDetail, error)
	GetByIDForUpdate(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) (*Ticket, error)
	UpdateAssign(ctx context.Context, tx pgx.Tx, ticketID, targetUserID uuid.UUID) (int64, error)
	GetUserStatusAndBlacklist(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (status string, blacklisted bool, err error)
	MarkFallbackPINUsed(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) (int64, error)
}

// PostgresTicketRepository is the pgx-backed implementation.
type PostgresTicketRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresTicketRepository constructs a PostgresTicketRepository.
func NewPostgresTicketRepository(pool *pgxpool.Pool) *PostgresTicketRepository {
	return &PostgresTicketRepository{pool: pool}
}

const ticketColumns = `id, booking_id, payment_obligation_id, owner_user_id, section_id,
	seat_position, status::text, is_assigned, qr_secret_key, last_qr_generated_at,
	fallback_pin, fallback_pin_used, fallback_pin_used_at, original_owner_user_id,
	resale_transaction_id, created_at, updated_at`

const ticketColumnsQualified = `t.id, t.booking_id, t.payment_obligation_id, t.owner_user_id,
	t.section_id, t.seat_position, t.status::text, t.is_assigned, t.qr_secret_key,
	t.last_qr_generated_at, t.fallback_pin, t.fallback_pin_used, t.fallback_pin_used_at,
	t.original_owner_user_id, t.resale_transaction_id, t.created_at, t.updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTicket(s rowScanner) (*Ticket, error) {
	var t Ticket
	if err := s.Scan(
		&t.ID, &t.BookingID, &t.PaymentObligationID, &t.OwnerUserID, &t.SectionID,
		&t.SeatPosition, &t.Status, &t.IsAssigned, &t.QRSecretKey, &t.LastQRGeneratedAt,
		&t.FallbackPIN, &t.FallbackPINUsed, &t.FallbackPINUsedAt, &t.OriginalOwnerUserID,
		&t.ResaleTransactionID, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &t, nil
}

// ListByOwner returns paginated tickets for an owner. The owner scope is
// enforced in SQL WHERE (owner_user_id = $1) so RLS-agnostic callers stay safe.
func (r *PostgresTicketRepository) ListByOwner(ctx context.Context, ownerID uuid.UUID, statusFilter string, page, limit int) ([]TicketListItem, int, error) {
	args := []any{ownerID}
	where := "t.owner_user_id = $1"
	if statusFilter != "" {
		args = append(args, statusFilter)
		where += fmt.Sprintf(" AND t.status::text = $%d", len(args))
	}
	offset := (page - 1) * limit

	listSQL := fmt.Sprintf(`
		SELECT t.id, e.id, e.name, s.name, t.seat_position, t.status::text,
		       t.fallback_pin, t.created_at
		FROM tickets t
		JOIN sections s ON s.id = t.section_id
		JOIN events e ON e.id = s.event_id
		WHERE %s
		ORDER BY t.created_at DESC
		LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2)
	listArgs := append(append([]any{}, args...), limit, offset)

	rows, err := r.pool.Query(ctx, listSQL, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list tickets by owner: %w", err)
	}
	defer rows.Close()

	items := make([]TicketListItem, 0)
	for rows.Next() {
		var it TicketListItem
		if err := rows.Scan(
			&it.ID, &it.EventID, &it.EventName, &it.SectionName,
			&it.SeatPosition, &it.Status, &it.FallbackPIN, &it.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan ticket list item: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate tickets: %w", err)
	}

	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM tickets t WHERE %s", where)
	var total int
	if err := r.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tickets by owner: %w", err)
	}
	return items, total, nil
}

// GetByID returns a ticket by ID with event and section names joined in.
func (r *PostgresTicketRepository) GetByID(ctx context.Context, ticketID uuid.UUID) (*TicketDetail, error) {
	q := fmt.Sprintf(`
		SELECT %s, e.id, e.name, s.name
		FROM tickets t
		JOIN sections s ON s.id = t.section_id
		JOIN events e ON e.id = s.event_id
		WHERE t.id = $1`, ticketColumnsQualified)

	var td TicketDetail
	err := r.pool.QueryRow(ctx, q, ticketID).Scan(
		&td.ID, &td.BookingID, &td.PaymentObligationID,
		&td.OwnerUserID, &td.SectionID, &td.SeatPosition,
		&td.Status, &td.IsAssigned, &td.QRSecretKey,
		&td.LastQRGeneratedAt, &td.FallbackPIN, &td.FallbackPINUsed,
		&td.FallbackPINUsedAt, &td.OriginalOwnerUserID,
		&td.ResaleTransactionID, &td.CreatedAt, &td.UpdatedAt,
		&td.EventID, &td.EventName, &td.SectionName,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTicketNotFound
		}
		return nil, fmt.Errorf("get ticket by id: %w", err)
	}
	return &td, nil
}

// GetByIDForUpdate returns a ticket row locked for update (requires active tx).
func (r *PostgresTicketRepository) GetByIDForUpdate(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) (*Ticket, error) {
	row := tx.QueryRow(ctx, `SELECT `+ticketColumns+` FROM tickets WHERE id = $1 FOR UPDATE`, ticketID)
	t, err := scanTicket(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTicketNotFound
		}
		return nil, fmt.Errorf("get ticket for update: %w", err)
	}
	return t, nil
}

// UpdateAssign assigns a ticket to a target user, returning rows affected
// (0 means the ticket was already assigned or not eligible).
func (r *PostgresTicketRepository) UpdateAssign(ctx context.Context, tx pgx.Tx, ticketID, targetUserID uuid.UUID) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE tickets
		SET owner_user_id = $1,
		    original_owner_user_id = owner_user_id,
		    is_assigned = TRUE,
		    updated_at = NOW()
		WHERE id = $2 AND is_assigned = FALSE`,
		targetUserID, ticketID)
	if err != nil {
		return 0, fmt.Errorf("assign ticket: %w", err)
	}
	return tag.RowsAffected(), nil
}

// GetUserStatusAndBlacklist returns the target user status and whether the
// user ID is blacklisted (type='user_id'). ROADMAP E6-S1-T14d/e. E8 admin
// can extend to set type='user_id' entries.
func (r *PostgresTicketRepository) GetUserStatusAndBlacklist(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (string, bool, error) {
	var status string
	var blacklisted bool
	err := tx.QueryRow(ctx, `
		SELECT u.status::text, EXISTS (
			SELECT 1 FROM blacklist b
			WHERE b.type = 'user_id' AND b.value = u.id::text
		)
		FROM users u
		WHERE u.id = $1`, userID).Scan(&status, &blacklisted)
	if err != nil {
		return "", false, fmt.Errorf("get user status/blacklist: %w", err)
	}
	return status, blacklisted, nil
}

// MarkFallbackPINUsed marks a ticket's fallback PIN as consumed (single-use).
// Idempotent via WHERE fallback_pin_used = FALSE.
func (r *PostgresTicketRepository) MarkFallbackPINUsed(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE tickets
		SET fallback_pin_used = TRUE,
		    fallback_pin_used_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1 AND fallback_pin_used = FALSE`,
		ticketID)
	if err != nil {
		return 0, fmt.Errorf("mark fallback pin used: %w", err)
	}
	return tag.RowsAffected(), nil
}
