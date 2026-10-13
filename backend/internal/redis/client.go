package redis

import (
	"context"
	"time"

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

func (c *Client) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return c.Client.SetNX(ctx, key, value, ttl).Result()
}

func (c *Client) GetString(ctx context.Context, key string) (string, error) {
	val, err := c.Client.Get(ctx, key).Result()
	if err == goredis.Nil {
		return "", goredis.Nil
	}
	return val, err
}

func (c *Client) IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	cnt, err := c.Client.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if cnt == 1 && ttl > 0 {
		if err := c.Client.Expire(ctx, key, ttl).Err(); err != nil {
			return cnt, err
		}
	}
	return cnt, nil
}

func (c *Client) EvalScript(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	return c.Client.Eval(ctx, script, keys, args...).Result()
}
