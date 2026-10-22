package worker

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PgxReconciliationRepo implements reconciliationRepo against Postgres.
type PgxReconciliationRepo struct {
	db Pool
}

func NewPgxReconciliationRepo(db Pool) *PgxReconciliationRepo {
	return &PgxReconciliationRepo{db: db}
}

func (r *PgxReconciliationRepo) ReconcileUpdate(ctx context.Context, tx pgx.Tx, obligationID uuid.UUID, status string, note string) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE payment_obligations
		SET status = $1, webhook_received_at = NOW(), updated_at = NOW()
		WHERE id = $2 AND status = 'PENDING'`, status, obligationID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *PgxReconciliationRepo) InsertAdminAction(ctx context.Context, tx pgx.Tx, adminID uuid.UUID, action, resourceType string, resourceID uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO admin_actions (admin_id, action_type, target_resource_type,
			target_resource_id, description, reason)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		adminID, action, resourceType, resourceID, "auto reconcile", reason)
	return err
}
