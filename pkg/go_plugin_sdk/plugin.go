package go_plugin_sdk

import (
	"context"

	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
)

// Plugin 是插件开发者需要实现的业务逻辑核心接口。
type Plugin interface {
	// Execute 将统一处理请求，内部需要有逻辑来分发不同类型的负载。
	Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error)

	// HealthCheck 执行健康检查，成功返回 nil，失败返回 error。
	HealthCheck(ctx context.Context) error

	// GracefulShutdown 执行优雅关闭前的清理工作，比如关闭数据库连接、保存内存数据等。
	// SDK 会在收到 SIGTERM 或 SIGINT 信号后调用此方法。
	GracefulShutdown(ctx context.Context) error
}

// PluginInfo 包含了插件的元数据信息。
type PluginInfo struct {
	Name                  string
	Version               string
	Type                  string
	DescriptionMarkdown   string
	SupportedCapabilities []string
	// SupportedPayloads 将由 SDK 自动生成
}

// PluginConfig 是由 SDK 解析命令行参数后传递给插件初始化器的配置。
type PluginConfig struct {
	Port        int
	BizName     string
	PluginName  string
	InstanceDir string
}

// Initializer 是一个函数类型，插件开发者需要提供此类型的函数来创建和配置他们的 Plugin 实例。
type Initializer func(ctx context.Context, cfg PluginConfig) (Plugin, error)
