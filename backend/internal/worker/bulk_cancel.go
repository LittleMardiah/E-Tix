package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

type BulkCancelPayload struct {
	EventID uuid.UUID `json:"event_id"`
	AdminID uuid.UUID `json:"admin_id"`
	Reason  string    `json:"reason"`
}

type BulkCancelWorker struct {
	pool *pgxpool.Pool
	rdb  *redis.Client
	gw   adapter.PaymentGateway
	log  *zap.Logger
}

func NewBulkCancelWorker(pool *pgxpool.Pool, rdb *redis.Client, gw adapter.PaymentGateway, log *zap.Logger) *BulkCancelWorker {
	return &BulkCancelWorker{pool: pool, rdb: rdb, gw: gw, log: log}
}

func (w *BulkCancelWorker) Handle(ctx context.Context, body []byte) error {
	var p BulkCancelPayload
	if err := json.Unmarshal(body, &p); err != nil {
		w.log.Warn("bulk_cancel: poison", zap.Error(err))
		return nil
	}
	// 1. Refund PAID obligations
	rows, err := w.pool.Query(ctx, `
		SELECT po.id, po.external_transaction_id, po.amount::text
		FROM payment_obligations po JOIN bookings b ON b.id=po.booking_id
		WHERE b.event_id=$1 AND po.status::text='PAID'`, p.EventID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			obID    uuid.UUID
			extTxID *string
			amount  string
		)
		if err := rows.Scan(&obID, &extTxID, &amount); err != nil {
			continue
		}
		if extTxID == nil {
			continue
		}
		if _, err := w.gw.Refund(ctx, adapter.RefundRequest{
			ExternalTransactionID: *extTxID, Amount: amount, Reason: p.Reason,
		}); err == nil {
			_, _ = w.pool.Exec(ctx,
				`UPDATE payment_obligations SET status='REFUNDED', refunded_at=NOW(), updated_at=NOW() WHERE id=$1`, obID)
		}
	}
	// 2. Cancel tickets
	_, _ = w.pool.Exec(ctx, `
		UPDATE tickets SET status='CANCELLED', updated_at=NOW()
		WHERE booking_id IN (SELECT id FROM bookings WHERE event_id=$1)
		  AND status::text IN ('CONFIRMED','LOCKED','PENDING_SPLIT_PAY','RESALE_PENDING')`, p.EventID)
	// 3. Redis bitmask release — scan events, keep simple: DEL per section
	secRows, _ := w.pool.Query(ctx, `SELECT id FROM sections WHERE event_id=$1`, p.EventID)
	if secRows != nil {
		defer secRows.Close()
		for secRows.Next() {
			var secID uuid.UUID
			_ = secRows.Scan(&secID)
			_ = w.rdb.Del(ctx, fmt.Sprintf("seats:%s:%s:v*", p.EventID.String(), secID.String()))
		}
	}
	w.log.Info("bulk_cancel: done", zap.String("event", p.EventID.String()))
	return nil
}

func (w *BulkCancelWorker) Run(ctx context.Context) error {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			// Stub — actual consume via queue conn in main.go
		}
	}
}
