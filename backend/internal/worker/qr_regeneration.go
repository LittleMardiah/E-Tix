package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
	ticketpkg "github.com/LittleMardiah/E-Tix/backend/internal/ticket"
)

const (
	qrRegenIntervalSeconds = 30
	qrRegenCacheTTLSeconds = 35
	qrRegenBatchSize       = 5000
)

type qrRegenRow struct {
	TicketID    uuid.UUID
	OwnerUserID uuid.UUID
	QRSecretKey string
}

// QRRegenerationWorker refreshes the dynamic HMAC QR for all active tickets
// every QRTimestampBlockSeconds. Batch-pipeline writes to Redis with a TTL
// slightly longer than the block window (latency grace).
type QRRegenerationWorker struct {
	pool     *pgxpool.Pool
	rdb      *redisclient.Client
	logger   *zap.Logger
	interval time.Duration
	nowFn    func() time.Time
}

// NewQRRegenerationWorker constructs a QRRegenerationWorker.
func NewQRRegenerationWorker(
	pool *pgxpool.Pool,
	rdb *redisclient.Client,
	logger *zap.Logger,
	interval time.Duration,
) *QRRegenerationWorker {
	if interval <= 0 {
		interval = qrRegenIntervalSeconds * time.Second
	}
	return &QRRegenerationWorker{
		pool: pool, rdb: rdb, logger: logger, interval: interval, nowFn: time.Now,
	}
}

// Run runs the regeneration loop until ctx is cancelled.
func (w *QRRegenerationWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				w.logger.Warn("qr regeneration cycle failed", zap.Error(err))
			}
		}
	}
}

// RunOnce executes a single regeneration cycle.
func (w *QRRegenerationWorker) RunOnce(ctx context.Context) error {
	start := w.nowFn()
	rows, err := w.listActive(ctx, qrRegenBatchSize)
	if err != nil {
		return fmt.Errorf("qr regen: list active: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	items := make([]redisclient.KVItem, 0, len(rows))
	for _, r := range rows {
		qrData, err := ticketpkg.GenerateQRData(r.TicketID, r.OwnerUserID, r.QRSecretKey, start)
		if err != nil {
			w.logger.Warn("qr regen: generate failed",
				zap.String("ticket_id", r.TicketID.String()),
				zap.Error(err))
			continue
		}
		items = append(items, redisclient.KVItem{
			Key:   "qr:" + r.TicketID.String(),
			Value: qrData,
		})
	}

	if err := w.rdb.PipelineSet(ctx, items, qrRegenCacheTTLSeconds*time.Second); err != nil {
		return fmt.Errorf("qr regen: pipeline set: %w", err)
	}

	w.logger.Info("qr regeneration cycle",
		zap.Int("count", len(items)),
		zap.Int64("duration_ms", time.Since(start).Milliseconds()),
		zap.Int64("timestamp_block", start.Unix()/int64(qrRegenIntervalSeconds)),
	)
	return nil
}

func (w *QRRegenerationWorker) listActive(ctx context.Context, limit int) ([]qrRegenRow, error) {
	q := `
		SELECT id, owner_user_id, qr_secret_key
		FROM tickets
		WHERE status IN ('CONFIRMED', 'RESALE_PENDING')
		ORDER BY id ASC
		LIMIT $1`
	rows, err := w.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]qrRegenRow, 0, 64)
	for rows.Next() {
		var r qrRegenRow
		if err := rows.Scan(&r.TicketID, &r.OwnerUserID, &r.QRSecretKey); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
