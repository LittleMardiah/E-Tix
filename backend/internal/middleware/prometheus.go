package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// FIX P9a-8 TD-F0-01: Prometheus instrumentation.
var (
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "endpoint", "status"})

	httpTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests.",
	}, []string{"method", "endpoint", "status"})
)

// PrometheusMiddleware records request duration and total count per route.
// Unmatched routes are labelled "unmatched" so 404s do not pollute a real path.
func PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		endpoint := c.FullPath()
		if endpoint == "" {
			endpoint = "unmatched"
		}
		status := strconv.Itoa(c.Writer.Status())

		httpDuration.WithLabelValues(c.Request.Method, endpoint, status).
			Observe(time.Since(start).Seconds())
		httpTotal.WithLabelValues(c.Request.Method, endpoint, status).Inc()
	}
}
