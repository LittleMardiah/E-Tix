package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// QRStore is the Redis-backed implementation of ticket.QRStore.
type QRStore struct {
	client *goredis.Client
}

// NewQRStore constructs a QRStore adapter.
func NewQRStore(client *goredis.Client) *QRStore {
	return &QRStore{client: client}
}

// Get returns the cached QR payload, or ("", nil) on cache miss.
func (s *QRStore) Get(ctx context.Context, key string) (string, error) {
	v, err := s.client.Get(ctx, key).Result()
	if err == goredis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("qr store get: %w", err)
	}
	return v, nil
}

// Set stores a QR payload with the given TTL.
func (s *QRStore) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := s.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("qr store set: %w", err)
	}
	return nil
}
