package queue

import (
	"context"
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/LittleMardiah/E-Tix/backend/internal/auth"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const (
	queueTokenTTL        = 5 * time.Minute
	queueTokenUsedPrefix = "queue_token:used:"
	queueTokenType       = "queue"
)

// QueueTokenClaims is the JWT payload for single-use queue tokens.
type QueueTokenClaims struct {
	jwt.RegisteredClaims
	Tier       string `json:"tier"`
	FinalScore int    `json:"final_score"`
	TokenType  string `json:"token_type"`
}

// QueueTokenService issues and validates queue tokens (RS256).
type QueueTokenService struct {
	priv   *rsa.PrivateKey
	pub    *rsa.PublicKey
	issuer string
}

func NewQueueTokenService(privatePath, publicPath, issuer string) (*QueueTokenService, error) {
	if issuer == "" {
		return nil, fmt.Errorf("queue_token: issuer must not be empty")
	}
	priv, err := auth.LoadPrivateKey(privatePath)
	if err != nil {
		return nil, err
	}
	pub, err := auth.LoadPublicKey(publicPath)
	if err != nil {
		return nil, err
	}
	return &QueueTokenService{priv: priv, pub: pub, issuer: issuer}, nil
}

func (s *QueueTokenService) Generate(userID uuid.UUID, tier string, score int) (string, error) {
	now := time.Now()
	claims := QueueTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ID:        uuid.NewString(),
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(queueTokenTTL)),
		},
		Tier:       tier,
		FinalScore: score,
		TokenType:  queueTokenType,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return tok.SignedString(s.priv)
}

func (s *QueueTokenService) Validate(tokenString string) (*QueueTokenClaims, error) {
	claims := &QueueTokenClaims{}
	tok, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("queue_token: unexpected signing method %q", t.Header["alg"])
		}
		return s.pub, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(s.issuer))
	if err != nil {
		return nil, fmt.Errorf("queue_token: parse: %w", err)
	}
	if !tok.Valid {
		return nil, fmt.Errorf("queue_token: invalid token")
	}
	if claims.TokenType != queueTokenType {
		return nil, fmt.Errorf("queue_token: invalid token_type %q", claims.TokenType)
	}
	return claims, nil
}

// MarkUsed atomically records the jti as used. Returns true on first use,
// false if already consumed (replay).
func (s *QueueTokenService) MarkUsed(ctx context.Context, rdb *redis.Client, jti string) (bool, error) {
	return rdb.SetNX(ctx, queueTokenUsedPrefix+jti, "1", 300*time.Second)
}

// NewQueueTokenServiceWithKeys is the test-friendly constructor: it accepts
// pre-generated RSA keys directly, avoiding the need for PEM files on disk.
// Mirrors auth.NewJWTServiceWithKeys.
func NewQueueTokenServiceWithKeys(privateKey *rsa.PrivateKey, publicKey *rsa.PublicKey, issuer string) *QueueTokenService {
	if issuer == "" {
		issuer = "e-tix-flash-queue"
	}
	return &QueueTokenService{priv: privateKey, pub: publicKey, issuer: issuer}
}

// Refund clears the single-use marker for a jti. Callers use this when a
// downstream step fails before any durable side-effect occurred (e.g. Lua
// lock returned CONFLICT — no booking was created), so the caller can retry
// with the same token without re-queueing.
//
// H6 FIX: Without this, a user who loses a seat race also loses their queue
// token and must start the queue over.
func (s *QueueTokenService) Refund(ctx context.Context, rdb *redis.Client, jti string) error {
	return rdb.Del(ctx, queueTokenUsedPrefix+jti).Err()
}
