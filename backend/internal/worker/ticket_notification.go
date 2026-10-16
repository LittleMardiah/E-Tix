package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/notification"
	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const (
	ticketNotifyLastSeenKey        = "ticket_notify:last_seen"
	ticketNotifyBatchSize          = 100
	DefaultTicketNotifyPollSeconds = 10
)

type ticketNotificationRow struct {
	ID          uuid.UUID
	OwnerID     uuid.UUID
	Email       string
	Phone       string
	FallbackPIN string
}

// TicketNotificationWorker polls for newly-created CONFIRMED tickets and
// dispatches their fallback PIN to the owner.
type TicketNotificationWorker struct {
	pool     *pgxpool.Pool
	notifier notification.TicketNotification
	rdb      *redisclient.Client
	logger   *zap.Logger
	interval time.Duration
}

// NewTicketNotificationWorker constructs a TicketNotificationWorker.
func NewTicketNotificationWorker(
	pool *pgxpool.Pool,
	notifier notification.TicketNotification,
	rdb *redisclient.Client,
	logger *zap.Logger,
	interval time.Duration,
) *TicketNotificationWorker {
	if interval <= 0 {
		interval = DefaultTicketNotifyPollSeconds * time.Second
	}
	return &TicketNotificationWorker{
		pool: pool, notifier: notifier, rdb: rdb, logger: logger, interval: interval,
	}
}

// Run runs the polling loop until ctx is cancelled.
func (w *TicketNotificationWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				w.logger.Warn("ticket notification poll failed", zap.Error(err))
			}
		}
	}
}

// RunOnce executes a single poll-and-dispatch cycle.
func (w *TicketNotificationWorker) RunOnce(ctx context.Context) error {
	lastSeen, err := w.getLastSeen(ctx)
	if err != nil {
		return fmt.Errorf("ticket notify: get last seen: %w", err)
	}

	rows, err := w.listConfirmedSince(ctx, lastSeen, ticketNotifyBatchSize)
	if err != nil {
		return fmt.Errorf("ticket notify: list: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	for _, r := range rows {
		if err := w.notifier.SendFallbackPIN(ctx, r.OwnerID, r.Email, r.Phone, r.FallbackPIN); err != nil {
			w.logger.Warn("ticket notify: dispatch failed",
				zap.String("ticket_id", r.ID.String()),
				zap.Error(err))
			continue
		}
		if err := w.setLastSeen(ctx, r.ID); err != nil {
			w.logger.Warn("ticket notify: set last seen failed",
				zap.String("ticket_id", r.ID.String()),
				zap.Error(err))
		}
	}
	return nil
}

func (w *TicketNotificationWorker) getLastSeen(ctx context.Context) (uuid.UUID, error) {
	v, err := w.rdb.GetString(ctx, ticketNotifyLastSeenKey)
	if err != nil {
		return uuid.Nil, err
	}
	if v == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(v)
}

func (w *TicketNotificationWorker) setLastSeen(ctx context.Context, id uuid.UUID) error {
	return w.rdb.SetValue(ctx, ticketNotifyLastSeenKey, id.String(), 0)
}

func (w *TicketNotificationWorker) listConfirmedSince(ctx context.Context, since uuid.UUID, limit int) ([]ticketNotificationRow, error) {
	q := `
		SELECT t.id, t.owner_user_id, u.email, u.phone, t.fallback_pin
		FROM tickets t
		JOIN users u ON u.id = t.owner_user_id
		WHERE t.id > $1 AND t.status = 'CONFIRMED'
		ORDER BY t.id ASC
		LIMIT $2`
	rows, err := w.pool.Query(ctx, q, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ticketNotificationRow, 0, limit)
	for rows.Next() {
		var r ticketNotificationRow
		if err := rows.Scan(&r.ID, &r.OwnerID, &r.Email, &r.Phone, &r.FallbackPIN); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
