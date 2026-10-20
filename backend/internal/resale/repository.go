package resale

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrResaleNotFound           = errors.New("resale: not found")
	ErrResaleForbidden          = errors.New("resale: forbidden")
	ErrResaleListingExists      = errors.New("resale: active listing already exists")
	ErrResaleNotPending         = errors.New("resale: not pending")
	ErrResaleAlreadyReserved    = errors.New("resale: already reserved")
	ErrResaleOwnListing         = errors.New("resale: cannot buy own listing")
	ErrResalePriceExceedsCap    = errors.New("resale: price exceeds cap")
	ErrResaleTicketNotConfirmed = errors.New("resale: ticket not CONFIRMED")
)

type ResaleRepository interface {
	Create(ctx context.Context, r *ResaleTransaction) error
	GetByID(ctx context.Context, id uuid.UUID) (*ResaleTransaction, error)
	GetByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*ResaleTransaction, error)
	ListPending(ctx context.Context, eventID uuid.UUID) ([]BrowseListing, error)
	UpdateBuyer(ctx context.Context, tx pgx.Tx, id uuid.UUID, buyerID uuid.UUID) error
	ClearBuyer(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
	UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error
	HasActiveListing(ctx context.Context, ticketID uuid.UUID) (bool, error)
	SetPaymentTransactionID(ctx context.Context, id uuid.UUID, txID string) error
	GetByPaymentTransactionID(ctx context.Context, txID string) (*ResaleTransaction, error)
	TicketInfoForResale(ctx context.Context, ticketID uuid.UUID) (*TicketForResale, error)
}

type PostgresResaleRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresResaleRepository(pool *pgxpool.Pool) *PostgresResaleRepository {
	return &PostgresResaleRepository{pool: pool}
}

// All bodies return not-implemented errors — E2..E6 fill each method.

