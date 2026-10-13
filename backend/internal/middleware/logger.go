package middleware

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var sensitiveHeaders = []string{
	"Authorization",
	"X-Admin-2FA-Token",
	"X-Queue-Token",
}

func isSensitiveHeader(key string) bool {
	for _, h := range sensitiveHeaders {
		if strings.EqualFold(key, h) {
			return true
		}
	}
	return false
}

func SanitizeHeaders(headers map[string][]string) map[string]string {
	sanitized := make(map[string]string, len(headers))
	for key, values := range headers {
		value := strings.Join(values, ", ")
		if isSensitiveHeader(key) {
			value = "[REDACTED]"
		}
		sanitized[key] = value
	}
	return sanitized
}

func LoggerMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		latency := time.Since(start)

		logger.Info("http request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Int64("latency_ms", latency.Milliseconds()),
			zap.String("client_ip", c.ClientIP()),
			zap.Any("sanitized_headers", SanitizeHeaders(c.Request.Header)),
		)
	}
}
