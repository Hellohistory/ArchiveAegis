// Package go_plugin_sdk 提供插件接口定义及配置信息结构
// 文件位置: pkg/go_plugin_sdk/plugin.go
package go_plugin_sdk

import (
	"context"

	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
)

// Plugin 定义插件需实现的核心接口，用于处理请求、健康检查与资源清理
type Plugin interface {
	// Execute 统一处理插件请求，需根据负载类型执行对应逻辑
	Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error)

	// HealthCheck 执行插件的健康状态检查，返回 nil 表示健康
	HealthCheck(ctx context.Context) error

	// GracefulShutdown 执行插件关闭前的清理操作，用于资源释放
	GracefulShutdown(ctx context.Context) error
}

// PluginMeta 描述插件在运行时需要向宿主声明的协议能力信息。
type PluginMeta struct {
	SupportedProtocolVersion string // 插件支持的插件协议版本 (SemVer)
}

// PluginInfo 表示插件的元数据信息
type PluginInfo struct {
	Name                  string   // 插件名称
	Version               string   // 插件版本
	Type                  string   // 插件类型
	DescriptionMarkdown   string   // 插件说明（Markdown 格式）
	SupportedCapabilities []string // 插件支持的功能列表
	Meta                  PluginMeta
}

// PluginConfig 表示插件初始化时提供的配置项
type PluginConfig struct {
	Port        int    // 插件监听端口
	BizName     string // 业务组名称
	PluginName  string // 插件实例名称
	InstanceDir string // 插件实例存储目录
}

// Initializer 是插件初始化函数的类型定义
type Initializer func(ctx context.Context, cfg PluginConfig) (Plugin, error)
