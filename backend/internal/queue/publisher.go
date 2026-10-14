package queue

import (
	"context"
	"errors"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Connection struct {
	*amqp.Connection
}

func NewConnection(url string) (*Connection, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	return &Connection{Connection: conn}, nil
}

func (c *Connection) Health(ctx context.Context) error {
	if c.Connection == nil || c.IsClosed() {
		return errors.New("rabbitmq connection is closed")
	}
	return nil
}

func (c *Connection) DeclareQueue(name string, durable bool) error {
	ch, err := c.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()

	_, err = ch.QueueDeclare(name, durable, false, false, false, nil)
	return err
}

func (c *Connection) Publish(ctx context.Context, queueName string, body []byte) error {
	ch, err := c.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()

	return ch.PublishWithContext(ctx, "", queueName, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        body,
	})
}

func (c *Connection) PublishToQueue(ctx context.Context, queueName string, payload []byte) error {
	ch, err := c.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()

	return ch.PublishWithContext(ctx, "", queueName, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        payload,
	})
}
