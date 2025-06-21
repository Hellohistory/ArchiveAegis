// file: internal/core/domain/plugin_models.go
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
	SupportedBizNames []string        `json:"supported_biz_names"`
	Versions          []PluginVersion `json:"versions"`
}

// PluginVersion 代表插件的一个特定版本
type PluginVersion struct {
	VersionString     string    `json:"version_string"`
	ReleaseDate       time.Time `json:"release_date"`
	Changelog         string    `json:"changelog"`
	MinGatewayVersion string    `json:"min_gateway_version"`
	Source            Source    `json:"source"`
	Execution         Execution `json:"execution"`
}

// Source 定义了如何获取插件的二进制文件
type Source struct {
	URL      string `json:"url"`
	Checksum string `json:"checksum"`
}

// Execution 定义了如何运行插件
type Execution struct {
	Entrypoint string   `json:"entrypoint"`
	Args       []string `json:"args"`
}

// PluginInstance 代表一个已配置的、可运行的插件实例。
// 将一个“已安装插件”转化为一个具体“服务”的配置实体。
type PluginInstance struct {
	InstanceID    string       `json:"instance_id"`
	DisplayName   string       `json:"display_name"`
	PluginID      string       `json:"plugin_id"`
	Version       string       `json:"version"`
	BizName       string       `json:"biz_name"`
	Port          int          `json:"port"`
	Status        string       `json:"status"`
	Enabled       bool         `json:"enabled"`
	CreatedAt     time.Time    `json:"created_at"`
	LastStartedAt sql.NullTime `json:"last_started_at"`
}
