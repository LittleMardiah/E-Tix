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
	"github.com/sony/gobreaker"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const lockTTLSeconds = 180

// lockKeyTTLSeconds is the TTL of the Redis keys written by the lock script
// (per-seat owner key + booking:{id} mapping).
//
// FIX P9a-7 TD-F3-02: the two keys were hard-coded to 7 days while the actual
// seat hold is lockTTLSeconds (180s). A key that outlives the booking by
// days is garbage (and the release path could not verify ownership once the
// owner key was gone). The keys now expire shortly after the hold: 180s hold
// + 120s grace for the expiry worker to release the seat. Both keys MUST
// share this TTL — the release script needs the owner key and the
// layout_version snapshot at the same time.
const lockKeyTTLSeconds = lockTTLSeconds + 120 // 300s

// DelayedPublisher abstracts the delayed message publisher for testability.
type DelayedPublisher interface {
	PublishDelayed(ctx context.Context, queueName string, payload []byte, delay time.Duration) error
}

// RedisCircuitBreakerName identifies the booking Redis breaker in logs and
// metrics (P9a-12 TD-F9-17).
const RedisCircuitBreakerName = "booking-redis"

// Circuit breaker tuning for the booking Redis calls.
//
// P9a-12 TD-F9-17 / LOGIC FLOW §9.4: when Redis is down for more than
// ~100ms the breaker must trip so the seat-lock path degrades gracefully
// (requests are parked in the durable DB queue) instead of piling up
// connection timeouts.
//
//   - ReadyToTrip trips the breaker after more than 3 consecutive failures.
//   - Interval resets the failure counters every 5s (closed state).
//   - Timeout is the cool-down before the breaker goes half-open (30s).
//   - MaxRequests is the number of probes allowed while half-open.
const (
	cbMaxRequests         uint32        = 5
	cbInterval            time.Duration = 5 * time.Second
	cbTimeout             time.Duration = 30 * time.Second
	cbConsecutiveFailures uint32        = 3
)

type BookingService struct {
	repo       BookingRepository
	rdb        *redis.Client
	scripts    *LuaScripts
	queueToken *queue.QueueTokenService
	queueConn  DelayedPublisher

	// redisCB guards every Redis write of the seat-lock path (P9a-12
	// TD-F9-17). When it is open the service runs in degraded mode and
	// parks the request in seatQueue.
	redisCB *gobreaker.CircuitBreaker

	// seatQueue is the durable FIFO queue used in degraded mode. It may be
	// nil (no queue configured): the request is then dropped with an error
	// log instead of being silently lost.
	seatQueue SeatLockQueue
}

func NewBookingService(repo BookingRepository, rdb *redis.Client, scripts *LuaScripts, queueToken *queue.QueueTokenService, queueConn DelayedPublisher) *BookingService {
	return NewBookingServiceWithCircuitBreaker(repo, rdb, scripts, queueToken, queueConn, nil, nil)
}

// NewBookingServiceWithCircuitBreaker builds a BookingService with an
// explicit circuit breaker and seat-lock queue.
//
// P9a-12 TD-F9-17: a nil cb falls back to DefaultRedisCircuitBreaker, a nil
// seatQueue disables the degraded-mode parking (requests are then rejected
// with ErrServiceUnavailable and logged).
func NewBookingServiceWithCircuitBreaker(repo BookingRepository, rdb *redis.Client, scripts *LuaScripts, queueToken *queue.QueueTokenService, queueConn DelayedPublisher, cb *gobreaker.CircuitBreaker, seatQueue SeatLockQueue) *BookingService {
	if cb == nil {
		cb = DefaultRedisCircuitBreaker()
	}
	return &BookingService{
		repo:       repo,
		rdb:        rdb,
		scripts:    scripts,
		queueToken: queueToken,
		queueConn:  queueConn,
		redisCB:    cb,
		seatQueue:  seatQueue,
	}
}

// DefaultRedisCircuitBreaker returns the circuit breaker protecting the
// booking Redis calls (P9a-12 TD-F9-17).
func DefaultRedisCircuitBreaker() *gobreaker.CircuitBreaker {
	return NewRedisCircuitBreakerWith(0, 0, 0)
}

