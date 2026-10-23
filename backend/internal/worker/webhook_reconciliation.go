package worker

import (
	"strings"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment"
	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

// reconciliationRepo is the minimum surface for WebhookReconciliationWorker.
// ListPendingOlderThan is served by payment.PaymentObligationRepository (w.repo),
// so it does not need to be duplicated here.
type reconciliationRepo interface {
	ReconcileUpdate(ctx context.Context, tx pgx.Tx, obligationID uuid.UUID, status string, note string) (int64, error)
	InsertAdminAction(ctx context.Context, tx pgx.Tx, adminID uuid.UUID, action, resourceType string, resourceID uuid.UUID, reason string) error
}

// statusFetcher abstracts the gateway GetStatus for testability.
type statusFetcher interface {
	GetStatus(ctx context.Context, externalTxID string) (*adapter.StatusResponse, error)
}

// WebhookReconciliationWorker polls the payment gateway for stuck PENDING
// obligations and reconciles them (ROADMAP FASE 4 §4, SEC_SPEC §6.2).
//
// Runs on a 10-minute ticker; each cycle processes up to 100 oldest PENDING
// obligations older than 10 minutes.
type WebhookReconciliationWorker struct {
	db         TxBeginner
	repo       payment.PaymentObligationRepository
	recon      reconciliationRepo
	fetcher    statusFetcher
	systemUser uuid.UUID
	logger     *zap.Logger

	interval  time.Duration
	threshold time.Duration
	batchSize int
}

// NewWebhookReconciliationWorker wires the worker with sensible defaults.
func NewWebhookReconciliationWorker(
	db TxBeginner,
	repo payment.PaymentObligationRepository,
	recon reconciliationRepo,
	fetcher statusFetcher,
	systemUser uuid.UUID,
	logger *zap.Logger,
) *WebhookReconciliationWorker {
	return &WebhookReconciliationWorker{
		db:         db,
		repo:       repo,
		recon:      recon,
		fetcher:    fetcher,
		systemUser: systemUser,
		logger:     logger,
		interval:   10 * time.Minute,
		threshold:  10 * time.Minute,
		batchSize:  100,
	}
}

// Run starts a ticker until ctx is cancelled.
func (w *WebhookReconciliationWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				w.logger.Error("reconciliation cycle failed", zap.Error(err))
			}
		}
	}
}

// RunOnce processes a single reconciliation batch. Exposed for tests.
func (w *WebhookReconciliationWorker) RunOnce(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-w.threshold)
	pending, err := w.repo.ListPendingOlderThan(ctx, cutoff, w.batchSize)
	if err != nil {
		return fmt.Errorf("reconciliation: list pending: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}

	w.logger.Info("reconciliation cycle", zap.Int("pending_count", len(pending)))

	for _, o := range pending {
		if o.ExternalTransactionID == nil || *o.ExternalTransactionID == "" {
			w.logger.Warn("reconciliation: obligation missing external_transaction_id",
				zap.String("obligation_id", o.ID.String()))
			continue
		}
		if err := w.reconcileOne(ctx, &o); err != nil {
			if strings.Contains(err.Error(), "seat_position cannot be NULL") {
				w.logger.Warn("reconciliation: skipping invalid split obligation",
					zap.String("obligation_id", o.ID.String()))
				continue
			}
			w.logger.Error("reconciliation: obligation failed",
				zap.String("obligation_id", o.ID.String()),
				zap.Error(err))
			// Continue — one failure does not stop the batch.
		}
	}
	return nil
}

func (w *WebhookReconciliationWorker) reconcileOne(ctx context.Context, o *payment.PaymentObligation) error {
	status, err := w.fetcher.GetStatus(ctx, *o.ExternalTransactionID)
	if err != nil {
		return fmt.Errorf("reconciliation: get status: %w", err)
	}
	// L-3: guard against nil status (interface contract doesn't forbid it).
	if status == nil {
		return fmt.Errorf("reconciliation: nil status response")
	}

	var newStatus string
	switch status.TransactionStatus {
	case "settlement", "capture":
		newStatus = payment.StatusPaid
	case "deny", "cancel", "expire", "failure":
		newStatus = payment.StatusFailed
	default:
		return nil
	}

	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("reconciliation: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// M-2: only insert audit row when the UPDATE actually changed a row.
	// This prevents fake MANUAL_PAID audits when the webhook already won
	// the race and flipped the obligation out of PENDING.
	affected, err := w.recon.ReconcileUpdate(ctx, tx, o.ID, newStatus, "reconciliation_cycle")
	if err != nil {
		return fmt.Errorf("reconciliation: update: %w", err)
	}
	if affected == 0 {
		w.logger.Info("reconciliation: no-op (state changed)",
			zap.String("obligation_id", o.ID.String()))
		return nil
	}

	if err := w.recon.InsertAdminAction(ctx, tx, w.systemUser,
		"MANUAL_PAID", "payment_obligation", o.ID,
		"RECONCILED_"+newStatus); err != nil {
		return fmt.Errorf("reconciliation: audit: %w", err)
	}
	return tx.Commit(ctx)
}