func (r *PostgresResaleRepository) Create(ctx context.Context, rt *ResaleTransaction) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO resale_transactions (
			id, ticket_id, event_id, seller_user_id, buyer_user_id,
			original_price, resale_price, platform_fee, seller_payout,
			status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW(),NOW())`,
		rt.ID, rt.TicketID, rt.EventID, rt.SellerUserID, rt.BuyerUserID,
		rt.OriginalPrice, rt.ResalePrice, rt.PlatformFee, rt.SellerPayout, rt.Status)
	if err != nil {
		// S2-1: map partial unique idx_resale_single_active violation to sentinel.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrResaleListingExists
		}
		return err
	}
	return nil
}
func (r *PostgresResaleRepository) GetByID(_ context.Context, _ uuid.UUID) (*ResaleTransaction, error) {
	return nil, errors.New("resale.GetByID: not implemented (E3)")
}
func (r *PostgresResaleRepository) GetByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*ResaleTransaction, error) {
	var rt ResaleTransaction
	err := tx.QueryRow(ctx, `
		SELECT id, ticket_id, event_id, seller_user_id, buyer_user_id,
		       original_price::text, resale_price::text, platform_fee::text, seller_payout::text,
		       status::text, payment_transaction_id, payout_transaction_id,
		       created_at, completed_at, cancelled_at, updated_at
		FROM resale_transactions WHERE id = $1 FOR UPDATE`, id).Scan(
		&rt.ID, &rt.TicketID, &rt.EventID, &rt.SellerUserID, &rt.BuyerUserID,
		&rt.OriginalPrice, &rt.ResalePrice, &rt.PlatformFee, &rt.SellerPayout,
		&rt.Status, &rt.PaymentTransactionID, &rt.PayoutTransactionID,
		&rt.CreatedAt, &rt.CompletedAt, &rt.CancelledAt, &rt.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrResaleNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rt, nil
}
func (r *PostgresResaleRepository) ListPending(ctx context.Context, eventID uuid.UUID) ([]BrowseListing, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT rt.id, rt.ticket_id, u.name, s.name || ' - ' || t.seat_position,
		       rt.resale_price::text, rt.platform_fee::text, rt.seller_payout::text,
		       rt.original_price::text, rt.created_at
		FROM resale_transactions rt
		JOIN tickets t ON t.id = rt.ticket_id
		JOIN sections s ON s.id = t.section_id
		JOIN users u ON u.id = rt.seller_user_id
		WHERE rt.event_id = $1
		  AND rt.status::text = 'PENDING'
		  AND rt.buyer_user_id IS NULL
		ORDER BY rt.created_at DESC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BrowseListing
	for rows.Next() {
		var b BrowseListing
		if err := rows.Scan(&b.ResaleID, &b.TicketID, &b.SellerName, &b.Seat,
			&b.ResalePrice, &b.PlatformFee, &b.SellerPayout, &b.OriginalPrice, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (r *PostgresResaleRepository) UpdateBuyer(ctx context.Context, tx pgx.Tx, id uuid.UUID, buyerID uuid.UUID) error {
	ct, err := tx.Exec(ctx,
		`UPDATE resale_transactions SET buyer_user_id=$1, updated_at=NOW()
		 WHERE id=$2 AND status::text='PENDING' AND buyer_user_id IS NULL`,
		buyerID, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrResaleAlreadyReserved
	}
	return nil
}
func (r *PostgresResaleRepository) ClearBuyer(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`UPDATE resale_transactions SET buyer_user_id=NULL, updated_at=NOW()
		 WHERE id=$1 AND status::text='PENDING' AND buyer_user_id IS NOT NULL`, id)
	return err
}
func (r *PostgresResaleRepository) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	var extra, guard string
	switch status {
	case "CANCELLED":
		extra = ", cancelled_at=NOW()"
		guard = " AND status::text='PENDING' AND buyer_user_id IS NULL"
	case "COMPLETED":
		extra = ", completed_at=NOW()"
		guard = " AND status::text='PENDING'" // from any PENDING (seller-listed or reserved)
	case "PENDING_PAYOUT":
		extra = ""
		guard = " AND status::text='PENDING'" // from PENDING
	default:
		return fmt.Errorf("resale.UpdateStatus: unexpected target status %q", status)
	}
	ct, err := tx.Exec(ctx,
		`UPDATE resale_transactions SET status=$1, updated_at=NOW()`+extra+` WHERE id=$2`+guard,
		status, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrResaleNotPending
	}
	return nil
}
func (r *PostgresResaleRepository) HasActiveListing(ctx context.Context, ticketID uuid.UUID) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM resale_transactions WHERE ticket_id=$1 AND status::text='PENDING')`,
		ticketID).Scan(&exists)
	return exists, err
}
func (r *PostgresResaleRepository) SetPaymentTransactionID(ctx context.Context, id uuid.UUID, txID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE resale_transactions SET payment_transaction_id=$1, updated_at=NOW() WHERE id=$2`,
		txID, id)
	return err
}

// GetByPaymentTransactionID looks up a resale row by gateway transaction id.
// Used by the webhook handler to detect resale payment SUCCESS (S5-1).
func (r *PostgresResaleRepository) GetByPaymentTransactionID(ctx context.Context, txID string) (*ResaleTransaction, error) {
	var rt ResaleTransaction
	err := r.pool.QueryRow(ctx, `
		SELECT id, ticket_id, event_id, seller_user_id, buyer_user_id,
		       original_price::text, resale_price::text, platform_fee::text, seller_payout::text,
		       status::text, payment_transaction_id, payout_transaction_id,
		       created_at, completed_at, cancelled_at, updated_at
		FROM resale_transactions WHERE payment_transaction_id = $1`, txID).Scan(
		&rt.ID, &rt.TicketID, &rt.EventID, &rt.SellerUserID, &rt.BuyerUserID,
		&rt.OriginalPrice, &rt.ResalePrice, &rt.PlatformFee, &rt.SellerPayout,
		&rt.Status, &rt.PaymentTransactionID, &rt.PayoutTransactionID,
		&rt.CreatedAt, &rt.CompletedAt, &rt.CancelledAt, &rt.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrResaleNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rt, nil
}

func (r *PostgresResaleRepository) TicketInfoForResale(ctx context.Context, ticketID uuid.UUID) (*TicketForResale, error) {
	var t TicketForResale
	err := r.pool.QueryRow(ctx, `
		SELECT t.owner_user_id, t.status::text, s.price::text, s.event_id
		FROM tickets t JOIN sections s ON s.id = t.section_id
		WHERE t.id = $1`, ticketID).Scan(&t.OwnerUserID, &t.Status, &t.SectionPrice, &t.EventID)
	if err == pgx.ErrNoRows {
		return nil, ErrResaleNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

var _ ResaleRepository = (*PostgresResaleRepository)(nil)
