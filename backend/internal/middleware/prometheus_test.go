package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// FIX P9a-8 TD-F0-01: Prometheus middleware coverage.

func TestPrometheusMiddleware_RecordsRegisteredRoute(t *testing.T) {
	r := gin.New()
	r.Use(PrometheusMiddleware())
	r.GET("/prom-test-ok", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	before := testutil.ToFloat64(httpTotal.WithLabelValues(http.MethodGet, "/prom-test-ok", "200"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/prom-test-ok", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	after := testutil.ToFloat64(httpTotal.WithLabelValues(http.MethodGet, "/prom-test-ok", "200"))
	assert.Equal(t, before+1, after, "request counter must increment")
}

func TestPrometheusMiddleware_RecordsStatusCode(t *testing.T) {
	r := gin.New()
	r.Use(PrometheusMiddleware())
	r.POST("/prom-test-teapot", func(c *gin.Context) {
		c.Status(http.StatusTeapot)
	})

	before := testutil.ToFloat64(httpTotal.WithLabelValues(http.MethodPost, "/prom-test-teapot", "418"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/prom-test-teapot", nil))

	assert.Equal(t, http.StatusTeapot, w.Code)
	after := testutil.ToFloat64(httpTotal.WithLabelValues(http.MethodPost, "/prom-test-teapot", "418"))
	assert.Equal(t, before+1, after)
}

func TestPrometheusMiddleware_UnmatchedRouteUsesFallbackLabel(t *testing.T) {
	r := gin.New()
	r.Use(PrometheusMiddleware())

	before := testutil.ToFloat64(httpTotal.WithLabelValues(http.MethodGet, "unmatched", "404"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/does-not-exist", nil))

	assert.Equal(t, http.StatusNotFound, w.Code)
	after := testutil.ToFloat64(httpTotal.WithLabelValues(http.MethodGet, "unmatched", "404"))
	assert.Equal(t, before+1, after, "unmatched routes must be labelled 'unmatched'")
}
