// Package aegobserve 暴露 Prometheus 指标
package aegobserve

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// 指标定义
var (
	TotalReq = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "archiveaegis_requests_total",
		Help: "请求总数",
	})
	FailReq = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "archiveaegis_requests_failed",
		Help: "请求失败数",
	})
)

// Register 必须在 main 调用一次
func Register() {
	prometheus.MustRegister(TotalReq, FailReq)
}

// Handler 返回 HTTP 处理器
func Handler() http.Handler { return promhttp.Handler() }
