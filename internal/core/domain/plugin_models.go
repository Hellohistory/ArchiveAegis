// Package domain file: internal/core/domain/plugin_models.go
package domain

import "time"

// Repository 代表一个插件仓库的元数据
type Repository struct {
	Name        string           `json:"repository_name"`
	Owner       string           `json:"owner"`
	LastUpdated time.Time        `json:"last_updated"`
	Plugins     []PluginManifest `json:"plugins"`
}

// PluginManifest 代表单个插件的完整描述信息
type PluginManifest struct {
	ID          string          `json:"id"`          // 插件的全局唯一ID, e.g., "io.archiveaegis.sqlite"
	Name        string          `json:"name"`        // 人类可读的名称
	Description string          `json:"description"` // 简短描述
	Author      string          `json:"author"`      // 作者
	Tags        []string        `json:"tags"`        // 标签，用于分类和搜索
	Versions    []PluginVersion `json:"versions"`    // 该插件的所有可用版本
}

// PluginVersion 代表插件的一个特定版本
type PluginVersion struct {
	VersionString     string    `json:"version_string"`      // 版本号, e.g., "1.0.1"
	ReleaseDate       time.Time `json:"release_date"`        // 发布日期
	Changelog         string    `json:"changelog"`           // 更新日志
	MinGatewayVersion string    `json:"min_gateway_version"` // 要求的最低网关版本
	Source            Source    `json:"source"`              // 下载源信息
	Execution         Execution `json:"execution"`           // 执行信息
}

// Source 定义了如何获取插件的二进制文件
type Source struct {
	URL      string `json:"url"`      // 下载地址
	Checksum string `json:"checksum"` // 文件校验和 (e.g., "sha256:f2ca...")
}

// Execution 定义了如何运行插件
type Execution struct {
	Entrypoint string `json:"entrypoint"` // 可执行文件的相对路径
	Args       string `json:"args"`       // 启动参数模板
}
