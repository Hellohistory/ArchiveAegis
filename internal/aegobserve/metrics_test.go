// file: internal/aegobserve/metrics_test.go

package aegobserve

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type regSwap struct {
	oldReg prometheus.Registerer
	oldGat prometheus.Gatherer
}

func swapDefaultRegistry() (*prometheus.Registry, func()) {
	newReg := prometheus.NewRegistry()
	backup := regSwap{
		oldReg: prometheus.DefaultRegisterer,
		oldGat: prometheus.DefaultGatherer,
	}
	prometheus.DefaultRegisterer = newReg
	prometheus.DefaultGatherer = newReg
	return newReg, func() {
		prometheus.DefaultRegisterer = backup.oldReg
		prometheus.DefaultGatherer = backup.oldGat
	}
}

func TestRegister_IsolatedRegistry(t *testing.T) {
	reg, restore := swapDefaultRegistry()
	defer restore()

	Register()

	// 写入一次样本，确保 HistogramVec 生成子指标
	httpRequestDuration.WithLabelValues("dummy", "GET", "200").Observe(0)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Registry.Gather() 失败: %v", err)
	}

	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "archiveaegis_http_request_duration_seconds" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("自定义 Histogram 未注册到 Registry 中")
	}
}

func TestHandler_MetricsEndpoint(t *testing.T) {
	_, restore := swapDefaultRegistry()
	defer restore()

	Register()
	httpRequestDuration.WithLabelValues("/", "GET", "200").Observe(0) // 注入样本

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()

	Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("/metrics HTTP 状态码错误, got=%d", w.Code)
	}
	bodyBytes, _ := io.ReadAll(w.Body)

	if !bytes.Contains(bodyBytes, []byte("archiveaegis_http_request_duration_seconds")) {
		t.Errorf("/metrics 输出缺少直方图, body=\n%s", bodyBytes)
	}
}

func TestPrometheusMiddleware_RecordOnce(t *testing.T) {
	reg, restore := swapDefaultRegistry()
	defer restore()

	gin.SetMode(gin.TestMode)
	Register() // Histogram 注册

	r := gin.New()
	r.Use(PrometheusMiddleware())
	r.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})

	// 触发一次请求
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != "pong" {
		t.Fatalf("Gin 处理 /ping 失败, code=%d, body=%s", w.Code, w.Body.String())
	}

	// 收集最新指标快照
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Registry.Gather() 失败: %v", err)
	}

	// 核心断言：Histogram 中存在 path="/ping", method="GET", code="200" 的计数
	var matched bool
	for _, mf := range mfs {
		if mf.GetName() != "archiveaegis_http_request_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsMatch(m, map[string]string{
				"path":   "/ping",
				"method": "GET",
				"code":   "200",
			}) && m.GetHistogram().GetSampleCount() == 1 {
				matched = true
				break
			}
		}
	}
	if !matched {
		t.Errorf("Histogram 未记录 /ping 请求 (path=/ping, method=GET, code=200, count=1)")
	}
}

// labelsMatch 比较 Metric 的 labelset 是否包含指定键值集合
func labelsMatch(m *dto.Metric, want map[string]string) bool {
	got := make(map[string]string, len(m.GetLabel()))
	for _, l := range m.GetLabel() {
		got[l.GetName()] = l.GetValue()
	}
	return reflect.DeepEqual(got, want)
}
