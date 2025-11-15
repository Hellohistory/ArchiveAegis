// Package domain 提供插件相关核心模型定义
// 文件位置: internal/core/domain/plugin_models.go
package domain

import (
	"database/sql"
	"time"
)

// Repository 表示插件仓库的元数据
type Repository struct {
	Name        string           `json:"repository_name"` // 仓库名称
	Owner       string           `json:"owner"`           // 仓库所有者
	LastUpdated time.Time        `json:"last_updated"`    // 最后更新时间
	Plugins     []PluginManifest `json:"plugins"`         // 仓库包含的插件列表
}

// PluginManifest 表示单个插件的完整描述信息
type PluginManifest struct {
	ID                string          `json:"id"`                  // 插件唯一标识符
	Name              string          `json:"name"`                // 插件名称
	Description       string          `json:"description"`         // 插件描述信息
	Author            string          `json:"author"`              // 插件作者
	Tags              []string        `json:"tags"`                // 插件标签集合
	SupportedBizNames []string        `json:"supported_biz_names"` // 支持的业务组列表
	Versions          []PluginVersion `json:"versions"`            // 插件可用版本列表
}

// PluginVersion 表示插件的一个版本信息
type PluginVersion struct {
	VersionString     string    `json:"version_string"`      // 版本号字符串
	ReleaseDate       time.Time `json:"release_date"`        // 发布日期
	Changelog         string    `json:"changelog"`           // 版本更新日志
	MinGatewayVersion string    `json:"min_gateway_version"` // 最低兼容的网关版本
	Source            Source    `json:"source"`              // 插件源配置
	Execution         Execution `json:"execution"`           // 插件运行配置
}

// Source 表示插件二进制文件的获取方式
type Source struct {
	URL      string `json:"url"`      // 下载地址
	Checksum string `json:"checksum"` // 文件校验和
}

// Execution 表示插件运行时的配置
type Execution struct {
	Entrypoint string   `json:"entrypoint"` // 启动入口命令
	Args       []string `json:"args"`       // 启动参数
}

// PluginInstance 表示一个已配置的插件实例
type PluginInstance struct {
        InstanceID    string       `json:"instance_id"`     // 插件实例唯一标识符
        DisplayName   string       `json:"display_name"`    // 实例展示名称
        PluginID      string       `json:"plugin_id"`       // 所属插件标识符
        Version       string       `json:"version"`         // 使用的插件版本
        BizName       string       `json:"biz_name"`        // 所属业务组名称
        Port          int          `json:"port"`            // 插件监听端口
        Status        string       `json:"status"`          // 插件当前状态
        Enabled       bool         `json:"enabled"`         // 插件是否启用
        CreatedAt     time.Time    `json:"created_at"`      // 插件实例创建时间
        LastStartedAt sql.NullTime `json:"last_started_at"` // 插件最近启动时间
        RuntimePID          *int        `json:"runtime_pid,omitempty"`           // 当前运行的进程 PID
        HealthStatus        string      `json:"health_status,omitempty"`         // 运行时健康状态
        LastHeartbeat       *time.Time  `json:"last_heartbeat,omitempty"`        // 最近一次心跳时间
        FailureCount        *int        `json:"failure_count,omitempty"`         // 连续失败次数
        CircuitOpenUntil    *time.Time  `json:"circuit_open_until,omitempty"`    // 熔断恢复时间
        ProtocolVersion     string      `json:"protocol_version,omitempty"`     // 插件声明的协议版本
        CircuitBreakerOpen  bool        `json:"circuit_breaker_open,omitempty"` // 是否处于熔断
}
