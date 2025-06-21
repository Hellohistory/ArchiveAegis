// Package domain file: internal/core/domain/plugin_models.go
package domain

import (
	"database/sql"
	"time"
)

// Repository 代表一个插件仓库的元数据
type Repository struct {
	Name        string           `json:"repository_name"`
	Owner       string           `json:"owner"`
	LastUpdated time.Time        `json:"last_updated"`
	Plugins     []PluginManifest `json:"plugins"`
}

// PluginManifest 代表单个插件的完整描述信息
type PluginManifest struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Author            string          `json:"author"`
	Tags              []string        `json:"tags"`
	SupportedBizNames []string        `json:"supported_biz_names"` // ✅ FIX: 在这里添加支持的业务组列表
	Versions          []PluginVersion `json:"versions"`
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

// InstalledPlugin 代表一个在本地已安装的插件及其当前状态。
type InstalledPlugin struct {
	PluginID         string       `json:"plugin_id"`
	InstalledVersion string       `json:"installed_version"`
	InstallPath      string       `json:"install_path"`
	Status           string       `json:"status"` // e.g., "STOPPED", "RUNNING", "ERROR"
	InstalledAt      time.Time    `json:"installed_at"`
	LastStartedAt    sql.NullTime `json:"last_started_at"`
}
