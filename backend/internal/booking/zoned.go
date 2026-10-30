package booking

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

//go:embed scripts/decr_quota.lua
var decrQuotaLua string

var ErrInsufficientQuota = errors.New("booking: insufficient zone quota")
var ErrQuotaNotInitialized = errors.New("booking: zone quota not initialized")

type ZonedBookingRequest struct {
	EventID  uuid.UUID `json:"event_id"`
	ZoneName string    `json:"zone_name"`
	Quantity int       `json:"quantity"`
	UserID   uuid.UUID `json:"user_id"`
}

type ZonedBookingResponse struct {
	EventID  string `json:"event_id"`
	ZoneName string `json:"zone_name"`
	Quantity int    `json:"quantity"`
	UserID   string `json:"user_id"`
	Status   string `json:"status"`
}

type zonedBookingMessage struct {
	BookingID string    `json:"booking_id"`
	SectionID string    `json:"section_id"`
	EventID   string    `json:"event_id"`
	ZoneName  string    `json:"zone_name"`
	Quantity  int       `json:"quantity"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

type ZonedService struct {
	rdb  *redis.Client
	conn *amqp.Connection
	db   *pgxpool.Pool
}

func NewZonedService(rdb *redis.Client, conn *amqp.Connection, db *pgxpool.Pool) *ZonedService {
	return &ZonedService{rdb: rdb, conn: conn, db: db}
}

func (s *ZonedService) CheckoutZoned(ctx context.Context, req ZonedBookingRequest) (*ZonedBookingResponse, error) {
	if req.Quantity <= 0 {
		return nil, errors.New("booking: quantity must be positive")
	}
	if req.ZoneName == "" {
		return nil, errors.New("booking: zone_name is required")
	}
	if req.EventID == uuid.Nil {
		return nil, errors.New("booking: event_id is required")
	}

	key := fmt.Sprintf("quota:%s:%s", req.EventID.String(), req.ZoneName)

	// Atomic check + DECRBY: prevents overselling under concurrent checkout.
	res, err := s.rdb.Eval(ctx, decrQuotaLua, []string{key}, req.Quantity).Int64()
	if err != nil {
		return nil, fmt.Errorf("booking: decr zone quota: %w", err)
	}
	switch res {
	case -2:
		return nil, ErrQuotaNotInitialized
	case -1:
		return nil, ErrInsufficientQuota
	}

	rollbackQuota := func() {
		_, _ = s.rdb.IncrBy(context.WithoutCancel(ctx), key, int64(req.Quantity)).Result()
	}

	// Insert booking BEFORE publishing so the worker can satisfy
	// tickets.booking_id -> bookings.id and tickets.section_id -> sections.id.
	// section_id and total_amount are derived atomically from sections.
	bookingID := uuid.New()
	var sectionID uuid.UUID
	err = s.db.QueryRow(ctx, `
		INSERT INTO bookings (id, event_id, section_id, leader_user_id,
			seat_positions, total_seats, total_amount, payment_mode,
			payment_status, expires_at)
		SELECT $1, $2, s.id, $3, '[]'::jsonb, $4::int, s.price * $4::numeric,
			'BULK', 'PAID', NOW() + INTERVAL '10 minutes'
		FROM sections s
		WHERE s.event_id = $2 AND s.name = $5
		ORDER BY s.layout_version DESC
		LIMIT 1
		RETURNING section_id`,
		bookingID, req.EventID, req.UserID, req.Quantity, req.ZoneName,
	).Scan(&sectionID)
	if err != nil {
		rollbackQuota()
		return nil, fmt.Errorf("booking: create zoned booking: %w", err)
	}

	rollback := func() {
		rollbackQuota()
		_, _ = s.db.Exec(context.WithoutCancel(ctx), `DELETE FROM bookings WHERE id = $1`, bookingID)
	}

	if s.conn != nil {
		ch, chErr := s.conn.Channel()
		if chErr != nil {
			rollback()
			return nil, fmt.Errorf("booking: open zoned channel: %w", chErr)
		}
		defer func() { _ = ch.Close() }()

		if _, qErr := ch.QueueDeclare("zoned_ticket_generation", true, false, false, false, nil); qErr != nil {
			rollback()
			return nil, fmt.Errorf("booking: declare zoned queue: %w", qErr)
		}
		body, mErr := json.Marshal(zonedBookingMessage{
			BookingID: bookingID.String(),
			SectionID: sectionID.String(),
			EventID:   req.EventID.String(),
			ZoneName:  req.ZoneName,
			Quantity:  req.Quantity,
			UserID:    req.UserID.String(),
			CreatedAt: time.Now().UTC(),
		})
		if mErr != nil {
			rollback()
			return nil, fmt.Errorf("booking: marshal zoned booking: %w", mErr)
		}
		if pErr := ch.PublishWithContext(ctx, "", "zoned_ticket_generation", false, false, amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		}); pErr != nil {
			rollback()
			return nil, fmt.Errorf("booking: publish zoned booking: %w", pErr)
		}
	}

	return &ZonedBookingResponse{
		EventID:  req.EventID.String(),
		ZoneName: req.ZoneName,
		Quantity: req.Quantity,
		UserID:   req.UserID.String(),
		Status:   "success",
	}, nil
}
