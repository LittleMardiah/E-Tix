package queue

import (
	"context"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	ExchangeDelayed     = "delayed_exchange"
	ExchangeTypeDelayed = "x-delayed-message"
)

var QueueNames = []string{
	"fan_score",
	"release_seat_check",
	"resale_expiry_check",
	"report_generation",
	"reconciliation_cycle",
}

func (c *Connection) DeclareDelayedExchange() error {
	ch, err := c.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()

	if err := ch.ExchangeDeclare(
		ExchangeDelayed,
		ExchangeTypeDelayed,
		true,
		false,
		false,
		false,
		amqp.Table{"x-delayed-type": "direct"},
	); err != nil {
		return err
	}

	for _, queueName := range QueueNames {
		if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
			return err
		}
		if err := ch.QueueBind(queueName, queueName, ExchangeDelayed, false, nil); err != nil {
			return err
		}
	}

	return nil
}

func (c *Connection) PublishDelayed(ctx context.Context, queueName string, payload []byte, delay time.Duration) error {
	ch, err := c.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()

	return ch.PublishWithContext(ctx, ExchangeDelayed, queueName, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        payload,
		Headers:     amqp.Table{"x-delay": int64(delay / time.Millisecond)},
	})
}

func (c *Connection) PublishFanScore(ctx context.Context, payload []byte) error {
	return c.PublishToQueue(ctx, "fan_score", payload)
}
