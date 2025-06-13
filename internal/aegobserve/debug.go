// Package aegobserve 启用 pprof 调试
package aegobserve

import (
	"log"
	"net/http"
	_ "net/http/pprof" // 自动注册 pprof
)

// EnablePprof 在 :6060 暴露 /debug/pprof
func EnablePprof() {
	go func() {
		if err := http.ListenAndServe("0.0.0.0:6060", nil); err != nil {
			log.Printf("pprof 启动失败: %v", err)
		}
	}()
}
