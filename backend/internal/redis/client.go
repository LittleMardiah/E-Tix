package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
)

type Client struct {
	*goredis.Client
}

func NewClient(url string) (*Client, error) {
	opt, err := goredis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return &Client{Client: goredis.NewClient(opt)}, nil
}

func (c *Client) Health(ctx context.Context) error {
	return c.Ping(ctx).Err()
}
