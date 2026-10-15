package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
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
		"layout_version": sec.LayoutVersion, // H3: captured at lock time
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

	// H1 FIX: read BOTH tickets (paid) and Redis bitmap (in-flight lock).
	// Precedence: TAKEN (paid ticket row) > LOCKED (Redis bit=1) > AVAILABLE.
	// Without the Redis read, seats held by other users in the 180s lock
	// window were reported AVAILABLE — contradicting API CONTRACT §8.3 and
	// LOGIC FLOW §199 which define LOCKED as "bit=1, TTL 3 min".
	taken, err := s.repo.GetTakenSeats(ctx, sectionID)
	if err != nil {
		return nil, err
	}
	takenMap := make(map[int]struct{}, len(taken))
	for _, p := range taken {
		takenMap[p] = struct{}{}
	}

	lockedBits, err := s.readLockedBitmap(ctx, eventID, sectionID, sec.LayoutVersion, sec.TotalSeats)
	if err != nil {
		// Best-effort: fall back to tickets-only (previous behavior) on
		// Redis failure rather than 500'ing the entire seat map.
		zap.L().Warn("booking: read bitmap failed; falling back to tickets-only",
			zap.String("section_id", sectionID.String()), zap.Error(err))
		lockedBits = nil
	}

	seats := make([]SeatStatus, 0, sec.TotalSeats)
	available := 0
	for pos := 1; pos <= sec.TotalSeats; pos++ {
		status := "AVAILABLE"
		if _, ok := takenMap[pos]; ok {
			status = "TAKEN"
		} else if lockedBits[pos] {
			status = "LOCKED"
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
	// H3 FIX: prefer layout_version captured at lock time (stored in
	// booking:{id} mapping). Fallback to current section version only
	// when mapping missing (pre-H3 locks / Redis eviction). Without this,
	// a seat-map redesign between lock and release targets the wrong key.
	layoutVersion, lerr := s.readLockLayoutVersion(ctx, bookingID)
	if lerr != nil {
		zap.L().Warn("booking: release: read lock layout_version failed",
			zap.String("booking_id", bookingID.String()), zap.Error(lerr))
	}
	sec, err := s.repo.GetSectionByID(ctx, bk.SectionID)
	if err != nil {
		return fmt.Errorf("booking: release: get section: %w", err)
	}
	if layoutVersion == 0 {
		layoutVersion = sec.LayoutVersion
	}
	key := fmt.Sprintf("seats:%s:%s:v%d", bk.EventID, bk.SectionID, layoutVersion)
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

// readLockedBitmap returns map[position]bool of Redis bitmap bits set to 1
// for the section's lock key. Position is 1-based (matches SeatStatus.Position);
// Redis offset is 0-based (pos-1).
//
// Redis SETBIT uses MSB-first ordering within each byte.
// Key absence is treated as all-zero.
func (s *BookingService) readLockedBitmap(ctx context.Context, eventID, sectionID uuid.UUID, layoutVersion, totalSeats int) (map[int]bool, error) {
	key := fmt.Sprintf("seats:%s:%s:v%d", eventID, sectionID, layoutVersion)
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return map[int]bool{}, nil
		}
		return nil, err
	}
	out := make(map[int]bool)
	for pos := 1; pos <= totalSeats; pos++ {
		if bitAt(raw, pos-1) == 1 {
			out[pos] = true
		}
	}
	return out, nil
}

// bitAt reads bit at idx (0-based) from a Redis-encoded bitmap byte slice.
// Redis SETBIT: bit 0 = MSB of byte 0.
func bitAt(raw []byte, idx int) int {
	byteIdx := idx / 8
	if byteIdx >= len(raw) {
		return 0
	}
	bitIdx := 7 - (idx % 8)
	if raw[byteIdx]&(1<<uint(bitIdx)) != 0 {
		return 1
	}
	return 0
}

// readLockLayoutVersion reads layout_version from the booking:{id} mapping
// captured at lock time. Returns (0, nil) on cache miss — callers fall back
// to the current section version.
func (s *BookingService) readLockLayoutVersion(ctx context.Context, bookingID uuid.UUID) (int, error) {
	raw, err := s.rdb.Get(ctx, "booking:"+bookingID.String()).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return 0, nil
		}
		return 0, err
	}
	var data struct {
		LayoutVersion int `json:"layout_version"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return 0, err
	}
	return data.LayoutVersion, nil
}
