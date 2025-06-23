// Package aegobserve file: internal/aegobserve/debug.go
package aegobserve

import (
	"log/slog"
	"net/http"
	_ "net/http/pprof"
)

// EnablePprof 在指定地址上暴露 /debug/pprof 端点。
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
