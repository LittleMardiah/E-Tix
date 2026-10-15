package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
)

// BookingExpiryPayload is the wire shape published by LockSeats (FASE 3).
type BookingExpiryPayload struct {
	Type      string `json:"type"`
	BookingID string `json:"booking_id"`
}

// errPoison marks messages that will never succeed on redelivery (JSON/UUID
// parse errors). These are discarded (Nack requeue=false). All other errors
// are treated as transient and requeued (Nack requeue=true).
var errPoison = errors.New("worker: poison message")

// bookingExpiryRepo is the minimum surface needed by BookingExpiryWorker.
type bookingExpiryRepo interface {
	GetPaymentStatus(ctx context.Context, bookingID uuid.UUID) (string, error)
	GetPaymentStatusForUpdate(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID) (string, error)
	MarkBookingExpired(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID) (int64, error)
	InsertAdminAction(ctx context.Context, tx pgx.Tx, adminID uuid.UUID, action, resourceType string, resourceID uuid.UUID, reason string) error
	InsertAdminActionStandalone(ctx context.Context, adminID uuid.UUID, action, resourceType string, resourceID uuid.UUID, reason string) error
}

// seatReleaser releases Redis bitmap bits for a booking.
type seatReleaser interface {
	ReleaseSeatsForExpiry(ctx context.Context, bookingID uuid.UUID) error
}

// refundGateway issues a refund via the payment provider.
type refundGateway interface {
	Refund(ctx context.Context, externalTxID, amount string) (string, error)
}

// BookingExpiryWorker consumes release_seat_check messages and rolls back
// unpaid bookings after their 180s TTL expires.
//
// Idempotent Lazy-Check pattern (LOGIC_FLOW §3.3): the message is never
// cancelled; at delivery time we re-read booking state and act accordingly.
type BookingExpiryWorker struct {
	conn        *queue.Connection
	db          *pgxpool.Pool
	repo        bookingExpiryRepo
	obligations payment.PaymentObligationRepository
	releaser    seatReleaser
	refund      refundGateway
	systemUser  uuid.UUID
	logger      *zap.Logger
}

// NewBookingExpiryWorker wires the worker.
func NewBookingExpiryWorker(
	conn *queue.Connection,
	db *pgxpool.Pool,
	repo bookingExpiryRepo,
	obligations payment.PaymentObligationRepository,
	releaser seatReleaser,
	refund refundGateway,
	systemUser uuid.UUID,
	logger *zap.Logger,
) *BookingExpiryWorker {
	return &BookingExpiryWorker{
		conn:        conn,
		db:          db,
		repo:        repo,
		obligations: obligations,
		releaser:    releaser,
		refund:      refund,
		systemUser:  systemUser,
		logger:      logger,
	}
}

// Run consumes the release_seat_check queue until ctx is cancelled.
func (w *BookingExpiryWorker) Run(ctx context.Context) error {
	deliveries, err := w.conn.Consume("release_seat_check")
	if err != nil {
		return fmt.Errorf("booking expiry: consume: %w", err)
	}
	for delivery := range deliveries {
		if perr := w.processMessage(ctx, delivery.Body); perr != nil {
			requeue := !errors.Is(perr, errPoison)
			w.logger.Error("booking expiry message failed",
				zap.Error(perr), zap.Bool("requeue", requeue))
			if nerr := w.conn.Nack(delivery, requeue); nerr != nil {
				w.logger.Error("booking expiry nack failed", zap.Error(nerr))
			}
			continue
		}
		if aerr := w.conn.Ack(delivery); aerr != nil {
			w.logger.Error("booking expiry ack failed", zap.Error(aerr))
		}
	}
	return nil
}

// ProcessOne is exposed for tests. Callers pass the raw message body.
func (w *BookingExpiryWorker) ProcessOne(ctx context.Context, body []byte) error {
	return w.processMessage(ctx, body)
}

func (w *BookingExpiryWorker) processMessage(ctx context.Context, body []byte) error {
	var p BookingExpiryPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("%w: unmarshal: %v", errPoison, err)
	}
	if p.BookingID == "" {
		return fmt.Errorf("%w: empty booking_id", errPoison)
	}
	bookingID, err := uuid.Parse(p.BookingID)
	if err != nil {
		return fmt.Errorf("%w: parse booking_id: %v", errPoison, err)
	}

	status, err := w.repo.GetPaymentStatus(ctx, bookingID)
	if err != nil {
		return fmt.Errorf("booking expiry: get status: %w", err)
	}
	// Lazy-check: PAID means the payer made it in time; nothing to do.
	if status == payment.StatusPaid {
		w.logger.Info("booking expiry no-op (paid)", zap.String("booking_id", p.BookingID))
		return nil
	}
	if status != payment.StatusPending {
		w.logger.Info("booking expiry no-op (non-pending)",
			zap.String("booking_id", p.BookingID),
			zap.String("status", status))
		return nil
	}

	return w.rollback(ctx, bookingID)
}

