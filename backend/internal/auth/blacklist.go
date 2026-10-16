package auth

import (
	"context"
	"time"

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
	if err != nil {
		return false, err
	}
	return val != "", nil
}

// MarkRotated atomically claims a refresh token's jti as rotated.
//
// Returns (true, nil) if this caller is the FIRST to claim the jti.
// Returns (false, nil) if the jti was already claimed (replay).
// Returns (false, err) on Redis failure.
//
// Use this instead of IsBlacklisted+AddToBlacklist for refresh rotation:
// SET NX is atomic, closing the TOCTOU race where two concurrent refresh
// calls both pass the pre-check before either writes.
func (s *TokenBlacklistService) MarkRotated(ctx context.Context, jti string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = time.Second
	}
	return s.rdb.SetNX(ctx, blacklistPrefix+jti, "1", ttl)
}
