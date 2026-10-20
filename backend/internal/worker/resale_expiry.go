package worker

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/resale"
)

type ResaleExpiryWorker struct {
	pool *pgxpool.Pool
	repo resale.ResaleRepository
	log  *zap.Logger
}

func NewResaleExpiryWorker(pool *pgxpool.Pool, repo resale.ResaleRepository, log *zap.Logger) *ResaleExpiryWorker {
	return &ResaleExpiryWorker{pool: pool, repo: repo, log: log}
}

func (w *ResaleExpiryWorker) Handle(ctx context.Context, body []byte) error {
	var p resale.ResaleExpiryPayload
	if err := json.Unmarshal(body, &p); err != nil {
		// TD-F7-03 FIXED: WARN with body preview (observability on poison)
		w.log.Warn("resale_expiry: poison payload discarded",
			zap.Error(err), zap.Int("body_len", len(body)))
		return nil
	}
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rt, err := w.repo.GetByIDForUpdate(ctx, tx, p.ResaleID)
	if err != nil {
		return nil
	}
	if rt.Status != "PENDING" || rt.BuyerUserID == nil {
		return nil
	}
	if err := w.repo.ClearBuyer(ctx, tx, p.ResaleID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	w.log.Info("resale_expiry: cleared", zap.String("id", p.ResaleID.String()))
	return nil
}

func (w *ResaleExpiryWorker) Run(ctx context.Context) error {
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			_, err := w.pool.Exec(ctx,
				`UPDATE resale_transactions SET buyer_user_id=NULL, updated_at=NOW()
				 WHERE status::text='PENDING' AND buyer_user_id IS NOT NULL
				   AND updated_at < NOW() - INTERVAL '3 minutes'`)
			if err != nil {
				w.log.Warn("resale_expiry ticker", zap.Error(err))
			}
		}
	}
}

var _ = uuid.Nil
