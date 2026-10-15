package worker

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxBookingExpiryRepo implements bookingExpiryRepo against Postgres.
type PgxBookingExpiryRepo struct {
	db *pgxpool.Pool
}

func NewPgxBookingExpiryRepo(db *pgxpool.Pool) *PgxBookingExpiryRepo {
	return &PgxBookingExpiryRepo{db: db}
}

func (r *PgxBookingExpiryRepo) GetPaymentStatus(ctx context.Context, bookingID uuid.UUID) (string, error) {
	var status string
	err := r.db.QueryRow(ctx,
		`SELECT payment_status::text FROM bookings WHERE id = $1`, bookingID).Scan(&status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", fmt.Errorf("worker: booking not found")
		}
		return "", err
	}
	return status, nil
}

func (r *PgxBookingExpiryRepo) GetPaymentStatusForUpdate(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID) (string, error) {
	var status string
	err := tx.QueryRow(ctx,
		`SELECT payment_status::text FROM bookings WHERE id = $1 FOR UPDATE`, bookingID).Scan(&status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", fmt.Errorf("worker: booking not found")
		}
		return "", err
	}
	return status, nil
}

func (r *PgxBookingExpiryRepo) MarkBookingExpired(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE bookings
		SET payment_status = 'EXPIRED', updated_at = NOW()
		WHERE id = $1 AND payment_status = 'PENDING'`, bookingID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *PgxBookingExpiryRepo) InsertAdminAction(ctx context.Context, tx pgx.Tx, adminID uuid.UUID, action, resourceType string, resourceID uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO admin_actions (admin_id, action_type, target_resource_type,
			target_resource_id, description, reason)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		adminID, action, resourceType, resourceID, "auto expire", reason)
	return err
}

func (r *PgxBookingExpiryRepo) InsertAdminActionStandalone(ctx context.Context, adminID uuid.UUID, action, resourceType string, resourceID uuid.UUID, reason string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO admin_actions (admin_id, action_type, target_resource_type,
			target_resource_id, description, reason)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		adminID, action, resourceType, resourceID, "worker standalone audit", reason)
	return err
}