// NewRedisCircuitBreakerWith builds a breaker with explicit tuning.
// Non-positive values fall back to the package defaults.
func NewRedisCircuitBreakerWith(interval, timeout time.Duration, maxRequests uint32) *gobreaker.CircuitBreaker {
	if interval <= 0 {
		interval = cbInterval
	}
	if timeout <= 0 {
		timeout = cbTimeout
	}
	if maxRequests == 0 {
		maxRequests = cbMaxRequests
	}
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        RedisCircuitBreakerName,
		MaxRequests: maxRequests,
		Interval:    interval,
		Timeout:     timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > cbConsecutiveFailures
		},
	})
}

// RedisCircuitBreaker exposes the breaker so the DB queue sync worker can
// drain the parked requests only after Redis recovered (LOGIC FLOW §9.4).
func (s *BookingService) RedisCircuitBreaker() *gobreaker.CircuitBreaker {
	return s.redisCB
}

// RedisCircuitBreakerOpen reports whether the breaker currently rejects
// requests (degraded mode active).
func (s *BookingService) RedisCircuitBreakerOpen() bool {
	return s.redisCB.State() == gobreaker.StateOpen
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

	// H7 FIX: reject duplicate seat positions before locking.
	// Without this, [5,5] passes conflict check (both read bit 0),
	// then charges twice for one bit (total_amount = 2× price).
	seen := make(map[int]struct{}, len(req.SeatPositions))
	for _, pos := range req.SeatPositions {
		if _, dup := seen[pos]; dup {
			return nil, ErrDuplicateSeatPosition
		}
		seen[pos] = struct{}{}
	}

	claims, err := s.queueToken.Validate(queueToken)
	if err != nil {
		return nil, ErrQueueTokenInvalid
	}
	// P9a-12 TD-F9-17: the single-use marker is a Redis write as well, so it
	// runs through the breaker. While the breaker is open the request is
	// parked in the durable DB queue instead of failing hard.
	firstUse, err := s.markTokenUsed(ctx, claims.ID)
	if err != nil {
		if errors.Is(err, ErrServiceUnavailable) {
			s.parkSeatLock(ctx, userID, eventID, sectionID, req, claims.ID, "")
		}
		return nil, err
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

	res, err := s.lockSeats(ctx, key, seatsJSON, bookingID, bookingData)
	if err != nil {
		if errors.Is(err, ErrServiceUnavailable) {
			// Degraded mode: park the request in the durable DB queue so it
			// can be replayed once Redis recovered (LOGIC FLOW §9.4).
			s.parkSeatLock(ctx, userID, eventID, sectionID, req, claims.ID, totalAmount.String())
		}
		return nil, err
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
		// H6 FIX: refund queue token. Token was consumed by MarkUsed earlier;
		// on CONFLICT no booking exists, so the marker can be safely removed.
		// Caller can retry with the same token (e.g. pick a different seat)
		// without re-queueing.
		if refundErr := s.queueToken.Refund(ctx, s.rdb, claims.ID); refundErr != nil {
			zap.L().Warn("booking: refund queue token failed",
				zap.String("jti", claims.ID), zap.Error(refundErr))
		}
		return nil, ErrSeatsNotAvailable
	}
	// H8 FIX: require explicit SUCCESS from Lua; any other status (e.g. "") must not fall through to CreateBooking.
	if lockResult.Status != "SUCCESS" {
		return nil, fmt.Errorf("booking: unexpected lock status %q", lockResult.Status)
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

	// P9a-12 TD-F9-17: this booking supersedes a parked request with the same
	// queue token (the client retried successfully once Redis recovered), so
	// the parked copy is dropped instead of being replayed later.
	if s.seatQueue != nil {
		if discardErr := s.seatQueue.DiscardByToken(ctx, claims.ID); discardErr != nil {
			zap.L().Warn("booking: discard superseded queued seat lock failed",
				zap.String("token_id", claims.ID),
				zap.Error(discardErr))
		}
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

// lockSeats runs the seat-lock Lua script through the Redis circuit breaker.
//
// P9a-12 TD-F9-17: while the breaker is closed the script runs as usual and
// a Redis error is reported as a normal failure (the breaker counts it). Once
// the breaker is open the request is parked in the durable DB queue and the
// caller receives ErrServiceUnavailable (HTTP 503) instead of a timeout.
func (s *BookingService) lockSeats(ctx context.Context, key string, seatsJSON []byte, bookingID uuid.UUID, bookingData []byte) (interface{}, error) {
	res, err := s.redisCB.Execute(func() (interface{}, error) {
		return s.rdb.EvalScript(ctx, s.scripts.Lock, []string{key},
			string(seatsJSON), bookingID.String(), string(bookingData), lockKeyTTLSeconds)
	})
	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return nil, ErrServiceUnavailable
		}
		return nil, fmt.Errorf("booking: lock seats: %w", err)
	}
	return res, nil
}

// markTokenUsed marks the queue token as used through the Redis circuit
// breaker.
//
// P9a-12 TD-F9-17: the marker is a Redis write, so a Redis outage must not
// surface as a hard failure — the caller parks the request in the DB queue.
func (s *BookingService) markTokenUsed(ctx context.Context, tokenID string) (bool, error) {
	res, err := s.redisCB.Execute(func() (interface{}, error) {
		return s.queueToken.MarkUsed(ctx, s.rdb, tokenID)
	})
	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return false, ErrServiceUnavailable
		}
		return false, fmt.Errorf("booking: mark queue token used: %w", err)
	}
	firstUse, ok := res.(bool)
	if !ok {
		return false, fmt.Errorf("booking: unexpected queue token result type %T", res)
	}
	return firstUse, nil
}

