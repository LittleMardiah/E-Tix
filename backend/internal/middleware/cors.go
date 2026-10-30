package middleware

import (
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func CORSMiddleware(origins []string, logger *zap.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = zap.NewNop()
	}
	// no origins configured; CORS disabled — return no-op middleware instead of
	// panicking. gin-contrib/cors panics with "all origins disabled" when
	// AllowOrigins is empty, so we guard against it here (defensive).
	if len(origins) == 0 {
		logger.Warn("no origins configured; CORS disabled")
		return func(c *gin.Context) {
			c.Next()
		}
	}

	// M1 FIX: validate each origin. gin-contrib/cors Validate panics on any
	// origin lacking http(s):// scheme. Reject cleanly instead of crashing.
	for _, o := range origins {
		if o == "*" {
			logger.Warn("CORS: wildcard origin with AllowCredentials=true will be rejected by browsers")
			continue
		}
		if !strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://") {
			logger.Error("CORS: invalid origin (missing http(s):// scheme); CORS disabled",
				zap.String("origin", o))
			return func(c *gin.Context) { c.Next() }
		}
	}

	cfg := cors.Config{
		AllowOrigins: origins,
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{
			"Authorization",
			"Content-Type",
			"X-Idempotency-Key",
			"ngrok-skip-browser-warning",
			"X-Queue-Token",
			"ngrok-skip-browser-warning",
			"X-Admin-2FA-Token",
			"X-Request-ID",
		},
		ExposeHeaders: []string{
			"X-RateLimit-Limit",
			"X-RateLimit-Remaining",
			"X-RateLimit-Reset",
		},
		AllowCredentials: true,
		MaxAge:           3600 * time.Second,
	}

	return cors.New(cfg)
}
