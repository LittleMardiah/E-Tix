package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const dynamicScoreTTL = 24 * time.Hour

type FanScoreRepo interface {
	InsertBlacklist(ctx context.Context, addedBy uuid.UUID, btype, value, reason string, expiresAt *time.Time) error
}

type FanScoreWorker struct {
	conn   queue.Consumer
	rdb    *redis.Client
	repo   FanScoreRepo
	logger *zap.Logger
}

func NewFanScoreWorker(conn queue.Consumer, rdb *redis.Client, repo FanScoreRepo, logger *zap.Logger) *FanScoreWorker {
	return &FanScoreWorker{conn: conn, rdb: rdb, repo: repo, logger: logger}
}

func (w *FanScoreWorker) Run(ctx context.Context) error {
	deliveries, err := w.conn.Consume("fan_score")
	if err != nil {
		return err
	}
	for delivery := range deliveries {
		if perr := w.processMessage(ctx, delivery.Body); perr != nil {
			requeue := !errors.Is(perr, errPoison)
			w.logger.Error("fan score message processing failed",
				zap.Error(perr), zap.Bool("requeue", requeue))
			if nerr := w.conn.Nack(delivery, requeue); nerr != nil {
				w.logger.Error("fan score nack failed", zap.Error(nerr))
			}
			continue
		}
		if aerr := w.conn.Ack(delivery); aerr != nil {
			w.logger.Error("fan score ack failed", zap.Error(aerr))
		}
	}
	return nil
}

func (w *FanScoreWorker) processMessage(ctx context.Context, body []byte) error {
	var meta queue.FanScoreMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return fmt.Errorf("%w: unmarshal fan score metadata: %v", errPoison, err)
	}
	if meta.UserID == "" {
		return fmt.Errorf("%w: empty user_id", errPoison)
	}

	dynamicScore := calculateDynamicScore(meta)
	key := fmt.Sprintf("user:score:%s:dynamic", meta.UserID)
	if err := w.rdb.Set(ctx, key, dynamicScore, dynamicScoreTTL).Err(); err != nil {
		return fmt.Errorf("set dynamic score in redis: %w", err)
	}

	// H4 FIX: blacklist from low FinalScore was self-referential (low score →
	// -30 dynamic → blacklist), conflating "new fan" with "bot". Bot detection
	// requires behavioral signals (request_interval, user_agent) not yet
	// present on FanScoreMetadata. Removed to prevent false-positive IP
	// blacklisting of legitimate low-score users. See TD-F2-01.
	if meta.FinalScore < 50 {
		w.logger.Warn("low fan score",
			zap.String("user_id", meta.UserID),
			zap.Int("score", meta.FinalScore))
	}
	return nil
}

// calculateDynamicScore returns a heuristic score in range [-50, 0].
// Deferred (TD-F2-01): extend queue.FanScoreMetadata with request_interval
// and user_agent so the faster-interval (-30), odd-hour 03:00-05:00 WIB (-20),
// and non-common User-Agent (-10) rules can run. Until that schema/ingress
// change lands, only the FinalScore guards apply.
func calculateDynamicScore(meta queue.FanScoreMetadata) int {
	switch {
	case meta.FinalScore < 30:
		return -30
	case meta.FinalScore < 50:
		return -20
	default:
		return 0
	}
}

type PgxBlacklistRepo struct {
	db Pool
}

func NewPgxBlacklistRepo(db Pool) *PgxBlacklistRepo {
	return &PgxBlacklistRepo{db: db}
}

func (r *PgxBlacklistRepo) InsertBlacklist(ctx context.Context, addedBy uuid.UUID, btype, value, reason string, expiresAt *time.Time) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO blacklist (type, value, reason, added_by, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (type, value) DO NOTHING`,
		btype, value, reason, addedBy, expiresAt)
	return err
}
