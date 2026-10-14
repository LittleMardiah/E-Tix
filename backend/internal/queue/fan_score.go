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
			return 0, err
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
		if errors.Is(err, goredis.Nil) {
			return 0, nil
		}
		return 0, err
	}

	score, perr := strconv.Atoi(val)
	if perr != nil {
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
