package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const lockTTLSeconds = 180

// DelayedPublisher abstracts the delayed message publisher for testability.
type DelayedPublisher interface {
	PublishDelayed(ctx context.Context, queueName string, payload []byte, delay time.Duration) error
}

type BookingService struct {
	repo       BookingRepository
	rdb        *redis.Client
	scripts    *LuaScripts
	queueToken *queue.QueueTokenService
	queueConn  DelayedPublisher
}

func NewBookingService(repo BookingRepository, rdb *redis.Client, scripts *LuaScripts, queueToken *queue.QueueTokenService, queueConn DelayedPublisher) *BookingService {
	return &BookingService{repo: repo, rdb: rdb, scripts: scripts, queueToken: queueToken, queueConn: queueConn}
}

func (s *BookingService) LockSeats(ctx context.Context, userID, eventID, sectionID uuid.UUID, req *LockSeatsRequest, queueToken string) (*LockSeatsResponse, error) {
	if len(req.SeatPositions) < 1 {
		return nil, ErrEmptySeatPositions
	}
	if len(req.SeatPositions) > MaxGroupSeats {
		return nil, ErrMaxGroupSeatsExceeded
	}
	if req.PaymentMode != "SPLIT" && req.PaymentMode != "BULK" {
		return nil, ErrInvalidPaymentMode
	}

	claims, err := s.queueToken.Validate(queueToken)
	if err != nil {
		return nil, ErrQueueTokenInvalid
	}
	firstUse, err := s.queueToken.MarkUsed(ctx, s.rdb, claims.ID)
	if err != nil {
		return nil, fmt.Errorf("booking: mark queue token used: %w", err)
	}
	if !firstUse {
		return nil, ErrTokenAlreadyUsed
	}

	sec, err := s.repo.GetSectionByID(ctx, sectionID)
	if err != nil {
		return nil, err
	}
	if sec.EventID != eventID {
		return nil, ErrSectionNotFound
	}
	for _, pos := range req.SeatPositions {
		if pos < 1 || pos > sec.TotalSeats {
			return nil, ErrSeatOutOfRange
		}
	}

	bookingID := uuid.New()

	priceDec, err := decimal.NewFromString(sec.Price)
	if err != nil {
		return nil, fmt.Errorf("booking: parse section price %q: %w", sec.Price, err)
	}
	totalAmount := priceDec.Mul(decimal.NewFromInt(int64(len(req.SeatPositions))))

	key := fmt.Sprintf("seats:%s:%s:v%d", eventID, sectionID, sec.LayoutVersion)

	seatsJSON, err := json.Marshal(req.SeatPositions)
	if err != nil {
		return nil, fmt.Errorf("booking: marshal seat positions: %w", err)
	}
	bookingData, err := json.Marshal(map[string]interface{}{
		"booking_id":     bookingID.String(),
		"user_id":        userID.String(),
		"seat_positions": req.SeatPositions,
		"total_amount":   totalAmount.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("booking: marshal booking data: %w", err)
	}

	res, err := s.rdb.EvalScript(ctx, s.scripts.Lock, []string{key},
		string(seatsJSON), bookingID.String(), string(bookingData))
	if err != nil {
		return nil, fmt.Errorf("booking: lock seats: %w", err)
	}
	var lockResult struct {
		Status    string `json:"status"`
		Position  int    `json:"position"`
		BookingID string `json:"booking_id"`
	}
	raw, err := lockResultRaw(res)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &lockResult); err != nil {
		return nil, fmt.Errorf("booking: parse lock result: %w", err)
	}
	if lockResult.Status == "CONFLICT" {
		return nil, ErrSeatsNotAvailable
	}

	booking := &Booking{
		ID:            bookingID,
		EventID:       eventID,
		SectionID:     sectionID,
		LeaderUserID:  userID,
		SeatPositions: req.SeatPositions,
		TotalSeats:    len(req.SeatPositions),
		TotalAmount:   totalAmount.String(),
		PaymentMode:   req.PaymentMode,
		PaymentStatus: "PENDING",
		ExpiresAt:     time.Now().Add(lockTTLSeconds * time.Second),
	}

	if err := s.repo.CreateBooking(ctx, booking); err != nil {
		if _, relErr := s.rdb.EvalScript(ctx, s.scripts.Release, []string{key},
			string(seatsJSON), bookingID.String()); relErr != nil {
			zap.L().Warn("booking: compensating seat release failed",
				zap.String("booking_id", bookingID.String()),
				zap.Error(relErr))
		}
		return nil, err
	}

	payload, err := json.Marshal(map[string]string{
		"type":       "RELEASE_SEAT_CHECK",
		"booking_id": bookingID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("booking: marshal delayed payload: %w", err)
	}
	if err := s.queueConn.PublishDelayed(ctx, "release_seat_check", payload, lockTTLSeconds*time.Second); err != nil {
		zap.L().Warn("booking: publish release_seat_check failed",
			zap.String("booking_id", bookingID.String()),
			zap.Error(err))
	}

	return &LockSeatsResponse{
		BookingID:     bookingID.String(),
		EventID:       eventID.String(),
		SectionID:     sectionID.String(),
		SeatPositions: req.SeatPositions,
		TotalSeats:    len(req.SeatPositions),
		TotalAmount:   totalAmount.String(),
		PaymentMode:   req.PaymentMode,
		PaymentStatus: "PENDING",
		ExpiresAt:     booking.ExpiresAt.UTC().Format(time.RFC3339),
	}, nil
}

func lockResultRaw(res interface{}) (string, error) {
	switch v := res.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("booking: unexpected lock result type %T", res)
	}
}

