// Package aegconf 负责集中式配置加载

package aegconf

import (
	"fmt"
	"os"
	"strconv"
)

// Config 结构体
type Config struct {
	Port int // HTTP 监听端口
}

const defaultPort = 10224

// Load 从环境变量加载配置，返回合并结果
func Load() *Config {
	port := defaultPort
	if p := os.Getenv("AEGIS_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 && v < 65535 {
			port = v
		} else {
			fmt.Printf("⚠️  AEGIS_PORT 非法，回退 %d\n", defaultPort)
		}
	}
	return &Config{Port: port}
}
