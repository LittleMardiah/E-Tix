package middleware

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func CORSMiddleware(origins []string, logger *zap.Logger) gin.HandlerFunc {
	// no origins configured; CORS disabled — return no-op middleware instead of
	// panicking. gin-contrib/cors panics with "all origins disabled" when
	// AllowOrigins is empty, so we guard against it here (defensive).
	if len(origins) == 0 {
		logger.Warn("no origins configured; CORS disabled")
		return func(c *gin.Context) {
			c.Next()
		}
	}

	cfg := cors.Config{
		AllowOrigins: origins,
		AllowMethods: []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{
			"Authorization",
			"Content-Type",
			"X-Idempotency-Key",
			"X-Queue-Token",
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