// parkSeatLock stores the seat-lock request in the durable DB queue so it can
// be replayed once Redis recovered (degraded mode, LOGIC FLOW §9.4).
//
// P9a-12 TD-F9-17: the queue is the PostgreSQL counterpart of the Redis seat
// bitmap — it survives a Redis outage and is drained by the
// DBQueueSyncWorker. The queue token is intentionally NOT refunded here: the
// parked request owns the booking and the queue dedups per token, so a retry
// with the same token cannot park the request twice.
func (s *BookingService) parkSeatLock(ctx context.Context, userID, eventID, sectionID uuid.UUID, req *LockSeatsRequest, tokenID, totalAmount string) {
	if s.seatQueue == nil {
		zap.L().Error("booking: degraded mode without seat-lock queue, request dropped",
			zap.String("user_id", userID.String()),
			zap.String("event_id", eventID.String()),
			zap.String("section_id", sectionID.String()))
		return
	}
	if totalAmount == "" {
		// The outage was detected before the section was read: compute the
		// amount now (the database is up, only Redis is down).
		totalAmount = s.estimateTotalAmount(ctx, sectionID, len(req.SeatPositions))
	}
	entry := QueuedSeatLock{
		ID:            uuid.New(),
		TokenID:       tokenID,
		EventID:       eventID,
		SectionID:     sectionID,
		LeaderUserID:  userID,
		SeatPositions: append([]int(nil), req.SeatPositions...),
		TotalSeats:    len(req.SeatPositions),
		TotalAmount:   totalAmount,
		PaymentMode:   req.PaymentMode,
		ExpiresAt:     time.Now().Add(lockTTLSeconds * time.Second),
	}
	if err := s.seatQueue.Enqueue(ctx, entry); err != nil {
		zap.L().Error("booking: enqueue seat lock failed",
			zap.String("token_id", tokenID),
			zap.String("user_id", userID.String()),
			zap.String("event_id", eventID.String()),
			zap.String("section_id", sectionID.String()),
			zap.Error(err))
		return
	}
	zap.L().Warn("booking: degraded mode, seat lock queued",
		zap.String("queue_id", entry.ID.String()),
		zap.String("token_id", tokenID),
		zap.String("user_id", userID.String()),
		zap.String("event_id", eventID.String()),
		zap.String("section_id", sectionID.String()),
		zap.Int("seats", len(req.SeatPositions)))
}

// estimateTotalAmount computes the price of a seat selection from the
// section row. It is used when a request is parked before the section was
// read (Redis outage detected on the queue-token marker).
func (s *BookingService) estimateTotalAmount(ctx context.Context, sectionID uuid.UUID, seats int) string {
	sec, err := s.repo.GetSectionByID(ctx, sectionID)
	if err != nil {
		zap.L().Warn("booking: section price unavailable for parked request",
			zap.String("section_id", sectionID.String()),
			zap.Error(err))
		return "0"
	}
	priceDec, err := decimal.NewFromString(sec.Price)
	if err != nil {
		zap.L().Warn("booking: unparsable section price for parked request",
			zap.String("section_id", sectionID.String()),
			zap.String("price", sec.Price),
			zap.Error(err))
		return "0"
	}
	return priceDec.Mul(decimal.NewFromInt(int64(seats))).String()
}

