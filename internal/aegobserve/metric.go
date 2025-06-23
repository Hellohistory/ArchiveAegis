// Package aegobserve file: internal/aegobserve/metrics.go
package aegobserve

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var Registry = prometheus.NewRegistry()

// 指标定义
var (
	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "archiveaegis_http_request_duration_seconds",
		Help:    "HTTP请求的延迟（秒）",
		Buckets: prometheus.DefBuckets,
	}, []string{"path", "method", "code"})
)

func init() {
	Registry.MustRegister(httpRequestDuration)
	Registry.MustRegister(collectors.NewGoCollector())
	Registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{})
}

func PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start).Seconds()
		statusCode := strconv.Itoa(c.Writer.Status())
		path := c.FullPath()
		if path == "" {
			path = "not_found"
		}
		httpRequestDuration.WithLabelValues(path, c.Request.Method, statusCode).Observe(duration)
	}
}
