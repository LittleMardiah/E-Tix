package queue

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

var (
	// ErrEventNotOnSale is returned when the target event is not open for sale.
	ErrEventNotOnSale = errors.New("queue: event is not on sale")
	// ErrCaptchaInvalid is returned when the CAPTCHA solution is rejected.
	ErrCaptchaInvalid = errors.New("queue: captcha invalid")
)

// FASE 9 P9a-7 TD-F2-02 — CAPTCHA failed-attempt lockout.
// Policy mirrors the admin 2FA lockout (internal/admin/lockout.go, FASE 8):
// 3 failed verifications from one client IP inside a rolling 15m window →
// 15m lockout. Complements the existing 3/5min per-IP rate limit on
// POST /queue/verify-captcha (cmd/api/main.go).
const (
	// KeyCaptchaFails   = "captcha_fails:%s"   // failure counter
	KeyCaptchaFails   = "captcha_fails:%s"
	KeyCaptchaLockout = "captcha_lockout:%s" // lockout flag (set when fails >= max)

	// CaptchaMaxFails   = failures before lockout
	CaptchaMaxFails = 3
	// CaptchaFailsTTL = counter TTL (rolling window)
	CaptchaFailsTTL = 15 * time.Minute
	// CaptchaLockoutTTL = lockout duration
	CaptchaLockoutTTL = 15 * time.Minute
)

// QueueService orchestrates fan-score, queue-token and event-status lookups.
type QueueService struct {
	fanScore   *FanScoreService
	queueToken *QueueTokenService
	captcha    CaptchaService
	eventRepo  EventRepository
	rdb        *redis.Client
}

func NewQueueService(fanScore *FanScoreService, queueToken *QueueTokenService,
	captcha CaptchaService, eventRepo EventRepository, rdb *redis.Client) *QueueService {
	return &QueueService{
		fanScore:   fanScore,
		queueToken: queueToken,
		captcha:    captcha,
		eventRepo:  eventRepo,
		rdb:        rdb,
	}
}

// estimatedPosition derives a stable MVP queue position from the user id bytes.
func estimatedPosition(userID uuid.UUID) int {
	h := binary.BigEndian.Uint64(userID[8:])
	return int(h%1000) + 1
}

func (s *QueueService) GetStatus(ctx context.Context, userID uuid.UUID, eventID uuid.UUID) (*QueueStatusResponse, error) {
	eventStatus, err := s.eventRepo.GetEventStatus(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if eventStatus != "ON_SALE" {
		return nil, ErrEventNotOnSale
	}

	score, tier, err := s.fanScore.ComputeFinalScore(ctx, userID)
	if err != nil {
		return nil, err
	}

	// H5 FIX: gate queue token issuance on ChallengeRequiredScoreThreshold.
	// Below threshold → no token; client must POST /queue/verify-captcha first.
	// Without this, /queue/status trivially bypassed CAPTCHA for any user.
	challengeRequired := score < ChallengeRequiredScoreThreshold
	var token string
	if !challengeRequired {
		token, err = s.queueToken.Generate(userID, tier, score)
		if err != nil {
			return nil, err
		}
	}

	// Position estimation SIMPLE (MVP): deterministic hash-based dummy.
	position := estimatedPosition(userID)
	totalInQueue := position + 1000
	estimatedWait := position * 2

	return &QueueStatusResponse{
		Position:             position,
		TotalInQueue:         totalInQueue,
		EstimatedWaitSeconds: estimatedWait,
		Tier:                 tier,
		TierLabel:            s.fanScore.TierLabel(tier),
		LoyaltyScore:         score,
		QueueToken:           token,
		ChallengeRequired:    challengeRequired,
	}, nil
}

func (s *QueueService) VerifyCaptcha(ctx context.Context, userID uuid.UUID, req *VerifyCaptchaRequest) (*VerifyCaptchaResponse, error) {
	ok, err := s.captcha.Verify(ctx, req.ChallengeID, req.Solution)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrCaptchaInvalid
	}

	score, tier, err := s.fanScore.ComputeFinalScore(ctx, userID)
	if err != nil {
		return nil, err
	}

	token, err := s.queueToken.Generate(userID, tier, score)
	if err != nil {
		return nil, err
	}

	return &VerifyCaptchaResponse{Verified: true, QueueToken: token}, nil
}

// CaptchaLockoutState reports whether the client IP is currently locked out
// and how long the lockout still lasts. FASE 9 P9a-7 TD-F2-02.
func (s *QueueService) CaptchaLockoutState(ctx context.Context, clientIP string) (bool, time.Duration) {
	if s.rdb == nil || clientIP == "" {
		return false, 0
	}
	key := fmt.Sprintf(KeyCaptchaLockout, clientIP)
	ttl, err := s.rdb.TTL(ctx, key).Result()
	if err != nil || ttl <= 0 {
		return false, 0
	}
	return true, ttl
}

// RecordCaptchaFailure increments the failed-verification counter for the
// client IP and triggers the lockout once CaptchaMaxFails is reached.
// FASE 9 P9a-7 TD-F2-02.
func (s *QueueService) RecordCaptchaFailure(ctx context.Context, clientIP string) (int64, bool) {
	if s.rdb == nil || clientIP == "" {
		return 0, false
	}
	failsKey := fmt.Sprintf(KeyCaptchaFails, clientIP)
	fails, err := s.rdb.Incr(ctx, failsKey).Result()
	if err != nil {
		return 0, false
	}
	if fails == 1 {
		_ = s.rdb.Expire(ctx, failsKey, CaptchaFailsTTL).Err()
	}
	if fails >= CaptchaMaxFails {
		lockKey := fmt.Sprintf(KeyCaptchaLockout, clientIP)
		_ = s.rdb.Set(ctx, lockKey, "1", CaptchaLockoutTTL).Err()
		// The lockout flag takes over — drop the counter.
		_ = s.rdb.Del(ctx, failsKey).Err()
		return fails, true
	}
	return fails, false
}

// ClearCaptchaFailures resets the failed-verification counter after a
// successful verification. FASE 9 P9a-7 TD-F2-02.
func (s *QueueService) ClearCaptchaFailures(ctx context.Context, clientIP string) {
	if s.rdb == nil || clientIP == "" {
		return
	}
	_ = s.rdb.Del(ctx, fmt.Sprintf(KeyCaptchaFails, clientIP)).Err()
}
