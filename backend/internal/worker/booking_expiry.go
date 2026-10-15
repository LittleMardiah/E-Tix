package worker

import (
	"context"
	"encoding/json"
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

// bookingExpiryRepo is the minimum surface needed by BookingExpiryWorker.
type bookingExpiryRepo interface {
	GetPaymentStatus(ctx context.Context, bookingID uuid.UUID) (string, error)
	MarkBookingExpired(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID) error
	InsertAdminAction(ctx context.Context, tx pgx.Tx, adminID uuid.UUID, action, resourceType string, resourceID uuid.UUID, reason string) error
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
			w.logger.Error("booking expiry message failed", zap.Error(perr))
			if nerr := w.conn.Nack(delivery, false); nerr != nil {
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
		return fmt.Errorf("booking expiry: unmarshal: %w", err)
	}
	if p.BookingID == "" {
		return fmt.Errorf("booking expiry: empty booking_id")
	}
	bookingID, err := uuid.Parse(p.BookingID)
	if err != nil {
		return fmt.Errorf("booking expiry: parse booking_id: %w", err)
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

	// 1. Flip PENDING → EXPIRED.
	if _, err := w.obligations.MarkExpiredByBooking(ctx, tx, bookingID); err != nil {
		return fmt.Errorf("booking expiry: mark expired: %w", err)
	}

	// 2. Mark booking EXPIRED.
	if err := w.repo.MarkBookingExpired(ctx, tx, bookingID); err != nil {
		return fmt.Errorf("booking expiry: mark booking expired: %w", err)
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
	// Note: at this point obligations were already flipped to EXPIRED, so
	// those that were PAID before expiry were NOT captured by MarkExpired.
	// The mock/real gateway refund is a no-op here when nothing matches;
	// real refunds are triggered from the pre-expiry snapshot if needed.
	// Kept simple for MVP: refund only obligations that are still PAID.
	for _, o := range obs {
		if o.Status != payment.StatusPaid || o.ExternalTransactionID == nil {
			continue
		}
		if err := w.refundWithRetry(ctx, *o.ExternalTransactionID, o.Amount); err != nil {
			w.logger.Error("booking expiry: refund failed after retries",
				zap.String("obligation_id", o.ID.String()), zap.Error(err))
			continue
		}
		w.logger.Info("booking expiry: refunded obligation",
			zap.String("obligation_id", o.ID.String()))
	}
}

func (w *BookingExpiryWorker) refundWithRetry(ctx context.Context, externalTxID, amount string) error {
	delays := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	var lastErr error
	for i, d := range delays {
		if _, err := w.refund.Refund(ctx, externalTxID, amount); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
		w.logger.Warn("booking expiry: refund retry",
			zap.Int("attempt", i+1), zap.String("tx", externalTxID))
	}
	return lastErr
}
