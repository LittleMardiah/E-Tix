package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const dynamicScoreTTL = 24 * time.Hour

type FanScoreRepo interface {
	InsertBlacklist(ctx context.Context, addedBy uuid.UUID, btype, value, reason string, expiresAt *time.Time) error
}

type FanScoreWorker struct {
	conn   *queue.Connection
	rdb    *redis.Client
	repo   FanScoreRepo
	logger *zap.Logger
}

func NewFanScoreWorker(conn *queue.Connection, rdb *redis.Client, repo FanScoreRepo, logger *zap.Logger) *FanScoreWorker {
	return &FanScoreWorker{conn: conn, rdb: rdb, repo: repo, logger: logger}
}

func (w *FanScoreWorker) Run(ctx context.Context) error {
	deliveries, err := w.conn.Consume("fan_score")
	if err != nil {
		return err
	}
	for delivery := range deliveries {
		if perr := w.processMessage(ctx, delivery.Body); perr != nil {
			w.logger.Error("fan score message processing failed", zap.Error(perr))
			if nerr := w.conn.Nack(delivery, false); nerr != nil {
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
		return fmt.Errorf("unmarshal fan score metadata: %w", err)
	}

	dynamicScore := calculateDynamicScore(meta)
	key := fmt.Sprintf("user:score:%s:dynamic", meta.UserID)
	if err := w.rdb.Set(ctx, key, dynamicScore, dynamicScoreTTL).Err(); err != nil {
		return fmt.Errorf("set dynamic score in redis: %w", err)
	}

	if dynamicScore <= -30 {
		if err := w.repo.InsertBlacklist(ctx, systemUserID(), "ip", meta.IP, "BOT_DETECTED", nil); err != nil {
			return fmt.Errorf("insert bot blacklist: %w", err)
		}
		w.logger.Warn("bot ip blacklisted", zap.String("ip", meta.IP))
	} else if meta.FinalScore < 50 {
		w.logger.Warn("low fan score", zap.String("user_id", meta.UserID), zap.Int("score", meta.FinalScore))
	}
	return nil
}

// calculateDynamicScore returns a heuristic score in range [-50, 0].
// TODO: extend queue.FanScoreMetadata with request_interval + user_agent
// fields so the faster-interval (-30), odd-hour 03:00-05:00 WIB (-20), and
// non-common User-Agent (-10) rules can run; until then only the
// FinalScore guards apply.
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

// systemUserID resolves SYSTEM_USER_UUID (config env source); falls back to
// the seeded system user from migration 001.
func systemUserID() uuid.UUID {
	if v := os.Getenv("SYSTEM_USER_UUID"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			return id
		}
	}
	return uuid.MustParse("00000000-0000-0000-0000-000000000000")
}

type PgxBlacklistRepo struct {
	db *pgxpool.Pool
}

func NewPgxBlacklistRepo(db *pgxpool.Pool) *PgxBlacklistRepo {
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
