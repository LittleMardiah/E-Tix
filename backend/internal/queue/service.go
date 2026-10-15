package queue

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/google/uuid"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

var (
	// ErrEventNotOnSale is returned when the target event is not open for sale.
	ErrEventNotOnSale = errors.New("queue: event is not on sale")
	// ErrCaptchaInvalid is returned when the CAPTCHA solution is rejected.
	ErrCaptchaInvalid = errors.New("queue: captcha invalid")
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
