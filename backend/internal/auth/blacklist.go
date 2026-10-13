package auth

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const blacklistPrefix = "jwt_blacklist:"

type TokenBlacklistService struct {
	rdb *redis.Client
}

func NewTokenBlacklistService(rdb *redis.Client) *TokenBlacklistService {
	return &TokenBlacklistService{rdb: rdb}
}

func (s *TokenBlacklistService) AddToBlacklist(ctx context.Context, jti string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = time.Second
	}
	return s.rdb.Set(ctx, blacklistPrefix+jti, "1", ttl).Err()
}

func (s *TokenBlacklistService) IsBlacklisted(ctx context.Context, jti string) (bool, error) {
	val, err := s.rdb.GetString(ctx, blacklistPrefix+jti)
	if errors.Is(err, goredis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return val != "", nil
}
