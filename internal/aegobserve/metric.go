// Package aegobserve file: internal/aegobserve/metrics.go
package aegobserve

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors" // 1. 导入新的包
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// 指标定义
var (
	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "archiveaegis_http_request_duration_seconds",
		Help:    "HTTP请求的延迟（秒）",
		Buckets: prometheus.DefBuckets, // 使用默认的延迟分桶
	}, []string{"path", "method", "code"})
)

func Register() {
	prometheus.MustRegister(httpRequestDuration)
	prometheus.MustRegister(collectors.NewGoCollector())
	prometheus.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

// Handler 返回 HTTP 处理器
func Handler() http.Handler {
	return promhttp.Handler()
}

// PrometheusMiddleware 返回一个 Gin 中间件，用于记录每个请求的指标。
func PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// 先执行请求链中的其他部分
		c.Next()

		// 请求处理完毕后，记录指标
		duration := time.Since(start).Seconds()
		statusCode := strconv.Itoa(c.Writer.Status())
		path := c.FullPath() // 使用 Gin 的路由模板作为 path，避免路径参数导致标签爆炸
		if path == "" {
			path = "not_found"
		}

		// 记录到 Histogram
		httpRequestDuration.WithLabelValues(path, c.Request.Method, statusCode).Observe(duration)
	}
}
