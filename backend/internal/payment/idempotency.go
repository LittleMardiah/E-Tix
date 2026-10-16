package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// Idempotency storage:
//   L1 (Redis)     — fast lookup, TTL 24h, caches the full response body.
//   L2 (Postgres)  — payment_obligations.idempotency_key (UNIQUE), persistent.
//
// Hash formula (ROADMAP FASE 4 §3.4):
//   sha256(clientKey + ":" + userID + ":" + bookingID)
//
// The client-supplied X-Idempotency-Key is what prevents double-charge when
// the same caller retries. Combining with userID + bookingID prevents a
// malicious client from replaying another caller's key across bookings.

const (
	// IdempotencyL1Prefix is the Redis key prefix for L1 cache entries.
	IdempotencyL1Prefix = "idempotency:payment:"
	// IdempotencyL1TTL matches the API_CONTRACT §4.1 (24 hours).
	IdempotencyL1TTL = 24 * time.Hour
)

// ErrIdempotencyMiss is returned by LookupL2 when no obligation matches the key.
var ErrIdempotencyMiss = errors.New("payment: idempotency key not found")

// ComputeIdempotencyHash returns the hex-encoded SHA-256 hash of
// (clientKey + ":" + userID + ":" + bookingID).
func ComputeIdempotencyHash(clientKey string, userID, bookingID uuid.UUID) string {
	h := sha256.New()
	h.Write([]byte(clientKey))
	h.Write([]byte(":"))
	h.Write([]byte(userID.String()))
	h.Write([]byte(":"))
	h.Write([]byte(bookingID.String()))
	return hex.EncodeToString(h.Sum(nil))
}

// IdempotencyService coordinates L1 (Redis) and L2 (Postgres) lookups.
type IdempotencyService struct {
	rdb  *redis.Client
	repo PaymentObligationRepository
}

// NewIdempotencyService returns a new service bound to rdb and repo.
func NewIdempotencyService(rdb *redis.Client, repo PaymentObligationRepository) *IdempotencyService {
	return &IdempotencyService{rdb: rdb, repo: repo}
}

// LookupL1 returns (cachedBody, true, nil) on hit, ("", false, nil) on miss.
func (s *IdempotencyService) LookupL1(ctx context.Context, hash string) ([]byte, bool, error) {
	body, err := s.rdb.GetString(ctx, IdempotencyL1Prefix+hash)
	if err != nil {
		return nil, false, err
	}
	if body == "" {
		return nil, false, nil
	}
	return []byte(body), true, nil
}

// StoreL1 writes a response body to Redis with the standard 24h TTL.
func (s *IdempotencyService) StoreL1(ctx context.Context, hash string, body []byte) error {
	return s.rdb.Set(ctx, IdempotencyL1Prefix+hash, string(body), IdempotencyL1TTL).Err()
}

// LookupL2 falls back to Postgres. Returns (nil, nil) if no match (miss),
// and the obligation on hit. A nil-nil return means "safe to proceed".
func (s *IdempotencyService) LookupL2(ctx context.Context, hash string) (*PaymentObligation, error) {
	o, err := s.repo.GetByIdempotencyKey(ctx, hash)
	if err != nil {
		return nil, err
	}
	return o, nil // nil, nil == miss
}