func (s *BookingService) GetSeatAvailability(ctx context.Context, eventID, sectionID uuid.UUID) (*SeatAvailabilityResponse, error) {
	sec, err := s.repo.GetSectionByID(ctx, sectionID)
	if err != nil {
		return nil, err
	}
	if sec.EventID != eventID {
		return nil, ErrSectionNotFound
	}

	taken, err := s.repo.GetTakenSeats(ctx, sectionID)
	if err != nil {
		return nil, err
	}
	takenMap := make(map[int]struct{}, len(taken))
	for _, p := range taken {
		takenMap[p] = struct{}{}
	}

	seats := make([]SeatStatus, 0, sec.TotalSeats)
	available := 0
	for pos := 1; pos <= sec.TotalSeats; pos++ {
		status := "AVAILABLE"
		if _, ok := takenMap[pos]; ok {
			status = "TAKEN"
		} else {
			available++
		}
		seats = append(seats, SeatStatus{Position: pos, Status: status})
	}

	return &SeatAvailabilityResponse{
		EventID:        eventID.String(),
		SectionID:      sectionID.String(),
		SectionName:    sec.Name,
		LayoutVersion:  sec.LayoutVersion,
		TotalSeats:     sec.TotalSeats,
		AvailableSeats: available,
		Seats:          seats,
		LastUpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

// ReleaseSeatsForExpiry releases a booking's seat bits in Redis (granular
// SETBIT 0 per seat, never overwriting) and deletes the booking:{id}
// mapping. Idempotent — safe to call multiple times for the same booking.
//
// Used by the BookingExpiryWorker (FASE 4) when a booking's TTL expires
// without full payment.
func (s *BookingService) ReleaseSeatsForExpiry(ctx context.Context, bookingID uuid.UUID) error {
	bk, err := s.repo.GetByID(ctx, bookingID)
	if err != nil {
		return fmt.Errorf("booking: release: get by id: %w", err)
	}
	sec, err := s.repo.GetSectionByID(ctx, bk.SectionID)
	if err != nil {
		return fmt.Errorf("booking: release: get section: %w", err)
	}
	key := fmt.Sprintf("seats:%s:%s:v%d", bk.EventID, bk.SectionID, sec.LayoutVersion)
	seatsJSON, err := json.Marshal(bk.SeatPositions)
	if err != nil {
		return fmt.Errorf("booking: release: marshal seats: %w", err)
	}
	if _, err := s.rdb.EvalScript(ctx, s.scripts.Release, []string{key},
		string(seatsJSON), bookingID.String()); err != nil {
		return fmt.Errorf("booking: release: eval script: %w", err)
	}
	return nil
}