func (w *BookingExpiryWorker) rollback(ctx context.Context, bookingID uuid.UUID) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("booking expiry: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// H-1 FIX: Re-check booking state INSIDE tx with FOR UPDATE. This
	// closes the race window between the outer lazy-check and commit
	// (webhook may have promoted the booking to PAID in between).
	// Without this, we would release seats for a PAID booking (oversell).
	status, err := w.repo.GetPaymentStatusForUpdate(ctx, tx, bookingID)
	if err != nil {
		return fmt.Errorf("booking expiry: re-check status: %w", err)
	}
	if status != payment.StatusPending {
		w.logger.Info("booking expiry: no-op (state changed inside tx)",
			zap.String("booking_id", bookingID.String()),
			zap.String("status", status))
		return nil // tx will be rolled back by defer (no writes yet)
	}

	// 1. Flip PENDING → EXPIRED. Guard RowsAffected: no PENDING means
	// something raced; abort without releasing seats.
	expired, err := w.obligations.MarkExpiredByBooking(ctx, tx, bookingID)
	if err != nil {
		return fmt.Errorf("booking expiry: mark expired: %w", err)
	}
	if expired == 0 {
		w.logger.Warn("booking expiry: no PENDING obligations to expire",
			zap.String("booking_id", bookingID.String()))
		// Fall through: booking itself still needs EXPIRED.
	}

	// 2. Mark booking EXPIRED with RowsAffected guard.
	affected, err := w.repo.MarkBookingExpired(ctx, tx, bookingID)
	if err != nil {
		return fmt.Errorf("booking expiry: mark booking expired: %w", err)
	}
	if affected == 0 {
		// Another writer beat us; abort all writes and skip seat release.
		w.logger.Info("booking expiry: booking no longer PENDING; aborting",
			zap.String("booking_id", bookingID.String()))
		return nil // deferred Rollback discards the partial UPDATE
	}

	// 3. Audit.
	if err := w.repo.InsertAdminAction(ctx, tx, w.systemUser,
		"FORCE_RELEASE_SEAT", "booking", bookingID,
		"SPLIT_PAY_TIMEOUT"); err != nil {
		return fmt.Errorf("booking expiry: insert admin action: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("booking expiry: commit: %w", err)
	}

	// 4. Release Redis bits (post-commit; best-effort, idempotent).
	if err := w.releaser.ReleaseSeatsForExpiry(ctx, bookingID); err != nil {
		w.logger.Warn("booking expiry: release seats failed (post-commit)",
			zap.String("booking_id", bookingID.String()),
			zap.Error(err))
	}

	// 5. Refund paid obligations (best-effort).
	w.refundPaidObligations(ctx, bookingID)

	w.logger.Info("booking expired", zap.String("booking_id", bookingID.String()))
	return nil
}

func (w *BookingExpiryWorker) refundPaidObligations(ctx context.Context, bookingID uuid.UUID) {
	obs, err := w.obligations.ListByBooking(ctx, bookingID)
	if err != nil {
		w.logger.Warn("booking expiry: list obligations for refund failed",
			zap.String("booking_id", bookingID.String()), zap.Error(err))
		return
	}
	for _, o := range obs {
		if o.Status != payment.StatusPaid {
			continue
		}
		if o.ExternalTransactionID == nil || *o.ExternalTransactionID == "" {
			// M-1: log so money-loss path is visible; not silent.
			w.logger.Warn("booking expiry: PAID obligation missing external_transaction_id",
				zap.String("obligation_id", o.ID.String()),
				zap.String("booking_id", bookingID.String()))
			w.recordRefundFailure(ctx, o.ID, bookingID, "MISSING_EXTERNAL_TX_ID")
			continue
		}

		refundID, err := w.refundWithRetry(ctx, *o.ExternalTransactionID, o.Amount)
		if err != nil {
			// H-2: persist failure to admin_actions so it's traceable.
			w.logger.Error("booking expiry: refund failed after retries",
				zap.String("obligation_id", o.ID.String()), zap.Error(err))
			w.recordRefundFailure(ctx, o.ID, bookingID, "REFUND_FAILED_RETRY_EXHAUSTED")
			continue
		}

		// M-1: record refund_tx_id on the obligation.
		if err := w.markRefunded(ctx, o.ID, refundID); err != nil {
			w.logger.Warn("booking expiry: mark refunded failed",
				zap.String("obligation_id", o.ID.String()), zap.Error(err))
		}
		w.logger.Info("booking expiry: refunded obligation",
			zap.String("obligation_id", o.ID.String()),
			zap.String("refund_id", refundID))
	}
}

// recordRefundFailure writes a standalone admin_action for an unresolved refund.
// Best-effort — a DB blip here does not block worker progress.
func (w *BookingExpiryWorker) recordRefundFailure(ctx context.Context, obligationID, bookingID uuid.UUID, reason string) {
	if err := w.repo.InsertAdminActionStandalone(ctx, w.systemUser,
		"MANUAL_REFUND", "payment_obligation", obligationID, reason); err != nil {
		w.logger.Error("booking expiry: refund failure audit failed",
			zap.String("obligation_id", obligationID.String()), zap.Error(err))
	}
}

// markRefunded flips an obligation to REFUNDED with its gateway refund id.
func (w *BookingExpiryWorker) markRefunded(ctx context.Context, obligationID uuid.UUID, refundID string) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := w.obligations.MarkRefunded(ctx, tx, obligationID, refundID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (w *BookingExpiryWorker) refundWithRetry(ctx context.Context, externalTxID, amount string) (string, error) {
	delays := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	var lastErr error
	for i, d := range delays {
		refundID, err := w.refund.Refund(ctx, externalTxID, amount)
		if err == nil {
			return refundID, nil
		}
		lastErr = err

		// L-1: no sleep after the last attempt — it would be dead time.
		if i == len(delays)-1 {
			break
		}
		w.logger.Warn("booking expiry: refund retry",
			zap.Int("attempt", i+1), zap.String("tx", externalTxID))
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(d):
		}
	}
	return "", lastErr
}
