// Package aegobserve file: internal/aegobserve/logging.go
package aegobserve

import (
	"log/slog"
	"os"
	"strings"
)

// InitLogger 初始化全局的结构化日志记录器。
// 它应该在 main 函数的早期被调用。
func InitLogger(levelStr string) {
	var level slog.Level

	// 根据配置字符串设置日志级别
	switch strings.ToUpper(levelStr) {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo // 默认为 INFO 级别
	}

	// 创建一个 JSON 格式的处理器，输出到标准输出
	// JSON 格式是生产环境的最佳实践
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     level,
		AddSource: true, // 添加代码源位置（文件:行号），方便调试
	})

	// 将我们创建的 logger 设置为全局默认 logger
	slog.SetDefault(slog.New(handler))
}
