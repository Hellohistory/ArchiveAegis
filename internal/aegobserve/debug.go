// Package aegobserve 提供系统可观测性相关功能
// 文件位置: internal/aegobserve/debug.go
package aegobserve

import (
	"log/slog"
	"net/http"
	_ "net/http/pprof"
)

// EnablePprof 在指定地址启动 HTTP 服务，提供 /debug/pprof 调试接口
func EnablePprof(addr string) {
	if addr == "" {
		slog.Info("pprof endpoint is disabled because address is empty")
		return
	}
	go func() {
		slog.Info("Starting pprof endpoint", "address", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			slog.Error("Failed to start pprof endpoint", "error", err)
		}
	}()
}
