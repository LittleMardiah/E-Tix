package queue

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/LittleMardiah/E-Tix/backend/internal/auth"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	baseScoreCacheTTL = 24 * time.Hour
	defaultBaseScore  = 50
)

type FanScoreService struct {
	rdb      *redis.Client
	userRepo auth.UserRepository
}

func NewFanScoreService(rdb *redis.Client, userRepo auth.UserRepository) *FanScoreService {
	return &FanScoreService{rdb: rdb, userRepo: userRepo}
}

func baseScoreKey(userID uuid.UUID) string {
	return "user:score:" + userID.String() + ":base"
}

func dynamicScoreKey(userID uuid.UUID) string {
	return "user:score:" + userID.String() + ":dynamic"
}

func (s *FanScoreService) GetBaseScore(ctx context.Context, userID uuid.UUID) (int, error) {
	if s.rdb != nil {
		val, err := s.rdb.GetString(ctx, baseScoreKey(userID))
		if err == nil {
			if score, perr := strconv.Atoi(val); perr == nil {
				return score, nil
			}
		} else if !errors.Is(err, goredis.Nil) {
			// H10 FIX: Redis error (not cache-miss) → fall through to DB.
			// Previous behavior returned error and turned Redis outages
			// into ingress 500s.
			zap.L().Warn("fan_score: redis read failed, falling back to DB",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
	}

	user, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		return 0, err
	}

	score := user.BaseScore
	if score <= 0 {
		score = defaultBaseScore
	}

	if s.rdb != nil {
		_ = s.rdb.Set(ctx, baseScoreKey(userID), strconv.Itoa(score), baseScoreCacheTTL).Err()
	}

	return score, nil
}

func (s *FanScoreService) GetDynamicScore(ctx context.Context, userID uuid.UUID) (int, error) {
	if s.rdb == nil {
		return 0, nil
	}

	val, err := s.rdb.GetString(ctx, dynamicScoreKey(userID))
	if err != nil {
		return 0, err
	}
	if val == "" {
		// Cache miss — GetString returns ("", nil) on miss (see client.go).
		return 0, nil
	}

	score, perr := strconv.Atoi(val)
	if perr != nil {
		// H13 FIX: surface corrupted cache entries instead of silently
		// treating them as 0.
		zap.L().Warn("fan_score: corrupt dynamic score in redis",
			zap.String("user_id", userID.String()),
			zap.String("raw", val), zap.Error(perr))
		return 0, nil
	}

	return score, nil
}

func (s *FanScoreService) ComputeFinalScore(ctx context.Context, userID uuid.UUID) (int, string, error) {
	base, err := s.GetBaseScore(ctx, userID)
	if err != nil {
		return 0, "", err
	}

	dynamic, err := s.GetDynamicScore(ctx, userID)
	if err != nil {
		return 0, "", err
	}

	finalScore := base + dynamic

	var tier string
	switch {
	case finalScore > 90:
		tier = TierVIP
	case finalScore >= 70:
		tier = TierRegular
	default:
		tier = TierLow
	}

	return finalScore, tier, nil
}

func (s *FanScoreService) TierLabel(tier string) string {
	switch tier {
	case TierVIP:
		return "VIP Fan"
	case TierRegular:
		return "Regular Fan"
	case TierLow:
		return "Low Priority"
	default:
		return "Unknown"
	}
}
