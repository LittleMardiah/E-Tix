package admin

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// F8-F05: X-Idempotency-Key middleware for admin POST/DELETE mutations.
// Mirrors resale.IdempotencyMiddleware (TD-F7-02) pattern: Redis-only cache,
// 24h TTL (API_CONTRACT §4.1). Replays return cached 2xx body.
// Storage pattern: admin:idem:{key} -> HTTP body (JSON).
func IdempotencyMiddleware(rdb *redis.Client, ttl time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-Idempotency-Key")
		if key == "" || rdb == nil {
			c.Next()
			return
		}
		cacheKey := "admin:idem:" + key

		// TD-F5-08 (accepted trade-off): the cached 2xx body is replayed
		// verbatim, so meta.request_id/timestamp are from the first request.
		// Identical replay is the idempotency contract; regenerating meta is
		// intentionally out of scope.
		if cached, err := rdb.Get(c.Request.Context(), cacheKey).Result(); err == nil {
			c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(cached))
			c.Abort()
			return
		}

		w := &adminResponseCapture{ResponseWriter: c.Writer, buf: make([]byte, 0, 1024)}
		c.Writer = w
		c.Next()

		if w.status >= 200 && w.status < 300 && len(w.buf) > 0 {
			_ = rdb.Set(c.Request.Context(), cacheKey, string(w.buf), ttl).Err()
		}
	}
}

type adminResponseCapture struct {
	gin.ResponseWriter
	buf    []byte
	status int
}

func (r *adminResponseCapture) Write(b []byte) (int, error) {
	r.buf = append(r.buf, b...)
	return r.ResponseWriter.Write(b)
}

func (r *adminResponseCapture) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