// LockQueuedSeats replays a parked seat-lock request once Redis recovered.
//
// P9a-12 TD-F9-17: called by the DBQueueSyncWorker when the Redis circuit
// breaker closed again. The replay uses the same atomic Lua lock as the
// direct path, so the buyer either gets the seats or the request is reported
// as failed (never silently lost).
func (s *BookingService) LockQueuedSeats(ctx context.Context, req QueuedSeatLock) error {
	if !req.ExpiresAt.IsZero() && time.Now().After(req.ExpiresAt) {
		return ErrQueuedSeatLockExpired
	}
	if len(req.SeatPositions) == 0 {
		return ErrEmptySeatPositions
	}

	sec, err := s.repo.GetSectionByID(ctx, req.SectionID)
	if err != nil {
		return err
	}
	if sec.EventID != req.EventID {
		return ErrSectionNotFound
	}
	for _, pos := range req.SeatPositions {
		if pos < 1 || pos > sec.TotalSeats {
			return ErrSeatOutOfRange
		}
	}

	priceDec, err := decimal.NewFromString(sec.Price)
	if err != nil {
		return fmt.Errorf("booking: parse section price %q: %w", sec.Price, err)
	}
	totalAmount := priceDec.Mul(decimal.NewFromInt(int64(len(req.SeatPositions))))

	bookingID := uuid.New()
	key := fmt.Sprintf("seats:%s:%s:v%d", req.EventID, req.SectionID, sec.LayoutVersion)

	seatsJSON, err := json.Marshal(req.SeatPositions)
	if err != nil {
		return fmt.Errorf("booking: marshal seat positions: %w", err)
	}
	bookingData, err := json.Marshal(map[string]interface{}{
		"booking_id":     bookingID.String(),
		"user_id":        req.LeaderUserID.String(),
		"seat_positions": req.SeatPositions,
		"total_amount":   totalAmount.String(),
		"layout_version": sec.LayoutVersion,
	})
	if err != nil {
		return fmt.Errorf("booking: marshal booking data: %w", err)
	}

	// The breaker is closed again (the worker only drains in that case), so
	// the raw script call is enough. A Redis error keeps the request queued.
	res, err := s.rdb.EvalScript(ctx, s.scripts.Lock, []string{key},
		string(seatsJSON), bookingID.String(), string(bookingData), lockKeyTTLSeconds)
	if err != nil {
		return fmt.Errorf("booking: lock queued seats: %w", err)
	}
	var lockResult struct {
		Status    string `json:"status"`
		Position  int    `json:"position"`
		BookingID string `json:"booking_id"`
	}
	raw, err := lockResultRaw(res)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), &lockResult); err != nil {
		return fmt.Errorf("booking: parse lock result: %w", err)
	}
	if lockResult.Status == "CONFLICT" {
		return ErrSeatsNotAvailable
	}
	if lockResult.Status != "SUCCESS" {
		return fmt.Errorf("booking: unexpected lock status %q", lockResult.Status)
	}

	booking := &Booking{
		ID:            bookingID,
		EventID:       req.EventID,
		SectionID:     req.SectionID,
		LeaderUserID:  req.LeaderUserID,
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
		return err
	}

	payload, err := json.Marshal(map[string]string{
		"type":       "RELEASE_SEAT_CHECK",
		"booking_id": bookingID.String(),
	})
	if err != nil {
		return fmt.Errorf("booking: marshal delayed payload: %w", err)
	}
	if err := s.queueConn.PublishDelayed(ctx, "release_seat_check", payload, lockTTLSeconds*time.Second); err != nil {
		zap.L().Warn("booking: publish release_seat_check failed",
			zap.String("booking_id", bookingID.String()),
			zap.Error(err))
	}

	zap.L().Info("booking: queued seat lock replayed",
		zap.String("booking_id", bookingID.String()),
		zap.String("token_id", req.TokenID),
		zap.String("user_id", req.LeaderUserID.String()))
	return nil
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
