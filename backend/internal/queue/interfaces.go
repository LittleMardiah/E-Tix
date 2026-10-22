package queue

import (
	"context"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Consumer abstracts the AMQP consumer surface used by the worker layer.
//
// FIX P9a-9c TD-F9-08: queue.Connection embeds *amqp.Connection directly, so
// it could not be substituted in unit tests. Every worker Run loop therefore
// had 0% coverage because exercising it needed a live RabbitMQ broker.
// *Connection stays the production implementation; tests inject a fake.
type Consumer interface {
	Consume(queueName string) (<-chan amqp.Delivery, error)
	Ack(delivery amqp.Delivery) error
	Nack(delivery amqp.Delivery, requeue bool) error
}

// DelayedPublisher abstracts the delayed-publish surface used by services that
// enqueue work with a delay (resale expiry scheduling).
type DelayedPublisher interface {
	PublishDelayed(ctx context.Context, queueName string, payload []byte, delay time.Duration) error
}

// Publisher abstracts the AMQP publish surface used by services that enqueue
// asynchronous work (admin BulkCancel, resale service, payment service).
type Publisher interface {
	PublishToQueue(ctx context.Context, queueName string, payload []byte) error
}

// Compile-time proof that the production connection satisfies both
// abstractions (R2: code is the source of truth).
var (
	_ Consumer         = (*Connection)(nil)
	_ Publisher        = (*Connection)(nil)
	_ DelayedPublisher = (*Connection)(nil)
)
