// Package plugin_manager 提供插件管理器的实现，包括插件目录、安装和生命周期管理功能
// 文件位置: internal/service/plugin_manager/plugin_manager.go
package plugin_manager

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/downloader"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

// PluginManager 管理插件的目录、安装和运行时状态
type PluginManager struct {
	db               *sql.DB                          // 插件管理器使用的数据库连接
	rootDir          string                           // 插件根目录路径
	installDir       string                           // 插件安装目录路径
	repositories     []RepositoryConfig               // 插件仓库配置列表
	catalog          map[string]domain.PluginManifest // 插件目录缓存
	downloaders      []downloader.Downloader          // 插件资源下载器列表
	runningPlugins   map[string]*exec.Cmd             // 当前运行中的插件进程映射
	executorRegistry map[string]port.Executor         // 业务组到执行器的注册映射
	closableAdapters *[]io.Closer                     // 所有可关闭的资源适配器
	bizToInstanceID  map[string]string                // 业务组与插件实例ID映射关系

	// 并发控制锁
	catalogMu        sync.RWMutex // 插件目录锁
	runningPluginsMu sync.Mutex   // 插件进程锁
	registryMu       sync.RWMutex // 注册映射锁
}

// RepositoryConfig 表示插件仓库的配置项
type RepositoryConfig struct {
	Name    string `mapstructure:"name"`    // 仓库名称
	URL     string `mapstructure:"url"`     // 仓库地址
	Enabled bool   `mapstructure:"enabled"` // 是否启用
}

// NewPluginManager 创建并返回一个新的插件管理器实例
func NewPluginManager(db *sql.DB, rootDir string, repos []RepositoryConfig, installDir string, registry map[string]port.Executor, closers *[]io.Closer) (*PluginManager, error) {
	if db == nil {
		return nil, errors.New("PluginManager 需要一个有效的数据库连接")
	}
	if installDir == "" {
		return nil, fmt.Errorf("插件安装目录(installDir)不能为空")
	}
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return nil, fmt.Errorf("创建插件安装目录 '%s' 失败: %w", installDir, err)
	}

	supportedDownloaders := []downloader.Downloader{
		&downloader.HTTPDownloader{
			Client: &http.Client{Timeout: 60 * time.Second},
		},
		&downloader.FileDownloader{},
	}

	return &PluginManager{
		db:               db,
		rootDir:          rootDir,
		installDir:       installDir,
		repositories:     repos,
		catalog:          make(map[string]domain.PluginManifest),
		downloaders:      supportedDownloaders,
		runningPlugins:   make(map[string]*exec.Cmd),
		executorRegistry: registry,
		closableAdapters: closers,
		bizToInstanceID:  make(map[string]string),
	}, nil
}
