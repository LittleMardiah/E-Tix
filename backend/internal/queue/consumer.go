package queue

import (
	amqp "github.com/rabbitmq/amqp091-go"
)

func (c *Connection) Consume(queueName string) (<-chan amqp.Delivery, error) {
	ch, err := c.Channel()
	if err != nil {
		return nil, err
	}

	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		_ = ch.Close()
		return nil, err
	}

	deliveries, err := ch.Consume(queueName, "", false, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return nil, err
	}

	return deliveries, nil
}

func (c *Connection) Ack(delivery amqp.Delivery) error {
	return delivery.Ack(false)
}

func (c *Connection) Nack(delivery amqp.Delivery, requeue bool) error {
	return delivery.Nack(false, requeue)
}
