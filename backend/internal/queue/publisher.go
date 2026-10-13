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
