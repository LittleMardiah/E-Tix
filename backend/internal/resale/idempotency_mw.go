package resale

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// TD-F7-02: X-Idempotency-Key middleware for POST mutations.
// Replays return cached 2xx body. Reuses same key = idempotent response.
// Design: Redis-only cache with 24h TTL (aligned with API_CONTRACT §4.1).
// Storage pattern: resale:idem:{key} → HTTP status + body (JSON).
func IdempotencyMiddleware(rdb *redis.Client, ttl time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-Idempotency-Key")
		if key == "" || rdb == nil {
			c.Next()
			return
		}
		cacheKey := "resale:idem:" + key

		if cached, err := rdb.Get(c.Request.Context(), cacheKey).Result(); err == nil {
			c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(cached))
			c.Abort()
			return
		}

		// Capture response body
		w := &responseCapture{ResponseWriter: c.Writer, buf: make([]byte, 0, 1024)}
		c.Writer = w
		c.Next()

		// Cache only successful mutations (2xx)
		if w.status >= 200 && w.status < 300 && len(w.buf) > 0 {
			_ = rdb.Set(c.Request.Context(), cacheKey, string(w.buf), ttl).Err()
		}
	}
}

type responseCapture struct {
	gin.ResponseWriter
	buf    []byte
	status int
}

func (r *responseCapture) Write(b []byte) (int, error) {
	r.buf = append(r.buf, b...)
	return r.ResponseWriter.Write(b)
}

func (r *responseCapture) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
