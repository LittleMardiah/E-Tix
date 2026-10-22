package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/config"
	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/resale"
)

type PayoutWorker struct {
	pool Pool
	repo resale.ResaleRepository
	gw   adapter.PaymentGateway
	log  *zap.Logger
	cfg  *config.Config
	conn queue.Consumer
}

func NewPayoutWorker(pool Pool, repo resale.ResaleRepository, gw adapter.PaymentGateway, log *zap.Logger, cfg *config.Config, conn queue.Consumer) *PayoutWorker {
	return &PayoutWorker{conn: conn, pool: pool, repo: repo, gw: gw, log: log, cfg: cfg}
}

func (w *PayoutWorker) Handle(ctx context.Context, body []byte) error {
	var p resale.PayoutPayload
	if err := json.Unmarshal(body, &p); err != nil {
		// TD-F7-03 FIXED: WARN with body preview (observability on poison)
		w.log.Warn("payout: poison payload discarded",
			zap.Error(err), zap.Int("body_len", len(body)))
		return nil
	}

	var lastErr error
	var payoutResp *adapter.PayoutResponse
	for attempt := 1; attempt <= 3; attempt++ {
		// FIX #14: PayoutRequest{RecipientID, Amount} — 2 fields only
		resp, err := w.gw.Payout(ctx, adapter.PayoutRequest{
			RecipientID: p.SellerUserID.String(),
			Amount:      p.SellerPayout,
		})
		if err == nil {
			payoutResp = resp
			break
		}
		lastErr = err
		w.log.Warn("payout: retry", zap.Int("attempt", attempt), zap.Error(err))
		if attempt < 3 {
			time.Sleep(time.Duration(100<<attempt) * time.Millisecond)
		}
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if payoutResp != nil {
		if e := w.repo.UpdateStatus(ctx, tx, p.ResaleID, "COMPLETED"); e != nil {
			return e
		}
		_, _ = tx.Exec(ctx,
			`UPDATE resale_transactions SET payout_transaction_id=$1 WHERE id=$2`,
			payoutResp.PayoutID, p.ResaleID)
		if e := tx.Commit(ctx); e != nil {
			return e
		}
		w.log.Info("payout: completed", zap.String("resale", p.ResaleID.String()))
		return nil
	}

	// Retry exhausted -> PENDING_PAYOUT + admin_actions log
	if e := w.repo.UpdateStatus(ctx, tx, p.ResaleID, "PENDING_PAYOUT"); e != nil {
		return e
	}
	_, _ = tx.Exec(ctx, `
		INSERT INTO admin_actions (admin_id, action_type, target_resource_type,
			target_resource_id, description, reason)
		VALUES ($1, 'MANUAL_REFUND',
			'resale_transaction', $2, $3, 'PAYOUT_RETRY_EXHAUSTED')`,
		w.cfg.SystemUserUUID, p.ResaleID, "Payout retry 3x failed: "+lastErr.Error())
	if e := tx.Commit(ctx); e != nil {
		return e
	}
	w.log.Error("payout: exhausted", zap.String("resale", p.ResaleID.String()), zap.Error(lastErr))
	return nil
}

// Run consumes the "resale_payout" queue until ctx cancel.
// Loop pattern matches BookingExpiryWorker.Run (FASE 4).
func (w *PayoutWorker) Run(ctx context.Context) error {
	if w.conn == nil {
		return errors.New("payout: mq connection not set")
	}
	deliveries, err := w.conn.Consume("resale_payout")
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return nil
			}
			if err := w.Handle(ctx, d.Body); err != nil {
				w.log.Warn("payout: handle failed", zap.Error(err))
				_ = d.Nack(false, true) // requeue transient
				continue
			}
			_ = d.Ack(false)
		}
	}
}
