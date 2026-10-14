package payment

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PaymentObligationRepository is the data access contract for payment_obligations.
type PaymentObligationRepository interface {
	// Create persists a new obligation.
	Create(ctx context.Context, o *PaymentObligation) error
	// GetByID returns an obligation by primary key.
	GetByID(ctx context.Context, id uuid.UUID) (*PaymentObligation, error)
	// GetByExternalTxID returns an obligation by external_transaction_id.
	GetByExternalTxID(ctx context.Context, txID string) (*PaymentObligation, error)
	// GetByIdempotencyKey returns an obligation by idempotency key (L2 lookup).
	GetByIdempotencyKey(ctx context.Context, key string) (*PaymentObligation, error)
	// ListByBooking returns all obligations for a booking, ordered by id ASC.
	ListByBooking(ctx context.Context, bookingID uuid.UUID) ([]PaymentObligation, error)

	// GetByIDForUpdate reads an obligation FOR UPDATE inside the caller's tx.
	GetByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*PaymentObligation, error)

	// GetByExternalTxIDForUpdate locks an obligation by external_transaction_id.
	// Used by the webhook receiver to serialize concurrent webhook deliveries.
	GetByExternalTxIDForUpdate(ctx context.Context, tx pgx.Tx, txID string) (*PaymentObligation, error)

	// UpdateStatusWithWebhook sets status + webhook_received_at atomically.
	UpdateStatusWithWebhook(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error
}

// PostgresPaymentObligationRepository is the PostgreSQL implementation.
type PostgresPaymentObligationRepository struct {
	db *pgxpool.Pool
}

// NewPostgresPaymentObligationRepository returns a new repository bound to db.
func NewPostgresPaymentObligationRepository(db *pgxpool.Pool) PaymentObligationRepository {
	return &PostgresPaymentObligationRepository{db: db}
}

const obligationColumns = `id, booking_id, user_id, seat_position, amount::text,
	status::text, payment_method::text, payment_link, external_transaction_id,
	webhook_received_at, idempotency_key, refunded_at, refund_transaction_id,
	created_at, updated_at`

func scanObligation(row pgx.Row) (*PaymentObligation, error) {
	var o PaymentObligation
	err := row.Scan(
		&o.ID, &o.BookingID, &o.UserID, &o.SeatPosition, &o.Amount,
		&o.Status, &o.PaymentMethod, &o.PaymentLink, &o.ExternalTransactionID,
		&o.WebhookReceivedAt, &o.IdempotencyKey, &o.RefundedAt, &o.RefundTransactionID,
		&o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *PostgresPaymentObligationRepository) Create(ctx context.Context, o *PaymentObligation) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO payment_obligations (
			id, booking_id, user_id, seat_position, amount, status,
			payment_method, payment_link, external_transaction_id,
			idempotency_key, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now(),now())`,
		o.ID, o.BookingID, o.UserID, o.SeatPosition, o.Amount, o.Status,
		o.PaymentMethod, o.PaymentLink, o.ExternalTransactionID, o.IdempotencyKey,
	)
	return err
}

func (r *PostgresPaymentObligationRepository) GetByID(ctx context.Context, id uuid.UUID) (*PaymentObligation, error) {
	row := r.db.QueryRow(ctx, `SELECT `+obligationColumns+` FROM payment_obligations WHERE id = $1`, id)
	o, err := scanObligation(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrObligationNotFound
		}
		return nil, err
	}
	return o, nil
}

func (r *PostgresPaymentObligationRepository) GetByExternalTxID(ctx context.Context, txID string) (*PaymentObligation, error) {
	row := r.db.QueryRow(ctx, `SELECT `+obligationColumns+` FROM payment_obligations WHERE external_transaction_id = $1`, txID)
	o, err := scanObligation(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrWebhookNotFound
		}
		return nil, err
	}
	return o, nil
}

func (r *PostgresPaymentObligationRepository) GetByIdempotencyKey(ctx context.Context, key string) (*PaymentObligation, error) {
	row := r.db.QueryRow(ctx, `SELECT `+obligationColumns+` FROM payment_obligations WHERE idempotency_key = $1`, key)
	o, err := scanObligation(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // caller treats nil, nil as cache-miss
		}
		return nil, err
	}
	return o, nil
}

func (r *PostgresPaymentObligationRepository) ListByBooking(ctx context.Context, bookingID uuid.UUID) ([]PaymentObligation, error) {
	rows, err := r.db.Query(ctx, `SELECT `+obligationColumns+`
		FROM payment_obligations WHERE booking_id = $1 ORDER BY id ASC`, bookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PaymentObligation
	for rows.Next() {
		o, err := scanObligation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

func (r *PostgresPaymentObligationRepository) GetByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*PaymentObligation, error) {
	row := tx.QueryRow(ctx, `SELECT `+obligationColumns+` FROM payment_obligations WHERE id = $1 FOR UPDATE`, id)
	o, err := scanObligation(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrObligationNotFound
		}
		return nil, err
	}
	return o, nil
}

func (r *PostgresPaymentObligationRepository) GetByExternalTxIDForUpdate(ctx context.Context, tx pgx.Tx, txID string) (*PaymentObligation, error) {
	row := tx.QueryRow(ctx, `SELECT `+obligationColumns+` FROM payment_obligations WHERE external_transaction_id = $1 FOR UPDATE`, txID)
	o, err := scanObligation(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrWebhookNotFound
		}
		return nil, err
	}
	return o, nil
}

func (r *PostgresPaymentObligationRepository) UpdateStatusWithWebhook(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	_, err := tx.Exec(ctx, `
		UPDATE payment_obligations
		SET status = $1, webhook_received_at = NOW(), updated_at = NOW()
		WHERE id = $2`, status, id)
	return err
}
