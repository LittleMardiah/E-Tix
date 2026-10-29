package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

type ZonedTicketPayload struct {
	EventID   string    `json:"event_id"`
	ZoneName  string    `json:"zone_name"`
	Quantity  int       `json:"quantity"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

type ZonedBatchWorker struct {
	db     *pgxpool.Pool
	conn   *amqp.Connection
	logger *zap.Logger
}

func NewZonedBatchWorker(db *pgxpool.Pool, conn *amqp.Connection, logger *zap.Logger) *ZonedBatchWorker {
	return &ZonedBatchWorker{db: db, conn: conn, logger: logger}
}

func (w *ZonedBatchWorker) Run(ctx context.Context) error {
	ch, err := w.conn.Channel()
	if err != nil {
		return fmt.Errorf("zoned batch worker: open channel: %w", err)
	}
	defer func() { _ = ch.Close() }()

	if _, err := ch.QueueDeclare("zoned_ticket_generation", true, false, false, false, nil); err != nil {
		return fmt.Errorf("zoned batch worker: declare queue: %w", err)
	}

	deliveries, err := ch.Consume("zoned_ticket_generation", "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("zoned batch worker: consume: %w", err)
	}

	var batch []ZonedTicketPayload
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		toFlush := batch
		batch = nil
		if err := w.flushBatch(ctx, toFlush); err != nil {
			w.logger.Error("zoned batch flush failed", zap.Error(err))
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				flush()
				return nil
			}
			var payload ZonedTicketPayload
			if err := json.Unmarshal(d.Body, &payload); err != nil {
				w.logger.Error("zoned batch unmarshal failed", zap.Error(err))
				_ = d.Nack(false, false)
				continue
			}
			batch = append(batch, payload)
			_ = d.Ack(false)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (w *ZonedBatchWorker) flushBatch(ctx context.Context, batch []ZonedTicketPayload) error {
	rows := make([][]any, 0)
	for _, p := range batch {
		qty := p.Quantity
		if qty <= 0 {
			qty = 1
		}
		uid, _ := uuid.Parse(p.UserID)
		for i := 0; i < qty; i++ {
			rows = append(rows, []any{
				uuid.New(),
				uuid.New(),
				uid,
				uuid.New(),
				nil, // seat_position NULL (ZONED)
				"CONFIRMED",
				false,
				fmt.Sprintf("qr-%s", uuid.NewString()),
				fmt.Sprintf("%06d", rand.Intn(1000000)),
			})
		}
	}
	if len(rows) == 0 {
		return nil
	}

	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("zoned batch: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.CopyFrom(ctx, pgx.Identifier{"tickets"},
		[]string{"id", "booking_id", "owner_user_id", "section_id", "seat_position", "status", "is_assigned", "qr_secret_key", "fallback_pin"},
		pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("zoned batch: copy from: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("zoned batch: commit: %w", err)
	}
	return nil
}
