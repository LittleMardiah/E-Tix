package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

var ErrInsufficientQuota = errors.New("booking: insufficient zone quota")

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
	EventID   string    `json:"event_id"`
	ZoneName  string    `json:"zone_name"`
	Quantity  int       `json:"quantity"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

type ZonedService struct {
	rdb *redis.Client
	ch  *amqp.Channel
}

func NewZonedService(rdb *redis.Client, ch *amqp.Channel) *ZonedService {
	return &ZonedService{rdb: rdb, ch: ch}
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

	current, err := s.rdb.Get(ctx, key).Int()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			current = 0
		} else {
			return nil, fmt.Errorf("booking: read zone quota: %w", err)
		}
	}

	if int64(current) < int64(req.Quantity) {
		return nil, ErrInsufficientQuota
	}

	newVal, err := s.rdb.DecrBy(ctx, key, int64(req.Quantity)).Result()
	if err != nil {
		return nil, fmt.Errorf("booking: decr zone quota: %w", err)
	}
	if newVal < 0 {
		_, _ = s.rdb.IncrBy(ctx, key, int64(req.Quantity)).Result()
		return nil, ErrInsufficientQuota
	}

	if s.ch != nil {
		_, _ = s.ch.QueueDeclare("zoned_ticket_generation", true, false, false, false, nil)
		body, mErr := json.Marshal(zonedBookingMessage{
			EventID:   req.EventID.String(),
			ZoneName:  req.ZoneName,
			Quantity:  req.Quantity,
			UserID:    req.UserID.String(),
			CreatedAt: time.Now().UTC(),
		})
		if mErr != nil {
			_, _ = s.rdb.IncrBy(ctx, key, int64(req.Quantity)).Result()
			return nil, fmt.Errorf("booking: marshal zoned booking: %w", mErr)
		}
		if pErr := s.ch.PublishWithContext(ctx, "", "zoned_ticket_generation", false, false, amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		}); pErr != nil {
			_, _ = s.rdb.IncrBy(ctx, key, int64(req.Quantity)).Result()
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
